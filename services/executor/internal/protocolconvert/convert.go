// Package protocolconvert provides transport-neutral, pure-function protocol
// conversion between OpenAI Chat and Anthropic Messages API shapes. It performs
// no I/O, holds no global state, and never exposes secrets, credentials, or
// raw error details. All conversions are deterministic JSON→JSON
// transformations; StreamState is caller-owned per-stream state.
//
// Supported conversions:
//   - OpenAI Chat ↔ Anthropic Messages: request, response, and stream chunks
//
// Thinking/reasoning fields are converted between their protocol-native shapes.
package protocolconvert

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── Errors ──────────────────────────────────────────────────────────────────

var (
	ErrUnsupportedConversion = errors.New("protocolconvert: unsupported protocol conversion")
	ErrInvalidRequest        = errors.New("protocolconvert: invalid request body")
	ErrInvalidResponse       = errors.New("protocolconvert: invalid response body")
	ErrInvalidStreamChunk    = errors.New("protocolconvert: invalid stream chunk")
)

// ── Constants ───────────────────────────────────────────────────────────────

const (
	maxBodyBytes     = 2 << 20 // 2 MiB
	maxJSONDepth     = 64
	maxJSONNodes     = 10_000
	defaultMaxTokens = 4096
)

// defaultEffortBudgets are conservative protocolconvert defaults. The
// conversion layer has no adapter-engine EffectiveThinking; runtime callers
// that require an exact budget must override this value in the Runner layer.
var defaultEffortBudgets = map[string]int64{
	"minimal": 1024,
	"low":     2048,
	"medium":  8192,
	"high":    16384,
	"xhigh":   32768,
	"max":     65536,
}

// ── Supported conversion check ──────────────────────────────────────────────

// supportedConversion reports whether protocolconvert implements the
// (fromProtocol → toProtocol) conversion for at least one of request,
// response, and streaming. The supported cross-protocol pairs are:
//   - OpenAI Chat ↔ Anthropic Messages (request, response, streaming)
//   - OpenAI Chat ↔ OpenAI Responses (request, response, streaming)
//   - Anthropic Messages ↔ OpenAI Responses (request, response, streaming;
//     composed through Chat)
//
// Images is intentionally unsupported. Same-protocol is never a conversion.
func supportedConversion(from, to adapter.Protocol) bool {
	if from == to {
		return false
	}
	for _, p := range []adapter.Protocol{adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses} {
		if from != p && to != p {
			continue
		}
	}
	// Either side must be one of the three convertible protocols, and the
	// other side must also be convertible (excluding Images).
	if !isConvertibleProtocol(from) || !isConvertibleProtocol(to) {
		return false
	}
	return true
}

// isConvertibleProtocol reports whether p participates in any conversion.
func isConvertibleProtocol(p adapter.Protocol) bool {
	switch p {
	case adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIResponses:
		return true
	}
	return false
}

// ── Strict JSON parser (shared, duplicate-key/prototype-safe) ───────────────

