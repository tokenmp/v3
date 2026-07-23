package requestlog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestExecutionEventSafeSurface(t *testing.T) {
	t.Parallel()

	for _, typ := range []reflect.Type{
		reflect.TypeFor[ExecutionEvent](),
		reflect.TypeFor[ExecutionCandidate](),
		reflect.TypeFor[ExecutionUsage](),
		reflect.TypeFor[ExecutionSettlement](),
	} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			for _, forbidden := range []string{"body", "url", "header", "ref", "secret"} {
				if containsFold(field.Name, forbidden) {
					t.Fatalf("%s unexpectedly exposes sensitive %q field", typ, field.Name)
				}
			}
		}
	}

	event := executionEvent(1)
	rendered := fmt.Sprintf("%+v", event)
	for _, marker := range []string{"secret", "api-key", "authorization", "https://", "request body"} {
		if containsFold(rendered, marker) {
			t.Fatalf("fmt rendering leaked marker %q: %s", marker, rendered)
		}
	}
}

func TestInMemoryExecutionOrderAndDefensiveCopy(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	first, second := executionEvent(1), executionEvent(2)
	if err := log.RecordExecution(context.Background(), first); err != nil {
		t.Fatalf("RecordExecution(first) error = %v", err)
	}
	if err := log.RecordExecution(context.Background(), second); err != nil {
		t.Fatalf("RecordExecution(second) error = %v", err)
	}

	got := log.Events(context.Background())
	if !reflect.DeepEqual(got, []ExecutionEvent{first, second}) {
		t.Fatalf("Events() = %#v, want %#v", got, []ExecutionEvent{first, second})
	}
	got[0].RequestID = "mutated"
	if reread := log.Events(context.Background()); reread[0].RequestID == "mutated" {
		t.Fatal("Events() returned an alias to internal storage")
	}
}

func TestInMemoryExecutionFaultInjectionRecordsBeforeReturningError(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	fault := errors.New("recording unavailable")
	log.SetFaultHook(func(event ExecutionEvent) error {
		if event.Attempt != 1 {
			t.Errorf("hook event attempt = %d, want 1", event.Attempt)
		}
		if got := len(log.Events(context.Background())); got != 1 {
			t.Errorf("Events() during hook = %d, want 1", got)
		}
		return fault
	})

	event := executionEvent(1)
	if err := log.RecordExecution(context.Background(), event); !errors.Is(err, fault) {
		t.Fatalf("RecordExecution() error = %v, want %v", err, fault)
	}
	if got := log.Events(context.Background()); !reflect.DeepEqual(got, []ExecutionEvent{event}) {
		t.Fatalf("Events() after injected fault = %#v, want %#v", got, []ExecutionEvent{event})
	}

	log.SetFaultHook(nil)
	if err := log.RecordExecution(context.Background(), executionEvent(2)); err != nil {
		t.Fatalf("RecordExecution() after clearing fault = %v", err)
	}
}

func TestInMemoryExecutionConcurrentRecordAndRead(t *testing.T) {
	log := NewInMemoryExecution()
	const records = 200

	start := make(chan struct{})
	var writers sync.WaitGroup
	for i := 0; i < records; i++ {
		writers.Add(1)
		go func(attempt int) {
			defer writers.Done()
			<-start
			if err := log.RecordExecution(context.Background(), executionEvent(attempt)); err != nil {
				t.Errorf("RecordExecution(%d) error = %v", attempt, err)
			}
		}(i + 1)
	}

	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for range 50 {
				_ = log.Events(context.Background())
			}
		}()
	}

	close(start)
	writers.Wait()
	readers.Wait()
	if got := len(log.Events(context.Background())); got != records {
		t.Fatalf("len(Events()) = %d, want %d", got, records)
	}
}

