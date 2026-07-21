package quarantinebridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/runtime"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// fixedClock is a deterministic routing.Clock for resolver integration tests.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func testNow() time.Time { return time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC) }

// setQuarantine stores an active quarantine for the single dimension of target
// on mem, using the bridge's own encoding so the read path round-trips.
func setQuarantine(t *testing.T, mem *runtime.InMemory, target routing.QuarantineTarget, until time.Time) {
	t.Helper()
	targets := dimensionTargets(target)
	if len(targets) != 1 {
		t.Fatalf("setQuarantine expects a single-dimension target, got %d", len(targets))
	}
	if err := mem.SetQuarantine(context.Background(), runtime.QuarantineInput{
		Target: targets[0],
		Until:  until,
		Reason: "test",
	}); err != nil {
		t.Fatalf("SetQuarantine: %v", err)
	}
}

func TestDimensionTargetsAreUnambiguousAndEmptyRemainsEmpty(t *testing.T) {
	cases := []struct {
		name   string
		target routing.QuarantineTarget
		want   []runtime.RuntimeTarget
	}{
		{"model only", routing.QuarantineTarget{ModelID: "m"}, []runtime.RuntimeTarget{"model:m"}},
		{"provider only", routing.QuarantineTarget{ProviderID: "p"}, []runtime.RuntimeTarget{"provider:p"}},
		{"route only", routing.QuarantineTarget{RouteID: "r"}, []runtime.RuntimeTarget{"route:r"}},
		{"credential only", routing.QuarantineTarget{CredentialID: "c"}, []runtime.RuntimeTarget{"credential:c"}},
		{"all empty", routing.QuarantineTarget{}, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := dimensionTargets(test.target)
			if !equalTargets(got, test.want) {
				t.Fatalf("dimensionTargets(%+v) = %v, want %v", test.target, got, test.want)
			}
		})
	}

	// Overlapping IDs across dimensions must map to distinct runtime targets so
	// a model and a route sharing the value "primary" cannot collide.
	model := dimensionTargets(routing.QuarantineTarget{ModelID: "primary"})[0]
	route := dimensionTargets(routing.QuarantineTarget{RouteID: "primary"})[0]
	if model == route {
		t.Fatalf("model and route targets collide: %q", model)
	}

	// A combined target maps to one runtime target per non-empty dimension, in
	// the fixed order model, provider, route, credential.
	combined := dimensionTargets(routing.QuarantineTarget{
		ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c",
	})
	want := []runtime.RuntimeTarget{"model:m", "provider:p", "route:r", "credential:c"}
	if !equalTargets(combined, want) {
		t.Fatalf("combined dimensionTargets = %v, want %v", combined, want)
	}
}

func equalTargets(a, b []runtime.RuntimeTarget) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBridgeEachDimensionRoundTripsViaInMemory(t *testing.T) {
	now := testNow()
	until := now.Add(time.Minute)
	for _, dim := range []routing.QuarantineTarget{
		{ModelID: "m"},
		{ProviderID: "p"},
		{RouteID: "r"},
		{CredentialID: "c"},
	} {
		t.Run(fmt.Sprintf("%+v", dim), func(t *testing.T) {
			mem := runtime.NewInMemory("v")
			setQuarantine(t, mem, dim, until)
			bridge := New(mem)
			got, err := bridge.GetQuarantine(context.Background(), dim)
			if err != nil {
				t.Fatalf("GetQuarantine: %v", err)
			}
			if !got.Until.Equal(until) {
				t.Fatalf("Until = %v, want %v", got.Until, until)
			}
		})
	}
}

func TestBridgeCombinedTargetReturnsLatestExpiry(t *testing.T) {
	now := testNow()
	mem := runtime.NewInMemory("v")
	// Two dimensions quarantined with different expiries; one not quarantined.
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "m"}, now.Add(30*time.Second))
	setQuarantine(t, mem, routing.QuarantineTarget{CredentialID: "c"}, now.Add(2*time.Minute))
	bridge := New(mem)
	combined := routing.QuarantineTarget{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c"}
	got, err := bridge.GetQuarantine(context.Background(), combined)
	if err != nil {
		t.Fatalf("GetQuarantine: %v", err)
	}
	if want := now.Add(2 * time.Minute); !got.Until.Equal(want) {
		t.Fatalf("combined Until = %v, want latest %v", got.Until, want)
	}
}

