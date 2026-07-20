// Package runtime defines the runtime state port and its Mock/InMemory
// implementations for the Executor service.
package runtime

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when no runtime state exists for a target.
var ErrNotFound = errors.New("runtime state not found")

// RuntimeTarget identifies a runtime resource whose state is tracked.
type RuntimeTarget string

// Quarantine is the temporary exclusion state for a runtime target.
type Quarantine struct {
	Target RuntimeTarget
	Until  time.Time
	Reason string
}

// QuarantineInput supplies a quarantine state to store.
type QuarantineInput struct {
	Target RuntimeTarget
	Until  time.Time
	Reason string
}

// Snapshot holds the current runtime state.
type Snapshot struct {
	// StartTime is when the process started.
	StartTime time.Time
	// Version is the application version string.
	Version string
	// Uptime is the duration since start.
	Uptime time.Duration
}

// Port provides access to the Executor runtime state.
type Port interface {
	// Snapshot returns the current runtime snapshot.
	Snapshot(ctx context.Context) (Snapshot, error)

	// GetQuarantine returns the quarantine state for target, or ErrNotFound.
	GetQuarantine(ctx context.Context, target RuntimeTarget) (Quarantine, error)

	// SetQuarantine stores the quarantine state for input.Target.
	SetQuarantine(ctx context.Context, input QuarantineInput) error
}
