// Package quota defines the quota reservation port and its Mock/InMemory
// implementations for the Executor service. The terminal state machine is:
//
//	reserved → finalized   (allowed)
//	reserved → released    (allowed)
//	finalized → finalized  (idempotent)
//	released → released    (idempotent)
//	finalized → released   (conflict)
//	released → finalized   (conflict)
package quota

import (
	"context"
	"errors"

	"github.com/tokenmp/v3/services/executor/internal/model"
)

var (
	// ErrNotFound is returned when a reservation ID does not exist.
	ErrNotFound = errors.New("reservation not found")
	// ErrConflict is returned when attempting an opposite terminal transition.
	ErrConflict = errors.New("terminal status conflict")
)

// Port is the quota reservation port.
type Port interface {
	// Reserve creates a new reservation or returns an existing one.
	// Reserve is idempotent: calling it with the same ID returns the same reservation.
	Reserve(ctx context.Context, id string) (model.Reservation, error)

	// Finalize transitions a reservation to the finalized terminal state.
	// Same terminal is idempotent; opposite terminal returns ErrConflict;
	// unknown ID returns ErrNotFound.
	Finalize(ctx context.Context, id string) (model.Reservation, error)

	// Release transitions a reservation to the released terminal state.
	// Same terminal is idempotent; opposite terminal returns ErrConflict;
	// unknown ID returns ErrNotFound.
	Release(ctx context.Context, id string) (model.Reservation, error)
}