func TestBridgeEmptyTargetReturnsNotFoundWithoutPortCall(t *testing.T) {
	mock := runtime.NewMockWith(runtime.WithGetQuarantineFn(func(_ context.Context, _ runtime.RuntimeTarget) (runtime.Quarantine, error) {
		t.Fatal("port must not be called for an empty target")
		return runtime.Quarantine{}, nil
	}))
	bridge := New(mock)
	if _, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{}); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("empty target error = %v, want routing.ErrNotFound", err)
	}
}

func TestBridgeNotFoundPropagatesAsRoutingNotFound(t *testing.T) {
	mem := runtime.NewInMemory("v")
	bridge := New(mem)
	if _, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "absent"}); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("absent target error = %v, want routing.ErrNotFound", err)
	}
	if !errors.Is(routing.ErrNotFound, runtime.ErrNotFound) {
		t.Fatalf("routing.ErrNotFound must alias runtime.ErrNotFound")
	}
}

func TestBridgeInMemoryActiveInactiveExpired(t *testing.T) {
	now := testNow()
	mem := runtime.NewInMemory("v")
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "active"}, now.Add(time.Minute))   // active
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "expired"}, now.Add(-time.Minute)) // expired
	// "inactive" is never set.
	bridge := New(mem)

	if got, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "active"}); err != nil {
		t.Fatalf("active error = %v", err)
	} else if !got.Until.After(now) {
		t.Fatalf("active Until = %v, want after %v", got.Until, now)
	}

	if got, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "expired"}); err != nil {
		t.Fatalf("expired error = %v", err)
	} else if got.Until.After(now) {
		t.Fatalf("expired Until = %v, want before/equal %v", got.Until, now)
	}

	if _, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "inactive"}); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("inactive error = %v, want routing.ErrNotFound", err)
	}
}

func TestBridgeNilAndTypedNilPortFailsClosed(t *testing.T) {
	if _, err := New(nil).GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, routing.ErrQuarantineUnavailable) {
		t.Fatalf("nil port error = %v, want ErrQuarantineUnavailable", err)
	}
	// Typed-nil pointer implementing runtime.Port.
	var typedNil *runtime.InMemory
	bridge := New(typedNil)
	if _, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, routing.ErrQuarantineUnavailable) {
		t.Fatalf("typed-nil port error = %v, want ErrQuarantineUnavailable", err)
	}
	// Typed-nil Mock.
	var typedNilMock *runtime.Mock
	if _, err := New(typedNilMock).GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, routing.ErrQuarantineUnavailable) {
		t.Fatalf("typed-nil mock error = %v, want ErrQuarantineUnavailable", err)
	}
	// Nil Bridge receiver also fails closed.
	var nilBridge *Bridge
	if _, err := nilBridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, routing.ErrQuarantineUnavailable) {
		t.Fatalf("nil Bridge error = %v, want ErrQuarantineUnavailable", err)
	}
}

func TestBridgeOrdinaryErrorFailsClosedWithoutLeakage(t *testing.T) {
	secret := errors.New("boom: db connection to 10.0.0.5:5432 with token s3cr3t")
	mock := runtime.NewMockWith(runtime.WithGetQuarantineErr(secret))
	bridge := New(mock)
	_, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"})
	if !errors.Is(err, routing.ErrQuarantineUnavailable) {
		t.Fatalf("ordinary error = %v, want ErrQuarantineUnavailable", err)
	}
	if errors.Is(err, secret) {
		t.Fatalf("ordinary error leaked raw sentinel: %v", err)
	}
	msg := err.Error()
	for _, needle := range []string{"boom", "10.0.0.5", "5432", "s3cr3t", "token"} {
		if strings.Contains(msg, needle) {
			t.Fatalf("ordinary error leaked raw text %q in %q", needle, msg)
		}
	}
}

func TestBridgeWrappedNotFoundStillTreatedAsNotFound(t *testing.T) {
	mock := runtime.NewMockWith(runtime.WithGetQuarantineErr(fmt.Errorf("wrap: %w", runtime.ErrNotFound)))
	bridge := New(mock)
	if _, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("wrapped NotFound error = %v, want routing.ErrNotFound", err)
	}
}

