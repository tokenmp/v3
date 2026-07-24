package protocolconvert

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── Helpers ─────────────────────────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func mustUnmarshal(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %q: %v", string(b[:min(len(b), 100)]), err)
	}
	return m
}

func jsonNum(v int) json.Number {
	return json.Number(fmt.Sprintf("%d", v))
}

// numEqual compares a value (from json.Unmarshal into map[string]any, where
// numbers become float64) against an expected integer. JSON numbers that were
// produced as json.Number in the converted output unmarshal back to float64.
func numEqual(got any, want int) bool {
	switch v := got.(type) {
	case float64:
		return v == float64(want)
	case int:
		return v == want
	case int64:
		return int(v) == want
	case json.Number:
		return v.String() == fmt.Sprintf("%d", want)
	}
	return false
}

// ── Request: OpenAI → Anthropic ─────────────────────────────────────────────

func TestConvertRequest_OpenAIToAnthropic_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", m["model"])
	}
	if !numEqual(m["max_tokens"], 4096) {
		t.Errorf("max_tokens = %v, want 4096", m["max_tokens"])
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
	if msg["content"] != "Hello" {
		t.Errorf("content = %v, want Hello", msg["content"])
	}
}

func TestConvertRequest_OpenAIToAnthropic_ImageDataURI(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "What is shown?"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGVsbG8="}},
		}}},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	blocks := mustUnmarshal(t, result)["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if got := blocks[0].(map[string]any)["text"]; got != "What is shown?" {
		t.Errorf("text = %v, want question", got)
	}
	source := blocks[1].(map[string]any)["source"].(map[string]any)
	if blocks[1].(map[string]any)["type"] != "image" || source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "aGVsbG8=" {
		t.Errorf("image block = %#v, want base64 PNG", blocks[1])
	}
}

func TestConvertRequest_OpenAIToAnthropic_ImageURL(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/cat.png"}},
		}}},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	source := mustUnmarshal(t, result)["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["source"].(map[string]any)
	if source["type"] != "url" || source["url"] != "https://example.test/cat.png" {
		t.Errorf("source = %#v, want URL source", source)
	}
}

func TestConvertRequest_OpenAIToAnthropic_MalformedImageDataURI(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,not base64"}},
		}}},
	})
	if _, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("error = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_AnthropicToOpenAI_ImageSources(t *testing.T) {
	tests := []struct {
		name, sourceType, wantURL string
		source                    map[string]any
	}{
		{name: "base64", sourceType: "base64", wantURL: "data:image/png;base64,aGVsbG8=", source: map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="}},
		{name: "url", sourceType: "url", wantURL: "https://example.test/cat.png", source: map[string]any{"type": "url", "url": "https://example.test/cat.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := mustMarshal(t, map[string]any{
				"model": "claude", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "text", "text": "Describe this"},
					map[string]any{"type": "image", "source": tt.source},
				}}},
			})
			result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
			if err != nil {
				t.Fatalf("ConvertRequest: %v", err)
			}
			parts := mustUnmarshal(t, result)["messages"].([]any)[0].(map[string]any)["content"].([]any)
			if parts[0].(map[string]any)["text"] != "Describe this" {
				t.Errorf("text part = %#v", parts[0])
			}
			url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"]
			if parts[1].(map[string]any)["type"] != "image_url" || url != tt.wantURL {
				t.Errorf("image part = %#v, want URL %q", parts[1], tt.wantURL)
			}
		})
	}
}

func TestConvertRequest_OpenAIToAnthropic_WithSystem(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are helpful"},
			map[string]any{"role": "user", "content": "Hi"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if _, ok := m["system"]; !ok {
		t.Error("missing system field")
	}
	msgs := m["messages"].([]any)
	for _, msg := range msgs {
		if msg.(map[string]any)["role"] == "system" {
			t.Error("system message should not be in messages array")
		}
	}
}

func TestConvertRequest_OpenAIToAnthropic_WithTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "What's the weather?"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get weather",
					"parameters":  map[string]any{"type": "object"},
				},
			},
		},
		"tool_choice": "auto",
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	tools := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "get_weather" {
		t.Errorf("tool name = %v, want get_weather", tool["name"])
	}
	if _, ok := tool["input_schema"]; !ok {
		t.Error("missing input_schema")
	}
	tc := m["tool_choice"].(map[string]any)
	if tc["type"] != "auto" {
		t.Errorf("tool_choice type = %v, want auto", tc["type"])
	}
}

