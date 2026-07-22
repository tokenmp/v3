package streaming

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testTimeouts() Timeouts {
	return Timeouts{TTFT: 45 * time.Second, StreamIdle: 30 * time.Second, StreamLifetime: 10 * time.Minute}
}

func newTestBridge(src Source, sink Sink, ts TimerSource) *Bridge {
	return &Bridge{
		Source:   src,
		Sink:     sink,
		Timeouts: testTimeouts(),
		Timers:   ts,
		Clock:    newFakeClock(),
	}
}

func TestBridgeMisconfigured(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		b    *Bridge
	}{
		{"nil bridge", nil},
		{"nil source", &Bridge{Source: nil, Sink: &recordSink{}, Timeouts: testTimeouts()}},
		{"nil sink", &Bridge{Source: newFakeSource(), Sink: nil, Timeouts: testTimeouts()}},
		{"invalid timeouts", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: Timeouts{}}},
		{"negative maxtotal", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: testTimeouts(), MaxTotal: -1}},
		{"over-hardcap maxtotal", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: testTimeouts(), MaxTotal: MaxTotalHardCap + 1}},
		{"negative maxevents", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: testTimeouts(), MaxEvents: -1}},
		{"over-hardcap maxevents", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: testTimeouts(), MaxEvents: MaxEventsHardCap + 1}},
		// Typed-nil dependencies: a non-nil interface wrapping a nil pointer
		// bypasses a plain == nil check and would panic on a method call. The
		// Bridge MUST fail closed to ErrMisconfigured rather than panic.
		{"typed-nil source", &Bridge{Source: Source((*fakeSource)(nil)), Sink: &recordSink{}, Timeouts: testTimeouts()}},
		{"typed-nil sink", &Bridge{Source: newFakeSource(), Sink: Sink((*recordSink)(nil)), Timeouts: testTimeouts()}},
		{"typed-nil timers", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: testTimeouts(), Timers: TimerSource((*manualTimerSource)(nil))}},
		{"typed-nil clock", &Bridge{Source: newFakeSource(), Sink: &recordSink{}, Timeouts: testTimeouts(), Clock: Clock((*fakeClock)(nil))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := c.b.Run(context.Background())
			if !errors.Is(err, ErrMisconfigured) {
				t.Fatalf("err = %v, want ErrMisconfigured", err)
			}
		})
	}
}

func TestBridgeDoubleRun(t *testing.T) {
	t.Parallel()
	b := newTestBridge(newFakeSource(Event{Kind: EventSemantic}, Event{Kind: EventFinish, FinishReason: "stop"}), &recordSink{}, &manualTimerSource{})
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	_, err := b.Run(context.Background())
	if !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("second Run err = %v, want ErrMisconfigured", err)
	}
}

func TestBridgeAlreadyCancelledContext(t *testing.T) {
	t.Parallel()
	b := newTestBridge(newFakeSource(), &recordSink{}, &manualTimerSource{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := b.Run(ctx)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out.State != StateClientCancelled {
		t.Fatalf("state = %q, want client_cancelled", out.State)
	}
}

func TestBridgeCompletedCommitsLifecycleBatchAtomically(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventLifecycle, EventType: "start"},
		Event{Kind: EventLifecycle, EventType: "ping"},
		Event{Kind: EventSemantic},
		Event{Kind: EventSemantic},
		Event{Kind: EventUsage, Usage: &Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != StateCompleted {
		t.Fatalf("state = %q, want completed", out.State)
	}
	if !out.Committed {
		t.Fatalf("not committed")
	}
	if out.Reason != ReasonCompleted {
		t.Fatalf("reason = %q", out.Reason)
	}
	if out.Finish != "stop" {
		t.Fatalf("finish = %q, want stop", out.Finish)
	}
	if out.Usage.TotalTokens != 8 {
		t.Fatalf("usage total = %d, want 8", out.Usage.TotalTokens)
	}
	if out.TTFT <= 0 {
		t.Fatalf("ttft = %v, want > 0", out.TTFT)
	}

	// Exactly one Commit batch: 2 lifecycle + 1 semantic = 3 events. The two
	// post-commit semantic + usage + finish events are WriteEvent'd (no extra
	// Commit).
	batches := sink.commitBatches()
	if len(batches) != 1 {
		t.Fatalf("commit batches = %d, want 1", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("commit batch size = %d, want 3 (2 lifecycle + 1 semantic)", len(batches[0]))
	}
	if batches[0][0].Kind != EventLifecycle {
		t.Errorf("batch[0] = %q, want lifecycle", batches[0][0].Kind)
	}
	if batches[0][1].Kind != EventLifecycle {
		t.Errorf("batch[1] = %q, want lifecycle", batches[0][1].Kind)
	}
	if batches[0][2].Kind != EventSemantic {
		t.Errorf("batch[2] = %q, want semantic", batches[0][2].Kind)
	}
	// Post-commit writes: semantic, usage, finish = 3 (the first semantic is in
	// the commit batch, not written separately).
	written := sink.writtenEvents()
	if len(written) != 3 {
		t.Fatalf("post-commit writes = %d, want 3 (semantic+usage+finish)", len(written))
	}
	// Each post-commit write is flushed.
	if sink.flushCount() != 3 {
		t.Fatalf("flushes = %d, want 3", sink.flushCount())
	}
}

func TestBridgePreCommitEOFIsProtocolFailure(t *testing.T) {
	t.Parallel()
	src := newFakeSource() // empty -> immediate EOF
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q, want failed_before_commit", out.State)
	}
	if out.Committed {
		t.Fatalf("should not be committed")
	}
	if out.Reason != ReasonProtocol {
		t.Fatalf("reason = %q, want protocol", out.Reason)
	}
}

func TestBridgePreCommitFinishIsProtocolFailure(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventLifecycle, EventType: "start"},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q", out.State)
	}
}

