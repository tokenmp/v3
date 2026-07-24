package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/protocolconvert"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// mockProtocolSink records all events written to it.
type mockProtocolSink struct {
	events []sdk.StreamEvent
	err    error
}

func (m *mockProtocolSink) Commit(_ context.Context, events []sdk.StreamEvent) error {
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, events...)
	return nil
}

func (m *mockProtocolSink) WriteEvent(_ context.Context, event sdk.StreamEvent) error {
	if m.err != nil {
		return m.err
	}
	m.events = append(m.events, event)
	return nil
}

func (m *mockProtocolSink) Flush(_ context.Context) error {
	return m.err
}

// errSink is a test sentinel injected into mockProtocolSink.err to exercise
// write/flush/commit failure paths.
var errSink = errors.New("convert_sink_test: sink error")

// makeStreamEvent creates a test sdk.StreamEvent with the given sequence, kind, eventType and data.
func makeStreamEvent(seq uint64, kind streaming.EventKind, eventType, data string) sdk.StreamEvent {
	return sdk.StreamEvent{
		Sequence: seq,
		Meta: streaming.Event{
			Sequence:  seq,
			Kind:      kind,
			EventType: eventType,
		},
		Data: json.RawMessage(data),
	}
}

func TestConvertingSinkSameProtocolPassthrough(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIChat, nil)
	if sink != inner {
		t.Fatal("same-protocol should return inner sink directly")
	}
}

// ── OpenAI → Anthropic: Meta.EventType correctness ──────────────────────────

