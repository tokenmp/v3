package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ContractTests runs the repository contract suite against any Port implementation.
// These tests verify basic behavior that works for both static and dynamic snapshots.
func ContractTests(t *testing.T, newPort func(version string) Port) {
	t.Helper()

	t.Run("snapshot returns version", func(t *testing.T) {
		t.Parallel()
		port := newPort("v1.0.0")
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.Version != "v1.0.0" {
			t.Errorf("Version = %q, want %q", got.Version, "v1.0.0")
		}
	})

	t.Run("snapshot with empty version", func(t *testing.T) {
		t.Parallel()
		port := newPort("")
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.Version != "" {
			t.Errorf("Version = %q, want empty", got.Version)
		}
	})

	t.Run("snapshot with context canceled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		port := newPort("v3.0.0")
		got, err := port.Snapshot(ctx)
		// InMemory does not check context; this verifies no panic.
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.Version != "v3.0.0" {
			t.Errorf("Version = %q, want %q", got.Version, "v3.0.0")
		}
	})
}

// ContractTestsRealtime runs tests that require real time behavior.
// Only run against InMemory, not Mock.
func ContractTestsRealtime(t *testing.T, newPort func(version string) Port) {
	t.Helper()

	t.Run("uptime is positive", func(t *testing.T) {
		t.Parallel()
		port := newPort("v0.1.0")
		time.Sleep(time.Millisecond)
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.Uptime <= 0 {
			t.Errorf("Uptime = %v, want positive", got.Uptime)
		}
	})

	t.Run("start time is in the past", func(t *testing.T) {
		t.Parallel()
		before := time.Now()
		port := newPort("v2.0.0")
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.StartTime.Before(before) || got.StartTime.After(time.Now()) {
			t.Errorf("StartTime = %v, want between %v and %v", got.StartTime, before, time.Now())
		}
	})
}

// ContractTestsInMemory runs InMemory-specific tests.
func ContractTestsInMemory(t *testing.T) {
	t.Helper()

	t.Run("quarantine get and set", func(t *testing.T) {
		t.Parallel()
		port := NewInMemory("v1")
		input := QuarantineInput{
			Target: "provider-a",
			Until:  time.Now().Add(time.Minute).Round(0),
			Reason: "upstream unavailable",
		}
		if err := port.SetQuarantine(context.Background(), input); err != nil {
			t.Fatalf("SetQuarantine() error = %v", err)
		}
		got, err := port.GetQuarantine(context.Background(), input.Target)
		if err != nil {
			t.Fatalf("GetQuarantine() error = %v", err)
		}
		want := Quarantine{Target: input.Target, Until: input.Until, Reason: input.Reason}
		if got != want {
			t.Errorf("GetQuarantine() = %+v, want %+v", got, want)
		}
	})

	t.Run("set quarantine overwrites same target", func(t *testing.T) {
		t.Parallel()
		port := NewInMemory("v1")
		target := RuntimeTarget("provider-a")
		first := QuarantineInput{
			Target: target,
			Until:  time.Now().Add(time.Minute).Round(0),
			Reason: "first failure",
		}
		second := QuarantineInput{
			Target: target,
			Until:  time.Now().Add(2 * time.Minute).Round(0),
			Reason: "latest failure",
		}
		if err := port.SetQuarantine(context.Background(), first); err != nil {
			t.Fatalf("SetQuarantine(first) error = %v", err)
		}
		if err := port.SetQuarantine(context.Background(), second); err != nil {
			t.Fatalf("SetQuarantine(second) error = %v", err)
		}

		got, err := port.GetQuarantine(context.Background(), target)
		if err != nil {
			t.Fatalf("GetQuarantine() error = %v", err)
		}
		want := Quarantine{Target: second.Target, Until: second.Until, Reason: second.Reason}
		if got != want {
			t.Errorf("GetQuarantine() = %+v, want latest value %+v", got, want)
		}
	})

	t.Run("unknown quarantine returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		port := NewInMemory("v1")
		_, err := port.GetQuarantine(context.Background(), "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("GetQuarantine() error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("concurrent quarantine access", func(t *testing.T) {
		t.Parallel()
		port := NewInMemory("v1")
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = port.SetQuarantine(context.Background(), QuarantineInput{Target: "target"})
			}()
			go func() {
				defer wg.Done()
				_, _ = port.GetQuarantine(context.Background(), "target")
			}()
		}
		wg.Wait()
	})

	t.Run("set version", func(t *testing.T) {
		t.Parallel()
		port := NewInMemory("old")
		port.SetVersion("new")
		got, err := port.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if got.Version != "new" {
			t.Errorf("Version = %q, want %q", got.Version, "new")
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		t.Parallel()
		port := NewInMemory("v1")
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				_, _ = port.Snapshot(context.Background())
			}()
			go func() {
				defer wg.Done()
				port.SetVersion("updated")
			}()
		}
		wg.Wait()
	})
}