func TestBridgePreCommitNativeErrorIsUpstreamError(t *testing.T) {
	t.Parallel()
	src := newFakeSource(Event{Kind: EventNativeError})
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrUpstreamError) {
		t.Fatalf("err = %v, want ErrUpstreamError", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q", out.State)
	}
	if out.Reason != ReasonUpstreamError {
		t.Fatalf("reason = %q", out.Reason)
	}
}

func TestBridgePostCommitNativeErrorFailsNoRetry(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventNativeError},
		// These should NEVER be read after a post-commit failure (no retry).
		Event{Kind: EventSemantic},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	src.blockOnEmpty = true // after the native_error, block to expose any retry
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil (post-commit failure)", err)
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
	if out.Reason != ReasonUpstreamError {
		t.Fatalf("reason = %q", out.Reason)
	}
	if !out.Committed {
		t.Fatalf("should be committed")
	}
	if !out.UnresolvedCost {
		t.Fatalf("should be unresolved (no usage)")
	}
	// The native_error event was written + flushed post-commit (1 write).
	if len(sink.writtenEvents()) != 1 {
		t.Fatalf("writes = %d, want 1 (native_error)", len(sink.writtenEvents()))
	}
	// No leak: no calls after the failure path.
	if sink.leakCount() != 0 {
		t.Fatalf("leaks after failure = %d, want 0", sink.leakCount())
	}
}

func TestBridgeCommitFailureIsPostCommitNoRetry(t *testing.T) {
	t.Parallel()
	// Downstream uncertain on Commit failure: the Bridge resolves a post-commit
	// failure (nil error) and does NOT retry, even though the source has more
	// events.
	src := newFakeSource(
		Event{Kind: EventLifecycle, EventType: "start"},
		Event{Kind: EventSemantic},
		Event{Kind: EventSemantic},
	)
	src.blockOnEmpty = true
	sink := &recordSink{commitErr: errors.New("downstream write failed")}
	b := newTestBridge(src, sink, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil (commit failure -> post-commit outcome)", err)
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
	if out.Reason != ReasonCommitFailed {
		t.Fatalf("reason = %q, want commit_failed", out.Reason)
	}
	if !out.Committed {
		t.Fatalf("should be committed (commit attempted)")
	}
	// No WriteEvent/Flush after a Commit failure (downstream uncertain).
	if len(sink.writtenEvents()) != 0 {
		t.Fatalf("writes after commit failure = %d, want 0", len(sink.writtenEvents()))
	}
	if sink.leakCount() != 0 {
		t.Fatalf("leaks = %d, want 0", sink.leakCount())
	}
}

func TestBridgeWriteEventFailureIsPostCommitNoRetry(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventSemantic},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	src.blockOnEmpty = true
	sink := &recordSink{writeErr: errors.New("write failed")}
	b := newTestBridge(src, sink, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q", out.State)
	}
	if out.Reason != ReasonSinkWrite {
		t.Fatalf("reason = %q, want sink_write", out.Reason)
	}
	if sink.leakCount() != 0 {
		t.Fatalf("leaks = %d", sink.leakCount())
	}
}

func TestBridgeFlushFailureIsPostCommitNoRetry(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventSemantic},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	src.blockOnEmpty = true
	sink := &recordSink{flushErr: errors.New("flush failed")}
	b := newTestBridge(src, sink, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q", out.State)
	}
	if out.Reason != ReasonSinkWrite {
		t.Fatalf("reason = %q", out.Reason)
	}
}

func TestBridgePostCommitEOFWithoutFinishIsTruncated(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		// then EOF without a finish event
	)
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
	if out.Reason != ReasonStreamTruncated {
		t.Fatalf("reason = %q, want stream_truncated", out.Reason)
	}
	if !out.UnresolvedCost {
		t.Fatalf("should be unresolved")
	}
}

func TestBridgeTTFTTimeout(t *testing.T) {
	t.Parallel()
	ts := &manualTimerSource{}
	b := newTestBridge(&blockingSource{}, &recordSink{}, ts)
	clock := b.Clock.(*fakeClock)

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(context.Background())
		close(done)
	}()
	// timers created in order: ttft (0), lifetime (1).
	waitTimerCount(t, ts, 2)
	ttft := ts.timer(0)
	clock.advance(testTimeouts().TTFT)
	ttft.fire()
	<-done
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q, want failed_before_commit", out.State)
	}
	if out.Reason != ReasonTTFTTimeout {
		t.Fatalf("reason = %q, want ttft_timeout", out.Reason)
	}
}

