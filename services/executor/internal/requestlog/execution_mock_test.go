package requestlog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestExecutionMockContract(t *testing.T) {
	ExecutionContractTests(t, func() ExecutionPort { return NewExecutionMock() })
}

func TestExecutionMockRecordFn(t *testing.T) {
	t.Parallel()

	var recorded []ExecutionEvent
	mock := NewExecutionMockWith(WithExecutionRecordFn(func(_ context.Context, event ExecutionEvent) error {
		recorded = append(recorded, event)
		return nil
	}))
	_ = mock.RecordExecution(context.Background(), ExecutionEvent{RequestID: "r1", Kind: KindAttempt})
	if len(recorded) != 1 || recorded[0].RequestID != "r1" {
		t.Errorf("recorded = %+v, want r1", recorded)
	}
}

func TestExecutionMockRecordError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("log full")
	mock := NewExecutionMockWith(WithExecutionRecordErr(sentinel))
	err := mock.RecordExecution(context.Background(), ExecutionEvent{})
	if !errors.Is(err, sentinel) {
		t.Errorf("RecordExecution() error = %v, want %v", err, sentinel)
	}
}

func TestExecutionMockQueryFn(t *testing.T) {
	t.Parallel()

	want := []ExecutionEvent{{RequestID: "r1", Kind: KindAttempt}}
	mock := NewExecutionMockWith(WithExecutionQueryFn(func(_ context.Context, _ ExecutionFilter) ([]ExecutionEvent, error) {
		return want, nil
	}))
	got, err := mock.QueryEvents(context.Background(), ExecutionFilter{})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != len(want) || got[0].RequestID != want[0].RequestID {
		t.Fatalf("QueryEvents() = %+v, want %+v", got, want)
	}
}

func TestExecutionMockQueryFnError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("query failed")
	mock := NewExecutionMockWith(WithExecutionQueryFn(func(_ context.Context, _ ExecutionFilter) ([]ExecutionEvent, error) {
		return nil, sentinel
	}))
	_, err := mock.QueryEvents(context.Background(), ExecutionFilter{})
	if !errors.Is(err, sentinel) {
		t.Errorf("QueryEvents() error = %v, want %v", err, sentinel)
	}
}

// TestExecutionMockQueryReturnsCopy verifies that QueryEvents() returns a
// defensive copy of the internal record store so callers cannot alias or
// mutate the mock's internal slice when no QueryFn is configured.
func TestExecutionMockQueryReturnsCopy(t *testing.T) {
	t.Parallel()

	mock := NewExecutionMock()
	_ = mock.RecordExecution(context.Background(), ExecutionEvent{RequestID: "a", Kind: KindAttempt})
	_ = mock.RecordExecution(context.Background(), ExecutionEvent{RequestID: "b", Kind: KindReserved})

	first, _ := mock.QueryEvents(context.Background(), ExecutionFilter{})
	second, _ := mock.QueryEvents(context.Background(), ExecutionFilter{})
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("len(QueryEvents()) = %d, %d, want 2, 2", len(first), len(second))
	}
	first[0].RequestID = "MUTATED"
	if got, _ := mock.QueryEvents(context.Background(), ExecutionFilter{}); got[0].RequestID == "MUTATED" {
		t.Error("QueryEvents() returned a reference to the internal slice")
	}
	if second[0].RequestID == "MUTATED" {
		t.Error("QueryEvents() returned slices that share a backing array")
	}
}

// TestExecutionMockQueryFnReturnsCopy verifies that when a QueryFn is
// configured, its result is defensively copied so callers of QueryEvents
// cannot mutate the slice the handler returns.
func TestExecutionMockQueryFnReturnsCopy(t *testing.T) {
	t.Parallel()

	shared := []ExecutionEvent{{RequestID: "r1", Kind: KindAttempt}}
	mock := NewExecutionMockWith(WithExecutionQueryFn(func(_ context.Context, _ ExecutionFilter) ([]ExecutionEvent, error) {
		return shared, nil
	}))

	got, _ := mock.QueryEvents(context.Background(), ExecutionFilter{})
	if len(got) != 1 || got[0].RequestID != shared[0].RequestID {
		t.Fatalf("QueryEvents() = %+v, want %+v", got, shared)
	}
	got[0].RequestID = "MUTATED"
	if shared[0].RequestID == "MUTATED" {
		t.Error("QueryEvents() returned an alias to the QueryFn result slice")
	}
	again, _ := mock.QueryEvents(context.Background(), ExecutionFilter{})
	if again[0].RequestID != "r1" {
		t.Errorf("QueryEvents() after mutation = %+v, want unchanged r1", again[0])
	}
	again[0].RequestID = "AGAIN"
	if got[0].RequestID == "AGAIN" || shared[0].RequestID == "AGAIN" {
		t.Error("successive QueryEvents() results alias each other")
	}
}

// TestExecutionMockConcurrentRecordQuery verifies RecordExecution and
// QueryEvents are safe for concurrent use when the mock is configured once up
// front and not reconfigured during the calls.
func TestExecutionMockConcurrentRecordQuery(t *testing.T) {
	t.Parallel()

	mock := NewExecutionMock()

	const records = 150
	const readers = 50
	var wg sync.WaitGroup
	wg.Add(records + readers)
	errs := make(chan error, readers)

	for i := 0; i < records; i++ {
		i := i
		go func() {
			defer wg.Done()
			event := ExecutionEvent{RequestID: fmt.Sprintf("r/%d", i), Kind: KindAttempt}
			if err := mock.RecordExecution(context.Background(), event); err != nil {
				errs <- err
			}
		}()
	}
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			_, _ = mock.QueryEvents(context.Background(), ExecutionFilter{})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	got, _ := mock.QueryEvents(context.Background(), ExecutionFilter{})
	if len(got) != records {
		t.Fatalf("len(QueryEvents()) = %d, want %d", len(got), records)
	}
	seen := make(map[string]bool, records)
	for _, e := range got {
		seen[e.RequestID] = true
	}
	for i := 0; i < records; i++ {
		if !seen[fmt.Sprintf("r/%d", i)] {
			t.Errorf("missing recorded entry for r/%d", i)
		}
	}
}

func TestInMemoryExecutionContract(t *testing.T) {
	ExecutionContractTests(t, func() ExecutionPort { return NewInMemoryExecution() })
}
