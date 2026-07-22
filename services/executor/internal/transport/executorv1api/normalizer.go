package executorv1api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/authcontext"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/imagecontract"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
)

const (
	maxJSONDepth          = 64
	maxJSONNodes          = 10_000
	maxSelectorBytes      = 512
	maxHTTPSImageURLBytes = 16 << 10
	maxImageDecodedBytes  = 1 << 20
	maxImageBase64Encoded = 4 * ((maxImageDecodedBytes + 2) / 3)
	maxImageDataURLBytes  = maxImageBase64Encoded + 64
)

var (
	// ErrStreamingUnsupported distinguishes a valid streaming request from an
	// invalid request. The non-stream facade must never execute it.
	ErrStreamingUnsupported = errors.New("executorv1api: streaming is unsupported")

	// Root field allow-lists mirror the Executor v1 OpenAPI exactly; any other
	// root key is rejected (strict additionalProperties:false).
	chatRootFields = makeFieldSet(
		"model", "messages", "stream", "temperature", "top_p", "max_tokens",
		"max_completion_tokens", "reasoning_effort", "stop", "tools", "tool_choice",
		"response_format", "user",
	)
	imageRootFields   = makeFieldSet("model", "prompt", "n", "size", "quality", "response_format", "style", "user")
	messageRootFields = makeFieldSet(
		"model", "messages", "max_tokens", "system", "thinking", "stream",
		"temperature", "top_p", "top_k", "stop_sequences", "tools", "tool_choice",
		"metadata",
	)
	messageThinkingFields        = makeFieldSet("type", "budget_tokens", "display")
	chatMessageFields            = makeFieldSet("role", "content", "name", "tool_calls", "tool_call_id", "reasoning_content")
	chatContentPartFields        = makeFieldSet("type", "text", "image_url")
	chatImageURLFields           = makeFieldSet("url", "detail")
	chatToolFields               = makeFieldSet("type", "function")
	chatToolFunctionFields       = makeFieldSet("name", "description", "parameters", "strict")
	chatToolChoiceFields         = makeFieldSet("type", "function")
	chatToolChoiceFunctionFields = makeFieldSet("name")
	chatToolCallFields           = makeFieldSet("id", "type", "function")
	chatToolCallFunctionFields   = makeFieldSet("name", "arguments")
	chatResponseFormatFields     = makeFieldSet("type")
	anthropicSystemBlockFields   = makeFieldSet("type", "text", "cache_control")
	anthropicMessageFields       = makeFieldSet("role", "content")
	anthropicContentBlockFields  = makeFieldSet(
		"type", "text", "source", "id", "name", "input",
		"tool_use_id", "content", "thinking", "signature", "cache_control",
	)
	anthropicImageSourceFields = makeFieldSet("type", "media_type", "data")
	anthropicCacheFields       = makeFieldSet("type")
	anthropicToolFields        = makeFieldSet("name", "description", "input_schema", "cache_control")
	anthropicToolChoiceFields  = makeFieldSet("type", "name", "disable_parallel_tool_use")
	anthropicMetadataFields    = makeFieldSet("user_id")
)

type fieldSet map[string]struct{}

