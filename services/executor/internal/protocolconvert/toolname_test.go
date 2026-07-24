package protocolconvert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── SanitizeToolNames: invalid names ────────────────────────────────────────

func TestSanitizeToolNames_ChineseName(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{
				"name": "搜索工具",
			},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	tools := m["tools"].([]any)
	name := tools[0].(map[string]any)["name"].(string)
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if orig, ok := nameMap[name]; !ok || orig != "搜索工具" {
		t.Errorf("map[%q] = %q, want 搜索工具", name, orig)
	}
}

func TestSanitizeToolNames_SpaceInName(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "my tool"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	m := mustUnmarshal(t, sanitized)
	name := m["tools"].([]any)[0].(map[string]any)["name"].(string)
	if name != "my_tool" {
		t.Errorf("name = %q, want my_tool", name)
	}
	if nameMap["my_tool"] != "my tool" {
		t.Errorf("map[my_tool] = %q, want 'my tool'", nameMap["my_tool"])
	}
}

func TestSanitizeToolNames_DotInName(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	m := mustUnmarshal(t, sanitized)
	name := m["tools"].([]any)[0].(map[string]any)["name"].(string)
	if name != "com_example_tool" {
		t.Errorf("name = %q, want com_example_tool", name)
	}
	if nameMap["com_example_tool"] != "com.example.tool" {
		t.Errorf("map mismatch")
	}
}

func TestSanitizeToolNames_NumericStart(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "123tool"},
		},
	})
	sanitized, _, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	m := mustUnmarshal(t, sanitized)
	name := m["tools"].([]any)[0].(map[string]any)["name"].(string)
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if name[:5] != "tool_" {
		t.Errorf("name = %q, should start with tool_", name)
	}
}

func TestSanitizeToolNames_TooLong(t *testing.T) {
	// Create a name longer than 64 chars.
	longName := ""
	for i := 0; i < 100; i++ {
		longName += "a"
	}
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": longName},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	m := mustUnmarshal(t, sanitized)
	name := m["tools"].([]any)[0].(map[string]any)["name"].(string)
	if len(name) > maxToolNameLen {
		t.Errorf("name length = %d, want <= %d", len(name), maxToolNameLen)
	}
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if nameMap[name] != longName {
		t.Errorf("map mismatch")
	}
}

func TestSanitizeToolNames_Collision(t *testing.T) {
	// Two tools that would sanitize to the same name.
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "my.tool"},
			map[string]any{"name": "my_tool"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	m := mustUnmarshal(t, sanitized)
	tools := m["tools"].([]any)
	name0 := tools[0].(map[string]any)["name"].(string)
	name1 := tools[1].(map[string]any)["name"].(string)
	// Both names must be valid and different.
	if !isValidToolName(name0) {
		t.Errorf("name0 %q is not valid", name0)
	}
	if !isValidToolName(name1) {
		t.Errorf("name1 %q is not valid", name1)
	}
	if name0 == name1 {
		t.Errorf("collision: both names are %q", name0)
	}
	// my_tool is already valid, should be preserved.
	if name1 != "my_tool" {
		t.Errorf("name1 = %q, want my_tool", name1)
	}
	// my.tool should be sanitized to my_tool, but collision → my_tool_2
	if name0 != "my_tool_2" {
		t.Errorf("name0 = %q, want my_tool_2", name0)
	}
	// Map should have the sanitized→original mapping.
	if nameMap["my_tool_2"] != "my.tool" {
		t.Errorf("map[my_tool_2] = %q, want my.tool", nameMap["my_tool_2"])
	}
}

func TestSanitizeToolNames_EmptyName(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": ""},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	m := mustUnmarshal(t, sanitized)
	name := m["tools"].([]any)[0].(map[string]any)["name"].(string)
	if name != "tool_0" {
		t.Errorf("name = %q, want tool_0", name)
	}
	if nameMap["tool_0"] != "" {
		t.Errorf("map[tool_0] = %q, want empty", nameMap["tool_0"])
	}
}

// ── SanitizeToolNames: references with no tools ─────────────────────────────

func TestSanitizeToolNames_ToolChoiceNoTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tool_choice": map[string]any{
			"type": "tool",
			"name": "com.example.tool",
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	tc := m["tool_choice"].(map[string]any)
	name := tc["name"].(string)
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if nameMap[name] != "com.example.tool" {
		t.Errorf("map[%q] = %q, want com.example.tool", name, nameMap[name])
	}
}

