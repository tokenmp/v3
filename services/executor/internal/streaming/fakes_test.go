package streaming

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// manualTimer is a controllable Timer for deterministic tests. Fire delivers
// one firing onto the buffered channel; Stop disarms; Reset re-arms.
type manualTimer struct {
	mu      sync.Mutex
	ch      chan time.Time
	fired   bool
	stopped bool
}

func newManualTimer(d time.Duration) *manualTimer {
	_ = d
	return &manualTimer{ch: make(chan time.Time, 1)}
}

func (m *manualTimer) Done() <-chan time.Time { return m.ch }

func (m *manualTimer) Stop() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return false
	}
	wasArmed := !m.fired
	m.stopped = true
	return wasArmed
}

func (m *manualTimer) Reset(d time.Duration) bool {
	_ = d
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = false
	wasArmed := !m.fired
	m.fired = false
	return wasArmed
}

// fire delivers one firing. It is non-blocking: a stopped or already-pending
// firing is dropped, mirroring time.Timer semantics.
func (m *manualTimer) fire() {
	m.mu.Lock()
	if m.stopped || m.fired {
		m.mu.Unlock()
		return
	}
	m.fired = true
	m.mu.Unlock()
	select {
	case m.ch <- time.Time{}:
	default:
	}
}

// manualTimerSource constructs manualTimer values and records them so a test
// can fire them in creation order.
type manualTimerSource struct {
	mu     sync.Mutex
	timers []*manualTimer
}

func (s *manualTimerSource) NewTimer(d time.Duration) Timer {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := newManualTimer(d)
	s.timers = append(s.timers, t)
	return t
}

func (s *manualTimerSource) timer(idx int) *manualTimer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.timers) {
		return nil
	}
	return s.timers[idx]
}

func (s *manualTimerSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// stopFiresTimer simulates a *time.Timer that fired in the race window
// between the pre-commit recheck and commit's Stop: its Done channel is
// initially blocking (so the recheck sees no firing and proceeds to commit),
// and Stop() pushes a value onto the channel (so the firing becomes
// selectable only AFTER Stop returns false), as happens with a real
// *time.Timer whose value lands in C at the same instant Stop reports the
// timer had already fired. This is the exact condition under which a
// fired-but-not-selected TTFT timer could cause a post-commit TTFT failure.
type stopFiresTimer struct {
	ch chan time.Time
}

func newStopFiresTimer() *stopFiresTimer {
	return &stopFiresTimer{ch: make(chan time.Time, 1)}
}

func (t *stopFiresTimer) Done() <-chan time.Time { return t.ch }

func (t *stopFiresTimer) Stop() bool {
	// Mimic a real timer that had already fired: make the firing selectable now.
	select {
	case t.ch <- time.Time{}:
	default:
	}
	return false // already fired
}

func (t *stopFiresTimer) Reset(d time.Duration) bool { return false }

// stopFiresTimerSource returns stopFiresTimer values for every NewTimer so a
// test can drive the post-commit-stale-TTFT race. Only the TTFT timer's Stop
// is called (at commit); lifetime and idle are never stopped during the test.
type stopFiresTimerSource struct {
	mu     sync.Mutex
	timers []*stopFiresTimer
}

func (s *stopFiresTimerSource) NewTimer(d time.Duration) Timer {
	_ = d
	t := newStopFiresTimer()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timers = append(s.timers, t)
	return t
}

func (s *stopFiresTimerSource) timer(idx int) *stopFiresTimer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idx < 0 || idx >= len(s.timers) {
		return nil
	}
	return s.timers[idx]
}

func (s *stopFiresTimerSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.timers)
}

// fakeClock is a deterministic Clock with a controllable now.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.now
	// Auto-advance by 1ms each call so elapsed (TTFT) measurements are > 0
	// deterministically without test-driven polling.
	c.now = c.now.Add(time.Millisecond)
	return t
}

func (c *fakeClock) Sub(t time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now.Sub(t)
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeSource yields a scripted sequence of events then ErrEndOfStream. It
// records Close calls and supports blocking-on-empty for cancel/timeout tests.
type fakeSource struct {
	mu           sync.Mutex
	events       []Event
	blockOnEmpty bool
	closeCount   int64
}

func newFakeSource(events ...Event) *fakeSource {
	return &fakeSource{events: append([]Event(nil), events...)}
}

func (s *fakeSource) Next(ctx context.Context) (Event, error) {
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	default:
	}
	s.mu.Lock()
	if len(s.events) == 0 {
		blockOnEmpty := s.blockOnEmpty
		s.mu.Unlock()
		if !blockOnEmpty {
			return Event{}, ErrEndOfStream
		}
		<-ctx.Done()
		return Event{}, ctx.Err()
	}
	ev := s.events[0]
	s.events = s.events[1:]
	s.mu.Unlock()
	return ev, nil
}

