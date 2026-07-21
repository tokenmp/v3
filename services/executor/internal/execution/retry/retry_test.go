package retry

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func testPlan(t *testing.T) routing.Plan {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "retry-test",
		Models: map[string]adapter.ModelInput{
			"m": {ID: "m", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		},
		Providers: map[string]adapter.ProviderInput{
			"p": {ID: "p", Name: "p", BaseURL: "https://p.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"a": {ID: "a", Name: "a", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"}},
		},
		Routes: []adapter.RouteInput{
			{ID: "r", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "one", Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "one", CredentialRef: "vault://p/one", Enabled: true}, {ID: "two", CredentialRef: "vault://p/two", Enabled: true}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 1)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := resolver.Resolve(context.Background(), routing.Selector{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func retryPolicy(rule adapter.RetryRule) adapter.CompiledRetry {
	return adapter.CompiledRetry{MaxTotalAttempts: 3, MaxSameTargetAttempts: 2, MaxTotalDuration: time.Minute, Backoff: time.Second, Rules: []adapter.RetryRule{rule}}
}

func failure(status int, code, typ string) Failure {
	return Failure{Classified: sdk.NewClassifiedError(sdk.ErrUpstream, status, "safe", code, typ)}
}

func TestStateRulesAndPlanNext(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)}
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{ID: "all", HTTPStatuses: []int{503}, ErrorCodes: []string{"busy"}, ErrorTypes: []string{"temporary"}, Action: adapter.RetryNextCredential})
	state := NewState(plan, clock)
	first, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := state.RecordFailure(context.Background(), first, failure(503, "busy", "temporary"), policy)
	if err != nil || !decision.Retry() || decision.Candidate.Credential.ID != "two" || decision.Delay != time.Second || decision.RuleID != "all" {
		t.Fatalf("RecordFailure() = %+v, %v", decision, err)
	}
	clock.now = clock.now.Add(decision.Delay)
	second, err := state.BeginAttempt(context.Background(), decision.Candidate, policy)
	if err != nil {
		t.Fatalf("BeginAttempt(next) = %v", err)
	}
	if err := state.RecordSuccess(context.Background(), second); err != nil {
		t.Fatalf("RecordSuccess = %v", err)
	}
}

func TestRuleMatchingANDORWildcardAndStops(t *testing.T) {
	plan := testPlan(t)
	cases := []struct {
		name string
		rule adapter.RetryRule
		f    Failure
		want StopReason
	}{
		{"AND all dimensions", adapter.RetryRule{HTTPStatuses: []int{500, 503}, ErrorCodes: []string{"busy", "again"}, ErrorTypes: []string{"temporary"}, Action: adapter.RetryNextCredential}, failure(503, "again", "temporary"), StopNone},
		{"AND rejects one dimension", adapter.RetryRule{HTTPStatuses: []int{503}, ErrorCodes: []string{"busy"}, Action: adapter.RetryNextCredential}, failure(503, "other", "temporary"), StopNoMatch},
		{"empty dimensions wildcard catchall", adapter.RetryRule{ID: "catch", Action: adapter.RetryNextCredential}, failure(418, "x", "y"), StopNone},
		{"none stops", adapter.RetryRule{Action: adapter.RetryNone}, failure(503, "", ""), StopRetryNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := NewState(plan, &fakeClock{})
			policy := retryPolicy(tc.rule)
			a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
			if err != nil {
				t.Fatal(err)
			}
			got, err := state.RecordFailure(context.Background(), a, tc.f, policy)
			if err != nil || got.Stop != tc.want {
				t.Fatalf("stop = %q, err = %v; want %q", got.Stop, err, tc.want)
			}
		})
	}
}

func TestBudgetsZeroAllowInitialOnlyAndSameTarget(t *testing.T) {
	plan := testPlan(t)
	rule := adapter.RetryRule{Action: adapter.RetrySameCredential}
	for _, policy := range []adapter.CompiledRetry{
		{MaxTotalAttempts: 0, MaxSameTargetAttempts: 0, MaxTotalDuration: time.Minute, Rules: []adapter.RetryRule{rule}},
		{MaxTotalAttempts: 3, MaxSameTargetAttempts: 0, MaxTotalDuration: time.Minute, Rules: []adapter.RetryRule{rule}},
	} {
		state := NewState(plan, &fakeClock{})
		a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := state.RecordFailure(context.Background(), a, failure(503, "", ""), policy)
		if err != nil || (decision.Stop != StopMaxTotalAttempts && decision.Stop != StopMaxSameTargetAttempts) {
			t.Fatalf("zero budget decision = %+v, %v", decision, err)
		}
	}
}

