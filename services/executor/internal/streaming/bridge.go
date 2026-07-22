package streaming

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Outcome is the terminal result of one streaming request. It carries only
// safe, sanitized state: the terminal State, the Reason, the Committed flag,
// the accumulated bounded Usage, the bounded safe Finish token, an
// UnresolvedCost flag, and the measured TTFT. It never carries an upstream
// body, request content, credential, URL, or routing detail.
type Outcome struct {
	// State is the terminal state the Bridge reached.
	State State

	// Reason is the safe terminal reason (ReasonCompleted on success).
	Reason Reason

	// Committed reports whether the stream reached commit (opened a downstream
	// stream). A transport uses it to decide between an HTTP error response
	// (pre-commit) and closing an already-open stream (post-commit).
	Committed bool

	// Usage is the final accumulated, monotonic, bounded usage. It is zero on
	// pre-commit failure.
	Usage Usage

	// Finish is the bounded safe finish reason on success (StateCompleted). It
	// is empty otherwise.
	Finish string

	// UnresolvedCost is true when the stream committed but no confirmed usage
	// was received (e.g. post-commit failure or finish without usage). Per the
	// billing contract the reservation is released and the unresolved cost is
	// recorded for future reconciliation; the Bridge never guesses a charge.
	UnresolvedCost bool

	// TTFT is the elapsed time from Run start to commit (the first semantic
	// event). It is zero when the stream never committed.
	TTFT time.Duration
}

// Bridge drives one streaming request lifecycle within the configured
// timeouts. It composes a protocol-neutral Source (upstream events), a Sink
// (downstream emission), the TTFT/idle/lifetime timers, and the streaming
// state machine.
//
// The Bridge is not safe for concurrent reuse: one Run drives one request. It
// performs no I/O of its own beyond the injected Source, Sink, and
// TimerSource; the real TimerSource uses time.Timer.
type Bridge struct {
	// Source is the protocol-neutral upstream stream event source. Required.
	Source Source

	// Sink is the downstream emission boundary. Required.
	Sink Sink

	// Timeouts are the effective, validated streaming timeouts. Required and
	// must pass Validate.
	Timeouts Timeouts

	// Timers supplies resettable timers. A nil value uses the real time-based
	// source.
	Timers TimerSource

	// Clock supplies the authoritative instant for TTFT, lifetime, and idle
	// deadline decisions. A nil value uses wall time; timer channels only wake
	// the Bridge and never decide timeout correctness.
	Clock Clock

	// MaxTotal is the configurable usage cap (each counter clamped to it). It
	// must be in (0, MaxTotalHardCap]; zero defaults to MaxTotalHardCap. A
	// negative value or a value above MaxTotalHardCap is misconfiguration.
	MaxTotal int64

	// MaxEvents is the configurable total-event limit: the maximum number of
	// Events the Bridge accepts from the Source in one Run. Every received
	// Event is counted before processing; the (MaxEvents+1)th Event fails
	// safely (pre-commit sentinel, post-commit nil-error Outcome) without
	// retries, and the exceeding Event is neither processed nor written. It
	// must be in (0, MaxEventsHardCap]; zero defaults to DefaultMaxEvents. A
	// negative value or a value above MaxEventsHardCap is misconfiguration.
	// MaxEvents is independent of MaxTotal, which remains the usage-counter
	// cap.
	MaxEvents int64

	runOnce atomic.Bool // guards single-use
}