func TestSanitizeToolNames_ToolUseInMessagesNoTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "tool_use", "id": "call_1", "name": "com.example.tool", "input": map[string]any{}},
				},
			},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	msgs := m["messages"].([]any)
	assistantMsg := msgs[1].(map[string]any)
	blocks := assistantMsg["content"].([]any)
	name := blocks[0].(map[string]any)["name"].(string)
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if nameMap[name] != "com.example.tool" {
		t.Errorf("map[%q] = %q, want com.example.tool", name, nameMap[name])
	}
}

func TestSanitizeToolNames_ValidToolsButInvalidToolChoice(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "get_weather"},
		},
		"tool_choice": map[string]any{
			"type": "tool",
			"name": "com.example.tool",
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	tc := m["tool_choice"].(map[string]any)
	name := tc["name"].(string)
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if nameMap[name] != "com.example.tool" {
		t.Errorf("map[%q] = %q, want com.example.tool", name, nameMap[name])
	}
	// get_weather should be unchanged.
	tools := m["tools"].([]any)
	if tools[0].(map[string]any)["name"] != "get_weather" {
		t.Errorf("valid tool name should be unchanged")
	}
}

func TestSanitizeToolNames_ValidToolsButInvalidToolUseInMessages(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "tool_use", "id": "call_1", "name": "com.example.tool", "input": map[string]any{}},
				},
			},
		},
		"tools": []any{
			map[string]any{"name": "get_weather"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	msgs := m["messages"].([]any)
	assistantMsg := msgs[1].(map[string]any)
	blocks := assistantMsg["content"].([]any)
	name := blocks[0].(map[string]any)["name"].(string)
	if !isValidToolName(name) {
		t.Errorf("sanitized name %q is not valid", name)
	}
	if nameMap[name] != "com.example.tool" {
		t.Errorf("map[%q] = %q, want com.example.tool", name, nameMap[name])
	}
}

// ── resolveCollision: high suffix / arbitrary length ────────────────────────

func TestResolveCollision_HighSuffix(t *testing.T) {
	used := make(map[string]bool)
	// Use a base name that fills the entire maxToolNameLen.
	base := strings.Repeat("a", maxToolNameLen)
	used[base] = true

	// Add collision names _2 through _99 by simulating sequential resolution.
	for i := 2; i <= 99; i++ {
		candidate := resolveCollision(base, used)
		used[candidate] = true
	}

	// The next collision should produce _100.
	result := resolveCollision(base, used)
	if len(result) > maxToolNameLen {
		t.Errorf("result length = %d, want <= %d", len(result), maxToolNameLen)
	}
	if !strings.HasSuffix(result, "_100") {
		t.Errorf("result %q should end with _100", result)
	}
	if used[result] {
		t.Errorf("result %q is already in used map", result)
	}
}

func TestResolveCollision_SuffixPreserved(t *testing.T) {
	used := make(map[string]bool)
	base := strings.Repeat("b", maxToolNameLen)
	used[base] = true

	// Fill up through _999 (4-char suffix).
	for i := 2; i <= 999; i++ {
		candidate := resolveCollision(base, used)
		used[candidate] = true
	}

	result := resolveCollision(base, used)
	if len(result) > maxToolNameLen {
		t.Errorf("result length = %d, want <= %d", len(result), maxToolNameLen)
	}
	if !strings.HasSuffix(result, "_1000") {
		t.Errorf("result %q should end with _1000", result)
	}
	if used[result] {
		t.Errorf("result %q is already in used map", result)
	}
}

// ── SanitizeToolNames: valid names ──────────────────────────────────────────

func TestSanitizeToolNames_ValidUnchanged(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "get_weather"},
			map[string]any{"name": "searchDatabase"},
			map[string]any{"name": "tool-123"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	// All names valid → no map, original bytes returned.
	if nameMap != nil {
		t.Errorf("expected nil map for all-valid names, got %v", nameMap)
	}
	// Body should be identical (bytes preserved).
	if string(sanitized) != string(body) {
		t.Errorf("body changed for valid names")
	}
}

func TestSanitizeToolNames_NoTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if nameMap != nil {
		t.Errorf("expected nil map for no tools, got %v", nameMap)
	}
	if string(sanitized) != string(body) {
		t.Errorf("body changed for no tools")
	}
}