func makeFieldSet(names ...string) fieldSet {
	set := make(fieldSet, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// DetectOpenAIChatStream performs only the bounded structural JSON gate on the
// captured raw body, then reads the optional stream flag. It deliberately does
// not validate the request schema, normalize it, or allocate a request ID.
func DetectOpenAIChatStream(ctx context.Context) (bool, error) {
	return detectStreamFlag(ctx)
}

// DetectAnthropicMessagesStream is the Messages counterpart of
// DetectOpenAIChatStream.
func DetectAnthropicMessagesStream(ctx context.Context) (bool, error) {
	return detectStreamFlag(ctx)
}

// detectStreamFlag shares the normalizer's strict structural decode (including
// the capture bound, UTF-8, one-object, duplicate/prototype, depth/node, and
// trailing-content checks) but intentionally reads no schema fields beyond
// stream. This permits Hybrid to delegate stream:false to the generated strict
// wrapper, where the non-stream adapter performs the sole full normalization.
func detectStreamFlag(ctx context.Context) (bool, error) {
	raw, ok := rawBodyView(ctx)
	if !ok {
		return false, ErrInvalidRequest
	}
	root, err := parseRequestJSON(ctx, raw)
	if err != nil {
		return false, ErrInvalidRequest
	}
	value, exists := root["stream"]
	if !exists {
		return false, nil
	}
	stream, ok := value.(bool)
	if !ok {
		return false, ErrInvalidRequest
	}
	return stream, nil
}

// NormalizeOpenAIChat extracts the non-stream execution inputs from the raw
// body captured before the generated decoder. It performs full strict schema
// validation of the entire CreateChatCompletionRequest tree (root and every
// nested schema: messages, content parts, tools, tool choices, tool calls,
// response_format, reasoning_effort) and retains the raw JSON bytes verbatim.
func NormalizeOpenAIChat(ctx context.Context, requestID string) (NonStreamRequest, error) {
	result, err := NormalizeOpenAIChatRequest(ctx, requestID)
	if err != nil {
		return NonStreamRequest{}, err
	}
	if result.Stream {
		return NonStreamRequest{}, ErrStreamingUnsupported
	}
	return result.Request, nil
}

// NormalizedRequest is a fully schema-validated request plus its execution
// mode. A true Stream value is valid here and is not executed by this layer.
type NormalizedRequest struct {
	Request NonStreamRequest
	Stream  bool
}

// StreamRequest returns the streaming-boundary form of this normalized request.
// It makes independent body storage so a future handler and its Sink cannot
// mutate the normalized result retained by its caller. Callers must only use it
// after Stream is true; mode dispatch remains outside this normalizer.
func (r NormalizedRequest) StreamRequest(sink execution.ProtocolSink) StreamRequest {
	req := r.Request
	return StreamRequest{
		Protocol: req.Protocol, Selector: req.Selector,
		Body: json.RawMessage(append([]byte(nil), req.Body...)), Thinking: req.Thinking,
		RequestID: req.RequestID, Principal: req.Principal, Sink: sink,
	}
}

// NormalizeOpenAIChatRequest validates a Chat request exactly once and returns
// its normalized request plus whether the caller selected streaming mode.
func NormalizeOpenAIChatRequest(ctx context.Context, requestID string) (NormalizedRequest, error) {
	return normalize(ctx, requestID, adapter.ProtocolOpenAIChat, validateChatRequest, normalizeChatThinking)
}

// NormalizedImageRequest carries the executor request and the response format
// selected by the client (or the observable url default). The renderer must use
// EffectiveResponseFormat so an injected execution result cannot change it.
type NormalizedImageRequest struct {
	Request                 NonStreamRequest
	EffectiveResponseFormat string
}

// NormalizeOpenAIImages validates the legacy Images Generate request before
// execution/quota reservation and retains its original captured JSON bytes.
func NormalizeOpenAIImages(ctx context.Context, requestID string) (NormalizedImageRequest, error) {
	result, err := normalize(ctx, requestID, adapter.ProtocolOpenAIImages, validateImageRequest, func(map[string]any) (adapter.ThinkingRequest, error) { return adapter.ThinkingRequest{}, nil })
	if err != nil {
		return NormalizedImageRequest{}, err
	}
	root, err := parseRequestJSON(ctx, result.Request.Body)
	if err != nil {
		return NormalizedImageRequest{}, ErrInvalidRequest
	}
	format, ok := imagecontract.ValidateRequest(root)
	if !ok {
		return NormalizedImageRequest{}, ErrInvalidRequest
	}
	return NormalizedImageRequest{Request: result.Request, EffectiveResponseFormat: format}, nil
}

// NormalizeAnthropicMessages extracts the non-stream execution inputs from the
// raw Messages body. It performs full strict schema validation of the entire
// CreateMessageRequest tree (root and every nested schema: messages, content
// blocks, system blocks, thinking, tools, tool_choice, metadata) and retains
// the raw JSON bytes verbatim.
func NormalizeAnthropicMessages(ctx context.Context, requestID string) (NonStreamRequest, error) {
	result, err := NormalizeAnthropicMessagesRequest(ctx, requestID)
	if err != nil {
		return NonStreamRequest{}, err
	}
	if result.Stream {
		return NonStreamRequest{}, ErrStreamingUnsupported
	}
	return result.Request, nil
}

// NormalizeAnthropicMessagesRequest validates a Messages request exactly once
// and returns its normalized request plus whether streaming was selected.
func NormalizeAnthropicMessagesRequest(ctx context.Context, requestID string) (NormalizedRequest, error) {
	return normalize(ctx, requestID, adapter.ProtocolAnthropic, validateMessageRequest, normalizeMessageThinking)
}

func normalize(
	ctx context.Context,
	requestID string,
	protocol adapter.Protocol,
	validate func(map[string]any) error,
	thinking func(map[string]any) (adapter.ThinkingRequest, error),
) (NormalizedRequest, error) {
	raw, ok := rawBodyView(ctx)
	if !ok {
		return NormalizedRequest{}, ErrInvalidRequest
	}
	root, err := parseRequestJSON(ctx, raw)
	if err != nil {
		return NormalizedRequest{}, ErrInvalidRequest
	}
	// Full strict schema validation runs first: a schema-invalid request is
	// uniformly ErrInvalidRequest (native 400), regardless of any stream field.
	// Only after the entire tree validates is a schema-valid stream:true
	// recognized as an unsupported streaming request.
	if err := validate(root); err != nil {
		return NormalizedRequest{}, ErrInvalidRequest
	}
	stream := false
	if value, exists := root["stream"]; exists {
		var ok bool
		stream, ok = value.(bool)
		if !ok {
			return NormalizedRequest{}, ErrInvalidRequest
		}
	}
	selector, ok := root["model"].(string)
	if !ok || strings.TrimSpace(selector) == "" || len(selector) > maxSelectorBytes {
		return NormalizedRequest{}, ErrInvalidRequest
	}
	requestThinking, err := thinking(root)
	if err != nil {
		return NormalizedRequest{}, err
	}
	// raw is capture-owned and immutable. The executor port is a separate
	// trust boundary, so it receives exactly one independent copy; it must not
	// be able to mutate the context's raw-body view. The trusted authenticated
	// Principal is derived here from the transport auth boundary's
	// authcontext.IdentityFromContext and carried on the request; it carries no
	// key material and is defensively revalidated downstream.
	principal := nonstream.Principal{}
	if id, ok := authcontext.IdentityFromContext(ctx); ok {
		principal = nonstream.Principal{Subject: id.Subject, KeyID: id.KeyID, Role: string(id.Role), Status: string(id.Status)}
	}
	return NormalizedRequest{Request: NonStreamRequest{
		Protocol: protocol, Selector: selector, Body: json.RawMessage(append([]byte(nil), raw...)), Thinking: requestThinking, RequestID: requestID, Principal: principal,
	}, Stream: stream}, nil
}

// normalizeChatThinking maps the validated reasoning_effort enum to a
// ThinkingRequest. "none" is the schema-valid way to disable reasoning: it
// maps to the zero (disabled) ThinkingRequest, matching the Engine's disabled
// path. The enum shape was already enforced by validateChatRequest.
func normalizeChatThinking(root map[string]any) (adapter.ThinkingRequest, error) {
	value, exists := root["reasoning_effort"]
	if !exists {
		return adapter.ThinkingRequest{}, nil
	}
	effort, _ := value.(string)
	if effort == string(adapter.ThinkingNone) {
		return adapter.ThinkingRequest{}, nil
	}
	return adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingEffort(effort)}, nil
}

// normalizeMessageThinking maps the validated ThinkingConfig to a
// ThinkingRequest. The shape (type/budget_tokens/display and the
// budget>=1024 && budget<max_tokens relationship) was already enforced by
// validateMessageRequest; this function only extracts the authoritative
// values. "disabled" yields the zero (disabled) ThinkingRequest.
func normalizeMessageThinking(root map[string]any) (adapter.ThinkingRequest, error) {
	value, exists := root["thinking"]
	if !exists {
		return adapter.ThinkingRequest{}, nil
	}
	thinking, _ := value.(map[string]any)
	typeValue, _ := thinking["type"].(string)
	if typeValue == "disabled" {
		return adapter.ThinkingRequest{}, nil
	}
	budget, _ := jsonInteger(thinking["budget_tokens"])
	return adapter.ThinkingRequest{Enabled: true, BudgetTokens: &budget}, nil
}

// parseRequestJSON is the strict structural gate: it rejects non-UTF-8,
// oversized bodies, duplicate object keys, prototype-family names, excessive
// nesting/nodes, trailing content and any non-object root. Semantic schema
// validation runs after this.
func parseRequestJSON(ctx context.Context, raw []byte) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > int(MaxCapturedBodyBytes) || !utf8.Valid(raw) {
		return nil, ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	value, err := parseJSONValue(ctx, decoder, 1, &nodes)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalidRequest
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInvalidRequest
	}
	return root, nil
}

