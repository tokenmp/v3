package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestStateAttemptLimitsInvariant(t *testing.T) {
	plan := testPlan(t)
	rng := rand.New(rand.NewPCG(0x5eed, 0xc0ffee))

	for caseIndex := range 256 {
		maxTotal := rng.IntN(6)
		maxSame := rng.IntN(6)
		policy := retryPolicy(adapter.RetryRule{Action: adapter.RetrySameCredential})
		policy.MaxTotalAttempts = maxTotal
		policy.MaxSameTargetAttempts = maxSame
		policy.Backoff = 0
		state := NewState(plan, &fakeClock{})

		effectiveTotal, effectiveSame := maxTotal, maxSame
		if effectiveTotal == 0 {
			effectiveTotal = 1
		}
		if effectiveSame == 0 {
			effectiveSame = 1
		}
		candidate := plan.Candidates[0]
		for {
			attempt, err := state.BeginAttempt(context.Background(), candidate, policy)
			if err != nil {
				t.Fatalf("case %d BeginAttempt() error = %v", caseIndex, err)
			}
			if state.attempts > effectiveTotal {
				t.Fatalf("case %d started %d > total limit %d", caseIndex, state.attempts, effectiveTotal)
			}
			target := plan.Candidates[0].Target()
			if state.byTarget[target] > effectiveSame {
				t.Fatalf("case %d target attempts %d > same-target limit %d", caseIndex, state.byTarget[target], effectiveSame)
			}
			decision, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), policy)
			if err != nil {
				t.Fatalf("case %d RecordFailure() error = %v", caseIndex, err)
			}
			if !decision.Retry() {
				if decision.Stop != StopMaxTotalAttempts && decision.Stop != StopMaxSameTargetAttempts {
					t.Fatalf("case %d terminal decision = %#v", caseIndex, decision)
				}
				break
			}
			if decision.Candidate != plan.Candidates[0] {
				t.Fatalf("case %d same-credential candidate changed: %#v", caseIndex, decision.Candidate)
			}
			candidate = decision.Candidate
		}
	}
}

func TestStateTerminalStopIsMonotonic(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})

	for _, terminal := range []struct {
		name string
		set  func(*State) error
		stop StopReason
		err  error
	}{
		{name: "commit", set: (*State).Commit, stop: StopCommitted, err: ErrCommitted},
		{name: "cancel", set: (*State).Cancel, stop: StopCanceled, err: ErrCanceled},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			state := NewState(plan, &fakeClock{})
			attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := terminal.set(state); err != nil {
				t.Fatalf("terminal operation = %v", err)
			}
			if err := terminal.set(state); err != nil {
				t.Fatalf("terminal replay = %v", err)
			}
			decision, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), policy)
			if err != nil || decision.Stop != terminal.stop {
				t.Fatalf("RecordFailure after terminal = %#v, %v", decision, err)
			}
			if _, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy); !errors.Is(err, terminal.err) {
				t.Fatalf("BeginAttempt after terminal = %v, want %v", err, terminal.err)
			}
		})
	}

	state := NewState(plan, &fakeClock{})
	if err := state.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := state.Cancel(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy); !errors.Is(err, ErrCommitted) {
		t.Fatalf("commit must remain terminal after Cancel: %v", err)
	}
}

func TestStateAdvancementExcludesVisitedAndPinsPlan(t *testing.T) {
	plan := testPlan(t)
	original := plan.Candidates[0]
	other := plan.Candidates[1]
	state := NewState(plan, &fakeClock{})
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.MaxTotalAttempts = 3

	// NewState must pin the caller-visible initial candidates as well as Plan's
	// private resolver universe; later caller mutation cannot select a new one.
	plan.Candidates[0] = other
	attempt, err := state.BeginAttempt(context.Background(), original, policy)
	if err != nil {
		t.Fatalf("pinned initial candidate rejected: %v", err)
	}
	if _, err := state.BeginAttempt(context.Background(), other, policy); !errors.Is(err, ErrAttemptActive) {
		t.Fatalf("active attempt gate = %v", err)
	}
	first, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), policy)
	if err != nil || !first.Retry() || first.Candidate != other {
		t.Fatalf("first advancement = %#v, %v", first, err)
	}
	state.clock.(*fakeClock).now = state.clock.Now().Add(first.Delay)

	attempt, err = state.BeginAttempt(context.Background(), first.Candidate, policy)
	if err != nil {
		t.Fatalf("begin advanced candidate = %v", err)
	}
	second, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), policy)
	if err != nil || second.Stop != StopNoCandidate {
		t.Fatalf("visited target must not be revisited: %#v, %v", second, err)
	}
	if len(state.visited) != 2 {
		t.Fatalf("visited targets = %d, want 2", len(state.visited))
	}
}

