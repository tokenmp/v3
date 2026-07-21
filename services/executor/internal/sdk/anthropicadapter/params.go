package anthropicadapter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

const (
	maxParamBodyBytes = 2 << 20
	maxJSONDepth      = 64
	maxJSONNodes      = 10_000
)

var (
	errInvalidMessageParams = errors.New("anthropicadapter: request body failed strict message validation")
	messageRootFields       = fields("model", "messages", "max_tokens", "system", "thinking", "stream", "temperature", "top_p", "top_k", "stop_sequences", "tools", "tool_choice", "metadata")
)

// decodeMessageParams establishes the provider request boundary. It validates
// the entire JSON tree before the SDK's permissive union decoders run, then
// rebuilds model and thinking from execution-authoritative values.
func decodeMessageParams(body []byte, effectiveThinking adapter.EffectiveThinking, targetModel string) (anthropic.MessageNewParams, error) {
	var params anthropic.MessageNewParams
	if len(body) == 0 || len(body) > maxParamBodyBytes || !utf8.Valid(body) || strings.TrimSpace(targetModel) == "" {
		return params, errInvalidMessageParams
	}
	value, err := parseStrictJSON(context.Background(), body)
	if err != nil {
		return params, errInvalidMessageParams
	}
	root, ok := value.(map[string]any)
	if !ok || validateMessageRequest(root) != nil || reconcileAuthoritative(root, effectiveThinking, targetModel) != nil || normalizeSDKUnions(root) != nil {
		return params, errInvalidMessageParams
	}
	canonical, err := json.Marshal(root)
	if err != nil || json.Unmarshal(canonical, &params) != nil || len(params.Messages) == 0 {
		return anthropic.MessageNewParams{}, errInvalidMessageParams
	}
	return params, nil
}

func reconcileAuthoritative(root map[string]any, effective adapter.EffectiveThinking, targetModel string) error {
	root["model"] = targetModel
	thinking, present := root["thinking"].(map[string]any)
	if _, exists := root["thinking"]; exists && !present {
		return errInvalidMessageParams
	}
	if effective.EffectiveBudget == 0 {
		if present && thinking["type"] == "enabled" {
			return errInvalidMessageParams
		}
		delete(root, "thinking")
		return nil
	}
	if effective.EffectiveBudget < 1024 || effective.EffectiveBudget >= integer(root["max_tokens"]) {
		return errInvalidMessageParams
	}
	if present {
		if thinking["type"] != "enabled" || integer(thinking["budget_tokens"]) != effective.EffectiveBudget {
			return errInvalidMessageParams
		}
	}
	rebuilt := map[string]any{"type": "enabled", "budget_tokens": json.Number(strconv.FormatInt(int64(effective.EffectiveBudget), 10))}
	if present {
		if display, exists := thinking["display"]; exists {
			rebuilt["display"] = display
		}
	}
	root["thinking"] = rebuilt
	return nil
}

// normalizeSDKUnions converts contract-permitted string shorthands into the
// SDK's array-only parameter representation after strict validation.
func normalizeSDKUnions(root map[string]any) error {
	if system, ok := root["system"].(string); ok {
		root["system"] = []any{map[string]any{"type": "text", "text": system}}
	}
	messages := root["messages"].([]any)
	for _, value := range messages {
		message := value.(map[string]any)
		if content, ok := message["content"].(string); ok {
			message["content"] = []any{map[string]any{"type": "text", "text": content}}
		}
		if err := normalizeContent(message["content"]); err != nil {
			return err
		}
	}
	return nil
}

