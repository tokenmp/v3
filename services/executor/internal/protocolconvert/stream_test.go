package protocolconvert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── Stream: OpenAI → Anthropic ──────────────────────────────────────────────

func TestConvertStreamChunk_OpenAIToAnthropic_TextStream(t *testing.T) {
	state := &StreamState{}

	// First chunk with role
	chunk1 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	})
	results1, err := ConvertStreamChunk(chunk1, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("chunk1: %v", err)
	}
	if len(results1) < 1 {
		t.Fatal("expected at least message_start")
	}
	var startEvent struct {
		Type string `json:"type"`
	}
	json.Unmarshal(results1[0], &startEvent)
	if startEvent.Type != "message_start" {
		t.Errorf("first event type = %v, want message_startF", startEvent.Type)
	}

	// Text content chunk
	chunk2 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "Hello"}}},
	})
	results2, err := ConvertStreamChunk(chunk2, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("chunk2: %v", err)
	}
	if len(results2) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(results2))
	}

	// Finish chunk
	chunk3 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-s1", "object": "chat.completion.chunk", "created": 3, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	})
	results3, err := ConvertStreamChunk(chunk3, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("chunk3: %v", err)
	}
	if len(results3) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(results3))
	}
}

func TestConvertStreamChunk_OpenAIToAnthropic_DoneSentinel(t *testing.T) {
	state := &StreamState{}
	state.OAIStarted = true
	state.OAIMessageID = "chatcmpl-s1"
	state.OAIModel = "gpt-4"
	state.OAIFinishReason = "end_turn"

	results, err := ConvertStreamChunk([]byte("[DONE]"), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("[DONE]: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 events for [DONE], got %d", len(results))
	}
}

func TestConvertStreamChunk_OpenAIToAnthropic_ToolCalls(t *testing.T) {
	state := &StreamState{}

	// message_start
	chunk0 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	})
	ConvertStreamChunk(chunk0, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)

	// Tool call start
	chunk1 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": ""},
			}},
		}}},
	})
	results1, err := ConvertStreamChunk(chunk1, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("tool call start: %v", err)
	}
	if len(results1) < 1 {
		t.Fatal("expected content_block_start for tool_use")
	}

	// Tool call arguments delta
	chunk2 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk", "created": 3, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index":    0,
				"function": map[string]any{"arguments": `{"city":"SF"}`},
			}},
		}}},
	})
	results2, err := ConvertStreamChunk(chunk2, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("tool call delta: %v", err)
	}
	if len(results2) < 1 {
		t.Fatal("expected content_block_delta for input_json_delta")
	}

	// Finish
	chunk3 := mustMarshal(t, map[string]any{
		"id": "chatcmpl-t1", "object": "chat.completion.chunk", "created": 4, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
	})
	results3, err := ConvertStreamChunk(chunk3, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if len(results3) < 2 {
		t.Fatalf("expected at least 2 events for finish, got %d", len(results3))
	}
}

// ── Stream: Anthropic → OpenAI ──────────────────────────────────────────────

func TestConvertStreamChunk_AnthropicToOpenAI_TextStream(t *testing.T) {
	state := &StreamState{}

	// message_start
	chunk1 := mustMarshal(t, map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_s1", "type": "message", "role": "assistant", "model": "claude-3",
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 0},
		},
	})
	results1, err := ConvertStreamChunk(chunk1, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("message_start: %v", err)
	}
	if len(results1) != 0 {
		t.Errorf("expected 0 results for message_start, got %d", len(results1))
	}

	// content_block_start (text)
	chunk2 := mustMarshal(t, map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	results2, err := ConvertStreamChunk(chunk2, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("content_block_start: %v", err)
	}
	if len(results2) < 1 {
		t.Fatal("expected role announcement chunk")
	}

	// content_block_delta (text)
	chunk3 := mustMarshal(t, map[string]any{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": "Hello"},
	})
	results3, err := ConvertStreamChunk(chunk3, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("content_block_delta: %v", err)
	}
	if len(results3) < 1 {
		t.Fatal("expected content chunk")
	}

	// content_block_stop
	chunk4 := mustMarshal(t, map[string]any{"type": "content_block_stop", "index": 0})
	results4, err := ConvertStreamChunk(chunk4, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("content_block_stop: %v", err)
	}
	if len(results4) != 0 {
		t.Errorf("expected 0 results for content_block_stop, got %d", len(results4))
	}

	// message_delta
	chunk5 := mustMarshal(t, map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"type": "message_delta", "stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 5},
	})
	results5, err := ConvertStreamChunk(chunk5, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("message_delta: %v", err)
	}
	if len(results5) != 0 {
		t.Errorf("expected 0 results for message_delta, got %d", len(results5))
	}

	// message_stop
	chunk6 := mustMarshal(t, map[string]any{"type": "message_stop"})
	results6, err := ConvertStreamChunk(chunk6, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("message_stop: %v", err)
	}
	if len(results6) < 1 {
		t.Fatal("expected final chunk with finish_reason")
	}
	finalChunk := mustUnmarshal(t, results6[0])
	choices := finalChunk["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
}