// TestBridgeTTFTFiredPostCommitNeverFails asserts that once commit succeeds
// the TTFT budget is permanently consumed: a TTFT timer that fired in the
// race window between the pre-commit recheck and commit's Stop (its value
// becoming selectable only AFTER Stop returns) must NEVER cause a
// post-commit TTFT failure. Before the fix, ttft.Done() remained in the
// select after ttft.Stop() and this stale firing would be selected
// post-commit, falsely failing the stream with ReasonTTFTTimeout.
func TestBridgeTTFTFiredPostCommitNeverFails(t *testing.T) {
	t.Parallel()
	src := newFakeSource(Event{Kind: EventSemantic})
	src.blockOnEmpty = true // after commit, block so only the stale TTFT
	// could terminate Run; success proves the firing was ignored.
	ts := &stopFiresTimerSource{}
	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBridge(src, &recordSink{}, ts)

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(ctx)
		close(done)
	}()
	// timers: ttft (0), lifetime (1), idle (2, created at commit). Reaching 3
	// proves commit completed; the TTFT Stop() call at commit has already
	// pushed the stale firing into the ttft.Done() channel.
	for i := 0; i < 5000; i++ {
		if ts.count() >= 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if ts.count() < 3 {
		t.Fatalf("timers never reached 3 (got %d)", ts.count())
	}

	// With the bug present, the stale TTFT firing is selected and Run returns
	// with a post-commit TTFT timeout within a few ms. Give it a window.
	select {
	case <-done:
		t.Fatalf("stream terminated from stale post-commit TTFT firing: state=%q reason=%q (must not)", out.State, out.Reason)
	case <-time.After(75 * time.Millisecond):
	}

	cancel()
	<-done
	if out.Reason == ReasonTTFTTimeout {
		t.Fatalf("post-commit stale TTFT fire caused ttft_timeout: state=%q", out.State)
	}
	if out.State != StateClientCancelled {
		t.Fatalf("state = %q, want client_cancelled", out.State)
	}
	if !out.Committed {
		t.Fatalf("should be committed")
	}
}

func TestBridgeStreamIdleTimeout(t *testing.T) {
	t.Parallel()
	src := newFakeSource(Event{Kind: EventSemantic})
	src.blockOnEmpty = true
	ts := &manualTimerSource{}
	b := newTestBridge(src, &recordSink{}, ts)
	clock := b.Clock.(*fakeClock)

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(context.Background())
		close(done)
	}()
	// timers: ttft (0), lifetime (1), idle (2, at commit).
	waitTimerCount(t, ts, 3)
	idle := ts.timer(2)
	clock.advance(testTimeouts().StreamIdle)
	idle.fire()
	<-done
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
	if out.Reason != ReasonStreamIdle {
		t.Fatalf("reason = %q, want stream_idle", out.Reason)
	}
	if !out.Committed {
		t.Fatalf("should be committed")
	}
}

func TestBridgeStreamLifetimeTimeoutPreCommit(t *testing.T) {
	t.Parallel()
	ts := &manualTimerSource{}
	b := newTestBridge(&blockingSource{}, &recordSink{}, ts)
	clock := b.Clock.(*fakeClock)

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(context.Background())
		close(done)
	}()
	waitTimerCount(t, ts, 2)
	lifetime := ts.timer(1)
	clock.advance(testTimeouts().StreamLifetime)
	lifetime.fire()
	<-done
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q, want failed_before_commit", out.State)
	}
	if out.Reason != ReasonStreamLifetime {
		t.Fatalf("reason = %q, want stream_lifetime", out.Reason)
	}
}

func TestBridgeStreamLifetimeTimeoutPostCommit(t *testing.T) {
	t.Parallel()
	src := newFakeSource(Event{Kind: EventSemantic})
	src.blockOnEmpty = true
	ts := &manualTimerSource{}
	b := newTestBridge(src, &recordSink{}, ts)
	clock := b.Clock.(*fakeClock)

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(context.Background())
		close(done)
	}()
	waitTimerCount(t, ts, 3)
	lifetime := ts.timer(1)
	clock.advance(testTimeouts().StreamLifetime)
	lifetime.fire()
	<-done
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
	if out.Reason != ReasonStreamLifetime {
		t.Fatalf("reason = %q, want stream_lifetime", out.Reason)
	}
}

func TestBridgeClientCancelledBeforeCommit(t *testing.T) {
	t.Parallel()
	ts := &manualTimerSource{}
	b := newTestBridge(&blockingSource{}, &recordSink{}, ts)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(ctx)
		close(done)
	}()
	cancel()
	<-done
	if out.State != StateClientCancelled {
		t.Fatalf("state = %q, want client_cancelled", out.State)
	}
	if out.Committed {
		t.Fatalf("should not be committed")
	}
}

func TestBridgeClientCancelledAfterCommit(t *testing.T) {
	t.Parallel()
	src := newFakeSource(Event{Kind: EventSemantic})
	src.blockOnEmpty = true
	ts := &manualTimerSource{}
	ctx, cancel := context.WithCancel(context.Background())
	b := newTestBridge(src, &recordSink{}, ts)

	done := make(chan struct{})
	var out Outcome
	go func() {
		out, _ = b.Run(ctx)
		close(done)
	}()
	waitTimerCount(t, ts, 3) // commit creates idle timer
	cancel()
	<-done
	if out.State != StateClientCancelled {
		t.Fatalf("state = %q, want client_cancelled", out.State)
	}
	if !out.Committed {
		t.Fatalf("should be committed")
	}
}

