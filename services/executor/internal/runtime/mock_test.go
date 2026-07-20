package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMockContract(t *testing.T) {
	ContractTests(t, func(version string) Port {
		return NewMockWith(WithSnapshotResult(Snapshot{Version: version}))
	})
}

func TestMockSnapshotFn(t *testing.T) {
	t.Parallel()

	want := Snapshot{Version: "custom", Uptime: 5 * time.Second}
	var called bool
	mock := NewMockWith(WithSnapshotFn(func(_ context.Context) (Snapshot, error) {
		called = true
		return want, nil
	}))
	got, err := mock.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !called {
		t.Error("SnapshotFn not called")
	}
	if got.Version != want.Version || got.Uptime != want.Uptime {
		t.Errorf("Snapshot() = %+v, want %+v", got, want)
	}
}

func TestMockSnapshotError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("runtime unavailable")
	mock := NewMockWith(WithSnapshotErr(sentinel))
	_, err := mock.Snapshot(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("Snapshot() error = %v, want %v", err, sentinel)
	}
}

func TestMockQuarantine(t *testing.T) {
	t.Parallel()

	want := Quarantine{Target: "provider-a", Until: time.Now().Add(time.Minute).Round(0), Reason: "failed"}
	var setInput QuarantineInput
	mock := NewMockWith(
		WithGetQuarantineResult(want),
		WithSetQuarantineFn(func(_ context.Context, input QuarantineInput) error {
			setInput = input
			return nil
		}),
	)
	got, err := mock.GetQuarantine(context.Background(), want.Target)
	if err != nil {
		t.Fatalf("GetQuarantine() error = %v", err)
	}
	if got != want {
		t.Errorf("GetQuarantine() = %+v, want %+v", got, want)
	}
	input := QuarantineInput{Target: want.Target, Until: want.Until, Reason: want.Reason}
	if err := mock.SetQuarantine(context.Background(), input); err != nil {
		t.Fatalf("SetQuarantine() error = %v", err)
	}
	if setInput != input {
		t.Errorf("SetQuarantine() input = %+v, want %+v", setInput, input)
	}
}

func TestMockQuarantineErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("runtime state unavailable")
	mock := NewMockWith(WithGetQuarantineErr(sentinel), WithSetQuarantineErr(sentinel))
	if _, err := mock.GetQuarantine(context.Background(), "provider-a"); !errors.Is(err, sentinel) {
		t.Errorf("GetQuarantine() error = %v, want %v", err, sentinel)
	}
	if err := mock.SetQuarantine(context.Background(), QuarantineInput{}); !errors.Is(err, sentinel) {
		t.Errorf("SetQuarantine() error = %v, want %v", err, sentinel)
	}
}

// TestMockConcurrentSnapshotQuarantine verifies Snapshot, GetQuarantine and
// SetQuarantine are safe for concurrent use when the mock is configured once up
// front and not reconfigured during the calls.
func TestMockConcurrentSnapshotQuarantine(t *testing.T) {
	t.Parallel()

	wantSnap := Snapshot{Version: "v-concurrent", Uptime: 7 * time.Second}
	wantQuarantine := Quarantine{Target: "provider-a", Until: time.Now().Add(time.Minute).Round(0), Reason: "flaky"}
	mock := NewMockWith(
		WithSnapshotResult(wantSnap),
		WithGetQuarantineResult(wantQuarantine),
	)

	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)
	errs := make(chan error, goroutines*3)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got, err := mock.Snapshot(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if got.Version != wantSnap.Version || got.Uptime != wantSnap.Uptime {
				errs <- fmt.Errorf("Snapshot() = %+v, want %+v", got, wantSnap)
			}
		}()
		go func() {
			defer wg.Done()
			got, err := mock.GetQuarantine(context.Background(), wantQuarantine.Target)
			if err != nil {
				errs <- err
				return
			}
			if got != wantQuarantine {
				errs <- fmt.Errorf("GetQuarantine() = %+v, want %+v", got, wantQuarantine)
			}
		}()
		go func() {
			defer wg.Done()
			// With no SetQuarantineFn, SetQuarantine is a read of the static
			// error (zero here) and returns nil without mutating mock state.
			if err := mock.SetQuarantine(context.Background(), QuarantineInput{Target: wantQuarantine.Target}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestMockOverwriteBehavior verifies that reconfiguring a mock via setters
// (done sequentially, never concurrently with reads) overrides previous
// behavior.
func TestMockOverwriteBehavior(t *testing.T) {
	t.Parallel()

	mock := NewMockWith(
		WithSnapshotResult(Snapshot{Version: "v1"}),
		WithGetQuarantineResult(Quarantine{Target: "a"}),
	)

	if got, err := mock.Snapshot(context.Background()); err != nil || got.Version != "v1" {
		t.Fatalf("initial Snapshot() = (%+v, %v), want v1", got, err)
	}
	if got, err := mock.GetQuarantine(context.Background(), "a"); err != nil || got.Target != "a" {
		t.Fatalf("initial GetQuarantine() = (%+v, %v), want target a", got, err)
	}

	mock.SetSnapshotResult(Snapshot{Version: "v2"})
	mock.SetGetQuarantineResult(Quarantine{Target: "b", Reason: "overwritten"})
	snapErr := errors.New("snap unavailable")
	mock.SetSnapshotErr(snapErr)

	if _, err := mock.Snapshot(context.Background()); !errors.Is(err, snapErr) {
		t.Fatalf("overwritten Snapshot() error = %v, want %v", err, snapErr)
	}

	// Clear error to confirm the latest value wins.
	mock.SetSnapshotErr(nil)
	if got, err := mock.Snapshot(context.Background()); err != nil || got.Version != "v2" {
		t.Fatalf("post-clear Snapshot() = (%+v, %v), want v2", got, err)
	}
	if got, err := mock.GetQuarantine(context.Background(), "b"); err != nil || got.Target != "b" || got.Reason != "overwritten" {
		t.Fatalf("overwritten GetQuarantine() = (%+v, %v), want target b", got, err)
	}
}