func parseStrictJSON(body []byte) (map[string]any, error) {
	if len(body) == 0 || len(body) > maxBodyBytes || !utf8.Valid(body) {
		return nil, errors.New("invalid body")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	nodes := 0
	value, err := parseJSONValue(dec, 1, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON content")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("root must be an object")
	}
	return root, nil
}

func parseJSONValue(dec *json.Decoder, depth int, nodes *int) (any, error) {
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
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok || isForbiddenKey(key) {
					return nil, errors.New("unsafe JSON key")
				}
				if _, exists := object[key]; exists {
					return nil, errors.New("duplicate JSON key")
				}
				child, err := parseJSONValue(dec, depth+1, nodes)
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
				child, err := parseJSONValue(dec, depth+1, nodes)
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

func isForbiddenKey(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}

// ── JSON helpers ────────────────────────────────────────────────────────────

func isString(v any) bool                      { _, ok := v.(string); return ok }
func isBool(v any) bool                        { _, ok := v.(bool); return ok }
func stringVal(v any) string                   { s, _ := v.(string); return s }
func intVal(v any) int64                       { n, _ := v.(json.Number); i, _ := n.Int64(); return i }
func floatVal(v any) float64                   { n, _ := v.(json.Number); f, _ := n.Float64(); return f }
func hasField(m map[string]any, k string) bool { _, ok := m[k]; return ok }

func onlyFields(m map[string]any, allowed map[string]struct{}) bool {
	for k := range m {
		if _, ok := allowed[k]; !ok {
			return false
		}
	}
	return true
}

func fieldSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func optionalString(m map[string]any, k string) bool {
	v, ok := m[k]
	return !ok || isString(v)
}

func optionalBool(m map[string]any, k string) bool {
	v, ok := m[k]
	return !ok || isBool(v)
}

func optionalPositiveInt(m map[string]any, k string) bool {
	v, ok := m[k]
	if !ok {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i >= 1
}

func optionalNonNegativeInt(m map[string]any, k string) bool {
	v, ok := m[k]
	if !ok {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i >= 0
}

func optionalNumberInRange(m map[string]any, k string, min, max float64) bool {
	v, ok := m[k]
	if !ok {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	f, err := n.Float64()
	return err == nil && f >= min && f <= max
}

// ── ConvertRequest ──────────────────────────────────────────────────────────

// ConvertRequest converts a request body from one protocol to another.
// Supported: OpenAI Chat ↔ Anthropic Messages, OpenAI Chat ↔ OpenAI
// Responses, and Anthropic Messages ↔ OpenAI Responses (composed through
// Chat). The model field is preserved as-is; the Runner layer replaces it
// with the target upstream model.
func ConvertRequest(reqBody []byte, fromProtocol, toProtocol adapter.Protocol) ([]byte, error) {
	if !supportedConversion(fromProtocol, toProtocol) {
		return nil, ErrUnsupportedConversion
	}
	switch {
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolOpenAIChat:
		return convertRequestResponsesToOpenAI(reqBody)
	case fromProtocol == adapter.ProtocolOpenAIChat && toProtocol == adapter.ProtocolOpenAIResponses:
		return convertRequestOpenAIToResponses(reqBody)
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolAnthropic:
		return convertRequestResponsesToAnthropic(reqBody)
	case fromProtocol == adapter.ProtocolAnthropic && toProtocol == adapter.ProtocolOpenAIResponses:
		return convertRequestAnthropicToResponses(reqBody)
	case fromProtocol == adapter.ProtocolOpenAIChat:
		return convertRequestOpenAIToAnthropic(reqBody)
	default:
		return convertRequestAnthropicToOpenAI(reqBody)
	}
}

// ── ConvertResponse ─────────────────────────────────────────────────────────

// ConvertResponse converts a non-streaming response body from one protocol to
// another. Supported: OpenAI Chat ↔ Anthropic Messages, OpenAI Chat ↔ OpenAI
// Responses, and Anthropic Messages ↔ OpenAI Responses (composed through Chat).
func ConvertResponse(respBody []byte, fromProtocol, toProtocol adapter.Protocol) ([]byte, error) {
	if !supportedConversion(fromProtocol, toProtocol) {
		return nil, ErrUnsupportedConversion
	}
	switch {
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolOpenAIChat:
		return convertResponseResponsesToOpenAI(respBody)
	case fromProtocol == adapter.ProtocolOpenAIChat && toProtocol == adapter.ProtocolOpenAIResponses:
		return convertResponseOpenAIToResponses(respBody)
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolAnthropic:
		return convertResponseResponsesToAnthropic(respBody)
	case fromProtocol == adapter.ProtocolAnthropic && toProtocol == adapter.ProtocolOpenAIResponses:
		return convertResponseAnthropicToResponses(respBody)
	case fromProtocol == adapter.ProtocolOpenAIChat:
		return convertResponseOpenAIToAnthropic(respBody)
	default:
		return convertResponseAnthropicToOpenAI(respBody)
	}
}

// ── StreamState ─────────────────────────────────────────────────────────────

// StreamState holds per-stream conversion state for ConvertStreamChunk.
// The caller owns and passes a pointer to the same StreamState for every chunk
// in one stream. It must not be reused across streams.
type StreamState struct {
	// OpenAI→Anthropic state
	OAIStarted         bool
	OAIBlockIndex      int
	OAIToolCallIndex   int
	OAIFinishReason    string
	OAIModel           string
	OAIMessageID       string
	OAIUsage           streamUsageAccum
	OAIThinkingStarted bool
	OAIThinkingStopped bool
	OAIThinkingIndex   int

	// Anthropic→OpenAI state
	AntStarted        bool
	AntBlockIndex     int
	AntToolCallIndex  int
	AntMessageID      string
	AntModel          string
	AntFinishReason   string
	AntUsage          streamUsageAccum
	AntRoleAnnounced  bool
	AntThinkingActive bool

	// Responses-inbound state (upstream Chat/Anthropic → inbound Responses).
	// RespStarted records that response.created has been emitted.
	RespStarted         bool
	RespResponseID      string
	RespModel           string
	RespSequence        int
	RespOutputIndex     int
	RespContentIndex    int
	RespMessageItemID   string
	RespTextBlockOpen   bool
	RespReasoningItemID string
	RespReasoningOpen   bool
	RespToolItemByID    map[string]string   // tool call id / index → Responses item id
	RespToolCallByItem  map[string][]string // item id → announced tool names (custom detection)
	RespToolCallsAccum  []map[string]any    // accumulated tool calls for response.completed
	RespTextAccum       string
	RespReasoningText   string
	RespUsage           streamUsageAccum
	RespStatus          string
	RespDone            bool

	// Responses-upstream state (upstream Responses → inbound Chat).
	// RespInCreated records receipt of the mandatory response.created event.
	// Responses lifecycle/output events are rejected before this transition.
	RespInCreated   bool
	RespInStarted   bool
	RespInMessageID string
	RespInModel     string
	RespInDone      bool
	RespInTextAccum string
	RespInToolItems map[string]int   // item_id → Chat tool_calls index
	RespInToolCalls []map[string]any // accumulating tool calls
	RespInReasoning string
	RespInUsage     streamUsageAccum
	RespInFinish    string

	// Composite sub-states for Anthropic ↔ Responses streaming, which is
	// composed through Chat. Each composite direction uses a distinct pair;
	// the unused pair stays nil and inert. They are lazily allocated.
	SubAntToChat  *StreamState // Anthropic → Chat leg (Anthropic → Responses)
	SubChatToResp *StreamState // Chat → Responses leg (Anthropic → Responses)
	SubRespToChat *StreamState // Responses → Chat leg (Responses → Anthropic)
	SubChatToAnt  *StreamState // Chat → Anthropic leg (Responses → Anthropic)
}

type streamUsageAccum struct {
	PromptTokens     int64
	CompletionTokens int64
}

// ── ConvertStreamChunk ──────────────────────────────────────────────────────

// ConvertStreamChunk converts one raw SSE data payload (the bytes after "data:"
// in the SSE frame) between protocol formats. The caller is responsible for
// SSE framing; this function only handles the JSON payload.
//
// For OpenAI→Anthropic: each OpenAI chat.completion.chunk may produce zero or
// more Anthropic events. The returned slice contains the JSON bytes for each
// event (without SSE framing).
//
// For Anthropic→OpenAI: each Anthropic event produces one OpenAI
// chat.completion.chunk JSON object.
//
// When the input is the OpenAI "[DONE]" sentinel, the returned slice contains
// an Anthropic message_stop + message_delta pair (if not already emitted).
// When the input is an Anthropic message_stop event, the returned slice
// contains a final OpenAI chunk with finish_reason.
func ConvertStreamChunk(rawChunk []byte, fromProtocol, toProtocol adapter.Protocol, state *StreamState) ([][]byte, error) {
	if !supportedConversion(fromProtocol, toProtocol) {
		return nil, ErrUnsupportedConversion
	}
	if state == nil {
		return nil, ErrInvalidStreamChunk
	}
	if len(rawChunk) == 0 {
		return nil, nil
	}

	switch {
	case fromProtocol == adapter.ProtocolOpenAIChat && toProtocol == adapter.ProtocolOpenAIResponses:
		return convertStreamChunkOpenAIToResponses(rawChunk, state)
	case fromProtocol == adapter.ProtocolAnthropic && toProtocol == adapter.ProtocolOpenAIResponses:
		return convertStreamChunkAnthropicToResponses(rawChunk, state)
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolOpenAIChat:
		return convertStreamChunkResponsesToOpenAI(rawChunk, state)
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolAnthropic:
		return convertStreamChunkResponsesToAnthropic(rawChunk, state)
	}

	if fromProtocol == adapter.ProtocolOpenAIChat {
		return convertStreamChunkOpenAIToAnthropic(rawChunk, state)
	}
	return convertStreamChunkAnthropicToOpenAI(rawChunk, state)
}

// ── OpenAI Chat → Anthropic Messages: Request ───────────────────────────────

func convertRequestOpenAIToAnthropic(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if err := validateOpenAIChatRequest(root); err != nil {
		return nil, ErrInvalidRequest
	}
	return buildAnthropicRequest(root)
}

var openaiChatRootFields = fieldSet(
	"model", "messages", "stream", "temperature", "top_p",
	"max_tokens", "max_completion_tokens", "reasoning_effort",
	"stop", "tools", "tool_choice", "response_format", "user",
)

func validateOpenAIChatRequest(root map[string]any) error {
	if !onlyFields(root, openaiChatRootFields) || !isString(root["model"]) {
		return ErrInvalidRequest
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return ErrInvalidRequest
	}
	for _, m := range messages {
		if err := validateOpenAIMessage(m); err != nil {
			return err
		}
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
	if !optionalPositiveInt(root, "max_tokens") {
		return ErrInvalidRequest
	}
	if !optionalPositiveInt(root, "max_completion_tokens") {
		return ErrInvalidRequest
	}
	if !optionalOpenAIReasoningEffort(root) {
		return ErrInvalidRequest
	}
	if !validateOpenAIStop(root) {
		return ErrInvalidRequest
	}
	if !validateOpenAITools(root) {
		return ErrInvalidRequest
	}
	if !validateOpenAIToolChoice(root) {
		return ErrInvalidRequest
	}
	if !optionalString(root, "user") {
		return ErrInvalidRequest
	}
	return nil
}

func validateOpenAIMessage(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return ErrInvalidRequest
	}
	role := stringVal(m["role"])
	switch role {
	case "system":
		if !onlyFields(m, fieldSet("role", "content", "name")) || !validOpenAIContent(m["content"]) {
			return ErrInvalidRequest
		}
	case "user":
		if !onlyFields(m, fieldSet("role", "content", "name")) || !validOpenAIContent(m["content"]) {
			return ErrInvalidRequest
		}
	case "assistant":
		if !onlyFields(m, fieldSet("role", "content", "name", "tool_calls", "reasoning_content")) {
			return ErrInvalidRequest
		}
		if !optionalString(m, "reasoning_content") {
			return ErrInvalidRequest
		}
		if content, ok := m["content"]; ok && content != nil && !validOpenAIContent(content) {
			return ErrInvalidRequest
		}
		if tc, ok := m["tool_calls"]; ok && !validateOpenAIToolCalls(tc) {
			return ErrInvalidRequest
		}
	case "tool":
		if !onlyFields(m, fieldSet("role", "content", "tool_call_id")) || !isString(m["tool_call_id"]) {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

func validOpenAIContent(v any) bool {
	if _, ok := v.(string); ok {
		return true
	}
	parts, ok := v.([]any)
	if !ok {
		return false
	}
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			return false
		}
		typ := stringVal(part["type"])
		switch typ {
		case "text":
			if !onlyFields(part, fieldSet("type", "text")) || !isString(part["text"]) {
				return false
			}
		case "image_url":
			if !validOpenAIImageURLPart(part) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validOpenAIImageURLPart(part map[string]any) bool {
	if !onlyFields(part, fieldSet("type", "image_url")) {
		return false
	}
	imageURL, ok := part["image_url"].(map[string]any)
	if !ok || !onlyFields(imageURL, fieldSet("url")) || !isString(imageURL["url"]) {
		return false
	}
	return validImageReference(stringVal(imageURL["url"]))
}

func optionalOpenAIReasoningEffort(root map[string]any) bool {
	v, ok := root["reasoning_effort"]
	if !ok {
		return true
	}
	effort, ok := v.(string)
	if !ok {
		return false
	}
	return effort == "none" || effort == "minimal" || effort == "low" || effort == "medium" || effort == "high" || effort == "xhigh" || effort == "max"
}

func validateOpenAIStop(root map[string]any) bool {
	v, ok := root["stop"]
	if !ok {
		return true
	}
	if isString(v) {
		return true
	}
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range arr {
		if !isString(item) {
			return false
		}
	}
	return true
}

func validateOpenAITools(root map[string]any) bool {
	v, ok := root["tools"]
	if !ok {
		return true
	}
	tools, ok := v.([]any)
	if !ok {
		return false
	}
	for _, t := range tools {
		tool, ok := t.(map[string]any)
		if !ok || !onlyFields(tool, fieldSet("type", "function")) || stringVal(tool["type"]) != "function" {
			return false
		}
		fn, ok := tool["function"].(map[string]any)
		if !ok {
			return false
		}
		if !onlyFields(fn, fieldSet("name", "description", "parameters", "strict")) || !isString(fn["name"]) {
			return false
		}
		if !optionalString(fn, "description") || !optionalBool(fn, "strict") {
			return false
		}
		if params, ok := fn["parameters"]; ok {
			if _, ok := params.(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}

func validateOpenAIToolChoice(root map[string]any) bool {
	v, ok := root["tool_choice"]
	if !ok {
		return true
	}
	if s, ok := v.(string); ok {
		return s == "none" || s == "auto" || s == "required"
	}
	choice, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if !onlyFields(choice, fieldSet("type", "function")) || stringVal(choice["type"]) != "function" {
		return false
	}
	fn, ok := choice["function"].(map[string]any)
	if !ok || !onlyFields(fn, fieldSet("name")) || !isString(fn["name"]) {
		return false
	}
	return true
}

func validateOpenAIToolCalls(v any) bool {
	calls, ok := v.([]any)
	if !ok {
		return false
	}
	for _, c := range calls {
		call, ok := c.(map[string]any)
		if !ok {
			return false
		}
		if !onlyFields(call, fieldSet("id", "type", "function")) {
			return false
		}
		if !isString(call["id"]) || stringVal(call["type"]) != "function" {
			return false
		}
		fn, ok := call["function"].(map[string]any)
		if !ok || !onlyFields(fn, fieldSet("name", "arguments")) {
			return false
		}
		if !isString(fn["name"]) || !isString(fn["arguments"]) {
			return false
		}
	}
	return true
}

func buildAnthropicRequest(root map[string]any) ([]byte, error) {
	out := make(map[string]any)

	// model preserved
	out["model"] = root["model"]

	// max_tokens: Anthropic requires it; prefer max_completion_tokens, then max_tokens, else default
	if mt, ok := root["max_completion_tokens"]; ok {
		out["max_tokens"] = mt
	} else if mt, ok := root["max_tokens"]; ok {
		out["max_tokens"] = mt
	} else {
		out["max_tokens"] = json.Number(fmt.Sprintf("%d", defaultMaxTokens))
	}

	// reasoning_effort → thinking. These conservative defaults are superseded
	// by execution-authoritative EffectiveThinking when the Runner applies it.
	if budget := effortBudget(stringVal(root["reasoning_effort"])); budget > 0 {
		// Anthropic requires budget_tokens to be less than max_tokens. Preserve a
		// caller's larger limit; otherwise raise this conversion-only default.
		if intVal(out["max_tokens"]) <= budget {
			out["max_tokens"] = json.Number(fmt.Sprintf("%d", budget+1024))
		}
		out["thinking"] = map[string]any{"type": "enabled", "budget_tokens": json.Number(fmt.Sprintf("%d", budget))}
	}

	// stream
	if stream, ok := root["stream"]; ok {
		out["stream"] = stream
	}

	// temperature / top_p
	if temp, ok := root["temperature"]; ok {
		out["temperature"] = temp
	}
	if topP, ok := root["top_p"]; ok {
		out["top_p"] = topP
	}

	// stop → stop_sequences
	if stop, ok := root["stop"]; ok {
		switch s := stop.(type) {
		case string:
			out["stop_sequences"] = []any{s}
		case []any:
			out["stop_sequences"] = s
		}
	}

	// Convert messages: extract system, convert roles
	var systemParts []any
	var anthropicMessages []any

	messages := root["messages"].([]any)
	// Collect tool_call_id→(name, arguments) mapping from assistant tool_calls
	toolCallMap := make(map[string]toolCallInfo)
	for _, m := range messages {
		msg := m.(map[string]any)
		if tcs, ok := msg["tool_calls"]; ok {
			for _, tc := range tcs.([]any) {
				call := tc.(map[string]any)
				fn := call["function"].(map[string]any)
				toolCallMap[stringVal(call["id"])] = toolCallInfo{
					Name:      stringVal(fn["name"]),
					Arguments: stringVal(fn["arguments"]),
				}
			}
		}
	}

	for _, m := range messages {
		msg := m.(map[string]any)
		role := stringVal(msg["role"])

		switch role {
		case "system":
			systemParts = append(systemParts, extractAnthropicSystemContent(msg["content"]))
		case "user":
			anthropicMessages = append(anthropicMessages, buildAnthropicUserMessage(msg))
		case "assistant":
			anthropicMessages = append(anthropicMessages, buildAnthropicAssistantMessage(msg))
		case "tool":
			anthropicMessages = append(anthropicMessages, buildAnthropicToolResultMessage(msg, toolCallMap))
		}
	}

	if len(systemParts) > 0 {
		if len(systemParts) == 1 {
			out["system"] = systemParts[0]
		} else {
			out["system"] = systemParts
		}
	}
	out["messages"] = anthropicMessages

	// tools
	if tools, ok := root["tools"]; ok {
		out["tools"] = convertOpenAIToolsToAnthropic(tools)
	}

	// tool_choice
	if tc, ok := root["tool_choice"]; ok {
		out["tool_choice"] = convertOpenAIToolChoiceToAnthropic(tc)
	}

	// metadata.user_id ← user
	if user, ok := root["user"]; ok {
		out["metadata"] = map[string]any{"user_id": user}
	}

	return json.Marshal(out)
}

type toolCallInfo struct {
	Name      string
	Arguments string
}

func extractAnthropicSystemContent(content any) any {
	if s, ok := content.(string); ok {
		return map[string]any{"type": "text", "text": s}
	}
	// content blocks: extract text parts
	parts, ok := content.([]any)
	if !ok {
		return map[string]any{"type": "text", "text": ""}
	}
	var textParts []any
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok || stringVal(part["type"]) != "text" {
			continue
		}
		textParts = append(textParts, map[string]any{"type": "text", "text": part["text"]})
	}
	if len(textParts) == 1 {
		return textParts[0]
	}
	return textParts
}

func buildAnthropicUserMessage(msg map[string]any) map[string]any {
	content := convertOpenAIContentToAnthropic(msg["content"], "user")
	return map[string]any{
		"role":    "user",
		"content": content,
	}
}

func buildAnthropicAssistantMessage(msg map[string]any) map[string]any {
	var blocks []any

	// Anthropic requires thinking blocks to precede text blocks.
	if reasoning, ok := msg["reasoning_content"].(string); ok {
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": reasoning, "signature": ""})
	}

	// Content text
	if content := msg["content"]; content != nil {
		if s, ok := content.(string); ok && s != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": s})
		} else if arr, ok := content.([]any); ok {
			for _, block := range convertOpenAIContentToAnthropic(arr, "assistant").([]any) {
				blocks = append(blocks, block)
			}
		}
	}

	// tool_calls → tool_use blocks
	if tcs, ok := msg["tool_calls"]; ok {
		for _, tc := range tcs.([]any) {
			call := tc.(map[string]any)
			fn := call["function"].(map[string]any)
			var input any
			if err := json.Unmarshal([]byte(stringVal(fn["arguments"])), &input); err != nil {
				input = map[string]any{}
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    call["id"],
				"name":  fn["name"],
				"input": input,
			})
		}
	}

	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}

	return map[string]any{
		"role":    "assistant",
		"content": blocks,
	}
}

func buildAnthropicToolResultMessage(msg map[string]any, toolCallMap map[string]toolCallInfo) map[string]any {
	toolCallID := stringVal(msg["tool_call_id"])
	content := msg["content"]

	// Convert the content to Anthropic content blocks
	var contentBlocks []any
	if s, ok := content.(string); ok {
		if s != "" {
			contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": s})
		}
	} else if arr, ok := content.([]any); ok {
		for _, c := range arr {
			if part, ok := c.(map[string]any); ok {
				contentBlocks = append(contentBlocks, part)
			}
		}
	}
	if len(contentBlocks) == 0 {
		contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": ""})
	}

	return map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolCallID,
				"content":     contentBlocks,
			},
		},
	}
}

func convertOpenAIContentToAnthropic(content any, role string) any {
	if s, ok := content.(string); ok {
		return s
	}
	parts, ok := content.([]any)
	if !ok {
		return ""
	}
	blocks := make([]any, 0, len(parts))
	for _, p := range parts {
		part := p.(map[string]any) // validated by validOpenAIContent
		switch stringVal(part["type"]) {
		case "text":
			blocks = append(blocks, map[string]any{"type": "text", "text": part["text"]})
		case "image_url":
			urlStr := stringVal(part["image_url"].(map[string]any)["url"])
			if mediaType, data, ok := parseDataURL(urlStr); ok {
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": mediaType, "data": data,
				}})
			} else {
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
					"type": "url", "url": urlStr,
				}})
			}
		}
	}
	return blocks
}