func TestInMemoryExecutionRingBufferFIFOEviction(t *testing.T) {
	t.Parallel()

	const cap = 5
	log := NewInMemoryExecutionWithCapacity(cap)
	for i := 0; i < 10; i++ {
		if err := log.RecordExecution(context.Background(), executionEvent(i+1)); err != nil {
			t.Fatalf("RecordExecution(%d) error = %v", i+1, err)
		}
	}
	got := log.Events(context.Background())
	if len(got) != cap {
		t.Fatalf("len(Events()) = %d, want %d", len(got), cap)
	}
	// The last 5 events (attempt 6..10) should remain.
	for i, want := range []int{6, 7, 8, 9, 10} {
		if got[i].Attempt != want {
			t.Errorf("Events()[%d].Attempt = %d, want %d", i, got[i].Attempt, want)
		}
	}
}

func TestInMemoryExecutionRingBufferExactCapacity(t *testing.T) {
	t.Parallel()

	const cap = 3
	log := NewInMemoryExecutionWithCapacity(cap)
	for i := 0; i < 3; i++ {
		if err := log.RecordExecution(context.Background(), executionEvent(i+1)); err != nil {
			t.Fatalf("RecordExecution(%d) error = %v", i+1, err)
		}
	}
	got := log.Events(context.Background())
	if len(got) != cap {
		t.Fatalf("len(Events()) = %d, want %d", len(got), cap)
	}
	for i, want := range []int{1, 2, 3} {
		if got[i].Attempt != want {
			t.Errorf("Events()[%d].Attempt = %d, want %d", i, got[i].Attempt, want)
		}
	}
}

func TestInMemoryExecutionRingBufferPanicsOnNonPositiveCapacity(t *testing.T) {
	t.Parallel()

	for _, cap := range []int{0, -1} {
		cap := cap
		t.Run(fmt.Sprintf("cap=%d", cap), func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected panic for capacity %d", cap)
				}
			}()
			NewInMemoryExecutionWithCapacity(cap)
		})
	}
}

func TestInMemoryExecutionQueryEventsNoFilter(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	e1 := executionEvent(1)
	e1.RequestID = "req-a"
	e2 := executionEvent(2)
	e2.RequestID = "req-b"
	_ = log.RecordExecution(context.Background(), e1)
	_ = log.RecordExecution(context.Background(), e2)

	got, err := log.QueryEvents(context.Background(), ExecutionFilter{})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(QueryEvents()) = %d, want 2", len(got))
	}
}

func TestInMemoryExecutionQueryEventsByRequestID(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	e1 := executionEvent(1)
	e1.RequestID = "req-a"
	e2 := executionEvent(2)
	e2.RequestID = "req-b"
	_ = log.RecordExecution(context.Background(), e1)
	_ = log.RecordExecution(context.Background(), e2)

	got, err := log.QueryEvents(context.Background(), ExecutionFilter{RequestID: "req-a"})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 1 || got[0].RequestID != "req-a" {
		t.Fatalf("QueryEvents() = %+v, want req-a only", got)
	}
}

func TestInMemoryExecutionQueryEventsByReservationID(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	e1 := executionEvent(1)
	e1.ReservationID = "res-1"
	e2 := executionEvent(2)
	e2.ReservationID = "res-2"
	_ = log.RecordExecution(context.Background(), e1)
	_ = log.RecordExecution(context.Background(), e2)

	got, err := log.QueryEvents(context.Background(), ExecutionFilter{ReservationID: "res-2"})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 1 || got[0].ReservationID != "res-2" {
		t.Fatalf("QueryEvents() = %+v, want res-2 only", got)
	}
}

