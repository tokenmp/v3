package anthropicadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

var errInvalidMessageResponse = errors.New("anthropicadapter: response failed strict message validation")

// validateMessageResponse verifies the raw, successful Messages API response
// before it crosses the SDK boundary. The SDK's response unions deliberately
// accept fields added by Anthropic; this adapter instead accepts exactly the
// response shape promised by the Executor contract.
func validateMessageResponse(raw []byte) error {
	if len(raw) == 0 || len(raw) > maxParamBodyBytes || !utf8.Valid(raw) {
		return errInvalidMessageResponse
	}
	value, err := parseStrictJSON(context.Background(), raw)
	if err != nil {
		return errInvalidMessageResponse
	}
	root, ok := value.(map[string]any)
	if !ok || !validMessageResponse(root) {
		return errInvalidMessageResponse
	}
	return nil
}

func validMessageResponse(root map[string]any) bool {
	if !requiredString(root, "id") ||
		root["type"] != "message" ||
		root["role"] != "assistant" ||
		!requiredString(root, "model") ||
		!nullableEnum(root, "stop_reason", "end_turn", "max_tokens", "stop_sequence", "tool_use") ||
		!nullableString(root, "stop_sequence") {
		return false
	}
	content, ok := root["content"].([]any)
	if !ok || !validResponseUsage(root["usage"]) {
		return false
	}
	for _, value := range content {
		if !validResponseBlock(value) {
			return false
		}
	}
	return true
}

func validResponseUsage(value any) bool {
	usage, ok := value.(map[string]any)
	if !ok || !requiredInteger(usage, "input_tokens") || !requiredInteger(usage, "output_tokens") {
		return false
	}
	for _, name := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if value, exists := usage[name]; exists && !jsonInteger(value) {
			return false
		}
	}
	return true
}

func validResponseBlock(value any) bool {
	block, ok := value.(map[string]any)
	if !ok {
		return false
	}
	switch block["type"] {
	case "text":
		return only(block, fields("type", "text")) && requiredString(block, "text")
	case "tool_use":
		input, ok := block["input"].(map[string]any)
		return only(block, fields("type", "id", "name", "input")) && requiredString(block, "id") && requiredString(block, "name") && ok && input != nil
	case "thinking":
		return only(block, fields("type", "thinking", "signature")) && requiredString(block, "thinking") && requiredString(block, "signature")
	default:
		return false
	}
}

func requiredString(value map[string]any, name string) bool {
	item, exists := value[name]
	return exists && stringValue(item)
}

func nullableString(value map[string]any, name string) bool {
	item, exists := value[name]
	return !exists || item == nil || stringValue(item)
}

func nullableEnum(value map[string]any, name string, allowed ...string) bool {
	item, exists := value[name]
	if !exists || item == nil {
		return exists
	}
	text, ok := item.(string)
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if text == candidate {
			return true
		}
	}
	return false
}

func requiredInteger(value map[string]any, name string) bool {
	item, exists := value[name]
	return exists && jsonInteger(item)
}

func jsonInteger(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := strconv.ParseInt(number.String(), 10, 64)
	return err == nil
}
