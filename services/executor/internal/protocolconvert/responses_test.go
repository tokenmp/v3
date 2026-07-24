package protocolconvert

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── Helpers for Responses tests ────────────────────────────────────────────

func respVal(t *testing.T, b []byte) map[string]any {
	t.Helper()
	return mustUnmarshal(t, b)
}

func eventType(b []byte) string {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return stringVal(m["type"])
}

// ── Request: Chat → Responses ──────────────────────────────────────────────

func TestConvertRequest_ChatToResponses_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":    "gpt-4o",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	if m["model"] != "gpt-4o" {
		t.Errorf("model = %v", m["model"])
	}
	input := m["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input len = %d", len(input))
	}
	item := input[0].(map[string]any)
	if item["role"] != "user" || item["content"] != "Hi" {
		t.Errorf("input item = %#v", item)
	}
}

func TestConvertRequest_ChatToResponses_SystemAndTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4o",
		"messages": []any{
			map[string]any{"role": "system", "content": "be nice"},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "get", "arguments": `{"q":"x"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "result"},
		},
		"tools":            []any{map[string]any{"type": "function", "function": map[string]any{"name": "get", "parameters": map[string]any{"type": "object"}}}},
		"max_tokens":       100,
		"temperature":      0.5,
		"reasoning_effort": "medium",
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	if !numEqual(m["max_output_tokens"], 100) {
		t.Errorf("max_output_tokens = %v", m["max_output_tokens"])
	}
	if m["temperature"] != 0.5 {
		t.Errorf("temperature = %v", m["temperature"])
	}
	if r, ok := m["reasoning"].(map[string]any); !ok || r["effort"] != "medium" {
		t.Errorf("reasoning = %#v", m["reasoning"])
	}
	input := m["input"].([]any)
	// system, user, assistant(empty content skipped via tool_calls path? content "" and has tool_calls → assistant item omitted, function_call added), function_call_output
	if len(input) < 3 {
		t.Fatalf("input len = %d", len(input))
	}
	// First item is system.
	if input[0].(map[string]any)["role"] != "system" {
		t.Errorf("first item role = %v", input[0])
	}
	// Find the function_call and function_call_output.
	var hasFC, hasFCO bool
	for _, it := range input {
		item := it.(map[string]any)
		switch item["type"] {
		case "function_call":
			hasFC = true
			if item["call_id"] != "call_1" || item["name"] != "get" {
				t.Errorf("function_call = %#v", item)
			}
		case "function_call_output":
			hasFCO = true
			if item["call_id"] != "call_1" || item["output"] != "result" {
				t.Errorf("function_call_output = %#v", item)
			}
		}
	}
	if !hasFC || !hasFCO {
		t.Fatalf("missing function_call=%v function_call_output=%v", hasFC, hasFCO)
	}
	tools := m["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "get" {
		t.Errorf("tools = %#v", tools)
	}
}

func TestConvertRequest_ChatToResponses_ToolChoice(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":       "gpt-4o",
		"messages":    []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":       []any{map[string]any{"type": "function", "function": map[string]any{"name": "get"}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "get"}},
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	tc := respVal(t, out)["tool_choice"]
	if m, ok := tc.(map[string]any); !ok || m["type"] != "function" || m["name"] != "get" {
		t.Errorf("tool_choice = %#v", tc)
	}
}

// ── Request: Responses → Chat ──────────────────────────────────────────────

func TestConvertRequest_ResponsesToChat_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4o",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "Hi"}},
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d", len(msgs))
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "Hi" {
		t.Errorf("message = %#v", msg)
	}
}

func TestConvertRequest_ResponsesToChat_StringInput(t *testing.T) {
	body := mustMarshal(t, map[string]any{"model": "gpt-4o", "input": "hello"})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	msg := respVal(t, out)["messages"].([]any)[0].(map[string]any)
	if msg["role"] != "user" || msg["content"] != "hello" {
		t.Errorf("message = %#v", msg)
	}
}

func TestConvertRequest_ResponsesToChat_InstructionsAndTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":        "gpt-4o",
		"instructions": "be nice",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "hi"},
		}}},
		"tools":             []any{map[string]any{"type": "function", "name": "get", "parameters": map[string]any{"type": "object"}}},
		"max_output_tokens": 50,
		"reasoning":         map[string]any{"effort": "high"},
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	if !numEqual(m["max_tokens"], 50) {
		t.Errorf("max_tokens = %v", m["max_tokens"])
	}
	if m["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v", m["reasoning_effort"])
	}
	msgs := m["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("first message should be system: %#v", msgs[0])
	}
	tools := m["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get" {
		t.Errorf("tool name = %v", fn["name"])
	}
}

func TestConvertRequest_ResponsesToChat_ToolPairing(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4o",
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "call it"},
			map[string]any{"type": "function_call", "id": "fc_1", "call_id": "fc_1", "name": "get", "arguments": `{"q":"x"}`},
			map[string]any{"type": "function_call_output", "call_id": "fc_1", "output": "result"},
		},
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	msgs := respVal(t, out)["messages"].([]any)
	// user, assistant(tool_calls), tool
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d", len(msgs))
	}
	assistant := msgs[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Errorf("assistant role = %v", assistant["role"])
	}
	tcs := assistant["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls len = %d", len(tcs))
	}
	fn := tcs[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get" || fn["arguments"] != `{"q":"x"}` {
		t.Errorf("tool call = %#v", fn)
	}
	tool := msgs[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "fc_1" || tool["content"] != "result" {
		t.Errorf("tool message = %#v", tool)
	}
}

// ── Custom tool wrapper ────────────────────────────────────────────────────

func TestConvertRequest_ResponsesToChat_CustomToolWrapper(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":       "gpt-4o",
		"input":       []any{map[string]any{"type": "message", "role": "user", "content": "go"}},
		"tools":       []any{map[string]any{"type": "custom", "name": "search.web", "description": "search"}},
		"tool_choice": map[string]any{"type": "custom", "name": "search.web"},
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	fn := m["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	wrapped := responsesCustomToolWrapperName("search.web")
	if fn["name"] != wrapped {
		t.Errorf("wrapped tool name = %v, want %v", fn["name"], wrapped)
	}
	// The wrapper sanitizes non-alphanumeric chars to '_', so the name is
	// lossy: "search.web" → "search_web". (The tool input itself is
	// preserved verbatim via the input string.)
	orig, ok := responsesCustomToolOriginalName(fn["name"].(string))
	if !ok || orig != "search_web" {
		t.Errorf("unwrap orig = %q ok=%v, want search_web", orig, ok)
	}
	tc := m["tool_choice"].(map[string]any)["function"].(map[string]any)
	if tc["name"] != wrapped {
		t.Errorf("tool_choice name = %v, want %v", tc["name"], wrapped)
	}
}

func TestCustomToolWrapper_RoundTrip(t *testing.T) {
	cases := []string{"search.web", "lookup", "a b c", "tool-name", "123", ""}
	for _, c := range cases {
		if c == "" {
			if w := responsesCustomToolWrapperName(c); w != "" {
				t.Errorf("empty name should yield empty wrapper, got %q", w)
			}
			continue
		}
		w := responsesCustomToolWrapperName(c)
		if !strings.HasPrefix(w, responsesCustomToolNamePrefix) {
			t.Errorf("wrapper %q missing prefix", w)
		}
		orig, ok := responsesCustomToolOriginalName(w)
		if !ok {
			t.Errorf("unwrap %q failed", w)
		}
		// The original is the sanitized form, not necessarily identical.
		if c == "search.web" && orig != "search_web" {
			t.Errorf("orig = %q, want search_web", orig)
		}
		if c == "lookup" && orig != "lookup" {
			t.Errorf("orig = %q, want lookup", orig)
		}
	}
}

// ── Request composition: Responses ↔ Anthropic ────────────────────────────

func TestConvertRequest_ResponsesToAnthropic_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":             "gpt-4o",
		"input":             []any{map[string]any{"type": "message", "role": "user", "content": "Hi"}},
		"max_output_tokens": 64,
	})
	out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	if m["model"] != "gpt-4o" {
		t.Errorf("model = %v", m["model"])
	}
	if !numEqual(m["max_tokens"], 64) {
		t.Errorf("max_tokens = %v", m["max_tokens"])
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "user" {
		t.Errorf("role = %v", msgs[0])
	}
}

func TestConvertRequest_AnthropicToResponses_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude",
		"max_tokens": 64,
		"messages":   []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	out, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := respVal(t, out)
	input := m["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["content"] != "Hi" {
		t.Errorf("input = %#v", input)
	}
}

// ── Response: Chat → Responses ─────────────────────────────────────────────

func TestConvertResponse_ChatToResponses_Text(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "chatcmpl_1", "object": "chat.completion", "model": "gpt-4o",
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "Hello world"},
		}},
		"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := respVal(t, out)
	if m["object"] != "response" {
		t.Errorf("object = %v", m["object"])
	}
	if m["status"] != "completed" {
		t.Errorf("status = %v", m["status"])
	}
	if m["output_text"] != "Hello world" {
		t.Errorf("output_text = %v", m["output_text"])
	}
	usage := m["usage"].(map[string]any)
	if !numEqual(usage["input_tokens"], 10) || !numEqual(usage["output_tokens"], 5) || !numEqual(usage["total_tokens"], 15) {
		t.Errorf("usage = %#v", usage)
	}
}

func TestConvertResponse_ChatToResponses_ToolCalls(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "chatcmpl_2", "object": "chat.completion", "model": "gpt-4o",
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
				map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "get", "arguments": `{"q":"x"}`}},
			}},
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	output := respVal(t, out)["output"].([]any)
	var fc map[string]any
	for _, it := range output {
		if it.(map[string]any)["type"] == "function_call" {
			fc = it.(map[string]any)
		}
	}
	if fc == nil {
		t.Fatal("missing function_call output item")
	}
	if fc["call_id"] != "call_1" || fc["name"] != "get" || fc["arguments"] != `{"q":"x"}` {
		t.Errorf("function_call = %#v", fc)
	}
}

func TestConvertResponse_ChatToResponses_CustomToolUnwrap(t *testing.T) {
	wrapped := responsesCustomToolWrapperName("search.web")
	body := mustMarshal(t, map[string]any{
		"id": "c", "object": "chat.completion", "model": "gpt-4o",
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{
				map[string]any{"id": "call_9", "type": "function", "function": map[string]any{"name": wrapped, "arguments": `{"input":"q"}`}},
			}},
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	output := respVal(t, out)["output"].([]any)
	for _, it := range output {
		item := it.(map[string]any)
		if item["type"] == "custom_tool_call" {
			// The name is the sanitized form ("search_web"), not the dotted
			// original, because the wrapper is lossy for non-alphanumeric
			// names. The input string is preserved verbatim.
			if item["name"] != "search_web" || item["input"] != "q" {
				t.Errorf("custom_tool_call = %#v", item)
			}
			return
		}
	}
	t.Fatal("missing custom_tool_call output item")
}

func TestConvertResponse_ChatToResponses_Reasoning(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "c", "object": "chat.completion", "model": "gpt-4o",
		"choices": []any{map[string]any{
			"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "answer", "reasoning_content": "thinking"},
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	output := respVal(t, out)["output"].([]any)
	if output[0].(map[string]any)["type"] != "reasoning" {
		t.Errorf("first output should be reasoning: %#v", output[0])
	}
	enc := output[0].(map[string]any)["encrypted_content"].(string)
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(enc, responsesReasoningCarrierPrefix))
	if err != nil || string(decoded) != "thinking" {
		t.Errorf("reasoning carrier decode = %q err=%v", string(decoded), err)
	}
}

// ── Response: Responses → Chat ─────────────────────────────────────────────

func TestConvertResponse_ResponsesToChat_Text(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "resp_1", "object": "response", "status": "completed", "model": "gpt-4o",
		"output": []any{map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "Hello", "annotations": []any{}}},
		}},
		"output_text": "Hello",
		"usage":       map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := respVal(t, out)
	if m["object"] != "chat.completion" {
		t.Errorf("object = %v", m["object"])
	}
	choice := m["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Hello" {
		t.Errorf("content = %v", msg["content"])
	}
}

func TestConvertResponse_ResponsesToChat_FunctionCall(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "resp_2", "object": "response", "status": "completed", "model": "gpt-4o",
		"output": []any{map[string]any{
			"id": "fc_1", "type": "function_call", "status": "completed",
			"call_id": "fc_1", "name": "get", "arguments": `{"q":"x"}`,
		}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	choice := respVal(t, out)["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v", choice["finish_reason"])
	}
	tc := choice["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if tc["id"] != "fc_1" || tc["function"].(map[string]any)["name"] != "get" {
		t.Errorf("tool call = %#v", tc)
	}
}

func TestConvertResponse_ResponsesToChat_CustomToolWrap(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "r", "object": "response", "status": "completed", "model": "gpt-4o",
		"output": []any{map[string]any{
			"id": "ct_1", "type": "custom_tool_call", "status": "completed",
			"call_id": "ct_1", "name": "search.web", "input": "q",
		}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	tc := respVal(t, out)["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	want := responsesCustomToolWrapperName("search.web")
	if tc["function"].(map[string]any)["name"] != want {
		t.Errorf("name = %v, want %v", tc["function"], want)
	}
}

// ── Response composition: Responses ↔ Anthropic ───────────────────────────

func TestConvertResponse_ResponsesToAnthropic_Text(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "resp_a", "object": "response", "status": "completed", "model": "claude",
		"output": []any{map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": "Hi"}},
		}},
		"usage": map[string]any{"input_tokens": 4, "output_tokens": 2, "total_tokens": 6},
	})
	out, err := ConvertResponse(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := respVal(t, out)
	if m["type"] != "message" {
		t.Errorf("type = %v", m["type"])
	}
	if m["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v", m["stop_reason"])
	}
	blocks := m["content"].([]any)
	if blocks[0].(map[string]any)["text"] != "Hi" {
		t.Errorf("content = %#v", blocks)
	}
}

func TestConvertResponse_AnthropicToResponses_Text(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "msg_a", "type": "message", "role": "assistant", "model": "claude",
		"content":     []any{map[string]any{"type": "text", "text": "Hi"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 4, "output_tokens": 2},
	})
	out, err := ConvertResponse(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := respVal(t, out)
	if m["object"] != "response" {
		t.Errorf("object = %v", m["object"])
	}
	if m["output_text"] != "Hi" {
		t.Errorf("output_text = %v", m["output_text"])
	}
}

// ── Round-trip: Responses → Chat → Responses (text) ────────────────────────

func TestRoundTrip_ResponsesChatResponses_Text(t *testing.T) {
	orig := mustMarshal(t, map[string]any{
		"model": "gpt-4o",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "ping"}},
	})
	chat, err := ConvertRequest(orig, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("to chat: %v", err)
	}
	back, err := ConvertRequest(chat, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatalf("to responses: %v", err)
	}
	m := respVal(t, back)
	input := m["input"].([]any)
	if len(input) != 1 || input[0].(map[string]any)["content"] != "ping" {
		t.Errorf("round-trip input = %#v", input)
	}
}

// ── Malformed rejection ─────────────────────────────────────────────────────

func TestConvertRequest_ResponsesMalformed(t *testing.T) {
	cases := map[string][]byte{
		"empty":          []byte(`{}`),
		"no input":       []byte(`{"model":"m"}`),
		"bad model":      []byte(`{"model":123,"input":"x"}`),
		"stateful field": []byte(`{"model":"m","input":"x","previous_response_id":"r"}`),
		"bad reasoning":  []byte(`{"model":"m","input":"x","reasoning":{"effort":"bogus"}}`),
		"bad tool type":  []byte(`{"model":"m","input":"x","tools":[{"type":"mcp"}]}`),
		"trailing":       []byte(`{"model":"m","input":"x"} garbage`),
		"dup key":        []byte(`{"model":"m","model":"n","input":"x"}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestConvertRequest_ResponsesBodyTooLarge(t *testing.T) {
	big := []byte(`{"model":"m","input":"`)
	big = append(big, make([]byte, maxBodyBytes)...)
	_, err := ConvertRequest(big, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertResponse_ResponsesMalformed(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`not json`),
		[]byte(`{}`),
		[]byte(`{"id":"r","object":"response","status":"completed","model":"m","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":1}}`),
	} {
		_, err := ConvertResponse(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
		if !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("body=%s err = %v, want ErrInvalidResponse", body, err)
		}
	}
}

func TestConvertResponse_ResponsesToAnthropic_TooLarge(t *testing.T) {
	big := []byte(`{"id":"r","object":"response","status":"completed","model":"m","output":`)
	big = append(big, make([]byte, maxBodyBytes)...)
	_, err := ConvertResponse(big, adapter.ProtocolOpenAIResponses, adapter.ProtocolAnthropic)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("err = %v, want ErrInvalidResponse", err)
	}
}