func (s *fakeSource) Close() error {
	atomic.AddInt64(&s.closeCount, 1)
	return nil
}

func (s *fakeSource) closeCalls() int {
	return int(atomic.LoadInt64(&s.closeCount))
}

// blockingSource never produces an event; it blocks until ctx is cancelled.
type blockingSource struct {
	closeCount int64
}

func (s *blockingSource) Next(ctx context.Context) (Event, error) {
	<-ctx.Done()
	return Event{}, ctx.Err()
}

func (s *blockingSource) Close() error {
	atomic.AddInt64(&s.closeCount, 1)
	return nil
}

func (s *blockingSource) closeCalls() int {
	return int(atomic.LoadInt64(&s.closeCount))
}

// recordSink records Commit/WriteEvent/Flush calls. It can be programmed to
// fail any of them to exercise the no-retry-uncertain contract.
type recordSink struct {
	mu               sync.Mutex
	committed        [][]Event
	written          []Event
	flushes          int
	commitErr        error
	writeErr         error
	flushErr         error
	writeAfterFailOK bool // if false, any call after a failure is recorded as a leak
	leaked           int
	failed           bool
}

func (s *recordSink) Commit(ctx context.Context, events []Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed && !s.writeAfterFailOK {
		s.leaked++
	}
	if s.commitErr != nil {
		s.failed = true
		return s.commitErr
	}
	cp := make([]Event, len(events))
	copy(cp, events)
	s.committed = append(s.committed, cp)
	return nil
}

func (s *recordSink) WriteEvent(ctx context.Context, ev Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed && !s.writeAfterFailOK {
		s.leaked++
	}
	if s.writeErr != nil {
		s.failed = true
		return s.writeErr
	}
	s.written = append(s.written, ev)
	return nil
}

func (s *recordSink) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed && !s.writeAfterFailOK {
		s.leaked++
	}
	if s.flushErr != nil {
		s.failed = true
		return s.flushErr
	}
	s.flushes++
	return nil
}

func (s *recordSink) commitBatches() [][]Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]Event, len(s.committed))
	copy(out, s.committed)
	return out
}

func (s *recordSink) writtenEvents() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.written...)
}

func (s *recordSink) flushCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushes
}

// firingSink wraps a recordSink and calls an optional CommitFire callback
// inside Commit. A test uses it to make a timer fire as a side-effect of the
// commit call itself: the pump concurrently buffers the next event into the
// events channel during main's commit processing, so when main reaches the
// next select both the post-commit event and the fired timer are ready — the
// exact race the precedence recheck must resolve.
type firingSink struct {
	*recordSink
	CommitFire func()
}

func (s *firingSink) Commit(ctx context.Context, events []Event) error {
	if s.CommitFire != nil {
		s.CommitFire()
	}
	return s.recordSink.Commit(ctx, events)
}

// closeProbeSource verifies the Source Close contract: Close is safe while
// Next is in flight, unblocks that Next, and returns without waiting for it.
type closeProbeSource struct {
	inFlight       atomic.Int32
	closedInFlight atomic.Int32
	closed         chan struct{}
	entered        chan struct{}
	cancelled      chan struct{}
	once           sync.Once
}

func (s *closeProbeSource) Next(ctx context.Context) (Event, error) {
	s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	close(s.entered)
	<-ctx.Done()
	// Deliberately remain in-flight until Close. This models a source whose
	// underlying read needs Close to interrupt after observing cancellation.
	close(s.cancelled)
	<-s.closed
	return Event{}, ctx.Err()
}

func (s *closeProbeSource) Close() error {
	if s.inFlight.Load() != 0 {
		s.closedInFlight.Add(1)
	}
	s.once.Do(func() { close(s.closed) })
	return nil
}

func newCloseProbeSource() *closeProbeSource {
	return &closeProbeSource{closed: make(chan struct{}), entered: make(chan struct{}), cancelled: make(chan struct{})}
}

func (s *recordSink) leakCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leaked
}