func TestConvertStreamChunk_AnthropicToOpenAI_ToolUse(t *testing.T) {
	state := &StreamState{}

	// message_start
	ConvertStreamChunk(mustMarshal(t, map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_t1", "type": "message", "role": "assistant", "model": "claude-3",
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 0},
		},
	}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)

	// content_block_start (tool_use)
	results, err := ConvertStreamChunk(mustMarshal(t, map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "tool_use", "id": "tu_1", "name": "get_weather", "input": map[string]any{}},
	}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("tool_use start: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected tool_calls chunk")
	}

	// content_block_delta (input_json_delta)
	results2, err := ConvertStreamChunk(mustMarshal(t, map[string]any{
		"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"city":"SF"}`},
	}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("input_json_delta: %v", err)
	}
	if len(results2) < 1 {
		t.Fatal("expected arguments delta chunk")
	}

	// content_block_stop
	ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "content_block_stop", "index": 0}),
		adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)

	// message_delta + message_stop
	ConvertStreamChunk(mustMarshal(t, map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"type": "message_delta", "stop_reason": "tool_use", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 10},
	}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)

	results3, err := ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "message_stop"}),
		adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("message_stop: %v", err)
	}
	if len(results3) < 1 {
		t.Fatal("expected final chunk")
	}
	finalChunk := mustUnmarshal(t, results3[0])
	choices := finalChunk["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice["finish_reason"])
	}
}

func TestConvertStreamChunk_AnthropicToOpenAI_Ping(t *testing.T) {
	state := &StreamState{}
	results, err := ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "ping"}),
		adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("ping should produce no chunks, got %d", len(results))
	}
}

// ── Stream round-trip: OpenAI → Anthropic → OpenAI ─────────────────────────

func TestStreamRoundTrip_OpenAI_TextStream(t *testing.T) {
	oaiChunks := [][]byte{
		mustMarshal(t, map[string]any{
			"id": "chatcmpl-rt1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}}},
		}),
		mustMarshal(t, map[string]any{
			"id": "chatcmpl-rt1", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "Hello"}}},
		}),
		mustMarshal(t, map[string]any{
			"id": "chatcmpl-rt1", "object": "chat.completion.chunk", "created": 3, "model": "gpt-4",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": " world"}}},
		}),
		mustMarshal(t, map[string]any{
			"id": "chatcmpl-rt1", "object": "chat.completion.chunk", "created": 4, "model": "gpt-4",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7},
		}),
	}

	// Phase 1: OpenAI → Anthropic
	antState := &StreamState{}
	var antEvents [][]byte
	for _, chunk := range oaiChunks {
		results, err := ConvertStreamChunk(chunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, antState)
		if err != nil {
			t.Fatalf("OAI→Ant chunk: %v", err)
		}
		antEvents = append(antEvents, results...)
	}
	if len(antEvents) == 0 {
		t.Fatal("no Anthropic events produced")
	}

	// Phase 2: Anthropic → OpenAI
	oaiState2 := &StreamState{}
	var oaiChunks2 [][]byte
	for _, ev := range antEvents {
		results, err := ConvertStreamChunk(ev, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, oaiState2)
		if err != nil {
			t.Fatalf("Ant→OAI event: %v", err)
		}
		oaiChunks2 = append(oaiChunks2, results...)
	}
	if len(oaiChunks2) == 0 {
		t.Fatal("no OpenAI chunks produced in round-trip")
	}

	var gotContent string
	var gotFinish string
	for _, c := range oaiChunks2 {
		m := mustUnmarshal(t, c)
		if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
			choice := choices[0].(map[string]any)
			if delta, ok := choice["delta"].(map[string]any); ok {
				if content, ok := delta["content"]; ok {
					gotContent += content.(string)
				}
			}
			if fr, ok := choice["finish_reason"]; ok && fr != nil {
				gotFinish = fr.(string)
			}
		}
	}
	if gotContent != "Hello world" {
		t.Errorf("round-trip content = %q, want 'Hello world'", gotContent)
	}
	if gotFinish != "stop" {
		t.Errorf("round-trip finish = %q, want 'stop'", gotFinish)
	}
}