func TestStateConcurrentInstancesAreIsolated(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.Backoff = 0

	const rounds = 128
	left, right := NewState(plan, &fakeClock{}), NewState(plan, &fakeClock{})
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	run := func(state *State) {
		defer wg.Done()
		for range rounds {
			attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
			if err != nil {
				errCh <- err
				return
			}
			decision, err := state.RecordFailure(context.Background(), attempt, failure(503, "", ""), policy)
			if err != nil || !decision.Retry() {
				errCh <- errors.New("isolated state did not produce retry")
				return
			}
			attempt, err = state.BeginAttempt(context.Background(), decision.Candidate, policy)
			if err != nil {
				errCh <- err
				return
			}
			if err := state.RecordSuccess(context.Background(), attempt); err != nil {
				errCh <- err
				return
			}
			// Each State is request-local. Start a fresh state for the next round.
			state = NewState(plan, &fakeClock{})
		}
	}
	wg.Add(2)
	go run(left)
	go run(right)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if left.attempts != 2 || right.attempts != 2 || len(left.visited) != 2 || len(right.visited) != 2 {
		t.Fatalf("initial states unexpectedly mutated across instances: left=%d/%d right=%d/%d", left.attempts, len(left.visited), right.attempts, len(right.visited))
	}
}

func TestStateRejectsForgedAndDuplicateAttempts(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	state, foreign := NewState(plan, &fakeClock{}), NewState(plan, &fakeClock{})
	attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordSuccess(context.Background(), Attempt{}); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("zero forged Attempt = %v", err)
	}
	if err := state.RecordSuccess(context.Background(), Attempt{state: foreign, id: attempt.id}); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("foreign forged Attempt = %v", err)
	}
	if err := state.RecordSuccess(context.Background(), Attempt{state: state, id: attempt.id + 1}); !errors.Is(err, ErrInvalidAttempt) {
		t.Fatalf("unknown forged Attempt = %v", err)
	}
	if err := state.RecordSuccess(context.Background(), attempt); err != nil {
		t.Fatalf("valid attempt = %v", err)
	}
	if err := state.RecordSuccess(context.Background(), attempt); !errors.Is(err, ErrAttemptComplete) {
		t.Fatalf("duplicate attempt = %v", err)
	}
}

func TestStateContextAndNegativeRetryAfterFailClosed(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.Backoff = -time.Second

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewState(plan, &fakeClock{}).BeginAttempt(cancelled, plan.Candidates[0], policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled BeginAttempt = %v", err)
	}
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelDeadline()
	if _, err := NewState(plan, &fakeClock{}).BeginAttempt(deadline, plan.Candidates[0], policy); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired BeginAttempt = %v", err)
	}

	state := NewState(plan, &fakeClock{})
	attempt, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	negative := -time.Hour
	decision, err := state.RecordFailure(context.Background(), attempt, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &negative}, policy)
	if err != nil || !decision.Retry() || decision.Delay != 0 {
		t.Fatalf("negative RetryAfter must not schedule a negative delay: %#v, %v", decision, err)
	}

	attempt, err = state.BeginAttempt(context.Background(), decision.Candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel = context.WithCancel(context.Background())
	cancel()
	decision, err = state.RecordFailure(cancelled, attempt, failure(503, "", ""), policy)
	if err != nil || decision.Stop != StopCanceled || decision.Retry() {
		t.Fatalf("cancelled RecordFailure = %#v, %v", decision, err)
	}
}