func parseJSONValue(ctx context.Context, decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if err := ctx.Err(); err != nil || depth > maxJSONDepth {
		return nil, ErrInvalidRequest
	}
	*nodes++
	if *nodes > maxJSONNodes {
		return nil, ErrInvalidRequest
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalidRequest
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok || forbiddenJSONName(key) {
					return nil, ErrInvalidRequest
				}
				if _, duplicate := object[key]; duplicate {
					return nil, ErrInvalidRequest
				}
				child, err := parseJSONValue(ctx, decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalidRequest
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				child, err := parseJSONValue(ctx, decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalidRequest
			}
			return array, nil
		default:
			return nil, ErrInvalidRequest
		}
	case string, bool, nil, json.Number:
		return token, nil
	default:
		return nil, ErrInvalidRequest
	}
}

// ── OpenAI Chat schema validation ──────────────────────────────────────

func validateChatRequest(root map[string]any) error {
	if !onlyFields(root, chatRootFields) {
		return ErrInvalidRequest
	}
	if !isString(root["model"]) {
		return ErrInvalidRequest
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return ErrInvalidRequest
	}
	for _, item := range messages {
		if !reqChatMessage(item) {
			return ErrInvalidRequest
		}
	}
	if stream, exists := root["stream"]; exists && !isBool(stream) ||
		!optionalNumberIn(root, "temperature", 0, 2) ||
		!optionalNumberIn(root, "top_p", 0, 1) ||
		!optionalPositiveInteger(root, "max_tokens") ||
		!optionalPositiveInteger(root, "max_completion_tokens") ||
		!optionalEnum(root, "reasoning_effort", "none", "minimal", "low", "medium", "high", "xhigh", "max") ||
		!validChatStop(root) ||
		!optionalString(root, "user") ||
		!validChatTools(root) ||
		!validChatToolChoice(root) ||
		!validChatResponseFormat(root) {
		return ErrInvalidRequest
	}
	return nil
}