func validImageReference(value string) bool {
	if strings.HasPrefix(value, "data:") {
		_, _, ok := parseDataURL(value)
		return ok
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func parseDataURL(value string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(value, prefix) {
		return "", "", false
	}
	metadata, data, found := strings.Cut(value[len(prefix):], ",")
	if !found || !strings.HasSuffix(metadata, ";base64") {
		return "", "", false
	}
	mediaType = strings.TrimSuffix(metadata, ";base64")
	parsedMediaType, _, err := mime.ParseMediaType(mediaType)
	if err != nil || parsedMediaType != mediaType || !strings.HasPrefix(mediaType, "image/") || data == "" {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) == 0 {
		return "", "", false
	}
	return mediaType, data, true
}

func convertOpenAIToolsToAnthropic(tools any) []any {
	toolList := tools.([]any)
	out := make([]any, 0, len(toolList))
	for _, t := range toolList {
		tool := t.(map[string]any)
		fn := tool["function"].(map[string]any)
		antTool := map[string]any{
			"name": fn["name"],
		}
		if desc, ok := fn["description"]; ok {
			antTool["description"] = desc
		}
		if params, ok := fn["parameters"]; ok {
			antTool["input_schema"] = params
		}
		out = append(out, antTool)
	}
	return out
}

func convertOpenAIToolChoiceToAnthropic(tc any) any {
	switch v := tc.(type) {
	case string:
		switch v {
		case "auto":
			return map[string]any{"type": "auto"}
		case "none":
			return map[string]any{"type": "auto"} // Anthropic has no "none"; auto is closest
		case "required":
			return map[string]any{"type": "any"}
		}
	case map[string]any:
		if stringVal(v["type"]) == "function" {
			fn := v["function"].(map[string]any)
			return map[string]any{
				"type": "tool",
				"name": fn["name"],
			}
		}
	}
	return map[string]any{"type": "auto"}
}

// ── Anthropic Messages → OpenAI Chat: Request ───────────────────────────────

func convertRequestAnthropicToOpenAI(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if err := validateAnthropicRequest(root); err != nil {
		return nil, ErrInvalidRequest
	}
	return buildOpenAIRequest(root)
}

var anthropicRootFields = fieldSet(
	"model", "messages", "max_tokens", "system", "thinking",
	"stream", "temperature", "top_p", "top_k", "stop_sequences",
	"tools", "tool_choice", "metadata",
)

func validateAnthropicRequest(root map[string]any) error {
	if !onlyFields(root, anthropicRootFields) || !isString(root["model"]) {
		return ErrInvalidRequest
	}
	if !hasField(root, "max_tokens") || !optionalPositiveInt(root, "max_tokens") {
		return ErrInvalidRequest
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return ErrInvalidRequest
	}
	for _, m := range messages {
		if err := validateAnthropicMessage(m); err != nil {
			return err
		}
	}
	if stream, ok := root["stream"]; ok && !isBool(stream) {
		return ErrInvalidRequest
	}
	if !optionalNumberInRange(root, "temperature", 0, 1) {
		return ErrInvalidRequest
	}
	if !optionalNumberInRange(root, "top_p", 0, 1) {
		return ErrInvalidRequest
	}
	if !optionalPositiveInt(root, "top_k") {
		return ErrInvalidRequest
	}
	if !validateAnthropicStopSequences(root) {
		return ErrInvalidRequest
	}
	if !validateAnthropicTools(root) {
		return ErrInvalidRequest
	}
	if !validateAnthropicToolChoice(root) {
		return ErrInvalidRequest
	}
	if !validateAnthropicMetadata(root) || !validateAnthropicThinking(root) {
		return ErrInvalidRequest
	}
	return nil
}

func validateAnthropicMessage(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return ErrInvalidRequest
	}
	if !onlyFields(m, fieldSet("role", "content")) {
		return ErrInvalidRequest
	}
	role := stringVal(m["role"])
	if role != "user" && role != "assistant" {
		return ErrInvalidRequest
	}
	return validateAnthropicContent(m["content"])
}

func validateAnthropicContent(v any) error {
	if isString(v) {
		return nil
	}
	blocks, ok := v.([]any)
	if !ok {
		return ErrInvalidRequest
	}
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			return ErrInvalidRequest
		}
		typ := stringVal(block["type"])
		switch typ {
		case "text":
			if !isString(block["text"]) {
				return ErrInvalidRequest
			}
		case "tool_use":
			if !isString(block["id"]) || !isString(block["name"]) {
				return ErrInvalidRequest
			}
			if _, ok := block["input"].(map[string]any); !ok {
				return ErrInvalidRequest
			}
		case "tool_result":
			if !isString(block["tool_use_id"]) {
				return ErrInvalidRequest
			}
		case "image":
			if !validAnthropicImageBlock(block) {
				return ErrInvalidRequest
			}
		case "thinking":
			if !isString(block["thinking"]) || !isString(block["signature"]) {
				return ErrInvalidRequest
			}
		default:
			return ErrInvalidRequest
		}
	}
	return nil
}

