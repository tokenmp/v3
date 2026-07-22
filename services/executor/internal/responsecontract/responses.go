// Package responsecontract owns the provider-neutral semantic boundary for the
// OpenAI Responses API request and success response shapes. Parsing, provider
// calls, routing, and HTTP rendering remain outside this package.
//
// This package enforces stateless-only execution: fields that imply server-side
// state (previous_response_id, conversation, store, background, include,
// moderation, prompt, truncation, service_tier) are explicitly rejected by
// ValidateRequest through a strict field allowlist.
package responsecontract

import (
	"encoding/json"
	"unicode/utf8"
)

const (
	// MaxWireResponseBytes is the hard cap on a Responses success response.
	MaxWireResponseBytes = 16 << 20 // 16 MiB

	maxInputStringLength    = 1 << 20
	maxMetadataUserID       = 256
	maxToolNameBytes        = 128
	maxToolDescriptionBytes = 512
	maxReasoningEffortLen   = 16
	maxReasoningSummaryLen  = 16
	maxTextInputBytes       = 1 << 20
	maxImageURLBytes        = 16 << 10
	maxOutputItems          = 1024
	maxOutputContentParts   = 256
	maxUsageTokenCap        = 1_000_000
	maxExtensionValueBytes  = 64 << 10
)

// ValidateRequest validates a strictly parsed Responses request object. It
// enforces the stateless subset: model (required), input (required), and the
// optional instructions, max_output_tokens, metadata, reasoning, stream,
// temperature, text, tool_choice, tools, top_p fields. All stateful/built-in
// fields are explicitly rejected through the field allowlist.
func ValidateRequest(r map[string]any) bool {
	if !onlyFields(r, requestFieldSet) {
		return false
	}
	model, ok := r["model"].(string)
	if !ok || model == "" {
		return false
	}
	if !validInput(r["input"]) {
		return false
	}
	if !optionalString(r["instructions"]) {
		return false
	}
	if !optionalPositiveInteger(r["max_output_tokens"]) {
		return false
	}
	if !validMetadata(r["metadata"]) {
		return false
	}
	if !validReasoning(r["reasoning"]) {
		return false
	}
	if stream, exists := r["stream"]; exists && !isBool(stream) {
		return false
	}
	if !optionalNumberIn(r["temperature"], 0, 2) {
		return false
	}
	if !validText(r["text"]) {
		return false
	}
	if !validToolChoice(r["tool_choice"]) {
		return false
	}
	if !validTools(r["tools"]) {
		return false
	}
	if !optionalNumberIn(r["top_p"], 0, 1) {
		return false
	}
	return true
}

// ValidateResponse validates a strictly parsed Responses success response. It
// performs bounded structural verification and rejects responses exceeding the
// wire cap, malformed usage, or unknown-shape output items.
func ValidateResponse(r map[string]any) bool {
	if len(r) > 64 {
		return false
	}
	if !boundedExtensions(r, responseFieldSet) {
		return false
	}
	id, ok := r["id"].(string)
	if !ok || id == "" {
		return false
	}
	if obj, ok := r["object"].(string); !ok || obj != "response" {
		return false
	}
	status, ok := r["status"].(string)
	if !ok {
		return false
	}
	switch status {
	case "completed", "failed", "in_progress", "cancelled", "incomplete", "queued":
	default:
		return false
	}
	output, ok := r["output"].([]any)
	if !ok || len(output) > maxOutputItems {
		return false
	}
	for _, item := range output {
		if !validOutputItem(item) {
			return false
		}
	}
	if !validResponseUsage(r["usage"]) {
		return false
	}
	if model, exists := r["model"]; exists {
		if _, ok := model.(string); !ok {
			return false
		}
	}
	if ca, exists := r["created_at"]; exists {
		if !nonnegativeInteger(ca) {
			return false
		}
	}
	return true
}

// ── Request validation ──────────────────────────────────────────────────

var requestFieldSet = fieldSet(
	"model", "input", "instructions", "max_output_tokens", "metadata",
	"reasoning", "stream", "temperature", "text", "tool_choice", "tools", "top_p",
)

func validInput(v any) bool {
	if s, ok := v.(string); ok {
		return len(s) <= maxInputStringLength
	}
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return false
	}
	for _, item := range items {
		if !validInputItem(item) {
			return false
		}
	}
	return true
}

