package runtime

import (
	"context"
	"sync"
)

// Mock is a concurrency-safe configurable test double for Port. All mutable
// configuration fields are private and accessed only through the mutex-guarded
// constructor/setters. Mock is safe for concurrent use: configure once up
// front, then call concurrently. Fields are never mutated concurrently with
// reads by the caller's contract.
type Mock struct {
	mu sync.Mutex

	snapshotFn     func(ctx context.Context) (Snapshot, error)
	snapshotResult Snapshot
	snapshotErr    error

	getQuarantineFn     func(ctx context.Context, target RuntimeTarget) (Quarantine, error)
	getQuarantineResult Quarantine
	getQuarantineErr    error

	setQuarantineFn  func(ctx context.Context, input QuarantineInput) error
	setQuarantineErr error
}

var _ Port = (*Mock)(nil)

// NewMock returns an empty Mock. Configure it with the Set* methods or With*
// options.
func NewMock() *Mock {
	return &Mock{}
}

// WithSnapshotResult sets the static Snapshot result.
func WithSnapshotResult(s Snapshot) func(*Mock) {
	return func(m *Mock) { m.snapshotResult = s }
}

// WithSnapshotErr sets the static Snapshot error.
func WithSnapshotErr(err error) func(*Mock) {
	return func(m *Mock) { m.snapshotErr = err }
}

// WithSnapshotFn installs a Snapshot handler.
func WithSnapshotFn(fn func(ctx context.Context) (Snapshot, error)) func(*Mock) {
	return func(m *Mock) { m.snapshotFn = fn }
}

// WithGetQuarantineResult sets the static GetQuarantine result.
func WithGetQuarantineResult(q Quarantine) func(*Mock) {
	return func(m *Mock) { m.getQuarantineResult = q }
}

// WithGetQuarantineErr sets the static GetQuarantine error.
func WithGetQuarantineErr(err error) func(*Mock) {
	return func(m *Mock) { m.getQuarantineErr = err }
}

// WithGetQuarantineFn installs a GetQuarantine handler.
func WithGetQuarantineFn(fn func(ctx context.Context, target RuntimeTarget) (Quarantine, error)) func(*Mock) {
	return func(m *Mock) { m.getQuarantineFn = fn }
}

// WithSetQuarantineFn installs a SetQuarantine handler.
func WithSetQuarantineFn(fn func(ctx context.Context, input QuarantineInput) error) func(*Mock) {
	return func(m *Mock) { m.setQuarantineFn = fn }
}

// WithSetQuarantineErr sets the static SetQuarantine error.
func WithSetQuarantineErr(err error) func(*Mock) {
	return func(m *Mock) { m.setQuarantineErr = err }
}

// NewMockWith returns a Mock configured with the given options.
func NewMockWith(opts ...func(*Mock)) *Mock {
	m := NewMock()
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// SetSnapshotResult sets the static Snapshot result. Safe for concurrent use.
func (m *Mock) SetSnapshotResult(s Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotResult = s
}

// SetSnapshotErr sets the static Snapshot error. Safe for concurrent use.
func (m *Mock) SetSnapshotErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotErr = err
}

// SetSnapshotFn installs a Snapshot handler. Pass nil to clear. Safe for
// concurrent use.
func (m *Mock) SetSnapshotFn(fn func(ctx context.Context) (Snapshot, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotFn = fn
}

// SetGetQuarantineResult sets the static GetQuarantine result. Safe for
// concurrent use.
func (m *Mock) SetGetQuarantineResult(q Quarantine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getQuarantineResult = q
}

// SetGetQuarantineErr sets the static GetQuarantine error. Safe for concurrent
// use.
func (m *Mock) SetGetQuarantineErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getQuarantineErr = err
}

// SetGetQuarantineFn installs a GetQuarantine handler. Pass nil to clear. Safe
// for concurrent use.
func (m *Mock) SetGetQuarantineFn(fn func(ctx context.Context, target RuntimeTarget) (Quarantine, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getQuarantineFn = fn
}

// SetQuarantineFn installs a SetQuarantine handler. Pass nil to clear. Safe for
// concurrent use.
func (m *Mock) SetQuarantineFn(fn func(ctx context.Context, input QuarantineInput) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setQuarantineFn = fn
}

// SetSetQuarantineErr sets the static SetQuarantine error. Safe for concurrent
// use.
func (m *Mock) SetSetQuarantineErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setQuarantineErr = err
}

// Snapshot returns the configured result or invokes the configured handler.
func (m *Mock) Snapshot(ctx context.Context) (Snapshot, error) {
	m.mu.Lock()
	fn := m.snapshotFn
	result := m.snapshotResult
	err := m.snapshotErr
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx)
	}
	return result, err
}

// GetQuarantine returns the configured result or invokes the configured handler.
func (m *Mock) GetQuarantine(ctx context.Context, target RuntimeTarget) (Quarantine, error) {
	m.mu.Lock()
	fn := m.getQuarantineFn
	result := m.getQuarantineResult
	err := m.getQuarantineErr
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, target)
	}
	return result, err
}

// SetQuarantine returns the configured result or invokes the configured handler.
func (m *Mock) SetQuarantine(ctx context.Context, input QuarantineInput) error {
	m.mu.Lock()
	fn := m.setQuarantineFn
	err := m.setQuarantineErr
	m.mu.Unlock()

	if fn != nil {
		return fn(ctx, input)
	}
	return err
}
