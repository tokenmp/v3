package protocolconvert

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── Chat → Responses streaming ─────────────────────────────────────────────

func chatChunk(id, model string, delta map[string]any, finish string) []byte {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	} else {
		choice["finish_reason"] = nil
	}
	b, _ := json.Marshal(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": 1, "model": model,
		"choices": []any{choice},
	})
	return b
}

func TestStream_ChatToResponses_Text(t *testing.T) {
	state := &StreamState{}
	var all []string
	collect := func(results [][]byte) {
		for _, r := range results {
			all = append(all, string(r))
		}
	}

	collect(convertStreamChunkT(t, chatChunk("chatcmpl_1", "gpt-4o", map[string]any{"role": "assistant"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state))
	collect(convertStreamChunkT(t, chatChunk("chatcmpl_1", "gpt-4o", map[string]any{"content": "Hello"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state))
	collect(convertStreamChunkT(t, chatChunk("chatcmpl_1", "gpt-4o", map[string]any{"content": " world"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state))
	collect(convertStreamChunkT(t, chatChunk("chatcmpl_1", "gpt-4o", map[string]any{}, "stop"), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state))

	joined := strings.Join(all, "\n")
	for _, want := range []string{
		`"type":"response.created"`,
		`"type":"response.in_progress"`,
		`"type":"response.output_item.added"`,
		`"type":"response.content_part.added"`,
		`"type":"response.output_text.delta"`,
		`response.output_text.done`,
		`"type":"response.completed"`,
		`"status":"completed"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in stream\n%s", want, joined)
		}
	}
	// Deltas must carry the text.
	if !strings.Contains(joined, `"delta":"Hello"`) || !strings.Contains(joined, `"delta":" world"`) {
		t.Errorf("missing text deltas\n%s", joined)
	}
	// Idempotent terminal: a second finalize must not re-emit.
	if results, _ := FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state); len(results) != 0 {
		t.Errorf("second finalize emitted %d events", len(results))
	}
}

func TestStream_ChatToResponses_DoneSentinel(t *testing.T) {
	state := &StreamState{}
	state.RespStarted = true
	state.RespResponseID = "resp_x"
	state.RespModel = "gpt-4o"
	results, err := ConvertStreamChunk([]byte("[DONE]"), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	if err != nil {
		t.Fatalf("[DONE]: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected terminal events for [DONE]")
	}
	if eventType(results[len(results)-1]) != "response.completed" {
		t.Errorf("last event = %v, want response.completed", eventType(results[len(results)-1]))
	}
}

func TestStream_ChatToResponses_ToolCalls(t *testing.T) {
	state := &StreamState{}
	// start
	convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"role": "assistant"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	// tool call start
	r1 := convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"tool_calls": []any{
		map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "get", "arguments": ""}},
	}}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	r2 := convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"tool_calls": []any{
		map[string]any{"index": 0, "function": map[string]any{"arguments": `{"q":"x"}`}},
	}}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	r3 := convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{}, "tool_calls"), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)

	var joined strings.Builder
	for _, r := range [][]byte{flatten(r1), flatten(r2), flatten(r3)} {
		joined.Write(r)
	}
	for _, want := range []string{
		`"type":"response.output_item.added"`,
		`"function_call"`,
		`"type":"response.function_call_arguments.delta"`,
		`"delta":"{\"q\":\"x\"}"`,
		`"type":"response.completed"`,
	} {
		if !strings.Contains(joined.String(), want) {
			t.Errorf("missing %q\n%s", want, joined.String())
		}
	}
}

func TestStream_ChatToResponses_CustomToolWrap(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"role": "assistant"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	wrapped := responsesCustomToolWrapperName("search.web")
	r := convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"tool_calls": []any{
		map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": wrapped, "arguments": ""}},
	}}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	if !strings.Contains(string(flatten(r)), `"custom_tool_call"`) {
		t.Errorf("expected custom_tool_call item:\n%s", string(flatten(r)))
	}
}

func TestStream_ChatToResponses_Reasoning(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"role": "assistant"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	r1 := convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"reasoning_content": "think"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	r2 := convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"content": "ans"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{}, "stop"), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)

	joined := string(flatten(r1)) + string(flatten(r2))
	if !strings.Contains(joined, `response.reasoning_summary_text.delta`) {
		t.Errorf("missing reasoning delta:\n%s", joined)
	}
	if !strings.Contains(joined, `"delta":"think"`) {
		t.Errorf("missing reasoning text:\n%s", joined)
	}
	if !strings.Contains(joined, `response.output_text.delta`) {
		t.Errorf("missing text delta after reasoning close:\n%s", joined)
	}
}

func TestStream_ChatToResponses_UsageOnlyFinalizes(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"role": "assistant"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	convertStreamChunkT(t, chatChunk("c1", "gpt-4o", map[string]any{"content": "hi"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	// usage-only terminal chunk (choices empty)
	usageChunk := mustMarshal(t, map[string]any{
		"id": "c1", "object": "chat.completion.chunk", "model": "gpt-4o",
		"choices": []any{}, "usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
	})
	r, err := ConvertStreamChunk(usageChunk, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	if err != nil {
		t.Fatalf("usage chunk: %v", err)
	}
	if len(r) == 0 || eventType(r[len(r)-1]) != "response.completed" {
		t.Fatalf("expected response.completed, got %v", r)
	}
}

func TestStream_ChatToResponses_Malformed(t *testing.T) {
	state := &StreamState{}
	if _, err := ConvertStreamChunk([]byte(`not json`), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state); err != ErrInvalidStreamChunk {
		t.Errorf("err = %v, want ErrInvalidStreamChunk", err)
	}
	if _, err := ConvertStreamChunk([]byte(`{"id":"c"}`), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state); err != ErrInvalidStreamChunk {
		t.Errorf("err = %v, want ErrInvalidStreamChunk", err)
	}
}

// ── Responses → Chat streaming ─────────────────────────────────────────────

func TestStream_ResponsesToChat_Text(t *testing.T) {
	state := &StreamState{}
	var all []string
	collect := func(r [][]byte) {
		for _, b := range r {
			all = append(all, string(b))
		}
	}
	collect(convertStreamChunkT(t, respEventBytes("response.created", map[string]any{"response": map[string]any{"id": "resp_1", "model": "gpt-4o", "status": "in_progress"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state))
	collect(convertStreamChunkT(t, respEventBytes("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{}}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state))
	collect(convertStreamChunkT(t, respEventBytes("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "delta": "Hello"}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state))
	collect(convertStreamChunkT(t, respEventBytes("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "delta": "!"}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state))
	collect(convertStreamChunkT(t, respEventBytes("response.completed", map[string]any{"response": map[string]any{"id": "resp_1", "status": "completed", "model": "gpt-4o", "usage": map[string]any{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3}}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state))

	joined := strings.Join(all, "\n")
	// Role announcement chunk.
	if !strings.Contains(joined, `"role":"assistant"`) {
		t.Errorf("missing role announcement:\n%s", joined)
	}
	// Content deltas.
	if !strings.Contains(joined, `"content":"Hello"`) || !strings.Contains(joined, `"content":"!"`) {
		t.Errorf("missing content deltas:\n%s", joined)
	}
	// Final chunk with finish_reason + usage.
	if !strings.Contains(joined, `"finish_reason":"stop"`) {
		t.Errorf("missing finish_reason:\n%s", joined)
	}
	if !strings.Contains(joined, `"completion_tokens":1`) {
		t.Errorf("missing usage:\n%s", joined)
	}
	// Idempotent terminal.
	if r, _ := FinalizeStream(adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state); len(r) != 0 {
		t.Errorf("second finalize emitted %d", len(r))
	}
}

func TestStream_ResponsesToChat_FunctionCall(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, respEventBytes("response.created", map[string]any{"response": map[string]any{"id": "resp_2", "model": "gpt-4o", "status": "in_progress"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	r1 := convertStreamChunkT(t, respEventBytes("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "fc_1", "type": "function_call", "call_id": "fc_1", "name": "get", "arguments": ""}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	r2 := convertStreamChunkT(t, respEventBytes("response.function_call_arguments.delta", map[string]any{"item_id": "fc_1", "output_index": 0, "delta": `{"q":"x"}`}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	r3 := convertStreamChunkT(t, respEventBytes("response.completed", map[string]any{"response": map[string]any{"id": "resp_2", "status": "completed", "model": "gpt-4o", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)

	joined := string(flatten(append(append(r1, r2...), r3...)))
	if !strings.Contains(joined, `"tool_calls"`) || !strings.Contains(joined, `"name":"get"`) {
		t.Errorf("missing tool_calls start:\n%s", joined)
	}
	if !strings.Contains(joined, `"arguments":"{\"q\":\"x\"}"`) {
		t.Errorf("missing arguments delta:\n%s", joined)
	}
	if !strings.Contains(joined, `"finish_reason":"tool_calls"`) {
		t.Errorf("missing tool_calls finish:\n%s", joined)
	}
}

func TestStream_ResponsesToChat_CustomToolWrap(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, respEventBytes("response.created", map[string]any{"response": map[string]any{"id": "r", "model": "gpt-4o", "status": "in_progress"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	r := convertStreamChunkT(t, respEventBytes("response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"id": "ct_1", "type": "custom_tool_call", "call_id": "ct_1", "name": "search.web"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	joined := string(flatten(r))
	want := responsesCustomToolWrapperName("search.web")
	if !strings.Contains(joined, fmt.Sprintf(`"name":%q`, want)) {
		t.Errorf("expected wrapped name %q:\n%s", want, joined)
	}
}

func TestStream_ResponsesToChat_Malformed(t *testing.T) {
	state := &StreamState{}
	if _, err := ConvertStreamChunk([]byte(`not json`), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state); err != ErrInvalidStreamChunk {
		t.Errorf("err = %v", err)
	}
	if _, err := ConvertStreamChunk([]byte(`{"foo":1}`), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state); err != ErrInvalidStreamChunk {
		t.Errorf("err = %v, want ErrInvalidStreamChunk (no type)", err)
	}
	// A Responses stream has an ordered lifecycle: output events are invalid
	// until a complete response.created event has been received.
	if _, err := ConvertStreamChunk([]byte(`{"type":"response.output_text.delta","delta":"x"}`), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, &StreamState{}); err != ErrInvalidStreamChunk {
		t.Errorf("output before created err = %v, want ErrInvalidStreamChunk", err)
	}
	if _, err := ConvertStreamChunk([]byte(`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, &StreamState{}); err != ErrInvalidStreamChunk {
		t.Errorf("incomplete created err = %v, want ErrInvalidStreamChunk", err)
	}
}

func TestStream_ResponsesToChat_FinalizeCleanEOF(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, respEventBytes("response.created", map[string]any{"response": map[string]any{"id": "r", "model": "gpt-4o", "status": "in_progress"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	convertStreamChunkT(t, respEventBytes("response.output_text.delta", map[string]any{"delta": "hi"}), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	r, err := FinalizeStream(adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if len(r) == 0 {
		t.Fatal("expected finalize chunk")
	}
	if !strings.Contains(string(r[0]), `"finish_reason":"stop"`) {
		t.Errorf("finalize chunk = %s", string(r[0]))
	}
}

// ── Composite streaming: Anthropic ↔ Responses ────────────────────────────

func TestStream_AnthropicToResponses_Text(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, mustMarshal(t, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": "claude", "usage": map[string]any{"input_tokens": 5, "output_tokens": 0}}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses, state)
	r2 := convertStreamChunkT(t, mustMarshal(t, map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses, state)
	r3 := convertStreamChunkT(t, mustMarshal(t, map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "Hi"}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses, state)
	convertStreamChunkT(t, mustMarshal(t, map[string]any{"type": "content_block_stop", "index": 0}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses, state)
	convertStreamChunkT(t, mustMarshal(t, map[string]any{"type": "message_delta", "delta": map[string]any{"type": "message_delta", "stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 2}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses, state)
	r6 := convertStreamChunkT(t, mustMarshal(t, map[string]any{"type": "message_stop"}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses, state)

	joined := string(flatten(append(append(r2, r3...), r6...)))
	for _, want := range []string{`response.created`, `response.output_text.delta`, `"delta":"Hi"`, `response.completed`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q:\n%s", want, joined)
		}
	}
}

func TestStream_ResponsesToAnthropic_Text(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, respEventBytes("response.created", map[string]any{"response": map[string]any{"id": "resp_1", "model": "gpt-4o", "status": "in_progress"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic, state)
	r2 := convertStreamChunkT(t, respEventBytes("response.output_text.delta", map[string]any{"delta": "Hi"}), adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic, state)
	r3 := convertStreamChunkT(t, respEventBytes("response.completed", map[string]any{"response": map[string]any{"id": "resp_1", "status": "completed", "model": "gpt-4o", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic, state)

	joined := string(flatten(append(r2, r3...)))
	for _, want := range []string{`message_start`, `content_block_delta`, `"text":"Hi"`, `message_stop`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q:\n%s", want, joined)
		}
	}
}

func TestStream_ResponsesToAnthropic_Finalize(t *testing.T) {
	state := &StreamState{}
	convertStreamChunkT(t, respEventBytes("response.created", map[string]any{"response": map[string]any{"id": "r", "model": "gpt-4o", "status": "in_progress"}}), adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic, state)
	convertStreamChunkT(t, respEventBytes("response.output_text.delta", map[string]any{"delta": "x"}), adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic, state)
	r, err := FinalizeStream(adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic, state)
	if err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if !strings.Contains(string(flatten(r)), `message_stop`) {
		t.Errorf("expected message_stop in finalize:\n%s", string(flatten(r)))
	}
}

// ── Race: concurrent streams must be independent ───────────────────────────

func TestStream_Responses_RaceIndependent(t *testing.T) {
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			state := &StreamState{}
			id := fmt.Sprintf("c%d", i)
			convertStreamChunkT(t, chatChunk(id, "gpt-4o", map[string]any{"role": "assistant"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
			convertStreamChunkT(t, chatChunk(id, "gpt-4o", map[string]any{"content": "hi"}, ""), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
			r := convertStreamChunkT(t, chatChunk(id, "gpt-4o", map[string]any{}, "stop"), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
			if len(r) == 0 {
				t.Errorf("stream %d: no terminal", i)
			}
		}(i)
	}
	wg.Wait()
}

// ── Fuzz: streaming must not panic ─────────────────────────────────────────

func FuzzStream_ChatToResponses(f *testing.F) {
	f.Add([]byte(`{"id":"c","object":"chat.completion.chunk","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`))
	f.Add([]byte(`{"id":"c","object":"chat.completion.chunk","model":"m","choices":[]}`))
	f.Add([]byte(`[DONE]`))
	f.Add([]byte(`garbage`))
	f.Add([]byte(`{"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"x","type":"function","function":{"name":"n","arguments":"{}"}}]}}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		state := &StreamState{}
		_, _ = ConvertStreamChunk(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
		_, _ = FinalizeStream(adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses, state)
	})
}

func FuzzStream_ResponsesToChat(f *testing.F) {
	f.Add([]byte(`{"type":"response.created","response":{"id":"r","model":"m","status":"in_progress"}}`))
	f.Add([]byte(`{"type":"response.output_text.delta","delta":"hi"}`))
	f.Add([]byte(`{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`))
	f.Add([]byte(`{"type":"response.output_item.added","item":{"id":"fc","type":"function_call","call_id":"fc","name":"n"}}`))
	f.Add([]byte(`garbage`))
	f.Add([]byte(`{"type":"unknown.event","x":1}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		state := &StreamState{}
		_, _ = ConvertStreamChunk(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
		_, _ = FinalizeStream(adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat, state)
	})
}

// ── Test helpers (avoid *testing.T in mustMarshal signature clash) ──────────

// ConvertStreamChunk wraps the package function so tests can pass *testing.T
// for helper attribution without changing the global signature.
func convertStreamChunkT(t *testing.T, raw []byte, from, to adapter.Protocol, state *StreamState) [][]byte {
	t.Helper()
	r, err := ConvertStreamChunk(raw, from, to, state)
	if err != nil {
		t.Fatalf("ConvertStreamChunk %s→%s: %v on %s", from, to, err, string(raw))
	}
	return r
}

// respEventBytes builds a Responses SSE event payload (no sequence_number,
// which the converter does not require on input).
func respEventBytes(eventType string, extra map[string]any) []byte {
	ev := map[string]any{"type": eventType}
	for k, v := range extra {
		ev[k] = v
	}
	b, _ := json.Marshal(ev)
	return b
}

// flatten joins a slice of event payloads with newlines for substring checks.
func flatten(events [][]byte) []byte {
	var b []byte
	for i, e := range events {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, e...)
	}
	return b
}

// mustMarshalT replaces the package-level mustMarshal signature to allow a nil
// first arg idiom; alias to the existing mustMarshal(*testing.T, any).
func mustMarshalT(t *testing.T, v any) []byte { return mustMarshal(t, v) }

var _ = mustMarshalT
