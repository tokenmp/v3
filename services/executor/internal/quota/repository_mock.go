package quota

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// TypedCallRecord records a typed Repository call without exposing metadata or
// settlement values, which may contain identifiers not appropriate for logs.
type TypedCallRecord struct {
	Method string
	ID     ReservationID
}

// TypedMock is a Repository test double with the same semantic state machine
// as DomainInMemory, injectable method overrides, post-commit fault injection,
// and defensive call recording.
type TypedMock struct {
	mu sync.Mutex
	*DomainInMemory
	calls      []TypedCallRecord
	reserveFn  func(context.Context, ReserveRequest) (Reservation, error)
	finalizeFn func(context.Context, FinalizeRequest) (Reservation, error)
	releaseFn  func(context.Context, ReleaseRequest) (Reservation, error)
	lookupFn   func(context.Context, ReservationID) (Reservation, error)
}

var _ Repository = (*TypedMock)(nil)

func NewTypedMock() *TypedMock { return &TypedMock{DomainInMemory: NewDomainInMemory()} }

func (m *TypedMock) SetReserveReservationFn(fn func(context.Context, ReserveRequest) (Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserveFn = fn
}
func (m *TypedMock) SetFinalizeReservationFn(fn func(context.Context, FinalizeRequest) (Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalizeFn = fn
}
func (m *TypedMock) SetReleaseReservationFn(fn func(context.Context, ReleaseRequest) (Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseFn = fn
}
func (m *TypedMock) SetLookupFn(fn func(context.Context, ReservationID) (Reservation, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookupFn = fn
}
func (m *TypedMock) TypedCalls() []TypedCallRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TypedCallRecord, len(m.calls))
	copy(out, m.calls)
	return out
}
func (m *TypedMock) typedCall(method string, id ReservationID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, TypedCallRecord{Method: method, ID: id})
}
func (m *TypedMock) ReserveReservation(ctx context.Context, r ReserveRequest) (Reservation, error) {
	m.typedCall("ReserveReservation", r.ID)
	m.mu.Lock()
	fn := m.reserveFn
	m.mu.Unlock()
	if fn != nil {
		if err := ctx.Err(); err != nil {
			return Reservation{}, err
		}
		return fn(ctx, r)
	}
	return m.DomainInMemory.ReserveReservation(ctx, r)
}
func (m *TypedMock) FinalizeReservation(ctx context.Context, in FinalizeRequest) (Reservation, error) {
	m.typedCall("FinalizeReservation", in.ID)
	m.mu.Lock()
	fn := m.finalizeFn
	m.mu.Unlock()
	if fn != nil {
		if err := ctx.Err(); err != nil {
			return Reservation{}, err
		}
		return fn(ctx, in)
	}
	return m.DomainInMemory.FinalizeReservation(ctx, in)
}
func (m *TypedMock) ReleaseReservation(ctx context.Context, in ReleaseRequest) (Reservation, error) {
	m.typedCall("ReleaseReservation", in.ID)
	m.mu.Lock()
	fn := m.releaseFn
	m.mu.Unlock()
	if fn != nil {
		if err := ctx.Err(); err != nil {
			return Reservation{}, err
		}
		return fn(ctx, in)
	}
	return m.DomainInMemory.ReleaseReservation(ctx, in)
}
func (m *TypedMock) Lookup(ctx context.Context, id ReservationID) (Reservation, error) {
	m.typedCall("Lookup", id)
	m.mu.Lock()
	fn := m.lookupFn
	m.mu.Unlock()
	if fn != nil {
		if err := ctx.Err(); err != nil {
			return Reservation{}, err
		}
		return fn(ctx, id)
	}
	return m.DomainInMemory.Lookup(ctx, id)
}

// String, GoString, and Format make call logs and test doubles safe to pass to
// generic formatters. The method is retained because it is not sensitive.
func (r TypedCallRecord) String() string {
	return "quota.TypedCallRecord{method:" + r.Method + ",redacted}"
}
func (r TypedCallRecord) GoString() string { return r.String() }
func (r TypedCallRecord) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, r.String())
}

func (m *TypedMock) String() string   { return "quota.TypedMock{redacted}" }
func (m *TypedMock) GoString() string { return m.String() }
func (m *TypedMock) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, m.String())
}