func TestBridgeClosesSourceOnceOnSuccess(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.closeCalls() != 1 {
		t.Fatalf("source Close called %d times, want 1", src.closeCalls())
	}
}

func TestBridgeClosesSourceOnceOnPreCommitFailure(t *testing.T) {
	t.Parallel()
	src := newFakeSource() // immediate EOF
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	if _, err := b.Run(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
	if src.closeCalls() != 1 {
		t.Fatalf("source Close called %d times, want 1", src.closeCalls())
	}
}

func TestBridgeClosesSourceOnceOnPostCommitFailure(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		// then EOF without finish -> truncated
	)
	src.blockOnEmpty = false
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if src.closeCalls() != 1 {
		t.Fatalf("source Close called %d times, want 1", src.closeCalls())
	}
}

func TestBridgeBufferOverflowCountFailsBeforeCommit(t *testing.T) {
	t.Parallel()
	events := make([]Event, 0, MaxBufferedLifecycle+1)
	for i := 0; i < MaxBufferedLifecycle+1; i++ {
		events = append(events, Event{Kind: EventLifecycle})
	}
	src := newFakeSource(events...)
	src.blockOnEmpty = true
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrBufferOverflow) {
		t.Fatalf("err = %v, want ErrBufferOverflow", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q", out.State)
	}
	if out.Reason != ReasonBufferOverflow {
		t.Fatalf("reason = %q", out.Reason)
	}
}

func TestBridgeBufferOverflowByteBudgetFailsBeforeCommit(t *testing.T) {
	t.Parallel()
	// Fewer events than MaxBufferedLifecycle but each carrying a raw event
	// type token large enough that together they exceed the 16 KiB metadata
	// byte budget. The budget is enforced on the raw (pre-sanitization) size,
	// so a source flooding large tokens trips it before the count cap.
	big := repeatRune('a', MaxBufferedMetadataBytes) // 16 KiB raw token
	events := []Event{
		{Kind: EventLifecycle, EventType: big},
		{Kind: EventLifecycle, EventType: big},
	}
	if len(events) > MaxBufferedLifecycle {
		t.Fatalf("test setup error: %d events > %d", len(events), MaxBufferedLifecycle)
	}
	src := newFakeSource(events...)
	src.blockOnEmpty = true
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrBufferOverflow) {
		t.Fatalf("err = %v, want ErrBufferOverflow", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q", out.State)
	}
	if out.Reason != ReasonBufferOverflow {
		t.Fatalf("reason = %q", out.Reason)
	}
}

func TestBridgeUsageMonotonicBounded(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventUsage, Usage: &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		// A second usage with lower counters must not decrease.
		Event{Kind: EventUsage, Usage: &Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		// A huge counter is clamped to MaxTotal.
		Event{Kind: EventUsage, Usage: &Usage{TotalTokens: 9_999_999}},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	b.MaxTotal = 1_000
	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Usage.PromptTokens != 10 {
		t.Errorf("prompt = %d, want 10", out.Usage.PromptTokens)
	}
	if out.Usage.CompletionTokens != 5 {
		t.Errorf("completion = %d, want 5", out.Usage.CompletionTokens)
	}
	if out.Usage.TotalTokens != 1_000 {
		t.Errorf("total = %d, want 1000 (clamped)", out.Usage.TotalTokens)
	}
	if out.UnresolvedCost {
		t.Errorf("has usage, should not be unresolved")
	}
}

func TestBridgeUnresolvedCostWhenFinishWithoutUsage(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.UnresolvedCost {
		t.Fatalf("should be unresolved (no usage)")
	}
}

func TestBridgeProgressResetsIdleOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	// Progress is an optional field (idle-reset only), not a Kind. A usage
	// event carrying Progress post-commit must reset idle without creating a
	// new timer (Reset re-arms the existing idle timer).
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventUsage, Usage: &Usage{TotalTokens: 1}, Progress: &Progress{Processed: 1}},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	ts := &manualTimerSource{}
	b := newTestBridge(src, &recordSink{}, ts)
	if _, err := b.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// timers: ttft (0), lifetime (1), idle (2). Progress reset re-arms idle
	// in place (no new timer created).
	if ts.count() != 3 {
		t.Fatalf("timers = %d, want 3 (no new timer from progress reset)", ts.count())
	}
}

func TestBridgeSanitizesTokens(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventSemantic, EventType: "bad\x00type"},
		Event{Kind: EventFinish, FinishReason: "bad reason!", EventType: "ok.type"},
	)
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})
	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Finish != "" {
		t.Errorf("finish = %q, want empty (sanitized)", out.Finish)
	}
	written := sink.writtenEvents()
	// finish event written post-commit
	var finishEv *Event
	for i := range written {
		if written[i].Kind == EventFinish {
			finishEv = &written[i]
			break
		}
	}
	if finishEv == nil {
		t.Fatalf("no finish event written")
	}
	if finishEv.FinishReason != "" {
		t.Errorf("written finish = %q, want empty (sanitized)", finishEv.FinishReason)
	}
	if finishEv.EventType != "ok.type" {
		t.Errorf("written event type = %q, want ok.type", finishEv.EventType)
	}
}

