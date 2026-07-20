package configrepo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMockContract(t *testing.T) {
	ContractTests(t, func(snapshot Snapshot) Port {
		return NewMockWith(WithSnapshotResult(snapshot))
	})
}

func TestMockSnapshotFn(t *testing.T) {
	t.Parallel()

	want := Snapshot{HTTPAddr: "custom", ShutdownTimeout: "1s"}
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
	if got != want {
		t.Errorf("Snapshot() = %+v, want %+v", got, want)
	}
}

func TestMockSnapshotError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("config unavailable")
	mock := NewMockWith(WithSnapshotErr(sentinel))
	_, err := mock.Snapshot(context.Background())
	if !errors.Is(err, sentinel) {
		t.Errorf("Snapshot() error = %v, want %v", err, sentinel)
	}
}

func TestMockSettersOverride(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	mock.SetSnapshotResult(Snapshot{HTTPAddr: "first"})
	if got, err := mock.Snapshot(context.Background()); err != nil || got.HTTPAddr != "first" {
		t.Fatalf("first Snapshot() = (%+v, %v), want first", got, err)
	}

	mock.SetSnapshotResult(Snapshot{HTTPAddr: "second"})
	sentinel := errors.New("snap unavailable")
	mock.SetSnapshotErr(sentinel)
	if _, err := mock.Snapshot(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("err Snapshot() error = %v, want %v", err, sentinel)
	}

	mock.SetSnapshotErr(nil)
	if got, err := mock.Snapshot(context.Background()); err != nil || got.HTTPAddr != "second" {
		t.Fatalf("cleared Snapshot() = (%+v, %v), want second", got, err)
	}
}

// TestMockConcurrentSnapshot verifies Snapshot is safe for concurrent use when
// the mock is configured once up front and not reconfigured during the calls.
func TestMockConcurrentSnapshot(t *testing.T) {
	t.Parallel()

	want := Snapshot{HTTPAddr: ":8080", ShutdownTimeout: "15s"}
	mock := NewMockWith(WithSnapshotResult(want))

	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got, err := mock.Snapshot(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if got != want {
				errs <- fmt.Errorf("Snapshot() = %+v, want %+v", got, want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
