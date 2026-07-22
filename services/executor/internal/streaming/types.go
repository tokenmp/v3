// Package streaming implements the transport-neutral streaming execution
// boundary for Executor SSE responses.
//
// Scope: this package owns ONLY the protocol-neutral streaming state machine,
// the first-token commit gate, the TTFT / stream-idle / stream-lifetime timer
// control, and the pre-commit lifecycle buffer. It owns NO SSE framing, NO
// protocol-aware semantic detection, and NO downstream rendering. A caller
// (a future protocol-specific Source adapter) classifies upstream events into
// EventKind and supplies bounded safe tokens; the Bridge drives the lifecycle
// within the configured timeouts.
//
// The package imports no execution / sdk / provider / transport / runtime
// code. It never accepts or returns raw upstream bytes, protocol fields,
// interfaces to downstream renderers, or credential/URL/routing detail.
package streaming

import (
	"context"
	"errors"
)

// EventKind classifies one upstream stream event as the Bridge's state machine
// requires it. Classification is supplied by the protocol-specific Source; the
// streaming package never parses protocol bytes or detects semantic deltas.
type EventKind string

const (
	// EventLifecycle is a non-semantic upstream event: keepalive, open/close
	// markers, or any protocol frame that carries no first-token-eligible
	// progress. Pre-commit lifecycle events are buffered (bounded); after
	// commit they are forwarded downstream. Lifecycle events do NOT reset the
	// stream-idle timer.
	EventLifecycle EventKind = "lifecycle"

	// EventSemantic is a first-token-eligible delta. The first EventSemantic
	// triggers commit (the buffered lifecycle batch + the semantic event are
	// committed atomically at the Sink contract). After commit each
	// EventSemantic resets the stream-idle timer.
	EventSemantic EventKind = "semantic"

	// EventUsage carries safe usage counters. The optional Usage carried by ANY
	// Event Kind is accumulated monotonically and bounded by MaxTotal (see
	// Event.Usage); EventUsage is the explicit kind that is also forwarded
	// downstream post-commit. Pre-commit usage is discarded: the Outcome
	// reports zero usage on pre-commit failure (nothing committed downstream,
	// so the reservation is released and nothing is billable). Post-commit
	// usage is forwarded and recorded for the Outcome.
	EventUsage EventKind = "usage"

	// EventFinish is the clean terminal marker carrying a bounded safe
	// FinishReason. Pre-commit finish means no semantic content was produced
	// (a protocol failure). Post-commit finish is the only success path.
	EventFinish EventKind = "finish"

	// EventNativeError is a classified upstream error delivered in-stream. It
	// carries no raw body, message, URL, or credential. Pre-commit it is a safe
	// failure; post-commit it is a post-commit failure.
	EventNativeError EventKind = "native_error"
)

// Progress is optional protocol-neutral progress metadata. Its ONLY timer
// effect is to reset the stream-idle timer after commit; it is never buffered
// pre-commit and never forwarded downstream. Counters are bounded.
type Progress struct {
	// Processed is an advisory, monotonic, bounded count of units processed so
	// far. It is advisory only and never billed.
	Processed int64
}

// Usage carries safe usage counters. Each counter is monotonic non-decreasing
// across events and bounded by MaxTotal (configurable, hard-capped at 1e6).
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// Event is one strict protocol-neutral upstream stream event. It carries ONLY
// safe metadata: a Kind, optional bounded safe EventType/FinishReason tokens,
// optional Progress (idle-reset only) and optional Usage counters. It carries
// NO raw bytes, NO protocol fields, NO downstream renderer interface, and NO
// credential/URL/routing detail.
type Event struct {
	// Kind is the event classification. Required; the zero value is treated as
	// a protocol violation.
	Kind EventKind

	// EventType is an optional bounded safe token naming the upstream event
	// type (e.g. a sanitized SSE event name). Empty when not applicable. It is
	// reduced to the [A-Za-z0-9_.-] subset and bounded in length before use.
	EventType string

	// FinishReason is an optional bounded safe token carried only by
	// EventFinish. It is reduced to the [A-Za-z0-9_.-] subset and bounded.
	FinishReason string

	// Progress is optional and present only for advisory progress metadata. It
	// resets the stream-idle timer after commit and is otherwise unused.
	Progress *Progress

	// Usage is optional safe usage counters. It is accumulated monotonically
	// and bounded; it is recorded for the Outcome only after commit —
	// pre-commit usage is discarded (pre-commit failure reports zero usage).
	Usage *Usage
}