func TestConvertRequest_OpenAIToAnthropic_ToolCallHistory(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "What's the weather?"},
			map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{
					map[string]any{
						"id":   "call_123",
						"type": "function",
						"function": map[string]any{
							"name":      "get_weather",
							"arguments": `{"city":"SF"}`,
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"content":      "Sunny, 72°F",
				"tool_call_id": "call_123",
			},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	msgs := m["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	assistantMsg := msgs[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Errorf("msg[1] role = %v, want assistant", assistantMsg["role"])
	}
	blocks := assistantMsg["content"].([]any)
	foundToolUse := false
	for _, b := range blocks {
		if block, ok := b.(map[string]any); ok && block["type"] == "tool_use" {
			foundToolUse = true
			if block["id"] != "call_123" {
				t.Errorf("tool_use id = %v, want call_123", block["id"])
			}
		}
	}
	if !foundToolUse {
		t.Error("missing tool_use block in assistant message")
	}
	toolResultMsg := msgs[2].(map[string]any)
	if toolResultMsg["role"] != "user" {
		t.Errorf("msg[2] role = %v, want user", toolResultMsg["role"])
	}
	resultBlocks := toolResultMsg["content"].([]any)
	if len(resultBlocks) == 0 {
		t.Fatal("tool result message has no content blocks")
	}
	if resultBlocks[0].(map[string]any)["type"] != "tool_result" {
		t.Error("first block should be tool_result")
	}
}

func TestConvertRequest_OpenAIToAnthropic_MultiTurn(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "system", "content": "Be concise"},
			map[string]any{"role": "user", "content": "Hello"},
			map[string]any{"role": "assistant", "content": "Hi there!"},
			map[string]any{"role": "user", "content": "How are you?"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	msgs := m["messages"].([]any)
	if len(msgs) != 3 {
		t.Errorf("messages len = %d, want 3", len(msgs))
	}
}

func TestConvertRequest_OpenAIToAnthropic_Stream(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"stream": true,
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["stream"] != true {
		t.Error("stream should be preserved")
	}
}

func TestConvertRequest_OpenAIToAnthropic_StopSequences(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"stop": []any{"END", "STOP"},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	ss := m["stop_sequences"].([]any)
	if len(ss) != 2 {
		t.Errorf("stop_sequences len = %d, want 2", len(ss))
	}
}

func TestConvertRequest_OpenAIToAnthropic_StopString(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"stop": "END",
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	ss := m["stop_sequences"].([]any)
	if len(ss) != 1 || ss[0] != "END" {
		t.Errorf("stop_sequences = %v, want [END]", ss)
	}
}

func TestConvertRequest_OpenAIToAnthropic_MaxTokens(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"max_tokens": 100,
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if !numEqual(m["max_tokens"], 100) {
		t.Errorf("max_tokens = %v, want 100", m["max_tokens"])
	}
}

func TestConvertRequest_OpenAIToAnthropic_MaxCompletionTokens(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"max_completion_tokens": 200,
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if !numEqual(m["max_tokens"], 200) {
		t.Errorf("max_tokens = %v, want 200 (from max_completion_tokens)", m["max_tokens"])
	}
}

func TestConvertRequest_OpenAIToAnthropic_UserMetadata(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"user": "user-123",
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	meta := m["metadata"].(map[string]any)
	if meta["user_id"] != "user-123" {
		t.Errorf("metadata.user_id = %v, want user-123", meta["user_id"])
	}
}

func TestConvertRequest_OpenAIToAnthropic_ToolChoiceNamed(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tool_choice": map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "get_weather"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	tc := m["tool_choice"].(map[string]any)
	if tc["type"] != "tool" || tc["name"] != "get_weather" {
		t.Errorf("tool_choice = %v, want type=tool name=get_weather", tc)
	}
}

func TestConvertRequest_OpenAIToAnthropic_ToolChoiceRequired(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tool_choice": "required",
	})
	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	tc := m["tool_choice"].(map[string]any)
	if tc["type"] != "any" {
		t.Errorf("tool_choice type = %v, want any (required→any)", tc["type"])
	}
}