func validAnthropicImageBlock(block map[string]any) bool {
	if !onlyFields(block, fieldSet("type", "source")) {
		return false
	}
	source, ok := block["source"].(map[string]any)
	if !ok {
		return false
	}
	switch stringVal(source["type"]) {
	case "base64":
		if !onlyFields(source, fieldSet("type", "media_type", "data")) || !isString(source["media_type"]) || !isString(source["data"]) {
			return false
		}
		mediaType, data := stringVal(source["media_type"]), stringVal(source["data"])
		_, _, ok := parseDataURL("data:" + mediaType + ";base64," + data)
		return ok
	case "url":
		return onlyFields(source, fieldSet("type", "url")) && isString(source["url"]) && validImageReference(stringVal(source["url"]))
	default:
		return false
	}
}

func validateAnthropicStopSequences(root map[string]any) bool {
	v, ok := root["stop_sequences"]
	if !ok {
		return true
	}
	arr, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range arr {
		if !isString(item) {
			return false
		}
	}
	return true
}

func validateAnthropicTools(root map[string]any) bool {
	v, ok := root["tools"]
	if !ok {
		return true
	}
	tools, ok := v.([]any)
	if !ok {
		return false
	}
	for _, t := range tools {
		tool, ok := t.(map[string]any)
		if !ok || !isString(tool["name"]) {
			return false
		}
		if !optionalString(tool, "description") {
			return false
		}
		if hasField(tool, "input_schema") {
			if _, ok := tool["input_schema"].(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}

func validateAnthropicToolChoice(root map[string]any) bool {
	v, ok := root["tool_choice"]
	if !ok {
		return true
	}
	choice, ok := v.(map[string]any)
	if !ok {
		return ErrInvalidRequest != nil
	}
	typ := stringVal(choice["type"])
	switch typ {
	case "auto", "any":
		if !optionalBool(choice, "disable_parallel_tool_use") {
			return false
		}
	case "tool":
		if !isString(choice["name"]) {
			return false
		}
		if !optionalBool(choice, "disable_parallel_tool_use") {
			return false
		}
	default:
		return false
	}
	return true
}

func validateAnthropicThinking(root map[string]any) bool {
	v, ok := root["thinking"]
	if !ok {
		return true
	}
	thinking, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if stringVal(thinking["type"]) == "disabled" {
		return onlyFields(thinking, fieldSet("type"))
	}
	return onlyFields(thinking, fieldSet("type", "budget_tokens")) &&
		stringVal(thinking["type"]) == "enabled" && optionalNonNegativeInt(thinking, "budget_tokens") && hasField(thinking, "budget_tokens")
}

func validateAnthropicMetadata(root map[string]any) bool {
	v, ok := root["metadata"]
	if !ok {
		return true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	return onlyFields(m, fieldSet("user_id")) && optionalString(m, "user_id")
}

func buildOpenAIRequest(root map[string]any) ([]byte, error) {
	out := make(map[string]any)

	// model preserved
	out["model"] = root["model"]

	// max_tokens
	if mt, ok := root["max_tokens"]; ok {
		out["max_tokens"] = mt
	}

	// thinking → reasoning_effort. These conservative protocolconvert thresholds
	// are superseded by execution-authoritative EffectiveThinking in the Runner.
	if thinking, ok := root["thinking"].(map[string]any); ok {
		if effort := budgetEffort(intVal(thinking["budget_tokens"])); effort != "" {
			out["reasoning_effort"] = effort
		}
	}

	// stream
	if stream, ok := root["stream"]; ok {
		out["stream"] = stream
	}

	// temperature / top_p
	if temp, ok := root["temperature"]; ok {
		out["temperature"] = temp
	}
	if topP, ok := root["top_p"]; ok {
		out["top_p"] = topP
	}

	// stop_sequences → stop
	if ss, ok := root["stop_sequences"]; ok {
		arr := ss.([]any)
		if len(arr) == 1 {
			out["stop"] = arr[0]
		} else {
			out["stop"] = arr
		}
	}

	// Convert messages: system → system message, tool_result → tool messages
	var openaiMessages []any

	// System
	if sys, ok := root["system"]; ok {
		openaiMessages = append(openaiMessages, buildOpenAISystemMessage(sys))
	}

	// Messages
	messages := root["messages"].([]any)
	// Collect tool_use id→name mapping from assistant messages
	toolUseMap := make(map[string]string) // id → name
	for _, m := range messages {
		msg := m.(map[string]any)
		if stringVal(msg["role"]) == "assistant" {
			if blocks, ok := msg["content"].([]any); ok {
				for _, b := range blocks {
					if block, ok := b.(map[string]any); ok && stringVal(block["type"]) == "tool_use" {
						toolUseMap[stringVal(block["id"])] = stringVal(block["name"])
					}
				}
			}
		}
	}

	for _, m := range messages {
		msg := m.(map[string]any)
		role := stringVal(msg["role"])
		switch role {
		case "user":
			openaiMessages = append(openaiMessages, buildOpenAIUserMessages(msg, toolUseMap)...)
		case "assistant":
			openaiMessages = append(openaiMessages, buildOpenAIAssistantMessage(msg))
		}
	}

	out["messages"] = openaiMessages

	// tools
	if tools, ok := root["tools"]; ok {
		out["tools"] = convertAnthropicToolsToOpenAI(tools)
	}

	// tool_choice
	if tc, ok := root["tool_choice"]; ok {
		out["tool_choice"] = convertAnthropicToolChoiceToOpenAI(tc)
	}

	// metadata.user_id → user
	if meta, ok := root["metadata"]; ok {
		if m, ok := meta.(map[string]any); ok {
			if uid, ok := m["user_id"]; ok {
				out["user"] = uid
			}
		}
	}

	return json.Marshal(out)
}

func buildOpenAISystemMessage(sys any) map[string]any {
	switch s := sys.(type) {
	case string:
		return map[string]any{"role": "system", "content": s}
	case []any:
		// Multiple system blocks → combine into one string
		var parts []string
		for _, b := range s {
			if block, ok := b.(map[string]any); ok && stringVal(block["type"]) == "text" {
				parts = append(parts, stringVal(block["text"]))
			}
		}
		content := ""
		for i, p := range parts {
			if i > 0 {
				content += "\n"
			}
			content += p
		}
		return map[string]any{"role": "system", "content": content}
	case map[string]any:
		// Single text block
		if stringVal(s["type"]) == "text" {
			return map[string]any{"role": "system", "content": s["text"]}
		}
	}
	return map[string]any{"role": "system", "content": ""}
}

func buildOpenAIUserMessages(msg map[string]any, toolUseMap map[string]string) []any {
	content := msg["content"]

	// Check if content contains tool_result blocks at the top level
	if blocks, ok := content.([]any); ok {
		hasToolResult := false
		for _, b := range blocks {
			if block, ok := b.(map[string]any); ok && stringVal(block["type"]) == "tool_result" {
				hasToolResult = true
				break
			}
		}
		if hasToolResult {
			// Split tool_result blocks into separate tool messages,
			// and non-tool_result blocks into user messages
			var result []any
			var userBlocks []any

			for _, b := range blocks {
				block := b.(map[string]any)
				if stringVal(block["type"]) == "tool_result" {
					// Flush user blocks first
					if len(userBlocks) > 0 {
						result = append(result, map[string]any{
							"role":    "user",
							"content": userBlocks,
						})
						userBlocks = nil
					}
					// Convert tool_result → OpenAI tool message
					result = append(result, map[string]any{
						"role":         "tool",
						"tool_call_id": block["tool_use_id"],
						"content":      convertAnthropicToolResultContent(block["content"]),
					})
				} else {
					userBlocks = append(userBlocks, convertAnthropicBlockToOpenAI(block))
				}
			}
			if len(userBlocks) > 0 {
				result = append(result, map[string]any{
					"role":    "user",
					"content": userBlocks,
				})
			}
			return result
		}

		// No tool_result blocks: convert content blocks
		var converted []any
		for _, b := range blocks {
			if block, ok := b.(map[string]any); ok {
				converted = append(converted, convertAnthropicBlockToOpenAI(block))
			}
		}
		return []any{map[string]any{
			"role":    "user",
			"content": converted,
		}}
	}

	// String content
	return []any{map[string]any{
		"role":    "user",
		"content": content,
	}}
}

func convertAnthropicToolResultContent(content any) any {
	if s, ok := content.(string); ok {
		return s
	}
	if blocks, ok := content.([]any); ok {
		var parts []string
		for _, b := range blocks {
			if block, ok := b.(map[string]any); ok && stringVal(block["type"]) == "text" {
				parts = append(parts, stringVal(block["text"]))
			}
		}
		if len(parts) == 1 {
			return parts[0]
		}
		if len(parts) > 1 {
			// Combine text parts
			combined := ""
			for i, p := range parts {
				if i > 0 {
					combined += "\n"
				}
				combined += p
			}
			return combined
		}
	}
	return ""
}

func convertAnthropicBlockToOpenAI(block map[string]any) any {
	switch stringVal(block["type"]) {
	case "text":
		return map[string]any{"type": "text", "text": block["text"]}
	case "image":
		source := block["source"].(map[string]any) // validated by validAnthropicImageBlock
		url := stringVal(source["url"])
		if stringVal(source["type"]) == "base64" {
			url = "data:" + stringVal(source["media_type"]) + ";base64," + stringVal(source["data"])
		}
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
	}
	return nil
}

func buildOpenAIAssistantMessage(msg map[string]any) map[string]any {
	content := msg["content"]
	out := map[string]any{"role": "assistant"}

	// Extract text and tool_use from content blocks
	var textParts []string
	var reasoningParts []string
	var toolCalls []any

	if s, ok := content.(string); ok {
		textParts = append(textParts, s)
	} else if blocks, ok := content.([]any); ok {
		for _, b := range blocks {
			block, ok := b.(map[string]any)
			if !ok {
				continue
			}
			typ := stringVal(block["type"])
			switch typ {
			case "text":
				textParts = append(textParts, stringVal(block["text"]))
			case "tool_use":
				argsJSON, _ := json.Marshal(block["input"])
				toolCalls = append(toolCalls, map[string]any{
					"id":   block["id"],
					"type": "function",
					"function": map[string]any{
						"name":      block["name"],
						"arguments": string(argsJSON),
					},
				})
			case "thinking":
				reasoningParts = append(reasoningParts, stringVal(block["thinking"]))
			}
		}
	}

	if len(textParts) > 0 {
		out["content"] = textParts[0] // OpenAI uses single string
		if len(textParts) > 1 {
			combined := ""
			for i, p := range textParts {
				if i > 0 {
					combined += "\n"
				}
				combined += p
			}
			out["content"] = combined
		}
	} else if len(toolCalls) == 0 {
		out["content"] = ""
	}

	if len(toolCalls) > 0 {
		out["tool_calls"] = toolCalls
	}
	if len(reasoningParts) > 0 {
		out["reasoning_content"] = joinTextParts(reasoningParts)
	}

	return out
}

func joinTextParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, part := range parts[1:] {
		out += "\n" + part
	}
	return out
}

func effortBudget(effort string) int64 {
	return defaultEffortBudgets[effort]
}

func budgetEffort(budget int64) string {
	switch {
	case budget <= 0:
		return "none"
	case budget >= defaultEffortBudgets["max"]:
		return "max"
	case budget >= defaultEffortBudgets["xhigh"]:
		return "xhigh"
	case budget >= defaultEffortBudgets["high"]:
		return "high"
	case budget >= defaultEffortBudgets["medium"]:
		return "medium"
	case budget >= defaultEffortBudgets["low"]:
		return "low"
	case budget >= defaultEffortBudgets["minimal"]:
		return "minimal"
	default:
		return ""
	}
}

func convertAnthropicToolsToOpenAI(tools any) []any {
	toolList := tools.([]any)
	out := make([]any, 0, len(toolList))
	for _, t := range toolList {
		tool := t.(map[string]any)
		oaiTool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": tool["name"],
			},
		}
		fn := oaiTool["function"].(map[string]any)
		if desc, ok := tool["description"]; ok {
			fn["description"] = desc
		}
		if schema, ok := tool["input_schema"]; ok {
			fn["parameters"] = schema
		}
		out = append(out, oaiTool)
	}
	return out
}

func convertAnthropicToolChoiceToOpenAI(tc any) any {
	choice, ok := tc.(map[string]any)
	if !ok {
		return "auto"
	}
	typ := stringVal(choice["type"])
	switch typ {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		if name, ok := choice["name"]; ok {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}
		}
	}
	return "auto"
}

