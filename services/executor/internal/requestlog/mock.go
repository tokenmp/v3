package requestlog

import (
	"context"
	"sync"
)

// Mock is a concurrency-safe configurable test double for Port. All mutable
// configuration and record fields are private and accessed only through the
// mutex-guarded constructor/setters and the accessors. Mock is safe for
// concurrent use: configure once up front, then call concurrently. Fields are
// never mutated concurrently with reads by the caller's contract.
type Mock struct {
	mu sync.Mutex

	recordFn  func(ctx context.Context, entry CallEntry) error
	recordErr error

	callsFn func(ctx context.Context) []CallEntry

	// entries is the internal record store when no RecordFn/CallsFn is set.
	entries []CallEntry
}

var _ Port = (*Mock)(nil)

// NewMock returns an empty Mock. Configure it with the Set* methods or With*
// options.
func NewMock() *Mock {
	return &Mock{}
}

// WithRecordFn is a functional option installing a Record handler.
func WithRecordFn(fn func(ctx context.Context, entry CallEntry) error) func(*Mock) {
	return func(m *Mock) { m.recordFn = fn }
}

// WithRecordErr is a functional option setting the static Record error (used
// when RecordFn is nil).
func WithRecordErr(err error) func(*Mock) {
	return func(m *Mock) { m.recordErr = err }
}

// WithCallsFn is a functional option installing a Calls handler.
func WithCallsFn(fn func(ctx context.Context) []CallEntry) func(*Mock) {
	return func(m *Mock) { m.callsFn = fn }
}

// NewMockWith returns a Mock configured with the given options.
func NewMockWith(opts ...func(*Mock)) *Mock {
	m := NewMock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// SetRecordFn installs a Record handler. Pass nil to clear. Safe for concurrent
// use.
func (m *Mock) SetRecordFn(fn func(ctx context.Context, entry CallEntry) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordFn = fn
}

// SetRecordErr sets the static Record error. Safe for concurrent use.
func (m *Mock) SetRecordErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordErr = err
}

// SetCallsFn installs a Calls handler. Pass nil to clear. Safe for concurrent
// use.
func (m *Mock) SetCallsFn(fn func(ctx context.Context) []CallEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callsFn = fn
}

// Record logs a call entry. If RecordFn is set it is invoked outside the lock;
// otherwise the entry is appended to the internal store and RecordErr is
// returned.
func (m *Mock) Record(ctx context.Context, entry CallEntry) error {
	m.mu.Lock()
	fn := m.recordFn
	err := m.recordErr
	if fn == nil && err == nil {
		m.entries = append(m.entries, entry)
	}
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, entry)
	}
	return err
}

// Calls returns all recorded entries in order. The returned slice is always
// a fresh copy; callers may mutate it without affecting the mock or any
// handler-supplied slice. If CallsFn is set, it is invoked outside the lock
// and its result is defensively copied before being returned.
func (m *Mock) Calls(ctx context.Context) []CallEntry {
	m.mu.Lock()
	fn := m.callsFn
	cp := append([]CallEntry(nil), m.entries...)
	m.mu.Unlock()

	if fn != nil {
		// Defensive copy: callers of Calls must not be able to mutate the
		// slice returned by the configured handler, so the mock never exposes
		// an alias to handler-internal state.
		return append([]CallEntry(nil), fn(ctx)...)
	}
	return cp
}