// ── Request: Anthropic → OpenAI ─────────────────────────────────────────────

func TestConvertRequest_AnthropicToOpenAI_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["model"] != "claude-3" {
		t.Errorf("model = %v, want claude-3", m["model"])
	}
	if !numEqual(m["max_tokens"], 1024) {
		t.Errorf("max_tokens = %v, want 1024", m["max_tokens"])
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestConvertRequest_AnthropicToOpenAI_WithSystem(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"system":     "You are helpful",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hi"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	msgs := m["messages"].([]any)
	if len(msgs) < 1 {
		t.Fatal("no messages")
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("first message role = %v, want system", first["role"])
	}
	if first["content"] != "You are helpful" {
		t.Errorf("system content = %v, want 'You are helpful'", first["content"])
	}
}

func TestConvertRequest_AnthropicToOpenAI_SystemBlocks(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"system": []any{
			map[string]any{"type": "text", "text": "Part 1"},
			map[string]any{"type": "text", "text": "Part 2"},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "Hi"},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	msgs := m["messages"].([]any)
	sysMsg := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" {
		t.Errorf("role = %v, want system", sysMsg["role"])
	}
	content := sysMsg["content"].(string)
	if content != "Part 1\nPart 2" {
		t.Errorf("system content = %q, want 'Part 1\\nPart 2'", content)
	}
}

func TestConvertRequest_AnthropicToOpenAI_ToolUse(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "What's the weather?"},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "Let me check"},
					map[string]any{
						"type":  "tool_use",
						"id":    "tu_123",
						"name":  "get_weather",
						"input": map[string]any{"city": "SF"},
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "tu_123",
						"content":     "Sunny, 72°F",
					},
				},
			},
		},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	msgs := m["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	assistantMsg := msgs[1].(map[string]any)
	if assistantMsg["role"] != "assistant" {
		t.Errorf("msg[1] role = %v, want assistant", assistantMsg["role"])
	}
	tcs := assistantMsg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(tcs))
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "tu_123" {
		t.Errorf("tool_call id = %v, want tu_123", tc["id"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("function name = %v, want get_weather", fn["name"])
	}
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Errorf("msg[2] role = %v, want tool", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "tu_123" {
		t.Errorf("tool_call_id = %v, want tu_123", toolMsg["tool_call_id"])
	}
}

func TestConvertRequest_AnthropicToOpenAI_Tools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{
				"name":         "get_weather",
				"description":  "Get weather",
				"input_schema": map[string]any{"type": "object"},
			},
		},
		"tool_choice": map[string]any{"type": "any"},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	tools := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("function name = %v, want get_weather", fn["name"])
	}
	if _, ok := fn["parameters"]; !ok {
		t.Error("missing parameters (from input_schema)")
	}
	if m["tool_choice"] != "required" {
		t.Errorf("tool_choice = %v, want required", m["tool_choice"])
	}
}

func TestConvertRequest_AnthropicToOpenAI_StopSequences(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"stop_sequences": []any{"END"},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["stop"] != "END" {
		t.Errorf("stop = %v, want END", m["stop"])
	}
}

func TestConvertRequest_AnthropicToOpenAI_Metadata(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"metadata": map[string]any{"user_id": "u-123"},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["user"] != "u-123" {
		t.Errorf("user = %v, want u-123", m["user"])
	}
}