// ── OpenAI Chat → Anthropic Messages: Response ──────────────────────────────

func convertResponseOpenAIToAnthropic(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidResponse
	}
	return buildAnthropicResponse(root), nil
}

func buildAnthropicResponse(root map[string]any) []byte {
	out := make(map[string]any)
	out["id"] = root["id"]
	out["type"] = "message"
	out["role"] = "assistant"
	out["model"] = root["model"]

	// Convert choices[0] → content + stop_reason
	choices, ok := root["choices"].([]any)
	if ok && len(choices) > 0 {
		choice := choices[0].(map[string]any)
		var contentBlocks []any

		// Text content
		if message, ok := choice["message"].(map[string]any); ok {
			if text := stringVal(message["content"]); text != "" {
				contentBlocks = append(contentBlocks, map[string]any{
					"type": "text",
					"text": text,
				})
			}
			// Tool calls → tool_use blocks
			if tcs, ok := message["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					call := tc.(map[string]any)
					fn := call["function"].(map[string]any)
					var input any
					if err := json.Unmarshal([]byte(stringVal(fn["arguments"])), &input); err != nil {
						input = map[string]any{}
					}
					contentBlocks = append(contentBlocks, map[string]any{
						"type":  "tool_use",
						"id":    call["id"],
						"name":  fn["name"],
						"input": input,
					})
				}
			}
		}

		if len(contentBlocks) == 0 {
			contentBlocks = append(contentBlocks, map[string]any{"type": "text", "text": ""})
		}
		out["content"] = contentBlocks

		// stop_reason mapping
		finishReason := stringVal(choice["finish_reason"])
		out["stop_reason"] = mapOpenAIFinishToAnthropic(finishReason)
		out["stop_sequence"] = nil
	}

	// Usage
	if usage, ok := root["usage"].(map[string]any); ok {
		out["usage"] = map[string]any{
			"input_tokens":  usage["prompt_tokens"],
			"output_tokens": usage["completion_tokens"],
		}
	} else {
		out["usage"] = map[string]any{
			"input_tokens":  json.Number("0"),
			"output_tokens": json.Number("0"),
		}
	}

	result, _ := json.Marshal(out)
	return result
}

func mapOpenAIFinishToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// ── Anthropic Messages → OpenAI Chat: Response ──────────────────────────────

func convertResponseAnthropicToOpenAI(body []byte) ([]byte, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidResponse
	}
	return buildOpenAIResponse(root), nil
}

func buildOpenAIResponse(root map[string]any) []byte {
	out := make(map[string]any)
	out["id"] = root["id"]
	out["object"] = "chat.completion"
	out["model"] = root["model"]
	out["created"] = json.Number("0")

	// Convert content → choices[0]
	var contentBlocks []any
	if blocks, ok := root["content"].([]any); ok {
		contentBlocks = blocks
	}

	var textContent string
	var toolCalls []any
	for _, b := range contentBlocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		switch stringVal(block["type"]) {
		case "text":
			textContent = stringVal(block["text"])
		case "tool_use":
			argsJSON, _ := json.Marshal(block["input"])
			toolCalls = append(toolCalls, map[string]any{
				"id":   block["id"],
				"type": "function",
				"function": map[string]any{
					"name":      block["name"],
					"arguments": string(argsJSON),
				},
			})
		}
	}

	// stop_reason mapping
	stopReason := stringVal(root["stop_reason"])
	finishReason := mapAnthropicFinishToOpenAI(stopReason)

	message := map[string]any{
		"role":    "assistant",
		"content": textContent,
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}

	choice := map[string]any{
		"index":         json.Number("0"),
		"message":       message,
		"finish_reason": finishReason,
	}
	out["choices"] = []any{choice}

	// Usage
	if usage, ok := root["usage"].(map[string]any); ok {
		inputTokens := intVal(usage["input_tokens"])
		outputTokens := intVal(usage["output_tokens"])
		out["usage"] = map[string]any{
			"prompt_tokens":     json.Number(fmt.Sprintf("%d", inputTokens)),
			"completion_tokens": json.Number(fmt.Sprintf("%d", outputTokens)),
			"total_tokens":      json.Number(fmt.Sprintf("%d", inputTokens+outputTokens)),
		}
	} else {
		out["usage"] = map[string]any{
			"prompt_tokens":     json.Number("0"),
			"completion_tokens": json.Number("0"),
			"total_tokens":      json.Number("0"),
		}
	}

	result, _ := json.Marshal(out)
	return result
}

func mapAnthropicFinishToOpenAI(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}

// ── Stream: OpenAI → Anthropic ──────────────────────────────────────────────
//
// Image blocks are request-only in this converter. Provider stream deltas do
// not carry image content, so image deltas are deliberately unsupported here.

