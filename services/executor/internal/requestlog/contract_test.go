package requestlog

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ContractTests runs the repository contract suite against any Port implementation.
func ContractTests(t *testing.T, newPort func() Port) {
	t.Helper()

	t.Run("calls returns empty initially", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		got := port.Calls(context.Background())
		if len(got) != 0 {
			t.Errorf("len(Calls()) = %d, want 0", len(got))
		}
	})

	t.Run("record and retrieve single entry", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		entry := CallEntry{Method: "GET", Path: "/healthz", Timestamp: time.Now()}
		if err := port.Record(context.Background(), entry); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
		got := port.Calls(context.Background())
		if len(got) != 1 {
			t.Fatalf("len(Calls()) = %d, want 1", len(got))
		}
		if got[0].Method != entry.Method || got[0].Path != entry.Path {
			t.Errorf("Calls()[0] = %+v, want %+v", got[0], entry)
		}
	})

	t.Run("record preserves order", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		entries := []CallEntry{
			{Method: "GET", Path: "/a"},
			{Method: "POST", Path: "/b"},
			{Method: "DELETE", Path: "/c"},
		}
		for _, e := range entries {
			if err := port.Record(context.Background(), e); err != nil {
				t.Fatalf("Record() error = %v", err)
			}
		}
		got := port.Calls(context.Background())
		if len(got) != len(entries) {
			t.Fatalf("len(Calls()) = %d, want %d", len(got), len(entries))
		}
		for i, e := range entries {
			if got[i].Method != e.Method || got[i].Path != e.Path {
				t.Errorf("Calls()[%d] = %+v, want %+v", i, got[i], e)
			}
		}
	})

	t.Run("calls returns a copy", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_ = port.Record(context.Background(), CallEntry{Method: "GET", Path: "/x"})
		got1 := port.Calls(context.Background())
		got2 := port.Calls(context.Background())
		if len(got1) != len(got2) {
			t.Fatalf("len mismatch: %d vs %d", len(got1), len(got2))
		}
		// Mutating the returned slice should not affect the port.
		got1[0].Method = "MUTATED"
		got3 := port.Calls(context.Background())
		if got3[0].Method == "MUTATED" {
			t.Error("Calls() returned a reference instead of a copy")
		}
	})

	t.Run("concurrent record is safe", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = port.Record(context.Background(), CallEntry{Method: "GET", Path: "/concurrent"})
			}()
		}
		wg.Wait()
		got := port.Calls(context.Background())
		if len(got) != 100 {
			t.Errorf("len(Calls()) = %d, want 100", len(got))
		}
	})
}