// ── Stream round-trip: Anthropic → OpenAI → Anthropic ──────────────────────

func TestStreamRoundTrip_Anthropic_TextStream(t *testing.T) {
	antEvents := [][]byte{
		mustMarshal(t, map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": "msg-rt1", "type": "message", "role": "assistant", "model": "claude-3",
				"usage": map[string]any{"input_tokens": 5, "output_tokens": 0},
			},
		}),
		mustMarshal(t, map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""},
		}),
		mustMarshal(t, map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hello"},
		}),
		mustMarshal(t, map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": " there"},
		}),
		mustMarshal(t, map[string]any{"type": "content_block_stop", "index": 0}),
		mustMarshal(t, map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"type": "message_delta", "stop_reason": "end_turn", "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 2},
		}),
		mustMarshal(t, map[string]any{"type": "message_stop"}),
	}

	// Phase 1: Anthropic → OpenAI
	oaiState := &StreamState{}
	var oaiChunks [][]byte
	for _, ev := range antEvents {
		results, err := ConvertStreamChunk(ev, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, oaiState)
		if err != nil {
			t.Fatalf("Ant→OAI: %v", err)
		}
		oaiChunks = append(oaiChunks, results...)
	}
	if len(oaiChunks) == 0 {
		t.Fatal("no OpenAI chunks produced")
	}

	// Phase 2: OpenAI → Anthropic
	antState2 := &StreamState{}
	var antEvents2 [][]byte
	for _, chunk := range oaiChunks {
		results, err := ConvertStreamChunk(chunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, antState2)
		if err != nil {
			t.Fatalf("OAI→Ant: %v", err)
		}
		antEvents2 = append(antEvents2, results...)
	}
	if len(antEvents2) == 0 {
		t.Fatal("no Anthropic events produced in round-trip")
	}

	var gotText string
	var gotStopReason string
	for _, ev := range antEvents2 {
		m := mustUnmarshal(t, ev)
		switch m["type"] {
		case "content_block_delta":
			if delta, ok := m["delta"].(map[string]any); ok {
				if text, ok := delta["text"]; ok {
					gotText += text.(string)
				}
			}
		case "message_delta":
			if delta, ok := m["delta"].(map[string]any); ok {
				gotStopReason = delta["stop_reason"].(string)
			}
		}
	}
	if gotText != "Hello there" {
		t.Errorf("round-trip text = %q, want 'Hello there'", gotText)
	}
	if gotStopReason != "end_turn" {
		t.Errorf("round-trip stop_reason = %q, want 'end_turn'", gotStopReason)
	}
}

// ── Fuzz tests ──────────────────────────────────────────────────────────────