func convertStreamChunkOpenAIToAnthropic(raw []byte, state *StreamState) ([][]byte, error) {
	// Handle [DONE] sentinel: it is equivalent to a clean EOF, so it delegates
	// to the same terminal-synthesis helper FinalizeStream exposes for a
	// committed clean stream end.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		return finalizeOpenAIToAnthropic(state), nil
	}

	root, err := parseStrictJSON(raw)
	if err != nil {
		return nil, ErrInvalidStreamChunk
	}

	// Basic validation
	if !isString(root["id"]) || !isString(root["model"]) {
		return nil, ErrInvalidStreamChunk
	}

	var results [][]byte

	// Accumulate usage from any chunk that carries it. OpenAI emits a
	// terminal choices:[] + usage chunk when stream_options.include_usage is
	// on; its completion_tokens must be captured before terminal synthesis.
	if usage, ok := root["usage"].(map[string]any); ok {
		if pt, ok := usage["prompt_tokens"]; ok {
			state.OAIUsage.PromptTokens = intVal(pt)
		}
		if ct, ok := usage["completion_tokens"]; ok {
			state.OAIUsage.CompletionTokens = intVal(ct)
		}
	}

	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		// Usage-only or lifecycle chunk. A usage-only chunk arriving on a
		// started (not yet closed) stream completes the message exactly once,
		// carrying the accumulated usage (the OpenAI include_usage final
		// chunk). A usage-only chunk on a stream that has not started (e.g. an
		// initial usage-only chunk) must not synthesize a message_start or a
		// terminal, so it is a no-op here.
		if _, ok := root["usage"].(map[string]any); ok && state.OAIStarted {
			return finalizeOpenAIToAnthropic(state), nil
		}
		return results, nil
	}

	// First content/role chunk starts the message. This is deferred until a
	// chunk with a non-empty choices array arrives so a usage-only initial
	// chunk does not emit a spurious message_start.
	if !state.OAIStarted {
		state.OAIStarted = true
		state.OAIMessageID = stringVal(root["id"])
		state.OAIModel = stringVal(root["model"])
		state.OAIBlockIndex = 0
		state.OAIToolCallIndex = 0
		state.OAIThinkingStarted = false
		state.OAIThinkingStopped = false
		results = append(results, buildAnthropicMessageStart(state.OAIMessageID, state.OAIModel))
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		return results, nil
	}

	delta, _ := choice["delta"].(map[string]any)
	finishReason := ""
	if fr, ok := choice["finish_reason"]; ok && fr != nil {
		finishReason = stringVal(fr)
	}

	// Role announcement
	if role, ok := delta["role"]; ok && stringVal(role) == "assistant" {
		// Skip role announcement, already in message_start
		return results, nil
	}

	// Reasoning must precede text in Anthropic content. A text delta closes an
	// open thinking block before opening the text block.
	if reasoning, ok := delta["reasoning_content"]; ok && isString(reasoning) && stringVal(reasoning) != "" {
		if !state.OAIThinkingStarted && !state.OAIThinkingStopped {
			state.OAIThinkingIndex = state.OAIBlockIndex
			results = append(results, buildAnthropicContentBlockStart(state.OAIThinkingIndex, "thinking"))
			state.OAIBlockIndex++
			state.OAIThinkingStarted = true
		}
		if state.OAIThinkingStarted {
			results = append(results, buildAnthropicContentBlockDelta(state.OAIThinkingIndex, "thinking_delta", stringVal(reasoning)))
		}
	}

	// Text content
	if content, ok := delta["content"]; ok && isString(content) {
		if state.OAIThinkingStarted {
			results = append(results, buildAnthropicContentBlockStop(state.OAIThinkingIndex))
			state.OAIThinkingStarted = false
			state.OAIThinkingStopped = true
		}
		text := stringVal(content)
		if !state.hasOpenAITextBlock() {
			results = append(results, buildAnthropicContentBlockStart(state.OAIBlockIndex, "text", ""))
			state.OAIBlockIndex++
			state.setOpenAITextBlock(true)
		}
		if text != "" {
			results = append(results, buildAnthropicContentBlockDelta(state.OAIBlockIndex-1, "text_delta", text))
		}
	}

	// Tool calls
	if tcs, ok := delta["tool_calls"].([]any); ok {
		if state.OAIThinkingStarted {
			results = append(results, buildAnthropicContentBlockStop(state.OAIThinkingIndex))
			state.OAIThinkingStarted = false
			state.OAIThinkingStopped = true
		}
		for _, tc := range tcs {
			call, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			idx := intVal(call["index"])

			// New tool call: emit content_block_start
			if fn, ok := call["function"].(map[string]any); ok {
				if isString(call["id"]) && isString(fn["name"]) {
					// This is a new tool call start
					if !state.hasOpenAIToolBlock(idx) {
						results = append(results, buildAnthropicContentBlockStart(state.OAIBlockIndex, "tool_use", stringVal(call["id"]), stringVal(fn["name"])))
						state.OAIBlockIndex++
						state.setOpenAIToolBlock(idx, true)
					}
				}
				// Arguments delta
				if args, ok := fn["arguments"]; ok && isString(args) && stringVal(args) != "" {
					results = append(results, buildAnthropicContentBlockDelta(state.OAIBlockIndex-1, "input_json_delta", stringVal(args)))
				}
			}
		}
	}

	// Finish reason
	if finishReason != "" {
		state.OAIFinishReason = mapOpenAIFinishToAnthropic(finishReason)

		// Close any open blocks
		if state.OAIThinkingStarted {
			results = append(results, buildAnthropicContentBlockStop(state.OAIThinkingIndex))
			state.OAIThinkingStarted = false
			state.OAIThinkingStopped = true
		}
		if state.hasOpenAITextBlock() {
			results = append(results, buildAnthropicContentBlockStop(state.OAIBlockIndex-1))
			state.setOpenAITextBlock(false)
		}
		for k := range state.openAIToolBlocks() {
			_ = k
			results = append(results, buildAnthropicContentBlockStop(state.OAIBlockIndex-1))
			break // close once is enough, they're sequential
		}
		state.clearOpenAIToolBlocks()

		// Emit message_delta + message_stop
		results = append(results, buildAnthropicMessageDelta(state.OAIFinishReason, &state.OAIUsage))
		results = append(results, buildAnthropicMessageStop())
		state.OAIStarted = false
	}

	return results, nil
}

// OpenAI stream state helpers using StreamState fields more efficiently
// We reuse OAIToolCallIndex as a bit-field tracker

func (s *StreamState) hasOpenAITextBlock() bool { return s.OAIToolCallIndex&1 != 0 }
func (s *StreamState) setOpenAITextBlock(v bool) {
	if v {
		s.OAIToolCallIndex |= 1
	} else {
		s.OAIToolCallIndex &^= 1
	}
}
func (s *StreamState) hasOpenAIToolBlock(idx int64) bool {
	return s.OAIToolCallIndex&(2<<(idx&31)) != 0
}
func (s *StreamState) setOpenAIToolBlock(idx int64, v bool) {
	if v {
		s.OAIToolCallIndex |= (2 << (idx & 31))
	} else {
		s.OAIToolCallIndex &^= (2 << (idx & 31))
	}
}
func (s *StreamState) openAIToolBlocks() map[int64]bool {
	m := make(map[int64]bool)
	bits := s.OAIToolCallIndex >> 1
	for i := int64(0); i < 32; i++ {
		if bits&(1<<uint(i)) != 0 {
			m[i] = true
		}
	}
	return m
}
func (s *StreamState) clearOpenAIToolBlocks() {
	s.OAIToolCallIndex &^= ^1 // clear all bits except text flag
}

// ── Anthropic stream event builders ─────────────────────────────────────────

func buildAnthropicMessageStart(id, model string) []byte {
	ev := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":    id,
			"type":  "message",
			"role":  "assistant",
			"model": model,
			"usage": map[string]any{
				"input_tokens":  json.Number("0"),
				"output_tokens": json.Number("0"),
			},
		},
	}
	b, _ := json.Marshal(ev)
	return b
}

func buildAnthropicContentBlockStart(index int, blockType string, extra ...string) []byte {
	block := map[string]any{
		"type": blockType,
	}
	switch blockType {
	case "text":
		block["text"] = ""
	case "thinking":
		block["thinking"] = ""
	case "tool_use":
		if len(extra) >= 2 {
			block["id"] = extra[0]
			block["name"] = extra[1]
			block["input"] = map[string]any{}
		}
	}
	ev := map[string]any{
		"type":          "content_block_start",
		"index":         json.Number(fmt.Sprintf("%d", index)),
		"content_block": block,
	}
	b, _ := json.Marshal(ev)
	return b
}

func buildAnthropicContentBlockDelta(index int, deltaType, text string) []byte {
	delta := map[string]any{
		"type": deltaType,
	}
	switch deltaType {
	case "text_delta":
		delta["text"] = text
	case "input_json_delta":
		delta["partial_json"] = text
	case "thinking_delta":
		delta["thinking"] = text
	}
	ev := map[string]any{
		"type":  "content_block_delta",
		"index": json.Number(fmt.Sprintf("%d", index)),
		"delta": delta,
	}
	b, _ := json.Marshal(ev)
	return b
}

func buildAnthropicContentBlockStop(index int) []byte {
	ev := map[string]any{
		"type":  "content_block_stop",
		"index": json.Number(fmt.Sprintf("%d", index)),
	}
	b, _ := json.Marshal(ev)
	return b
}

func buildAnthropicMessageDelta(stopReason string, usage *streamUsageAccum) []byte {
	ev := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"type":          "message_delta",
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": json.Number(fmt.Sprintf("%d", usage.CompletionTokens)),
		},
	}
	b, _ := json.Marshal(ev)
	return b
}

func buildAnthropicMessageStop() []byte {
	ev := map[string]any{"type": "message_stop"}
	b, _ := json.Marshal(ev)
	return b
}

// ── Stream: Anthropic → OpenAI ──────────────────────────────────────────────