// ── Unsupported (Images) still rejected ────────────────────────────────────

func TestConvert_ResponsesImagesUnsupported(t *testing.T) {
	if supportedConversion(adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIImages) {
		t.Fatal("responses→images should be unsupported")
	}
	_, err := ConvertRequest([]byte(`{}`), adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIImages)
	if err != ErrUnsupportedConversion {
		t.Errorf("err = %v, want ErrUnsupportedConversion", err)
	}
}

// ── RestoreToolNames: Responses shapes ─────────────────────────────────────

func TestRestoreToolNamesResponse_Responses(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id": "r", "object": "response", "status": "completed", "model": "m",
		"output": []any{map[string]any{
			"id": "fc", "type": "function_call", "name": "sanitized_1", "call_id": "fc", "arguments": "{}",
		}},
		"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
	})
	nameMap := map[string]string{"sanitized_1": "original.name"}
	out, err := RestoreToolNamesResponse(body, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	item := respVal(t, out)["output"].([]any)[0].(map[string]any)
	if item["name"] != "original.name" {
		t.Errorf("name = %v", item["name"])
	}
}

func TestRestoreToolNamesStreamChunk_Responses(t *testing.T) {
	chunk := mustMarshal(t, map[string]any{
		"type": "response.output_item.added", "output_index": 0,
		"item": map[string]any{"id": "fc", "type": "function_call", "name": "sanitized_1", "call_id": "fc"},
	})
	nameMap := map[string]string{"sanitized_1": "original.name"}
	out, err := RestoreToolNamesStreamChunk(chunk, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", err)
	}
	item := respVal(t, out)["item"].(map[string]any)
	if item["name"] != "original.name" {
		t.Errorf("name = %v", item["name"])
	}
}

