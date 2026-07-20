package identity

import (
	"context"
	"sync"
)

// Mock is a concurrency-safe configurable test double for Port. All mutable
// configuration fields are private and accessed only through the mutex-guarded
// constructors/setters. Mock is safe for concurrent use: configure once up
// front, then call concurrently. Fields are never mutated concurrently with
// reads by the caller's contract.
type Mock struct {
	mu sync.Mutex

	lookupByKeyFn     func(ctx context.Context, apiKey string) (Identity, error)
	lookupByKeyResult Identity
	lookupByKeyErr    error
}

var _ Port = (*Mock)(nil)

// NewMock returns an empty Mock. Configure it with the Set* methods.
func NewMock() *Mock {
	return &Mock{}
}

// WithLookupByKeyResult is a functional option that sets the static lookup
// result returned when LookupByKeyFn is nil.
func WithLookupByKeyResult(id Identity) func(*Mock) {
	return func(m *Mock) { m.lookupByKeyResult = id }
}

// WithLookupByKeyErr is a functional option that sets the error returned when
// LookupByKeyFn is nil.
func WithLookupByKeyErr(err error) func(*Mock) {
	return func(m *Mock) { m.lookupByKeyErr = err }
}

// WithLookupByKeyFn is a functional option that installs a lookup handler. When
// set, LookupByKey calls it instead of returning the static result/error.
func WithLookupByKeyFn(fn func(ctx context.Context, apiKey string) (Identity, error)) func(*Mock) {
	return func(m *Mock) { m.lookupByKeyFn = fn }
}

// NewMockWith returns a Mock configured with the given options. It is a
// convenience wrapper around NewMock plus the corresponding Set* methods and is
// safe to use for one-time construction.
func NewMockWith(opts ...func(*Mock)) *Mock {
	m := NewMock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// SetLookupByKeyResult sets the static lookup result. Safe for concurrent use.
func (m *Mock) SetLookupByKeyResult(id Identity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookupByKeyResult = id
}

// SetLookupByKeyErr sets the static lookup error. Safe for concurrent use.
func (m *Mock) SetLookupByKeyErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookupByKeyErr = err
}

// SetLookupByKeyFn installs a lookup handler. Pass nil to clear. Safe for
// concurrent use.
func (m *Mock) SetLookupByKeyFn(fn func(ctx context.Context, apiKey string) (Identity, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookupByKeyFn = fn
}

// LookupByKey returns the configured result or invokes the configured handler.
func (m *Mock) LookupByKey(ctx context.Context, apiKey string) (Identity, error) {
	m.mu.Lock()
	fn := m.lookupByKeyFn
	result := m.lookupByKeyResult
	err := m.lookupByKeyErr
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, apiKey)
	}
	return result, err
}