func validInputItem(v any) bool {
	item, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if typ, ok := item["type"].(string); !ok || typ != "message" {
		return false
	}
	if !onlyFields(item, fieldSet("type", "role", "content")) {
		return false
	}
	if role, exists := item["role"]; exists {
		r, ok := role.(string)
		if !ok {
			return false
		}
		switch r {
		case "user", "system", "developer", "assistant":
		default:
			return false
		}
	}
	if content, exists := item["content"]; exists {
		if !validContent(content) {
			return false
		}
	}
	return true
}

func validContent(v any) bool {
	if s, ok := v.(string); ok {
		return len(s) <= maxInputStringLength
	}
	parts, ok := v.([]any)
	if !ok {
		return false
	}
	for _, part := range parts {
		if !validContentPart(part) {
			return false
		}
	}
	return true
}

func validContentPart(v any) bool {
	part, ok := v.(map[string]any)
	if !ok {
		return false
	}
	typ, ok := part["type"].(string)
	if !ok {
		return false
	}
	switch typ {
	case "input_text":
		if !onlyFields(part, fieldSet("type", "text")) {
			return false
		}
		text, ok := part["text"].(string)
		if !ok || len(text) > maxTextInputBytes {
			return false
		}
	case "input_image":
		if !onlyFields(part, fieldSet("type", "image_url")) {
			return false
		}
		url, ok := part["image_url"].(string)
		if !ok || len(url) > maxImageURLBytes {
			return false
		}
	case "output_text":
		if !onlyFields(part, fieldSet("type", "text")) {
			return false
		}
		text, ok := part["text"].(string)
		if !ok || len(text) > maxTextInputBytes {
			return false
		}
	default:
		return false
	}
	return true
}

