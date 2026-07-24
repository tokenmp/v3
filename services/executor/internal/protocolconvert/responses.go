// This file implements the OpenAI Responses ↔ (OpenAI Chat | Anthropic
// Messages) conversions for requests and non-streaming responses. It is a
// pure, transport-neutral JSON→JSON boundary: no I/O, no secrets, no HTTP.
//
// Chat↔Responses is implemented directly. The Anthropic↔Responses variants
// are composed by routing through Chat and reusing the existing, exhaustively
// tested Chat↔Anthropic request/response converters. This keeps a single
// source of truth for the Chat↔Anthropic leg and avoids divergent behavior.
//
// Responses "custom" tools (type "custom") have no Chat/Anthropic function-tool
// equivalent: they are downgraded to a synthetic function tool whose name is
// derived deterministically from the original custom tool name via
// responsesCustomToolWrapperName, and whose single string argument carries the
// raw custom-tool input. The inverse direction unwraps that name. This makes
// the custom-tool mapping fully reversible without any per-request state.

package protocolconvert

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// ── Responses conversion constants ─────────────────────────────────────────

const (
	// responsesCustomToolNamePrefix marks function tools that are synthetic
	// stand-ins for Responses custom tools. It is followed by a sanitized
	// (ASCII-safe) form of the original custom-tool name.
	responsesCustomToolNamePrefix = "responses_custom_"
	// responsesReasoningCarrierPrefix marks the encrypted_content carrier that
	// transports OpenAI reasoning_content text through a Responses reasoning
	// output item. The payload is RawURLEncoding base64 of the raw text.
	responsesReasoningCarrierPrefix = "resp_rs_b64:"
)

// ── Shared JSON helpers (Responses-flavored) ───────────────────────────────
//
// These helpers operate on the json.Number-bearing maps produced by
// parseStrictJSON (which uses UseNumber). They are deliberately narrow and
// defensive: a wrong type yields a zero value, never a panic.

// asSlice returns v as []any or nil when v is not a JSON array.
func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// positiveJSONInt returns a non-negative integer parsed from a json.Number.
// Anything else (including nil, floats, and negative values) yields 0.
func positiveJSONInt(v any) int64 {
	n, ok := v.(json.Number)
	if !ok {
		return 0
	}
	i, err := n.Int64()
	if err != nil || i < 0 {
		return 0
	}
	return i
}

// jsonString returns a JSON-string representation of v suitable for the
// "arguments" / "input" string fields. A string is passed through verbatim
// (OpenAI tool-call arguments are already a JSON-encoded string); any other
// value is JSON-marshaled. A nil/unsupported value becomes the empty string.
func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// messageContentToText extracts concatenated text from a Chat/Responses
// content value. A string is returned as-is. An array of content parts yields
// the concatenation of every text-like part (text/input_text/output_text).
// Anything else yields the empty string.
func messageContentToText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	parts, ok := v.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			continue
		}
		switch stringVal(part["type"]) {
		case "text", "input_text", "output_text":
			b.WriteString(stringVal(part["text"]))
		}
	}
	return b.String()
}

// cloneValue returns a deep copy of a json.Number-bearing value. It preserves
// json.Number (and all other concrete types) without re-decoding.
func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[k] = cloneValue(val)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = cloneValue(val)
		}
		return out
	default:
		return v
	}
}

// numToInt64 coerces a json.Number (or numeric-shaped value) to int64.
func numToInt64(v any) int64 {
	if n, ok := v.(json.Number); ok {
		i, _ := n.Int64()
		return i
	}
	return 0
}

// ── OpenAI usage / tool-call shapes (for response conversion) ──────────────

type openAIUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// parseOpenAIUsage extracts a Chat completion usage object. It tolerates
// json.Number and plain float64 (via json.Number fields) and never errors: a
// missing/malformed usage yields nil.
func parseOpenAIUsage(body []byte) *openAIUsage {
	var resp struct {
		Usage *struct {
			PromptTokens     json.Number `json:"prompt_tokens"`
			CompletionTokens json.Number `json:"completion_tokens"`
			TotalTokens      json.Number `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Usage == nil {
		return nil
	}
	return &openAIUsage{
		PromptTokens:     numToInt64(resp.Usage.PromptTokens),
		CompletionTokens: numToInt64(resp.Usage.CompletionTokens),
		TotalTokens:      numToInt64(resp.Usage.TotalTokens),
	}
}

// syntheticOpenAIFunctionCallID derives a deterministic call id for a legacy
// OpenAI function_call (which carries no id). The id is ASCII-safe.
func syntheticOpenAIFunctionCallID(name string) string {
	var b strings.Builder
	b.WriteString("call_")
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == len("call_") {
		b.WriteByte('0')
	}
	return b.String()
}

// statusForOutputItem maps a top-level response status to the per-output-item
// status. Completed responses yield completed items; terminal non-success
// statuses propagate.
func statusForOutputItem(status string) string {
	switch status {
	case "incomplete", "failed", "cancelled":
		return status
	default:
		return "completed"
	}
}

// responsesUsageMap converts an openAIUsage into the Responses usage object
// shape (input_tokens/output_tokens/total_tokens).
func responsesUsageMap(usage *openAIUsage) map[string]int64 {
	if usage == nil {
		return map[string]int64{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	}
	return map[string]int64{
		"input_tokens":  usage.PromptTokens,
		"output_tokens": usage.CompletionTokens,
		"total_tokens":  usage.TotalTokens,
	}
}

// ── Responses request validation (stateless subset) ────────────────────────
//
// ConvertRequest for a Responses source validates a strict, stateless-only
// field allowlist mirroring internal/responsecontract. This is a defense-in-
// depth boundary: the executor's transport strict options already reject
// stateful fields upstream, but protocolconvert never trusts its input.

var responsesRequestFields = fieldSet(
	"model", "input", "instructions", "max_output_tokens", "metadata",
	"reasoning", "stream", "temperature", "text", "tool_choice", "tools", "top_p",
)

func validateResponsesRequest(root map[string]any) error {
	if !onlyFields(root, responsesRequestFields) || !isString(root["model"]) || stringVal(root["model"]) == "" {
		return ErrInvalidRequest
	}
	if !validResponsesInput(root["input"]) {
		return ErrInvalidRequest
	}
	if instr, ok := root["instructions"]; ok && !isString(instr) {
		return ErrInvalidRequest
	}
	if !optionalPositiveInt(root, "max_output_tokens") {
		return ErrInvalidRequest
	}
	if !validResponsesMetadata(root["metadata"]) {
		return ErrInvalidRequest
	}
	if !validResponsesReasoning(root["reasoning"]) {
		return ErrInvalidRequest
	}
	if stream, ok := root["stream"]; ok && !isBool(stream) {
		return ErrInvalidRequest
	}
	if !optionalNumberInRange(root, "temperature", 0, 2) {
		return ErrInvalidRequest
	}
	if !optionalNumberInRange(root, "top_p", 0, 1) {
		return ErrInvalidRequest
	}
	if !validResponsesToolChoice(root["tool_choice"]) {
		return ErrInvalidRequest
	}
	if !validResponsesTools(root["tools"]) {
		return ErrInvalidRequest
	}
	return nil
}

func validResponsesInput(v any) bool {
	if s, ok := v.(string); ok {
		return s != ""
	}
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	for _, item := range items {
		if _, ok := item.(map[string]any); !ok {
			return false
		}
	}
	return true
}

func validResponsesMetadata(v any) bool {
	if v == nil {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok || !onlyFields(m, fieldSet("user_id")) {
		return false
	}
	uid, ok := m["user_id"]
	return !ok || isString(uid)
}

func validResponsesReasoning(v any) bool {
	if v == nil {
		return true
	}
	r, ok := v.(map[string]any)
	if !ok || !onlyFields(r, fieldSet("effort", "summary")) {
		return false
	}
	if effort, ok := r["effort"]; ok {
		s, ok := effort.(string)
		if !ok {
			return false
		}
		switch s {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return false
		}
	}
	if summary, ok := r["summary"]; ok {
		s, ok := summary.(string)
		if !ok {
			return false
		}
		switch s {
		case "auto", "detailed", "none", "concise":
		default:
			return false
		}
	}
	return true
}

func validResponsesToolChoice(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return s == "auto" || s == "none" || s == "required"
	}
	choice, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if stringVal(choice["type"]) == "function" && isString(choice["name"]) {
		return true
	}
	if stringVal(choice["type"]) == "custom" && isString(choice["name"]) {
		return true
	}
	return false
}

func validResponsesTools(v any) bool {
	if v == nil {
		return true
	}
	tools, ok := v.([]any)
	if !ok {
		return false
	}
	for _, t := range tools {
		tool, ok := t.(map[string]any)
		if !ok {
			return false
		}
		typ := stringVal(tool["type"])
		switch typ {
		case "function":
			if !onlyFields(tool, fieldSet("type", "name", "description", "parameters", "strict")) {
				return false
			}
			if !isString(tool["name"]) || stringVal(tool["name"]) == "" {
				return false
			}
			if !optionalString(tool, "description") || !optionalBool(tool, "strict") {
				return false
			}
			if params, ok := tool["parameters"]; ok {
				if _, ok := params.(map[string]any); !ok {
					return false
				}
			}
		case "custom":
			if !onlyFields(tool, fieldSet("type", "name", "description", "parameters")) {
				return false
			}
			if !isString(tool["name"]) || stringVal(tool["name"]) == "" {
				return false
			}
			if !optionalString(tool, "description") {
				return false
			}
			if params, ok := tool["parameters"]; ok {
				if _, ok := params.(map[string]any); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// ── Request: OpenAI Chat → Responses ───────────────────────────────────────

func convertRequestOpenAIToResponses(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if err := validateOpenAIChatRequest(root); err != nil {
		return nil, ErrInvalidRequest
	}
	out := convertOpenAIChatPayloadToResponses(root)
	return json.Marshal(out)
}

// convertOpenAIChatPayloadToResponses builds a Responses request payload from
// a validated Chat request map. model is preserved verbatim.
func convertOpenAIChatPayloadToResponses(payload map[string]any) map[string]any {
	converted := map[string]any{"model": payload["model"], "input": []map[string]any{}}
	for _, key := range []string{"temperature", "top_p", "stream", "metadata", "parallel_tool_calls"} {
		if value, ok := payload[key]; ok {
			converted[key] = value
		}
	}
	if tokens := positiveJSONInt(payload["max_tokens"]); tokens > 0 {
		converted["max_output_tokens"] = tokens
	}
	if tokens := positiveJSONInt(payload["max_completion_tokens"]); tokens > 0 {
		converted["max_output_tokens"] = tokens
	}
	if stop, ok := payload["stop"]; ok {
		converted["stop"] = stop
	}
	if effort := stringVal(payload["reasoning_effort"]); effort != "" {
		converted["reasoning"] = map[string]any{"effort": effort}
	}
	if user, ok := payload["user"]; ok {
		converted["metadata"] = map[string]any{"user_id": user}
	}
	if tools := convertOpenAIChatToolsToResponses(payload["tools"]); len(tools) > 0 {
		converted["tools"] = tools
	}
	if toolChoice := convertOpenAIChatToolChoiceToResponses(payload["tool_choice"]); toolChoice != nil {
		converted["tool_choice"] = toolChoice
	}

	input := []map[string]any{}
	for _, item := range asSlice(payload["messages"]) {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := message["role"].(string)
		switch role {
		case "system", "developer", "user", "assistant":
			text := messageContentToText(message["content"])
			if text != "" || role != "assistant" || len(asSlice(message["tool_calls"])) == 0 {
				input = append(input, map[string]any{"role": role, "content": text})
			}
			if role == "assistant" {
				for _, toolCall := range asSlice(message["tool_calls"]) {
					if converted := openAIChatToolCallToResponsesInput(toolCall); converted != nil {
						input = append(input, converted)
					}
				}
			}
		case "tool":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": stringVal(message["tool_call_id"]),
				"output":  messageContentToText(message["content"]),
			})
		}
	}
	converted["input"] = input
	return converted
}

func openAIChatToolCallToResponsesInput(value any) map[string]any {
	toolCall, ok := value.(map[string]any)
	if !ok || stringVal(toolCall["type"]) != "function" {
		return nil
	}
	function, ok := toolCall["function"].(map[string]any)
	if !ok {
		return nil
	}
	callID := stringVal(toolCall["id"])
	name := stringVal(function["name"])
	if callID == "" || name == "" {
		return nil
	}
	return map[string]any{
		"type":      "function_call",
		"id":        callID,
		"call_id":   callID,
		"name":      name,
		"arguments": jsonString(function["arguments"]),
	}
}

// convertOpenAIChatToolsToResponses maps Chat function tools to Responses
// function tools. Non-function tools are deep-cloned verbatim.
func convertOpenAIChatToolsToResponses(value any) []map[string]any {
	tools := []map[string]any{}
	for _, item := range asSlice(value) {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if toolType, _ := tool["type"].(string); toolType != "function" {
			tools = append(tools, cloneValue(tool).(map[string]any))
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		name := stringVal(function["name"])
		if name == "" {
			continue
		}
		converted := map[string]any{"type": "function", "name": name}
		if description := stringVal(function["description"]); description != "" {
			converted["description"] = description
		}
		if parameters, ok := function["parameters"]; ok {
			converted["parameters"] = parameters
		} else {
			converted["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if strict, ok := function["strict"]; ok {
			converted["strict"] = strict
		}
		tools = append(tools, converted)
	}
	return tools
}

func convertOpenAIChatToolChoiceToResponses(value any) any {
	if value == nil {
		return nil
	}
	if s, ok := value.(string); ok {
		return s
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if stringVal(choice["type"]) == "function" {
		if function, ok := choice["function"].(map[string]any); ok && stringVal(function["name"]) != "" {
			return map[string]any{"type": "function", "name": stringVal(function["name"])}
		}
	}
	return nil
}

// ── Request: Responses → OpenAI Chat ───────────────────────────────────────

func convertRequestResponsesToOpenAI(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if err := validateResponsesRequest(root); err != nil {
		return nil, ErrInvalidRequest
	}
	return convertResponsesPayloadToOpenAIChatBytes(root), nil
}

// convertResponsesPayloadToOpenAIChatBytes marshals the Chat payload built by
// convertResponsesPayloadToOpenAIChat.
func convertResponsesPayloadToOpenAIChatBytes(payload map[string]any) []byte {
	out, _ := json.Marshal(convertResponsesPayloadToOpenAIChat(payload))
	return out
}

// convertResponsesPayloadToOpenAIChat builds a Chat request payload from a
// validated Responses request map. instructions → system message; input →
// messages (with tool-call/tool-output pairing); tools/tool_choice mapped.
func convertResponsesPayloadToOpenAIChat(payload map[string]any) map[string]any {
	messages := []map[string]any{}
	if instructions := messageContentToText(payload["instructions"]); instructions != "" {
		messages = append(messages, map[string]any{"role": "system", "content": instructions})
	}
	messages = append(messages, responsesInputToOpenAIMessages(payload["input"])...)
	converted := map[string]any{"model": payload["model"], "messages": messages}
	for _, key := range []string{"temperature", "top_p", "stream"} {
		if value, ok := payload[key]; ok {
			converted[key] = value
		}
	}
	if tokens := positiveJSONInt(payload["max_output_tokens"]); tokens > 0 {
		converted["max_tokens"] = tokens
	}
	if stop, ok := payload["stop"]; ok {
		converted["stop"] = stop
	}
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		if effort := stringVal(reasoning["effort"]); effort != "" {
			converted["reasoning_effort"] = effort
		}
	}
	if tools := convertResponsesToolsToOpenAIChat(payload["tools"]); len(tools) > 0 {
		converted["tools"] = tools
		if parallelToolCalls, ok := payload["parallel_tool_calls"]; ok {
			converted["parallel_tool_calls"] = parallelToolCalls
		}
		if toolChoice := convertResponsesToolChoiceToOpenAIChat(payload["tool_choice"]); toolChoice != nil {
			converted["tool_choice"] = toolChoice
		}
	}
	if meta, ok := payload["metadata"].(map[string]any); ok {
		if uid, ok := meta["user_id"]; ok {
			converted["user"] = uid
		}
	}
	return converted
}

// responsesInputToOpenAIMessages converts a Responses input (string or array
// of items) into Chat messages. It pairs function_call/function_call_output
// items by call_id into an assistant tool_calls message followed by tool
// messages, and coalesces adjacent reasoning + assistant text into one
// assistant message carrying reasoning_content.
func responsesInputToOpenAIMessages(input any) []map[string]any {
	switch typed := input.(type) {
	case string:
		return []map[string]any{{"role": "user", "content": typed}}
	case []any:
		messages := []map[string]any{}
		toolCalls := []map[string]any{}
		toolOutputs := []map[string]any{}
		toolCallIDs := map[string]bool{}
		toolOutputIDs := map[string]bool{}
		pendingReasoning := []string{}
		pendingAssistantContent := ""
		pendingAssistantMessage := false

		pendingReasoningContent := func() string {
			return strings.Join(pendingReasoning, "\n")
		}
		clearPendingAssistant := func() {
			pendingReasoning = nil
			pendingAssistantContent = ""
			pendingAssistantMessage = false
		}
		flushToolBlock := func() {
			if len(toolCalls) == 0 {
				toolOutputs = nil
				toolCallIDs = map[string]bool{}
				toolOutputIDs = map[string]bool{}
				return
			}
			pairedCalls := []map[string]any{}
			pairedCallIDs := map[string]bool{}
			for _, toolCall := range toolCalls {
				callID := stringVal(toolCall["id"])
				if callID == "" || !toolOutputIDs[callID] {
					continue
				}
				pairedCalls = append(pairedCalls, toolCall)
				pairedCallIDs[callID] = true
			}
			if len(pairedCalls) > 0 {
				assistant := map[string]any{"role": "assistant", "content": nil, "tool_calls": pairedCalls}
				if pendingAssistantMessage {
					assistant["content"] = pendingAssistantContent
				}
				if reasoning := pendingReasoningContent(); reasoning != "" {
					assistant["reasoning_content"] = reasoning
				}
				messages = append(messages, assistant)
				for _, toolOutput := range toolOutputs {
					callID := stringVal(toolOutput["tool_call_id"])
					if pairedCallIDs[callID] {
						messages = append(messages, toolOutput)
					}
				}
			}
			toolCalls = nil
			toolOutputs = nil
			toolCallIDs = map[string]bool{}
			toolOutputIDs = map[string]bool{}
			clearPendingAssistant()
		}
		flushAssistantMessage := func() {
			if !pendingAssistantMessage && len(pendingReasoning) == 0 {
				return
			}
			assistant := map[string]any{"role": "assistant", "content": pendingAssistantContent}
			if reasoning := pendingReasoningContent(); reasoning != "" {
				assistant["reasoning_content"] = reasoning
			}
			messages = append(messages, assistant)
			clearPendingAssistant()
		}
		flushPending := func() {
			flushToolBlock()
			flushAssistantMessage()
		}

		for _, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entryType, _ := entry["type"].(string)
			switch entryType {
			case "reasoning":
				if len(toolOutputs) > 0 {
					flushToolBlock()
				}
				if reasoning := responsesReasoningContent(entry); reasoning != "" {
					pendingReasoning = append(pendingReasoning, reasoning)
				}
			case "function_call_output", "custom_tool_call_output":
				callID := stringVal(entry["call_id"])
				if callID != "" && toolCallIDs[callID] && !toolOutputIDs[callID] {
					toolOutputs = append(toolOutputs, map[string]any{
						"role":         "tool",
						"tool_call_id": callID,
						"content":      messageContentToText(entry["output"]),
					})
					toolOutputIDs[callID] = true
				}
			case "function_call":
				callID := stringVal(entry["call_id"])
				if callID == "" {
					callID = stringVal(entry["id"])
				}
				name := stringVal(entry["name"])
				if callID == "" || name == "" {
					continue
				}
				if len(toolOutputs) > 0 {
					flushToolBlock()
				}
				if toolCallIDs[callID] {
					continue
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": jsonString(entry["arguments"]),
					},
				})
				toolCallIDs[callID] = true
			case "custom_tool_call":
				callID := stringVal(entry["call_id"])
				if callID == "" {
					callID = stringVal(entry["id"])
				}
				name := responsesCustomToolWrapperName(stringVal(entry["name"]))
				if callID == "" || name == "" {
					continue
				}
				if len(toolOutputs) > 0 {
					flushToolBlock()
				}
				if toolCallIDs[callID] {
					continue
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": responsesCustomToolArguments(entry["input"]),
					},
				})
				toolCallIDs[callID] = true
			default:
				role, _ := entry["role"].(string)
				if role == "" {
					role = "user"
				}
				if role == "developer" {
					role = "system"
				}
				if role == "assistant" {
					flushToolBlock()
					if pendingAssistantMessage {
						flushAssistantMessage()
					}
					pendingAssistantContent = messageContentToText(entry["content"])
					pendingAssistantMessage = true
					continue
				}
				flushPending()
				messages = append(messages, map[string]any{"role": role, "content": messageContentToText(entry["content"])})
			}
		}
		flushPending()
		return messages
	default:
		return []map[string]any{{"role": "user", "content": ""}}
	}
}

// responsesReasoningContent extracts text from a Responses reasoning item,
// checking native fields then the encrypted_content carrier.
func responsesReasoningContent(entry map[string]any) string {
	for _, key := range []string{"reasoning_content", "content", "text"} {
		if text := messageContentToText(entry[key]); text != "" {
			return text
		}
	}
	if encoded := stringVal(entry["encrypted_content"]); strings.HasPrefix(encoded, responsesReasoningCarrierPrefix) {
		if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, responsesReasoningCarrierPrefix)); err == nil {
			return string(decoded)
		}
	}
	if summary := messageContentToText(entry["summary"]); summary != "" {
		return summary
	}
	return ""
}

// convertResponsesToolsToOpenAIChat maps Responses tools to Chat function
// tools. Responses custom tools become synthetic wrapped function tools.
func convertResponsesToolsToOpenAIChat(value any) []map[string]any {
	tools := []map[string]any{}
	for _, item := range asSlice(value) {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := tool["type"].(string)
		if toolType == "custom" {
			name := responsesCustomToolWrapperName(stringVal(tool["name"]))
			if name == "" {
				continue
			}
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        name,
					"description": responsesCustomToolDescription(tool),
					"parameters":  responsesCustomToolParameters(),
				},
			})
			continue
		}
		if toolType != "function" {
			continue
		}
		name := stringVal(tool["name"])
		if name == "" {
			continue
		}
		function := map[string]any{"name": name}
		if description := stringVal(tool["description"]); description != "" {
			function["description"] = description
		}
		if parameters, ok := tool["parameters"]; ok {
			function["parameters"] = parameters
		} else {
			function["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		if strict, ok := tool["strict"]; ok {
			function["strict"] = strict
		}
		tools = append(tools, map[string]any{"type": "function", "function": function})
	}
	return tools
}

func convertResponsesToolChoiceToOpenAIChat(value any) any {
	if value == nil {
		return nil
	}
	if s, ok := value.(string); ok {
		return s
	}
	choice, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	choiceType, _ := choice["type"].(string)
	name := stringVal(choice["name"])
	if choiceType == "function" && name != "" {
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	}
	if choiceType == "custom" && name != "" {
		return map[string]any{"type": "function", "function": map[string]any{"name": responsesCustomToolWrapperName(name)}}
	}
	return nil
}

// ── Custom tool wrapper helpers ─────────────────────────────────────────────

// responsesCustomToolWrapperName derives a deterministic, OpenAI-valid
// function-tool name that stands in for a Responses custom tool. It is the
// inverse of responsesCustomToolOriginalName.
func responsesCustomToolWrapperName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var safe strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			safe.WriteRune(r)
			continue
		}
		safe.WriteByte('_')
	}
	wrapped := responsesCustomToolNamePrefix + safe.String()
	// Ensure the OpenAI function-name rule (leading alpha). The prefix already
	// starts with 'r', so this is satisfied by construction.
	return wrapped
}

// responsesCustomToolOriginalName reverses responsesCustomToolWrapperName. ok
// is false when name is not a wrapper.
func responsesCustomToolOriginalName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, responsesCustomToolNamePrefix) {
		return "", false
	}
	original := strings.TrimPrefix(name, responsesCustomToolNamePrefix)
	return original, original != ""
}

func responsesCustomToolDescription(tool map[string]any) string {
	description := strings.TrimSpace(stringVal(tool["description"]))
	if description != "" {
		description += "\n\n"
	}
	return description + "This is an OpenAI Responses custom tool downgraded for a function-tool upstream. Put the exact raw custom tool input in the `input` string."
}

func responsesCustomToolParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{"type": "string", "description": "Raw input for the original custom tool."},
		},
		"required":             []string{"input"},
		"additionalProperties": false,
	}
}

// responsesCustomToolArguments encodes a custom-tool input value as the JSON
// arguments string for the synthetic wrapper function.
func responsesCustomToolArguments(input any) string {
	encoded, err := json.Marshal(map[string]any{"input": messageContentToText(input)})
	if err != nil {
		return `{"input":""}`
	}
	return string(encoded)
}

// responsesCustomToolInput recovers the raw custom-tool input string from a
// wrapper function's arguments.
func responsesCustomToolInput(arguments any) string {
	text := jsonString(arguments)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		if input, ok := parsed["input"].(string); ok {
			return input
		}
	}
	return text
}

// ── Response validation ───────────────────────────────────────────────────

// validOpenAIChatResponse validates only the bounded Chat completion subset
// consumed by this converter. A successful conversion must never manufacture a
// response from an arbitrary JSON object.
func validOpenAIChatResponse(root map[string]any) bool {
	if !isString(root["id"]) || !isString(root["object"]) || !isString(root["model"]) {
		return false
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 || len(choices) > 16 {
		return false
	}
	for _, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok || !onlyFields(choice, fieldSet("index", "message", "finish_reason", "logprobs")) {
			return false
		}
		if _, ok := choice["index"].(json.Number); !ok {
			return false
		}
		if choice["finish_reason"] != nil && !isString(choice["finish_reason"]) {
			return false
		}
		message, ok := choice["message"].(map[string]any)
		if !ok || !onlyFields(message, fieldSet("role", "content", "reasoning_content", "tool_calls", "function_call")) || stringVal(message["role"]) != "assistant" {
			return false
		}
		if content, exists := message["content"]; exists && content != nil && !validOpenAIContent(content) {
			return false
		}
		if !optionalString(message, "reasoning_content") {
			return false
		}
		if calls, exists := message["tool_calls"]; exists && !validateOpenAIToolCalls(calls) {
			return false
		}
		if function, exists := message["function_call"]; exists {
			call, ok := function.(map[string]any)
			if !ok || !onlyFields(call, fieldSet("name", "arguments")) || !isString(call["name"]) || !isString(call["arguments"]) {
				return false
			}
		}
	}
	return validOpenAIResponseUsage(root["usage"])
}

func validOpenAIResponseUsage(value any) bool {
	usage, ok := value.(map[string]any)
	if !ok || !onlyFields(usage, fieldSet("prompt_tokens", "completion_tokens", "total_tokens", "prompt_tokens_details", "completion_tokens_details")) {
		return false
	}
	prompt, ok := usage["prompt_tokens"].(json.Number)
	if !ok || positiveJSONInt(prompt) > 1_000_000 {
		return false
	}
	completion, ok := usage["completion_tokens"].(json.Number)
	if !ok || positiveJSONInt(completion) > 1_000_000 {
		return false
	}
	total, ok := usage["total_tokens"].(json.Number)
	return ok && positiveJSONInt(total) <= 1_000_000 && positiveJSONInt(prompt)+positiveJSONInt(completion) == positiveJSONInt(total)
}

// validResponsesResponse accepts the bounded Responses success subset used by
// this converter, including custom-tool output items which are intentionally
// outside the executor's provider-native response contract but are needed for
// lossless cross-protocol conversion.
func validResponsesResponse(root map[string]any) bool {
	if !isString(root["id"]) || stringVal(root["id"]) == "" || stringVal(root["object"]) != "response" || !isString(root["status"]) || !isString(root["model"]) {
		return false
	}
	output, ok := root["output"].([]any)
	if !ok || len(output) > 1024 || !validResponsesUsage(root["usage"]) {
		return false
	}
	for _, value := range output {
		item, ok := value.(map[string]any)
		if !ok || !validResponsesOutputItem(item) {
			return false
		}
	}
	return true
}

func validResponsesUsage(value any) bool {
	usage, ok := value.(map[string]any)
	if !ok {
		return false
	}
	input, ok := usage["input_tokens"].(json.Number)
	if !ok || positiveJSONInt(input) > 1_000_000 {
		return false
	}
	output, ok := usage["output_tokens"].(json.Number)
	if !ok || positiveJSONInt(output) > 1_000_000 {
		return false
	}
	total, ok := usage["total_tokens"].(json.Number)
	return ok && positiveJSONInt(total) <= 1_000_000 && positiveJSONInt(input)+positiveJSONInt(output) == positiveJSONInt(total)
}

func validResponsesOutputItem(item map[string]any) bool {
	typ := stringVal(item["type"])
	if !isString(item["id"]) || stringVal(item["id"]) == "" {
		return false
	}
	switch typ {
	case "message":
		if !onlyFields(item, fieldSet("id", "type", "status", "role", "content")) || !isString(item["role"]) {
			return false
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) > 256 {
			return false
		}
		for _, value := range content {
			part, ok := value.(map[string]any)
			if !ok || !onlyFields(part, fieldSet("type", "text", "annotations")) || stringVal(part["type"]) != "output_text" || !isString(part["text"]) {
				return false
			}
		}
		return true
	case "reasoning":
		return onlyFields(item, fieldSet("id", "type", "status", "summary", "reasoning_content", "content", "text", "encrypted_content")) && optionalString(item, "reasoning_content") && optionalString(item, "encrypted_content")
	case "function_call":
		return onlyFields(item, fieldSet("id", "type", "status", "call_id", "name", "arguments")) && isString(item["call_id"]) && isString(item["name"]) && isString(item["arguments"])
	case "custom_tool_call":
		return onlyFields(item, fieldSet("id", "type", "status", "call_id", "name", "input")) && isString(item["call_id"]) && isString(item["name"]) && isString(item["input"])
	default:
		return false
	}
}

// ── Response: OpenAI Chat → Responses ───────────────────────────────────────

func convertResponseOpenAIToResponses(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil || !validOpenAIChatResponse(root) {
		return nil, ErrInvalidResponse
	}
	return convertOpenAIChatResponseToResponses(root), nil
}

// convertOpenAIChatResponseToResponses builds a Responses success response
// from a Chat completion response map. model is taken from the body.
func convertOpenAIChatResponseToResponses(root map[string]any) []byte {
	var response struct {
		ID      string `json:"id"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          any                 `json:"content"`
				ReasoningContent string              `json:"reasoning_content"`
				ToolCalls        []openAIToolCall    `json:"tool_calls"`
				FunctionCall     *openAIFunctionCall `json:"function_call"`
			} `json:"message"`
		} `json:"choices"`
	}
	// Re-marshal the strictly parsed root into the typed struct. json.Number
	// values encode fine; decoding into string fields is lossless for the
	// fields we read.
	raw, _ := json.Marshal(root)
	_ = json.Unmarshal(raw, &response)

	usage := parseOpenAIUsage(raw)
	output := []map[string]any{}
	outputText := ""
	status := "completed"
	var incompleteDetails any
	if len(response.Choices) > 0 {
		choice := response.Choices[0]
		status, incompleteDetails = responsesStatusFromOpenAIFinishReason(choice.FinishReason)
		outputText = messageContentToText(choice.Message.Content)
		if reasoningContent := strings.TrimSpace(choice.Message.ReasoningContent); reasoningContent != "" {
			output = append(output, responsesReasoningOutputItem("rs_converted", reasoningContent))
		}
		content := []map[string]any{{"type": "output_text", "text": outputText, "annotations": []any{}}}
		output = append(output, map[string]any{
			"id":      "msg_converted",
			"type":    "message",
			"status":  statusForOutputItem(status),
			"role":    "assistant",
			"content": content,
		})
		for _, toolCall := range choice.Message.ToolCalls {
			output = append(output, openAIChatToolCallToResponsesOutput(toolCall.ID, toolCall.ID, toolCall.Function.Name, toolCall.Function.Arguments))
		}
		if choice.Message.FunctionCall != nil && choice.Message.FunctionCall.Name != "" {
			callID := syntheticOpenAIFunctionCallID(choice.Message.FunctionCall.Name)
			output = append(output, openAIChatToolCallToResponsesOutput(callID, callID, choice.Message.FunctionCall.Name, choice.Message.FunctionCall.Arguments))
		}
	}
	id := response.ID
	if id == "" {
		id = "resp_converted"
	}
	converted := map[string]any{
		"id":                 id,
		"object":             "response",
		"created_at":         time.Now().Unix(),
		"status":             status,
		"error":              nil,
		"incomplete_details": incompleteDetails,
		"model":              root["model"],
		"output":             output,
		"output_text":        outputText,
		"usage":              responsesUsageMap(usage),
	}
	b, _ := json.Marshal(converted)
	return b
}