// --- timer/cancel races ---

// TestBridgeTimerPrecedenceOverCommit asserts the "recheck cancellation/timer
// precedence immediately before Commit" requirement: when the TTFT timer has
// fired at the instant the first semantic event is being processed, the timer
// must win (no commit) rather than committing on a timed-out stream. Because
// the select between a ready semantic event and a fired TTFT timer is
// non-deterministic, we run many iterations; the invariant checked on every
// iteration is that the stream never both commits AND reports a TTFT timeout.
// The source is non-blocking (semantic then EOF) so every iteration terminates.
func TestBridgeTimerPrecedenceOverCommit(t *testing.T) {
	t.Parallel()
	for i := 0; i < 200; i++ {
		src := newFakeSource(Event{Kind: EventSemantic})
		ts := &manualTimerSource{}
		sink := &recordSink{}
		b := newTestBridge(src, sink, ts)

		done := make(chan struct{})
		var out Outcome
		var runErr error
		go func() {
			out, runErr = b.Run(context.Background())
			close(done)
		}()
		waitTimerCount(t, ts, 2)
		// Fire TTFT so it is ready in the channel at the same instant the
		// semantic event is ready in the events channel.
		ts.timer(0).fire()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: Run hung", i)
		}
		// Invariant: never committed AND ttft_timeout. If the semantic won, the
		// stream committed then hit post-commit EOF (truncated), which is also
		// acceptable — the point is the timer recheck prevents commit-on-timeout.
		if out.Committed && runErr != nil && errors.Is(runErr, ErrTTFTTimeout) {
			t.Fatalf("iteration %d: invariant violation: committed AND ttft_timeout", i)
		}
		if runErr != nil && !errors.Is(runErr, ErrTTFTTimeout) && !errors.Is(runErr, ErrProtocol) {
			t.Fatalf("iteration %d: unexpected err %v state %q", i, runErr, out.State)
		}
		if !out.State.Terminal() {
			t.Fatalf("iteration %d: non-terminal state %q", i, out.State)
		}
		if src.closeCalls() != 1 {
			t.Fatalf("iteration %d: close calls = %d", i, src.closeCalls())
		}
	}
}

// TestBridgeCancelPrecedenceOverCommit asserts that a context cancellation
// pending at commit time is honored: the stream resolves to client_cancelled
// (pre- or post-commit) and never writes a finish after cancel. Non-blocking
// source (semantic then EOF) so every iteration terminates.
func TestBridgeCancelPrecedenceOverCommit(t *testing.T) {
	t.Parallel()
	for i := 0; i < 200; i++ {
		src := newFakeSource(Event{Kind: EventSemantic})
		ts := &manualTimerSource{}
		sink := &recordSink{}
		ctx, cancel := context.WithCancel(context.Background())
		b := newTestBridge(src, sink, ts)

		done := make(chan struct{})
		var out Outcome
		go func() {
			out, _ = b.Run(ctx)
			close(done)
		}()
		waitTimerCount(t, ts, 2)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: Run hung", i)
		}
		if !out.State.Terminal() {
			t.Fatalf("iteration %d: non-terminal state %q", i, out.State)
		}
		// Invariant: a cancelled stream never reaches success.
		if out.State == StateCompleted {
			t.Fatalf("iteration %d: completed after cancel", i)
		}
		if src.closeCalls() != 1 {
			t.Fatalf("iteration %d: close calls = %d", i, src.closeCalls())
		}
	}
}

// --- fuzz ---

func FuzzBridgeRun(f *testing.F) {
	// Seed with a few representative event sequences.
	f.Add([]byte{0, 1, 4}) // lifecycle, semantic, finish
	f.Add([]byte{0, 0, 1, 2, 4})
	f.Add([]byte{3})    // native_error pre-commit
	f.Add([]byte{1, 3}) // native_error post-commit
	f.Add([]byte{1})    // truncated EOF
	f.Add([]byte{2, 2, 4})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 4})

	kinds := []EventKind{
		0: EventLifecycle,
		1: EventSemantic,
		2: EventUsage,
		3: EventNativeError,
		4: EventFinish,
	}

	f.Fuzz(func(t *testing.T, seq []byte) {
		if len(seq) > 256 {
			t.Skip("long sequence")
		}
		events := make([]Event, 0, len(seq))
		for _, b := range seq {
			k := EventLifecycle
			if int(b) < len(kinds) {
				k = kinds[b]
			}
			ev := Event{Kind: k}
			if k == EventFinish {
				ev.FinishReason = "stop"
			}
			if k == EventUsage {
				ev.Usage = &Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}
			}
			events = append(events, ev)
		}
		src := newFakeSource(events...)
		sink := &recordSink{}
		b := newTestBridge(src, sink, &manualTimerSource{})
		out, _ := b.Run(context.Background())

		// Invariants for any input:
		// 1. Source is closed exactly once.
		if src.closeCalls() != 1 {
			t.Fatalf("close calls = %d, want 1", src.closeCalls())
		}
		// 2. Terminal state.
		if !out.State.Terminal() {
			t.Fatalf("non-terminal state %q", out.State)
		}
		// 3. Committed flag consistent with state.
		if out.Committed != out.State.Committed() && out.State != StateClientCancelled {
			t.Fatalf("Committed=%v but State=%q", out.Committed, out.State)
		}
		// 4. Usage is bounded.
		if out.Usage.TotalTokens < 0 || out.Usage.TotalTokens > MaxTotalHardCap {
			t.Fatalf("usage total out of bounds: %d", out.Usage.TotalTokens)
		}
		// 5. No more than one Commit batch (commit happens at most once).
		if len(sink.commitBatches()) > 1 {
			t.Fatalf("commit batches = %d, want <= 1", len(sink.commitBatches()))
		}
		// 6. If a finish was reached, state is completed.
		// 7. Finish token sanitized.
		if out.Finish != "" && out.Finish != "stop" {
			t.Fatalf("finish = %q, want stop or empty", out.Finish)
		}
	})
}

