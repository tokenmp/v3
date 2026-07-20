package requestlog

import (
	"context"
	"testing"
)

func TestInMemoryContract(t *testing.T) {
	ContractTests(t, func() Port { return NewInMemory() })
}

// TestInMemoryCallsReturnsCopy verifies that InMemory.Calls returns a
// defensive copy of the internal record slice so callers cannot alias or
// mutate the store's internal state through the read API.
func TestInMemoryCallsReturnsCopy(t *testing.T) {
	t.Parallel()

	port := NewInMemory()
	_ = port.Record(context.Background(), CallEntry{Method: "GET", Path: "/a"})
	_ = port.Record(context.Background(), CallEntry{Method: "POST", Path: "/b"})

	first := port.Calls(context.Background())
	second := port.Calls(context.Background())
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("len(Calls()) = %d, %d, want 2, 2", len(first), len(second))
	}
	// Mutate the first returned copy; the store and subsequent reads must be
	// unaffected.
	first[0].Method = "MUTATED"
	first[1].Path = "MUTATED"
	if got := port.Calls(context.Background()); got[0].Method == "MUTATED" || got[1].Path == "MUTATED" {
		t.Error("Calls() returned a reference to the internal slice")
	}
	if second[0].Method == "MUTATED" || second[1].Path == "MUTATED" {
		t.Error("Calls() returned slices that share a backing array")
	}
}
