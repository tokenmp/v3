package quota

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// TypedFaultHook runs after a typed terminal transition is committed. Returning
// an error leaves the committed state intact, allowing an exact replay.
type TypedFaultHook func(Reservation) error

// DomainInMemory implements Repository. It deliberately owns typed records
// separately from the legacy Port map until Phase 12.2 migrates execution.
type DomainInMemory struct {
	mu        sync.Mutex
	records   map[ReservationID]Reservation
	faultHook TypedFaultHook
}

var _ Repository = (*DomainInMemory)(nil)

func NewDomainInMemory() *DomainInMemory {
	return &DomainInMemory{records: make(map[ReservationID]Reservation)}
}

func (m *DomainInMemory) SetTypedFaultHook(hook TypedFaultHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faultHook = hook
}

func (m *DomainInMemory) ReserveReservation(ctx context.Context, in Reservation) (Reservation, error) {
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	if !in.ID.Valid() {
		return Reservation{}, ErrInvalidReservation
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	// An existing ID is a replay claim: it must match exactly, even when the
	// divergent input would itself fail current validation.
	if old, ok := m.records[in.ID]; ok {
		if !sameReservationClaim(old, in) {
			return Reservation{}, ErrConflict
		}
		return old.clone(), nil
	}
	if !in.validForReserve() {
		if !in.Metadata.valid() {
			return Reservation{}, ErrInvalidMetadata
		}
		return Reservation{}, ErrInvalidEstimate
	}
	in.CreatedAt = time.Now().UTC()
	m.records[in.ID] = in.clone()
	return in.clone(), nil
}

func (m *DomainInMemory) FinalizeReservation(ctx context.Context, id ReservationID, outcome FinalizeOutcome) (Reservation, error) {
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	if !id.Valid() {
		return Reservation{}, ErrInvalidReservation
	}
	if !outcome.valid() {
		return Reservation{}, ErrInvalidOutcome
	}
	return m.terminal(ctx, id, ReservationFinalized, TerminalSettlement{Outcome: &outcome})
}

func (m *DomainInMemory) ReleaseReservation(ctx context.Context, id ReservationID, reason ReleaseReason) (Reservation, error) {
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	if !id.Valid() {
		return Reservation{}, ErrInvalidReservation
	}
	if !reason.valid() {
		return Reservation{}, ErrInvalidRelease
	}
	return m.terminal(ctx, id, ReservationReleased, TerminalSettlement{Reason: &reason})
}

func (m *DomainInMemory) Lookup(ctx context.Context, id ReservationID) (Reservation, error) {
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	if !id.Valid() {
		return Reservation{}, ErrInvalidReservation
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reservation{}, err
	}
	r, ok := m.records[id]
	if !ok {
		return Reservation{}, ErrNotFound
	}
	return r.clone(), nil
}

func (m *DomainInMemory) terminal(ctx context.Context, id ReservationID, state ReservationState, settlement TerminalSettlement) (Reservation, error) {
	m.mu.Lock()
	// A context may be cancelled while waiting for the lock. Do not inspect or
	// mutate state after acquiring it in that case.
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return Reservation{}, err
	}
	r, ok := m.records[id]
	if !ok {
		m.mu.Unlock()
		return Reservation{}, ErrNotFound
	}
	if r.State == state {
		if !sameSettlement(r.Settlement, settlement) {
			m.mu.Unlock()
			return Reservation{}, ErrConflict
		}
		m.mu.Unlock()
		return r.clone(), nil
	}
	if r.State != ReservationReserved {
		m.mu.Unlock()
		return Reservation{}, ErrConflict
	}
	r.State, r.Settlement = state, settlement
	m.records[id] = r.clone()
	hook := m.faultHook
	committed := r.clone()
	m.mu.Unlock()
	// Hooks observe an already committed state. Context cancellation after this
	// point is intentionally the hook's responsibility.
	if hook != nil {
		if err := hook(committed); err != nil {
			return committed, err
		}
	}
	return committed, nil
}

func sameReservationClaim(stored, claim Reservation) bool {
	return claim.Metadata == stored.Metadata && claim.Estimate == stored.Estimate &&
		claim.State == ReservationReserved && claim.Settlement == (TerminalSettlement{}) && claim.CreatedAt.IsZero()
}

func sameSettlement(a, b TerminalSettlement) bool {
	if (a.Outcome == nil) != (b.Outcome == nil) || (a.Reason == nil) != (b.Reason == nil) {
		return false
	}
	if a.Outcome != nil && *a.Outcome != *b.Outcome {
		return false
	}
	return a.Reason == nil || *a.Reason == *b.Reason
}

func (m *DomainInMemory) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.records)
}

func (m *DomainInMemory) String() string   { return "quota.DomainInMemory{redacted}" }
func (m *DomainInMemory) GoString() string { return m.String() }
func (m *DomainInMemory) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, m.String())
}
