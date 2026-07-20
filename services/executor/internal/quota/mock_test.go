package quota

import (
	"context"
	"testing"
)

func TestMockContract(t *testing.T) {
	ContractTests(t, func() Port { return NewMock() })
}

func TestMockCallRecording(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	_, _ = mock.Reserve(context.Background(), "r1")
	_, _ = mock.Finalize(context.Background(), "r1")
	_, _ = mock.Release(context.Background(), "r2") // conflict

	want := []CallRecord{
		{Method: "Reserve", ID: "r1"},
		{Method: "Finalize", ID: "r1"},
		{Method: "Release", ID: "r2"},
	}
	calls := mock.Calls()
	if len(calls) != len(want) {
		t.Fatalf("len(Calls()) = %d, want %d", len(calls), len(want))
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("Calls()[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestMockCallsReturnsCopy verifies that Calls() returns a defensive copy so
// callers cannot alias or mutate the mock's internal record slice.
func TestMockCallsReturnsCopy(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	_, _ = mock.Reserve(context.Background(), "r1")
	_, _ = mock.Finalize(context.Background(), "r1")

	first := mock.Calls()
	second := mock.Calls()
	if len(first) != len(second) {
		t.Fatalf("len mismatch: %d vs %d", len(first), len(second))
	}
	// Mutate the first returned copy; the second copy and subsequent reads must
	// be unaffected.
	first[0].Method = "MUTATED"
	if got := mock.Calls(); got[0].Method == "MUTATED" {
		t.Error("Calls() returned a reference to the internal slice")
	}
}