func TestDelayUsesMaximumAndDecisionMustBeConsumed(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	state := NewState(plan, &fakeClock{})
	a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	negative := -time.Second
	got, err := state.RecordFailure(context.Background(), a, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &negative}, policy)
	if err != nil || got.Delay != time.Second {
		t.Fatalf("negative RetryAfter delay = %+v, %v", got, err)
	}
	if _, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy); !errors.Is(err, ErrCandidateNotAllowed) {
		t.Fatalf("candidate other than decision = %v", err)
	}
	if _, err := state.BeginAttempt(context.Background(), got.Candidate, policy); !errors.Is(err, ErrAttemptNotReady) {
		t.Fatalf("early decision candidate = %v", err)
	}
	state.clock.(*fakeClock).now = state.clock.Now().Add(got.Delay)
	if _, err := state.BeginAttempt(context.Background(), got.Candidate, policy); err != nil {
		t.Fatalf("decision candidate = %v", err)
	}

	state = NewState(plan, &fakeClock{})
	policy.Rules[0].Action = adapter.RetryNextProvider
	a, _ = state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	got, err = state.RecordFailure(context.Background(), a, failure(503, "", ""), policy)
	if err != nil || got.Stop != StopNoCandidate {
		t.Fatalf("exhausted Plan.Next = %+v, %v", got, err)
	}
}

func TestDurationDelayDeadlineAndContextGates(t *testing.T) {
	start := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.MaxTotalDuration = 2 * time.Second
	clock := &fakeClock{now: start}
	state := NewState(plan, clock)
	a, _ := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	retryAfter := 3 * time.Second
	got, err := state.RecordFailure(context.Background(), a, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &retryAfter}, policy)
	if err != nil || got.Stop != StopMaxTotalDuration {
		t.Fatalf("duration gate = %+v, %v", got, err)
	}

	clock.now = start
	state = NewState(plan, clock)
	policy = retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(time.Second))
	a, _ = state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	defer cancel()
	got, err = state.RecordFailure(ctx, a, failure(503, "", ""), policy)
	if err != nil || got.Stop != StopDeadline { // equal deadline blocks
		t.Fatalf("deadline gate = %+v, %v", got, err)
	}

	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := NewState(plan, clock).BeginAttempt(cancelled, plan.Candidates[0], policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled BeginAttempt error = %v", err)
	}
}

func TestTotalDurationStartsAtFirstFailureEvenAtTimeZero(t *testing.T) {
	plan := testPlan(t)
	clock := &fakeClock{} // time.Time{} must still be a valid first-failure instant.
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetrySameCredential})
	policy.MaxTotalDuration = time.Second
	policy.MaxSameTargetAttempts = 3
	policy.Backoff = 0
	state := NewState(plan, clock)
	a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := state.RecordFailure(context.Background(), a, failure(503, "", ""), policy)
	if err != nil || !decision.Retry() {
		t.Fatalf("first failure = %+v, %v", decision, err)
	}
	clock.now = clock.now.Add(decision.Delay)
	second, err := state.BeginAttempt(context.Background(), decision.Candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = time.Time{}.Add(time.Second)
	decision, err = state.RecordFailure(context.Background(), second, failure(503, "", ""), policy)
	if err != nil || decision.Stop != StopMaxTotalDuration {
		t.Fatalf("duration from zero epoch = %+v, %v", decision, err)
	}
}

func TestCommitCancelAndAttemptLifecycle(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	state := NewState(plan, &fakeClock{})
	a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginAttempt(context.Background(), plan.Candidates[1], policy); !errors.Is(err, ErrAttemptActive) {
		t.Fatalf("parallel BeginAttempt = %v", err)
	}
	if err := state.Commit(); err != nil || state.Commit() != nil {
		t.Fatalf("Commit is not idempotent: %v", err)
	}
	got, err := state.RecordFailure(context.Background(), a, failure(503, "", ""), policy)
	if err != nil || got.Stop != StopCommitted {
		t.Fatalf("committed failure = %+v, %v", got, err)
	}
	if err := state.RecordSuccess(context.Background(), a); !errors.Is(err, ErrAttemptComplete) {
		t.Fatalf("duplicate completion = %v", err)
	}
	if err := state.Cancel(); err != nil || state.Cancel() != nil {
		t.Fatalf("Cancel is not idempotent: %v", err)
	}
}

func TestAttemptOpaqueAndSafeDecision(t *testing.T) {
	if fields := reflect.TypeFor[Attempt]().NumField(); fields != 3 {
		t.Fatalf("Attempt exported surface unexpectedly changed: %d fields", fields)
	}
	plan := testPlan(t)
	state := NewState(plan, &fakeClock{})
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	a, _ := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	got, _ := state.RecordFailure(context.Background(), a, failure(503, "", ""), policy)
	if reflect.TypeFor[Decision]().Field(0).Name != "Candidate" || got.Candidate.Credential.ID == "" {
		t.Fatalf("unexpected decision: %+v", got)
	}
	if err := state.RecordSuccess(context.Background(), Attempt{}); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("foreign attempt error = %v", err)
	}
}