func FuzzConvertRequest_OpenAIToAnthropic(f *testing.F) {
	f.Add([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`))
	f.Add([]byte(`not json at all`))
	f.Add([]byte(``))
	f.Add([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]}`))
	f.Add([]byte(`{"__proto__":{}}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
		if err != nil {
			return
		}
		if !json.Valid(result) {
			t.Errorf("result is not valid JSON: %s", string(result[:min(len(result), 200)]))
		}
		if bytes.Contains(result, []byte("password")) || bytes.Contains(result, []byte("secret")) {
			t.Errorf("result contains sensitive marker")
		}
	})
}

func FuzzConvertRequest_AnthropicToOpenAI(f *testing.F) {
	f.Add([]byte(`{"model":"claude-3","max_tokens":1024,"messages":[{"role":"user","content":"hello"}]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, body []byte) {
		result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
		if err != nil {
			return
		}
		if !json.Valid(result) {
			t.Errorf("result is not valid JSON: %s", string(result[:min(len(result), 200)]))
		}
	})
}

func FuzzConvertResponse_OpenAIToAnthropic(f *testing.F) {
	f.Add([]byte(fmt.Sprintf(`{"id":"c1","object":"chat.completion","model":"gpt-4","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)))
	f.Add([]byte(`bad`))
	f.Fuzz(func(t *testing.T, body []byte) {
		result, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
		if err != nil {
			return
		}
		if !json.Valid(result) {
			t.Errorf("result is not valid JSON")
		}
	})
}

func FuzzConvertResponse_AnthropicToOpenAI(f *testing.F) {
	f.Add([]byte(`{"id":"m1","type":"message","role":"assistant","model":"claude-3","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	f.Add([]byte(`bad`))
	f.Fuzz(func(t *testing.T, body []byte) {
		result, err := ConvertResponse(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
		if err != nil {
			return
		}
		if !json.Valid(result) {
			t.Errorf("result is not valid JSON")
		}
	})
}

func FuzzConvertStreamChunk_OpenAIToAnthropic(f *testing.F) {
	f.Add([]byte(`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"}}]}`))
	f.Add([]byte(`[DONE]`))
	f.Add([]byte(`bad`))
	f.Fuzz(func(t *testing.T, body []byte) {
		state := &StreamState{}
		results, err := ConvertStreamChunk(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
		if err != nil {
			return
		}
		for _, r := range results {
			if !json.Valid(r) {
				t.Errorf("stream result is not valid JSON")
			}
		}
	})
}

func FuzzConvertStreamChunk_AnthropicToOpenAI(f *testing.F) {
	f.Add([]byte(`{"type":"message_start","message":{"id":"m1","type":"message","role":"assistant","model":"claude-3","usage":{"input_tokens":1,"output_tokens":0}}}`))
	f.Add([]byte(`{"type":"ping"}`))
	f.Add([]byte(`bad`))
	f.Fuzz(func(t *testing.T, body []byte) {
		state := &StreamState{}
		results, err := ConvertStreamChunk(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
		if err != nil {
			return
		}
		for _, r := range results {
			if !json.Valid(r) {
				t.Errorf("stream result is not valid JSON")
			}
		}
	})
}

// ── FinalizeStream ─────────────────────────────────────────────────────────

func TestFinalizeStream_NilStateOrUnsupported(t *testing.T) {
	t.Parallel()
	if _, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil); err != ErrInvalidStreamChunk {
		t.Errorf("nil state: err=%v want %v", err, ErrInvalidStreamChunk)
	}
	if _, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIChat, &StreamState{}); err != ErrUnsupportedConversion {
		t.Errorf("same protocol: err=%v want %v", err, ErrUnsupportedConversion)
	}
}

func TestFinalizeStream_OpenAIToAnthropic_SynthesizesTerminalPair(t *testing.T) {
	t.Parallel()
	state := &StreamState{}
	// Build state by converting a content-only stream that never sends a
	// finish_reason chunk (then EOF). The provider relied on [DONE] to end.
	role := mustMarshal(t, map[string]any{
		"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	})
	content := mustMarshal(t, map[string]any{
		"id": "c", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}}},
	})
	if _, err := ConvertStreamChunk(role, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state); err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := ConvertStreamChunk(content, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state); err != nil {
		t.Fatalf("content: %v", err)
	}
	if !state.OAIStarted {
		t.Fatal("OAIStarted must be true before finalize")
	}

	results, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected message_delta+message_stop, got %d", len(results))
	}
	var delta struct {
		Type  string `json:"type"`
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(results[0], &delta); err != nil {
		t.Fatalf("delta unmarshal: %v", err)
	}
	if delta.Type != "message_delta" {
		t.Errorf("delta type=%q want message_delta", delta.Type)
	}
	if delta.Delta.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn (default)", delta.Delta.StopReason)
	}
	var stop struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(results[1], &stop); err != nil {
		t.Fatalf("stop unmarshal: %v", err)
	}
	if stop.Type != "message_stop" {
		t.Errorf("stop type=%q want message_stop", stop.Type)
	}
	if state.OAIStarted {
		t.Error("OAIStarted must be false after finalize (exactly-once)")
	}
}

func TestFinalizeStream_OpenAIToAnthropic_UsesObservedFinishReason(t *testing.T) {
	t.Parallel()
	state := &StreamState{}
	state.OAIStarted = true
	state.OAIMessageID = "c"
	state.OAIModel = "gpt-4"
	state.OAIFinishReason = "tool_use"
	state.OAIUsage = streamUsageAccum{PromptTokens: 7, CompletionTokens: 9}

	results, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	var delta struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens json.Number `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(results[0], &delta); err != nil {
		t.Fatalf("delta unmarshal: %v", err)
	}
	if delta.Delta.StopReason != "tool_use" {
		t.Errorf("stop_reason=%q want tool_use", delta.Delta.StopReason)
	}
	if got := delta.Usage.OutputTokens.String(); got != "9" {
		t.Errorf("output_tokens=%q want 9", got)
	}
}

func TestFinalizeStream_OpenAIToAnthropic_AfterExplicitFinishIsNoOp(t *testing.T) {
	t.Parallel()
	state := &StreamState{}
	// A finish_reason chunk already closed the message (OAIStarted=false).
	state.OAIStarted = false
	results, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil after explicit finish, got %d events", len(results))
	}
}

func TestFinalizeStream_OpenAIToAnthropic_Idempotent(t *testing.T) {
	t.Parallel()
	state := &StreamState{}
	state.OAIStarted = true
	state.OAIMessageID = "c"
	state.OAIModel = "gpt-4"
	first, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("first finalize synthesized nothing")
	}
	second, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second != nil {
		t.Errorf("second finalize must be no-op, got %d events", len(second))
	}
}

func TestFinalizeStream_DoneSentinelEquivalent(t *testing.T) {
	t.Parallel()
	// [DONE] via ConvertStreamChunk and FinalizeStream must produce identical
	// output for the same state.
	s1 := &StreamState{OAIStarted: true, OAIMessageID: "c", OAIModel: "gpt-4", OAIFinishReason: "stop"}
	s2 := &StreamState{OAIStarted: true, OAIMessageID: "c", OAIModel: "gpt-4", OAIFinishReason: "stop"}
	done, err := ConvertStreamChunk([]byte("[DONE]"), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, s1)
	if err != nil {
		t.Fatalf("[DONE]: %v", err)
	}
	fin, err := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, s2)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if len(done) != len(fin) {
		t.Fatalf("len mismatch: [DONE]=%d finalize=%d", len(done), len(fin))
	}
	for i := range done {
		if !bytes.Equal(done[i], fin[i]) {
			t.Errorf("event[%d] mismatch: [DONE]=%s finalize=%s", i, done[i], fin[i])
		}
	}
}

func TestFinalizeStream_AnthropicToOpenAI_SynthesizesFinalChunk(t *testing.T) {
	t.Parallel()
	state := &StreamState{}
	// Build state: message_start, content delta, message_delta (stop+usage),
	// then EOF WITHOUT message_stop.
	start := mustMarshal(t, map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-3",
			"usage": map[string]any{"input_tokens": json.Number("3"), "output_tokens": json.Number("0")},
		},
	})
	delta := mustMarshal(t, map[string]any{
		"type": "content_block_delta", "index": json.Number("0"),
		"delta": map[string]any{"type": "text_delta", "text": "hi"},
	})
	msgDelta := mustMarshal(t, map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": json.Number("5")},
	})
	if _, err := ConvertStreamChunk(start, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := ConvertStreamChunk(delta, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state); err != nil {
		t.Fatalf("delta: %v", err)
	}
	if _, err := ConvertStreamChunk(msgDelta, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state); err != nil {
		t.Fatalf("msgDelta: %v", err)
	}
	if !state.AntStarted {
		t.Fatal("AntStarted must be true before finalize")
	}

	results, err := FinalizeStream(adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 final chunk, got %d", len(results))
	}
	var chunk struct {
		Object  string `json:"object"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     json.Number `json:"prompt_tokens"`
			CompletionTokens json.Number `json:"completion_tokens"`
			TotalTokens      json.Number `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(results[0], &chunk); err != nil {
		t.Fatalf("chunk unmarshal: %v", err)
	}
	if chunk.Object != "chat.completion.chunk" {
		t.Errorf("object=%q want chat.completion.chunk", chunk.Object)
	}
	if len(chunk.Choices) != 1 || chunk.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason mismatch: %+v", chunk.Choices)
	}
	if got := chunk.Usage.PromptTokens.String(); got != "3" {
		t.Errorf("prompt_tokens=%q want 3", got)
	}
	if got := chunk.Usage.CompletionTokens.String(); got != "5" {
		t.Errorf("completion_tokens=%q want 5", got)
	}
	if got := chunk.Usage.TotalTokens.String(); got != "8" {
		t.Errorf("total_tokens=%q want 8", got)
	}
	if state.AntStarted {
		t.Error("AntStarted must be false after finalize (exactly-once)")
	}
}

func TestFinalizeStream_AnthropicToOpenAI_AfterExplicitFinishIsNoOp(t *testing.T) {
	t.Parallel()
	state := &StreamState{AntStarted: false}
	results, err := FinalizeStream(adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil after explicit finish, got %d events", len(results))
	}
}

// ── Stream: OpenAI → Anthropic, usage-only terminal chunk ────────────────────

// TestConvertStreamChunk_OpenAIToAnthropic_UsageOnlyChunkCompletesStartedStream
// verifies that a started OpenAI stream receiving a terminal choices:[] +
// usage chunk (OpenAI stream_options.include_usage final chunk) completes
// exactly once: a single message_delta (end_turn) carrying the accumulated
// completion_tokens, followed by a single message_stop, and that a subsequent
// terminal ([DONE] or another usage-only chunk) synthesizes nothing.
func TestConvertStreamChunk_OpenAIToAnthropic_UsageOnlyChunkCompletesStartedStream(t *testing.T) {
	t.Parallel()
	state := &StreamState{}

	// Start the message with a role/content pair so message_start is emitted.
	role := mustMarshal(t, map[string]any{
		"id": "chatcmpl-u1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}}},
	})
	if _, err := ConvertStreamChunk(role, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state); err != nil {
		t.Fatalf("role: %v", err)
	}
	content := mustMarshal(t, map[string]any{
		"id": "chatcmpl-u1", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}}},
	})
	if _, err := ConvertStreamChunk(content, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state); err != nil {
		t.Fatalf("content: %v", err)
	}
	if !state.OAIStarted {
		t.Fatal("stream must be started before the usage-only chunk")
	}

	// Terminal choices:[] + usage chunk (no finish_reason chunk precedes it).
	usageChunk := mustMarshal(t, map[string]any{
		"id": "chatcmpl-u1", "object": "chat.completion.chunk", "created": 3, "model": "gpt-4",
		"choices": []any{},
		"usage":   map[string]any{"prompt_tokens": json.Number("5"), "completion_tokens": json.Number("7"), "total_tokens": json.Number("12")},
	})
	results, err := ConvertStreamChunk(usageChunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("usage chunk: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected exactly 2 events (message_delta+message_stop), got %d", len(results))
	}

	var delta struct {
		Type  string `json:"type"`
		Delta struct {
			Type       string `json:"type"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens json.Number `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(results[0], &delta); err != nil {
		t.Fatalf("delta unmarshal: %v", err)
	}
	if delta.Type != "message_delta" || delta.Delta.Type != "message_delta" {
		t.Errorf("delta type=%q (inner %q), want message_delta", delta.Type, delta.Delta.Type)
	}
	if delta.Delta.StopReason != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn (synthesized default)", delta.Delta.StopReason)
	}
	if got := delta.Usage.OutputTokens.String(); got != "7" {
		t.Errorf("output_tokens=%q want 7 (accumulated completion_tokens)", got)
	}

	var stop struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(results[1], &stop); err != nil {
		t.Fatalf("stop unmarshal: %v", err)
	}
	if stop.Type != "message_stop" {
		t.Errorf("stop type=%q want message_stop", stop.Type)
	}
	if state.OAIStarted {
		t.Error("OAIStarted must be false after the usage-only terminal (exactly-once)")
	}

	// Exactly-once: a follow-up [DONE] and another usage-only chunk must both
	// synthesize nothing because the stream is already closed.
	for name, chunk := range map[string][]byte{
		"done": []byte("[DONE]"),
		"usage2": mustMarshal(t, map[string]any{
			"id": "chatcmpl-u1", "object": "chat.completion.chunk", "created": 4, "model": "gpt-4",
			"choices": []any{},
			"usage":   map[string]any{"prompt_tokens": json.Number("5"), "completion_tokens": json.Number("7")},
		}),
	} {
		dup, err := ConvertStreamChunk(chunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(dup) != 0 {
			t.Errorf("%s: expected no further events (exactly-once), got %d", name, len(dup))
		}
	}
}

// TestConvertStreamChunk_OpenAIToAnthropic_UsageOnlyChunkUsesObservedFinishReason
// verifies that when a finish_reason was observed on an earlier content chunk
// (without closing, i.e. no finish_reason chunk path) the usage-only terminal
// carries that observed stop reason rather than the default end_turn. It also
// confirms completion_tokens are accumulated from the usage chunk.
func TestConvertStreamChunk_OpenAIToAnthropic_UsageOnlyChunkUsesObservedFinishReason(t *testing.T) {
	t.Parallel()
	state := &StreamState{OAIStarted: true, OAIMessageID: "c", OAIModel: "gpt-4", OAIFinishReason: "tool_use"}

	usageChunk := mustMarshal(t, map[string]any{
		"id": "c", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
		"choices": []any{},
		"usage":   map[string]any{"prompt_tokens": json.Number("3"), "completion_tokens": json.Number("9")},
	})
	results, err := ConvertStreamChunk(usageChunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("usage chunk: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 events, got %d", len(results))
	}
	var delta struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			OutputTokens json.Number `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(results[0], &delta); err != nil {
		t.Fatalf("delta unmarshal: %v", err)
	}
	if delta.Delta.StopReason != "tool_use" {
		t.Errorf("stop_reason=%q want tool_use", delta.Delta.StopReason)
	}
	if got := delta.Usage.OutputTokens.String(); got != "9" {
		t.Errorf("output_tokens=%q want 9", got)
	}
	if state.OAIStarted {
		t.Error("OAIStarted must be false (exactly-once)")
	}
}

// TestConvertStreamChunk_OpenAIToAnthropic_UsageOnlyInitialChunkDoesNotEmitMessageStart
// verifies that a usage-only chunk arriving before the stream has started does
// not synthesize a spurious message_start (nor a terminal): the message is
// only started by the first chunk carrying a non-empty choices array.
func TestConvertStreamChunk_OpenAIToAnthropic_UsageOnlyInitialChunkDoesNotEmitMessageStart(t *testing.T) {
	t.Parallel()
	state := &StreamState{}

	usageChunk := mustMarshal(t, map[string]any{
		"id": "chatcmpl-i1", "object": "chat.completion.chunk", "created": 1, "model": "gpt-4",
		"choices": []any{},
		"usage":   map[string]any{"prompt_tokens": json.Number("5"), "completion_tokens": json.Number("2")},
	})
	results, err := ConvertStreamChunk(usageChunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("usage chunk: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("usage-only initial chunk must emit nothing, got %d events", len(results))
	}
	if state.OAIStarted {
		t.Error("OAIStarted must remain false; a usage-only chunk must not start the message")
	}

	// A subsequent content chunk is what starts the message.
	content := mustMarshal(t, map[string]any{
		"id": "chatcmpl-i1", "object": "chat.completion.chunk", "created": 2, "model": "gpt-4",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "hi"}}},
	})
	results, err = ConvertStreamChunk(content, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected message_start + content events, got %d", len(results))
	}
	var start struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(results[0], &start); err != nil {
		t.Fatalf("start unmarshal: %v", err)
	}
	if start.Type != "message_start" {
		t.Errorf("first event type=%q want message_start", start.Type)
	}
	// Usage from the earlier usage-only chunk must have been accumulated and
	// surface on the terminal synthesized from a subsequent usage-only chunk.
	if state.OAIUsage.PromptTokens != 5 || state.OAIUsage.CompletionTokens != 2 {
		t.Errorf("accumulated usage = {prompt=%d completion=%d}, want {5,2}", state.OAIUsage.PromptTokens, state.OAIUsage.CompletionTokens)
	}
}