func TestConvertRequest_AnthropicToOpenAI_ToolChoiceNamed(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": 1024,
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tool_choice": map[string]any{"type": "tool", "name": "get_weather"},
	})
	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	m := mustUnmarshal(t, result)
	tc := m["tool_choice"].(map[string]any)
	if tc["type"] != "function" {
		t.Errorf("tool_choice type = %v, want function", tc["type"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("function name = %v, want get_weather", fn["name"])
	}
}

// ── Request: Round-trip ─────────────────────────────────────────────────────

func TestConvertRequest_RoundTrip_PlainText(t *testing.T) {
	original := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
	})
	ant, err := ConvertRequest(original, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("first conversion: %v", err)
	}
	back, err := ConvertRequest(ant, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("second conversion: %v", err)
	}
	m := mustUnmarshal(t, back)
	if m["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", m["model"])
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
	if msg["content"] != "Hello" {
		t.Errorf("content = %v, want Hello", msg["content"])
	}
}

func TestConvertRequest_RoundTrip_WithTools(t *testing.T) {
	original := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":       "calc",
					"parameters": map[string]any{"type": "object"},
				},
			},
		},
		"tool_choice": "auto",
	})
	ant, err := ConvertRequest(original, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	back, err := ConvertRequest(ant, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	m := mustUnmarshal(t, back)
	tools := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "calc" {
		t.Errorf("function name = %v, want calc", fn["name"])
	}
	if m["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", m["tool_choice"])
	}
}

// ── Response: OpenAI → Anthropic ────────────────────────────────────────────

func TestConvertResponse_OpenAIToAnthropic_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion",
		"model":   "gpt-4",
		"created": 1234,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello!",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 5,
			"total_tokens":      15,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["type"] != "message" {
		t.Errorf("type = %v, want message", m["type"])
	}
	if m["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", m["role"])
	}
	if m["stop_reason"] != "end_turn" {
		t.Errorf("stop_reason = %v, want end_turn", m["stop_reason"])
	}
	usage := m["usage"].(map[string]any)
	if !numEqual(usage["input_tokens"], 10) {
		t.Errorf("input_tokens = %v, want 10", usage["input_tokens"])
	}
	if !numEqual(usage["output_tokens"], 5) {
		t.Errorf("output_tokens = %v, want 5", usage["output_tokens"])
	}
}

