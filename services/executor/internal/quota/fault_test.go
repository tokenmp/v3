package quota

import (
	"context"
	"errors"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/model"
)

func TestInMemoryFaultInjection(t *testing.T) {
	t.Parallel()
	testFaultInjection(t, NewInMemory())
}

func TestMockFaultInjection(t *testing.T) {
	t.Parallel()
	testFaultInjection(t, NewMock())
}

type faultInjectingPort interface {
	Port
	Count() int
	SetFaultHook(FaultHook)
}

func testFaultInjection(t *testing.T, port faultInjectingPort) {
	t.Helper()

	fault := errors.New("simulated fault")
	port.SetFaultHook(func(r model.Reservation) error {
		if r.Status != model.StatusFinalized {
			t.Errorf("hook status = %q, want %q", r.Status, model.StatusFinalized)
		}
		if got := port.Count(); got != 1 {
			t.Errorf("Count() during hook = %d, want 1", got)
		}
		return fault
	})

	if _, err := port.Reserve(context.Background(), "r1"); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	r, err := port.Finalize(context.Background(), "r1")
	if !errors.Is(err, fault) {
		t.Fatalf("Finalize() error = %v, want %v", err, fault)
	}
	if r.Status != model.StatusFinalized {
		t.Errorf("Finalize() status = %q, want %q", r.Status, model.StatusFinalized)
	}
	if got := port.Count(); got != 1 {
		t.Errorf("Count() = %d, want 1", got)
	}

	// The terminal transition committed despite the injected post-transition fault.
	r, err = port.Finalize(context.Background(), "r1")
	if err != nil {
		t.Fatalf("Finalize() retry error = %v", err)
	}
	if r.Status != model.StatusFinalized {
		t.Errorf("Finalize() retry status = %q, want %q", r.Status, model.StatusFinalized)
	}
	if _, err := port.Release(context.Background(), "r1"); !errors.Is(err, ErrConflict) {
		t.Errorf("Release() error = %v, want %v", err, ErrConflict)
	}
}

func TestReleaseFaultAfterTransitionRetryAndFinalizeConflict(t *testing.T) {
	t.Parallel()

	for _, newPort := range []struct {
		name string
		new  func() faultInjectingPort
	}{
		{name: "in-memory", new: func() faultInjectingPort { return NewInMemory() }},
		{name: "mock", new: func() faultInjectingPort { return NewMock() }},
	} {
		t.Run(newPort.name, func(t *testing.T) {
			port := newPort.new()
			fault := errors.New("release post-transition fault")
			port.SetFaultHook(func(r model.Reservation) error {
				if r.Status != model.StatusReleased {
					t.Errorf("hook status = %q, want %q", r.Status, model.StatusReleased)
				}
				return fault
			})

			if _, err := port.Reserve(context.Background(), "r1"); err != nil {
				t.Fatalf("Reserve() error = %v", err)
			}
			r, err := port.Release(context.Background(), "r1")
			if !errors.Is(err, fault) {
				t.Fatalf("Release() error = %v, want %v", err, fault)
			}
			if r.Status != model.StatusReleased {
				t.Fatalf("Release() status = %q, want %q", r.Status, model.StatusReleased)
			}

			// A same-terminal retry bypasses the hook because the state is already committed.
			r, err = port.Release(context.Background(), "r1")
			if err != nil {
				t.Fatalf("Release() retry error = %v", err)
			}
			if r.Status != model.StatusReleased {
				t.Errorf("Release() retry status = %q, want %q", r.Status, model.StatusReleased)
			}
			if _, err := port.Finalize(context.Background(), "r1"); !errors.Is(err, ErrConflict) {
				t.Errorf("Finalize() after released state error = %v, want %v", err, ErrConflict)
			}
		})
	}
}

func TestMockCallbacksRunWithoutLock(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	mock.SetReserveFn(func(_ context.Context, _ string) (model.Reservation, error) {
		if got := mock.Count(); got != 0 {
			t.Errorf("Count() = %d, want 0", got)
		}
		return model.Reservation{ID: "r1", Status: model.StatusReserved}, nil
	})
	mock.SetFinalizeFn(func(_ context.Context, _ string) (model.Reservation, error) {
		if got := mock.Count(); got != 0 {
			t.Errorf("Count() = %d, want 0", got)
		}
		return model.Reservation{ID: "r1", Status: model.StatusFinalized}, nil
	})
	mock.SetReleaseFn(func(_ context.Context, _ string) (model.Reservation, error) {
		if got := mock.Count(); got != 0 {
			t.Errorf("Count() = %d, want 0", got)
		}
		return model.Reservation{ID: "r1", Status: model.StatusReleased}, nil
	})

	for _, call := range []struct {
		name string
		fn   func() error
	}{
		{"Reserve", func() error { _, err := mock.Reserve(context.Background(), "r1"); return err }},
		{"Finalize", func() error { _, err := mock.Finalize(context.Background(), "r1"); return err }},
		{"Release", func() error { _, err := mock.Release(context.Background(), "r1"); return err }},
	} {
		t.Run(call.name, func(t *testing.T) {
			if err := call.fn(); err != nil {
				t.Fatalf("%s() error = %v", call.name, err)
			}
		})
	}
}
