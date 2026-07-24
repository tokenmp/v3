package openaiadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

const (
	maxParamBodyBytes = 2 << 20
	maxJSONDepth      = 64
	maxJSONNodes      = 10_000
)

var (
	errInvalidChatParams = errors.New("openaiadapter: request body failed strict chat validation")
	chatRootFields       = fieldSet(
		"model", "messages", "stream", "temperature", "top_p", "max_tokens",
		"max_completion_tokens", "reasoning_effort", "stop", "tools", "tool_choice",
		"response_format", "user",
	)
)

// decodeChatParams first parses and validates the whole JSON tree. The SDK is
// deliberately only the final typed decode: its union unmarshallers do not form
// this service's input security boundary. Effective thinking is execution-
// authoritative: unsupported thinking is omitted, and a supported effective
// effort replaces any caller-provided reasoning_effort.
func decodeChatParams(ctx context.Context, body []byte, effectiveThinking adapter.EffectiveThinking) (openai.ChatCompletionNewParams, error) {
	var params openai.ChatCompletionNewParams
	if len(body) == 0 || len(body) > maxParamBodyBytes || !utf8.Valid(body) {
		return params, errInvalidChatParams
	}
	value, err := parseStrictJSON(ctx, body)
	root, ok := value.(map[string]any)
	if err != nil || !ok || validateChatRequest(root) != nil || reconcileEffectiveThinking(root, effectiveThinking) != nil {
		return params, errInvalidChatParams
	}
	canonical, err := json.Marshal(root)
	if err != nil || json.Unmarshal(canonical, &params) != nil || len(params.Messages) == 0 {
		return params, errInvalidChatParams
	}
	return params, nil
}

func reconcileEffectiveThinking(root map[string]any, effective adapter.EffectiveThinking) error {
	switch effort := effective.EffectiveEffort; {
	case effort == "" || effort == adapter.ThinkingNone:
		delete(root, "reasoning_effort")
	case !effort.Valid():
		return errInvalidChatParams
	default:
		root["reasoning_effort"] = string(effort)
	}
	return nil
}

// parseStrictJSON rejects duplicate keys at every object depth, unsafe
// prototype-family names, excessive nesting and excessive JSON nodes.
func parseStrictJSON(ctx context.Context, body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	nodes := 0
	value, err := parseJSONValue(ctx, dec, 1, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON content")
	}
	return value, nil
}

