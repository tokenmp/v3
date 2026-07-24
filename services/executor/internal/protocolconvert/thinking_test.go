package protocolconvert

import (
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestConvertRequest_OpenAIToAnthropic_Thinking(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":            "gpt-4",
		"reasoning_effort": "medium",
		"messages": []any{
			map[string]any{"role": "user", "content": "solve"},
			map[string]any{"role": "assistant", "reasoning_content": "work it out", "content": "answer"},
		},
	})

	result, err := ConvertRequest(body, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	root := mustUnmarshal(t, result)
	thinking := root["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || !numEqual(thinking["budget_tokens"], 8192) {
		t.Fatalf("thinking = %#v, want enabled budget 8192", thinking)
	}
	blocks := root["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if blocks[0].(map[string]any)["type"] != "thinking" || blocks[0].(map[string]any)["thinking"] != "work it out" {
		t.Fatalf("first assistant block = %#v, want thinking", blocks[0])
	}
	if blocks[1].(map[string]any)["type"] != "text" {
		t.Fatalf("second assistant block = %#v, want text", blocks[1])
	}
}

func TestConvertRequest_AnthropicToOpenAI_Thinking(t *testing.T) {
	body := mustMarshal(t, map[string]any{
		"model":      "claude",
		"max_tokens": jsonNum(20000),
		"thinking":   map[string]any{"type": "enabled", "budget_tokens": jsonNum(16384)},
		"messages": []any{
			map[string]any{"role": "user", "content": "solve"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "work it out", "signature": "sig"},
				map[string]any{"type": "text", "text": "answer"},
			}},
		},
	})

	result, err := ConvertRequest(body, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest: %v", err)
	}
	root := mustUnmarshal(t, result)
	if root["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v, want high", root["reasoning_effort"])
	}
	assistant := root["messages"].([]any)[1].(map[string]any)
	if assistant["reasoning_content"] != "work it out" || assistant["content"] != "answer" {
		t.Fatalf("assistant = %#v, want converted reasoning and text", assistant)
	}
}

func TestThinkingEffortBudgetBoundaries(t *testing.T) {
	for _, tc := range []struct {
		effort string
		budget int64
	}{
		{"none", 0}, {"minimal", 1024}, {"low", 2048}, {"medium", 8192},
		{"high", 16384}, {"xhigh", 32768}, {"max", 65536},
	} {
		if got := effortBudget(tc.effort); got != tc.budget {
			t.Errorf("effortBudget(%q) = %d, want %d", tc.effort, got, tc.budget)
		}
	}
	for _, tc := range []struct {
		budget int64
		effort string
	}{
		{0, "none"}, {1023, ""}, {1024, "minimal"}, {2048, "low"}, {8192, "medium"},
		{16384, "high"}, {32768, "xhigh"}, {65536, "max"},
	} {
		if got := budgetEffort(tc.budget); got != tc.effort {
			t.Errorf("budgetEffort(%d) = %q, want %q", tc.budget, got, tc.effort)
		}
	}
}