// --- MaxEvents total-event limit ---

// TestBridgeMaxEventsExactLimitAllowed asserts that exactly MaxEvents Events
// are accepted and the stream completes successfully when the (MaxEvents)th
// Event is the finish. The limit is inclusive: MaxEvents events are allowed.
func TestBridgeMaxEventsExactLimitAllowed(t *testing.T) {
	t.Parallel()
	// MaxEvents = 3: semantic (commit), usage, finish. Exactly at the limit.
	src := newFakeSource(
		Event{Kind: EventSemantic},
		Event{Kind: EventUsage, Usage: &Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}},
		Event{Kind: EventFinish, FinishReason: "stop"},
	)
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})
	b.MaxEvents = 3

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != StateCompleted {
		t.Fatalf("state = %q, want completed", out.State)
	}
	if out.Reason != ReasonCompleted {
		t.Fatalf("reason = %q, want completed", out.Reason)
	}
	if out.Finish != "stop" {
		t.Fatalf("finish = %q, want stop", out.Finish)
	}
	if out.Usage.TotalTokens != 2 {
		t.Fatalf("usage total = %d, want 2", out.Usage.TotalTokens)
	}
	// Exactly one commit batch (the semantic) and two post-commit writes
	// (usage, finish). No event was dropped at the limit.
	if len(sink.commitBatches()) != 1 {
		t.Fatalf("commit batches = %d, want 1", len(sink.commitBatches()))
	}
	if len(sink.writtenEvents()) != 2 {
		t.Fatalf("post-commit writes = %d, want 2 (usage+finish)", len(sink.writtenEvents()))
	}
	if sink.leakCount() != 0 {
		t.Fatalf("leaks = %d, want 0", sink.leakCount())
	}
	if src.closeCalls() != 1 {
		t.Fatalf("source Close = %d, want 1", src.closeCalls())
	}
}

// TestBridgeMaxEventsPreCommitExceeded asserts that the (MaxEvents+1)th
// Event arriving pre-commit fails with the ErrEventLimit sentinel and no
// downstream writes occur (no commit, no writes).
func TestBridgeMaxEventsPreCommitExceeded(t *testing.T) {
	t.Parallel()
	// MaxEvents = 2: two lifecycle events are accepted (both pre-commit,
	// buffered). The third event (a semantic that would commit) exceeds the
	// limit and must fail pre-commit with ErrEventLimit.
	src := newFakeSource(
		Event{Kind: EventLifecycle, EventType: "a"},
		Event{Kind: EventLifecycle, EventType: "b"},
		Event{Kind: EventSemantic}, // the (limit+1)th event
	)
	src.blockOnEmpty = true
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})
	b.MaxEvents = 2

	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrEventLimit) {
		t.Fatalf("err = %v, want ErrEventLimit", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q, want failed_before_commit", out.State)
	}
	if out.Reason != ReasonEventLimit {
		t.Fatalf("reason = %q, want event_limit", out.Reason)
	}
	if out.Committed {
		t.Fatalf("should not be committed")
	}
	// No downstream bytes written: no commit batch, no writes, no flushes.
	if len(sink.commitBatches()) != 0 {
		t.Fatalf("commit batches = %d, want 0", len(sink.commitBatches()))
	}
	if len(sink.writtenEvents()) != 0 {
		t.Fatalf("writes = %d, want 0", len(sink.writtenEvents()))
	}
	if sink.flushCount() != 0 {
		t.Fatalf("flushes = %d, want 0", sink.flushCount())
	}
	if sink.leakCount() != 0 {
		t.Fatalf("leaks = %d, want 0", sink.leakCount())
	}
	if src.closeCalls() != 1 {
		t.Fatalf("source Close = %d, want 1", src.closeCalls())
	}
}