func TestBridgeContextCancellationNormalizedToSentinel(t *testing.T) {
	// A port-reported context error — bare or wrapped — must be normalized to
	// the exact context sentinel. errors.Is must still recognize it, the raw
	// upstream marker must be absent from err.Error(), and the returned error
	// must be the exact sentinel (never a wrapper, never ErrQuarantineUnavailable).
	for _, portErr := range []error{
		context.Canceled,
		context.DeadlineExceeded,
		fmt.Errorf("quarantine read marker: %w", context.Canceled),
		fmt.Errorf("upstream deadline marker: %w", context.DeadlineExceeded),
	} {
		mock := runtime.NewMockWith(runtime.WithGetQuarantineErr(portErr))
		bridge := New(mock)
		_, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"})

		wantSentinel := context.Canceled
		if errors.Is(portErr, context.DeadlineExceeded) {
			wantSentinel = context.DeadlineExceeded
		}
		if !errors.Is(err, wantSentinel) {
			t.Fatalf("portErr=%v: error = %v, want errors.Is(%v)", portErr, err, wantSentinel)
		}
		// The exact sentinel must be returned: no wrapper preserved.
		if err != wantSentinel {
			t.Fatalf("portErr=%v: error = %v, want exact sentinel %v", portErr, err, wantSentinel)
		}
		// Never downgraded to ErrQuarantineUnavailable.
		if errors.Is(err, routing.ErrQuarantineUnavailable) {
			t.Fatalf("portErr=%v: context error was converted to ErrQuarantineUnavailable", portErr)
		}
		// The raw upstream marker text must not survive in err.Error().
		msg := err.Error()
		for _, needle := range []string{"quarantine read marker", "upstream deadline marker", ":", "%w"} {
			if strings.Contains(msg, needle) {
				t.Fatalf("portErr=%v: normalized error leaked raw marker %q in %q", portErr, needle, msg)
			}
		}
	}
}

func TestBridgeContextErrorCtxErrPrecedence(t *testing.T) {
	// When the port returns a wrapped context error but ctx itself is also now
	// done, ctx.Err() is authoritative and must be returned exactly — even if it
	// is a different sentinel than the port's wrapped variant. The port's raw
	// marker must not survive.
	//
	// Port cancels the shared ctx during the call and returns a wrapped
	// DeadlineExceeded; ctx.Err() (context.Canceled) must win.
	ctx, cancel := context.WithCancel(context.Background())
	mock := runtime.NewMockWith(runtime.WithGetQuarantineFn(func(_ context.Context, _ runtime.RuntimeTarget) (runtime.Quarantine, error) {
		cancel() // make ctx done during the port call
		return runtime.Quarantine{}, fmt.Errorf("upstream deadline marker: %w", context.DeadlineExceeded)
	}))
	bridge := New(mock)
	_, err := bridge.GetQuarantine(ctx, routing.QuarantineTarget{ModelID: "m"})
	if err != context.Canceled {
		t.Fatalf("error = %v, want exact context.Canceled (ctx.Err precedence)", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx.Err precedence lost: error matched DeadlineExceeded: %v", err)
	}
	if strings.Contains(err.Error(), "upstream deadline marker") {
		t.Fatalf("ctx.Err error leaked port marker: %q", err.Error())
	}

	// Reverse: port returns a wrapped Canceled, but ctx has an expired
	// deadline. ctx.Err() (context.DeadlineExceeded) must win.
	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	dcancel()
	mock2 := runtime.NewMockWith(runtime.WithGetQuarantineFn(func(context.Context, runtime.RuntimeTarget) (runtime.Quarantine, error) {
		return runtime.Quarantine{}, fmt.Errorf("upstream cancel marker: %w", context.Canceled)
	}))
	_, err2 := New(mock2).GetQuarantine(dctx, routing.QuarantineTarget{ModelID: "m"})
	if err2 != context.DeadlineExceeded {
		t.Fatalf("error = %v, want exact context.DeadlineExceeded (ctx.Err precedence)", err2)
	}
	if errors.Is(err2, context.Canceled) {
		t.Fatalf("ctx.Err precedence lost: error matched Canceled: %v", err2)
	}
	if strings.Contains(err2.Error(), "upstream cancel marker") {
		t.Fatalf("ctx.Err error leaked port marker: %q", err2.Error())
	}
}

func TestBridgeContextCancellationBeatsCombinedDimensions(t *testing.T) {
	now := testNow()
	mem := runtime.NewInMemory("v")
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "m"}, now.Add(time.Minute))
	mock := runtime.NewMockWith(runtime.WithGetQuarantineFn(func(ctx context.Context, target runtime.RuntimeTarget) (runtime.Quarantine, error) {
		if target == "model:m" {
			return runtime.Quarantine{Target: target, Until: now.Add(time.Minute)}, nil
		}
		return runtime.Quarantine{}, context.Canceled
	}))
	bridge := New(mock)
	_, err := bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m", RouteID: "r"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("combined error = %v, want context.Canceled", err)
	}
}

