package requestlog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMockContract(t *testing.T) {
	ContractTests(t, func() Port { return NewMock() })
}

func TestMockRecordFn(t *testing.T) {
	t.Parallel()

	var recorded []CallEntry
	mock := NewMockWith(WithRecordFn(func(_ context.Context, entry CallEntry) error {
		recorded = append(recorded, entry)
		return nil
	}))
	_ = mock.Record(context.Background(), CallEntry{Method: "GET"})
	if len(recorded) != 1 {
		t.Errorf("len(recorded) = %d, want 1", len(recorded))
	}
}

func TestMockRecordError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("log full")
	mock := NewMockWith(WithRecordErr(sentinel))
	err := mock.Record(context.Background(), CallEntry{})
	if !errors.Is(err, sentinel) {
		t.Errorf("Record() error = %v, want %v", err, sentinel)
	}
}

func TestMockCallsFn(t *testing.T) {
	t.Parallel()

	want := []CallEntry{{Method: "POST", Path: "/api"}}
	mock := NewMockWith(WithCallsFn(func(_ context.Context) []CallEntry {
		return want
	}))
	got := mock.Calls(context.Background())
	if len(got) != len(want) {
		t.Fatalf("len(Calls()) = %d, want %d", len(got), len(want))
	}
	if got[0] != want[0] {
		t.Errorf("Calls()[0] = %+v, want %+v", got[0], want[0])
	}
}

// TestMockCallsReturnsCopy verifies that Calls() returns a defensive copy of
// the internal record store so callers cannot alias or mutate the mock's
// internal slice when no CallsFn is configured.
func TestMockCallsReturnsCopy(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	_ = mock.Record(context.Background(), CallEntry{Method: "GET", Path: "/a"})
	_ = mock.Record(context.Background(), CallEntry{Method: "POST", Path: "/b"})

	first := mock.Calls(context.Background())
	second := mock.Calls(context.Background())
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("len(Calls()) = %d, %d, want 2, 2", len(first), len(second))
	}
	// Mutate the first returned copy; the second copy, the internal store, and
	// subsequent reads must be unaffected.
	first[0].Method = "MUTATED"
	first[1].Path = "MUTATED"
	if got := mock.Calls(context.Background()); got[0].Method == "MUTATED" || got[1].Path == "MUTATED" {
		t.Error("Calls() returned a reference to the internal slice")
	}
	if second[0].Method == "MUTATED" || second[1].Path == "MUTATED" {
		t.Error("Calls() returned slices that share a backing array")
	}
}

// TestMockCallsFnReturnsCopy verifies that when a CallsFn is configured, its
// result is defensively copied so callers of Calls cannot mutate the slice
// the handler returns (and thus cannot mutate handler-internal state through
// the read API).
func TestMockCallsFnReturnsCopy(t *testing.T) {
	t.Parallel()

	shared := []CallEntry{{Method: "POST", Path: "/api"}}
	mock := NewMockWith(WithCallsFn(func(_ context.Context) []CallEntry {
		return shared
	}))

	got := mock.Calls(context.Background())
	if len(got) != 1 || got[0] != shared[0] {
		t.Fatalf("Calls() = %+v, want %+v", got, shared)
	}
	// Mutate the returned copy; the handler's shared slice must not change.
	got[0].Method = "MUTATED"
	if shared[0].Method == "MUTATED" {
		t.Error("Calls() returned an alias to the CallsFn result slice")
	}
	// A second read must still reflect the unchanged handler state and be an
	// independent copy.
	again := mock.Calls(context.Background())
	if again[0].Method != "POST" {
		t.Errorf("Calls() after mutation = %+v, want unchanged POST", again[0])
	}
	again[0].Method = "AGAIN"
	if got[0].Method == "AGAIN" || shared[0].Method == "AGAIN" {
		t.Error("successive Calls() results alias each other")
	}
}

// TestMockConcurrentRecordCalls verifies Record and Calls are safe for
// concurrent use when the mock is configured once up front and not reconfigured
// during the calls. With no RecordFn/CallsFn configured, Record appends to the
// internal store and Calls returns a copy; both are guarded by the mock's
// mutex.
func TestMockConcurrentRecordCalls(t *testing.T) {
	t.Parallel()

	mock := NewMock()

	const records = 150
	const readers = 50
	var wg sync.WaitGroup
	wg.Add(records + readers)
	errs := make(chan error, readers)

	for i := 0; i < records; i++ {
		i := i
		go func() {
			defer wg.Done()
			entry := CallEntry{Method: "GET", Path: fmt.Sprintf("/r/%d", i)}
			if err := mock.Record(context.Background(), entry); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			_ = mock.Calls(context.Background()) // must not race or panic
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	got := mock.Calls(context.Background())
	if len(got) != records {
		t.Fatalf("len(Calls()) = %d, want %d", len(got), records)
	}
	seen := make(map[string]bool, records)
	for _, e := range got {
		seen[e.Path] = true
	}
	for i := 0; i < records; i++ {
		if !seen[fmt.Sprintf("/r/%d", i)] {
			t.Errorf("missing recorded entry for /r/%d", i)
		}
	}
}