func TestStateRetryBlockers(t *testing.T) {
	plan := testPlan(t)
	base := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})

	t.Run("nil classified never matches catchall", func(t *testing.T) {
		state := NewState(plan, &fakeClock{})
		attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], base)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := state.RecordFailure(context.Background(), attempt, Failure{}, base)
		if err != nil || decision.Stop != StopUnclassified {
			t.Fatalf("nil classified decision = %#v, %v", decision, err)
		}
	})

	t.Run("route switch cannot widen frozen policy", func(t *testing.T) {
		state := NewState(plan, &fakeClock{})
		attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], base)
		if err != nil {
			t.Fatal(err)
		}
		widened := base
		widened.MaxTotalAttempts++
		if _, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), widened); !errors.Is(err, ErrPolicyMismatch) {
			t.Fatalf("widened route policy = %v", err)
		}
		if err := state.RecordSuccess(context.Background(), attempt); err != nil {
			t.Fatalf("policy mismatch must not complete active attempt: %v", err)
		}
	})

	t.Run("retry begin enforces not before and total duration", func(t *testing.T) {
		start := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
		clock := &fakeClock{now: start}
		policy := base
		policy.Backoff = time.Second
		policy.MaxTotalDuration = 2 * time.Second
		state := NewState(plan, clock)
		attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), policy)
		if err != nil || !decision.Retry() {
			t.Fatalf("retry decision = %#v, %v", decision, err)
		}
		if _, err := state.BeginAttempt(context.Background(), decision.Candidate, policy); !errors.Is(err, ErrAttemptNotReady) {
			t.Fatalf("early retry begin = %v", err)
		}
		clock.now = start.Add(policy.MaxTotalDuration)
		if _, err := state.BeginAttempt(context.Background(), decision.Candidate, policy); !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("late retry begin = %v", err)
		}
	})

	t.Run("terminal gates clear pending retry", func(t *testing.T) {
		for _, terminal := range []struct {
			name string
			set  func(*State) error
		}{
			{name: "commit", set: (*State).Commit},
			{name: "cancel", set: (*State).Cancel},
		} {
			t.Run(terminal.name, func(t *testing.T) {
				state := NewState(plan, &fakeClock{})
				attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], base)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), base); err != nil {
					t.Fatal(err)
				}
				if state.pending == nil {
					t.Fatal("retry decision did not set pending attempt")
				}
				if err := terminal.set(state); err != nil {
					t.Fatal(err)
				}
				if state.pending != nil {
					t.Fatal("terminal operation left pending attempt")
				}
			})
		}
	})

	t.Run("retry begin enforces fake clock deadline", func(t *testing.T) {
		clock := &fakeClock{now: time.Now().Add(time.Hour)}
		policy := base
		policy.Backoff = 0
		state := NewState(plan, clock)
		ctx, cancel := context.WithDeadline(context.Background(), clock.now.Add(time.Second))
		defer cancel()
		attempt, err := state.BeginAttempt(ctx, plan.Candidates[0], policy)
		if err != nil {
			t.Fatal(err)
		}
		decision, err := state.RecordFailure(ctx, attempt, failure(503, "", ""), policy)
		if err != nil || !decision.Retry() {
			t.Fatalf("retry decision = %#v, %v", decision, err)
		}
		clock.now = clock.now.Add(time.Second)
		if _, err := state.BeginAttempt(ctx, decision.Candidate, policy); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline retry begin = %v", err)
		}
	})
}

func TestRuleMatcherProperty(t *testing.T) {
	rule := adapter.RetryRule{HTTPStatuses: []int{429, 503}, ErrorCodes: []string{"busy", "rate"}, ErrorTypes: []string{"temporary", "limited"}}
	for _, status := range []int{0, 429, 500, 503} {
		for _, code := range []string{"", "busy", "rate", "other"} {
			for _, typ := range []string{"", "temporary", "limited", "other"} {
				got := matchesInt(rule.HTTPStatuses, status) && matchesString(rule.ErrorCodes, code) && matchesString(rule.ErrorTypes, typ)
				want := (status == 429 || status == 503) && (code == "busy" || code == "rate") && (typ == "temporary" || typ == "limited")
				if got != want {
					t.Fatalf("matcher(%d,%q,%q) = %t, want %t", status, code, typ, got, want)
				}
			}
		}
	}
}

func TestStateIsSerial(t *testing.T) {
	// State intentionally has no synchronization: callers serialize lifecycle
	// operations. This test supplies that serialization and is race-testable.
	plan := testPlan(t)
	state := NewState(plan, &fakeClock{})
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		mu.Lock()
		candidate := plan.Candidates[0]
		if i%2 == 1 {
			candidate = plan.Candidates[1]
		}
		a, err := state.BeginAttempt(context.Background(), candidate, policy)
		if err == nil {
			_ = state.RecordSuccess(context.Background(), a)
		}
		mu.Unlock()
	}
}