func TestSanitizeToolNames_EmptyTools(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if nameMap != nil {
		t.Errorf("expected nil map for empty tools, got %v", nameMap)
	}
	if string(sanitized) != string(body) {
		t.Errorf("body changed for empty tools")
	}
}

// ── SanitizeToolNames: tool_choice renaming ─────────────────────────────────

func TestSanitizeToolNames_ToolChoiceRenamed(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool"},
		},
		"tool_choice": map[string]any{
			"type": "tool",
			"name": "com.example.tool",
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	tc := m["tool_choice"].(map[string]any)
	name := tc["name"].(string)
	if name != "com_example_tool" {
		t.Errorf("tool_choice.name = %q, want com_example_tool", name)
	}
}

// ── SanitizeToolNames: assistant message tool_use renaming ──────────────────

func TestSanitizeToolNames_AssistantToolUseRenamed(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "Let me use a tool"},
					map[string]any{"type": "tool_use", "id": "call_1", "name": "com.example.tool", "input": map[string]any{}},
				},
			},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool"},
		},
	})
	sanitized, nameMap, err := SanitizeToolNames(body)
	if err != nil {
		t.Fatalf("SanitizeToolNames: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, sanitized)
	msgs := m["messages"].([]any)
	assistantMsg := msgs[1].(map[string]any)
	blocks := assistantMsg["content"].([]any)
	toolUseBlock := blocks[1].(map[string]any)
	name := toolUseBlock["name"].(string)
	if name != "com_example_tool" {
		t.Errorf("tool_use.name = %q, want com_example_tool", name)
	}
}

// ── RestoreToolNamesResponse ────────────────────────────────────────────────

func TestRestoreToolNamesResponse_RestoresToolCalls(t *testing.T) {
	nameMap := map[string]string{
		"com_example_tool": "com.example.tool",
	}
	response := mustMarshal(t, map[string]any{
		"id":     "chatcmpl-123",
		"object": "chat.completion",
		"model":  "gpt-4",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []any{
						map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      "com_example_tool",
								"arguments": "{}",
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	})
	restored, err := RestoreToolNamesResponse(response, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	m := mustUnmarshal(t, restored)
	choices := m["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	tcs := msg["tool_calls"].([]any)
	name := tcs[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if name != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", name)
	}
}

func TestRestoreToolNamesResponse_NoopEmptyMap(t *testing.T) {
	response := mustMarshal(t, map[string]any{
		"id":     "chatcmpl-123",
		"object": "chat.completion",
		"model":  "gpt-4",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello",
				},
				"finish_reason": "stop",
			},
		},
	})
	restored, err := RestoreToolNamesResponse(response, nil)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	if string(restored) != string(response) {
		t.Errorf("noop should return same bytes")
	}
}

func TestRestoreToolNamesResponse_NilMap(t *testing.T) {
	response := []byte(`{"choices":[]}`)
	restored, err := RestoreToolNamesResponse(response, nil)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	if string(restored) != string(response) {
		t.Errorf("nil map should return same bytes")
	}
}

func TestRestoreToolNamesResponse_AnthropicContentBlocks(t *testing.T) {
	nameMap := map[string]string{
		"com_example_tool": "com.example.tool",
	}
	// Anthropic response with content[].type=tool_use.
	response := mustMarshal(t, map[string]any{
		"id":    "msg_123",
		"type":  "message",
		"model": "claude-3",
		"role":  "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "Let me use a tool"},
			map[string]any{"type": "tool_use", "id": "toolu_123", "name": "com_example_tool", "input": map[string]any{"q": "test"}},
		},
		"stop_reason": "tool_use",
	})
	restored, err := RestoreToolNamesResponse(response, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	m := mustUnmarshal(t, restored)
	content := m["content"].([]any)
	toolBlock := content[1].(map[string]any)
	name := toolBlock["name"].(string)
	if name != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", name)
	}
}

