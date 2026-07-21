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

	for _, typ := range []reflect.Type{reflect.TypeFor[ExecutionEvent](), reflect.TypeFor[ExecutionCandidate]()} {
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
		Protocol:  "openai_chat",
		Kind:      "attempt",
		RuleID:    "retry-safe",
		Action:    "next_route",
		Status:    "failed",
		Code:      "UPSTREAM_ERROR",
		Type:      "server_error",
		Timestamp: time.Date(2026, time.July, 21, 12, 0, attempt, 0, time.UTC),
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
