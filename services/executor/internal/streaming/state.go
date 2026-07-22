package streaming

import "errors"

// State is a streaming request lifecycle state. States follow the state
// machine defined in docs/executor/architecture.md §11. Only the transitions
// encoded in [validTransition] are permitted; any other move is a programming
// fault.
type State string

const (
	// StateInit is the initial state before Run has armed timers.
	StateInit State = "init"

	// StateConnecting is the state while the upstream stream is being
	// established but no event has yet been received.
	StateConnecting State = "connecting"

	// StateWaitingFirstSemanticEvent is the state after at least one lifecycle
	// event has been received but before the first semantic event. Lifecycle
	// events are buffered; commit has not occurred.
	StateWaitingFirstSemanticEvent State = "waiting_first_semantic_event"

	// StateCommitted is the state immediately after the first semantic event
	// has been committed to the downstream sink. The idle timer is armed.
	StateCommitted State = "committed"

	// StateStreaming is the steady state while semantic deltas continue to
	// flow after commit. The idle timer is armed and reset on each
	// semantic/progress event.
	StateStreaming State = "streaming"

	// StateCompleted is the terminal success state: the upstream stream ended
	// cleanly after commit via an EventFinish and the terminal was emitted.
	StateCompleted State = "completed"

	// StateFailedBeforeCommit is a terminal failure state reached before any
	// semantic event was committed. No downstream bytes were written.
	StateFailedBeforeCommit State = "failed_before_commit"

	// StateFailedAfterCommit is a terminal failure state reached after commit.
	// The downstream stream was already opened; the Bridge resolves a failure
	// without retry and the transport closes it per the protocol-native
	// failure policy.
	StateFailedAfterCommit State = "failed_after_commit"

	// StateClientCancelled is a terminal state reached when the caller context
	// is cancelled. The Committed flag distinguishes pre/post-commit cancel.
	StateClientCancelled State = "client_cancelled"
)

// Terminal reports whether s is a terminal state (no further transitions).
func (s State) Terminal() bool {
	switch s {
	case StateCompleted, StateFailedBeforeCommit, StateFailedAfterCommit, StateClientCancelled:
		return true
	default:
		return false
	}
}

// Committed reports whether s is at or after the commit point (committed,
// streaming, completed, failed_after_commit). A request that reached commit
// has opened a downstream stream and must be closed by the transport.
func (s State) Committed() bool {
	switch s {
	case StateCommitted, StateStreaming, StateCompleted, StateFailedAfterCommit:
		return true
	default:
		return false
	}
}

// validTransition reports whether moving from s to next is a permitted state
// machine transition. It is the single source of truth for state legality; the
// Bridge asserts it before recording any state change.
func validTransition(s, next State) bool {
	switch s {
	case StateInit:
		return next == StateConnecting || next == StateFailedBeforeCommit || next == StateClientCancelled
	case StateConnecting:
		return next == StateWaitingFirstSemanticEvent || next == StateCommitted ||
			next == StateFailedBeforeCommit || next == StateClientCancelled
	case StateWaitingFirstSemanticEvent:
		return next == StateCommitted || next == StateFailedBeforeCommit || next == StateClientCancelled
	case StateCommitted:
		return next == StateStreaming || next == StateCompleted ||
			next == StateFailedAfterCommit || next == StateClientCancelled
	case StateStreaming:
		return next == StateStreaming || next == StateCompleted ||
			next == StateFailedAfterCommit || next == StateClientCancelled
	default:
		return false
	}
}

// transition asserts a legal state machine move and records it. An illegal
// transition is a programming fault; it records a pre-commit failure so the
// outcome remains a terminal state the caller can act on.
func transition(state *State, next State) {
	if !validTransition(*state, next) {
		if !(*state).Terminal() {
			*state = StateFailedBeforeCommit
		}
		return
	}
	*state = next
}

// Reason is the safe, sanitized reason a stream reached a terminal state. It
// carries no upstream message, body, URL, credential, or routing detail.
type Reason string

const (
	ReasonCompleted       Reason = "completed"
	ReasonTTFTTimeout     Reason = "ttft_timeout"
	ReasonStreamIdle      Reason = "stream_idle"
	ReasonStreamLifetime  Reason = "stream_lifetime"
	ReasonProtocol        Reason = "protocol"
	ReasonUpstreamError   Reason = "upstream_error"
	ReasonStreamTruncated Reason = "stream_truncated"
	ReasonBufferOverflow  Reason = "buffer_overflow"
	ReasonCommitFailed    Reason = "commit_failed"
	ReasonSinkWrite       Reason = "sink_write"
	ReasonClientCancelled Reason = "client_cancelled"
	ReasonEventLimit      Reason = "event_limit"
)

// reasonForError maps a sentinel error to its Reason. An unknown error is
// reduced to ReasonUpstreamError so an arbitrary internal error can never
// surface an unsafe reason string.
func reasonForError(err error) Reason {
	switch {
	case err == nil:
		return ReasonCompleted
	case errors.Is(err, ErrTTFTTimeout):
		return ReasonTTFTTimeout
	case errors.Is(err, ErrStreamIdle):
		return ReasonStreamIdle
	case errors.Is(err, ErrStreamLifetime):
		return ReasonStreamLifetime
	case errors.Is(err, ErrUpstreamError):
		return ReasonUpstreamError
	case errors.Is(err, ErrProtocol):
		return ReasonProtocol
	case errors.Is(err, ErrBufferOverflow):
		return ReasonBufferOverflow
	case errors.Is(err, ErrEventLimit):
		return ReasonEventLimit
	default:
		return ReasonUpstreamError
	}
}

// cancelState returns the terminal state for a client cancellation. Both pre-
// and post-commit cancellation map to StateClientCancelled; the Committed flag
// in the Outcome distinguishes them.
func cancelState(committed bool) State {
	_ = committed
	return StateClientCancelled
}
