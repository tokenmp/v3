package quota

import (
	"context"
	"sync"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/model"
)

// FaultHook is called during terminal transitions for testing.
// If set, the hook receives the reservation AFTER the state transition has been
// committed. If the hook returns a non-nil error, the transition is still
// committed (the reservation is in the new terminal state), but the error is
// returned to the caller. This enables testing fault-recovery scenarios:
// retrying the same terminal on the same ID will be idempotent, and attempting
// the opposite terminal will return ErrConflict.
type FaultHook func(reservation model.Reservation) error

// InMemory implements Port using an in-memory map with a mutex.
type InMemory struct {
	mu        sync.Mutex
	reserves  map[string]model.Reservation
	faultHook FaultHook
}

var _ Port = (*InMemory)(nil)

// NewInMemory creates an empty InMemory quota port.
func NewInMemory() *InMemory {
	return &InMemory{
		reserves: make(map[string]model.Reservation),
	}
}

// SetFaultHook sets the fault hook for testing. Pass nil to clear.
func (m *InMemory) SetFaultHook(hook FaultHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faultHook = hook
}

// Reserve creates or returns an existing reservation. It is idempotent.
func (m *InMemory) Reserve(_ context.Context, id string) (model.Reservation, error) {
	if !validID(id) {
		return model.Reservation{}, ErrInvalidID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.reserves[id]; ok {
		return r, nil
	}

	r := model.Reservation{
		ID:        id,
		Status:    model.StatusReserved,
		CreatedAt: time.Now(),
	}
	m.reserves[id] = r
	return r, nil
}

// Finalize transitions a reservation to the finalized terminal state.
func (m *InMemory) Finalize(ctx context.Context, id string) (model.Reservation, error) {
	return m.transition(ctx, id, model.StatusFinalized)
}

// Release transitions a reservation to the released terminal state.
func (m *InMemory) Release(ctx context.Context, id string) (model.Reservation, error) {
	return m.transition(ctx, id, model.StatusReleased)
}

func (m *InMemory) transition(_ context.Context, id string, target model.ReservationStatus) (model.Reservation, error) {
	if !validID(id) {
		return model.Reservation{}, ErrInvalidID
	}

	m.mu.Lock()

	r, ok := m.reserves[id]
	if !ok {
		m.mu.Unlock()
		return model.Reservation{}, ErrNotFound
	}

	// Same terminal: idempotent.
	if r.Status == target {
		m.mu.Unlock()
		return r, nil
	}

	// Opposite terminal: conflict.
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
func (m *InMemory) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.reserves)
}
