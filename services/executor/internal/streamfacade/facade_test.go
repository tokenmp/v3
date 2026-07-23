package streamfacade

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/requestid"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
	"github.com/tokenmp/v3/services/executor/internal/stream"
)

// newFacade builds a facade wired to a real store, a recording driver, and
// a no-op quarantine reader that consults state and finds nothing excluded
// (returns routing.ErrNotFound for every target). This is the safe default
// reader, distinct from a nil reader, which the facade rejects because it
// would silently bypass quarantine filtering.
func newFacade(t *testing.T, driver *recordingDriver) (*Facade, *snapshot.Store) {
	t.Helper()
	store := buildStore(t)
	opts := Options{
		Store:       store,
		Driver:      driver,
		Credentials: staticCredentials{value: []byte("call-local-secret")},
		Quarantine:  noopQuarantine{},
	}
	return New(opts), store
}

func TestFacadeNilFacadeFailsClosed(t *testing.T) {
	t.Parallel()
	var f *Facade
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
}

func TestFacadeNilQuarantineFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	// A nil Quarantine would silently bypass quarantine filtering, which is a
	// security degradation. It must fail closed with ErrMisconfigured before
	// any snapshot read, routing, reservation, or Run call.
	f := New(Options{Store: store, Driver: driver, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeTypedNilQuarantineFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	// A typed-nil Quarantine wrapped in the interface is an injected-but-broken
	// reader. It must fail closed with ErrMisconfigured rather than panicking
	// on dispatch or silently bypassing quarantine consultation.
	var typedNil *stubErrQuarantine
	f := New(Options{Store: store, Driver: driver, Credentials: staticCredentials{value: []byte("x")}, Quarantine: typedNil})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeNilStoreFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{}
	f := New(Options{Driver: driver, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeNilDriverFailsClosed(t *testing.T) {
	t.Parallel()
	store := buildStore(t)
	f := New(Options{Store: store, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
}

func TestFacadeTypedNilDriverFailsClosed(t *testing.T) {
	t.Parallel()
	store := buildStore(t)
	// A typed-nil *recordingDriver wrapped in the Driver interface must not
	// panic; it fails closed.
	var typedNil *recordingDriver
	f := New(Options{Store: store, Driver: typedNil, Credentials: staticCredentials{value: []byte("x")}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
}

func TestFacadeTrustedPrincipalRequired(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// No Principal: must fail closed before routing or execution.
	_, err := f.Execute(context.Background(), unauthenticated(chatStreamRequest("chat-model", "req-1")))
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeDisabledPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	req := chatStreamRequest("chat-model", "req-1")
	req.Principal.Status = stream.StatusDisabled
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeNonServiceRolePrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	req := chatStreamRequest("chat-model", "req-1")
	req.Principal.Role = "user"
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestFacadeEmptySubjectPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	req := chatStreamRequest("chat-model", "req-1")
	req.Principal.Subject = ""
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestFacadeNonPrintableSubjectPrincipalFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	req := chatStreamRequest("chat-model", "req-1")
	req.Principal.Subject = "injected\x00\x01"
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

func TestFacadeAdminRolePrincipalAdmitted(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	req := chatStreamRequest("chat-model", "req-1")
	req.Principal.Role = stream.RoleAdmin
	_, err := f.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

func TestFacadeInvalidProtocolFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	req := chatStreamRequest("chat-model", "req-1")
	req.Protocol = "" // absent protocol must not resolve cross-protocol routes
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, ErrInvalidProtocol) {
		t.Fatalf("err = %v, want ErrInvalidProtocol", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeEmptyRequestIDFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "   "))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeNoSnapshotFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f := New(Options{Store: &snapshot.Store{}, Driver: driver, Credentials: staticCredentials{value: []byte("x")}, Quarantine: noopQuarantine{}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrNoSnapshot) {
		t.Fatalf("err = %v, want ErrNoSnapshot", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeModelNotFoundReturnsTransport404Sentinel(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// Well-formed selector for a model that does not exist in the chat protocol.
	_, err := f.Execute(context.Background(), chatStreamRequest("no-such-model", "req-1"))
	if !errors.Is(err, stream.ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
	// The error must not carry routing detail.
	if strings.Contains(err.Error(), "no-such-model") {
		t.Fatalf("error leaked selector: %v", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeProtocolFilterOpenAIChatResolvesCrossProtocolAnthropicRoute(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// The anthropic model has only an anthropic_messages route; a chat request
	// for it now resolves cross-protocol so the Driver can perform conversion.
	_, err := f.Execute(context.Background(), chatStreamRequest("anthropic-model", "req-1"))
	if err != nil {
		t.Fatalf("err = %v, want nil (cross-protocol resolution)", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
}

func TestFacadeProtocolFilterAnthropicResolvesCrossProtocolChatRoute(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// The chat model has only an openai_chat route; an anthropic messages request
	// for it now resolves cross-protocol so the Driver can perform conversion.
	_, err := f.Execute(context.Background(), messageStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("err = %v, want nil (cross-protocol resolution)", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
}

func TestFacadeInvalidSelectorReturnsInvalidRequest(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// Two provider delimiters: syntactically invalid selector grammar.
	req := chatStreamRequest("chat-model@openai@extra", "req-1")
	_, err := f.Execute(context.Background(), req)
	if !errors.Is(err, stream.ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeQuarantineUnavailableFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	f := New(Options{
		Store:       store,
		Driver:      driver,
		Credentials: staticCredentials{value: []byte("x")},
		Quarantine:  stubErrQuarantine{err: routing.ErrQuarantineUnavailable},
	})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrRouting) {
		t.Fatalf("err = %v, want ErrRouting", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeContextCancellationPropagatesBeforeRouting(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.Execute(ctx, chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeDriverCalledExactlyOnceOnSuccess(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{
		onceGate: true,
		result:   execution.StreamResult{},
	}
	f, _ := newFacade(t, driver)
	result, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
	if result != (execution.StreamResult{}) {
		t.Fatalf("result = %#v, want zero canned result", result)
	}
}

func TestFacadeDriverCalledExactlyOnceOnFailure(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true, runErr: execution.ErrUnclassified}
	f, _ := newFacade(t, driver)
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, execution.ErrUnclassified) {
		t.Fatalf("err = %v, want ErrUnclassified", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
}

func TestFacadeDriverNotCalledOnPreExecutionFailure(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// Model-not-found is a pre-execution failure; the onceGate guarantees any
	// second call would panic, and callCount must remain zero.
	_, _ = f.Execute(context.Background(), chatStreamRequest("no-such-model", "req-1"))
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeCSPRNGReservationIDDefaultPassedToDriver(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := driver.lastInput()
	if !requestid.ValidReservationID(in.ReservationID) {
		t.Fatalf("reservation id = %q, want valid grammar", in.ReservationID)
	}
}

func TestFacadeReservationIDSourceInjection(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	f := New(Options{
		Store:          store,
		Driver:         driver,
		Credentials:    staticCredentials{value: []byte("x")},
		Quarantine:     noopQuarantine{},
		ReservationIDs: requestid.SourceFunc(func(context.Context) string { return "res_injected-valid-id1" }),
	})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := driver.lastInput().ReservationID; got != "res_injected-valid-id1" {
		t.Fatalf("reservation id = %q, want res_injected-valid-id1", got)
	}
}

func TestFacadeTypedNilReservationIDSourceFallsBackToCSPRNG(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	// A typed-nil func wrapped in the interface must not panic; the default
	// CSPRNG source is used.
	var typedNil requestid.SourceFunc
	f := New(Options{Store: store, Driver: driver, Credentials: staticCredentials{value: []byte("x")}, Quarantine: noopQuarantine{}, ReservationIDs: typedNil})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !requestid.ValidReservationID(driver.lastInput().ReservationID) {
		t.Fatalf("reservation id = %q, want CSPRNG default", driver.lastInput().ReservationID)
	}
}

func TestFacadeEmptyReservationIDFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	f := New(Options{
		Store:          store,
		Driver:         driver,
		Credentials:    staticCredentials{value: []byte("x")},
		Quarantine:     noopQuarantine{},
		ReservationIDs: requestid.SourceFunc(func(context.Context) string { return "" }),
	})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrReservationID) {
		t.Fatalf("err = %v, want ErrReservationID", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeInvalidGrammarReservationIDFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	// A source returning a short suffix fails the grammar gate (16-128 chars).
	f := New(Options{
		Store:          store,
		Driver:         driver,
		Credentials:    staticCredentials{value: []byte("x")},
		Quarantine:     noopQuarantine{},
		ReservationIDs: requestid.SourceFunc(func(context.Context) string { return "res_short" }),
	})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrReservationID) {
		t.Fatalf("err = %v, want ErrReservationID", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadePinsSnapshotPerRequest(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: false}
	f, store := newFacade(t, driver)

	// First request pins generation 1.
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute gen 1: %v", err)
	}
	if got := driver.lastInput().Plan.Generation; got != 1 {
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

	_, err = f.Execute(context.Background(), chatStreamRequest("chat-model", "req-2"))
	if err != nil {
		t.Fatalf("Execute gen 2: %v", err)
	}
	if got := driver.lastInput().Plan.Generation; got != 2 {
		t.Fatalf("gen 2 plan generation = %d, want 2", got)
	}
	if got := driver.lastInput().Plan.Revision; got != "facade-revision-2" {
		t.Fatalf("gen 2 plan revision = %q, want facade-revision-2", got)
	}
}

func TestFacadePreservesResolverOwnedPlan(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, store := newFacade(t, driver)
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := driver.lastInput()
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

// TestFacadePassesPinnedResolverAndPlanToDriver verifies the driver receives a
// resolver that can Prepare the first plan candidate without error, proving the
// pinned snapshot, resolver, and owner-bound plan are mutually consistent.
func TestFacadeTypedNilCredentialsFailsClosed(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	// A typed-nil *staticCredentials wrapped in the CredentialResolver
	// interface is an injected-but-broken dependency. It must fail closed with
	// ErrMisconfigured before any snapshot read, routing, reservation, or Run
	// call rather than being silently normalized to a no-credentials execution.
	var typedNil *staticCredentials
	f := New(Options{Store: store, Driver: driver, Credentials: typedNil, Quarantine: noopQuarantine{}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("err = %v, want ErrMisconfigured", err)
	}
	if driver.callCount() != 0 {
		t.Fatalf("driver called %d times, want 0", driver.callCount())
	}
}

func TestFacadeNilCredentialsAdmissibleForAuthNone(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	store := buildStore(t)
	// A clean, untyped nil Credentials is admissible: AuthNone-only
	// configurations need no resolver, and the Driver resolves credentials per
	// route. The facade must not fail closed on a genuinely absent Credentials;
	// only a typed-nil injection fails closed.
	f := New(Options{Store: store, Driver: driver, Credentials: nil, Quarantine: noopQuarantine{}})
	_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
	if driver.lastInput().Credentials != nil {
		t.Fatal("expected credentials nil, got a resolver")
	}
}

func TestFacadeAnthropicMessageRequestRoutesToAnthropicProtocol(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	_, err := f.Execute(context.Background(), messageStreamRequest("anthropic-model", "req-1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	in := driver.lastInput()
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

type typedNilSink struct{}

func (*typedNilSink) Commit(context.Context, []sdk.StreamEvent) error   { return nil }
func (*typedNilSink) WriteEvent(context.Context, sdk.StreamEvent) error { return nil }
func (*typedNilSink) Flush(context.Context) error                       { return nil }

func TestFacadeNilOrTypedNilSinkFailsClosed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		sink execution.ProtocolSink
	}{
		{name: "nil"},
		{name: "typed nil", sink: (*typedNilSink)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := &recordingDriver{onceGate: true}
			f, _ := newFacade(t, driver)
			req := chatStreamRequest("chat-model", "req-1")
			req.Sink = tc.sink
			_, err := f.Execute(context.Background(), req)
			if !errors.Is(err, ErrMisconfigured) {
				t.Fatalf("err = %v, want ErrMisconfigured", err)
			}
			if driver.callCount() != 0 {
				t.Fatalf("driver called %d times, want 0", driver.callCount())
			}
		})
	}
}

func TestFacadeConcurrentRequestsEachCallDriverOnce(t *testing.T) {
	driver := &recordingDriver{}
	f, _ := newFacade(t, driver)
	const requests = 32
	errs := make(chan error, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.Execute(context.Background(), chatStreamRequest("chat-model", "req-concurrent"))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Execute: %v", err)
		}
	}
	if got := driver.callCount(); got != requests {
		t.Fatalf("driver calls = %d, want %d", got, requests)
	}
}

func TestFacadeCrossProtocolResolvesWhenSameProtocolNotFound(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// The anthropic model has only an anthropic_messages route. A chat stream
	// request for it should resolve cross-protocol.
	_, err := f.Execute(context.Background(), chatStreamRequest("anthropic-model", "req-cross-1"))
	if err != nil {
		t.Fatalf("err = %v, want nil (cross-protocol resolution)", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
	input := driver.lastInput()
	if input.QuotaIdentity.Protocol != "openai_chat" {
		t.Fatalf("request protocol = %q, want openai_chat", input.QuotaIdentity.Protocol)
	}
	if len(input.Plan.Candidates) == 0 {
		t.Fatal("no candidates in plan")
	}
	if input.Plan.Candidates[0].Protocol != adapter.ProtocolAnthropic {
		t.Fatalf("route protocol = %q, want anthropic_messages", input.Plan.Candidates[0].Protocol)
	}
}

func TestFacadeCrossProtocolReverseResolvesWhenSameProtocolNotFound(t *testing.T) {
	t.Parallel()
	driver := &recordingDriver{onceGate: true}
	f, _ := newFacade(t, driver)
	// The chat model has only an openai_chat route. An anthropic messages stream
	// request for it should resolve cross-protocol.
	_, err := f.Execute(context.Background(), messageStreamRequest("chat-model", "req-cross-rev-1"))
	if err != nil {
		t.Fatalf("err = %v, want nil (cross-protocol resolution)", err)
	}
	if driver.callCount() != 1 {
		t.Fatalf("driver called %d times, want 1", driver.callCount())
	}
	input := driver.lastInput()
	if input.QuotaIdentity.Protocol != "anthropic_messages" {
		t.Fatalf("request protocol = %q, want anthropic_messages", input.QuotaIdentity.Protocol)
	}
	if len(input.Plan.Candidates) == 0 {
		t.Fatal("no candidates in plan")
	}
	if input.Plan.Candidates[0].Protocol != adapter.ProtocolOpenAIChat {
		t.Fatalf("route protocol = %q, want openai_chat", input.Plan.Candidates[0].Protocol)
	}
}
