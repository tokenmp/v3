package requestlog

import (
	"context"
	"sync"
)

// ExecutionFaultHook is invoked after an event has been appended. A returned
// error does not remove the recorded event, allowing pipeline tests to model a
// recording failure after observation. It is a test seam only and does not add
// durability or idempotency semantics.
type ExecutionFaultHook func(ExecutionEvent) error

// InMemoryExecution is a concurrency-safe, in-memory ExecutionPort intended
// for tests and local composition. Events are retained in successful call
// order. Events returns a defensive copy.
type InMemoryExecution struct {
	mu        sync.Mutex
	events    []ExecutionEvent
	faultHook ExecutionFaultHook
}

var _ ExecutionPort = (*InMemoryExecution)(nil)

// NewInMemoryExecution creates an empty in-memory execution log.
func NewInMemoryExecution() *InMemoryExecution {
	return &InMemoryExecution{}
}

// SetFaultHook configures a post-record fault hook for tests. Pass nil to
// clear it. The hook always runs outside the log lock.
func (m *InMemoryExecution) SetFaultHook(hook ExecutionFaultHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faultHook = hook
}

// RecordExecution appends event and then invokes the configured test hook.
// When the hook returns an error, event remains recorded.
func (m *InMemoryExecution) RecordExecution(_ context.Context, event ExecutionEvent) error {
	m.mu.Lock()
	m.events = append(m.events, event)
	hook := m.faultHook
	m.mu.Unlock()

	if hook != nil {
		return hook(event)
	}
	return nil
}

// Events returns all recorded execution events in successful call order. The
// returned slice is independent from the in-memory log.
func (m *InMemoryExecution) Events(_ context.Context) []ExecutionEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ExecutionEvent(nil), m.events...)
}
