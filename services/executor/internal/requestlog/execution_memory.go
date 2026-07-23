package requestlog

import (
	"context"
	"sync"
)

// defaultRingCapacity is the default FIFO ring buffer capacity.
const defaultRingCapacity = 10000

// ExecutionFaultHook is invoked after an event has been appended. A returned
// error does not remove the recorded event, allowing pipeline tests to model a
// recording failure after observation. It is a test seam only and does not add
// durability or idempotency semantics.
type ExecutionFaultHook func(ExecutionEvent) error

// InMemoryExecution is a concurrency-safe, in-memory ExecutionPort intended
// for tests and local composition. Events are retained in a FIFO ring buffer
// with a configurable capacity (default 10000). When the buffer is full, the
// oldest events are evicted. Events and QueryEvents return defensive copies.
type InMemoryExecution struct {
	mu        sync.Mutex
	events    []ExecutionEvent
	capacity  int
	head      int // index of oldest element
	count     int
	faultHook ExecutionFaultHook
}

var _ ExecutionPort = (*InMemoryExecution)(nil)

// NewInMemoryExecution creates an empty in-memory execution log with the
// default ring buffer capacity (10000).
func NewInMemoryExecution() *InMemoryExecution {
	return &InMemoryExecution{
		capacity: defaultRingCapacity,
		events:   make([]ExecutionEvent, defaultRingCapacity),
	}
}

// NewInMemoryExecutionWithCapacity creates an empty in-memory execution log
// with the given ring buffer capacity. Capacity must be positive; it panics
// otherwise.
func NewInMemoryExecutionWithCapacity(capacity int) *InMemoryExecution {
	if capacity <= 0 {
		panic("requestlog: InMemoryExecution capacity must be positive")
	}
	return &InMemoryExecution{
		capacity: capacity,
		events:   make([]ExecutionEvent, capacity),
	}
}

// SetFaultHook configures a post-record fault hook for tests. Pass nil to
// clear it. The hook always runs outside the log lock.
func (m *InMemoryExecution) SetFaultHook(hook ExecutionFaultHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faultHook = hook
}

// RecordExecution appends event and then invokes the configured test hook.
// When the ring buffer is full, the oldest event is evicted (FIFO).
// When the hook returns an error, event remains recorded.
func (m *InMemoryExecution) RecordExecution(_ context.Context, event ExecutionEvent) error {
	m.mu.Lock()
	idx := (m.head + m.count) % m.capacity
	if m.count < m.capacity {
		m.events[idx] = event
		m.count++
	} else {
		// Buffer full: overwrite the oldest entry.
		m.events[m.head] = event
		m.head = (m.head + 1) % m.capacity
	}
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
	return m.snapshot()
}

// QueryEvents returns recorded events matching the filter. All filter fields
// are optional; a zero-value filter returns all events. The returned slice is
// a defensive copy in insertion order.
func (m *InMemoryExecution) QueryEvents(_ context.Context, filter ExecutionFilter) ([]ExecutionEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.snapshot()
	if filter == (ExecutionFilter{}) {
		return all, nil
	}
	var result []ExecutionEvent
	for _, e := range all {
		if filter.RequestID != "" && e.RequestID != filter.RequestID {
			continue
		}
		if filter.ReservationID != "" && e.ReservationID != filter.ReservationID {
			continue
		}
		if filter.Kind != "" && e.Kind != filter.Kind {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

// EventCount returns the number of events currently in the ring buffer.
// This is the count of events that have not been evicted; it does not include
// previously evicted events.
func (m *InMemoryExecution) EventCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// snapshot returns a defensive copy of the ring buffer in insertion order.
// Caller must hold m.mu.
func (m *InMemoryExecution) snapshot() []ExecutionEvent {
	if m.count == 0 {
		return nil
	}
	result := make([]ExecutionEvent, m.count)
	for i := 0; i < m.count; i++ {
		result[i] = m.events[(m.head+i)%m.capacity]
	}
	return result
}
