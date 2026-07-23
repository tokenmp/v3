package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// testReservationID is a valid reservation identifier (res_ + 22 URL-safe
// chars) shared by runner fixtures. It mirrors the requestid grammar so the
// Runner's grammar gate admits it.
const testReservationID = "res_aaaaaaaaaaaaaaaaaaaa"

// runnerTestClient is a configurable fake sdk.Client. It records every Complete
// call and returns the configured completion or classified error per call index.
type runnerTestClient struct {
	mu           sync.Mutex
	calls        int32
	completeFn   func(ctx context.Context, call sdk.Call) (sdk.Completion, error)
	recordedCtx  []context.Context
	recordedCall []sdk.Call
}

var _ sdk.Client = (*runnerTestClient)(nil)

func (c *runnerTestClient) Complete(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
	atomic.AddInt32(&c.calls, 1)
	c.mu.Lock()
	c.recordedCtx = append(c.recordedCtx, ctx)
	c.recordedCall = append(c.recordedCall, call)
	fn := c.completeFn
	c.mu.Unlock()
	if fn != nil {
		return fn(ctx, call)
	}
	return sdk.Completion{RawJSON: json.RawMessage(`{"ok":true}`), Status: 200, RequestID: "req_ok"}, nil
}

func (c *runnerTestClient) callCount() int32 { return atomic.LoadInt32(&c.calls) }

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type recordingSleeper struct {
	mu     sync.Mutex
	delays []time.Duration
	ctxs   []context.Context
	clock  *fakeClock
}

func (s *recordingSleeper) Sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.ctxs = append(s.ctxs, ctx)
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.clock != nil {
		s.clock.now = s.clock.now.Add(d)
	}
	return nil
}

type staticCredentials struct{ value []byte }

func (s staticCredentials) Resolve(context.Context, string) (sdk.CredentialSecret, error) {
	return sdk.NewCredentialSecret(s.value), nil
}

// countingCredentials makes credential-resolution side effects observable at
// the Runner boundary. Runner is serial per request, but the mutex keeps this
// fake safe if a failing test exercises it from another goroutine.
type countingCredentials struct {
	mu    sync.Mutex
	calls int
	refs  []string
	value []byte
	err   error
}

func (c *countingCredentials) Resolve(_ context.Context, ref string) (sdk.CredentialSecret, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.refs = append(c.refs, ref)
	if c.err != nil {
		return sdk.CredentialSecret{}, c.err
	}
	return sdk.NewCredentialSecret(c.value), nil
}

func (c *countingCredentials) snapshot() (int, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, append([]string(nil), c.refs...)
}

// runnerFixture builds a compiled, frozen resolver and Plan with one OpenAI
// Chat route and two credentials, supporting RetryNextCredential.
func runnerFixture(t *testing.T) (*routing.Resolver, routing.Plan) {
	return runnerFixtureWithRule(t, adapter.RetryRule{ID: "next-cred", HTTPStatuses: []int{503}, Action: adapter.RetryNextCredential}, nil)
}

// runnerFixtureWithRule builds a compiled, frozen resolver and Plan with one
// OpenAI Chat route, two credentials, and a configurable route retry rule.
func runnerFixtureWithRule(t *testing.T, rule adapter.RetryRule, maxSame *int) (*routing.Resolver, routing.Plan) {
	return runnerFixtureWithRuleTimeout(t, rule, maxSame, "")
}