func reqChatMessage(value any) bool {
	message, ok := value.(map[string]any)
	if !ok || !onlyFields(message, chatMessageFields) {
		return false
	}
	role, ok := message["role"].(string)
	if !ok {
		return false
	}
	switch role {
	case "system", "user", "assistant", "tool":
	default:
		return false
	}
	if !reqChatContent(message["content"]) {
		return false
	}
	if !optionalString(message, "name") ||
		!optionalNullableString(message, "reasoning_content") ||
		!optionalString(message, "tool_call_id") {
		return false
	}
	if toolCalls, exists := message["tool_calls"]; exists && !reqChatToolCalls(toolCalls) {
		return false
	}
	return true
}

func reqChatContent(value any) bool {
	if value == nil {
		return false
	}
	if _, ok := value.(string); ok {
		return true
	}
	parts, ok := value.([]any)
	if !ok {
		return false
	}
	for _, part := range parts {
		if !validChatContentPart(part) {
			return false
		}
	}
	return true
}

func validChatContentPart(value any) bool {
	part, ok := value.(map[string]any)
	if !ok || !onlyFields(part, chatContentPartFields) {
		return false
	}
	kind, ok := part["type"].(string)
	if !ok {
		return false
	}
	switch kind {
	case "text", "image_url":
	default:
		return false
	}
	// Only type is required; text and image_url are both optional for either
	// enum value, so a single fieldset applies regardless of kind.
	if !optionalString(part, "text") {
		return false
	}
	image, exists := part["image_url"]
	if !exists {
		return true
	}
	imageMap, ok := image.(map[string]any)
	if !ok || !onlyFields(imageMap, chatImageURLFields) {
		return false
	}
	if url, hasURL := imageMap["url"]; hasURL && !validOpenAIImageURL(url) {
		return false
	}
	return optionalEnum(imageMap, "detail", "auto", "low", "high")
}

