package execution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/model"
	"github.com/tokenmp/v3/services/executor/internal/quota"
)

func TestTerminalizerSameIntentIsIdempotentAndCallsPortOnce(t *testing.T) {
	t.Parallel()

	port := quota.NewMock()
	reserveForTerminalizer(t, port, "reservation-1")
	terminalizer := NewTerminalizer(port, "reservation-1")

	if err := terminalizer.Finalize(context.Background()); err != nil {
		t.Fatalf("first Finalize() error = %v", err)
	}
	if err := terminalizer.Finalize(context.Background()); err != nil {
		t.Fatalf("second Finalize() error = %v", err)
	}
	assertCalls(t, port, []quota.CallRecord{
		{Method: "Reserve", ID: "reservation-1"},
		{Method: "Finalize", ID: "reservation-1"},
	})
}

func TestTerminalizerFirstIntentWinsAndOppositeNeverCallsPort(t *testing.T) {
	t.Parallel()

	port := quota.NewMock()
	reserveForTerminalizer(t, port, "reservation-1")
	started := make(chan struct{})
	finish := make(chan struct{})
	port.SetFinalizeFn(func(_ context.Context, id string) (model.Reservation, error) {
		close(started)
		<-finish
		return model.Reservation{ID: id, Status: model.StatusFinalized}, nil
	})
	terminalizer := NewTerminalizer(port, "reservation-1")

	finalizeDone := make(chan error, 1)
	go func() { finalizeDone <- terminalizer.Finalize(context.Background()) }()
	<-started

	if err := terminalizer.Release(context.Background()); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("Release() error = %v, want %v", err, ErrTerminalConflict)
	}
	close(finish)
	if err := <-finalizeDone; err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	assertCalls(t, port, []quota.CallRecord{
		{Method: "Reserve", ID: "reservation-1"},
		{Method: "Finalize", ID: "reservation-1"},
	})
}

func TestTerminalizerFaultAfterTransitionRetainsIntent(t *testing.T) {
	t.Parallel()

	port := quota.NewMock()
	reserveForTerminalizer(t, port, "reservation-1")
	fault := errors.New("terminal response lost after transition")
	port.SetFaultHook(func(model.Reservation) error { return fault })
	terminalizer := NewTerminalizer(port, "reservation-1")

	if err := terminalizer.Finalize(context.Background()); !errors.Is(err, fault) {
		t.Fatalf("Finalize() error = %v, want %v", err, fault)
	}
	// The selected call's result is sticky: a local replay must not turn a
	// post-transition failure into a second port call.
	if err := terminalizer.Finalize(context.Background()); !errors.Is(err, fault) {
		t.Fatalf("second Finalize() error = %v, want %v", err, fault)
	}
	if err := terminalizer.Release(context.Background()); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("Release() error = %v, want %v", err, ErrTerminalConflict)
	}

	stored, err := port.Reserve(context.Background(), "reservation-1")
	if err != nil {
		t.Fatalf("Reserve() after terminal fault error = %v", err)
	}
	if stored.Status != model.StatusFinalized {
		t.Errorf("stored status = %q, want %q", stored.Status, model.StatusFinalized)
	}
	assertCalls(t, port, []quota.CallRecord{
		{Method: "Reserve", ID: "reservation-1"},
		{Method: "Finalize", ID: "reservation-1"},
		{Method: "Reserve", ID: "reservation-1"},
	})
}

func TestTerminalizerReleaseUsesRunnerProvidedCancellationIndependentCleanupContext(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	requestCtx, requestCancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-value"))
	requestCancel()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(requestCtx), time.Second)
	defer cleanupCancel()

	port := quota.NewMock()
	reserveForTerminalizer(t, port, "reservation-1")
	port.SetReleaseFn(func(ctx context.Context, id string) (model.Reservation, error) {
		if err := ctx.Err(); err != nil {
			t.Errorf("cleanup context error = %v, want nil", err)
		}
		if got := ctx.Value(contextKey{}); got != "request-value" {
			t.Errorf("cleanup context value = %v, want request-value", got)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("cleanup context has no timeout deadline")
		}
		return model.Reservation{ID: id, Status: model.StatusReleased}, nil
	})
	terminalizer := NewTerminalizer(port, "reservation-1")

	if err := terminalizer.Release(cleanupCtx); err != nil {
		t.Fatalf("Release(cleanupCtx) error = %v", err)
	}
	assertCalls(t, port, []quota.CallRecord{
		{Method: "Reserve", ID: "reservation-1"},
		{Method: "Release", ID: "reservation-1"},
	})
}

func TestTerminalizerConcurrentSameIntentCallsPortOnce(t *testing.T) {
	t.Parallel()

	port := quota.NewMock()
	reserveForTerminalizer(t, port, "reservation-1")
	terminalizer := NewTerminalizer(port, "reservation-1")

	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- terminalizer.Release(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Release() error = %v", err)
		}
	}
	assertCalls(t, port, []quota.CallRecord{
		{Method: "Reserve", ID: "reservation-1"},
		{Method: "Release", ID: "reservation-1"},
	})
}

func reserveForTerminalizer(t *testing.T, port *quota.Mock, id string) {
	t.Helper()
	if _, err := port.Reserve(context.Background(), id); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
}

func assertCalls(t *testing.T, port *quota.Mock, want []quota.CallRecord) {
	t.Helper()
	if got := port.Calls(); len(got) != len(want) {
		t.Fatalf("len(Calls()) = %d, want %d; calls = %+v", len(got), len(want), got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Calls()[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
	}
}
