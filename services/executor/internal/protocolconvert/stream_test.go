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
