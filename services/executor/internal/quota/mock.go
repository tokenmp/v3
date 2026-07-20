package quota

import (
	"context"
	"sync"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/model"
)

// CallRecord records a single call to the quota port.
type CallRecord struct {
	Method string
	ID     string
}

// Mock implements Port with a full terminal state machine and call recording.
// All mutable configuration and record fields are private and accessed only
// through the mutex-guarded constructor/setters and the accessor methods. Mock
// is safe for concurrent use: configure once up front, then call concurrently.
// Fields are never mutated concurrently with reads by the caller's contract.
// The Calls accessor returns a defensive copy so callers cannot alias the
// internal record slice.
type Mock struct {
	mu sync.Mutex

	reserves   map[string]model.Reservation
	calls      []CallRecord
	reserveFn  func(ctx context.Context, id string) (model.Reservation, error)
	finalizeFn func(ctx context.Context, id string) (model.Reservation, error)
	releaseFn  func(ctx context.Context, id string) (model.Reservation, error)
	faultHook  FaultHook
}

var _ Port = (*Mock)(nil)

// NewMock creates an empty Mock quota port.
func NewMock() *Mock {
	return &Mock{
		reserves: make(map[string]model.Reservation),
	}
}

// SetReserveFn overrides Reserve behavior when set. Pass nil to clear. Safe
// for concurrent use.
func (m *Mock) SetReserveFn(fn func(ctx context.Context, id string) (model.Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserveFn = fn
}

// SetFinalizeFn overrides Finalize behavior when set. Pass nil to clear. Safe
// for concurrent use.
func (m *Mock) SetFinalizeFn(fn func(ctx context.Context, id string) (model.Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeFn = fn
}

// SetReleaseFn overrides Release behavior when set. Pass nil to clear. Safe for
// concurrent use.
func (m *Mock) SetReleaseFn(fn func(ctx context.Context, id string) (model.Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseFn = fn
}

// SetFaultHook sets the fault hook for testing. Pass nil to clear. Safe for
// concurrent use.
func (m *Mock) SetFaultHook(hook FaultHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faultHook = hook
}

// Calls returns a defensive copy of the recorded method calls in order. Safe
// for concurrent use; the returned slice may be mutated by the caller without
// affecting the mock.
func (m *Mock) Calls() []CallRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]CallRecord, len(m.calls))
	copy(cp, m.calls)
	return cp
}

func (m *Mock) record(method, id string) {
	m.calls = append(m.calls, CallRecord{Method: method, ID: id})
}

// Reserve creates or returns an existing reservation.
func (m *Mock) Reserve(ctx context.Context, id string) (model.Reservation, error) {
	m.mu.Lock()
	m.record("Reserve", id)
	fn := m.reserveFn
	if fn == nil {
		if r, ok := m.reserves[id]; ok {
			m.mu.Unlock()
			return r, nil
		}

		r := model.Reservation{
			ID:        id,
			Status:    model.StatusReserved,
			CreatedAt: time.Now(),
		}
		m.reserves[id] = r
		m.mu.Unlock()
		return r, nil
	}
	m.mu.Unlock()

	return fn(ctx, id)
}

// Finalize transitions a reservation to the finalized terminal state.
func (m *Mock) Finalize(ctx context.Context, id string) (model.Reservation, error) {
	return m.terminal(ctx, id, "Finalize", model.StatusFinalized, true)
}

// Release transitions a reservation to the released terminal state.
func (m *Mock) Release(ctx context.Context, id string) (model.Reservation, error) {
	return m.terminal(ctx, id, "Release", model.StatusReleased, false)
}

func (m *Mock) terminal(ctx context.Context, id, method string, target model.ReservationStatus, finalize bool) (model.Reservation, error) {
	m.mu.Lock()
	m.record(method, id)
	fn := m.releaseFn
	if finalize {
		fn = m.finalizeFn
	}
	if fn != nil {
		m.mu.Unlock()
		return fn(ctx, id)
	}

	r, ok := m.reserves[id]
	if !ok {
		m.mu.Unlock()
		return model.Reservation{}, ErrNotFound
	}
	if r.Status == target {
		m.mu.Unlock()
		return r, nil
	}
	if r.Status.IsTerminal() {
		m.mu.Unlock()
		return model.Reservation{}, ErrConflict
	}

	// Commit the terminal transition atomically before invoking test code.
	r.Status = target
	m.reserves[id] = r
	hook := m.faultHook
	m.mu.Unlock()

	if hook != nil {
		if err := hook(r); err != nil {
			return r, err
		}
	}
	return r, nil
}

// Count returns the number of reservations. Exposed for testing.
func (m *Mock) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reserves)
}
