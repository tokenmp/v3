package configrepo

import (
	"context"
	"testing"
)

// ContractTests runs the repository contract suite against any Port implementation.
func ContractTests(t *testing.T, newPort func(snapshot Snapshot) Port) {
	t.Helper()

	t.Run("snapshot returns stored config", func(t *testing.T) {
		t.Parallel()
		want := Snapshot{HTTPAddr: "127.0.0.1:9999", ShutdownTimeout: "30s"}
		port := newPort(want)
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got != want {
			t.Errorf("Snapshot() = %+v, want %+v", got, want)
		}
	})

	t.Run("snapshot with empty config", func(t *testing.T) {
		t.Parallel()
		port := newPort(Snapshot{})
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got != (Snapshot{}) {
			t.Errorf("Snapshot() = %+v, want empty", got)
		}
	})

	t.Run("snapshot with context canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		port := newPort(Snapshot{HTTPAddr: "addr"})
		got, err := port.Snapshot(ctx)
		// InMemory does not check context; this verifies no panic.
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.HTTPAddr != "addr" {
			t.Errorf("Snapshot() = %+v", got)
		}
	})
}