func runnerFixtureWithRuleTimeout(t *testing.T, rule adapter.RetryRule, maxSame *int, requestTimeout adapter.RawDuration) (*routing.Resolver, routing.Plan) {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "runner-revision",
		Models: map[string]adapter.ModelInput{
			"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		},
		Providers: map[string]adapter.ProviderInput{
			"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"adapter": {
				ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat,
				Auth:    adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
				Request: adapter.RequestPolicy{AllowedHeaders: []string{"X-Test"}},
				Response: adapter.ResponsePolicy{Rules: []adapter.ResponseRule{{
					ID: "resp-529-to-429", Priority: 1, Match: adapter.ResponseMatch{HTTPStatuses: []int{529}},
					Output: adapter.ResponseOutput{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limited", Message: "rate limited"},
				}}},
			},
		},
		Routes: []adapter.RouteInput{
			{
				ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "upstream",
				Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "group",
				Credentials: []adapter.CredentialInput{
					{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true},
					{ID: "cred-b", CredentialRef: "vault://private/cred-b", Priority: 2, Enabled: true},
				},
				Retry:   adapter.RetryPolicy{Rules: []adapter.RetryRule{rule}, MaxSameTargetAttempts: maxSame},
				Timeout: adapter.TimeoutPolicy{RequestTimeout: requestTimeout, TTFTTimeout: timeoutTTFT(requestTimeout)},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 7)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	resolver, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	plan, err := resolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolver, plan
}

func timeoutTTFT(request adapter.RawDuration) adapter.RawDuration {
	if request == "" {
		return ""
	}
	return "10ms"
}

func newRunner(t *testing.T, client sdk.Client, log requestlog.ExecutionPort) (*Runner, *quota.TypedMock, *SDKRegistry) {
	t.Helper()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	return &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}, quotaPort, registry
}

func runnerInput(resolver *routing.Resolver, plan routing.Plan) Input {
	return Input{
		RequestID:     "req-1",
		QuotaIdentity: QuotaIdentity{Subject: "subject", KeyID: "key-1", Protocol: "openai_chat"},
		ReservationID: testReservationID,
		Plan:          plan,
		Resolver:      resolver,
		Credentials:   staticCredentials{value: []byte("call-local-secret")},
		Body:          json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`),
	}
}

func TestRunnerSuccessPreflightReserveFinalizeAndLogsOnce(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.Status != 200 || result.Completion.RequestID != "req_ok" || len(result.Completion.RawJSON) == 0 {
		t.Fatalf("Completion = %+v", result.Completion)
	}

	// Preflight did not Reserve; the only Reserve is the single committed one.
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
	if client.callCount() != 1 {
		t.Fatalf("client Complete calls = %d, want 1", client.callCount())
	}
	events := log.Events(context.Background())
	// Lifecycle: reserved + attempt(success) + finalized
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Kind != requestlog.KindReserved {
		t.Fatalf("events[0].Kind = %q, want reserved", events[0].Kind)
	}
	if events[1].Kind != requestlog.KindAttempt || events[1].Status != "success" || events[1].Attempt != 1 {
		t.Fatalf("events[1] = %+v, want attempt success", events[1])
	}
	if events[2].Kind != requestlog.KindFinalized {
		t.Fatalf("events[2].Kind = %q, want finalized", events[2].Kind)
	}
	if events[1].Candidate.CredentialID != "cred-a" || events[1].Protocol != "openai_chat" || events[1].Revision != "runner-revision" || events[1].Generation != 7 {
		t.Fatalf("event metadata = %+v", events[1])
	}
}

func TestRunnerRevokesSavedSDKCallAfterComplete(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	if _, err := runner.Run(context.Background(), runnerInput(resolver, plan)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	client.mu.Lock()
	if len(client.recordedCall) != 1 {
		client.mu.Unlock()
		t.Fatalf("recorded calls = %d, want 1", len(client.recordedCall))
	}
	saved := client.recordedCall[0]
	client.mu.Unlock()
	if err := saved.Secret.Use(func([]byte) error { t.Fatal("saved SDK call secret remained usable"); return nil }); !errors.Is(err, sdk.ErrSecretUnavailable) {
		t.Fatalf("saved SDK call Secret.Use = %v, want sdk.ErrSecretUnavailable", err)
	}
}

func TestRunnerParentCancellationAfterCompleteWinsOverSuccess(t *testing.T) {
	// Once Complete returns, the caller's cancellation has precedence even if
	// the SDK returned a success concurrently. The reservation is released via
	// detached cleanup; no success result, finalization, or retry is allowed.
	port := quota.NewTypedMock()
	completeStarted := make(chan struct{})
	ctx, ctxCancel := context.WithCancel(context.Background())
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		close(completeStarted)
		ctxCancel()
		return sdk.Completion{RawJSON: json.RawMessage(`{"ok":true}`), Status: 200, RequestID: "req_ok"}, nil
	}}
	log := requestlog.NewInMemoryExecution()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: clock, Sleeper: &recordingSleeper{clock: clock}, CleanupTimeout: time.Second}
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(ctx, runnerInput(resolver, plan))
	select {
	case <-completeStarted:
	default:
		t.Fatal("Complete was not called")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if result.Completion.RawJSON != nil || result.Failure != nil {
		t.Fatalf("result = %+v, want empty result after caller cancellation", result)
	}
	if calls := port.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release only", calls)
	}
	events := log.Events(context.Background())
	// Lifecycle: reserved + attempt(failed) + released
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Kind != requestlog.KindReserved {
		t.Fatalf("events[0].Kind = %q, want reserved", events[0].Kind)
	}
	if events[1].Kind != requestlog.KindAttempt || events[1].Status != "failed" || events[1].RuleID != "" || events[1].Action != "" {
		t.Fatalf("events[1] = %+v, want safe cancellation failure attempt event", events[1])
	}
	if events[2].Kind != requestlog.KindReleased {
		t.Fatalf("events[2].Kind = %q, want released", events[2].Kind)
	}
}

func TestRunnerRejectsForeignResolverPlanBeforePreflightCredentialOrReserve(t *testing.T) {
	client := &runnerTestClient{}
	runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	resolver, _ := runnerFixture(t)
	_, foreignPlan := runnerFixture(t)
	credentials := &countingCredentials{value: []byte("must-not-resolve")}
	in := runnerInput(resolver, foreignPlan)
	in.Credentials = credentials

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, routing.ErrInvalidPlan) {
		t.Fatalf("Run error = %v, want routing.ErrInvalidPlan", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 || client.callCount() != 0 {
		t.Fatalf("foreign Plan used quota or SDK: quota=%+v sdk=%d", calls, client.callCount())
	}
	if calls, _ := credentials.snapshot(); calls != 0 {
		t.Fatalf("foreign Plan resolved credential %d times", calls)
	}
}

func TestRunnerPreflightFailureDoesNotReserve(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	// Forging a candidate whose provider selector disagrees with the frozen
	// config forces Prepare to fail closed during preflight.
	in := runnerInput(resolver, plan)
	in.Plan.Candidates[0].Provider.Selector = "forged"

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, routing.ErrInvalidCandidate) {
		t.Fatalf("Run error = %v, want routing.ErrInvalidCandidate", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 {
		t.Fatalf("preflight reserved quota: %+v", calls)
	}
	if client.callCount() != 0 {
		t.Fatalf("preflight called client %d times", client.callCount())
	}
	if events := log.Events(context.Background()); len(events) != 0 {
		t.Fatalf("preflight logged %d events", len(events))
	}
}

func TestRunnerRetriesOnClassifiedFailureThenSucceeds(t *testing.T) {
	var callCount int32
	client := &runnerTestClient{
		completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
			n := atomic.AddInt32(&callCount, 1)
			if n == 1 {
				return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "req_a", "", "")
			}
			return sdk.Completion{RawJSON: json.RawMessage(`{"ok":true}`), Status: 200, RequestID: "req_b"}, nil
		},
	}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.RequestID != "req_b" {
		t.Fatalf("Completion = %+v", result.Completion)
	}
	// Each attempt reran Prepare/Apply/Client lookup and used the next
	// credential (RetryNextCredential), so the second call used cred-b.
	client.mu.Lock()
	if len(client.recordedCall) != 2 {
		t.Fatalf("recorded calls = %d", len(client.recordedCall))
	}
	second := client.recordedCall[1]
	client.mu.Unlock()
	if second.Candidate.CredentialID != "cred-b" {
		t.Fatalf("retry did not advance credential: %+v", second.Candidate)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
	events := log.Events(context.Background())
	// Lifecycle: reserved + attempt(failed) + attempt(success) + finalized
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	if events[0].Kind != requestlog.KindReserved {
		t.Fatalf("events[0].Kind = %q, want reserved", events[0].Kind)
	}
	if events[1].Status != "failed" || events[1].Attempt != 1 || events[1].RuleID != "next-cred" || events[1].Action != "next_credential" {
		t.Fatalf("failed event = %+v", events[1])
	}
	if events[2].Status != "success" || events[2].Attempt != 2 {
		t.Fatalf("success event = %+v", events[2])
	}
	if events[3].Kind != requestlog.KindFinalized {
		t.Fatalf("events[3].Kind = %q, want finalized", events[3].Kind)
	}
}

func TestRunnerPerAttemptContextUsesPreparedRequestTimeout(t *testing.T) {
	started := make(chan struct{})
	client := &runnerTestClient{completeFn: func(ctx context.Context, _ sdk.Call) (sdk.Completion, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("Complete context has no request deadline")
		}
		close(started)
		<-ctx.Done()
		return sdk.Completion{}, ctx.Err()
	}}
	runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	resolver, plan := runnerFixtureWithRuleTimeout(t, adapter.RetryRule{ID: "stop-timeout", ErrorCodes: []string{"timeout"}, Action: adapter.RetryNone}, nil, "20ms")

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v, want nil after confirmed Release", err)
	}
	if result.Failure == nil || result.Failure.HTTPStatus != 504 || result.Failure.ErrorType != "upstream_timeout" {
		t.Fatalf("Failure = %#v, want mapped timeout", result.Failure)
	}
	select {
	case <-started:
	default:
		t.Fatal("Complete was not called")
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

func TestRunnerParentDeadlineWinsOverCatchAllRetryAndPreservesReleaseUncertainty(t *testing.T) {
	// A parent deadline can race with a provider's context-derived timeout. It
	// is not an upstream failure: even a catch-all retry policy must neither map
	// nor retry it, and Release uncertainty must remain joined to the caller's
	// context verdict.
	started := make(chan struct{})
	client := &runnerTestClient{completeFn: func(ctx context.Context, _ sdk.Call) (sdk.Completion, error) {
		close(started)
		<-ctx.Done()
		return sdk.Completion{}, ctx.Err()
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	releaseErr := errors.New("quota release private fault")
	quotaPort.SetReleaseReservationFn(func(context.Context, quota.ReleaseRequest) (quota.Reservation, error) {
		return quota.Reservation{}, releaseErr
	})
	resolver, plan := runnerFixtureWithRuleTimeout(t, adapter.RetryRule{
		ID: "catch-all", Action: adapter.RetryNextCredential,
	}, nil, "1s")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := runner.Run(ctx, runnerInput(resolver, plan))
	select {
	case <-started:
	default:
		t.Fatal("Complete was not called")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want context.DeadlineExceeded", err)
	}
	assertTerminalizationError(t, err, "release", releaseErr, testReservationID)
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("client Complete calls = %d, want 1; parent deadline retried", calls)
	}
	if events := log.Events(context.Background()); len(events) != 3 || events[0].Kind != requestlog.KindReserved || events[1].Kind != requestlog.KindAttempt || events[1].Status != "failed" || events[1].RuleID != "" || events[1].Action != "" || events[2].Kind != requestlog.KindReleased || events[2].Settlement.Reason != "unknown" {
		t.Fatalf("events = %+v, want reserved + safe unmapped parent-deadline failure + released(unknown)", events)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

func TestRunnerAttemptDeadlineRetriesByTimeoutErrorTypeThenSucceeds(t *testing.T) {
	// The parent remains live while the per-attempt deadline expires. This is a
	// classified SDK timeout, so an ErrorTypes timeout rule may start exactly
	// one second SDK call and return its success.
	var completeCalls int32
	client := &runnerTestClient{completeFn: func(ctx context.Context, _ sdk.Call) (sdk.Completion, error) {
		if atomic.AddInt32(&completeCalls, 1) == 1 {
			<-ctx.Done()
			return sdk.Completion{}, ctx.Err()
		}
		return sdk.Completion{RawJSON: json.RawMessage(`{"ok":true}`), Status: 200, RequestID: "retried"}, nil
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixtureWithRuleTimeout(t, adapter.RetryRule{
		ID: "timeout-type", ErrorTypes: []string{"timeout"}, Action: adapter.RetryNextCredential,
	}, nil, "20ms")

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.RequestID != "retried" {
		t.Fatalf("Completion = %+v, want second-attempt success", result.Completion)
	}
	if calls := client.callCount(); calls != 2 {
		t.Fatalf("client Complete calls = %d, want 2", calls)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Finalize", calls)
	}
	events := log.Events(context.Background())
	// Lifecycle: reserved + attempt(failed,timeout) + attempt(success) + finalized
	if len(events) != 4 || events[0].Kind != requestlog.KindReserved || events[1].Status != "failed" || events[1].RuleID != "timeout-type" || events[1].Action != "next_credential" || events[2].Status != "success" || events[3].Kind != requestlog.KindFinalized {
		t.Fatalf("events = %+v, want reserved + timeout retry + success + finalized", events)
	}
}

func TestRunnerFallbackUsesFrozenFirstRouteRetryPolicy(t *testing.T) {
	// The first route may explicitly move to fallback, but its retry policy is
	// request-lifetime state. The fallback route's divergent policy must not
	// replace it: after fallback's 503, the frozen NextRoute policy has no next
	// route and stops instead of retrying fallback with its SameCredential rule.
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision:  "frozen-retry-policy",
		Models:    map[string]adapter.ModelInput{"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}}},
		Providers: map[string]adapter.ProviderInput{"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat}},
		Adapters:  map[string]adapter.AdapterConfig{"adapter": {ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"}}},
		Routes: []adapter.RouteInput{
			{ID: "primary", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "primary", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "primary-credential", CredentialRef: "vault://private/primary", Enabled: true}}, FallbackRouteIDs: []string{"fallback"}, Retry: adapter.RetryPolicy{Rules: []adapter.RetryRule{{ID: "primary-next-route", HTTPStatuses: []int{503}, Action: adapter.RetryNextRoute}}}},
			{ID: "fallback", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "fallback", Priority: 2, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "fallback-credential", CredentialRef: "vault://private/fallback", Enabled: true}}, Retry: adapter.RetryPolicy{Rules: []adapter.RetryRule{{ID: "fallback-same-credential", HTTPStatuses: []int{503}, Action: adapter.RetrySameCredential}}, MaxTotalAttempts: intPtr(3)}},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 11)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	resolver, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	plan, err := resolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	runner, _, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil || result.Failure == nil {
		t.Fatalf("Run result/error = %+v/%v, want confirmed mapped failure/nil", result, err)
	}
	if client.callCount() != 2 {
		t.Fatalf("SDK calls = %d, want primary plus one actual fallback; fallback policy must not take over", client.callCount())
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.recordedCall) != 2 || client.recordedCall[0].Candidate.RouteID != "primary" || client.recordedCall[1].Candidate.RouteID != "fallback" {
		t.Fatalf("actual fallback calls = %+v", client.recordedCall)
	}
}

func intPtr(v int) *int { return &v }

func TestRunnerAllAttemptsFailReleasesAfterReserve(t *testing.T) {
	client := &runnerTestClient{
		completeFn: func(_ context.Context, _ sdk.Call) (sdk.Completion, error) {
			return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "req_x", "", "")
		},
	}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil || result.Failure == nil {
		t.Fatalf("Run result/error = %+v/%v, want confirmed mapped failure/nil", result, err)
	}
	// Two credentials, each retried up to MaxSameTargetAttempts. The compiled
	// default permits MaxTotalAttempts=3. The runner attempts cred-a twice
	// (StopMaxSameTargetAttempts) and cred-b once (StopMaxTotalAttempts), but
	// stops the request as a failure. Verify Release happened exactly once.
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
	events := log.Events(context.Background())
	for _, e := range events {
		if e.Kind == requestlog.KindAttempt && e.Status != "failed" {
			t.Fatalf("unexpected non-failure attempt event: %+v", e)
		}
	}
	if client.callCount() < 2 {
		t.Fatalf("client calls = %d, want >=2", client.callCount())
	}
}

func TestRunnerUnclassifiedFailureIsFailClosedAndReleases(t *testing.T) {
	// The client returns a raw, unclassified error that mentions secret
	// material the Runner must not surface. The Runner fails closed: it
	// cancels retries, releases, and returns ErrUnclassified.
	leakingErr := fmt.Errorf("upstream said: vault://private/secret-leak body=%s", "raw-request-body")
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, leakingErr
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	_, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, ErrUnclassified) {
		t.Fatalf("Run error = %v, want ErrUnclassified", err)
	}
	if strings.Contains(err.Error(), "secret-leak") || strings.Contains(err.Error(), "raw-request-body") {
		t.Fatalf("unclassified error leaked upstream material: %v", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
	if client.callCount() != 1 {
		t.Fatalf("unclassified path retried: %d", client.callCount())
	}
}

func TestRunnerContextCancellationDuringCompleteReleasesUnderCleanupTimeout(t *testing.T) {
	started := make(chan struct{})
	cancel := make(chan struct{})
	client := &runnerTestClient{completeFn: func(ctx context.Context, _ sdk.Call) (sdk.Completion, error) {
		close(started)
		select {
		case <-ctx.Done():
			return sdk.Completion{}, ctx.Err()
		case <-cancel:
			return sdk.Completion{}, context.Canceled
		}
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	ctx, ctxCancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, runnerInput(resolver, plan)); runDone <- err }()
	<-started
	ctxCancel()

	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestRunnerCleanupContextOutlivesRequestCancellation(t *testing.T) {
	// The Runner's Release must run under a cleanup context detached from
	// request cancellation and bounded by CleanupTimeout, even when the
	// request context is canceled mid-Complete.
	completeStarted := make(chan struct{})
	startedRelease := make(chan struct{})
	finish := make(chan struct{})
	port := quota.NewTypedMock()
	port.SetReleaseReservationFn(func(ctx context.Context, in quota.ReleaseRequest) (quota.Reservation, error) {
		if err := ctx.Err(); err != nil {
			t.Errorf("cleanup context was canceled: %v", err)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("cleanup context has no deadline")
		}
		close(startedRelease)
		<-finish
		return quota.Reservation{}, nil
	})
	client := &runnerTestClient{completeFn: func(ctx context.Context, _ sdk.Call) (sdk.Completion, error) {
		close(completeStarted)
		<-ctx.Done()
		return sdk.Completion{}, ctx.Err()
	}}
	log := requestlog.NewInMemoryExecution()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: clock, Sleeper: &recordingSleeper{clock: clock}, CleanupTimeout: time.Second}
	resolver, plan := runnerFixture(t)

	ctx, ctxCancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, runnerInput(resolver, plan)); runDone <- err }()
	<-completeStarted
	ctxCancel()
	<-startedRelease
	close(finish)
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestRunnerNeverReleasesAfterFinalizeEvenIfFinalizeFails(t *testing.T) {
	client := &runnerTestClient{}
	port := quota.NewTypedMock()
	finalizeErr := errors.New("finalize storage lost")
	port.SetTypedFaultHook(func(quota.Reservation) error { return finalizeErr })
	log := requestlog.NewInMemoryExecution()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: &fakeClock{}, Sleeper: &recordingSleeper{}}
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	// Finalize uncertainty is surfaced safely with no Result because Release
	// must never compensate a Finalize attempt.
	if err == nil {
		t.Fatal("Run error = nil, want terminalization error")
	}
	assertTerminalizationError(t, err, "finalize", finalizeErr, testReservationID)
	if result.Completion.RawJSON != nil || result.Completion.Status != 0 || result.Completion.RequestID != "" || result.Failure != nil {
		t.Fatalf("result = %+v, want zero after Finalize uncertainty", result)
	}
	if calls := port.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Finalize only", calls)
	}
	if events := log.Events(context.Background()); len(events) != 2 || events[0].Kind != requestlog.KindReserved || events[1].Kind != requestlog.KindReleased || events[1].Settlement.Reason != "unknown" {
		t.Fatalf("events = %+v, must not claim success when finalization is unknown", events)
	}
}

func TestRunnerPostReserveCredentialFailureJoinsSafeReleaseTerminalization(t *testing.T) {
	creds := &prepareCounter{}
	client := &runnerTestClient{}
	runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	releaseErr := errors.New("release vault://private/terminal-secret")
	quotaPort.SetReleaseReservationFn(func(context.Context, quota.ReleaseRequest) (quota.Reservation, error) {
		return quota.Reservation{}, releaseErr
	})
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)
	in.Credentials = creds

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, routing.ErrCredentialUnavailable) {
		t.Fatalf("Run error = %v, want routing.ErrCredentialUnavailable", err)
	}
	assertTerminalizationError(t, err, "release", releaseErr, testReservationID)
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

func TestRunnerLogsAreBestEffortAndNeverAlterVerdict(t *testing.T) {
	// Success with a faulting logger still returns the Completion.
	log := requestlog.NewInMemoryExecution()
	log.SetFaultHook(func(requestlog.ExecutionEvent) error { return errors.New("log unavailable") })
	client := &runnerTestClient{}
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v, want nil despite log fault", err)
	}
	if result.Completion.Status != 200 {
		t.Fatalf("Completion = %+v", result.Completion)
	}
	if events := log.Events(context.Background()); len(events) != 3 {
		t.Fatalf("event still recorded = %d, want 3 (reserved+attempt+finalized)", len(events))
	}

	// Failure path: the faulting logger records but the retry decision is
	// unchanged (still advances to cred-b) and the final verdict is the
	// classified error.
	log = requestlog.NewInMemoryExecution()
	log.SetFaultHook(func(requestlog.ExecutionEvent) error { return errors.New("log unavailable") })
	failingClient := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	runner, _, _ = newRunner(t, failingClient, log)
	failureResult, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil || failureResult.Failure == nil {
		t.Fatalf("Run result/error = %+v/%v, want confirmed mapped failure/nil", failureResult, err)
	}
	if events := log.Events(context.Background()); len(events) == 0 {
		t.Fatal("faulting logger dropped all events")
	}
}

type deadlineObservingLogger struct {
	called chan struct{}
	once   sync.Once
}

func (l *deadlineObservingLogger) RecordExecution(ctx context.Context, _ requestlog.ExecutionEvent) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("logger received canceled context: %w", err)
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("logger context has no deadline")
	}
	l.once.Do(func() { close(l.called) })
	return nil
}

func (l *deadlineObservingLogger) QueryEvents(_ context.Context, _ requestlog.ExecutionFilter) ([]requestlog.ExecutionEvent, error) {
	return nil, nil
}

func TestRunnerCanceledRequestLogsWithLiveBoundedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		cancel()
		return sdk.Completion{}, context.Canceled
	}}
	logger := &deadlineObservingLogger{called: make(chan struct{})}
	runner, _, _ := newRunner(t, client, logger)
	runner.LogTimeout = time.Second
	resolver, plan := runnerFixture(t)

	_, err := runner.Run(ctx, runnerInput(resolver, plan))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	select {
	case <-logger.called:
	case <-time.After(time.Second):
		t.Fatal("logger did not receive a live bounded context")
	}
}

func TestRunnerLogSurfaceNeverLeaksSecretOrReferenceOrBody(t *testing.T) {
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "request/leak", "server_error", "")
	}}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)
	in.Credentials = staticCredentials{value: []byte("super-secret-key")}

	_, _ = runner.Run(context.Background(), in)
	for _, event := range log.Events(context.Background()) {
		rendered := fmt.Sprintf("%+v", event)
		for _, marker := range []string{"super-secret-key", "vault://", "secret", "messages", "raw-request-body"} {
			if strings.Contains(rendered, marker) {
				t.Fatalf("event leaked %q: %s", marker, rendered)
			}
		}
		if event.Kind == requestlog.KindAttempt && strings.Contains(event.Code, "/") {
			t.Fatalf("event Code leaked unsafe characters: %q", event.Code)
		}
	}
}

func TestRunnerSuccessRawJSONAllowedOnlyOnSuccess(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)
	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(result.Completion.RawJSON) == 0 {
		t.Fatal("success RawJSON must be present")
	}

	// On failure the returned error must not contain RawJSON.
	failingClient := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{RawJSON: json.RawMessage(`{"leaked":"body"}`)}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	runner, _, _ = newRunner(t, failingClient, log)
	failureResult, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil || failureResult.Failure == nil {
		t.Fatalf("Run result/error = %+v/%v, want confirmed mapped failure/nil", failureResult, err)
	}
	if rendered := fmt.Sprintf("%+v", failureResult); strings.Contains(rendered, "leaked") || strings.Contains(rendered, "body") {
		t.Fatalf("failure result leaked response body: %v", failureResult)
	}
}

func TestRunnerMisconfiguredDoesNotReserve(t *testing.T) {
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)

	// Missing Quota.
	runner := &Runner{SDKRegistry: NewSDKRegistry()}
	if _, err := runner.Run(context.Background(), in); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("no-quota error = %v", err)
	}
	// Missing SDKRegistry.
	runner = &Runner{Quota: quota.NewTypedMock()}
	if _, err := runner.Run(context.Background(), in); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("no-registry error = %v", err)
	}
	// Missing Resolver.
	runner = &Runner{Quota: quota.NewTypedMock(), SDKRegistry: NewSDKRegistry()}
	in.Resolver = nil
	if _, err := runner.Run(context.Background(), in); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("no-resolver error = %v", err)
	}
}

func TestRunnerNoCandidateRejectedBeforeReserve(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)
	in.Plan = routing.Plan{}

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("Run error = %v, want ErrNoCandidate", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 {
		t.Fatalf("no-candidate reserved quota: %+v", calls)
	}
}

func TestRunnerInvalidReservationIDFailsBeforePreflightOrQuota(t *testing.T) {
	for _, id := range []string{"", " \t\n", "res_short", "no-prefix-aaaaaaaaaaaa", "res_+invalid+chars!!!", "res_" + strings.Repeat("a", 15), "res_" + strings.Repeat("a", 129)} {
		id := id
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := &runnerTestClient{}
			runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
			resolver, plan := runnerFixture(t)
			in := runnerInput(resolver, plan)
			in.ReservationID = id

			_, err := runner.Run(context.Background(), in)
			if !errors.Is(err, ErrInvalidReservation) {
				t.Fatalf("Run error = %v, want %v", err, ErrInvalidReservation)
			}
			if calls := quotaPort.TypedCalls(); len(calls) != 0 {
				t.Fatalf("invalid ID called quota: %+v", calls)
			}
			if calls := client.callCount(); calls != 0 {
				t.Fatalf("invalid ID called SDK %d times", calls)
			}
		})
	}
}

func TestRunnerInvalidRequestIDFailsBeforePreflightOrQuota(t *testing.T) {
	for _, id := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := &runnerTestClient{}
			runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
			resolver, plan := runnerFixture(t)
			in := runnerInput(resolver, plan)
			in.RequestID = id
			credentials := &countingCredentials{value: []byte("must-not-resolve")}
			in.Credentials = credentials

			_, err := runner.Run(context.Background(), in)
			if !errors.Is(err, ErrInvalidRequestID) {
				t.Fatalf("Run error = %v, want %v", err, ErrInvalidRequestID)
			}
			if calls := quotaPort.TypedCalls(); len(calls) != 0 || client.callCount() != 0 {
				t.Fatalf("invalid request ID used quota or SDK: quota=%+v sdk=%d", calls, client.callCount())
			}
			if calls, _ := credentials.snapshot(); calls != 0 {
				t.Fatalf("invalid request ID resolved credential %d times", calls)
			}
		})
	}
}

func TestRunnerInvalidQuotaIdentityFailsBeforePreflightOrQuota(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity QuotaIdentity
	}{
		{"empty subject", QuotaIdentity{Subject: "", KeyID: "key-1", Protocol: "openai_chat"}},
		{"empty key", QuotaIdentity{Subject: "subject", KeyID: "", Protocol: "openai_chat"}},
		{"empty protocol", QuotaIdentity{Subject: "subject", KeyID: "key-1", Protocol: ""}},
		{"invalid subject", QuotaIdentity{Subject: " invalid", KeyID: "key-1", Protocol: "openai_chat"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &runnerTestClient{}
			runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
			resolver, plan := runnerFixture(t)
			in := runnerInput(resolver, plan)
			in.QuotaIdentity = tc.identity
			credentials := &countingCredentials{value: []byte("must-not-resolve")}
			in.Credentials = credentials

			_, err := runner.Run(context.Background(), in)
			if !errors.Is(err, ErrInvalidQuotaIdentity) {
				t.Fatalf("Run error = %v, want %v", err, ErrInvalidQuotaIdentity)
			}
			if calls := quotaPort.TypedCalls(); len(calls) != 0 || client.callCount() != 0 {
				t.Fatalf("invalid quota identity used quota or SDK: quota=%+v sdk=%d", calls, client.callCount())
			}
			if calls, _ := credentials.snapshot(); calls != 0 {
				t.Fatalf("invalid quota identity resolved credential %d times", calls)
			}
		})
	}
}

func TestRunnerReserveFailureReturnsSafeSentinelWithoutTerminalOrSDK(t *testing.T) {
	client := &runnerTestClient{}
	port := quota.NewTypedMock()
	// A quota backend error may contain opaque reservation data, credentials, or
	// endpoint details. Runner must expose only its fixed safe sentinel.
	rawReserveErr := errors.New("quota failure reservation=res-secret credential=super-secret url=https://quota.internal/res-secret")
	port.SetReserveReservationFn(func(context.Context, quota.ReserveRequest) (quota.Reservation, error) {
		return quota.Reservation{}, rawReserveErr
	})
	log := requestlog.NewInMemoryExecution()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: &fakeClock{}, Sleeper: &recordingSleeper{}}
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, ErrQuotaReserve) {
		t.Fatalf("Run error = %v, want ErrQuotaReserve", err)
	}
	if errors.Is(err, rawReserveErr) {
		t.Fatalf("Run error unwraps raw quota error: %v", err)
	}
	if got := err.Error(); got != ErrQuotaReserve.Error() {
		t.Fatalf("Run error text = %q, want fixed safe text %q", got, ErrQuotaReserve.Error())
	}
	for _, sensitive := range []string{"res-secret", "super-secret", "https://quota.internal"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("Run error leaked %q: %q", sensitive, err.Error())
		}
	}
	if result.Completion.RawJSON != nil || result.Completion.Status != 0 || result.Completion.RequestID != "" || result.Failure != nil {
		t.Fatalf("result = %+v, want zero Result", result)
	}
	if calls := port.TypedCalls(); len(calls) != 1 || calls[0].Method != "ReserveReservation" {
		t.Fatalf("quota calls = %+v, want Reserve only", calls)
	}
	if client.callCount() != 0 {
		t.Fatalf("client Complete calls = %d, want 0 (Reserve failed before any Complete)", client.callCount())
	}
	if events := log.Events(context.Background()); len(events) != 0 {
		t.Fatalf("events = %+v, want no terminal or execution logs", events)
	}
}

func TestRunnerPrepareFailsAfterReserveReleasesAndSurfacesSafeError(t *testing.T) {
	// Pure preflight does not resolve a secret. The first actual attempt's
	// credential resolution fails after Reserve and before BeginAttempt/Complete.
	creds := &prepareCounter{}
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)
	in.Credentials = creds

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, routing.ErrCredentialUnavailable) {
		t.Fatalf("Run error = %v, want routing.ErrCredentialUnavailable", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
	if client.callCount() != 0 {
		t.Fatalf("client calls = %d, want 0 (post-Reserve Prepare failed before Complete)", client.callCount())
	}
}

type prepareCounter struct{ calls int32 }

func (p *prepareCounter) Resolve(context.Context, string) (sdk.CredentialSecret, error) {
	atomic.AddInt32(&p.calls, 1)
	return sdk.CredentialSecret{}, errors.New("vault: access denied for vault://private/leak")
}

func TestRunnerEachAttemptRerunsPrepareAndApplyAndClientLookup(t *testing.T) {
	var count int32
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		n := atomic.AddInt32(&count, 1)
		if n <= 2 {
			return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
		}
		return sdk.Completion{RawJSON: json.RawMessage(`{}`), Status: 200, RequestID: "ok"}, nil
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, registry := newRunner(t, client, log)
	maxSame := 3
	resolver, plan := runnerFixtureWithRule(t, adapter.RetryRule{ID: "same-cred", HTTPStatuses: []int{503}, Action: adapter.RetrySameCredential}, &maxSame)
	in := runnerInput(resolver, plan)
	credentials := &countingCredentials{value: []byte("per-prepare-secret")}
	in.Credentials = credentials

	_, err := runner.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	// Three attempts => three Prepare/Apply/Client lookups. The same client
	// identity must be returned each time (write-once registry).
	if client.callCount() != 3 {
		t.Fatalf("client calls = %d, want 3", client.callCount())
	}
	// Pure preflight never resolves credentials; each of the three actual wire
	// attempts resolves exactly once, preventing secret reuse across retries.
	if calls, refs := credentials.snapshot(); calls != 3 || len(refs) != 3 {
		t.Fatalf("credential Resolve calls/refs = %d/%q, want 3 resolutions", calls, refs)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Finalize", calls)
	}
	if events := log.Events(context.Background()); len(events) != 5 {
		t.Fatalf("execution events = %d, want 5 (reserved + 3 attempts + finalized)", len(events))
	}
	// Registry must still hold exactly one client, proving each lookup reused
	// the registered instance rather than creating a new one.
	got, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat)
	if err != nil || got != client {
		t.Fatalf("registry client = %#v, %v, want original", got, err)
	}
}

func TestRunnerOfficialSDKRejectsIncompatibleAuthBeforeReserve(t *testing.T) {
	// The compiler rejects this production-invalid combination too. Mutate an
	// otherwise compiled fixture to exercise Runner's independent preflight
	// defense, which must reject before Reserve or SDK invocation.
	// Rebuild a fixture from a compiled config and change only the frozen
	// adapter/provider compatibility seen by Resolver.Prepare.
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "incompatible-auth", Models: map[string]adapter.ModelInput{"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}}},
		Providers: map[string]adapter.ProviderInput{"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindGenericHTTP, Protocol: adapter.ProtocolOpenAIChat}},
		Adapters:  map[string]adapter.AdapterConfig{"adapter": {ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindGenericHTTP, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthNone}}},
		Routes:    []adapter.RouteInput{{ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "upstream", Enabled: true, Protocol: adapter.ProtocolOpenAIChat}},
	})
	if err != nil {
		t.Fatalf("Compile fixture: %v", err)
	}
	genericSource, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 8)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(generic): %v", err)
	}
	genericResolver, err := routing.NewResolver(genericSource, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver(generic): %v", err)
	}
	genericPlan, err := genericResolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve(generic): %v", err)
	}
	genericClient := &runnerTestClient{}
	genericQuota := quota.NewTypedMock()
	genericRegistry := NewSDKRegistry()
	genericRunner := &Runner{Quota: genericQuota, SDKRegistry: genericRegistry}
	genericInput := runnerInput(genericResolver, genericPlan)
	genericCredentials := &countingCredentials{err: errors.New("AuthNone must not resolve credentials")}
	genericInput.Credentials = genericCredentials
	if _, err := genericRunner.Run(context.Background(), genericInput); !errors.Is(err, ErrSDKClientUnknown) {
		t.Fatalf("generic unregistered Run error = %v, want ErrSDKClientUnknown", err)
	}
	if calls, _ := genericCredentials.snapshot(); calls != 0 {
		t.Fatalf("generic AuthNone preflight resolved credentials %d times", calls)
	}
	if calls := genericQuota.TypedCalls(); len(calls) != 0 || genericClient.callCount() != 0 {
		t.Fatalf("unregistered generic path used quota or SDK: quota=%+v sdk=%d", calls, genericClient.callCount())
	}
	if err := genericRegistry.Register(adapter.SDKKindGenericHTTP, adapter.ProtocolOpenAIChat, genericClient); err != nil {
		t.Fatalf("Register generic client: %v", err)
	}
	result, err := genericRunner.Run(context.Background(), genericInput)
	if err != nil || result.Completion.Status != 200 {
		t.Fatalf("generic AuthNone Run result/error = %+v/%v", result, err)
	}
	if calls, _ := genericCredentials.snapshot(); calls != 0 {
		t.Fatalf("generic AuthNone Run resolved credentials %d times", calls)
	}
	if calls := genericQuota.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" || genericClient.callCount() != 1 {
		t.Fatalf("generic AuthNone calls quota=%+v sdk=%d, want Reserve+Finalize and one Complete", calls, genericClient.callCount())
	}
	config.Providers["provider"] = adapter.CompiledProvider{ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat}
	config.Adapters["adapter"] = adapter.CompiledAdapter{ID: "adapter", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthNone}}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 9)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	resolver, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	plan, err := resolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	client := &runnerTestClient{}
	runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	_, err = runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, ErrIncompatibleAuth) {
		t.Fatalf("Run error = %v, want ErrIncompatibleAuth", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 || client.callCount() != 0 {
		t.Fatalf("incompatible auth used quota or SDK: calls=%+v sdk=%d", calls, client.callCount())
	}
}

func TestSDKAuthCompatibleOfficialKinds(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind adapter.SDKKind
		auth adapter.AuthKind
		want bool
	}{
		{"openai bearer", adapter.SDKKindOpenAI, adapter.AuthBearerHeader, true},
		{"openai api key", adapter.SDKKindOpenAI, adapter.AuthAPIKeyHeader, false},
		{"openai none", adapter.SDKKindOpenAI, adapter.AuthNone, false},
		{"anthropic api key", adapter.SDKKindAnthropic, adapter.AuthAPIKeyHeader, true},
		{"anthropic bearer", adapter.SDKKindAnthropic, adapter.AuthBearerHeader, false},
		{"anthropic none", adapter.SDKKindAnthropic, adapter.AuthNone, false},
		{"generic remains registry governed", adapter.SDKKindGenericHTTP, adapter.AuthNone, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sdkAuthCompatible(tc.kind, tc.auth); got != tc.want {
				t.Fatalf("sdkAuthCompatible(%q, %q) = %v, want %v", tc.kind, tc.auth, got, tc.want)
			}
		})
	}
}

func TestRunnerInvalidRequestTimeoutFailsPreflightWithoutQuotaOrSDK(t *testing.T) {
	resolver, plan := runnerFixture(t)
	// A malformed frozen snapshot must be rejected by Runner even though the
	// compiler normally prevents it, before Reserve and Complete.
	prepared, err := resolver.Prepare(plan.Candidates[0])
	if err != nil || prepared.Timeout.Request <= 0 {
		t.Fatalf("fixture Prepare = %+v, %v", prepared.Timeout, err)
	}
	// Recreate the normal fixture and change only the compiled route timeout.
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "zero-timeout", Models: map[string]adapter.ModelInput{"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}}},
		Providers: map[string]adapter.ProviderInput{"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat}},
		Adapters:  map[string]adapter.AdapterConfig{"adapter": {ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"}}},
		Routes:    []adapter.RouteInput{{ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "upstream", Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "credential", CredentialRef: "vault://private/timeout", Enabled: true}}}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	config.Routes[0].Timeout.Request = 0
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 12)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	resolver, err = routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	plan, err = resolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	client := &runnerTestClient{}
	runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	_, err = runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("Run error = %v, want ErrMisconfigured", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 || client.callCount() != 0 {
		t.Fatalf("invalid timeout used quota or SDK: calls=%+v sdk=%d", calls, client.callCount())
	}
}

func TestRunnerUnknownSDKKindDuringPreflightDoesNotReserve(t *testing.T) {
	// Register a client for the wrong protocol to force a preflight
	// UnknownSDKClientError before any Reserve.
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIResponses, client)
	runner := &Runner{Quota: quotaPort, SDKRegistry: registry, Logger: log, Clock: &fakeClock{}, Sleeper: &recordingSleeper{}}
	resolver, plan := runnerFixture(t)

	_, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, ErrSDKClientUnknown) {
		t.Fatalf("Run error = %v, want ErrSDKClientUnknown", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 {
		t.Fatalf("preflight reserved quota: %+v", calls)
	}
	if client.callCount() != 0 {
		t.Fatalf("preflight called client %d times", client.callCount())
	}
}

func TestRunnerApplyFailureDuringPreflightDoesNotReserve(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)
	// An empty body fails Engine.Apply because the contract requires a
	// top-level JSON object.
	in.Body = nil

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, adapter.ErrInvalidInput) {
		t.Fatalf("Run error = %v, want adapter.ErrInvalidInput", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 0 {
		t.Fatalf("preflight reserved quota: %+v", calls)
	}
}

func TestRunnerDefaultSleeperRespectsContextCancellation(t *testing.T) {
	sleeper := contextSleeper{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleeper.Sleep(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep canceled error = %v, want context.Canceled", err)
	}
	if err := sleeper.Sleep(context.Background(), -time.Second); err != nil {
		t.Fatalf("Sleep negative error = %v, want nil", err)
	}
	if err := (contextSleeper{}).Sleep(context.Background(), 0); err != nil {
		t.Fatalf("Sleep zero error = %v", err)
	}
}

func TestRunnerSleepCancellationReleasesUnderCleanupTimeout(t *testing.T) {
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	log := requestlog.NewInMemoryExecution()
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     cancelingSleeper{}, // simulates request cancellation during backoff
	}
	resolver, plan := runnerFixture(t)

	_, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

type cancelingSleeper struct{}

func (cancelingSleeper) Sleep(context.Context, time.Duration) error { return context.Canceled }

type waitingSleeper struct {
	started chan struct{}
	calls   int32
}

func (s *waitingSleeper) Sleep(ctx context.Context, _ time.Duration) error {
	atomic.AddInt32(&s.calls, 1)
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestRunnerCancellationDuringRetryWaitReleasesAndDoesNotStartAnotherSDKCall(t *testing.T) {
	// The retry wait must consume the request context. Once it is canceled, the
	// Runner releases under its detached cleanup context and must not begin the
	// retry candidate's logical or wire attempt.
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	waiter := &waitingSleeper{started: make(chan struct{})}
	runner.Sleeper = waiter
	resolver, plan := runnerFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, runnerInput(resolver, plan)); runDone <- err }()
	<-waiter.started
	cancel()

	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation during retry wait")
	}
	if calls := atomic.LoadInt32(&waiter.calls); calls != 1 {
		t.Fatalf("Sleep calls = %d, want 1", calls)
	}
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("client Complete calls = %d, want 1; retry wait cancellation started another wire call", calls)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

func TestRunnerReleaseFailureJoinsSafeTerminalizationWithClassifiedFailure(t *testing.T) {
	// Once an upstream verdict is safely classified, a failed Release retains
	// that primary verdict and adds only a safe terminalization uncertainty;
	// neither the raw port error nor reservation identifier may escape.
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	log := requestlog.NewInMemoryExecution()
	runner, quotaPort, _ := newRunner(t, client, log)
	releaseErr := errors.New("quota release unavailable")
	quotaPort.SetReleaseReservationFn(func(context.Context, quota.ReleaseRequest) (quota.Reservation, error) {
		return quota.Reservation{}, releaseErr
	})
	resolver, plan := runnerFixtureWithRule(t, adapter.RetryRule{ID: "stop", HTTPStatuses: []int{503}, Action: adapter.RetryNone}, nil)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	var classified *sdk.ClassifiedError
	if !errors.As(err, &classified) || classified == nil {
		t.Fatalf("Run error = %v, want classified upstream error", err)
	}
	assertTerminalizationError(t, err, "release", releaseErr, testReservationID)
	assertZeroResult(t, result)
	if calls := client.callCount(); calls != 1 {
		t.Fatalf("client Complete calls = %d, want 1", calls)
	}
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release only", calls)
	}
}

func TestRunnerReleaseTerminalizationPreservesContextPrimary(t *testing.T) {
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, context.Canceled
	}}
	runner, quotaPort, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	releaseErr := errors.New("quota release secret=do-not-leak")
	quotaPort.SetReleaseReservationFn(func(context.Context, quota.ReleaseRequest) (quota.Reservation, error) {
		return quota.Reservation{}, releaseErr
	})
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	assertTerminalizationError(t, err, "release", releaseErr, testReservationID)
	assertZeroResult(t, result)
}

func assertZeroResult(t *testing.T, result Result) {
	t.Helper()
	if result.Completion.RawJSON != nil || result.Completion.Status != 0 || result.Completion.RequestID != "" || result.Failure != nil {
		t.Fatalf("result = %+v, want zero Result", result)
	}
}

func assertTerminalizationError(t *testing.T, err error, operation string, raw error, reservationID string) {
	t.Helper()
	if !errors.Is(err, ErrTerminalization) {
		t.Fatalf("error = %v, want ErrTerminalization", err)
	}
	var terminal *TerminalizationError
	if !errors.As(err, &terminal) || terminal == nil {
		t.Fatalf("error = %v, want *TerminalizationError", err)
	}
	if terminal.Operation != operation || terminal.Outcome != "unknown" {
		t.Fatalf("TerminalizationError = %#v, want operation=%q outcome=unknown", terminal, operation)
	}
	if errors.Is(err, raw) || strings.Contains(err.Error(), raw.Error()) || strings.Contains(err.Error(), reservationID) {
		t.Fatalf("terminalization error leaked raw terminal detail: %v", err)
	}
}

func TestRunnerFinalClassifiedStopReturnsMappedFailureAndSafeError(t *testing.T) {
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		// Anthropic's 529 overload classification remains safe upstream metadata;
		// the adapter response policy is the authoritative public mapping.
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 529, "req_529", "", "overloaded_error")
	}}
	runner, _, _ := newRunner(t, client, requestlog.NewInMemoryExecution())
	resolver, plan := runnerFixtureWithRule(t, adapter.RetryRule{ID: "stop", HTTPStatuses: []int{529}, Action: adapter.RetryNone}, nil)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v, want nil after confirmed Release", err)
	}
	if result.Failure == nil || result.Failure.HTTPStatus != 429 || result.Failure.ErrorCode != "RATE_LIMITED" || result.Failure.ErrorType != "rate_limited" || result.Failure.MatchedID != "resp-529-to-429" {
		t.Fatalf("Failure = %#v, want adapter 529-to-429 mapping", result.Failure)
	}
	if result.Completion.RawJSON != nil {
		t.Fatalf("failure result leaked completion = %#v", result.Completion)
	}
}

func TestRunnerResultAndErrorSurfacesDoNotLeak(t *testing.T) {
	// Each failure surface must not echo the call-local secret, credential
	// reference, or request body. Use reflection-style rendering of the
	// returned error string only.
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)
	in := runnerInput(resolver, plan)
	in.Credentials = staticCredentials{value: []byte("leak-secret-key")}
	in.Body = json.RawMessage(`{"messages":[{"role":"user","content":"leak-body"}]}`)

	result, err := runner.Run(context.Background(), in)
	if err != nil || result.Failure == nil {
		t.Fatalf("Run result/error = %+v/%v, want confirmed mapped failure/nil", result, err)
	}
	rendered := fmt.Sprintf("%+v", result)
	for _, marker := range []string{"leak-secret-key", "leak-body", "vault://"} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("result leaked %q: %v", marker, rendered)
		}
	}
}

func TestRunnerConfirmedUsageFinalizesWithAccountingConfirmedUsage(t *testing.T) {
	// When the adapter extracts known and valid usage, the Runner finalizes
	// with AccountingConfirmedUsage and the confirmed token counts.
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{
			RawJSON:   json.RawMessage(`{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`),
			Status:    200,
			RequestID: "req_ok",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			Known:     true,
		}, nil
	}}
	log := requestlog.NewInMemoryExecution()
	port := quota.NewTypedMock()
	var gotFinalize quota.FinalizeRequest
	port.SetFinalizeReservationFn(func(_ context.Context, in quota.FinalizeRequest) (quota.Reservation, error) {
		gotFinalize = in
		return port.DomainInMemory.FinalizeReservation(context.Background(), in)
	})
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: clock, Sleeper: &recordingSleeper{clock: clock}}
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.Usage != (sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}) || !result.Completion.Known {
		t.Fatalf("Completion usage = %+v, known=%v", result.Completion.Usage, result.Completion.Known)
	}
	if gotFinalize.Outcome.Disposition != quota.AccountingConfirmedUsage {
		t.Fatalf("disposition = %q, want %q", gotFinalize.Outcome.Disposition, quota.AccountingConfirmedUsage)
	}
	if gotFinalize.Outcome.Usage != (quota.ConfirmedUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}) {
		t.Fatalf("usage = %+v, want {10 20 30}", gotFinalize.Outcome.Usage)
	}
}

func TestRunnerUnknownUsageFinalizesWithAccountingUnpricedSuccess(t *testing.T) {
	// When the adapter does not extract known usage (Known=false), the Runner
	// falls back to AccountingUnpricedSuccess, preserving the current behavior.
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	port := quota.NewTypedMock()
	var gotFinalize quota.FinalizeRequest
	port.SetFinalizeReservationFn(func(_ context.Context, in quota.FinalizeRequest) (quota.Reservation, error) {
		gotFinalize = in
		return port.DomainInMemory.FinalizeReservation(context.Background(), in)
	})
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: clock, Sleeper: &recordingSleeper{clock: clock}}
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.Known {
		t.Fatalf("Completion.Known = true, want false for default test client")
	}
	if gotFinalize.Outcome.Disposition != quota.AccountingUnpricedSuccess {
		t.Fatalf("disposition = %q, want %q", gotFinalize.Outcome.Disposition, quota.AccountingUnpricedSuccess)
	}
	if gotFinalize.Outcome.Usage != (quota.ConfirmedUsage{}) {
		t.Fatalf("usage = %+v, want zero", gotFinalize.Outcome.Usage)
	}
}

func TestRunnerKnownButInvalidUsageFallsBackToUnpricedSuccess(t *testing.T) {
	// When Known=true but Valid()=false (inconsistent counters), the Runner
	// must not record incorrect usage and falls back to unpriced success.
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{
			RawJSON:   json.RawMessage(`{}`),
			Status:    200,
			RequestID: "req_ok",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 99}, // inconsistent
			Known:     true,
		}, nil
	}}
	log := requestlog.NewInMemoryExecution()
	port := quota.NewTypedMock()
	var gotFinalize quota.FinalizeRequest
	port.SetFinalizeReservationFn(func(_ context.Context, in quota.FinalizeRequest) (quota.Reservation, error) {
		gotFinalize = in
		return port.DomainInMemory.FinalizeReservation(context.Background(), in)
	})
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: clock, Sleeper: &recordingSleeper{clock: clock}}
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if gotFinalize.Outcome.Disposition != quota.AccountingUnpricedSuccess {
		t.Fatalf("disposition = %q, want %q for invalid usage", gotFinalize.Outcome.Disposition, quota.AccountingUnpricedSuccess)
	}
	if gotFinalize.Outcome.Usage != (quota.ConfirmedUsage{}) {
		t.Fatalf("usage = %+v, want zero for invalid usage", gotFinalize.Outcome.Usage)
	}
	_ = result
}

func TestRunnerFinalizeOutcomeMapping(t *testing.T) {
	for _, tc := range []struct {
		name        string
		completion  sdk.Completion
		disposition quota.AccountingDisposition
		usage       quota.ConfirmedUsage
	}{
		{
			name:        "known valid usage",
			completion:  sdk.Completion{Usage: sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}, Known: true},
			disposition: quota.AccountingConfirmedUsage,
			usage:       quota.ConfirmedUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		},
		{
			name:        "unknown usage",
			completion:  sdk.Completion{Known: false},
			disposition: quota.AccountingUnpricedSuccess,
			usage:       quota.ConfirmedUsage{},
		},
		{
			name:        "known but invalid usage",
			completion:  sdk.Completion{Usage: sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 99}, Known: true},
			disposition: quota.AccountingUnpricedSuccess,
			usage:       quota.ConfirmedUsage{},
		},
		{
			name:        "known zero usage",
			completion:  sdk.Completion{Usage: sdk.Usage{}, Known: true},
			disposition: quota.AccountingConfirmedUsage,
			usage:       quota.ConfirmedUsage{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome := runnerFinalizeOutcome(tc.completion)
			if outcome.Disposition != tc.disposition {
				t.Fatalf("disposition = %q, want %q", outcome.Disposition, tc.disposition)
			}
			if outcome.Outcome != quota.OutcomeCompleted {
				t.Fatalf("outcome = %q, want completed", outcome.Outcome)
			}
			if outcome.Usage != tc.usage {
				t.Fatalf("usage = %+v, want %+v", outcome.Usage, tc.usage)
			}
		})
	}
}

func TestRunnerLifecycleEventSequenceSuccess(t *testing.T) {
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.Status != 200 {
		t.Fatalf("Completion = %+v", result.Completion)
	}

	events := log.Events(context.Background())
	// Expected: reserved → attempt(success) → finalized
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Kind != requestlog.KindReserved {
		t.Fatalf("events[0].Kind = %q, want reserved", events[0].Kind)
	}
	if events[0].Subject != "subject" || events[0].KeyID != "key-1" {
		t.Fatalf("reserved event Subject/KeyID = %q/%q", events[0].Subject, events[0].KeyID)
	}
	if events[1].Kind != requestlog.KindAttempt || events[1].Status != "success" {
		t.Fatalf("events[1] = %+v, want attempt success", events[1])
	}
	if events[1].Subject != "subject" || events[1].KeyID != "key-1" {
		t.Fatalf("attempt event Subject/KeyID = %q/%q", events[1].Subject, events[1].KeyID)
	}
	if events[2].Kind != requestlog.KindFinalized {
		t.Fatalf("events[2].Kind = %q, want finalized", events[2].Kind)
	}
	if events[2].Settlement.Disposition != string(quota.AccountingUnpricedSuccess) || events[2].Settlement.Outcome != string(quota.OutcomeCompleted) {
		t.Fatalf("finalized Settlement = %+v", events[2].Settlement)
	}
}

func TestRunnerLifecycleEventSequenceFailure(t *testing.T) {
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	}}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixtureWithRule(t, adapter.RetryRule{ID: "stop", HTTPStatuses: []int{503}, Action: adapter.RetryNone}, nil)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil || result.Failure == nil {
		t.Fatalf("Run result/error = %+v/%v", result, err)
	}

	events := log.Events(context.Background())
	// Expected: reserved → attempt(failed) → released
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Kind != requestlog.KindReserved {
		t.Fatalf("events[0].Kind = %q, want reserved", events[0].Kind)
	}
	if events[1].Kind != requestlog.KindAttempt || events[1].Status != "failed" {
		t.Fatalf("events[1] = %+v, want attempt failed", events[1])
	}
	if events[2].Kind != requestlog.KindReleased {
		t.Fatalf("events[2].Kind = %q, want released", events[2].Kind)
	}
	if events[2].Settlement.Reason != string(quota.ReleaseFailed) {
		t.Fatalf("released Settlement.Reason = %q, want %q", events[2].Settlement.Reason, quota.ReleaseFailed)
	}
}

func TestRunnerLifecycleEventSequenceTerminalizationUnknown(t *testing.T) {
	client := &runnerTestClient{}
	port := quota.NewTypedMock()
	port.SetTypedFaultHook(func(quota.Reservation) error { return errors.New("finalize lost") })
	log := requestlog.NewInMemoryExecution()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	runner := &Runner{Quota: port, SDKRegistry: registry, Logger: log, Clock: &fakeClock{}, Sleeper: &recordingSleeper{}}
	resolver, plan := runnerFixture(t)

	_, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if !errors.Is(err, ErrTerminalization) {
		t.Fatalf("Run error = %v, want ErrTerminalization", err)
	}

	events := log.Events(context.Background())
	// Expected: reserved → released(unknown) — no attempt success, no finalized
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if events[0].Kind != requestlog.KindReserved {
		t.Fatalf("events[0].Kind = %q, want reserved", events[0].Kind)
	}
	if events[1].Kind != requestlog.KindReleased || events[1].Settlement.Reason != "unknown" {
		t.Fatalf("events[1] = %+v, want released(unknown)", events[1])
	}
}

func TestApplyRetryAfter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		mapped         adapter.MappedResponse
		classified     *sdk.ClassifiedError
		wantRetryAfter int
	}{
		{
			name:           "429 with Retry-After 30s",
			mapped:         adapter.MappedResponse{HTTPStatus: 429},
			classified:     sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrRateLimited, 429, "req_1", "rate_limited", "rate_limit_error", 30*time.Second, true),
			wantRetryAfter: 30,
		},
		{
			name:           "529 with Retry-After 60s",
			mapped:         adapter.MappedResponse{HTTPStatus: 529},
			classified:     sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrUnavailable, 529, "req_2", "overloaded", "overloaded_error", 60*time.Second, true),
			wantRetryAfter: 60,
		},
		{
			name:           "429 without Retry-After",
			mapped:         adapter.MappedResponse{HTTPStatus: 429},
			classified:     sdk.NewClassifiedError(sdk.ErrRateLimited, 429, "req_3", "rate_limited", "rate_limit_error"),
			wantRetryAfter: 0,
		},
		{
			name:           "500 with Retry-After ignored",
			mapped:         adapter.MappedResponse{HTTPStatus: 500},
			classified:     sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrUpstream, 500, "req_4", "internal", "api_error", 10*time.Second, true),
			wantRetryAfter: 0,
		},
		{
			name:           "429 with sub-second Retry-After ignored",
			mapped:         adapter.MappedResponse{HTTPStatus: 429},
			classified:     sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrRateLimited, 429, "req_5", "rate_limited", "rate_limit_error", 500*time.Millisecond, true),
			wantRetryAfter: 0,
		},
		{
			name:           "429 with Retry-After clamped to 300",
			mapped:         adapter.MappedResponse{HTTPStatus: 429},
			classified:     sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrRateLimited, 429, "req_6", "rate_limited", "rate_limit_error", 600*time.Second, true),
			wantRetryAfter: 300,
		},
		{
			name:           "nil classified",
			mapped:         adapter.MappedResponse{HTTPStatus: 429},
			classified:     nil,
			wantRetryAfter: 0,
		},
		{
			name:           "nil mapped",
			mapped:         adapter.MappedResponse{},
			classified:     nil,
			wantRetryAfter: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapped := tc.mapped
			applyRetryAfter(&mapped, tc.classified)
			if mapped.RetryAfterSeconds != tc.wantRetryAfter {
				t.Errorf("RetryAfterSeconds = %d, want %d", mapped.RetryAfterSeconds, tc.wantRetryAfter)
			}
		})
	}
}

func TestRunnerLifecycleEventConfirmedUsageInFinalized(t *testing.T) {
	client := &runnerTestClient{completeFn: func(context.Context, sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{
			RawJSON:   json.RawMessage(`{}`),
			Status:    200,
			RequestID: "req_ok",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			Known:     true,
		}, nil
	}}
	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	_ = result

	events := log.Events(context.Background())
	// Find the finalized event
	var finalized *requestlog.ExecutionEvent
	for i := range events {
		if events[i].Kind == requestlog.KindFinalized {
			finalized = &events[i]
			break
		}
	}
	if finalized == nil {
		t.Fatal("no finalized event found")
	}
	if finalized.Settlement.Disposition != string(quota.AccountingConfirmedUsage) {
		t.Fatalf("Disposition = %q, want %q", finalized.Settlement.Disposition, quota.AccountingConfirmedUsage)
	}
	if !finalized.UsageKnown || finalized.Usage != (requestlog.ExecutionUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}) {
		t.Fatalf("finalized Usage = %+v, UsageKnown = %v", finalized.Usage, finalized.UsageKnown)
	}

	// Find the attempt success event
	var attempt *requestlog.ExecutionEvent
	for i := range events {
		if events[i].Kind == requestlog.KindAttempt && events[i].Status == "success" {
			attempt = &events[i]
			break
		}
	}
	if attempt == nil {
		t.Fatal("no attempt success event found")
	}
	if !attempt.UsageKnown || attempt.Usage != (requestlog.ExecutionUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}) {
		t.Fatalf("attempt Usage = %+v, UsageKnown = %v", attempt.Usage, attempt.UsageKnown)
	}
}