func validMetadata(v any) bool {
	if v == nil {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok || !onlyFields(m, fieldSet("user_id")) {
		return false
	}
	if uid, exists := m["user_id"]; exists {
		s, ok := uid.(string)
		if !ok || len(s) > maxMetadataUserID || hasCTL(s) {
			return false
		}
	}
	return true
}

func validReasoning(v any) bool {
	if v == nil {
		return true
	}
	r, ok := v.(map[string]any)
	if !ok || !onlyFields(r, fieldSet("effort", "summary")) {
		return false
	}
	if effort, exists := r["effort"]; exists {
		s, ok := effort.(string)
		if !ok || len(s) > maxReasoningEffortLen {
			return false
		}
		switch s {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return false
		}
	}
	if summary, exists := r["summary"]; exists {
		s, ok := summary.(string)
		if !ok || len(s) > maxReasoningSummaryLen {
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

func validText(v any) bool {
	if v == nil {
		return true
	}
	t, ok := v.(map[string]any)
	if !ok || !onlyFields(t, fieldSet("format")) {
		return false
	}
	if format, exists := t["format"]; exists {
		f, ok := format.(map[string]any)
		if !ok {
			return false
		}
		for key := range f {
			switch key {
			case "name", "schema", "type":
			default:
				return false
			}
		}
	}
	return true
}

func validToolChoice(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	if !ok {
		return false
	}
	switch s {
	case "auto", "none", "required":
		return true
	default:
		return false
	}
}

func validTools(v any) bool {
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
		if typ, ok := tool["type"].(string); !ok || typ != "function" {
			return false
		}
		if !onlyFields(tool, fieldSet("type", "name", "description", "parameters", "strict")) {
			return false
		}
		name, ok := tool["name"].(string)
		if !ok || name == "" || len(name) > maxToolNameBytes {
			return false
		}
		if desc, exists := tool["description"]; exists {
			s, ok := desc.(string)
			if !ok || len(s) > maxToolDescriptionBytes {
				return false
			}
		}
		params, ok := tool["parameters"].(map[string]any)
		if !ok || params == nil {
			return false
		}
		if strict, exists := tool["strict"]; exists {
			if _, ok := strict.(bool); !ok {
				return false
			}
		}
	}
	return true
}

// ── Response validation ─────────────────────────────────────────────────

var responseFieldSet = fieldSet(
	"id", "object", "status", "output", "usage", "model", "created_at",
	"error", "incomplete_details", "instructions", "metadata",
	"parallel_tool_calls", "temperature", "tool_choice", "tools", "top_p",
	"background", "completed_at", "conversation", "max_output_tokens",
	"max_tool_calls", "moderation", "previous_response_id", "prompt",
	"prompt_cache_key", "prompt_cache_options", "prompt_cache_retention",
	"reasoning", "safety_identifier", "service_tier", "text", "top_logprobs",
	"truncation", "user",
)

func validOutputItem(v any) bool {
	item, ok := v.(map[string]any)
	if !ok {
		return false
	}
	typ, ok := item["type"].(string)
	if !ok {
		return false
	}
	switch typ {
	case "message", "function_call", "reasoning":
	default:
		return false
	}
	for _, key := range []string{"id", "name", "arguments", "call_id"} {
		if val, exists := item[key]; exists {
			if _, ok := val.(string); !ok {
				return false
			}
		}
	}
	if role, exists := item["role"]; exists {
		if _, ok := role.(string); !ok {
			return false
		}
	}
	if status, exists := item["status"]; exists {
		if _, ok := status.(string); !ok {
			return false
		}
	}
	if content, exists := item["content"]; exists {
		parts, ok := content.([]any)
		if !ok || len(parts) > maxOutputContentParts {
			return false
		}
		for _, part := range parts {
			if !validOutputContentPart(part) {
				return false
			}
		}
	}
	if summary, exists := item["summary"]; exists {
		if _, ok := summary.([]any); !ok {
			return false
		}
	}
	return true
}

func validOutputContentPart(v any) bool {
	part, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if _, ok := part["type"].(string); !ok {
		return false
	}
	if text, exists := part["text"]; exists {
		if _, ok := text.(string); !ok {
			return false
		}
	}
	if ann, exists := part["annotations"]; exists {
		if _, ok := ann.([]any); !ok {
			return false
		}
	}
	return true
}

func validResponseUsage(v any) bool {
	u, ok := v.(map[string]any)
	if !ok {
		return false
	}
	inputTokens, ok1 := nonnegativeInt64(u["input_tokens"])
	outputTokens, ok2 := nonnegativeInt64(u["output_tokens"])
	totalTokens, ok3 := nonnegativeInt64(u["total_tokens"])
	if !ok1 || !ok2 || !ok3 {
		return false
	}
	if inputTokens > maxUsageTokenCap || outputTokens > maxUsageTokenCap || totalTokens > maxUsageTokenCap {
		return false
	}
	if inputTokens+outputTokens != totalTokens {
		return false
	}
	return true
}

// ── Shared helpers ─────────────────────────────────────────────────────

func fieldSet(names ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(names))
	for _, n := range names {
		s[n] = struct{}{}
	}
	return s
}

func onlyFields(o map[string]any, allowed map[string]struct{}) bool {
	for k := range o {
		if _, ok := allowed[k]; !ok {
			return false
		}
	}
	return true
}

func isBool(v any) bool { _, ok := v.(bool); return ok }

func hasCTL(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func optionalString(v any) bool {
	if v == nil {
		return true
	}
	_, ok := v.(string)
	return ok
}

func optionalNumberIn(v any, min, max float64) bool {
	if v == nil {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	parsed, err := n.Float64()
	return err == nil && parsed >= min && parsed <= max
}

func optionalPositiveInteger(v any) bool {
	if v == nil {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	parsed, err := n.Int64()
	return err == nil && parsed >= 1
}

func nonnegativeInteger(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	parsed, err := n.Int64()
	return err == nil && parsed >= 0
}

func nonnegativeInt64(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := n.Int64()
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func boundedExtensions(o map[string]any, known map[string]struct{}) bool {
	for key, value := range o {
		if _, ok := known[key]; !ok && jsonValueSize(value, maxExtensionValueBytes) > maxExtensionValueBytes {
			return false
		}
	}
	return true
}

func jsonValueSize(value any, limit int) int {
	var size func(any, int) int
	size = func(v any, used int) int {
		if used > limit {
			return used
		}
		switch x := v.(type) {
		case nil:
			return used + 4
		case bool:
			if x {
				return used + 4
			}
			return used + 5
		case json.Number:
			return used + len(x)
		case string:
			used += 2
			for _, r := range x {
				if r < 0x20 || r == '"' || r == '\\' || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029 {
					used += 6
				} else {
					used += utf8.RuneLen(r)
				}
				if used > limit {
					return used
				}
			}
			return used
		case []any:
			used++
			for i, item := range x {
				if i > 0 {
					used++
				}
				used = size(item, used)
				if used > limit {
					return used
				}
			}
			return used + 1
		case map[string]any:
			used++
			for key, item := range x {
				if used > limit {
					return used
				}
				if used > 1 {
					used++
				}
				used = size(key, used) + 1
				used = size(item, used)
			}
			return used + 1
		default:
			return limit + 1
		}
	}
	return size(value, 0)
}