func validChatTools(root map[string]any) bool {
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
		if !ok || !onlyFields(tool, chatToolFields) || tool["type"] != "function" {
			return false
		}
		function, ok := tool["function"].(map[string]any)
		if !ok || !onlyFields(function, chatToolFunctionFields) || !isString(function["name"]) ||
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

func validChatToolChoice(root map[string]any) bool {
	value, exists := root["tool_choice"]
	if !exists {
		return true
	}
	if choice, ok := value.(string); ok {
		return choice == "none" || choice == "auto" || choice == "required"
	}
	choice, ok := value.(map[string]any)
	if !ok || !onlyFields(choice, chatToolChoiceFields) || choice["type"] != "function" {
		return false
	}
	function, ok := choice["function"].(map[string]any)
	return ok && onlyFields(function, chatToolChoiceFunctionFields) && isString(function["name"])
}

func reqChatToolCalls(value any) bool {
	calls, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range calls {
		call, ok := value.(map[string]any)
		if !ok || !onlyFields(call, chatToolCallFields) || !isString(call["id"]) || call["type"] != "function" {
			return false
		}
		function, ok := call["function"].(map[string]any)
		if !ok || !onlyFields(function, chatToolCallFunctionFields) || !isString(function["name"]) || !isString(function["arguments"]) {
			return false
		}
	}
	return true
}

func validChatResponseFormat(root map[string]any) bool {
	value, exists := root["response_format"]
	if !exists {
		return true
	}
	format, ok := value.(map[string]any)
	if !ok || !onlyFields(format, chatResponseFormatFields) {
		return false
	}
	return optionalEnum(format, "type", "text", "json_object", "json_schema")
}

func validChatStop(root map[string]any) bool {
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

// ── OpenAI Images schema validation ─────────────────────────────────────

func validateImageRequest(root map[string]any) error {
	if _, ok := imagecontract.ValidateRequest(root); !ok {
		return ErrInvalidRequest
	}
	return nil
}

// ── Anthropic Messages schema validation ────────────────────────────────

func validateMessageRequest(root map[string]any) error {
	if !onlyFields(root, messageRootFields) || !isString(root["model"]) || !requiredPositiveInteger(root, "max_tokens") {
		return ErrInvalidRequest
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return ErrInvalidRequest
	}
	for _, message := range messages {
		if !validAnthropicMessage(message) {
			return ErrInvalidRequest
		}
	}
	if stream, exists := root["stream"]; exists && !isBool(stream) ||
		!optionalNumberIn(root, "temperature", 0, 1) ||
		!optionalNumberIn(root, "top_p", 0, 1) ||
		!optionalPositiveInteger(root, "top_k") ||
		!validAnthropicSystem(root) ||
		!validAnthropicThinking(root) ||
		!optionalStringArray(root, "stop_sequences") ||
		!validAnthropicTools(root) ||
		!validAnthropicToolChoice(root) ||
		!validAnthropicMetadata(root) {
		return ErrInvalidRequest
	}
	return nil
}

func validAnthropicSystem(root map[string]any) bool {
	value, exists := root["system"]
	if !exists {
		return true
	}
	if isString(value) {
		return true
	}
	blocks, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok || !onlyFields(block, anthropicSystemBlockFields) || block["type"] != "text" || !isString(block["text"]) || !validAnthropicCache(block) {
			return false
		}
	}
	return true
}

func validAnthropicThinking(root map[string]any) bool {
	value, exists := root["thinking"]
	if !exists {
		return true
	}
	thinking, ok := value.(map[string]any)
	if !ok || !onlyFields(thinking, messageThinkingFields) {
		return false
	}
	typeValue, ok := thinking["type"].(string)
	if !ok {
		return false
	}
	switch typeValue {
	case "disabled":
		// budget_tokens is only valid when type=enabled.
		if _, hasBudget := thinking["budget_tokens"]; hasBudget {
			return false
		}
		return optionalEnum(thinking, "display", "summarized", "omitted")
	case "enabled":
		budget, ok := jsonInteger(thinking["budget_tokens"])
		if !ok {
			return false
		}
		maxTokens, ok := jsonInteger(root["max_tokens"])
		if !ok || budget < 1024 || maxTokens < 1 || budget >= maxTokens {
			return false
		}
		return optionalEnum(thinking, "display", "summarized", "omitted")
	default:
		return false
	}
}

func validAnthropicMessage(value any) bool {
	message, ok := value.(map[string]any)
	if !ok || !onlyFields(message, anthropicMessageFields) {
		return false
	}
	role, ok := message["role"].(string)
	if !ok {
		return false
	}
	if role != "user" && role != "assistant" {
		return false
	}
	return validAnthropicContent(message["content"])
}

func validAnthropicContent(value any) bool {
	if value == nil {
		return false
	}
	if isString(value) {
		return true
	}
	blocks, ok := value.([]any)
	if !ok {
		return false
	}
	for _, block := range blocks {
		if !validAnthropicBlock(block) {
			return false
		}
	}
	return true
}

func validAnthropicBlock(value any) bool {
	block, ok := value.(map[string]any)
	if !ok || !onlyFields(block, anthropicContentBlockFields) {
		return false
	}
	kind, ok := block["type"].(string)
	if !ok {
		return false
	}
	switch kind {
	case "text", "image", "tool_use", "tool_result", "thinking":
	default:
		return false
	}
	return optionalString(block, "text") &&
		optionalString(block, "id") &&
		optionalString(block, "name") &&
		optionalString(block, "tool_use_id") &&
		optionalString(block, "thinking") &&
		optionalString(block, "signature") &&
		validAnthropicSource(block) &&
		validAnthropicInput(block) &&
		validAnthropicBlockContent(block) &&
		validAnthropicCache(block)
}

// validAnthropicSource enforces the security boundary for every image source,
// including image blocks nested recursively in tool_result content. OpenAPI
// currently only enums source.type; its media_type is unconstrained, so the
// accepted image media types below are a deliberate tighter safety policy.
func validAnthropicSource(block map[string]any) bool {
	source, exists := block["source"]
	if !exists {
		return true
	}
	object, ok := source.(map[string]any)
	if !ok || !onlyFields(object, anthropicImageSourceFields) {
		return false
	}
	typeValue, typeOK := object["type"].(string)
	mediaType, mediaTypeOK := object["media_type"].(string)
	data, dataOK := object["data"].(string)
	return typeOK && typeValue == "base64" && mediaTypeOK && validImageMediaType(mediaType) &&
		dataOK && validImageBase64(data)
}

// validAnthropicInput validates the optional tool_use input object. It must be
// a non-nil JSON object when present.
func validAnthropicInput(block map[string]any) bool {
	input, exists := block["input"]
	if !exists {
		return true
	}
	object, ok := input.(map[string]any)
	return ok && object != nil
}

// validAnthropicBlockContent validates the optional tool_result content, which
// mirrors the AnthropicMessage content shape (string or block array).
func validAnthropicBlockContent(block map[string]any) bool {
	content, exists := block["content"]
	if !exists {
		return true
	}
	return validAnthropicContent(content)
}

func validAnthropicCache(value map[string]any) bool {
	cache, exists := value["cache_control"]
	if !exists {
		return true
	}
	object, ok := cache.(map[string]any)
	return ok && onlyFields(object, anthropicCacheFields) && optionalEnum(object, "type", "ephemeral")
}

func validAnthropicTools(root map[string]any) bool {
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
		if !ok || !onlyFields(tool, anthropicToolFields) || !isString(tool["name"]) || !optionalString(tool, "description") || !validAnthropicCache(tool) {
			return false
		}
		if schema, ok := tool["input_schema"].(map[string]any); !ok || schema == nil {
			return false
		}
	}
	return true
}