// Run drives the request lifecycle and returns the terminal Outcome.
//
// Pre-commit failures (protocol, TTFT, pre-commit lifetime, buffer overflow,
// pre-commit native_error, misconfiguration, double Run) return a non-nil
// sentinel error so a transport may render a protocol-native HTTP error
// response (no stream opened). Post-commit failures (post-commit lifetime,
// idle, sink write/flush/commit failure, post-commit native_error, truncated
// EOF) return a nil error with a failed Outcome (the stream was already
// opened). Success returns a nil error with StateCompleted. Client
// cancellation returns a nil error with StateClientCancelled.
func (b *Bridge) Run(ctx context.Context) (Outcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Outcome{State: StateClientCancelled, Reason: ReasonClientCancelled}, nil
	}
	if b == nil {
		return Outcome{}, ErrMisconfigured
	}
	maxTotal := b.MaxTotal
	if maxTotal == 0 {
		maxTotal = MaxTotalHardCap
	}
	maxEvents := b.MaxEvents
	if maxEvents == 0 {
		maxEvents = DefaultMaxEvents
	}
	if isNilInterface(b.Source) || isNilInterface(b.Sink) || isTypedNil(b.Timers) ||
		isTypedNil(b.Clock) || !b.Timeouts.Validate() ||
		maxTotal < 0 || maxTotal > MaxTotalHardCap ||
		maxEvents < 0 || maxEvents > MaxEventsHardCap {
		return Outcome{}, ErrMisconfigured
	}
	if !b.runOnce.CompareAndSwap(false, true) {
		return Outcome{}, ErrMisconfigured
	}

	timers := timerSourceOr(b.Timers)
	clock := clockOr(b.Clock)
	sink := b.Sink
	source := b.Source

	var state State = StateInit
	transition(&state, StateConnecting)
	started := clock.Now()
	lifetimeDeadline := started.Add(b.Timeouts.StreamLifetime)
	ttftDeadline := started.Add(b.Timeouts.TTFT)

	ttft := timers.NewTimer(b.Timeouts.TTFT)
	defer ttft.Stop()
	// ttftChan is the TTFT timer's Done channel snapshot used in every select.
	// It is set to nil the instant commit succeeds: once the first semantic
	// event has been committed, the TTFT budget is permanently consumed and a
	// fired-but-not-selected TTFT timer must NEVER cause a post-commit TTFT
	// failure. A nil channel is never selected by the Go select statement, so
	// any value already buffered in the timer channel after Stop is rendered
	// unreachable. (ttft.Stop alone is insufficient: a *time.Timer that fired
	// before Stop leaves a value in C that remains selectable.)
	ttftChan := ttft.Done()
	lifetime := timers.NewTimer(b.Timeouts.StreamLifetime)
	defer lifetime.Stop()
	var idle Timer

	var (
		buffer       []Event // pre-commit lifecycle buffer
		bufferBytes  int
		usage        Usage
		hasUsage     bool
		committed    bool
		ttftElapsed  time.Duration
		lastProgress time.Time // authoritative idle-deadline anchor after commit
		eventsSeen   int64
		lastSequence uint64
	)

	type recvResult struct {
		ev  Event
		err error
	}
	events := make(chan recvResult, 1)
	pumpCtx, cancelPump := context.WithCancel(ctx)
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			ev, err := source.Next(pumpCtx)
			select {
			case events <- recvResult{ev: ev, err: err}:
			case <-pumpCtx.Done():
				return
			}
			if errors.Is(err, ErrEndOfStream) {
				return
			}
			if err != nil && isCtxErr(err) {
				return
			}
		}
	}()
	var closeOnce sync.Once
	// Exit ordering deliberately permits Close|Next concurrency: cancel first,
	// then Close so an adapter can unblock an in-flight read, then wait for the
	// pump before Run returns. Source requires Close to be concurrent-safe and
	// non-blocking/bounded; a malicious source that violates that contract can
	// still prevent Run from returning.
	defer func() {
		cancelPump()
		closeOnce.Do(func() { _ = source.Close() })
		<-pumpDone
	}()

	// preFail returns a pre-commit failure outcome with the given safe sentinel.
	// Pre-commit failures report ZERO usage: nothing was committed downstream,
	// so per the billing contract the reservation is released (no charge, no
	// unresolved cost). Any usage accumulated pre-commit is discarded so a
	// failed stream that never committed cannot be mis-billed.
	preFail := func(reason Reason, err error) (Outcome, error) {
		return Outcome{
			State:  StateFailedBeforeCommit,
			Reason: reason,
			Usage:  Usage{},
			TTFT:   0,
		}, err
	}
	// postFail returns a post-commit failure outcome (nil error). Downstream is
	// uncertain; the Bridge does not retry.
	postFail := func(reason Reason) (Outcome, error) {
		return Outcome{
			State:          StateFailedAfterCommit,
			Reason:         reason,
			Committed:      true,
			Usage:          usage,
			UnresolvedCost: !hasUsage,
			TTFT:           ttftElapsed,
		}, nil
	}
	cancelOut := func() (Outcome, error) {
		return Outcome{
			State:          cancelState(committed),
			Reason:         ReasonClientCancelled,
			Committed:      committed,
			Usage:          usage,
			UnresolvedCost: committed && !hasUsage,
			TTFT:           ttftElapsed,
		}, nil
	}

	// deadlineOutcome applies absolute timeout precedence. Clock deadlines are
	// authoritative; timer channels merely wake the select. Parent cancellation
	// wins even when an event and a deadline become ready simultaneously.
	deadlineOutcome := func() (Outcome, error, bool) {
		if ctx.Err() != nil {
			o, err := cancelOut()
			return o, err, true
		}
		now := clock.Now()
		if !now.Before(lifetimeDeadline) {
			if committed {
				o, err := postFail(ReasonStreamLifetime)
				return o, err, true
			}
			o, err := preFail(ReasonStreamLifetime, ErrStreamLifetime)
			return o, err, true
		}
		if !committed && !now.Before(ttftDeadline) {
			o, err := preFail(ReasonTTFTTimeout, ErrTTFTTimeout)
			return o, err, true
		}
		if committed && !lastProgress.IsZero() && !now.Before(lastProgress.Add(b.Timeouts.StreamIdle)) {
			o, err := postFail(ReasonStreamIdle)
			return o, err, true
		}
		return Outcome{}, nil, false
	}

	// writeAndFlush forwards one post-commit event. On a context error it
	// resolves a client cancellation; on any other error it resolves a
	// post-commit sink failure (downstream uncertain, no retry).
	writeAndFlush := func(ev Event) (Outcome, error, bool) {
		if err := sink.WriteEvent(ctx, ev); err != nil {
			if isCtxErr(err) {
				o, e := cancelOut()
				return o, e, false
			}
			o, e := postFail(ReasonSinkWrite)
			return o, e, false
		}
		if err := sink.Flush(ctx); err != nil {
			if isCtxErr(err) {
				o, e := cancelOut()
				return o, e, false
			}
			o, e := postFail(ReasonSinkWrite)
			return o, e, false
		}
		return Outcome{}, nil, true
	}

	for {
		// Check Clock deadlines before blocking. This establishes the same
		// precedence when no timer value happened to be selected.
		if out, err, terminal := deadlineOutcome(); terminal {
			return out, err
		}
		select {
		case <-ctx.Done():
			return cancelOut()
		case <-lifetime.Done():
			// A timer wake is not itself proof of expiry; Clock is authoritative.
			if out, err, terminal := deadlineOutcome(); terminal {
				return out, err
			}
		case <-ttftChan:
			if out, err, terminal := deadlineOutcome(); terminal {
				return out, err
			}
		case <-idleChan(idle):
			if out, err, terminal := deadlineOutcome(); terminal {
				return out, err
			}
		case r := <-events:
			// An event never outruns cancellation or an elapsed absolute
			// deadline, including finish and semantic events.
			if out, err, terminal := deadlineOutcome(); terminal {
				return out, err
			}
			if r.err != nil {
				switch {
				case errors.Is(r.err, ErrEndOfStream):
					if !committed {
						return preFail(ReasonProtocol, ErrProtocol)
					}
					// Post-commit EOF without a finish event: the only success
					// path after commit is an explicit finish.
					return postFail(ReasonStreamTruncated)
				case isCtxErr(r.err):
					return cancelOut()
				default:
					// A source-level non-EOF, non-ctx error is treated as a
					// native_error (a well-behaved Source yields
					// EventNativeError instead).
					if !committed {
						return preFail(ReasonUpstreamError, ErrUpstreamError)
					}
					return postFail(ReasonUpstreamError)
				}
			}

			// Source sequence is a strict intake contract. Validate it before
			// sanitizing, counting, buffering, committing, or writing so a bad
			// provider stream cannot create ambiguous downstream ordering.
			if r.ev.Sequence == 0 || r.ev.Sequence <= lastSequence {
				if !committed {
					return preFail(ReasonProtocol, ErrProtocol)
				}
				return postFail(ReasonProtocol)
			}
			lastSequence = r.ev.Sequence
			ev := sanitizeEvent(r.ev, maxTotal)

			// Count every received Event before processing. The
			// (MaxEvents+1)th Event fails safely without retries: a pre-commit
			// sentinel (no downstream bytes) or a post-commit nil-error
			// Outcome (stream already opened, no extra write). The exceeding
			// Event is neither processed nor written.
			//
			// Counting is placed after sanitizeEvent is computed for clarity
			// but before any processing branch; the count itself uses the raw
			// received Event so a misbehaving Source cannot dodge the limit.
			eventsSeen++
			if eventsSeen > maxEvents {
				if !committed {
					return preFail(ReasonEventLimit, ErrEventLimit)
				}
				return postFail(ReasonEventLimit)
			}

			// Accumulate optional Usage regardless of Kind so usage carried by
			// semantic/finish (or any) metadata isn't silently lost. Done once
			// here in the common path; the EventUsage branch only forwards and
			// no longer merges, avoiding a double merge for EventUsage.
			if ev.Usage != nil {
				usage = mergeUsage(usage, *ev.Usage, maxTotal)
				hasUsage = true
			}

			switch ev.Kind {
			case EventLifecycle:
				if !committed {
					// Size the raw (pre-sanitization) event so a huge token trips
					// the byte budget even though the stored event is sanitized.
					sz := eventMetadataSize(r.ev)
					if len(buffer)+1 > MaxBufferedLifecycle || bufferBytes+sz > MaxBufferedMetadataBytes {
						return preFail(ReasonBufferOverflow, ErrBufferOverflow)
					}
					buffer = append(buffer, ev)
					bufferBytes += sz
					if state == StateConnecting {
						transition(&state, StateWaitingFirstSemanticEvent)
					}
					continue
				}
				if out, err, ok := writeAndFlush(ev); !ok {
					return out, err
				}

			case EventSemantic:
				if !committed {
					// Recheck authoritative cancellation/deadlines immediately before
					// Commit to cover the work between receiving the event and writing.
					if out, err, terminal := deadlineOutcome(); terminal {
						return out, err
					}
					batch := make([]Event, 0, len(buffer)+1)
					batch = append(batch, buffer...)
					batch = append(batch, ev)
					if err := sink.Commit(ctx, batch); err != nil {
						if isCtxErr(err) {
							return cancelOut()
						}
						// Downstream uncertain (some/all/no bytes may be
						// written); the Bridge MUST NOT retry.
						committed = true
						return Outcome{
							State:          StateFailedAfterCommit,
							Reason:         ReasonCommitFailed,
							Committed:      true,
							Usage:          usage,
							UnresolvedCost: !hasUsage,
							TTFT:           clock.Sub(started),
						}, nil
					}
					committed = true
					buffer = nil
					bufferBytes = 0
					ttftElapsed = clock.Sub(started)
					lastProgress = clock.Now()
					// Permanently disable the TTFT timer: the budget is consumed at
					// commit. Stop disarms a pending firing and ttftChan=nil renders
					// any already-fired value in the timer channel unreachable, so a
					// fired-but-not-selected TTFT can never cause a post-commit TTFT
					// failure.
					ttft.Stop()
					ttftChan = nil
					idle = timers.NewTimer(b.Timeouts.StreamIdle)
					transition(&state, StateCommitted)
					transition(&state, StateStreaming)
					continue
				}
				// Post-commit semantic: forward + flush, reset idle.
				if out, err, ok := writeAndFlush(ev); !ok {
					return out, err
				}
				lastProgress = clock.Now()
				if !idle.Reset(b.Timeouts.StreamIdle) {
					idle = timers.NewTimer(b.Timeouts.StreamIdle)
				}
				if state == StateCommitted {
					transition(&state, StateStreaming)
				}

			case EventUsage:
				// Usage was accumulated in the common path above; this branch
				// only forwards the event downstream post-commit.
				if committed {
					if out, err, ok := writeAndFlush(ev); !ok {
						return out, err
					}
				}

			case EventFinish:
				if !committed {
					// A finish before any semantic content is a protocol
					// failure (no downstream bytes written).
					return preFail(ReasonProtocol, ErrProtocol)
				}
				if out, err, ok := writeAndFlush(ev); !ok {
					return out, err
				}
				transition(&state, StateCompleted)
				return Outcome{
					State:          StateCompleted,
					Reason:         ReasonCompleted,
					Committed:      true,
					Usage:          usage,
					Finish:         ev.FinishReason,
					UnresolvedCost: !hasUsage,
					TTFT:           ttftElapsed,
				}, nil

			case EventNativeError:
				if !committed {
					return preFail(ReasonUpstreamError, ErrUpstreamError)
				}
				// Native errors are classified upstream metadata, not renderer
				// payloads. After commit, terminate without calling Sink; a future
				// transport closes silently while a future driver consumes the safe
				// outcome classification. Provider-legal error payloads need their
				// own event kind and payload contract.
				return postFail(ReasonUpstreamError)

			default:
				// Unknown kind: protocol violation.
				if !committed {
					return preFail(ReasonProtocol, ErrProtocol)
				}
				return postFail(ReasonProtocol)
			}

			// Progress is an optional idle-reset signal carried by any event.
			// Its ONLY effect is to reset the stream-idle timer after commit;
			// it is never buffered or forwarded as its own event. Pre-commit it
			// has no effect (the idle timer is not armed). Semantic events reset
			// idle unconditionally above; this covers Progress carried by
			// lifecycle/usage events.
			if committed && ev.Progress != nil {
				lastProgress = clock.Now()
				if !idle.Reset(b.Timeouts.StreamIdle) {
					idle = timers.NewTimer(b.Timeouts.StreamIdle)
				}
			}
		}
	}
}

// isCtxErr reports whether err is a context cancellation or deadline.
func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// idleChan returns the idle timer's Done channel, or nil if the idle timer has
// not been armed (pre-commit). A nil channel is never selected.
func idleChan(t Timer) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.Done()
}