// ── Fuzz: Responses → Chat request must not panic ──────────────────────────

func FuzzConvertRequest_ResponsesToChat(f *testing.F) {
	f.Add([]byte(`{"model":"m","input":"hi"}`))
	f.Add([]byte(`{"model":"m","input":[{"type":"message","role":"user","content":"x"}]}`))
	f.Add([]byte(`{"model":"m","input":[{"type":"function_call","id":"c","call_id":"c","name":"n","arguments":"{}"},{"type":"function_call_output","call_id":"c","output":"r"}]}`))
	f.Add([]byte(`{"model":"m","input":"x","tools":[{"type":"custom","name":"a.b"}]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"input":[]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		out, err := ConvertRequest(body, adapter.ProtocolOpenAIResponses, adapter.ProtocolOpenAIChat)
		if err != nil {
			if out != nil {
				t.Errorf("non-nil out on error")
			}
			return
		}
		if _, err := ConvertRequest(out, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses); err != nil {
			// round-trip may legitimately fail on exotic shapes; just ensure no panic.
		}
	})
}

// ── Fuzz: Chat → Responses response must not panic ─────────────────────────

func FuzzConvertResponse_ChatToResponses(f *testing.F) {
	f.Add([]byte(`{"id":"c","object":"chat.completion","model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	f.Add([]byte(`{"choices":[]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`garbage`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
		_, _ = ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	})
}

var _ = fmt.Sprintf