func convertStreamChunkAnthropicToOpenAI(raw []byte, state *StreamState) ([][]byte, error) {
	root, err := parseStrictJSON(raw)
	if err != nil {
		return nil, ErrInvalidStreamChunk
	}

	eventType := stringVal(root["type"])
	if eventType == "" {
		return nil, ErrInvalidStreamChunk
	}

	var results [][]byte

	switch eventType {
	case "message_start":
		if msg, ok := root["message"].(map[string]any); ok {
			state.AntStarted = true
			state.AntMessageID = stringVal(msg["id"])
			state.AntModel = stringVal(msg["model"])
			if usage, ok := msg["usage"].(map[string]any); ok {
				state.AntUsage.PromptTokens = intVal(usage["input_tokens"])
			}
		}
		// No OpenAI chunk emitted for message_start

	case "content_block_start":
		blockIndex := int(intVal(root["index"]))
		state.AntBlockIndex = blockIndex
		if block, ok := root["content_block"].(map[string]any); ok {
			if !state.AntRoleAnnounced {
				results = append(results, buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{"role": "assistant"}, "", nil))
				state.AntRoleAnnounced = true
			}
			switch stringVal(block["type"]) {
			case "thinking":
				state.AntThinkingActive = true
			case "text":
			case "tool_use":
				// Emit tool_calls start
				toolCallIdx := state.AntToolCallIndex
				results = append(results, buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{
					"tool_calls": []any{
						map[string]any{
							"index": json.Number(fmt.Sprintf("%d", toolCallIdx)),
							"id":    block["id"],
							"type":  "function",
							"function": map[string]any{
								"name":      block["name"],
								"arguments": "",
							},
						},
					},
				}, "", nil))
				state.AntToolCallIndex++
			}
		}

	case "content_block_delta":
		_ = intVal(root["index"]) // consumed for state tracking
		delta, ok := root["delta"].(map[string]any)
		if !ok {
			return nil, ErrInvalidStreamChunk
		}
		deltaType := stringVal(delta["type"])
		switch deltaType {
		case "text_delta":
			text := stringVal(delta["text"])
			if text != "" {
				results = append(results, buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{"content": text}, "", nil))
			}
		case "thinking_delta":
			thinking := stringVal(delta["thinking"])
			if thinking != "" {
				results = append(results, buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{"reasoning_content": thinking}, "", nil))
			}
		case "input_json_delta":
			partialJSON := stringVal(delta["partial_json"])
			if partialJSON != "" {
				results = append(results, buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{
					"tool_calls": []any{
						map[string]any{
							"index": json.Number(fmt.Sprintf("%d", state.AntToolCallIndex-1)),
							"function": map[string]any{
								"arguments": partialJSON,
							},
						},
					},
				}, "", nil))
			}
		}

	case "content_block_stop":
		if state.AntThinkingActive && int(intVal(root["index"])) == state.AntBlockIndex {
			state.AntThinkingActive = false
		}
		// No explicit OpenAI equivalent

	case "message_delta":
		if delta, ok := root["delta"].(map[string]any); ok {
			state.AntFinishReason = stringVal(delta["stop_reason"])
		}
		if usage, ok := root["usage"].(map[string]any); ok {
			state.AntUsage.CompletionTokens = intVal(usage["output_tokens"])
		}
		// Don't emit final chunk yet; wait for message_stop

	case "message_stop":
		finishReason := mapAnthropicFinishToOpenAI(state.AntFinishReason)
		usage := map[string]any{
			"prompt_tokens":     json.Number(fmt.Sprintf("%d", state.AntUsage.PromptTokens)),
			"completion_tokens": json.Number(fmt.Sprintf("%d", state.AntUsage.CompletionTokens)),
			"total_tokens":      json.Number(fmt.Sprintf("%d", state.AntUsage.PromptTokens+state.AntUsage.CompletionTokens)),
		}
		results = append(results, buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{}, finishReason, usage))
		state.AntStarted = false

	case "ping":
		// Ignore

	default:
		return nil, ErrInvalidStreamChunk
	}

	return results, nil
}

// FinalizeStream synthesizes the protocol-native terminal stream event(s)
// for a converted stream that ended cleanly (the upstream returned EOF)
// without an explicit terminal event having been converted. It is driven
// entirely by the caller-owned StreamState accumulated across ConvertStreamChunk
// calls for the same stream: it performs no I/O, never sees a downstream
// renderer, and never touches credentials or URLs.
//
// It is idempotent and exactly-once: once the state records the message has
// been closed (OAIStarted/AntStarted false — set by an earlier finish chunk
// conversion or a prior FinalizeStream), it returns nil without synthesizing a
// second terminal. A caller MUST call it at most once per stream after the
// last ConvertStreamChunk; the exactly-once guard makes a redundant call
// safe but a well-behaved caller avoids it.
//
// For OpenAI→Anthropic: if the state still has an open Anthropic message, it
// synthesizes message_delta (carrying the accumulated stop reason, defaulting
// to "end_turn" when none was seen, and accumulated usage) and message_stop,
// then marks the message closed.
//
// For Anthropic→OpenAI: if the state still has an open message, it synthesizes
// one final OpenAI chat.completion.chunk carrying finish_reason (mapped from
// the accumulated Anthropic stop reason) and usage, then marks the message
// closed.
//
// Returns the synthesized JSON event payloads (without SSE framing). An empty
// slice means no synthesis was needed: the stream already emitted its terminal
// via a converted finish chunk, so a finalizer must not synthesize a second.
func FinalizeStream(fromProtocol, toProtocol adapter.Protocol, state *StreamState) ([][]byte, error) {
	if !supportedConversion(fromProtocol, toProtocol) {
		return nil, ErrUnsupportedConversion
	}
	if state == nil {
		return nil, ErrInvalidStreamChunk
	}
	switch {
	case fromProtocol == adapter.ProtocolOpenAIChat && toProtocol == adapter.ProtocolOpenAIResponses:
		return finalizeOpenAIToResponses(state), nil
	case fromProtocol == adapter.ProtocolAnthropic && toProtocol == adapter.ProtocolOpenAIResponses:
		return finalizeAnthropicToResponses(state), nil
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolOpenAIChat:
		return finalizeResponsesToOpenAI(state), nil
	case fromProtocol == adapter.ProtocolOpenAIResponses && toProtocol == adapter.ProtocolAnthropic:
		return finalizeResponsesToAnthropic(state), nil
	}
	if fromProtocol == adapter.ProtocolOpenAIChat {
		return finalizeOpenAIToAnthropic(state), nil
	}
	return finalizeAnthropicToOpenAI(state), nil
}

// finalizeOpenAIToAnthropic is the shared, exactly-once terminal synthesis for
// an OpenAI→Anthropic stream that ended cleanly. It mirrors the legacy
// [DONE]-sentinel semantics so both paths produce identical output.
func finalizeOpenAIToAnthropic(state *StreamState) [][]byte {
	if !state.OAIStarted {
		return nil
	}
	if state.OAIFinishReason == "" {
		state.OAIFinishReason = "end_turn"
	}
	var results [][]byte
	if state.OAIThinkingStarted {
		results = append(results, buildAnthropicContentBlockStop(state.OAIThinkingIndex))
		state.OAIThinkingStarted = false
		state.OAIThinkingStopped = true
	}
	results = append(results,
		buildAnthropicMessageDelta(state.OAIFinishReason, &state.OAIUsage),
		buildAnthropicMessageStop(),
	)
	state.OAIStarted = false
	return results
}

// finalizeAnthropicToOpenAI is the exactly-once terminal synthesis for an
// Anthropic→OpenAI stream that ended cleanly. It mirrors the message_stop
// branch of convertStreamChunkAnthropicToOpenAI so both paths produce identical
// output.
func finalizeAnthropicToOpenAI(state *StreamState) [][]byte {
	if !state.AntStarted {
		return nil
	}
	finishReason := mapAnthropicFinishToOpenAI(state.AntFinishReason)
	usage := map[string]any{
		"prompt_tokens":     json.Number(fmt.Sprintf("%d", state.AntUsage.PromptTokens)),
		"completion_tokens": json.Number(fmt.Sprintf("%d", state.AntUsage.CompletionTokens)),
		"total_tokens":      json.Number(fmt.Sprintf("%d", state.AntUsage.PromptTokens+state.AntUsage.CompletionTokens)),
	}
	results := [][]byte{buildOpenAIChunk(state.AntMessageID, state.AntModel, map[string]any{}, finishReason, usage)}
	state.AntStarted = false
	return results
}

func buildOpenAIChunk(id, model string, delta map[string]any, finishReason string, usage map[string]any) []byte {
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": json.Number("0"),
		"model":   model,
	}
	if len(delta) > 0 || finishReason != "" {
		choice := map[string]any{
			"index": json.Number("0"),
			"delta": delta,
		}
		if finishReason != "" {
			choice["finish_reason"] = finishReason
		} else {
			choice["finish_reason"] = nil
		}
		chunk["choices"] = []any{choice}
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	b, _ := json.Marshal(chunk)
	return b
}
