package streaming

import (
	"errors"
	"testing"
	"time"
)

func TestStateTerminal(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"init": false, "connecting": false, "waiting_first_semantic_event": false,
		"committed": false, "streaming": false,
		"completed": true, "failed_before_commit": true,
		"failed_after_commit": true, "client_cancelled": true,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := State(name).Terminal(); got != want {
				t.Errorf("%q.Terminal() = %v, want %v", name, got, want)
			}
		})
	}
}

func TestStateCommitted(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"init": false, "connecting": false, "waiting_first_semantic_event": false,
		"committed": true, "streaming": true, "completed": true,
		"failed_after_commit":  true,
		"failed_before_commit": false, "client_cancelled": false,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := State(name).Committed(); got != want {
				t.Errorf("%q.Committed() = %v, want %v", name, got, want)
			}
		})
	}
}

func TestValidTransition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		from, to State
		want     bool
	}{
		{StateInit, StateConnecting, true},
		{StateInit, StateCommitted, false},
		{StateInit, StateFailedBeforeCommit, true},
		{StateInit, StateClientCancelled, true},
		{StateConnecting, StateWaitingFirstSemanticEvent, true},
		{StateConnecting, StateCommitted, true},
		{StateConnecting, StateFailedBeforeCommit, true},
		{StateWaitingFirstSemanticEvent, StateCommitted, true},
		{StateWaitingFirstSemanticEvent, StateFailedBeforeCommit, true},
		{StateWaitingFirstSemanticEvent, StateStreaming, false},
		{StateCommitted, StateStreaming, true},
		{StateCommitted, StateCompleted, true},
		{StateCommitted, StateFailedAfterCommit, true},
		{StateCommitted, StateClientCancelled, true},
		{StateCommitted, StateFailedBeforeCommit, false},
		{StateStreaming, StateStreaming, true},
		{StateStreaming, StateCompleted, true},
		{StateStreaming, StateFailedAfterCommit, true},
		{StateStreaming, StateFailedBeforeCommit, false},
		{StateCompleted, StateStreaming, false},
		{StateFailedBeforeCommit, StateCompleted, false},
		{StateFailedAfterCommit, StateCompleted, false},
		{StateClientCancelled, StateCompleted, false},
	}
	for _, c := range cases {
		got := validTransition(c.from, c.to)
		if got != c.want {
			t.Errorf("validTransition(%s→%s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestReasonForError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want Reason
	}{
		{nil, ReasonCompleted},
		{ErrTTFTTimeout, ReasonTTFTTimeout},
		{ErrStreamIdle, ReasonStreamIdle},
		{ErrStreamLifetime, ReasonStreamLifetime},
		{ErrUpstreamError, ReasonUpstreamError},
		{ErrProtocol, ReasonProtocol},
		{ErrBufferOverflow, ReasonBufferOverflow},
		{ErrEventLimit, ReasonEventLimit},
		{errors.New("arbitrary internal error"), ReasonUpstreamError},
	}
	for _, c := range cases {
		if got := reasonForError(c.err); got != c.want {
			t.Errorf("reasonForError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestTimeoutsValidate(t *testing.T) {
	t.Parallel()
	good := Timeouts{TTFT: 45 * time.Second, StreamIdle: 30 * time.Second, StreamLifetime: 10 * time.Minute}
	if !good.Validate() {
		t.Errorf("good timeouts failed Validate")
	}
	bad := []Timeouts{
		{TTFT: 0, StreamIdle: 30 * time.Second, StreamLifetime: 10 * time.Minute},
		{TTFT: 45 * time.Second, StreamIdle: 0, StreamLifetime: 10 * time.Minute},
		{TTFT: 45 * time.Second, StreamIdle: 30 * time.Second, StreamLifetime: 0},
		{TTFT: -1, StreamIdle: 30 * time.Second, StreamLifetime: 10 * time.Minute},
		{TTFT: 45 * time.Second, StreamIdle: 10 * time.Minute, StreamLifetime: 30 * time.Second},
	}
	for i, b := range bad {
		if b.Validate() {
			t.Errorf("case %d: invalid timeouts passed Validate: %+v", i, b)
		}
	}
}

func TestSanitizeToken(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"message_start":                  "message_start",
		"content_block_delta":            "content_block_delta",
		"response.output_text.delta":     "response.output_text.delta",
		"stop":                           "stop",
		"":                               "",
		"bad\x00type":                    "",
		"bad type!":                      "",
		"UPSTREAM-ERROR":                 "UPSTREAM-ERROR",
		repeatRune('a', maxTokenBytes+1): "",
		repeatRune('a', maxTokenBytes):   repeatRune('a', maxTokenBytes),
	}
	for in, want := range cases {
		if got := sanitizeToken(in); got != want {
			t.Errorf("sanitizeToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClampCounter(t *testing.T) {
	t.Parallel()
	const maxTotal = int64(100)
	cases := []struct {
		in   int64
		want int64
	}{
		{-5, 0}, {0, 0}, {50, 50}, {100, 100}, {101, 100}, {1_000_000, 100},
	}
	for _, c := range cases {
		if got := clampCounter(c.in, maxTotal); got != c.want {
			t.Errorf("clampCounter(%d, %d) = %d, want %d", c.in, maxTotal, got, c.want)
		}
	}
}

func TestMergeUsageMonotonic(t *testing.T) {
	t.Parallel()
	const maxTotal = int64(1_000_000)
	cur := Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}
	// Incoming below current: must not decrease.
	in := Usage{PromptTokens: 5, CompletionTokens: 20, TotalTokens: 25}
	got := mergeUsage(cur, in, maxTotal)
	if got.PromptTokens != 10 || got.CompletionTokens != 20 || got.TotalTokens != 30 {
		t.Errorf("mergeUsage decreased counters: %+v", got)
	}
	// Incoming above current: increases.
	in2 := Usage{PromptTokens: 15, CompletionTokens: 40, TotalTokens: 55}
	got = mergeUsage(cur, in2, maxTotal)
	if got.PromptTokens != 15 || got.CompletionTokens != 40 || got.TotalTokens != 55 {
		t.Errorf("mergeUsage did not increase: %+v", got)
	}
}

func TestMaxTotalHardCap(t *testing.T) {
	t.Parallel()
	if MaxTotalHardCap != 1_000_000 {
		t.Fatalf("MaxTotalHardCap = %d, want 1000000", MaxTotalHardCap)
	}
}

func TestMaxEventsConstants(t *testing.T) {
	t.Parallel()
	if MaxEventsHardCap != 1_000_000 {
		t.Fatalf("MaxEventsHardCap = %d, want 1000000", MaxEventsHardCap)
	}
	if DefaultMaxEvents <= 0 || DefaultMaxEvents > MaxEventsHardCap {
		t.Fatalf("DefaultMaxEvents = %d, want in (0, %d]", DefaultMaxEvents, MaxEventsHardCap)
	}
}

func repeatRune(r rune, n int) string {
	b := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		b = append(b, byte(r))
	}
	return string(b)
}