func TestBridgeDirectContextCanceledFailsClosedBeforePortAccess(t *testing.T) {
	// A directly cancelled context must fail closed before any port access,
	// even when the port is a real InMemory that ignores ctx and holds data.
	// The exact context.Canceled error must be preserved.
	now := testNow()
	mem := runtime.NewInMemory("v")
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "m"}, now.Add(time.Minute))
	bridge := New(mem)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := bridge.GetQuarantine(ctx, routing.QuarantineTarget{ModelID: "m"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx error = %v, want errors.Is(context.Canceled)", err)
	}
	if got.Until != (routing.Quarantine{}).Until {
		t.Fatalf("cancelled ctx returned non-zero quarantine: %+v", got)
	}
	// Deadline path: an already-expired deadline must surface the exact error.
	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	dcancel()
	if _, err := bridge.GetQuarantine(dctx, routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline error = %v, want errors.Is(context.DeadlineExceeded)", err)
	}
}

func TestBridgeDirectContextCanceledNeverCallsPort(t *testing.T) {
	// The port function must never run when ctx is already cancelled.
	called := false
	mock := runtime.NewMockWith(runtime.WithGetQuarantineFn(func(_ context.Context, _ runtime.RuntimeTarget) (runtime.Quarantine, error) {
		called = true
		return runtime.Quarantine{}, nil
	}))
	bridge := New(mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, target := range []routing.QuarantineTarget{
		{ModelID: "m"},
		{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c"},
	} {
		if _, err := bridge.GetQuarantine(ctx, target); !errors.Is(err, context.Canceled) {
			t.Fatalf("target %+v error = %v, want context.Canceled", target, err)
		}
	}
	if called {
		t.Fatalf("port GetQuarantine was called despite cancelled ctx")
	}

	// Deadline-exceeded ctx must also short-circuit before the port.
	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	dcancel()
	if _, err := bridge.GetQuarantine(dctx, routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v, want context.DeadlineExceeded", err)
	}
	if called {
		t.Fatalf("port GetQuarantine was called despite expired deadline")
	}
}

func TestBridgeDirectContextCanceledBeatsNilPort(t *testing.T) {
	// A cancelled context must preserve the exact context error even when the
	// port is nil, rather than surfacing ErrQuarantineUnavailable.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(nil).GetQuarantine(ctx, routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil port + cancelled ctx error = %v, want context.Canceled", err)
	}
	var nilBridge *Bridge
	if _, err := nilBridge.GetQuarantine(ctx, routing.QuarantineTarget{ModelID: "m"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil bridge + cancelled ctx error = %v, want context.Canceled", err)
	}
}

func TestBridgeConcurrentReadsAreSafe(t *testing.T) {
	mem := runtime.NewInMemory("v")
	now := testNow()
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "m"}, now.Add(time.Minute))
	bridge := New(mem)
	const goroutines = 64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = bridge.GetQuarantine(context.Background(), routing.QuarantineTarget{ModelID: "m"})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error = %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Resolver integration: the bridge wired into a real routing.Resolver.
// ---------------------------------------------------------------------------

func bridgeTestSnapshot(t *testing.T) *snapshot.CompiledSnapshot {
	t.Helper()
	config := &snapshot.CompiledConfig{
		Revision:     "bridge",
		AutoModelIDs: []string{"m"},
		Models: map[string]adapter.CompiledModel{
			"m": {ID: "m"},
		},
		Providers: map[string]adapter.CompiledProvider{
			"p": {ID: "p", Selector: "openai"},
		},
		Adapters: map[string]adapter.CompiledAdapter{
			"a": {ID: "a", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Routes: []adapter.CompiledRoute{
			{ID: "r", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "up", Priority: 10, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CompiledCredential{{ID: "c-1", Enabled: true, Priority: 1}}},
		},
	}
	frozen, err := snapshot.NewCompiledSnapshot("bridge", config, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	return frozen
}

func bridgeResolver(t *testing.T, reader routing.QuarantineReader) *routing.Resolver {
	t.Helper()
	r, err := routing.NewResolver(bridgeTestSnapshot(t), reader, fixedClock{now: testNow()})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func TestResolverWithBridgeQuarantineDimensionsExcludeCandidates(t *testing.T) {
	for _, dim := range []routing.QuarantineTarget{
		{ModelID: "m"},
		{ProviderID: "p"},
		{RouteID: "r"},
		{CredentialID: "c-1"},
	} {
		t.Run(fmt.Sprintf("active_%v", dim), func(t *testing.T) {
			mem := runtime.NewInMemory("v")
			setQuarantine(t, mem, dim, testNow().Add(time.Minute))
			_, err := bridgeResolver(t, New(mem)).Resolve(context.Background(), routing.Selector{Model: "m"})
			if !errors.Is(err, routing.ErrNotFound) {
				t.Fatalf("Resolve with active %v error = %v, want ErrNotFound", dim, err)
			}
		})
	}
}

func TestResolverWithBridgeExpiredAndInactiveKeepCandidates(t *testing.T) {
	// Expired quarantine on the credential; candidate must still resolve.
	mem := runtime.NewInMemory("v")
	setQuarantine(t, mem, routing.QuarantineTarget{CredentialID: "c-1"}, testNow().Add(-time.Minute))
	plan, err := bridgeResolver(t, New(mem)).Resolve(context.Background(), routing.Selector{Model: "m"})
	if err != nil {
		t.Fatalf("Resolve expired error = %v", err)
	}
	if len(plan.Candidates) != 1 || plan.Candidates[0].Credential.ID != "c-1" {
		t.Fatalf("expired quarantine candidates = %+v, want one c-1 candidate", plan.Candidates)
	}

	// A quarantine on a non-matching model must not exclude the candidate.
	other := runtime.NewInMemory("v")
	setQuarantine(t, other, routing.QuarantineTarget{ModelID: "other"}, testNow().Add(time.Minute))
	plan, err = bridgeResolver(t, New(other)).Resolve(context.Background(), routing.Selector{Model: "m"})
	if err != nil {
		t.Fatalf("Resolve non-matching error = %v", err)
	}
	if len(plan.Candidates) != 1 {
		t.Fatalf("non-matching quarantine candidates = %d, want 1", len(plan.Candidates))
	}
}

func TestResolverWithBridgePortErrorFailsClosed(t *testing.T) {
	mock := runtime.NewMockWith(runtime.WithGetQuarantineErr(errors.New("runtime port exploded: secret=abc")))
	_, err := bridgeResolver(t, New(mock)).Resolve(context.Background(), routing.Selector{Model: "m"})
	if !errors.Is(err, routing.ErrQuarantineUnavailable) {
		t.Fatalf("Resolve port error = %v, want ErrQuarantineUnavailable", err)
	}
	if strings.Contains(err.Error(), "exploded") || strings.Contains(err.Error(), "secret=abc") {
		t.Fatalf("resolver leaked raw port error: %v", err)
	}
}

func TestResolverWithBridgeNoCandidatesReturnsNotFound(t *testing.T) {
	mem := runtime.NewInMemory("v")
	// Quarantine the only route's model so no candidate survives.
	setQuarantine(t, mem, routing.QuarantineTarget{ModelID: "m"}, testNow().Add(time.Minute))
	_, err := bridgeResolver(t, New(mem)).Resolve(context.Background(), routing.Selector{Model: "m"})
	if !errors.Is(err, routing.ErrNotFound) {
		t.Fatalf("Resolve no-candidates error = %v, want ErrNotFound", err)
	}
}

func TestResolverWithBridgeContextCancelled(t *testing.T) {
	mem := runtime.NewInMemory("v")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := bridgeResolver(t, New(mem)).Resolve(ctx, routing.Selector{Model: "m"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve cancelled error = %v, want context.Canceled", err)
	}
}

func TestResolverWithBridgeConcurrentResolve(t *testing.T) {
	mem := runtime.NewInMemory("v")
	resolver := bridgeResolver(t, New(mem))
	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make([]error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = resolver.Resolve(context.Background(), routing.Selector{Model: "m"})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error = %v", i, err)
		}
	}
}
