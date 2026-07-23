package requestlog

import (
	"context"
	"sync"
)

// ExecutionMock is a concurrency-safe configurable test double for
// ExecutionPort. All mutable configuration and record fields are private and
// accessed only through the mutex-guarded constructor/setters and the
// accessors. ExecutionMock is safe for concurrent use: configure once up
// front, then call concurrently. Fields are never mutated concurrently with
// reads by the caller's contract.
type ExecutionMock struct {
	mu sync.Mutex

	recordFn  func(ctx context.Context, event ExecutionEvent) error
	recordErr error

	queryFn func(ctx context.Context, filter ExecutionFilter) ([]ExecutionEvent, error)

	// events is the internal record store when no RecordFn is set.
	events []ExecutionEvent
}

var _ ExecutionPort = (*ExecutionMock)(nil)

// NewExecutionMock returns an empty ExecutionMock. Configure it with the Set*
// methods or With* options.
func NewExecutionMock() *ExecutionMock {
	return &ExecutionMock{}
}

// WithExecutionRecordFn is a functional option installing a RecordExecution handler.
func WithExecutionRecordFn(fn func(ctx context.Context, event ExecutionEvent) error) func(*ExecutionMock) {
	return func(m *ExecutionMock) { m.recordFn = fn }
}

// WithExecutionRecordErr is a functional option setting the static
// RecordExecution error (used when RecordFn is nil).
func WithExecutionRecordErr(err error) func(*ExecutionMock) {
	return func(m *ExecutionMock) { m.recordErr = err }
}

// WithExecutionQueryFn is a functional option installing a QueryEvents handler.
func WithExecutionQueryFn(fn func(ctx context.Context, filter ExecutionFilter) ([]ExecutionEvent, error)) func(*ExecutionMock) {
	return func(m *ExecutionMock) { m.queryFn = fn }
}

// NewExecutionMockWith returns an ExecutionMock configured with the given options.
func NewExecutionMockWith(opts ...func(*ExecutionMock)) *ExecutionMock {
	m := NewExecutionMock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// SetRecordFn installs a RecordExecution handler. Pass nil to clear. Safe for
// concurrent use.
func (m *ExecutionMock) SetRecordFn(fn func(ctx context.Context, event ExecutionEvent) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordFn = fn
}

// SetRecordErr sets the static RecordExecution error. Safe for concurrent use.
func (m *ExecutionMock) SetRecordErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordErr = err
}

// SetQueryFn installs a QueryEvents handler. Pass nil to clear. Safe for
// concurrent use.
func (m *ExecutionMock) SetQueryFn(fn func(ctx context.Context, filter ExecutionFilter) ([]ExecutionEvent, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryFn = fn
}

// RecordExecution logs an execution event. If RecordFn is set it is invoked
// outside the lock; otherwise the event is appended to the internal store and
// RecordErr is returned.
func (m *ExecutionMock) RecordExecution(ctx context.Context, event ExecutionEvent) error {
	m.mu.Lock()
	fn := m.recordFn
	err := m.recordErr
	if fn == nil && err == nil {
		m.events = append(m.events, event)
	}
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, event)
	}
	return err
}

// QueryEvents returns recorded events matching the filter. The returned slice
// is always a fresh copy; callers may mutate it without affecting the mock or
// any handler-supplied slice. If QueryFn is set, it is invoked outside the
// lock and its result is defensively copied before being returned.
func (m *ExecutionMock) QueryEvents(ctx context.Context, filter ExecutionFilter) ([]ExecutionEvent, error) {
	m.mu.Lock()
	fn := m.queryFn
	cp := append([]ExecutionEvent(nil), m.events...)
	m.mu.Unlock()

	if fn != nil {
		result, err := fn(ctx, filter)
		if err != nil {
			return nil, err
		}
		// Defensive copy: callers of QueryEvents must not be able to mutate
		// the slice returned by the configured handler.
		return append([]ExecutionEvent(nil), result...), nil
	}

	if filter == (ExecutionFilter{}) {
		return cp, nil
	}
	var result []ExecutionEvent
	for _, e := range cp {
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
