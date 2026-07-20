// Package model defines stable internal domain types for the Executor service.
package model

import "time"

// ReservationStatus represents the lifecycle state of a quota reservation.
type ReservationStatus string

const (
	// StatusReserved is the initial state after a reservation is created.
	StatusReserved ReservationStatus = "reserved"
	// StatusFinalized is the terminal state indicating successful completion.
	StatusFinalized ReservationStatus = "finalized"
	// StatusReleased is the terminal state indicating the reservation was released.
	StatusReleased ReservationStatus = "released"
)

// IsTerminal reports whether s is a terminal status.
func (s ReservationStatus) IsTerminal() bool {
	return s == StatusFinalized || s == StatusReleased
}

// Reservation represents a quota reservation.
type Reservation struct {
	ID        string
	Status    ReservationStatus
	CreatedAt time.Time
}