func TestRestoreToolNamesResponse_AnthropicWrappedMessage(t *testing.T) {
	nameMap := map[string]string{
		"com_example_tool": "com.example.tool",
	}
	// Anthropic response with top-level message wrapper containing content[].
	response := mustMarshal(t, map[string]any{
		"type": "message",
		"message": map[string]any{
			"id":    "msg_456",
			"type":  "message",
			"model": "claude-3",
			"role":  "assistant",
			"content": []any{
				map[string]any{"type": "tool_use", "id": "toolu_456", "name": "com_example_tool", "input": map[string]any{}},
			},
		},
	})
	restored, err := RestoreToolNamesResponse(response, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	m := mustUnmarshal(t, restored)
	msg := m["message"].(map[string]any)
	content := msg["content"].([]any)
	toolBlock := content[0].(map[string]any)
	name := toolBlock["name"].(string)
	if name != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", name)
	}
}

// ── RestoreToolNamesStreamChunk: OpenAI direction ───────────────────────────

func TestRestoreToolNamesStreamChunk_OpenAIChunk(t *testing.T) {
	nameMap := map[string]string{
		"com_example_tool": "com.example.tool",
	}
	// OpenAI tool_calls start chunk.
	chunk := mustMarshal(t, map[string]any{
		"id":      "chatcmpl-123",
		"object":  "chat.completion.chunk",
		"created": json.Number("0"),
		"model":   "gpt-4",
		"choices": []any{
			map[string]any{
				"index": json.Number("0"),
				"delta": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"index": json.Number("0"),
							"id":    "call_1",
							"type":  "function",
							"function": map[string]any{
								"name":      "com_example_tool",
								"arguments": "",
							},
						},
					},
				},
				"finish_reason": nil,
			},
		},
	})
	restored, err := RestoreToolNamesStreamChunk(chunk, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", err)
	}
	m := mustUnmarshal(t, restored)
	choices := m["choices"].([]any)
	delta := choices[0].(map[string]any)["delta"].(map[string]any)
	tcs := delta["tool_calls"].([]any)
	name := tcs[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if name != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", name)
	}
}

func TestRestoreToolNamesStreamChunk_AnthropicChunk(t *testing.T) {
	nameMap := map[string]string{
		"com_example_tool": "com.example.tool",
	}
	// Anthropic content_block_start with tool_use.
	chunk := mustMarshal(t, map[string]any{
		"type":  "content_block_start",
		"index": json.Number("0"),
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    "toolu_123",
			"name":  "com_example_tool",
			"input": map[string]any{},
		},
	})
	restored, err := RestoreToolNamesStreamChunk(chunk, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", err)
	}
	m := mustUnmarshal(t, restored)
	block := m["content_block"].(map[string]any)
	name := block["name"].(string)
	if name != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", name)
	}
}

func TestRestoreToolNamesStreamChunk_NoopEmptyMap(t *testing.T) {
	chunk := []byte(`{"type":"content_block_start"}`)
	restored, err := RestoreToolNamesStreamChunk(chunk, nil)
	if err != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", err)
	}
	if string(restored) != string(chunk) {
		t.Errorf("noop should return same bytes")
	}
}

func TestRestoreToolNamesStreamChunk_EmptyChunk(t *testing.T) {
	nameMap := map[string]string{"a": "b"}
	restored, err := RestoreToolNamesStreamChunk(nil, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", err)
	}
	if len(restored) != 0 {
		t.Errorf("empty chunk should return empty, got %q", restored)
	}
}

// ── ConvertRequestWithToolMap: direction tests ──────────────────────────────

func TestConvertRequestWithToolMap_AnthropicToOpenAI_SanitizesNames(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": json.Number("1024"),
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool", "input_schema": map[string]any{}},
		},
	})
	converted, nameMap, err := ConvertRequestWithToolMap(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	m := mustUnmarshal(t, converted)
	tools := m["tools"].([]any)
	name := tools[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if name != "com_example_tool" {
		t.Errorf("tool name = %q, want com_example_tool", name)
	}
}

func TestConvertRequestWithToolMap_OpenAIToAnthropic_NoMap(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model": "gpt-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
	})
	converted, nameMap, err := ConvertRequestWithToolMap(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) > 0 {
		t.Errorf("expected empty map for OpenAI→Anthropic, got %v", nameMap)
	}
	_ = converted
}