// openAIChatToolCallToResponsesOutput builds a Responses function_call (or, for
// wrapped custom-tool names, custom_tool_call) output item from a Chat tool call.
func openAIChatToolCallToResponsesOutput(id, callID, name, arguments string) map[string]any {
	if customName, ok := responsesCustomToolOriginalName(name); ok {
		return map[string]any{
			"id":      id,
			"type":    "custom_tool_call",
			"status":  "completed",
			"call_id": callID,
			"name":    customName,
			"input":   responsesCustomToolInput(arguments),
		}
	}
	return map[string]any{
		"id":        id,
		"type":      "function_call",
		"status":    "completed",
		"call_id":   callID,
		"name":      name,
		"arguments": arguments,
	}
}

func responsesReasoningOutputItem(id, reasoningContent string) map[string]any {
	if strings.TrimSpace(id) == "" {
		id = "rs_converted"
	}
	return map[string]any{
		"id":                id,
		"type":              "reasoning",
		"status":            "completed",
		"summary":           []any{},
		"encrypted_content": responsesReasoningCarrierPrefix + base64.RawURLEncoding.EncodeToString([]byte(reasoningContent)),
	}
}

func responsesStatusFromOpenAIFinishReason(finishReason string) (string, any) {
	switch finishReason {
	case "length":
		return "incomplete", map[string]any{"reason": "max_output_tokens"}
	default:
		return "completed", nil
	}
}