// Source is the protocol-neutral upstream stream event source. A
// protocol-specific adapter implements it; the Bridge never parses upstream
// bytes or detects semantic deltas.
//
// Next blocks until the next event is available, ctx is cancelled, or the
// upstream stream ends cleanly. It MUST honor ctx: on cancellation it returns
// ctx.Err() promptly (without relying on Close to unblock it). It returns
// ErrEndOfStream when the upstream stream has terminated cleanly and no
// further events will be produced. Any other non-nil error is treated by the
// Bridge as a native_error (classified upstream failure); a well-behaved
// Source yields EventNativeError instead.
//
// Close is idempotent, safe to call concurrently with an in-flight Next, and
// non-blocking (or bounded). It MUST unblock Next where that is possible for
// the adapter. On exit the Bridge cancels Next's context, calls Close, then
// waits for its pump goroutine before Run returns. This is necessary to avoid
// leaving an upstream-read goroutine behind. A Source that has been closed
// must not be reused.
type Source interface {
	Next(ctx context.Context) (Event, error)
	Close() error
}

// Sink is the downstream emission boundary. It renders protocol-native SSE
// from the protocol-neutral Events the Bridge forwards; the Bridge never
// renders bytes itself.
//
// Commit writes an entire batch of events and flushes. Success means the whole
// batch was written and flushed to the downstream. Failure means the downstream
// state is uncertain (some, all, or no bytes may have been written) and the
// Bridge MUST NOT retry: it resolves a post-commit failure without further
// source reads or writes. The atomicity is at the Sink contract only — the
// Bridge does NOT claim atomic HTTP (the underlying transport may split or
// partially flush).
//
// WriteEvent writes one post-commit event (it need not flush).
// Flush flushes any buffered writes.
type Sink interface {
	Commit(ctx context.Context, events []Event) error
	WriteEvent(ctx context.Context, event Event) error
	Flush(ctx context.Context) error
}

// Safe sentinel errors. Each is a non-wrapping errors.New value:
// errors.Unwrap returns nil, and Error() returns a fixed string that never
// echoes an upstream message, body, URL, credential, or routing detail.
//
// Runtime outcomes are surfaced via (Outcome, error): pre-commit failures
// return a non-nil sentinel error so a transport may render a protocol-native
// HTTP error response (no stream opened); post-commit failures return a nil
// error with a failed Outcome (the stream was already opened and must be
// closed per the protocol-native failure policy).
var (
	// ErrMisconfigured means a required Bridge dependency is nil/typed-nil or
	// the effective timeouts/limits are invalid. Returned before any upstream
	// call, timer arm, or state transition; also returned for a double Run.
	ErrMisconfigured = errors.New("streaming: bridge misconfigured")

	// ErrProtocol is a pre-commit protocol violation: the upstream stream ended
	// cleanly before any semantic content was produced, or a finish/empty
	// stream arrived pre-commit. No downstream bytes were written.
	ErrProtocol = errors.New("streaming: protocol")

	// ErrTTFTTimeout means the first semantic event did not arrive within the
	// TTFT budget. Pre-commit failure.
	ErrTTFTTimeout = errors.New("streaming: ttft timeout")

	// ErrStreamIdle means no idle-resetting event (semantic or progress)
	// arrived within the stream-idle budget after commit. The idle timer is
	// armed only after a successful commit, so this is always a post-commit
	// failure: it is surfaced via Outcome.Reason (ReasonStreamIdle) with a nil
	// returned error rather than as a returned sentinel. It is declared for
	// symmetry with the other safe timeout sentinels and so a caller can map
	// it via errors.Is if it is ever surfaced.
	ErrStreamIdle = errors.New("streaming: stream idle timeout")

	// ErrStreamLifetime means the total stream duration exceeded the
	// stream-lifetime hard cap. May fire pre- or post-commit; pre-commit it is
	// returned as this sentinel, post-commit it is a failed Outcome.
	ErrStreamLifetime = errors.New("streaming: stream lifetime exceeded")

	// ErrUpstreamError means the upstream delivered a classified native_error
	// pre-commit. Post-commit it is a failed Outcome (nil error).
	ErrUpstreamError = errors.New("streaming: upstream error")

	// ErrBufferOverflow means the pre-commit lifecycle buffer exceeded its
	// count or metadata-byte budget. Pre-commit failure.
	ErrBufferOverflow = errors.New("streaming: buffer overflow")

	// ErrEventLimit means the total number of Events received from the Source
	// exceeded the configured MaxEvents. Pre-commit it is returned as this
	// sentinel (no downstream bytes written); post-commit it is a failed
	// Outcome (nil error, stream already opened). The Bridge never retries:
	// the exceeding Event is neither processed nor written downstream.
	ErrEventLimit = errors.New("streaming: event limit exceeded")

	// ErrEndOfStream is returned by a Source when the upstream stream has
	// terminated cleanly and no further events will be produced. It is not a
	// failure sentinel; the Bridge maps it per the commit phase.
	ErrEndOfStream = errors.New("streaming: end of stream")
)