func TestConvertResponse_OpenAIToAnthropic_ToolCalls(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":      "chatcmpl-2",
		"object":  "chat.completion",
		"model":   "gpt-4",
		"created": 1234,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []any{
						map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      "get_weather",
								"arguments": `{"city":"SF"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     20,
			"completion_tokens": 10,
			"total_tokens":      30,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v, want tool_use", m["stop_reason"])
	}
	content := m["content"].([]any)
	foundToolUse := false
	for _, c := range content {
		if block, ok := c.(map[string]any); ok && block["type"] == "tool_use" {
			foundToolUse = true
			if block["id"] != "call_1" {
				t.Errorf("tool_use id = %v, want call_1", block["id"])
			}
		}
	}
	if !foundToolUse {
		t.Error("missing tool_use block")
	}
}

func TestConvertResponse_OpenAIToAnthropic_LengthStop(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":      "chatcmpl-3",
		"object":  "chat.completion",
		"model":   "gpt-4",
		"created": 1234,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Truncated...",
				},
				"finish_reason": "length",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     5,
			"completion_tokens": 100,
			"total_tokens":      105,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["stop_reason"] != "max_tokens" {
		t.Errorf("stop_reason = %v, want max_tokens", m["stop_reason"])
	}
}

// ── Response: Anthropic → OpenAI ────────────────────────────────────────────

func TestConvertResponse_AnthropicToOpenAI_PlainText(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":    "msg_1",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-3",
		"content": []any{
			map[string]any{"type": "text", "text": "Hello!"},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  10,
			"output_tokens": 5,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	if m["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", m["object"])
	}
	choices := m["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Hello!" {
		t.Errorf("content = %v, want Hello!", msg["content"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
	usage := m["usage"].(map[string]any)
	if !numEqual(usage["prompt_tokens"], 10) {
		t.Errorf("prompt_tokens = %v, want 10", usage["prompt_tokens"])
	}
	if !numEqual(usage["total_tokens"], 15) {
		t.Errorf("total_tokens = %v, want 15", usage["total_tokens"])
	}
}

func TestConvertResponse_AnthropicToOpenAI_ToolUse(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":    "msg_2",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-3",
		"content": []any{
			map[string]any{
				"type":  "tool_use",
				"id":    "tu_1",
				"name":  "get_weather",
				"input": map[string]any{"city": "SF"},
			},
		},
		"stop_reason":   "tool_use",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  20,
			"output_tokens": 10,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	choices := m["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	tcs := msg["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(tcs))
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "tu_1" {
		t.Errorf("tool_call id = %v, want tu_1", tc["id"])
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice["finish_reason"])
	}
}

func TestConvertResponse_AnthropicToOpenAI_MaxTokens(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":    "msg_3",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-3",
		"content": []any{
			map[string]any{"type": "text", "text": "Truncated"},
		},
		"stop_reason":   "max_tokens",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  5,
			"output_tokens": 100,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	choices := m["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "length" {
		t.Errorf("finish_reason = %v, want length", choice["finish_reason"])
	}
}

func TestConvertResponse_AnthropicToOpenAI_StopSequence(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"id":    "msg_4",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-3",
		"content": []any{
			map[string]any{"type": "text", "text": "Done"},
		},
		"stop_reason":   "stop_sequence",
		"stop_sequence": "END",
		"usage": map[string]any{
			"input_tokens":  5,
			"output_tokens": 3,
		},
	})
	result, err := ConvertResponse(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertResponse: %v", err)
	}
	m := mustUnmarshal(t, result)
	choices := m["choices"].([]any)
	choice := choices[0].(map[string]any)
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop (stop_sequence→stop)", choice["finish_reason"])
	}
}

// ── Response: Round-trip ────────────────────────────────────────────────────

func TestConvertResponse_RoundTrip_PlainText(t *testing.T) {
	original := mustMarshal(t, map[string]any{
		"id":      "chatcmpl-1",
		"object":  "chat.completion",
		"model":   "gpt-4",
		"created": 1234,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello!",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     10,
			"completion_tokens": 5,
			"total_tokens":      15,
		},
	})
	ant, err := ConvertResponse(original, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	back, err := ConvertResponse(ant, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	m := mustUnmarshal(t, back)
	choices := m["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)
	if msg["content"] != "Hello!" {
		t.Errorf("content = %v, want Hello!", msg["content"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
	usage := m["usage"].(map[string]any)
	if !numEqual(usage["prompt_tokens"], 10) {
		t.Errorf("prompt_tokens = %v, want 10", usage["prompt_tokens"])
	}
	if !numEqual(usage["completion_tokens"], 5) {
		t.Errorf("completion_tokens = %v, want 5", usage["completion_tokens"])
	}
}

// ── Rejection tests ─────────────────────────────────────────────────────────

func TestConvertRequest_UnsupportedProtocol(t *testing.T) {
	_, err := ConvertRequest([]byte(`{}`), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIChat)
	if err != ErrUnsupportedConversion {
		t.Errorf("same protocol: err = %v, want ErrUnsupportedConversion", err)
	}
	// Chat → Responses is now a supported pair: an empty body must be rejected
	// as ErrInvalidRequest, NOT ErrUnsupportedConversion.
	_, err = ConvertRequest([]byte(`{}`), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIResponses)
	if err == ErrUnsupportedConversion {
		t.Errorf("chat→responses: err = ErrUnsupportedConversion, want supported (ErrInvalidRequest)")
	}
}

func TestConvertRequest_UnsupportedImages(t *testing.T) {
	_, err := ConvertRequest([]byte(`{}`), adapter.ProtocolOpenAIImages, adapter.ProtocolAnthropic)
	if err != ErrUnsupportedConversion {
		t.Errorf("images: err = %v, want ErrUnsupportedConversion", err)
	}
}

func TestConvertRequest_MalformedJSON(t *testing.T) {
	_, err := ConvertRequest([]byte(`not json`), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("malformed: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_EmptyBody(t *testing.T) {
	_, err := ConvertRequest([]byte{}, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("empty: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_MissingModel(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "Hi"},
		},
	})
	_, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("missing model: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_MissingMessages(t *testing.T) {
	body := mustMarshal(t, map[string]any{"model": "gpt-4"})
	_, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("missing messages: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_InvalidRole(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "invalid", "content": "Hi"},
		},
	})
	_, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("invalid role: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_AnthropicMissingMaxTokens(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "claude-3",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hi"},
		},
	})
	_, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != ErrInvalidRequest {
		t.Errorf("missing max_tokens: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertResponse_UnsupportedProtocol(t *testing.T) {
	_, err := ConvertResponse([]byte(`{}`), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIChat)
	if err != ErrUnsupportedConversion {
		t.Errorf("err = %v, want ErrUnsupportedConversion", err)
	}
}

func TestConvertResponse_MalformedJSON(t *testing.T) {
	_, err := ConvertResponse([]byte(`bad`), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidResponse {
		t.Errorf("err = %v, want ErrInvalidResponse", err)
	}
}

func TestConvertStreamChunk_UnsupportedProtocol(t *testing.T) {
	_, err := ConvertStreamChunk([]byte(`{}`), adapter.ProtocolOpenAIChat, adapter.ProtocolOpenAIChat, &StreamState{})
	if err != ErrUnsupportedConversion {
		t.Errorf("err = %v, want ErrUnsupportedConversion", err)
	}
}

func TestConvertStreamChunk_NilState(t *testing.T) {
	_, err := ConvertStreamChunk([]byte(`{}`), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, nil)
	if err != ErrInvalidStreamChunk {
		t.Errorf("err = %v, want ErrInvalidStreamChunk", err)
	}
}

func TestConvertStreamChunk_InvalidChunk(t *testing.T) {
	_, err := ConvertStreamChunk([]byte(`not json`), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, &StreamState{})
	if err != ErrInvalidStreamChunk {
		t.Errorf("err = %v, want ErrInvalidStreamChunk", err)
	}
}

func TestConvertStreamChunk_EmptyChunk(t *testing.T) {
	results, err := ConvertStreamChunk([]byte{}, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, &StreamState{})
	if err != nil {
		t.Errorf("empty chunk: err = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty chunk: results = %d, want 0", len(results))
	}
}

func TestConvertRequest_DuplicateKeys(t *testing.T) {
	body := []byte(`{"model":"gpt-4","model":"gpt-3","messages":[{"role":"user","content":"hi"}]}`)
	_, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("duplicate keys: err = %v, want ErrInvalidRequest", err)
	}
}

func TestConvertRequest_PrototypeKey(t *testing.T) {
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"__proto__":{}}`)
	_, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != ErrInvalidRequest {
		t.Errorf("prototype key: err = %v, want ErrInvalidRequest", err)
	}
}

// ── Randomized round-trip property test ─────────────────────────────────────

func TestRequestRoundTrip_Randomized(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	roles := []string{"user", "assistant"}
	contents := []string{"Hello", "How are you?", "Let me check", "The answer is 42", "Goodbye"}

	for i := 0; i < 100; i++ {
		nMessages := rng.Intn(5) + 1
		var messages []any
		for j := 0; j < nMessages; j++ {
			role := roles[rng.Intn(len(roles))]
			content := contents[rng.Intn(len(contents))]
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
		body := mustMarshal(t, map[string]any{
			"model":    "test-model",
			"messages": messages,
		})
		ant, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
		if err != nil {
			continue // some random combos may be invalid (e.g. assistant first)
		}
		back, err := ConvertRequest(ant, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
		if err != nil {
			t.Errorf("round-trip iteration %d: back conversion failed: %v", i, err)
			continue
		}
		if !json.Valid(back) {
			t.Errorf("round-trip iteration %d: result is not valid JSON", i)
		}
	}
}