func TestConvertRequestWithToolMap_AnthropicToOpenAI_ValidNoMap(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": json.Number("1024"),
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "get_weather", "input_schema": map[string]any{}},
		},
	})
	converted, nameMap, err := ConvertRequestWithToolMap(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) > 0 {
		t.Errorf("expected empty map for valid names, got %v", nameMap)
	}
	_ = converted
}

// ── Round-trip: sanitize → convert → response restore ───────────────────────

func TestRoundTrip_SanitizeConvertResponseRestore(t *testing.T) {
	// Anthropic request with invalid tool name.
	reqBody := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": json.Number("1024"),
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool", "input_schema": map[string]any{}},
		},
	})
	// Convert with sanitization.
	converted, nameMap, err := ConvertRequestWithToolMap(reqBody, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	// Verify converted tool name is sanitized.
	convM := mustUnmarshal(t, converted)
	tools := convM["tools"].([]any)
	sanitizedName := tools[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if sanitizedName == "com.example.tool" {
		t.Fatal("expected sanitized name")
	}

	// Simulate an OpenAI response using the sanitized name.
	respBody := mustMarshal(t, map[string]any{
		"id":     "chatcmpl-123",
		"object": "chat.completion",
		"model":  "gpt-4",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []any{
						map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      sanitizedName,
								"arguments": "{}",
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	})
	// Restore original names.
	restored, err := RestoreToolNamesResponse(respBody, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", err)
	}
	restM := mustUnmarshal(t, restored)
	restTcs := restM["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["tool_calls"].([]any)
	restoredName := restTcs[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if restoredName != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", restoredName)
	}
}

// ── Round-trip: sanitize → convert → stream restore ─────────────────────────

func TestRoundTrip_SanitizeConvertStreamRestore(t *testing.T) {
	reqBody := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": json.Number("1024"),
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "中文工具", "input_schema": map[string]any{}},
		},
	})
	converted, nameMap, err := ConvertRequestWithToolMap(reqBody, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	convM := mustUnmarshal(t, converted)
	sanitizedName := convM["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"].(string)

	// Simulate an OpenAI stream chunk with the sanitized name.
	chunk := mustMarshal(t, map[string]any{
		"id":      "chatcmpl-123",
		"object":  "chat.completion.chunk",
		"created": json.Number("0"),
		"model":   "gpt-4",
		"choices": []any{
			map[string]any{
				"index": json.Number("0"),
				"delta": map[string]any{
					"tool_calls": []any{
						map[string]any{
							"index": json.Number("0"),
							"id":    "call_1",
							"type":  "function",
							"function": map[string]any{
								"name":      sanitizedName,
								"arguments": "",
							},
						},
					},
				},
				"finish_reason": nil,
			},
		},
	})
	restored, err := RestoreToolNamesStreamChunk(chunk, nameMap)
	if err != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", err)
	}
	restM := mustUnmarshal(t, restored)
	restDelta := restM["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	restTcs := restDelta["tool_calls"].([]any)
	restoredName := restTcs[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if restoredName != "中文工具" {
		t.Errorf("restored name = %q, want 中文工具", restoredName)
	}
}

// ── Round-trip: sanitize → convert → Anthropic response restore ─────────────

func TestRoundTrip_SanitizeConvertAnthropicResponseRestore(t *testing.T) {
	// Anthropic request with invalid tool name.
	reqBody := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": json.Number("1024"),
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool", "input_schema": map[string]any{}},
		},
	})
	// Convert with sanitization (Anthropic→OpenAI).
	converted, nameMap, err := ConvertRequestWithToolMap(reqBody, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}
	convM := mustUnmarshal(t, converted)
	sanitizedName := convM["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"].(string)
	if sanitizedName == "com.example.tool" {
		t.Fatal("expected sanitized name")
	}

	// Simulate an OpenAI response using the sanitized name (what the upstream
	// actually returns after we sent the sanitized tool name).
	oaiRespBody := mustMarshal(t, map[string]any{
		"id":     "chatcmpl-123",
		"object": "chat.completion",
		"model":  "gpt-4",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "",
					"tool_calls": []any{
						map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      sanitizedName,
								"arguments": "{\"q\":\"test\"}",
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
	})

	// Convert response back to Anthropic protocol (OpenAI→Anthropic).
	// This simulates what the runner does after a successful upstream call.
	anthRespBody, convErr := ConvertResponse(oaiRespBody, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if convErr != nil {
		t.Fatalf("ConvertResponse: %v", convErr)
	}
	// The converted Anthropic response should contain content[].type=tool_use
	// with the sanitized name. Verify it.
	anthM := mustUnmarshal(t, anthRespBody)
	content := anthM["content"].([]any)
	foundToolUse := false
	for _, block := range content {
		b, ok := block.(map[string]any)
		if !ok || b["type"] != "tool_use" {
			continue
		}
		foundToolUse = true
		if b["name"] != sanitizedName {
			t.Errorf("converted tool_use.name = %q, want sanitized %q", b["name"], sanitizedName)
		}
	}
	if !foundToolUse {
		t.Fatal("converted Anthropic response missing tool_use content block")
	}

	// Now restore original tool names. This is the fix: RestoreToolNamesResponse
	// must handle Anthropic content[].type=tool_use.name, not just OpenAI
	// choices[].message.tool_calls[].function.name.
	restored, restErr := RestoreToolNamesResponse(anthRespBody, nameMap)
	if restErr != nil {
		t.Fatalf("RestoreToolNamesResponse: %v", restErr)
	}
	restM := mustUnmarshal(t, restored)
	restContent := restM["content"].([]any)
	for _, block := range restContent {
		b, ok := block.(map[string]any)
		if !ok || b["type"] != "tool_use" {
			continue
		}
		restoredName := b["name"].(string)
		if restoredName != "com.example.tool" {
			t.Errorf("restored tool_use.name = %q, want com.example.tool", restoredName)
		}
	}
}

func TestRoundTrip_SanitizeConvertAnthropicStreamRestore(t *testing.T) {
	// Anthropic request with invalid tool name.
	reqBody := mustMarshal(t, map[string]any{
		"model":      "claude-3",
		"max_tokens": json.Number("1024"),
		"messages": []any{
			map[string]any{"role": "user", "content": "Hello"},
		},
		"tools": []any{
			map[string]any{"name": "com.example.tool", "input_schema": map[string]any{}},
		},
	})
	_, nameMap, err := ConvertRequestWithToolMap(reqBody, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequestWithToolMap: %v", err)
	}
	if len(nameMap) == 0 {
		t.Fatal("expected non-empty name map")
	}

	// Simulate an Anthropic content_block_start stream chunk with sanitized name.
	chunk := mustMarshal(t, map[string]any{
		"type":  "content_block_start",
		"index": json.Number("0"),
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    "toolu_123",
			"name":  "com_example_tool",
			"input": map[string]any{},
		},
	})
	restored, restErr := RestoreToolNamesStreamChunk(chunk, nameMap)
	if restErr != nil {
		t.Fatalf("RestoreToolNamesStreamChunk: %v", restErr)
	}
	restM := mustUnmarshal(t, restored)
	block := restM["content_block"].(map[string]any)
	restoredName := block["name"].(string)
	if restoredName != "com.example.tool" {
		t.Errorf("restored name = %q, want com.example.tool", restoredName)
	}
}

// ── isValidToolName ─────────────────────────────────────────────────────────

func TestIsValidToolName(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"get_weather", true},
		{"searchDatabase", true},
		{"tool-123", true},
		{"a", true},
		{"A1b2C3", true},
		// Exactly 64 valid chars
		{func() string {
			b := make([]byte, 64)
			for i := range b {
				b[i] = 'a'
			}
			return string(b)
		}(), true},
		// Invalid
		{"", false},
		{"123tool", false},          // starts with digit
		{"my.tool", false},          // contains dot
		{"my tool", false},          // contains space
		{"com.example.tool", false}, // contains dots
		{"搜索工具", false},             // Chinese characters
		{"tool!@#", false},          // special chars
		{"_private", false},         // starts with underscore (not alpha)
		// 65 valid chars (too long)
		{func() string {
			b := make([]byte, 65)
			for i := range b {
				b[i] = 'a'
			}
			return string(b)
		}(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidToolName(tt.name)
			if got != tt.valid {
				t.Errorf("isValidToolName(%q) = %v, want %v", tt.name, got, tt.valid)
			}
		})
	}
}