func validAnthropicToolChoice(root map[string]any) bool {
	value, exists := root["tool_choice"]
	if !exists {
		return true
	}
	choice, ok := value.(map[string]any)
	if !ok || !onlyFields(choice, anthropicToolChoiceFields) {
		return false
	}
	typeValue, ok := choice["type"].(string)
	if !ok {
		return false
	}
	switch typeValue {
	case "auto", "any", "tool":
	default:
		return false
	}
	// One fieldset applies to all type values: only type is required, while
	// name (optional string) and disable_parallel_tool_use (optional bool) are
	// accepted for auto/any as well as tool.
	return optionalString(choice, "name") && optionalBool(choice, "disable_parallel_tool_use")
}

func validAnthropicMetadata(root map[string]any) bool {
	value, exists := root["metadata"]
	if !exists {
		return true
	}
	metadata, ok := value.(map[string]any)
	if !ok || !onlyFields(metadata, anthropicMetadataFields) {
		return false
	}
	return optionalString(metadata, "user_id")
}

// ── shared helpers ─────────────────────────────────────────────────────

func onlyFields(object map[string]any, allowed fieldSet) bool {
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
}

func forbiddenJSONName(name string) bool {
	return name == "__proto__" || name == "prototype" || name == "constructor"
}

func isString(value any) bool { _, ok := value.(string); return ok }
func isBool(value any) bool   { _, ok := value.(bool); return ok }