func normalizeContent(value any) error {
	blocks, ok := value.([]any)
	if !ok {
		return errInvalidMessageParams
	}
	for _, value := range blocks {
		block := value.(map[string]any)
		if block["type"] == "tool_result" {
			if content, ok := block["content"].(string); ok {
				block["content"] = []any{map[string]any{"type": "text", "text": content}}
			}
			if err := normalizeContent(block["content"]); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseStrictJSON is provider-independent structural validation. It rejects
// duplicate keys and prototype-family names at every depth before semantic
// validation, and enforces bounded resource use.
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
	if err := ctx.Err(); err != nil || depth > maxJSONDepth {
		return nil, errors.New("invalid JSON depth or context")
	}
	*nodes++
	if *nodes > maxJSONNodes {
		return nil, errors.New("JSON node limit exceeded")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := map[string]any{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok || forbiddenKey(key) {
					return nil, errors.New("unsafe JSON key")
				}
				if _, exists := object[key]; exists {
					return nil, errors.New("duplicate JSON key")
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
			array := []any{}
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
		}
	case string, bool, nil, json.Number:
		return token, nil
	}
	return nil, errors.New("invalid JSON token")
}

func validateMessageRequest(root map[string]any) error {
	if !only(root, messageRootFields) || !stringValue(root["model"]) || !positive(root["max_tokens"]) {
		return errInvalidMessageParams
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return errInvalidMessageParams
	}
	for _, message := range messages {
		if !validMessage(message) {
			return errInvalidMessageParams
		}
	}
	if stream, exists := root["stream"]; exists && stream != false ||
		!optionalNumber(root, "temperature", 0, 1) || !optionalNumber(root, "top_p", 0, 1) || !optionalPositive(root, "top_k") ||
		!validSystem(root) || !validThinking(root) || !stringsArray(root["stop_sequences"], has(root, "stop_sequences")) ||
		!validTools(root) || !validToolChoice(root) || !validMetadata(root) {
		return errInvalidMessageParams
	}
	return nil
}

func validSystem(root map[string]any) bool {
	value, exists := root["system"]
	if !exists {
		return true
	}
	if stringValue(value) {
		return true
	}
	blocks, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok || !only(block, fields("type", "text", "cache_control")) || block["type"] != "text" || !stringValue(block["text"]) || !validCache(block) {
			return false
		}
	}
	return true
}

func validThinking(root map[string]any) bool {
	value, exists := root["thinking"]
	if !exists {
		return true
	}
	thinking, ok := value.(map[string]any)
	if !ok || !only(thinking, fields("type", "budget_tokens", "display")) {
		return false
	}
	switch thinking["type"] {
	case "disabled":
		_, hasBudget := thinking["budget_tokens"]
		return !hasBudget && optionalEnum(thinking, "display", "summarized", "omitted")
	case "enabled":
		budget := integer(thinking["budget_tokens"])
		return budget >= 1024 && budget < integer(root["max_tokens"]) && optionalEnum(thinking, "display", "summarized", "omitted")
	default:
		return false
	}
}

func validMessage(value any) bool {
	message, ok := value.(map[string]any)
	if !ok || !only(message, fields("role", "content")) {
		return false
	}
	if message["role"] != "user" && message["role"] != "assistant" {
		return false
	}
	return validContent(message["content"])
}

func validContent(value any) bool {
	if stringValue(value) {
		return true
	}
	blocks, ok := value.([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		if !validBlock(block) {
			return false
		}
	}
	return true
}

func validBlock(value any) bool {
	block, ok := value.(map[string]any)
	if !ok {
		return false
	}
	switch block["type"] {
	case "text":
		return only(block, fields("type", "text", "cache_control")) && stringValue(block["text"]) && validCache(block)
	case "image":
		source, ok := block["source"].(map[string]any)
		return only(block, fields("type", "source", "cache_control")) && ok && only(source, fields("type", "media_type", "data")) && source["type"] == "base64" && stringValue(source["media_type"]) && validBase64(source["data"]) && validCache(block)
	case "tool_use":
		_, inputOK := block["input"].(map[string]any)
		return only(block, fields("type", "id", "name", "input", "cache_control")) && stringValue(block["id"]) && stringValue(block["name"]) && inputOK && validCache(block)
	case "tool_result":
		return only(block, fields("type", "tool_use_id", "content", "cache_control")) && stringValue(block["tool_use_id"]) && validContent(block["content"]) && validCache(block)
	case "thinking":
		return only(block, fields("type", "thinking", "signature", "cache_control")) && stringValue(block["thinking"]) && stringValue(block["signature"]) && validCache(block)
	default:
		return false
	}
}

func validCache(value map[string]any) bool {
	cache, exists := value["cache_control"]
	if !exists {
		return true
	}
	object, ok := cache.(map[string]any)
	return ok && only(object, fields("type")) && object["type"] == "ephemeral"
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
		if !ok || !only(tool, fields("name", "description", "input_schema", "cache_control")) || !stringValue(tool["name"]) || !optionalString(tool, "description") || !validCache(tool) {
			return false
		}
		if _, ok := tool["input_schema"].(map[string]any); !ok {
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
	choice, ok := value.(map[string]any)
	if !ok {
		return false
	}
	switch choice["type"] {
	case "auto", "any":
		return only(choice, fields("type", "disable_parallel_tool_use")) && optionalBool(choice, "disable_parallel_tool_use")
	case "tool":
		return only(choice, fields("type", "name", "disable_parallel_tool_use")) && stringValue(choice["name"]) && optionalBool(choice, "disable_parallel_tool_use")
	default:
		return false
	}
}

func validMetadata(root map[string]any) bool {
	value, exists := root["metadata"]
	if !exists {
		return true
	}
	metadata, ok := value.(map[string]any)
	return ok && only(metadata, fields("user_id")) && optionalString(metadata, "user_id")
}

func fields(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}
func only(value map[string]any, allowed map[string]struct{}) bool {
	for name := range value {
		if _, ok := allowed[name]; !ok {
			return false
		}
	}
	return true
}
func has(value map[string]any, name string) bool { _, ok := value[name]; return ok }
func stringValue(value any) bool                 { _, ok := value.(string); return ok }
func validBase64(value any) bool {
	data, ok := value.(string)
	if !ok {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(data)
	return err == nil
}
func optionalString(value map[string]any, name string) bool {
	item, exists := value[name]
	return !exists || stringValue(item)
}
func optionalBool(value map[string]any, name string) bool {
	item, exists := value[name]
	if !exists {
		return true
	}
	_, ok := item.(bool)
	return ok
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
	for _, candidate := range allowed {
		if text == candidate {
			return true
		}
	}
	return false
}
func stringsArray(value any, present bool) bool {
	if !present {
		return true
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if !stringValue(item) {
			return false
		}
	}
	return true
}
func positive(value any) bool { return integer(value) >= 1 }
func optionalPositive(value map[string]any, name string) bool {
	item, exists := value[name]
	return !exists || positive(item)
}
func integer(value any) int {
	number, ok := value.(json.Number)
	if !ok {
		return 0
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || parsed < 1 || parsed > int64(^uint(0)>>1) {
		return 0
	}
	return int(parsed)
}
func optionalNumber(value map[string]any, name string, min, max float64) bool {
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
func forbiddenKey(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}
