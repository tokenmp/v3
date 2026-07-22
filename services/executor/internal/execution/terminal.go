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
	repo          quota.Repository
	reservationID quota.ReservationID
	finalize      quota.FinalizeRequest
	release       quota.ReleaseRequest

	mu     sync.Mutex
	intent terminalIntent
	done   chan struct{}
	err    error
}

// NewTerminalizer creates a terminalizer for reservationID. It does not call
// Reserve or validate reservation state: the Runner is responsible for
// constructing it only after a successful reservation.
func NewTerminalizer(repo quota.Repository, reservationID quota.ReservationID) *Terminalizer {
	return &Terminalizer{repo: repo, reservationID: reservationID}
}

// Finalize selects finalization as this reservation's terminal intent.
func (t *Terminalizer) Finalize(ctx context.Context, outcome quota.FinalizeOutcome) error {
	return t.terminal(ctx, terminalIntentFinalize, quota.FinalizeRequest{ID: t.reservationID, Outcome: outcome}, quota.ReleaseRequest{})
}

// Release selects release as this reservation's terminal intent.
func (t *Terminalizer) Release(ctx context.Context, reason quota.ReleaseReason) error {
	return t.terminal(ctx, terminalIntentRelease, quota.FinalizeRequest{}, quota.ReleaseRequest{ID: t.reservationID, Reason: reason})
}

func (t *Terminalizer) terminal(ctx context.Context, requested terminalIntent, finalize quota.FinalizeRequest, release quota.ReleaseRequest) error {
	t.mu.Lock()
	if t.intent != terminalIntentNone {
		if t.intent != requested ||
			(requested == terminalIntentFinalize && !sameFinalizeRequest(t.finalize, finalize)) ||
			(requested == terminalIntentRelease && !sameReleaseRequest(t.release, release)) {
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
	t.finalize, t.release = finalize, release
	t.done = make(chan struct{})
	done := t.done
	t.mu.Unlock()

	var err error
	switch requested {
	case terminalIntentFinalize:
		_, err = t.repo.FinalizeReservation(ctx, t.finalize)
	case terminalIntentRelease:
		_, err = t.repo.ReleaseReservation(ctx, t.release)
	default:
		panic("execution: unknown terminal intent")
	}

	t.mu.Lock()
	t.err = err
	close(done)
	t.mu.Unlock()
	return err
}

// These value-only comparisons are race-safe under Terminalizer.mu and avoid
// reflect on a terminal settlement path. They intentionally compare every
// request field: only an exact replay may wait for/replay the first result.
func sameFinalizeRequest(a, b quota.FinalizeRequest) bool {
	return a.ID == b.ID && a.Outcome.Disposition == b.Outcome.Disposition &&
		a.Outcome.Outcome == b.Outcome.Outcome &&
		a.Outcome.Usage.InputTokens == b.Outcome.Usage.InputTokens &&
		a.Outcome.Usage.OutputTokens == b.Outcome.Usage.OutputTokens &&
		a.Outcome.Usage.TotalTokens == b.Outcome.Usage.TotalTokens
}

func sameReleaseRequest(a, b quota.ReleaseRequest) bool {
	return a.ID == b.ID && a.Reason == b.Reason
}