func TestInMemoryExecutionQueryEventsByKind(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	e1 := executionEvent(1)
	e1.Kind = KindAttempt
	e2 := executionEvent(2)
	e2.Kind = KindReserved
	e3 := executionEvent(3)
	e3.Kind = KindAttempt
	_ = log.RecordExecution(context.Background(), e1)
	_ = log.RecordExecution(context.Background(), e2)
	_ = log.RecordExecution(context.Background(), e3)

	got, err := log.QueryEvents(context.Background(), ExecutionFilter{Kind: KindAttempt})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(QueryEvents()) = %d, want 2", len(got))
	}
	for _, e := range got {
		if e.Kind != KindAttempt {
			t.Errorf("event.Kind = %q, want %q", e.Kind, KindAttempt)
		}
	}
}

func TestInMemoryExecutionQueryEventsCombinedFilter(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	e1 := executionEvent(1)
	e1.RequestID = "req-a"
	e1.Kind = KindAttempt
	e2 := executionEvent(2)
	e2.RequestID = "req-a"
	e2.Kind = KindReserved
	e3 := executionEvent(3)
	e3.RequestID = "req-b"
	e3.Kind = KindAttempt
	_ = log.RecordExecution(context.Background(), e1)
	_ = log.RecordExecution(context.Background(), e2)
	_ = log.RecordExecution(context.Background(), e3)

	got, err := log.QueryEvents(context.Background(), ExecutionFilter{RequestID: "req-a", Kind: KindAttempt})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 1 || got[0].RequestID != "req-a" || got[0].Kind != KindAttempt {
		t.Fatalf("QueryEvents() = %+v, want req-a+attempt only", got)
	}
}

func TestInMemoryExecutionQueryEventsNoMatch(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	_ = log.RecordExecution(context.Background(), executionEvent(1))

	got, err := log.QueryEvents(context.Background(), ExecutionFilter{RequestID: "nonexistent"})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(QueryEvents()) = %d, want 0", len(got))
	}
}

func TestInMemoryExecutionQueryEventsReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()

	log := NewInMemoryExecution()
	_ = log.RecordExecution(context.Background(), executionEvent(1))

	got, _ := log.QueryEvents(context.Background(), ExecutionFilter{})
	got[0].RequestID = "mutated"
	again, _ := log.QueryEvents(context.Background(), ExecutionFilter{})
	if again[0].RequestID == "mutated" {
		t.Fatal("QueryEvents() returned an alias to internal storage")
	}
}

func TestInMemoryExecutionConcurrentRecordAndQuery(t *testing.T) {
	log := NewInMemoryExecution()
	const records = 200

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < records; i++ {
		wg.Add(1)
		go func(attempt int) {
			defer wg.Done()
			<-start
			_ = log.RecordExecution(context.Background(), executionEvent(attempt))
		}(i + 1)
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 50 {
				_, _ = log.QueryEvents(context.Background(), ExecutionFilter{Kind: KindAttempt})
			}
		}()
	}

	close(start)
	wg.Wait()
	if got := len(log.Events(context.Background())); got != records {
		t.Fatalf("len(Events()) = %d, want %d", got, records)
	}
}

func executionEvent(attempt int) ExecutionEvent {
	return ExecutionEvent{
		RequestID:     fmt.Sprintf("request-%d", attempt),
		ReservationID: fmt.Sprintf("reservation-%d", attempt),
		Revision:      "revision-1",
		Generation:    7,
		Attempt:       attempt,
		Candidate: ExecutionCandidate{
			ModelID: "model-safe", ProviderID: "provider-safe", RouteID: "route-safe", CredentialID: "credential-safe", AdapterID: "adapter-safe",
		},
		Protocol:   "openai_chat",
		Kind:       KindAttempt,
		RuleID:     "retry-safe",
		Action:     "next_route",
		Status:     "failed",
		Code:       "UPSTREAM_ERROR",
		Type:       "server_error",
		Timestamp:  time.Date(2026, time.July, 21, 12, 0, attempt, 0, time.UTC),
		Subject:    "subject-safe",
		KeyID:      "keyid-safe",
		Latency:    time.Duration(attempt) * 100 * time.Millisecond,
		Usage:      ExecutionUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		UsageKnown: true,
		Committed:  false,
		Settlement: ExecutionSettlement{},
	}
}