// ── Response: Responses → OpenAI Chat ───────────────────────────────────────

func convertResponseResponsesToOpenAI(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil || !validResponsesResponse(root) {
		return nil, ErrInvalidResponse
	}
	return convertResponsesResponseToOpenAIChat(root), nil
}

// convertResponsesResponseToOpenAIChat builds a Chat completion response from a
// Responses success response map. model is taken from the body.
func convertResponsesResponseToOpenAIChat(root map[string]any) []byte {
	var response struct {
		ID     string `json:"id"`
		Output []struct {
			ID               string `json:"id"`
			Type             string `json:"type"`
			Role             string `json:"role"`
			Content          any    `json:"content"`
			Text             any    `json:"text"`
			Summary          any    `json:"summary"`
			ReasoningContent string `json:"reasoning_content"`
			EncryptedContent string `json:"encrypted_content"`
			CallID           string `json:"call_id"`
			Name             string `json:"name"`
			Arguments        any    `json:"arguments"`
			Input            any    `json:"input"`
		} `json:"output"`
		OutputText string `json:"output_text"`
	}
	raw, _ := json.Marshal(root)
	_ = json.Unmarshal(raw, &response)

	textParts := []string{}
	reasoningParts := []string{}
	toolCalls := []map[string]any{}
	for _, output := range response.Output {
		switch output.Type {
		case "reasoning":
			if output.ReasoningContent != "" {
				reasoningParts = append(reasoningParts, output.ReasoningContent)
			} else if strings.HasPrefix(output.EncryptedContent, responsesReasoningCarrierPrefix) {
				if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(output.EncryptedContent, responsesReasoningCarrierPrefix)); err == nil {
					reasoningParts = append(reasoningParts, string(decoded))
				}
			} else if text := messageContentToText(output.Content); text != "" {
				reasoningParts = append(reasoningParts, text)
			} else if text := messageContentToText(output.Text); text != "" {
				reasoningParts = append(reasoningParts, text)
			} else if text := messageContentToText(output.Summary); text != "" {
				reasoningParts = append(reasoningParts, text)
			}
		case "message":
			if text := messageContentToText(output.Content); text != "" {
				textParts = append(textParts, text)
			}
		case "function_call":
			id := output.CallID
			if id == "" {
				id = output.ID
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      output.Name,
					"arguments": jsonString(output.Arguments),
				},
			})
		case "custom_tool_call":
			id := output.CallID
			if id == "" {
				id = output.ID
			}
			name := responsesCustomToolWrapperName(output.Name)
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": responsesCustomToolArguments(output.Input),
				},
			})
		}
	}
	if len(textParts) == 0 && response.OutputText != "" {
		textParts = append(textParts, response.OutputText)
	}
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	usage := parseOpenAIUsage(raw)
	usageBody := map[string]int64{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	if usage != nil {
		usageBody = map[string]int64{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		}
	}
	id := response.ID
	if id == "" {
		id = "chatcmpl_converted"
	}
	message := map[string]any{"role": "assistant", "content": strings.Join(textParts, "")}
	if reasoning := strings.Join(reasoningParts, "\n"); reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	converted := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   root["model"],
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
		"usage": usageBody,
	}
	b, _ := json.Marshal(converted)
	return b
}