// validOpenAIImageURL is stricter than the contract's format: uri and than the
// existing SDK validator's generic absolute URI check. Only bounded HTTPS
// URLs with a host and no userinfo, or bounded strict base64 image data URLs,
// can reach the non-stream executor.
func validOpenAIImageURL(value any) bool {
	text, ok := value.(string)
	if !ok || len(text) == 0 {
		return false
	}
	if strings.HasPrefix(text, "data:") {
		return len(text) <= maxImageDataURLBytes && validImageDataURL(text)
	}
	if len(text) > maxHTTPSImageURLBytes {
		return false
	}
	parsed, err := url.ParseRequestURI(text)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func validImageDataURL(text string) bool {
	const prefix = "data:"
	comma := strings.IndexByte(text, ',')
	if !strings.HasPrefix(text, prefix) || comma < len(prefix) {
		return false
	}
	meta := text[len(prefix):comma]
	data := text[comma+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return false
	}
	mediaType := strings.TrimSuffix(meta, ";base64")
	return validImageMediaType(mediaType) && validImageBase64(data)
}

func validImageMediaType(mediaType string) bool {
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// validImageBase64 uses StdEncoding (not RawStdEncoding): empty values,
// unpadded encodings, URL alphabets, whitespace, and malformed padding are
// rejected. EncodedLen bounds allocation before DecodeString, and the decoded
// bound is retained as a defense against future encoding changes.
func validImageBase64(data string) bool {
	if len(data) == 0 || len(data) > maxImageBase64Encoded {
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(data)
	return err == nil && len(decoded) > 0 && len(decoded) <= maxImageDecodedBytes
}

func optionalString(value map[string]any, name string) bool {
	item, exists := value[name]
	return !exists || isString(item)
}

// optionalNullableString accepts an absent field, a string, or a JSON null.
// It mirrors OpenAPI `nullable: true` string fields such as ChatMessage's
// reasoning_content.
func optionalNullableString(value map[string]any, name string) bool {
	item, exists := value[name]
	if !exists || item == nil {
		return true
	}
	return isString(item)
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
	return isPositiveInteger(item)
}

// requiredPositiveInteger enforces a required positive integer field. Unlike
// optionalPositiveInteger, an absent field is rejected. This mirrors the
// Anthropic CreateMessageRequest where max_tokens is required by the contract.
func requiredPositiveInteger(value map[string]any, name string) bool {
	item, exists := value[name]
	return exists && isPositiveInteger(item)
}

func isPositiveInteger(value any) bool {
	integer, ok := jsonInteger(value)
	return ok && integer >= 1
}

func optionalStringArray(value map[string]any, name string) bool {
	item, exists := value[name]
	if !exists {
		return true
	}
	items, ok := item.([]any)
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

// jsonInteger parses a JSON number produced by UseNumber into a non-negative
// int. Non-integer, negative or out-of-range values are rejected (the caller
// applies the >= 1 bound where required).
func jsonInteger(value any) (int, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return int(parsed), true
}
