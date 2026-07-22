package executorv1api

import (
	"context"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// NormalizeOpenAIResponses validates the Responses request before
// execution/quota reservation and retains its original captured JSON bytes.
func NormalizeOpenAIResponses(ctx context.Context, requestID string) (NonStreamRequest, error) {
	result, err := NormalizeOpenAIResponsesRequest(ctx, requestID)
	if err != nil {
		return NonStreamRequest{}, err
	}
	if result.Stream {
		return NonStreamRequest{}, ErrStreamingUnsupported
	}
	return result.Request, nil
}

// NormalizeOpenAIResponsesRequest validates a Responses request exactly once
// and returns its normalized request plus whether the caller selected streaming mode.
func NormalizeOpenAIResponsesRequest(ctx context.Context, requestID string) (NormalizedRequest, error) {
	return normalize(ctx, requestID, adapter.ProtocolOpenAIResponses, validateResponseRequest, normalizeResponseThinking)
}

// DetectOpenAIResponsesStream performs only the bounded structural JSON gate
// on the captured raw body, then reads the optional stream flag.
func DetectOpenAIResponsesStream(ctx context.Context) (bool, error) {
	return detectStreamFlag(ctx)
}

func validateResponseRequest(root map[string]any) error {
	if !onlyFields(root, responsesRootFields) {
		return ErrInvalidRequest
	}
	if !isString(root["model"]) {
		return ErrInvalidRequest
	}
	if !validResponsesInput(root["input"]) {
		return ErrInvalidRequest
	}
	if !optionalString(root, "instructions") {
		return ErrInvalidRequest
	}
	if !optionalPositiveInteger(root, "max_output_tokens") {
		return ErrInvalidRequest
	}
	if !validResponsesMetadata(root["metadata"]) {
		return ErrInvalidRequest
	}
	if !validResponsesReasoning(root["reasoning"]) {
		return ErrInvalidRequest
	}
	if stream, exists := root["stream"]; exists && !isBool(stream) {
		return ErrInvalidRequest
	}
	if !optionalNumberIn(root, "temperature", 0, 2) {
		return ErrInvalidRequest
	}
	if !validResponsesText(root["text"]) {
		return ErrInvalidRequest
	}
	if !validResponsesToolChoice(root["tool_choice"]) {
		return ErrInvalidRequest
	}
	if !validResponsesTools(root["tools"]) {
		return ErrInvalidRequest
	}
	if !optionalNumberIn(root, "top_p", 0, 1) {
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
		if !validResponsesInputItem(item) {
			return false
		}
	}
	return true
}

func validResponsesInputItem(v any) bool {
	item, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if typ, ok := item["type"].(string); !ok || typ != "message" {
		return false
	}
	if !onlyFields(item, makeFieldSet("type", "role", "content")) {
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
		if !validResponsesContent(content) {
			return false
		}
	}
	return true
}

func validResponsesContent(v any) bool {
	if _, ok := v.(string); ok {
		return true
	}
	parts, ok := v.([]any)
	if !ok {
		return false
	}
	for _, part := range parts {
		if !validResponsesContentPart(part) {
			return false
		}
	}
	return true
}

func validResponsesContentPart(v any) bool {
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
		return onlyFields(part, makeFieldSet("type", "text")) && optionalString(part, "text")
	case "input_image":
		return onlyFields(part, makeFieldSet("type", "image_url")) && optionalString(part, "image_url")
	case "output_text":
		return onlyFields(part, makeFieldSet("type", "text")) && optionalString(part, "text")
	default:
		return false
	}
}

func validResponsesMetadata(v any) bool {
	if v == nil {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok || !onlyFields(m, makeFieldSet("user_id")) {
		return false
	}
	return optionalString(m, "user_id")
}

func validResponsesReasoning(v any) bool {
	if v == nil {
		return true
	}
	r, ok := v.(map[string]any)
	if !ok || !onlyFields(r, makeFieldSet("effort", "summary")) {
		return false
	}
	if effort, exists := r["effort"]; exists {
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
	if summary, exists := r["summary"]; exists {
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

func validResponsesText(v any) bool {
	if v == nil {
		return true
	}
	t, ok := v.(map[string]any)
	if !ok || !onlyFields(t, makeFieldSet("format")) {
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

func validResponsesToolChoice(v any) bool {
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
		if typ, ok := tool["type"].(string); !ok || typ != "function" {
			return false
		}
		if !onlyFields(tool, makeFieldSet("type", "name", "description", "parameters", "strict")) {
			return false
		}
		name, ok := tool["name"].(string)
		if !ok || name == "" {
			return false
		}
		if !optionalString(tool, "description") {
			return false
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

// normalizeResponseThinking maps the validated reasoning effort to a
// ThinkingRequest, paralleling normalizeChatThinking.
func normalizeResponseThinking(root map[string]any) (adapter.ThinkingRequest, error) {
	reasoning, exists := root["reasoning"]
	if !exists || reasoning == nil {
		return adapter.ThinkingRequest{}, nil
	}
	r, ok := reasoning.(map[string]any)
	if !ok {
		return adapter.ThinkingRequest{}, nil
	}
	effort, exists := r["effort"]
	if !exists {
		return adapter.ThinkingRequest{}, nil
	}
	s, ok := effort.(string)
	if !ok {
		return adapter.ThinkingRequest{}, nil
	}
	if s == string(adapter.ThinkingNone) {
		return adapter.ThinkingRequest{}, nil
	}
	return adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingEffort(s)}, nil
}