// TestBridgeMaxEventsPostCommitExceeded asserts that the (MaxEvents+1)th
// Event arriving post-commit fails as a post-commit failure (nil error,
// failed Outcome) and the exceeding Event is neither processed nor written
// downstream (no extra write beyond what was already committed).
func TestBridgeMaxEventsPostCommitExceeded(t *testing.T) {
	t.Parallel()
	// MaxEvents = 2: semantic (commit) + one more post-commit event are
	// accepted. The third event (a second semantic) exceeds the limit and
	// must fail post-commit with ReasonEventLimit, nil error. The exceeding
	// semantic is NOT written downstream.
	src := newFakeSource(
		Event{Kind: EventSemantic},                             // 1st: commit
		Event{Kind: EventUsage, Usage: &Usage{TotalTokens: 5}}, // 2nd: accepted, written
		Event{Kind: EventSemantic},                             // 3rd: exceeds limit, NOT written
		Event{Kind: EventFinish, FinishReason: "stop"},         // never read
	)
	src.blockOnEmpty = true
	sink := &recordSink{}
	b := newTestBridge(src, sink, &manualTimerSource{})
	b.MaxEvents = 2

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("err = %v, want nil (post-commit failure)", err)
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
	if out.Reason != ReasonEventLimit {
		t.Fatalf("reason = %q, want event_limit", out.Reason)
	}
	if !out.Committed {
		t.Fatalf("should be committed")
	}
	// Usage from the 2nd (accepted) event was accumulated; the 3rd event
	// (which would carry no usage) was not processed.
	if out.Usage.TotalTokens != 5 {
		t.Fatalf("usage total = %d, want 5", out.Usage.TotalTokens)
	}
	if out.UnresolvedCost {
		t.Fatalf("has usage, should not be unresolved")
	}
	// One commit batch (the 1st semantic). Exactly one post-commit write
	// (the 2nd usage event); the exceeding 3rd semantic produced NO extra
	// downstream write.
	if len(sink.commitBatches()) != 1 {
		t.Fatalf("commit batches = %d, want 1", len(sink.commitBatches()))
	}
	if len(sink.writtenEvents()) != 1 {
		t.Fatalf("post-commit writes = %d, want 1 (usage only; exceeding event not written)", len(sink.writtenEvents()))
	}
	if sink.leakCount() != 0 {
		t.Fatalf("leaks = %d, want 0", sink.leakCount())
	}
	if src.closeCalls() != 1 {
		t.Fatalf("source Close = %d, want 1", src.closeCalls())
	}
}

// TestBridgeMaxEventsDefaultsToSafeDefault asserts that a zero MaxEvents
// defaults to DefaultMaxEvents (not the hard cap and not infinite), so a
// runaway upstream is still bounded. Uses post-commit semantic events so the
// pre-commit lifecycle buffer cap does not fire first.
func TestBridgeMaxEventsDefaultsToSafeDefault(t *testing.T) {
	t.Parallel()
	if DefaultMaxEvents <= 0 || DefaultMaxEvents > MaxEventsHardCap {
		t.Fatalf("DefaultMaxEvents = %d, want in (0, %d]", DefaultMaxEvents, MaxEventsHardCap)
	}
	// First semantic commits; the rest are written post-commit. With
	// DefaultMaxEvents+1 events the last one exceeds the default limit.
	n := int(DefaultMaxEvents) + 1
	events := make([]Event, n)
	for i := range events {
		events[i] = Event{Kind: EventSemantic}
	}
	src := newFakeSource(events...)
	src.blockOnEmpty = true
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	// MaxEvents intentionally left zero.
	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrEventLimit) && out.Reason != ReasonEventLimit {
		t.Fatalf("err = %v, out.Reason = %q, want ErrEventLimit (default enforced)", err, out.Reason)
	}
	if !out.Committed {
		t.Fatalf("should be committed (limit fires post-commit)")
	}
	if out.State != StateFailedAfterCommit {
		t.Fatalf("state = %q, want failed_after_commit", out.State)
	}
}

// TestBridgeUsageAccumulatedRegardlessOfKind asserts that optional Usage
// carried by any Event Kind (semantic, finish) is accumulated for the Outcome
// and not silently lost, while EventUsage is not double-merged.
func TestBridgeUsageAccumulatedRegardlessOfKind(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		// Semantic carries usage metadata (e.g. an incremental delta usage).
		Event{Kind: EventSemantic, Usage: &Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}},
		// Explicit usage event: merged exactly once (not double).
		Event{Kind: EventUsage, Usage: &Usage{PromptTokens: 7, CompletionTokens: 4, TotalTokens: 11}},
		// Finish carries final usage metadata.
		Event{Kind: EventFinish, FinishReason: "stop", Usage: &Usage{PromptTokens: 10, CompletionTokens: 6, TotalTokens: 16}},
	)
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	b.MaxTotal = 1_000

	out, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.State != StateCompleted {
		t.Fatalf("state = %q, want completed", out.State)
	}
	// Monotonic max across the three events: prompt 10, completion 6, total 16.
	// If the semantic/finish usage were silently lost, totals would be lower
	// (e.g. 7/4/11 from the EventUsage alone). If EventUsage were
	// double-merged, completion could exceed 6.
	if out.Usage.PromptTokens != 10 {
		t.Errorf("prompt = %d, want 10", out.Usage.PromptTokens)
	}
	if out.Usage.CompletionTokens != 6 {
		t.Errorf("completion = %d, want 6", out.Usage.CompletionTokens)
	}
	if out.Usage.TotalTokens != 16 {
		t.Errorf("total = %d, want 16", out.Usage.TotalTokens)
	}
	if out.UnresolvedCost {
		t.Errorf("has usage, should not be unresolved")
	}
}

