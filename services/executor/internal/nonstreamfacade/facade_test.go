package nonstreamfacade

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
	"github.com/tokenmp/v3/services/executor/internal/requestid"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// newFacade builds a facade wired to a real store, a recording runner, and
// a no-op quarantine reader that consults state and finds nothing excluded
// (returns routing.ErrNotFound for every target). This is the safe default
// reader, distinct from a nil reader, which the facade rejects because it
// would silently bypass quarantine filtering.
func newFacade(t *testing.T, runner *recordingRunner) (*Facade, *snapshot.Store) {
	t.Helper()
	store := buildStore(t)
	opts := Options{
		Store:       store,
		Runner:      runner,
		Credentials: staticCredentials{value: []byte("call-local-secret")},
		Quarantine:  noopQuarantine{},
	}
	return New(opts), store
}

func TestFacadeAllowsOpenAIImagesProtocol(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	facade, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "image-request")
	req.Protocol = adapter.ProtocolOpenAIImages
	_, err := facade.Execute(context.Background(), req)
	if errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("images protocol rejected: %v", err)
	}
}

func TestFacadeNilFacadeFailsClosed(t *testing.T) {
	t.Parallel()
	var f *Facade
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
}

func TestFacadeNilQuarantineFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	// A nil Quarantine would silently bypass quarantine filtering, which is a
	// security degradation. It must fail closed with ErrMisconfigured before
	// any snapshot read, routing, reservation, or Run call.
	f := New(Options{Store: store, Runner: runner, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeTypedNilQuarantineFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	// A typed-nil Quarantine wrapped in the interface is an injected-but-broken
	// reader. It must fail closed with ErrMisconfigured rather than panicking
	// on dispatch or silently bypassing quarantine consultation.
	var typedNil *stubErrQuarantine
	f := New(Options{Store: store, Runner: runner, Credentials: staticCredentials{value: []byte("x")}, Quarantine: typedNil})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeNilStoreFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{}
	f := New(Options{Runner: runner, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeNilRunnerFailsClosed(t *testing.T) {
	t.Parallel()
	store := buildStore(t)
	f := New(Options{Store: store, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
}

func TestFacadeTypedNilRunnerFailsClosed(t *testing.T) {
	t.Parallel()
	store := buildStore(t)
	// A typed-nil *recordingRunner wrapped in the Runner interface must not
	// panic; it fails closed.
	var typedNil *recordingRunner
	f := New(Options{Store: store, Runner: typedNil, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
}

func TestFacadeTrustedPrincipalRequired(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	// No Principal: must fail closed before routing or execution.
	_, err := f.Execute(context.Background(), unauthenticated(chatRequest("chat-model", "req-1")))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeDisabledPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "req-1")
	req.Principal.Status = nonstream.StatusDisabled
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeNonServiceRolePrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "req-1")
	req.Principal.Role = "user"
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestFacadeEmptySubjectPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "req-1")
	req.Principal.Subject = ""
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestFacadeNonPrintableSubjectPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "req-1")
	req.Principal.Subject = "injected\x00\x01"
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestFacadeAdminRolePrincipalAdmitted(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "req-1")
	req.Principal.Role = nonstream.RoleAdmin
	_, err := f.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestFacadeInvalidProtocolFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	req := chatRequest("chat-model", "req-1")
	req.Protocol = "" // absent protocol must not resolve cross-protocol routes
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("err = %v, want ErrInvalidProtocol", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeEmptyRequestIDFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "   "))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeNoSnapshotFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f := New(Options{Store: &snapshot.Store{}, Runner: runner, Credentials: staticCredentials{value: []byte("x")}, Quarantine: noopQuarantine{}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("err = %v, want ErrNoSnapshot", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeModelNotFoundReturnsTransport404Sentinel(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	// Well-formed selector for a model that does not exist in the chat protocol.
	_, err := f.Execute(context.Background(), chatRequest("no-such-model", "req-1"))
	if !errors.Is(err, nonstream.ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
	// The error must not carry routing detail.
	if strings.Contains(err.Error(), "no-such-model") {
		t.Fatalf("error leaked selector: %v", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeProtocolFilterOpenAIChatExcludesAnthropicRoute(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	// The anthropic model is not a chat route; a chat request for it must be
	// model-not-found, proving the protocol filter excludes cross-protocol.
	_, err := f.Execute(context.Background(), chatRequest("anthropic-model", "req-1"))
	if !errors.Is(err, nonstream.ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeProtocolFilterAnthropicExcludesChatRoute(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	_, err := f.Execute(context.Background(), messageRequest("chat-model", "req-1"))
	if !errors.Is(err, nonstream.ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
}

func TestFacadeInvalidSelectorReturnsInvalidRequest(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	// Two provider delimiters: syntactically invalid selector grammar.
	req := chatRequest("chat-model@openai@extra", "req-1")
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, nonstream.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeQuarantineUnavailableFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	f := New(Options{
		Store:       store,
		Runner:      runner,
		Credentials: staticCredentials{value: []byte("x")},
		Quarantine:  stubErrQuarantine{err: routing.ErrQuarantineUnavailable},
	})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrRouting) {
		t.Fatalf("err = %v, want ErrRouting", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeContextCancellationPropagatesBeforeRouting(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.Execute(ctx, chatRequest("chat-model", "req-1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeRunnerCalledExactlyOnceOnSuccess(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{
		onceGate: true,
		result:   execution.Result{Completion: sdk.Completion{RawJSON: []byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`), Status: 200, RequestID: "req-1"}},
	}
	f, _ := newFacade(t, runner)
	result, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", runner.callCount())
	}
	if result.Completion.Status != 200 {
		t.Fatalf("result status = %d, want 200", result.Completion.Status)
	}
}

func TestFacadeRunnerCalledExactlyOnceOnFailure(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true, runErr: execution.ErrUnclassified}
	f, _ := newFacade(t, runner)
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, execution.ErrUnclassified) {
		t.Fatalf("err = %v, want ErrUnclassified", err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", runner.callCount())
	}
}

func TestFacadeRunnerNotCalledOnPreExecutionFailure(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	// Model-not-found is a pre-execution failure; the onceGate guarantees any
	// second call would panic, and callCount must remain zero.
	_, _ = f.Execute(context.Background(), chatRequest("no-such-model", "req-1"))
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeCSPRNGReservationIDDefaultPassedToRunner(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := runner.lastInput()
	if !requestid.ValidReservationID(in.ReservationID) {
		t.Fatalf("reservation id = %q, want valid grammar", in.ReservationID)
	}
}

func TestFacadeReservationIDSourceInjection(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	f := New(Options{
		Store:          store,
		Runner:         runner,
		Credentials:    staticCredentials{value: []byte("x")},
		Quarantine:     noopQuarantine{},
		ReservationIDs: requestid.SourceFunc(func(context.Context) string { return "res_injected-valid-id1" }),
	})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := runner.lastInput().ReservationID; got != "res_injected-valid-id1" {
		t.Fatalf("reservation id = %q, want res_injected-valid-id1", got)
	}
}

func TestFacadeTypedNilReservationIDSourceFallsBackToCSPRNG(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	// A typed-nil func wrapped in the interface must not panic; the default
	// CSPRNG source is used.
	var typedNil requestid.SourceFunc
	f := New(Options{Store: store, Runner: runner, Credentials: staticCredentials{value: []byte("x")}, Quarantine: noopQuarantine{}, ReservationIDs: typedNil})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !requestid.ValidReservationID(runner.lastInput().ReservationID) {
		t.Fatalf("reservation id = %q, want CSPRNG default", runner.lastInput().ReservationID)
	}
}

func TestFacadeEmptyReservationIDFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	f := New(Options{
		Store:          store,
		Runner:         runner,
		Credentials:    staticCredentials{value: []byte("x")},
		Quarantine:     noopQuarantine{},
		ReservationIDs: requestid.SourceFunc(func(context.Context) string { return "" }),
	})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrReservationID) {
		t.Fatalf("err = %v, want ErrReservationID", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeInvalidGrammarReservationIDFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	// A source returning a short suffix fails the grammar gate (16-128 chars).
	f := New(Options{
		Store:          store,
		Runner:         runner,
		Credentials:    staticCredentials{value: []byte("x")},
		Quarantine:     noopQuarantine{},
		ReservationIDs: requestid.SourceFunc(func(context.Context) string { return "res_short" }),
	})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrReservationID) {
		t.Fatalf("err = %v, want ErrReservationID", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadePinsSnapshotPerRequest(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: false}
	f, store := newFacade(t, runner)

	// First request pins generation 1.
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute gen 1: %v", err)
	}
	if got := runner.lastInput().Plan.Generation; got != 1 {
		t.Fatalf("gen 1 plan generation = %d, want 1", got)
	}

	// Publish generation 2. The first request's resolver is already pinned and
	// unaffected; a second request pins generation 2.
	config2, err := adapter.Compile(adapter.ConfigInput{
		Revision: "facade-revision-2",
		Models:   map[string]adapter.ModelInput{"chat-model": {ID: "chat-model", Capabilities: []adapter.Capability{adapter.CapabilityChat}}},
		Providers: map[string]adapter.ProviderInput{
			"openai": {ID: "openai", Name: "openai", Selector: "openai", BaseURL: "https://openai.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"chat-adapter": {ID: "chat-adapter", Name: "chat-adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"}},
		},
		Routes: []adapter.RouteInput{
			{ID: "chat-route", ModelID: "chat-model", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "gpt-upstream", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true}}},
		},
	})
	if err != nil {
		t.Fatalf("Compile 2: %v", err)
	}
	src2, err := snapshot.NewCompiledSnapshot(config2.Revision, &config2, 2)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot 2: %v", err)
	}
	if err := store.Publish(src2); err != nil {
		t.Fatalf("Publish 2: %v", err)
	}

	_, err = f.Execute(context.Background(), chatRequest("chat-model", "req-2"))
	if err != nil {
		t.Fatalf("Execute gen 2: %v", err)
	}
	if got := runner.lastInput().Plan.Generation; got != 2 {
		t.Fatalf("gen 2 plan generation = %d, want 2", got)
	}
	if got := runner.lastInput().Plan.Revision; got != "facade-revision-2" {
		t.Fatalf("gen 2 plan revision = %q, want facade-revision-2", got)
	}
}

func TestFacadePreservesResolverOwnedPlan(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, store := newFacade(t, runner)
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := runner.lastInput()
	// The plan must be owner-bound by the pinned resolver: ValidatePlan must
	// succeed for this exact (resolver, plan) pair.
	if err := in.Resolver.ValidatePlan(in.Plan); err != nil {
		t.Fatalf("ValidatePlan: %v", err)
	}
	// A foreign resolver over the same snapshot must reject this plan: the
	// owner identity is allocation-unique, so a separately-constructed resolver
	// cannot adopt a plan it did not issue.
	source, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	foreign, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if err := foreign.ValidatePlan(in.Plan); !errors.Is(err, routing.ErrInvalidPlan) {
		t.Fatalf("foreign ValidatePlan err = %v, want ErrInvalidPlan", err)
	}
}

// TestFacadePassesPinnedResolverAndPlanToRunner verifies the runner receives a
// resolver that can Prepare the first plan candidate without error, proving the
// pinned snapshot, resolver, and owner-bound plan are mutually consistent.
func TestFacadePassesPinnedResolverAndPlanToRunner(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := runner.lastInput()
	if len(in.Plan.Candidates) == 0 {
		t.Fatal("plan has no candidates")
	}
	prepared, perr := in.Resolver.Prepare(in.Plan.Candidates[0])
	if perr != nil {
		t.Fatalf("Prepare: %v", perr)
	}
	if prepared.Revision != in.Plan.Revision || prepared.Generation != in.Plan.Generation {
		t.Fatalf("prepared revision/generation = %q/%d, plan = %q/%d", prepared.Revision, prepared.Generation, in.Plan.Revision, in.Plan.Generation)
	}
	// The protocol filter produced a chat-protocol candidate.
	if prepared.Target.Protocol != adapter.ProtocolOpenAIChat {
		t.Fatalf("protocol = %q, want openai_chat", prepared.Target.Protocol)
	}
}

func TestFacadeTypedNilCredentialsFailsClosed(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	// A typed-nil *staticCredentials wrapped in the CredentialResolver
	// interface is an injected-but-broken dependency. It must fail closed with
	// ErrMisconfigured before any snapshot read, routing, reservation, or Run
	// call rather than being silently normalized to a no-credentials execution.
	var typedNil *staticCredentials
	f := New(Options{Store: store, Runner: runner, Credentials: typedNil, Quarantine: noopQuarantine{}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0", runner.callCount())
	}
}

func TestFacadeNilCredentialsAdmissibleForAuthNone(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	store := buildStore(t)
	// A clean, untyped nil Credentials is admissible: AuthNone-only
	// configurations need no resolver, and the Runner resolves credentials per
	// route. The facade must not fail closed on a genuinely absent Credentials;
	// only a typed-nil injection fails closed.
	f := New(Options{Store: store, Runner: runner, Credentials: nil, Quarantine: noopQuarantine{}})
	_, err := f.Execute(context.Background(), chatRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner called %d times, want 1", runner.callCount())
	}
	if runner.lastInput().Credentials != nil {
		t.Fatal("expected credentials nil, got a resolver")
	}
}

func TestFacadeAnthropicMessageRequestRoutesToAnthropicProtocol(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{onceGate: true}
	f, _ := newFacade(t, runner)
	_, err := f.Execute(context.Background(), messageRequest("anthropic-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := runner.lastInput()
	if len(in.Plan.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(in.Plan.Candidates))
	}
	prepared, perr := in.Resolver.Prepare(in.Plan.Candidates[0])
	if perr != nil {
		t.Fatalf("Prepare: %v", perr)
	}
	if prepared.Target.Protocol != adapter.ProtocolAnthropic {
		t.Fatalf("protocol = %q, want anthropic_messages", prepared.Target.Protocol)
	}
}