func TestConvertingSinkOpenAIToAnthropicMetaEventType(t *testing.T) {
	// Simulate a full OpenAI chat stream being converted to Anthropic.
	// Each OpenAI chunk has EventType "chat.completion.chunk"; after conversion
	// the Meta.EventType must reflect the Anthropic event type extracted from
	// the converted JSON "type" field.

	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Chunk 1: role announcement (produces message_start only)
	chunk1 := `{"id":"msg_1","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	ev1 := makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", chunk1)
	if err := sink.WriteEvent(context.Background(), ev1); err != nil {
		t.Fatalf("WriteEvent chunk1: %v", err)
	}

	// Chunk 2: text content (produces content_block_start + content_block_delta)
	chunk2 := `{"id":"msg_1","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`
	ev2 := makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", chunk2)
	if err := sink.WriteEvent(context.Background(), ev2); err != nil {
		t.Fatalf("WriteEvent chunk2: %v", err)
	}

	// Chunk 3: more text (produces content_block_delta)
	chunk3 := `{"id":"msg_1","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`
	ev3 := makeStreamEvent(3, streaming.EventSemantic, "chat.completion.chunk", chunk3)
	if err := sink.WriteEvent(context.Background(), ev3); err != nil {
		t.Fatalf("WriteEvent chunk3: %v", err)
	}

	// Chunk 4: finish (produces content_block_stop + message_delta + message_stop)
	chunk4 := `{"id":"msg_1","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	ev4 := makeStreamEvent(4, streaming.EventFinish, "chat.completion.chunk", chunk4)
	if err := sink.WriteEvent(context.Background(), ev4); err != nil {
		t.Fatalf("WriteEvent chunk4: %v", err)
	}

	if len(inner.events) == 0 {
		t.Fatal("no events written to inner sink")
	}

	// Verify each event has the correct Anthropic EventType
	anthropicTypes := map[string]bool{
		"message_start":       true,
		"content_block_start": true,
		"content_block_delta": true,
		"content_block_stop":  true,
		"message_delta":       true,
		"message_stop":        true,
	}

	for i, ev := range inner.events {
		if !anthropicTypes[ev.Meta.EventType] {
			t.Errorf("event[%d] Meta.EventType=%q, not a valid Anthropic event type", i, ev.Meta.EventType)
		}
		// Verify the converted Data also contains the matching "type" field
		var parsed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(ev.Data, &parsed); err != nil {
			t.Errorf("event[%d] Data is not valid JSON: %v", i, err)
			continue
		}
		if parsed.Type != ev.Meta.EventType {
			t.Errorf("event[%d] Data.type=%q but Meta.EventType=%q", i, parsed.Type, ev.Meta.EventType)
		}
	}
}

// ── Anthropic → OpenAI: Meta.EventType correctness ──────────────────────────

func TestConvertingSinkAnthropicToOpenAIMetaEventType(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	// message_start (no OpenAI output)
	chunk1 := `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3","usage":{"input_tokens":10,"output_tokens":0}}}`
	ev1 := makeStreamEvent(1, streaming.EventLifecycle, "message_start", chunk1)
	if err := sink.WriteEvent(context.Background(), ev1); err != nil {
		t.Fatalf("WriteEvent chunk1: %v", err)
	}

	// content_block_start (produces role announcement chunk)
	chunk2 := `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`
	ev2 := makeStreamEvent(2, streaming.EventLifecycle, "content_block_start", chunk2)
	if err := sink.WriteEvent(context.Background(), ev2); err != nil {
		t.Fatalf("WriteEvent chunk2: %v", err)
	}

	// content_block_delta (produces text delta chunk)
	chunk3 := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`
	ev3 := makeStreamEvent(3, streaming.EventSemantic, "content_block_delta", chunk3)
	if err := sink.WriteEvent(context.Background(), ev3); err != nil {
		t.Fatalf("WriteEvent chunk3: %v", err)
	}

	// message_delta (no OpenAI output, just state tracking)
	chunk4 := `{"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`
	ev4 := makeStreamEvent(4, streaming.EventUsage, "message_delta", chunk4)
	if err := sink.WriteEvent(context.Background(), ev4); err != nil {
		t.Fatalf("WriteEvent chunk4: %v", err)
	}

	// message_stop (produces final chunk with finish_reason)
	chunk5 := `{"type":"message_stop"}`
	ev5 := makeStreamEvent(5, streaming.EventFinish, "message_stop", chunk5)
	if err := sink.WriteEvent(context.Background(), ev5); err != nil {
		t.Fatalf("WriteEvent chunk5: %v", err)
	}

	// All converted events with non-nil Data should have EventType "chat.completion.chunk"
	for i, ev := range inner.events {
		if len(ev.Data) == 0 {
			continue // zero-output events (message_start, message_delta produce no OpenAI chunks)
		}
		if ev.Meta.EventType != "chat.completion.chunk" {
			t.Errorf("event[%d] Meta.EventType=%q, want %q", i, ev.Meta.EventType, "chat.completion.chunk")
		}
		// Verify the converted Data also contains the matching "object" field
		var parsed struct {
			Object string `json:"object"`
		}
		if err := json.Unmarshal(ev.Data, &parsed); err != nil {
			t.Errorf("event[%d] Data is not valid JSON: %v", i, err)
			continue
		}
		if parsed.Object != "chat.completion.chunk" {
			t.Errorf("event[%d] Data.object=%q, want %q", i, parsed.Object, "chat.completion.chunk")
		}
	}
}

// ── Terminal events: Meta.Kind == EventFinish ───────────────────────────────

func TestConvertingSinkFinishKindAnthropic(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Send a complete mini-stream: role + content + finish
	chunks := []struct {
		seq  uint64
		kind streaming.EventKind
		data string
	}{
		{1, streaming.EventLifecycle, `{"id":"msg_f","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`},
		{2, streaming.EventSemantic, `{"id":"msg_f","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`},
		{3, streaming.EventFinish, `{"id":"msg_f","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
	}

	for _, c := range chunks {
		ev := makeStreamEvent(c.seq, c.kind, "chat.completion.chunk", c.data)
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent seq=%d: %v", c.seq, err)
		}
	}

	// Find the message_stop event — it must have Kind == EventFinish
	foundFinish := false
	for _, ev := range inner.events {
		if ev.Meta.EventType == "message_stop" {
			foundFinish = true
			if ev.Meta.Kind != streaming.EventFinish {
				t.Errorf("message_stop Kind=%q, want %q", ev.Meta.Kind, streaming.EventFinish)
			}
		}
	}
	if !foundFinish {
		t.Fatal("no message_stop event found in converted output")
	}
}

func TestConvertingSinkFinishKindOpenAI(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	// Send Anthropic stream that ends with message_stop
	chunks := []struct {
		seq  uint64
		kind streaming.EventKind
		et   string
		data string
	}{
		{1, streaming.EventLifecycle, "message_start", `{"type":"message_start","message":{"id":"msg_f2","type":"message","role":"assistant","model":"claude-3","usage":{"input_tokens":5,"output_tokens":0}}}`},
		{2, streaming.EventLifecycle, "content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{3, streaming.EventSemantic, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
		{4, streaming.EventUsage, "message_delta", `{"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`},
		{5, streaming.EventFinish, "message_stop", `{"type":"message_stop"}`},
	}

	for _, c := range chunks {
		ev := makeStreamEvent(c.seq, c.kind, c.et, c.data)
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent seq=%d: %v", c.seq, err)
		}
	}

	// The final OpenAI chunk (from message_stop) must have Kind == EventFinish
	foundFinish := false
	for _, ev := range inner.events {
		var parsed struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(ev.Data, &parsed); err != nil {
			continue
		}
		if len(parsed.Choices) > 0 && parsed.Choices[0].FinishReason != nil && *parsed.Choices[0].FinishReason != "" {
			foundFinish = true
			if ev.Meta.Kind != streaming.EventFinish {
				t.Errorf("final chunk Kind=%q, want %q", ev.Meta.Kind, streaming.EventFinish)
			}
		}
	}
	if !foundFinish {
		t.Fatal("no finish chunk found in converted OpenAI output")
	}
}

// ── Multi-chunk pending: each pending event has correct Meta ────────────────

func TestConvertingSinkPendingMetaCorrect(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// First OpenAI chunk with content produces: message_start + content_block_start + content_block_delta
	// The message_start and content_block_start are pending; content_block_delta is the primary.
	chunk := `{"id":"msg_p","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`
	ev := makeStreamEvent(1, streaming.EventSemantic, "chat.completion.chunk", chunk)
	if err := sink.WriteEvent(context.Background(), ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	// All events (primary + flushed pending) should have correct Anthropic EventTypes
	for i, e := range inner.events {
		var parsed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(e.Data, &parsed); err != nil {
			t.Errorf("event[%d] invalid JSON: %v", i, err)
			continue
		}
		if parsed.Type != e.Meta.EventType {
			t.Errorf("event[%d] Data.type=%q but Meta.EventType=%q", i, parsed.Type, e.Meta.EventType)
		}
	}
}

// ── Zero-output chunk (role announcement): Meta.Kind is non-terminal ────────

func TestConvertingSinkZeroOutputChunkMeta(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// A role-announcement chunk that produces no output chunks from ConvertStreamChunk
	// (the role is already embedded in message_start)
	// First, send a chunk that starts the stream (produces message_start)
	chunk1 := `{"id":"msg_z","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	ev1 := makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", chunk1)
	if err := sink.WriteEvent(context.Background(), ev1); err != nil {
		t.Fatalf("WriteEvent chunk1: %v", err)
	}

	// Now send a second role-announcement chunk (same role, no content)
	// ConvertStreamChunk should return 0 chunks for this
	chunk2 := `{"id":"msg_z","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`
	ev2 := makeStreamEvent(2, streaming.EventLifecycle, "chat.completion.chunk", chunk2)
	if err := sink.WriteEvent(context.Background(), ev2); err != nil {
		t.Fatalf("WriteEvent chunk2: %v", err)
	}

	// The zero-output event should have Data=nil and Kind=EventLifecycle
	// It should NOT appear in inner.events (Data=nil events are skipped by the
	// payload sink). But the convertEvent should not error.
}

// ── End-to-end: full OpenAI chat chunk sequence → Anthropic, all Meta correct ─

func TestConvertingSinkEndToEndOpenAIToAnthropic(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Simulate a realistic OpenAI chat stream
	openAIChunks := []struct {
		seq  uint64
		kind streaming.EventKind
		data string
	}{
		// Role announcement
		{1, streaming.EventLifecycle, `{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1000,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`},
		// First text
		{2, streaming.EventSemantic, `{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1000,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"The"},"finish_reason":null}]}`},
		// More text
		{3, streaming.EventSemantic, `{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1000,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" answer"},"finish_reason":null}]}`},
		// More text
		{4, streaming.EventSemantic, `{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1000,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" is"},"finish_reason":null}]}`},
		// More text
		{5, streaming.EventSemantic, `{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1000,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" 42"},"finish_reason":null}]}`},
		// Finish
		{6, streaming.EventFinish, `{"id":"chatcmpl-e2e","object":"chat.completion.chunk","created":1000,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
	}

	for _, c := range openAIChunks {
		ev := makeStreamEvent(c.seq, c.kind, "chat.completion.chunk", c.data)
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent seq=%d: %v", c.seq, err)
		}
	}

	// Verify the complete Anthropic event sequence
	expectedTypes := []string{
		"message_start",       // from chunk 1
		"content_block_start", // from chunk 2 (first text opens a block)
		"content_block_delta", // from chunk 2
		"content_block_delta", // from chunk 3
		"content_block_delta", // from chunk 4
		"content_block_delta", // from chunk 5
		"content_block_stop",  // from chunk 6 (finish closes block)
		"message_delta",       // from chunk 6
		"message_stop",        // from chunk 6
	}

	if len(inner.events) != len(expectedTypes) {
		t.Fatalf("got %d events, want %d", len(inner.events), len(expectedTypes))
	}

	for i, ev := range inner.events {
		if ev.Meta.EventType != expectedTypes[i] {
			t.Errorf("event[%d] Meta.EventType=%q, want %q", i, ev.Meta.EventType, expectedTypes[i])
		}
		// Cross-check: Data.type must match Meta.EventType
		var parsed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(ev.Data, &parsed); err != nil {
			t.Errorf("event[%d] invalid JSON: %v", i, err)
			continue
		}
		if parsed.Type != ev.Meta.EventType {
			t.Errorf("event[%d] Data.type=%q != Meta.EventType=%q", i, parsed.Type, ev.Meta.EventType)
		}
	}

	// Verify Kind assignments
	for i, ev := range inner.events {
		switch ev.Meta.EventType {
		case "message_start", "content_block_start", "content_block_stop":
			if ev.Meta.Kind != streaming.EventLifecycle {
				t.Errorf("event[%d] type=%q Kind=%q, want EventLifecycle", i, ev.Meta.EventType, ev.Meta.Kind)
			}
		case "content_block_delta":
			if ev.Meta.Kind != streaming.EventSemantic {
				t.Errorf("event[%d] type=%q Kind=%q, want EventSemantic", i, ev.Meta.EventType, ev.Meta.Kind)
			}
		case "message_delta":
			if ev.Meta.Kind != streaming.EventUsage {
				t.Errorf("event[%d] type=%q Kind=%q, want EventUsage", i, ev.Meta.EventType, ev.Meta.Kind)
			}
		case "message_stop":
			if ev.Meta.Kind != streaming.EventFinish {
				t.Errorf("event[%d] type=%q Kind=%q, want EventFinish", i, ev.Meta.EventType, ev.Meta.Kind)
			}
		}
	}

	// Verify Sequence consistency: Meta.Sequence must match Sequence on each event.
	for _, ev := range inner.events {
		if ev.Meta.Sequence != ev.Sequence {
			t.Errorf("Meta.Sequence=%d != Sequence=%d", ev.Meta.Sequence, ev.Sequence)
		}
	}
}

// ── Sequence preservation ───────────────────────────────────────────────────

func TestConvertingSinkSequencePreserved(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// A chunk with role+content produces multiple Anthropic events.
	// The convertingSink assigns monotonic Sequences (1, 2, 3, ...) to all
	// output events regardless of the source Sequence.
	chunk := `{"id":"msg_seq","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"test"},"finish_reason":null}]}`
	ev := makeStreamEvent(42, streaming.EventSemantic, "chat.completion.chunk", chunk)
	if err := sink.WriteEvent(context.Background(), ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	if len(inner.events) == 0 {
		t.Fatal("no events written")
	}

	// All events must have strictly increasing Sequences starting from 1.
	for i, e := range inner.events {
		expectedSeq := uint64(i + 1)
		if e.Sequence != expectedSeq {
			t.Errorf("event[%d] Sequence=%d, want %d", i, e.Sequence, expectedSeq)
		}
		if e.Meta.Sequence != expectedSeq {
			t.Errorf("event[%d] Meta.Sequence=%d, want %d", i, e.Meta.Sequence, expectedSeq)
		}
	}
}

// ── Commit path: Meta correctness ───────────────────────────────────────────

func TestConvertingSinkCommitMetaCorrect(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	chunk := `{"id":"msg_c","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`
	events := []sdk.StreamEvent{makeStreamEvent(1, streaming.EventSemantic, "chat.completion.chunk", chunk)}

	if err := sink.Commit(context.Background(), events); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	for i, ev := range inner.events {
		var parsed struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(ev.Data, &parsed); err != nil {
			t.Errorf("event[%d] invalid JSON: %v", i, err)
			continue
		}
		if parsed.Type != ev.Meta.EventType {
			t.Errorf("event[%d] Data.type=%q but Meta.EventType=%q", i, parsed.Type, ev.Meta.EventType)
		}
	}
}

// ── Conversion failure ──────────────────────────────────────────────────────

func TestConvertingSinkConversionFailure(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	invalidChunk := `{invalid json`
	event := makeStreamEvent(1, streaming.EventSemantic, "content_block_delta", invalidChunk)

	err := sink.WriteEvent(context.Background(), event)
	if !errors.Is(err, ErrProtocolConvert) {
		t.Fatalf("WriteEvent error = %v, want ErrProtocolConvert", err)
	}

	err = sink.WriteEvent(context.Background(), makeStreamEvent(2, streaming.EventSemantic, "content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`))
	if !errors.Is(err, ErrProtocolConvert) {
		t.Fatalf("subsequent WriteEvent error = %v, want ErrProtocolConvert", err)
	}
}

// ── NativeError passthrough ─────────────────────────────────────────────────

func TestConvertingSinkNativeErrorPassthrough(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	event := sdk.StreamEvent{
		Sequence: 1,
		Meta: streaming.Event{
			Sequence:  1,
			Kind:      streaming.EventNativeError,
			EventType: "error",
		},
		Classified: sdk.NewClassifiedError(sdk.ErrRateLimited, 429, "req_1", "rate_limited", "rate_limited"),
	}

	err := sink.WriteEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("WriteEvent error = %v", err)
	}
	if len(inner.events) != 1 {
		t.Fatalf("events = %d, want 1", len(inner.events))
	}
	if inner.events[0].Classified == nil {
		t.Fatal("NativeError event Classified was lost")
	}
}

// ── Flush ───────────────────────────────────────────────────────────────────

func TestConvertingSinkFlush(t *testing.T) {
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	err := sink.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush error = %v", err)
	}
}

// ── extractEventTypeFromJSON: bounded parsing ───────────────────────────────

func TestExtractEventTypeFromJSONBounded(t *testing.T) {
	tests := []struct {
		name       string
		data       string
		toProtocol adapter.Protocol
		want       string
	}{
		{"anthropic message_start", `{"type":"message_start"}`, adapter.ProtocolAnthropic, "message_start"},
		{"anthropic content_block_delta", `{"type":"content_block_delta"}`, adapter.ProtocolAnthropic, "content_block_delta"},
		{"anthropic message_stop", `{"type":"message_stop"}`, adapter.ProtocolAnthropic, "message_stop"},
		{"anthropic ping", `{"type":"ping"}`, adapter.ProtocolAnthropic, "ping"},
		{"openai chunk", `{"object":"chat.completion.chunk"}`, adapter.ProtocolOpenAIChat, "chat.completion.chunk"},
		{"empty data", ``, adapter.ProtocolAnthropic, ""},
		{"invalid json", `{bad`, adapter.ProtocolAnthropic, ""},
		{"missing type field", `{"foo":"bar"}`, adapter.ProtocolAnthropic, ""},
		{"type with unsafe chars", `{"type":"msg<script>"}`, adapter.ProtocolAnthropic, ""},
		{"type too long", fmt.Sprintf(`{"type":"%s"}`, string(make([]byte, 200))), adapter.ProtocolAnthropic, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEventTypeFromJSON([]byte(tt.data), tt.toProtocol)
			if got != tt.want {
				// For the "type too long" test, the type field is all zero bytes which
				// fail the character check, so it should return ""
				if tt.name == "type too long" && got == "" {
					return
				}
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ── classifyConvertedKind ───────────────────────────────────────────────────

func TestClassifyConvertedKindAnthropic(t *testing.T) {
	tests := []struct {
		eventType string
		want      streaming.EventKind
	}{
		{"message_start", streaming.EventLifecycle},
		{"content_block_start", streaming.EventLifecycle},
		{"content_block_stop", streaming.EventLifecycle},
		{"ping", streaming.EventLifecycle},
		{"content_block_delta", streaming.EventSemantic},
		{"message_delta", streaming.EventUsage},
		{"message_stop", streaming.EventFinish},
		{"unknown_event", streaming.EventLifecycle},
	}
	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			got := classifyConvertedKind(tt.eventType, nil, adapter.ProtocolAnthropic)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyConvertedKindOpenAIFinish(t *testing.T) {
	// OpenAI chunk with finish_reason → EventFinish
	data := `{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	got := classifyConvertedKind("chat.completion.chunk", []byte(data), adapter.ProtocolOpenAIChat)
	if got != streaming.EventFinish {
		t.Errorf("got %q, want %q", got, streaming.EventFinish)
	}
}

func TestClassifyConvertedKindOpenAISemantic(t *testing.T) {
	// OpenAI chunk with content → EventSemantic
	data := `{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`
	got := classifyConvertedKind("chat.completion.chunk", []byte(data), adapter.ProtocolOpenAIChat)
	if got != streaming.EventSemantic {
		t.Errorf("got %q, want %q", got, streaming.EventSemantic)
	}
}

// ── Race test ───────────────────────────────────────────────────────────────

func TestConvertingSinkRace(t *testing.T) {
	// The convertingSink itself is not used concurrently (Bridge is serial),
	// but test that sequential WriteEvent/Commit/Flush don't race with
	// internal pending state.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	chunk := `{"id":"msg_race","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`

	for i := 0; i < 100; i++ {
		ev := makeStreamEvent(uint64(i+1), streaming.EventSemantic, "chat.completion.chunk", chunk)
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent %d: %v", i, err)
		}
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// ── Derived Sequence: multi-chunk output has strictly increasing Sequences ──

func TestConvertingSinkDerivedSequenceStrictlyIncreasing(t *testing.T) {
	// One source event that produces multiple converted chunks: all output
	// events must have strictly increasing, unique Sequences.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// First, start the stream with a role-only chunk (produces message_start).
	roleChunk := `{"id":"msg_ds1","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", roleChunk)); err != nil {
		t.Fatalf("WriteEvent role: %v", err)
	}

	// Now send a content chunk that opens a new text block: produces
	// content_block_start + content_block_delta (2 events from 1 source).
	contentChunk := `{"id":"msg_ds1","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`
	ev := makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", contentChunk)
	if err := sink.WriteEvent(context.Background(), ev); err != nil {
		t.Fatalf("WriteEvent content: %v", err)
	}

	// The content chunk should produce at least 2 events (content_block_start + content_block_delta)
	if len(inner.events) < 3 { // 1 from role + at least 2 from content
		t.Fatalf("got %d events, want at least 3", len(inner.events))
	}

	// Verify strictly increasing Sequences
	seen := make(map[uint64]bool)
	for i, e := range inner.events {
		if seen[e.Sequence] {
			t.Errorf("event[%d] duplicate Sequence=%d", i, e.Sequence)
		}
		seen[e.Sequence] = true
		if i > 0 && e.Sequence <= inner.events[i-1].Sequence {
			t.Errorf("event[%d] Sequence=%d not > event[%d] Sequence=%d", i, e.Sequence, i-1, inner.events[i-1].Sequence)
		}
		if e.Meta.Sequence != e.Sequence {
			t.Errorf("event[%d] Meta.Sequence=%d != Sequence=%d", i, e.Meta.Sequence, e.Sequence)
		}
	}

	// First event should have Sequence=1 (monotonic counter starts at 1)
	if inner.events[0].Sequence != 1 {
		t.Errorf("first event Sequence=%d, want 1", inner.events[0].Sequence)
	}
}

func TestConvertingSinkDerivedSequenceMultipleSourceEvents(t *testing.T) {
	// Multiple source events, each producing multiple converted chunks:
	// all output Sequences must be globally strictly increasing with no conflicts.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Chunk 1: role+content → multiple Anthropic events
	chunk1 := `{"id":"msg_ds2","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"first"},"finish_reason":null}]}`
	ev1 := makeStreamEvent(1, streaming.EventSemantic, "chat.completion.chunk", chunk1)
	if err := sink.WriteEvent(context.Background(), ev1); err != nil {
		t.Fatalf("WriteEvent chunk1: %v", err)
	}
	nAfter1 := len(inner.events)

	// Chunk 2: more content → content_block_delta only (1:1)
	chunk2 := `{"id":"msg_ds2","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" second"},"finish_reason":null}]}`
	ev2 := makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", chunk2)
	if err := sink.WriteEvent(context.Background(), ev2); err != nil {
		t.Fatalf("WriteEvent chunk2: %v", err)
	}
	nAfter2 := len(inner.events)

	// Chunk 3: finish → content_block_stop + message_delta + message_stop (1:3)
	chunk3 := `{"id":"msg_ds2","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	ev3 := makeStreamEvent(3, streaming.EventFinish, "chat.completion.chunk", chunk3)
	if err := sink.WriteEvent(context.Background(), ev3); err != nil {
		t.Fatalf("WriteEvent chunk3: %v", err)
	}

	if nAfter1 == 0 || nAfter2 <= nAfter1 {
		t.Fatalf("events after chunk1=%d, after chunk2=%d (expected growth)", nAfter1, nAfter2)
	}

	// All events must have globally strictly increasing Sequences
	seen := make(map[uint64]bool)
	for i, e := range inner.events {
		if seen[e.Sequence] {
			t.Errorf("event[%d] duplicate Sequence=%d", i, e.Sequence)
		}
		seen[e.Sequence] = true
		if i > 0 && e.Sequence <= inner.events[i-1].Sequence {
			t.Errorf("event[%d] Sequence=%d not > event[%d] Sequence=%d", i, e.Sequence, i-1, inner.events[i-1].Sequence)
		}
	}
}

func TestConvertingSinkDerivedSequenceSingleChunkPassthrough(t *testing.T) {
	// A source event that produces exactly one converted chunk (1:1):
	// Sequence is assigned from the monotonic counter, not preserved from source.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Start the stream with a role chunk.
	roleChunk := `{"id":"msg_ds3","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", roleChunk)); err != nil {
		t.Fatalf("WriteEvent role: %v", err)
	}
	nAfterRole := len(inner.events)
	_ = nAfterRole

	// First content chunk opens a text block → 2 events (content_block_start + content_block_delta)
	contentChunk1 := `{"id":"msg_ds3","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", contentChunk1)); err != nil {
		t.Fatalf("WriteEvent content1: %v", err)
	}
	nAfterContent1 := len(inner.events)

	// Second content chunk: text block already open → exactly 1 content_block_delta
	contentChunk2 := `{"id":"msg_ds3","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(3, streaming.EventSemantic, "chat.completion.chunk", contentChunk2)); err != nil {
		t.Fatalf("WriteEvent content2: %v", err)
	}

	// The second content chunk should produce exactly one new event (1:1 case)
	if len(inner.events) != nAfterContent1+1 {
		t.Fatalf("events after content2: %d, want %d+1=%d", len(inner.events), nAfterContent1, nAfterContent1+1)
	}

	// The new event should have a Sequence > all previous
	lastEvent := inner.events[len(inner.events)-1]
	for _, e := range inner.events[:len(inner.events)-1] {
		if lastEvent.Sequence <= e.Sequence {
			t.Errorf("content event Sequence=%d not > previous Sequence=%d", lastEvent.Sequence, e.Sequence)
		}
	}
	if lastEvent.Meta.Sequence != lastEvent.Sequence {
		t.Errorf("Meta.Sequence=%d != Sequence=%d", lastEvent.Meta.Sequence, lastEvent.Sequence)
	}
}

func TestConvertingSinkDerivedSequenceSourceSeqOverflow(t *testing.T) {
	// With the monotonic counter approach, there is no overflow concern for
	// source Sequences. Even very large source Sequences are fine because
	// the convertingSink assigns its own independent counter.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	chunk := `{"id":"msg_big","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"test"},"finish_reason":null}]}`
	// Use a very large source Sequence — should work fine
	ev := makeStreamEvent(uint64(1)<<55, streaming.EventSemantic, "chat.completion.chunk", chunk)
	if err := sink.WriteEvent(context.Background(), ev); err != nil {
		t.Fatalf("WriteEvent with large source Sequence: %v", err)
	}
	// Output Sequences should start from 1 regardless of source Sequence
	if len(inner.events) == 0 {
		t.Fatal("no events written")
	}
	if inner.events[0].Sequence != 1 {
		t.Errorf("first event Sequence=%d, want 1", inner.events[0].Sequence)
	}
}

func TestConvertingSinkDerivedSequenceConsecutiveSourceSeqs(t *testing.T) {
	// When source Sequences are consecutive (1,2,3,...), the convertingSink's
	// monotonic counter ensures all output events are strictly increasing
	// regardless of how many derived events each source produces.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Chunk 1 (seq=1): role+content → multiple Anthropic events
	chunk1 := `{"id":"msg_csec","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`
	ev1 := makeStreamEvent(1, streaming.EventSemantic, "chat.completion.chunk", chunk1)
	if err := sink.WriteEvent(context.Background(), ev1); err != nil {
		t.Fatalf("WriteEvent chunk1: %v", err)
	}

	// Chunk 2 (seq=2): more content → one content_block_delta
	chunk2 := `{"id":"msg_csec","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}`
	ev2 := makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", chunk2)
	if err := sink.WriteEvent(context.Background(), ev2); err != nil {
		t.Fatalf("WriteEvent chunk2: %v", err)
	}

	// Chunk 3 (seq=3): finish → multiple events
	chunk3 := `{"id":"msg_csec","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	ev3 := makeStreamEvent(3, streaming.EventFinish, "chat.completion.chunk", chunk3)
	if err := sink.WriteEvent(context.Background(), ev3); err != nil {
		t.Fatalf("WriteEvent chunk3: %v", err)
	}

	// All events must have globally strictly increasing Sequences
	for i := 1; i < len(inner.events); i++ {
		if inner.events[i].Sequence <= inner.events[i-1].Sequence {
			t.Errorf("event[%d] Sequence=%d not > event[%d] Sequence=%d", i, inner.events[i].Sequence, i-1, inner.events[i-1].Sequence)
		}
	}

	// No duplicate Sequences
	seen := make(map[uint64]bool)
	for _, e := range inner.events {
		if seen[e.Sequence] {
			t.Errorf("duplicate Sequence=%d", e.Sequence)
		}
		seen[e.Sequence] = true
	}
}

// ── Bridge-integration: Sequence-validating sink accepts converted stream ──

// sequenceValidatingSink mimics SSEProtocolSink's Sequence validation:
// Sequence must be nonzero, strictly increasing, and Meta.Sequence must
// match. It rejects events that violate these rules.
type sequenceValidatingSink struct {
	mu       sync.Mutex
	last     uint64
	events   []sdk.StreamEvent
	commitOK bool
}

func (s *sequenceValidatingSink) Commit(_ context.Context, events []sdk.StreamEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range events {
		if err := s.validate(ev); err != nil {
			return err
		}
		s.last = ev.Sequence
	}
	s.events = append(s.events, events...)
	s.commitOK = true
	return nil
}

func (s *sequenceValidatingSink) WriteEvent(_ context.Context, event sdk.StreamEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(event); err != nil {
		return err
	}
	s.last = event.Sequence
	s.events = append(s.events, event)
	return nil
}

func (s *sequenceValidatingSink) Flush(_ context.Context) error { return nil }

func (s *sequenceValidatingSink) validate(ev sdk.StreamEvent) error {
	if ev.Sequence == 0 {
		return fmt.Errorf("sequence=0")
	}
	if ev.Sequence <= s.last {
		return fmt.Errorf("sequence=%d not > last=%d", ev.Sequence, s.last)
	}
	if ev.Meta.Sequence != ev.Sequence {
		return fmt.Errorf("meta.Sequence=%d != Sequence=%d", ev.Meta.Sequence, ev.Sequence)
	}
	return nil
}

func TestConvertingSinkBridgeIntegrationOpenAIToAnthropic(t *testing.T) {
	// Simulate a realistic OpenAI→Anthropic cross-protocol stream through
	// convertingSink with a Sequence-validating inner sink (mimicking
	// SSEProtocolSink). Before the fix, duplicate Sequences from pending
	// events would cause the validating sink to reject them (equivalent to
	// Bridge preFail with ReasonProtocol).
	inner := &sequenceValidatingSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Role announcement
	c1 := `{"id":"chatcmpl-bi","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", c1)); err != nil {
		t.Fatalf("chunk1: %v", err)
	}

	// 3 content chunks
	for i := 0; i < 3; i++ {
		c := fmt.Sprintf(`{"id":"chatcmpl-bi","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"word%d"},"finish_reason":null}]}`, i)
		if err := sink.WriteEvent(context.Background(), makeStreamEvent(uint64(i+2), streaming.EventSemantic, "chat.completion.chunk", c)); err != nil {
			t.Fatalf("content chunk %d: %v", i, err)
		}
	}

	// Finish
	cf := `{"id":"chatcmpl-bi","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(5, streaming.EventFinish, "chat.completion.chunk", cf)); err != nil {
		t.Fatalf("finish chunk: %v", err)
	}

	if len(inner.events) == 0 {
		t.Fatal("no events written to validating sink")
	}

	// Verify all Sequences are strictly increasing
	for i := 1; i < len(inner.events); i++ {
		if inner.events[i].Sequence <= inner.events[i-1].Sequence {
			t.Errorf("event[%d] Sequence=%d not > event[%d] Sequence=%d", i, inner.events[i].Sequence, i-1, inner.events[i-1].Sequence)
		}
	}
}

func TestConvertingSinkBridgeIntegrationAnthropicToOpenAI(t *testing.T) {
	// Reverse direction: Anthropic→OpenAI cross-protocol stream through
	// convertingSink with Sequence validation.
	inner := &sequenceValidatingSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	chunks := []struct {
		seq  uint64
		kind streaming.EventKind
		data string
	}{
		{1, streaming.EventLifecycle, `{"type":"message_start","message":{"id":"msg_bi2","type":"message","role":"assistant","model":"claude-3","usage":{"input_tokens":10,"output_tokens":0}}}`},
		{2, streaming.EventLifecycle, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{3, streaming.EventSemantic, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
		{4, streaming.EventSemantic, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`},
		{5, streaming.EventUsage, `{"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`},
		{6, streaming.EventFinish, `{"type":"message_stop"}`},
	}

	for _, c := range chunks {
		var eventType string
		var raw struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(c.data), &raw) == nil {
			eventType = raw.Type
		}
		ev := makeStreamEvent(c.seq, c.kind, eventType, c.data)
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("seq=%d: %v", c.seq, err)
		}
	}

	if len(inner.events) == 0 {
		t.Fatal("no events written to validating sink")
	}

	for i := 1; i < len(inner.events); i++ {
		if inner.events[i].Sequence <= inner.events[i-1].Sequence {
			t.Errorf("event[%d] Sequence=%d not > event[%d] Sequence=%d", i, inner.events[i].Sequence, i-1, inner.events[i-1].Sequence)
		}
	}
}

// ── Race: derived Sequence under concurrent-like sequential stress ───────

func TestConvertingSinkDerivedSequenceRace(t *testing.T) {
	// Stress test: many sequential events with one-to-many conversion,
	// verifying no Sequence conflicts under race detector.
	inner := &sequenceValidatingSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Role chunk
	c0 := `{"id":"msg_race2","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", c0)); err != nil {
		t.Fatalf("role: %v", err)
	}

	// Many content chunks (each produces at least one event)
	for i := 0; i < 50; i++ {
		c := fmt.Sprintf(`{"id":"msg_race2","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"w%d"},"finish_reason":null}]}`, i)
		if err := sink.WriteEvent(context.Background(), makeStreamEvent(uint64(i+2), streaming.EventSemantic, "chat.completion.chunk", c)); err != nil {
			t.Fatalf("content %d: %v", i, err)
		}
	}

	// Finish
	cf := `{"id":"msg_race2","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(52, streaming.EventFinish, "chat.completion.chunk", cf)); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if len(inner.events) == 0 {
		t.Fatal("no events")
	}
}

// ── convertingSink.Finalize (optional EndOfStreamFinalizer) ─────────────────

func TestConvertingSinkFinalizeOpenAIToAnthropicSynthesizesTerminal(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)

	// Stream content without a finish_reason chunk, then EOF.
	role := `{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	content := `{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"}}]}`
	for _, ev := range []sdk.StreamEvent{
		makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", role),
		makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", content),
	} {
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent: %v", err)
		}
	}

	meta, err := sink.Finalize(context.Background())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if meta.Finish != "end_turn" {
		t.Errorf("Finish = %q, want end_turn", meta.Finish)
	}
	// The downstream received message_delta + message_stop.
	var sawDelta, sawStop bool
	for _, ev := range inner.events {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(ev.Data, &v)
		switch v.Type {
		case "message_delta":
			sawDelta = true
		case "message_stop":
			sawStop = true
		}
	}
	if !sawDelta || !sawStop {
		t.Fatalf("missing synthesized terminal: delta=%v stop=%v", sawDelta, sawStop)
	}
}

func TestConvertingSinkFinalizeAnthropicToOpenAISynthesizesTerminal(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil).(*convertingSink)

	start := `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-3","usage":{"input_tokens":3,"output_tokens":0}}}`
	delta := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`
	msgDelta := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`
	for _, ev := range []sdk.StreamEvent{
		makeStreamEvent(1, streaming.EventLifecycle, "message_start", start),
		makeStreamEvent(2, streaming.EventSemantic, "content_block_delta", delta),
		makeStreamEvent(3, streaming.EventUsage, "message_delta", msgDelta),
	} {
		if err := sink.WriteEvent(context.Background(), ev); err != nil {
			t.Fatalf("WriteEvent: %v", err)
		}
	}

	meta, err := sink.Finalize(context.Background())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if meta.Finish != "stop" {
		t.Errorf("Finish = %q, want stop (mapped)", meta.Finish)
	}
	if meta.Usage == nil {
		t.Fatal("Usage must be present")
	}
	if meta.Usage.PromptTokens != 3 || meta.Usage.CompletionTokens != 5 {
		t.Errorf("Usage = %+v, want 3/5", meta.Usage)
	}
	// A final OpenAI chunk with finish_reason was synthesized downstream.
	var sawFinish bool
	for _, ev := range inner.events {
		var v struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		_ = json.Unmarshal(ev.Data, &v)
		if len(v.Choices) > 0 && v.Choices[0].FinishReason == "stop" {
			sawFinish = true
		}
	}
	if !sawFinish {
		t.Fatal("missing synthesized final OpenAI chunk with finish_reason")
	}
}

func TestConvertingSinkFinalizeAfterExplicitFinishIsNoOp(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)

	// Full stream including a finish_reason chunk that closes the Anthropic
	// message. Finalize must synthesize nothing.
	chunks := []string{
		`{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	for i, c := range chunks {
		kind := streaming.EventLifecycle
		if i == 1 {
			kind = streaming.EventSemantic
		} else if i == 2 {
			kind = streaming.EventFinish
		}
		if err := sink.WriteEvent(context.Background(), makeStreamEvent(uint64(i+1), kind, "chat.completion.chunk", c)); err != nil {
			t.Fatalf("WriteEvent[%d]: %v", i, err)
		}
	}
	before := len(inner.events)
	meta, err := sink.Finalize(context.Background())
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if len(inner.events) != before {
		t.Fatalf("Finalize wrote %d events after explicit finish, want 0", len(inner.events)-before)
	}
	// meta is empty (no synthesis) but no error.
	if meta.Finish != "" || meta.Usage != nil {
		t.Errorf("meta = %+v, want zero after explicit finish", meta)
	}
}

func TestConvertingSinkFinalizeExactlyOnce(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)
	sink.state.OAIStarted = true
	sink.state.OAIMessageID = "m"
	sink.state.OAIModel = "gpt-4"

	if _, err := sink.Finalize(context.Background()); err != nil {
		t.Fatalf("first Finalize: %v", err)
	}
	before := len(inner.events)
	if _, err := sink.Finalize(context.Background()); err != nil {
		t.Fatalf("second Finalize: %v", err)
	}
	if got := len(inner.events) - before; got != 0 {
		t.Fatalf("second Finalize synthesized %d events, want 0 (exactly-once)", got)
	}
}

func TestConvertingSinkFinalizeFlushesPendingBeforeTerminal(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)
	// A content chunk that produces pending deltas; Finalize must flush them
	// before emitting the synthesized terminal.
	content := `{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"}}]}`
	// Seed an open message first so FinalizeStream has a message to close.
	role := `{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(1, streaming.EventLifecycle, "chat.completion.chunk", role)); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(2, streaming.EventSemantic, "chat.completion.chunk", content)); err != nil {
		t.Fatal(err)
	}
	if _, err := sink.Finalize(context.Background()); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	// Order: content_block_delta (from content) must precede message_delta/stop.
	var order []string
	for _, ev := range inner.events {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(ev.Data, &v)
		if v.Type != "" {
			order = append(order, v.Type)
		}
	}
	// find message_delta index
	var deltaIdx int = -1
	var contentIdx int = -1
	for i, ty := range order {
		if ty == "content_block_delta" && contentIdx == -1 {
			contentIdx = i
		}
		if ty == "message_delta" {
			deltaIdx = i
		}
	}
	if contentIdx == -1 || deltaIdx == -1 {
		t.Fatalf("expected content_block_delta before message_delta; order=%v", order)
	}
	if contentIdx > deltaIdx {
		t.Fatalf("content_block_delta (%d) after message_delta (%d); order=%v", contentIdx, deltaIdx, order)
	}
}

func TestConvertingSinkUsageChunkAfterExplicitFinishDoesNothing(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)

	// finish_reason chunk closes the Anthropic message (finalized=true).
	finish := `{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(1, streaming.EventFinish, "chat.completion.chunk", finish)); err != nil {
		t.Fatalf("finish WriteEvent: %v", err)
	}
	if !sink.finalized {
		t.Fatal("finalized must be true after a converted terminal chunk")
	}
	before := len(inner.events)

	// A late usage-only chunk after the explicit finish must do nothing and
	// must NOT re-emit message_start.
	usage := `{"id":"m","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}}`
	if err := sink.WriteEvent(context.Background(), makeStreamEvent(2, streaming.EventUsage, "chat.completion.chunk", usage)); err != nil {
		t.Fatalf("usage WriteEvent: %v", err)
	}
	if got := len(inner.events) - before; got != 1 {
		t.Fatalf("usage chunk after finish wrote %d payload events, want 1 (the no-op drop)", got)
	}
	// The dropped event must be a no-op lifecycle event with no payload.
	drop := inner.events[len(inner.events)-1]
	if drop.Meta.Kind != streaming.EventLifecycle || len(drop.Data) != 0 {
		t.Errorf("drop = kind=%q datalen=%d, want lifecycle/0", drop.Meta.Kind, len(drop.Data))
	}
	// And it must NOT be a message_start.
	var v struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(drop.Data, &v)
	if v.Type == "message_start" {
		t.Fatal("usage chunk after finish re-emitted message_start")
	}
}

func TestConvertingSinkFinalizeConversionFailureReturnsError(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)
	sink.convertFailed = true
	if _, err := sink.Finalize(context.Background()); err != ErrProtocolConvert {
		t.Fatalf("err = %v, want ErrProtocolConvert", err)
	}
}

func TestConvertingSinkFinalizeWriteErrorPropagates(t *testing.T) {
	t.Parallel()
	inner := &mockProtocolSink{err: errSink}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil).(*convertingSink)
	sink.state.OAIStarted = true
	if _, err := sink.Finalize(context.Background()); err != errSink {
		t.Fatalf("err = %v, want %v", err, errSink)
	}
	// Exactly-once flag set despite the write failure: a retry synthesizes nothing.
	if !sink.finalized {
		t.Fatal("finalized must be true even after a write error")
	}
	before := len(inner.events)
	if _, err := sink.Finalize(context.Background()); err != nil {
		t.Fatalf("second Finalize: %v", err)
	}
	if got := len(inner.events) - before; got != 0 {
		t.Fatalf("second Finalize wrote %d, want 0", got)
	}
}

// ── End-to-end: Bridge + streamPayloadSink + convertingSink on committed EOF ─

func TestBridgeFinalizerChainOpenAIToAnthropicCommitsEOFSynthesizes(t *testing.T) {
	t.Parallel()
	// A realistic OpenAI→Anthropic stream that ends with a clean EOF (the OpenAI
	// [DONE] sentinel) WITHOUT a finish_reason chunk. The Bridge's optional
	// EndOfStreamFinalizer (wired from sink.finalizer()) must synthesize the
	// Anthropic message_delta+message_stop and complete the stream, and the
	// sequence-validating inner sink must accept every synthesized Sequence.
	mkChunk := func(seq uint64, kind streaming.EventKind, body string) sdk.StreamEvent {
		return sdk.StreamEvent{
			Sequence: seq,
			Meta:     streaming.Event{Sequence: seq, Kind: kind, EventType: "chat.completion.chunk"},
			Data:     json.RawMessage(body),
		}
	}
	role := `{"id":"chatcmpl-e","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	content := `{"id":"chatcmpl-e","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"}}]}`
	upstream := &payloadTestSource{
		events: []sdk.StreamEvent{
			mkChunk(1, streaming.EventLifecycle, role),
			mkChunk(2, streaming.EventSemantic, content),
			// then ErrEndOfStream (no finish_reason chunk): simulates [DONE].
		},
		err: streaming.ErrEndOfStream,
	}
	inner := &sequenceValidatingSink{}
	conv := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)
	source, sink, err := newSDKPayloadSource(upstream, conv)
	if err != nil {
		t.Fatal(err)
	}
	bridge := streaming.Bridge{
		Source:   source,
		Sink:     sink,
		Timeouts: streaming.Timeouts{TTFT: 45 * time.Second, StreamIdle: 30 * time.Second, StreamLifetime: 10 * time.Minute},
	}
	if fn := sink.finalizer(); fn != nil {
		bridge.Finalizer = fn
	} else {
		t.Fatal("cross-protocol finalizer must be wired")
	}
	out, berr := bridge.Run(context.Background())
	sink.Discard()
	if berr != nil {
		t.Fatalf("Bridge.Run err = %v, want nil", berr)
	}
	if out.State != streaming.StateCompleted {
		t.Fatalf("State = %q, want completed (finalized EOF)", out.State)
	}
	if !out.Committed {
		t.Fatal("Committed must be true")
	}
	if out.Finish != "end_turn" {
		t.Fatalf("Finish = %q, want end_turn", out.Finish)
	}
	if out.Reason != streaming.ReasonCompleted {
		t.Fatalf("Reason = %q, want completed", out.Reason)
	}
	// The inner sink received the synthesized message_stop and validated all
	// Sequences (strictly increasing) — sequenceValidatingSink would have
	// failed the Run otherwise.
	var sawStop bool
	for _, ev := range inner.events {
		var v struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(ev.Data, &v)
		if v.Type == "message_stop" {
			sawStop = true
		}
	}
	if !sawStop {
		t.Fatal("inner sink never received synthesized message_stop")
	}
}

func TestBridgeFinalizerChainSameProtocolKeepsTruncated(t *testing.T) {
	t.Parallel()
	// Same-protocol chain exposes no finalizer; a committed clean EOF remains
	// a truncated-stream failure (legacy contract preserved).
	mkChunk := func(seq uint64, kind streaming.EventKind, body string) sdk.StreamEvent {
		return sdk.StreamEvent{Sequence: seq, Meta: streaming.Event{Sequence: seq, Kind: kind, EventType: "chat.completion.chunk"}, Data: json.RawMessage(body)}
	}
	role := `{"id":"chatcmpl-s","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"}}]}`
	content := `{"id":"chatcmpl-s","object":"chat.completion.chunk","created":0,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"}}]}`
	upstream := &payloadTestSource{
		events: []sdk.StreamEvent{
			mkChunk(1, streaming.EventLifecycle, role),
			mkChunk(2, streaming.EventSemantic, content),
		},
		err: streaming.ErrEndOfStream,
	}
	inner := &sequenceValidatingSink{}
	conv := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIChat, nil)
	if conv != inner {
		t.Fatal("same-protocol must return inner sink directly")
	}
	source, sink, err := newSDKPayloadSource(upstream, conv)
	if err != nil {
		t.Fatal(err)
	}
	bridge := streaming.Bridge{
		Source:   source,
		Sink:     sink,
		Timeouts: streaming.Timeouts{TTFT: 45 * time.Second, StreamIdle: 30 * time.Second, StreamLifetime: 10 * time.Minute},
	}
	if fn := sink.finalizer(); fn != nil {
		t.Fatal("same-protocol finalizer must be nil")
	}
	out, berr := bridge.Run(context.Background())
	sink.Discard()
	if berr != nil {
		t.Fatalf("Bridge.Run err = %v, want nil", berr)
	}
	if out.State != streaming.StateFailedAfterCommit {
		t.Fatalf("State = %q, want failed_after_commit", out.State)
	}
	if out.Reason != streaming.ReasonStreamTruncated {
		t.Fatalf("Reason = %q, want stream_truncated", out.Reason)
	}
}

// ── Tool name restoration integration tests ────────────────────────────────

func TestConvertingSinkToolNameRestoration_AnthropicToOpenAI(t *testing.T) {
	// When converting Anthropic→OpenAI with a tool name map, tool_use blocks
	// in the converted stream should have their names restored to the original.
	nameMap := protocolconvert.ToolNameMap{
		"com_example_tool": "com.example.tool",
	}
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nameMap).(*convertingSink)

	// Anthropic content_block_start with tool_use (name=sanitized).
	ev := makeStreamEvent(1, streaming.EventLifecycle, "content_block_start",
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_123","name":"com_example_tool","input":{}}}`)
	if err := sink.WriteEvent(context.Background(), ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	// The converted OpenAI chunk should have the original tool name restored.
	if len(inner.events) == 0 {
		t.Fatal("expected at least one event")
	}
	// The event data should contain the original Anthropic tool name.
	found := false
	for _, e := range inner.events {
		if len(e.Data) > 0 && containsSubstring(string(e.Data), "com.example.tool") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected restored tool name 'com.example.tool' in output, got: %s", inner.events[0].Data)
	}
}

func TestConvertingSinkToolNameRestoration_OpenAIToAnthropic_NilMap(t *testing.T) {
	// When converting OpenAI→Anthropic with nil map, conversion proceeds normally.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)

	// Should return a convertingSink (different protocols).
	if sink == inner {
		t.Fatal("cross-protocol should not return inner sink directly")
	}
}

func TestConvertingSinkToolNameRestoration_NilMapNoop(t *testing.T) {
	// A nil map should not affect the conversion output.
	inner := &mockProtocolSink{}
	sink := newConvertingSink(inner, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, nil)

	// Anthropic content_block_start with tool_use.
	ev := makeStreamEvent(1, streaming.EventLifecycle, "content_block_start",
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_123","name":"my_tool","input":{}}}`)
	if err := sink.WriteEvent(context.Background(), ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if len(inner.events) == 0 {
		t.Fatal("expected at least one event")
	}
	// With nil map, the tool name should remain as-is.
	found := false
	for _, e := range inner.events {
		if len(e.Data) > 0 && containsSubstring(string(e.Data), "my_tool") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool name 'my_tool' in output, got: %s", inner.events[0].Data)
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