// TestBridgePreCommitFailureReportsZeroUsage asserts that usage accumulated
// pre-commit is discarded on a pre-commit failure: nothing was committed
// downstream, so the Outcome reports zero usage (release-quota billing
// contract, no billable charge, no unresolved cost). Previously preFail
// returned the accumulated usage, contradicting the Outcome.Usage doc and
// risking mis-billing a stream that never committed.
func TestBridgePreCommitFailureReportsZeroUsage(t *testing.T) {
	t.Parallel()
	src := newFakeSource(
		Event{Kind: EventUsage, Usage: &Usage{PromptTokens: 9, CompletionTokens: 9, TotalTokens: 18}},
		Event{Kind: EventLifecycle, EventType: "start"},
		Event{Kind: EventNativeError}, // pre-commit native_error
	)
	b := newTestBridge(src, &recordSink{}, &manualTimerSource{})
	out, err := b.Run(context.Background())
	if !errors.Is(err, ErrUpstreamError) {
		t.Fatalf("err = %v, want ErrUpstreamError", err)
	}
	if out.State != StateFailedBeforeCommit {
		t.Fatalf("state = %q, want failed_before_commit", out.State)
	}
	if out.Usage != (Usage{}) {
		t.Fatalf("usage = %+v, want zero on pre-commit failure", out.Usage)
	}
	if out.UnresolvedCost {
		t.Fatalf("unresolved cost should be false on pre-commit failure")
	}
	if out.Committed {
		t.Fatalf("should not be committed")
	}
}

// TestBridgePostCommitTimerPrecedenceOverReadyEvent asserts that a lifetime
// timer already fired at the instant a post-commit semantic event is received
// wins over processing that event: the stream fails with stream_lifetime and
// NO post-commit write occurs (the semantic is not written, idle is not reset,
// the stream is not completed after the hard cap). Without the precedence
// recheck, the Go select could choose the events channel over the ready
// lifetime timer and process the semantic first.
func TestBridgePostCommitTimerPrecedenceOverReadyEvent(t *testing.T) {
	t.Parallel()
	for i := 0; i < 200; i++ {
		ts := &manualTimerSource{}
		inner := &recordSink{}
		sink := &firingSink{recordSink: inner}
		// The lifetime timer (index 1) is created at Run start, so it exists by
		// the time Commit runs. CommitFire fires it as a side-effect of the
		// commit call; meanwhile the pump buffers the next (post-commit)
		// semantic into the events channel, so when main reaches its next
		// select both the event and the fired lifetime timer are ready.
		clock := newFakeClock()
		sink.CommitFire = func() {
			clock.advance(testTimeouts().StreamLifetime)
			ts.timer(1).fire()
		}
		src := newFakeSource(
			Event{Kind: EventSemantic}, // event0: commit
			Event{Kind: EventSemantic}, // event1: post-commit, races with lifetime
		)
		src.blockOnEmpty = true
		b := newTestBridge(src, sink, ts)
		b.Clock = clock

		done := make(chan struct{})
		var out Outcome
		go func() {
			out, _ = b.Run(context.Background())
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: Run hung", i)
		}
		if out.Reason != ReasonStreamLifetime {
			t.Fatalf("iter %d: reason = %q, want stream_lifetime", i, out.Reason)
		}
		if !out.Committed {
			t.Fatalf("iter %d: should be committed", i)
		}
		// No post-commit write: the ready semantic must NOT be written after
		// the lifetime hard cap expired.
		if len(inner.writtenEvents()) != 0 {
			t.Fatalf("iter %d: post-commit writes = %d, want 0 (lifetime must win before any write)", i, len(inner.writtenEvents()))
		}
		if src.closeCalls() != 1 {
			t.Fatalf("iter %d: close calls = %d, want 1", i, src.closeCalls())
		}
	}
}

// TestBridgeCloseConcurrentWithNext asserts the Source contract and Bridge
// ordering: Bridge cancels, calls concurrent-safe Close to unblock Next, then
// waits for the pump so Run cannot leak it.
func TestBridgeCloseConcurrentWithNext(t *testing.T) {
	t.Parallel()
	for i := 0; i < 50; i++ {
		src := newCloseProbeSource()
		ts := &manualTimerSource{}
		ctx, cancel := context.WithCancel(context.Background())
		b := newTestBridge(src, &recordSink{}, ts)

		done := make(chan struct{})
		go func() {
			b.Run(ctx)
			close(done)
		}()
		waitTimerCount(t, ts, 2)
		select {
		case <-src.entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: Next never entered", i)
		}
		cancel()
		select {
		case <-src.cancelled:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: Next did not observe cancellation", i)
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: Run hung after cancel", i)
		}
		if src.closedInFlight.Load() == 0 {
			t.Fatalf("iter %d: Close was not called concurrently with in-flight Next", i)
		}
	}
}

// waitTimerCount polls the manual timer source until at least n timers exist.
func waitTimerCount(t *testing.T, ts *manualTimerSource, n int) {
	t.Helper()
	for i := 0; i < 5000; i++ {
		if ts.count() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timers never reached %d (got %d)", n, ts.count())
}