// Bounding constants for safe tokens and the pre-commit buffer.
const (
	// maxTokenBytes bounds any safe EventType/FinishReason token so a
	// misbehaving Source cannot flood downstream metadata or the Outcome.
	maxTokenBytes = 128

	// MaxBufferedLifecycle is the maximum number of pre-commit lifecycle
	// events the Bridge buffers before commit. Exceeding it means the upstream
	// is flooding without producing a semantic token, so the Bridge fails
	// before commit rather than growing unbounded.
	MaxBufferedLifecycle = 32

	// MaxBufferedMetadataBytes is the metadata byte budget for the pre-commit
	// lifecycle buffer. The sum of sanitized metadata sizes of buffered events
	// must not exceed it.
	MaxBufferedMetadataBytes = 16 * 1024

	// MaxTotalHardCap is the absolute upper bound on the configurable
	// MaxTotal usage limit. A configured MaxTotal above this is misconfiguration.
	MaxTotalHardCap int64 = 1_000_000

	// MaxEventsHardCap is the absolute upper bound on the configurable
	// MaxEvents total-event limit. A configured MaxEvents above this is
	// misconfiguration. It caps how many Events the Bridge accepts from the
	// Source in one Run so a runaway upstream cannot flood downstream
	// indefinitely. MaxEvents is independent of MaxTotal (the usage-counter
	// cap): it counts received Events, not usage counters.
	MaxEventsHardCap int64 = 1_000_000

	// DefaultMaxEvents is the safe default total-event limit applied when
	// MaxEvents is zero. It is well below the hard cap so a misconfigured or
	// runaway upstream cannot flood downstream indefinitely while still
	// allowing large legitimate streams.
	DefaultMaxEvents int64 = 100_000
)

// sanitizeToken reduces an upstream-supplied token to the safe
// [A-Za-z0-9_.-] subset, bounded in length. Anything outside that set (or too
// long) reduces to empty so arbitrary remote content cannot reach downstream
// metadata or the Outcome.
func sanitizeToken(v string) string {
	if len(v) == 0 || len(v) > maxTokenBytes {
		return ""
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			continue
		case r == '_' || r == '-' || r == '.':
			continue
		default:
			return ""
		}
	}
	return v
}

// eventMetadataSize returns the metadata size of one event as received from
// the Source, used to enforce the pre-commit buffer byte budget. It counts the
// RAW (pre-sanitization) token lengths so a single event carrying a huge
// upstream-supplied token trips the byte budget even though the stored event is
// sanitized down. It counts only metadata; it never counts raw bytes (there are
// none) and counters are charged a fixed cost.
func eventMetadataSize(ev Event) int {
	n := len(ev.Kind)
	n += len(ev.EventType)
	n += len(ev.FinishReason)
	if ev.Progress != nil {
		n += 32
	}
	if ev.Usage != nil {
		n += 48
	}
	return n
}

// sanitizeEvent returns a copy of ev with all tokens bounded and usage counters
// clamped to [0, maxTotal]. It is the single intake boundary so a misbehaving
// Source cannot push unbounded or unsafe content past the Bridge intake.
func sanitizeEvent(ev Event, maxTotal int64) Event {
	out := ev
	out.EventType = sanitizeToken(ev.EventType)
	out.FinishReason = sanitizeToken(ev.FinishReason)
	if ev.Progress != nil {
		p := clampCounter(ev.Progress.Processed, maxTotal)
		out.Progress = &Progress{Processed: p}
	}
	if ev.Usage != nil {
		out.Usage = &Usage{
			PromptTokens:     clampCounter(ev.Usage.PromptTokens, maxTotal),
			CompletionTokens: clampCounter(ev.Usage.CompletionTokens, maxTotal),
			TotalTokens:      clampCounter(ev.Usage.TotalTokens, maxTotal),
		}
	}
	return out
}

// clampCounter clamps a usage/progress counter to [0, maxTotal].
func clampCounter(v, maxTotal int64) int64 {
	if v < 0 {
		return 0
	}
	if v > maxTotal {
		return maxTotal
	}
	return v
}

// mergeUsage accumulates incoming usage into current monotonically (each
// counter never decreases) and bounded by maxTotal.
func mergeUsage(cur, in Usage, maxTotal int64) Usage {
	return Usage{
		PromptTokens:     monoClamp(cur.PromptTokens, in.PromptTokens, maxTotal),
		CompletionTokens: monoClamp(cur.CompletionTokens, in.CompletionTokens, maxTotal),
		TotalTokens:      monoClamp(cur.TotalTokens, in.TotalTokens, maxTotal),
	}
}

// monoClamp returns max(cur, in) clamped to [0, maxTotal].
func monoClamp(cur, in, maxTotal int64) int64 {
	v := in
	if v < cur {
		v = cur
	}
	return clampCounter(v, maxTotal)
}