func containsFold(value, part string) bool {
	for len(value) >= len(part) {
		if len(value) >= len(part) && equalFoldASCII(value[:len(part)], part) {
			return true
		}
		value = value[1:]
	}
	return false
}

func equalFoldASCII(value, want string) bool {
	if len(value) != len(want) {
		return false
	}
	for i := range value {
		a, b := value[i], want[i]
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

// ExecutionContractTests runs the repository contract suite against any
// ExecutionPort implementation.
func ExecutionContractTests(t *testing.T, newPort func() ExecutionPort) {
	t.Helper()

	t.Run("events returns empty initially", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		got, err := port.QueryEvents(context.Background(), ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len(QueryEvents()) = %d, want 0", len(got))
		}
	})

	t.Run("record and query single event", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		event := ExecutionEvent{RequestID: "r1", Kind: KindAttempt, Timestamp: time.Now()}
		if err := port.RecordExecution(context.Background(), event); err != nil {
			t.Fatalf("RecordExecution() error = %v", err)
		}
		got, err := port.QueryEvents(context.Background(), ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != 1 || got[0].RequestID != "r1" {
			t.Fatalf("QueryEvents() = %+v, want r1", got)
		}
	})

	t.Run("record preserves order", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		events := []ExecutionEvent{
			{RequestID: "a", Kind: KindAttempt},
			{RequestID: "b", Kind: KindReserved},
			{RequestID: "c", Kind: KindFinalized},
		}
		for _, e := range events {
			if err := port.RecordExecution(context.Background(), e); err != nil {
				t.Fatalf("RecordExecution() error = %v", err)
			}
		}
		got, err := port.QueryEvents(context.Background(), ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != len(events) {
			t.Fatalf("len(QueryEvents()) = %d, want %d", len(got), len(events))
		}
		for i, e := range events {
			if got[i].RequestID != e.RequestID || got[i].Kind != e.Kind {
				t.Errorf("QueryEvents()[%d] = %+v, want %+v", i, got[i], e)
			}
		}
	})

	t.Run("query returns defensive copy", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_ = port.RecordExecution(context.Background(), ExecutionEvent{RequestID: "x", Kind: KindAttempt})
		got1, _ := port.QueryEvents(context.Background(), ExecutionFilter{})
		got2, _ := port.QueryEvents(context.Background(), ExecutionFilter{})
		if len(got1) != len(got2) {
			t.Fatalf("len mismatch: %d vs %d", len(got1), len(got2))
		}
		got1[0].RequestID = "MUTATED"
		got3, _ := port.QueryEvents(context.Background(), ExecutionFilter{})
		if got3[0].RequestID == "MUTATED" {
			t.Error("QueryEvents() returned a reference instead of a copy")
		}
	})

	t.Run("query by kind filters correctly", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_ = port.RecordExecution(context.Background(), ExecutionEvent{RequestID: "a", Kind: KindAttempt})
		_ = port.RecordExecution(context.Background(), ExecutionEvent{RequestID: "b", Kind: KindReserved})
		_ = port.RecordExecution(context.Background(), ExecutionEvent{RequestID: "c", Kind: KindAttempt})

		got, err := port.QueryEvents(context.Background(), ExecutionFilter{Kind: KindAttempt})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(QueryEvents()) = %d, want 2", len(got))
		}
		for _, e := range got {
			if e.Kind != KindAttempt {
				t.Errorf("event.Kind = %q, want %q", e.Kind, KindAttempt)
			}
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
				_ = port.RecordExecution(context.Background(), ExecutionEvent{RequestID: "concurrent", Kind: KindAttempt})
			}()
		}
		wg.Wait()
		got, err := port.QueryEvents(context.Background(), ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != 100 {
			t.Errorf("len(QueryEvents()) = %d, want 100", len(got))
		}
	})
}