func TestConvertStreamChunk_OpenAIToAnthropic_ReasoningThenText(t *testing.T) {
	state := &StreamState{}
	chunk := func(delta map[string]any) []byte {
		return mustMarshal(t, map[string]any{"id": "c", "model": "gpt-4", "choices": []any{map[string]any{"delta": delta}}})
	}
	got, err := ConvertStreamChunk(chunk(map[string]any{"reasoning_content": "think"}), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil || len(got) != 3 {
		t.Fatalf("reasoning: events=%d err=%v, want message start + thinking start + delta", len(got), err)
	}
	assertEventType(t, got[1], "content_block_start")
	if mustUnmarshal(t, got[1])["content_block"].(map[string]any)["type"] != "thinking" {
		t.Fatalf("reasoning start = %s", got[1])
	}
	assertEventType(t, got[2], "content_block_delta")
	if mustUnmarshal(t, got[2])["delta"].(map[string]any)["type"] != "thinking_delta" {
		t.Fatalf("reasoning delta = %s", got[2])
	}
	got, err = ConvertStreamChunk(chunk(map[string]any{"content": "text"}), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil || len(got) != 3 {
		t.Fatalf("text: events=%d err=%v, want thinking stop + text start + delta", len(got), err)
	}
	assertEventType(t, got[0], "content_block_stop")
	if mustUnmarshal(t, got[1])["content_block"].(map[string]any)["type"] != "text" {
		t.Fatalf("text start = %s", got[1])
	}
}

func TestConvertStreamChunk_OpenAIToAnthropic_ReasoningTerminalClosesBlock(t *testing.T) {
	state := &StreamState{}
	chunk := mustMarshal(t, map[string]any{
		"id": "c", "model": "gpt-4",
		"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "think"}}},
	})
	if _, err := ConvertStreamChunk(chunk, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state); err != nil {
		t.Fatal(err)
	}
	got, err := ConvertStreamChunk([]byte("[DONE]"), adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, state)
	if err != nil || len(got) != 3 {
		t.Fatalf("terminal events=%d err=%v, want thinking stop + terminal pair", len(got), err)
	}
	assertEventType(t, got[0], "content_block_stop")
	assertEventType(t, got[1], "message_delta")
	assertEventType(t, got[2], "message_stop")
}

func TestConvertRequest_WithoutThinkingHasNoThinkingFields(t *testing.T) {
	openAI := mustMarshal(t, map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	anthropic, err := ConvertRequest(openAI, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mustUnmarshal(t, anthropic)["thinking"]; ok {
		t.Fatal("unexpected Anthropic thinking field")
	}

	messages := mustMarshal(t, map[string]any{"model": "claude", "max_tokens": jsonNum(4096), "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	converted, err := ConvertRequest(messages, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mustUnmarshal(t, converted)["reasoning_effort"]; ok {
		t.Fatal("unexpected OpenAI reasoning_effort field")
	}
}

func TestConvertStreamChunk_AnthropicToOpenAI_ThinkingThenText(t *testing.T) {
	state := &StreamState{}
	start := func(index int, typ string) []byte {
		return mustMarshal(t, map[string]any{"type": "content_block_start", "index": jsonNum(index), "content_block": map[string]any{"type": typ}})
	}
	if _, err := ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "message_start", "message": map[string]any{"id": "m", "model": "claude"}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state); err != nil {
		t.Fatal(err)
	}
	got, err := ConvertStreamChunk(start(0, "thinking"), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil || len(got) != 1 || mustUnmarshal(t, got[0])["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["role"] != "assistant" {
		t.Fatalf("thinking start: %#v err=%v", got, err)
	}
	got, err = ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "content_block_delta", "index": jsonNum(0), "delta": map[string]any{"type": "thinking_delta", "thinking": "think"}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil || len(got) != 1 || mustUnmarshal(t, got[0])["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["reasoning_content"] != "think" {
		t.Fatalf("thinking delta: %#v err=%v", got, err)
	}
	if _, err := ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "content_block_stop", "index": jsonNum(0)}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state); err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertStreamChunk(start(1, "text"), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state); err != nil {
		t.Fatal(err)
	}
	got, err = ConvertStreamChunk(mustMarshal(t, map[string]any{"type": "content_block_delta", "index": jsonNum(1), "delta": map[string]any{"type": "text_delta", "text": "text"}}), adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat, state)
	if err != nil || len(got) != 1 || mustUnmarshal(t, got[0])["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"] != "text" {
		t.Fatalf("text delta: %#v err=%v", got, err)
	}
}

func assertEventType(t *testing.T, raw []byte, want string) {
	t.Helper()
	if got := mustUnmarshal(t, raw)["type"]; got != want {
		t.Fatalf("event type = %v, want %s: %s", got, want, raw)
	}
}
