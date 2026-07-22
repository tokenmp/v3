package streaming

import (
	"reflect"
	"time"
)

// Timeouts is the effective, validated set of streaming timeout durations for
// one request. All durations are positive. The Bridge rejects a zero or
// negative value as misconfiguration rather than silently treating it as
// infinite.
type Timeouts struct {
	// TTFT is the time-to-first-token budget: the maximum elapsed time from
	// Run start to the first EventSemantic that triggers commit. Firing
	// pre-commit transitions the stream to a TTFT timeout failure. The TTFT
	// timer is stopped at commit and never re-armed.
	TTFT time.Duration

	// StreamIdle is the maximum gap between idle-resetting events (semantic
	// or progress) after commit. The idle timer is armed ONLY after a
	// successful commit; it is reset by each EventSemantic and EventProgress.
	// Firing transitions to a stream-idle failure.
	StreamIdle time.Duration

	// StreamLifetime is the absolute hard cap on total stream duration from
	// Run start to terminal. It may fire pre- or post-commit. Firing
	// transitions to a stream-lifetime failure.
	StreamLifetime time.Duration
}

// Validate reports whether the timeouts are all positive and satisfy
// StreamIdle <= StreamLifetime. A zero or negative value is invalid; the
// Bridge treats invalid timeouts as misconfiguration and never arms a timer.
func (t Timeouts) Validate() bool {
	if t.TTFT <= 0 || t.StreamIdle <= 0 || t.StreamLifetime <= 0 {
		return false
	}
	if t.StreamIdle > t.StreamLifetime {
		return false
	}
	return true
}

// Timer is a resettable single-shot timer. The real implementation wraps
// *time.Timer; tests inject a manual timer exposing the same channel
// semantics so the Bridge's select treats them uniformly.
//
// Done returns a channel that receives or is closed (time.Timer semantics)
// when the timer fires. Stop disarms the timer and returns true if it had not
// yet fired. Reset re-arms the timer for d (replacing any pending firing).
type Timer interface {
	Done() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// TimerSource constructs Timer values. A nil source falls back to the real
// time-based source so a production Bridge is never blocked by a nil injection.
type TimerSource interface {
	NewTimer(d time.Duration) Timer
}

// Clock supplies the authoritative instant for the Bridge's absolute timeout
// deadlines and elapsed durations. A nil clock uses wall time. Timer channels
// only wake the Bridge; timeout decisions always compare Clock.Now with the
// recorded deadline so a stale, reset, or simultaneously-ready timer value
// can never override deadline precedence.
type Clock interface {
	Now() time.Time
	Sub(t time.Time) time.Duration
}

// realTimer wraps a *time.Timer.
type realTimer struct{ t *time.Timer }

func (r *realTimer) Done() <-chan time.Time { return r.t.C }
func (r *realTimer) Stop() bool             { return r.t.Stop() }
func (r *realTimer) Reset(d time.Duration) bool {
	if d <= 0 {
		return r.t.Stop()
	}
	return r.t.Reset(d)
}

type realTimerSource struct{}

func (realTimerSource) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

func timerSourceOr(ts TimerSource) TimerSource {
	if !isNilInterface(ts) {
		return ts
	}
	return realTimerSource{}
}

func clockOr(c Clock) Clock {
	if !isNilInterface(c) {
		return c
	}
	return realClock{}
}

// isTypedNil reports whether v is a typed-nil interface: a non-nil interface
// value wrapping a nil pointer, slice, map, chan, func, or interface. A plain
// == nil check misses this case, and calling a method on such a value panics
// with a nil pointer dereference. Detecting it lets the Bridge fail closed
// (ErrMisconfigured) or fall back instead of panicking.
func isTypedNil(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	}
	return false
}

// isNilInterface reports whether v is an untyped nil interface or a typed-nil
// interface. Required Bridge dependencies (Source, Sink) use this so neither
// form of nil proceeds to an upstream call.
func isNilInterface(v any) bool {
	return v == nil || isTypedNil(v)
}

type realClock struct{}

func (realClock) Now() time.Time                { return time.Now() }
func (realClock) Sub(t time.Time) time.Duration { return time.Since(t) }
