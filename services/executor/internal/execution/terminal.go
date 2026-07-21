// Package execution contains request-lifecycle helpers for the Executor.
package execution

import (
	"context"
	"errors"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/quota"
)

var (
	// ErrTerminalConflict is returned when a request has already chosen the
	// opposite quota terminal intent. The first intent wins even when its port
	// call returns an error, so a caller must never compensate a failed
	// Finalize with Release (or vice versa).
	ErrTerminalConflict = errors.New("execution: terminal intent conflict")
)

type terminalIntent uint8

const (
	terminalIntentNone terminalIntent = iota
	terminalIntentFinalize
	terminalIntentRelease
)

// Terminalizer performs the one quota terminal action selected for a single
// already-reserved reservation. Runner owns Reserve and must construct a
// Terminalizer only after Reserve succeeds. Runner also supplies a cleanup
// context when it needs cleanup to outlive request cancellation, for example:
//
//	cleanup, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), timeout)
//	defer cancel()
//	_ = terminalizer.Release(cleanup)
//
// Terminalizer is safe for concurrent use. It records the first intent before
// calling the port; therefore it calls the port at most once. Replaying that
// intent waits for and returns the original result. The opposite intent always
// returns ErrTerminalConflict and never calls the port, including when the
// selected port operation fails after committing remotely.
type Terminalizer struct {
	port          quota.Port
	reservationID string

	mu     sync.Mutex
	intent terminalIntent
	done   chan struct{}
	err    error
}

// NewTerminalizer creates a terminalizer for reservationID. It does not call
// Reserve or validate reservation state: the Runner is responsible for
// constructing it only after a successful reservation.
func NewTerminalizer(port quota.Port, reservationID string) *Terminalizer {
	return &Terminalizer{port: port, reservationID: reservationID}
}

// Finalize selects finalization as this reservation's terminal intent.
func (t *Terminalizer) Finalize(ctx context.Context) error {
	return t.terminal(ctx, terminalIntentFinalize)
}

// Release selects release as this reservation's terminal intent.
func (t *Terminalizer) Release(ctx context.Context) error {
	return t.terminal(ctx, terminalIntentRelease)
}

func (t *Terminalizer) terminal(ctx context.Context, requested terminalIntent) error {
	t.mu.Lock()
	if t.intent != terminalIntentNone {
		if t.intent != requested {
			t.mu.Unlock()
			return ErrTerminalConflict
		}
		done := t.done
		t.mu.Unlock()
		<-done

		t.mu.Lock()
		err := t.err
		t.mu.Unlock()
		return err
	}

	t.intent = requested
	t.done = make(chan struct{})
	done := t.done
	t.mu.Unlock()

	var err error
	switch requested {
	case terminalIntentFinalize:
		_, err = t.port.Finalize(ctx, t.reservationID)
	case terminalIntentRelease:
		_, err = t.port.Release(ctx, t.reservationID)
	default:
		panic("execution: unknown terminal intent")
	}

	t.mu.Lock()
	t.err = err
	close(done)
	t.mu.Unlock()
	return err
}