// ── Request/Response composition for Anthropic ↔ Responses ──────────────────
//
// These reuse the existing, exhaustively tested Chat↔Anthropic converters so
// the Anthropic leg has a single source of truth.

func convertRequestResponsesToAnthropic(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if err := validateResponsesRequest(root); err != nil {
		return nil, ErrInvalidRequest
	}
	chatBody := convertResponsesPayloadToOpenAIChatBytes(root)
	return convertRequestOpenAIToAnthropic(chatBody)
}

func convertRequestAnthropicToResponses(body []byte) ([]byte, error) {
	chatBody, err := convertRequestAnthropicToOpenAI(body)
	if err != nil {
		return nil, err
	}
	root, err := parseStrictJSON(chatBody)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	// The Chat body was already validated by convertRequestAnthropicToOpenAI;
	// re-validate the Chat shape before the Responses builder for safety.
	if err := validateOpenAIChatRequest(root); err != nil {
		return nil, ErrInvalidRequest
	}
	out := convertOpenAIChatPayloadToResponses(root)
	return json.Marshal(out)
}

func convertResponseResponsesToAnthropic(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil || !validResponsesResponse(root) {
		return nil, ErrInvalidResponse
	}
	chatBody := convertResponsesResponseToOpenAIChat(root)
	return convertResponseOpenAIToAnthropic(chatBody)
}

func convertResponseAnthropicToResponses(body []byte) ([]byte, error) {
	chatBody, err := convertResponseAnthropicToOpenAI(body)
	if err != nil {
		return nil, err
	}
	root, err := parseStrictJSON(chatBody)
	if err != nil || !validOpenAIChatResponse(root) {
		return nil, ErrInvalidResponse
	}
	return convertOpenAIChatResponseToResponses(root), nil
}