func parseJSONValue(ctx context.Context, dec *json.Decoder, depth int, nodes *int) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > maxJSONDepth {
		return nil, errors.New("JSON nesting limit exceeded")
	}
	*nodes++
	if *nodes > maxJSONNodes {
		return nil, errors.New("JSON node limit exceeded")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch token := tok.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := make(map[string]any)
			for dec.More() {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok || isForbiddenName(key) {
					return nil, errors.New("unsafe JSON object key")
				}
				if _, exists := object[key]; exists {
					return nil, errors.New("duplicate JSON object key")
				}
				child, err := parseJSONValue(ctx, dec, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return nil, errors.New("unterminated JSON object")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for dec.More() {
				child, err := parseJSONValue(ctx, dec, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return nil, errors.New("unterminated JSON array")
			}
			return array, nil
		default:
			return nil, errors.New("invalid JSON delimiter")
		}
	case string, bool, nil, json.Number:
		return token, nil
	default:
		return nil, errors.New("invalid JSON token")
	}
}

func validateChatRequest(value any) error {
	root, ok := value.(map[string]any)
	if !ok || !onlyFields(root, chatRootFields) || !isString(root["model"]) {
		return errInvalidChatParams
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return errInvalidChatParams
	}
	for _, item := range messages {
		if err := validateMessage(item); err != nil {
			return err
		}
	}
	if stream, exists := root["stream"]; exists && !isBool(stream) ||
		!optionalNumberIn(root, "temperature", 0, 2) ||
		!optionalNumberIn(root, "top_p", 0, 1) ||
		!optionalPositiveInteger(root, "max_tokens") ||
		!optionalPositiveInteger(root, "max_completion_tokens") ||
		!optionalEnum(root, "reasoning_effort", "none", "minimal", "low", "medium", "high", "xhigh", "max") ||
		!validStop(root) ||
		!optionalString(root, "user") ||
		!validTools(root) ||
		!validToolChoice(root) ||
		!validResponseFormat(root) {
		return errInvalidChatParams
	}
	return nil
}

func validateMessage(value any) error {
	message, ok := value.(map[string]any)
	if !ok {
		return errInvalidChatParams
	}
	role, ok := message["role"].(string)
	if !ok || !validContent(message["content"]) || !optionalString(message, "name") {
		return errInvalidChatParams
	}
	switch role {
	case "system", "user":
		if !onlyFields(message, fieldSet("role", "content", "name")) {
			return errInvalidChatParams
		}
	case "assistant":
		if !onlyFields(message, fieldSet("role", "content", "name", "tool_calls", "reasoning_content")) ||
			!optionalString(message, "reasoning_content") {
			return errInvalidChatParams
		}
		if toolCalls, exists := message["tool_calls"]; exists && !validToolCalls(toolCalls) {
			return errInvalidChatParams
		}
	case "tool":
		if !onlyFields(message, fieldSet("role", "content", "tool_call_id")) || !isString(message["tool_call_id"]) {
			return errInvalidChatParams
		}
	default:
		return errInvalidChatParams
	}
	return nil
}

func validContent(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	parts, ok := value.([]any)
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

func validContentPart(value any) bool {
	part, ok := value.(map[string]any)
	if !ok {
		return false
	}
	kind, ok := part["type"].(string)
	if !ok {
		return false
	}
	switch kind {
	case "text":
		return onlyFields(part, fieldSet("type", "text")) && isString(part["text"])
	case "image_url":
		image, ok := part["image_url"].(map[string]any)
		if !onlyFields(part, fieldSet("type", "image_url")) || !ok || !onlyFields(image, fieldSet("url", "detail")) || !isURI(image["url"]) {
			return false
		}
		return optionalEnum(image, "detail", "auto", "low", "high")
	default:
		return false
	}
}

func validTools(root map[string]any) bool {
	value, exists := root["tools"]
	if !exists {
		return true
	}
	tools, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		function, functionOK := tool["function"].(map[string]any)
		if !ok || !onlyFields(tool, fieldSet("type", "function")) || tool["type"] != "function" || !functionOK ||
			!onlyFields(function, fieldSet("name", "description", "parameters", "strict")) || !isString(function["name"]) ||
			!optionalString(function, "description") || !optionalBool(function, "strict") {
			return false
		}
		parameters, ok := function["parameters"].(map[string]any)
		if !ok || parameters == nil {
			return false
		}
	}
	return true
}

func validToolChoice(root map[string]any) bool {
	value, exists := root["tool_choice"]
	if !exists {
		return true
	}
	if choice, ok := value.(string); ok {
		return choice == "none" || choice == "auto" || choice == "required"
	}
	choice, ok := value.(map[string]any)
	function, functionOK := choice["function"].(map[string]any)
	return ok && onlyFields(choice, fieldSet("type", "function")) && choice["type"] == "function" && functionOK &&
		onlyFields(function, fieldSet("name")) && isString(function["name"])
}

func validToolCalls(value any) bool {
	calls, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range calls {
		call, ok := value.(map[string]any)
		function, functionOK := call["function"].(map[string]any)
		if !ok || !onlyFields(call, fieldSet("id", "type", "function")) || !isString(call["id"]) || call["type"] != "function" || !functionOK ||
			!onlyFields(function, fieldSet("name", "arguments")) || !isString(function["name"]) || !isString(function["arguments"]) {
			return false
		}
	}
	return true
}

func validResponseFormat(root map[string]any) bool {
	value, exists := root["response_format"]
	if !exists {
		return true
	}
	format, ok := value.(map[string]any)
	return ok && onlyFields(format, fieldSet("type")) && optionalEnum(format, "type", "text", "json_object", "json_schema")
}

func validStop(root map[string]any) bool {
	value, exists := root["stop"]
	if !exists {
		return true
	}
	if isString(value) {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !isString(item) {
			return false
		}
	}
	return true
}

func fieldSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func onlyFields(value map[string]any, allowed map[string]struct{}) bool {
	for name := range value {
		if _, ok := allowed[name]; !ok {
			return false
		}
	}
	return true
}

func isString(value any) bool { _, ok := value.(string); return ok }
func isBool(value any) bool   { _, ok := value.(bool); return ok }

func isURI(value any) bool {
	s, ok := value.(string)
	if !ok || s == "" {
		return false
	}
	u, err := url.ParseRequestURI(s)
	return err == nil && u.IsAbs()
}

func optionalString(value map[string]any, name string) bool {
	item, exists := value[name]
	return !exists || isString(item)
}

func optionalBool(value map[string]any, name string) bool {
	item, exists := value[name]
	return !exists || isBool(item)
}

func optionalEnum(value map[string]any, name string, allowed ...string) bool {
	item, exists := value[name]
	if !exists {
		return true
	}
	text, ok := item.(string)
	if !ok {
		return false
	}
	for _, option := range allowed {
		if text == option {
			return true
		}
	}
	return false
}

func optionalNumberIn(value map[string]any, name string, min, max float64) bool {
	item, exists := value[name]
	if !exists {
		return true
	}
	number, ok := item.(json.Number)
	if !ok {
		return false
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	return err == nil && parsed >= min && parsed <= max
}

func optionalPositiveInteger(value map[string]any, name string) bool {
	item, exists := value[name]
	if !exists {
		return true
	}
	number, ok := item.(json.Number)
	if !ok {
		return false
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	return err == nil && parsed >= 1
}

func isForbiddenName(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}
