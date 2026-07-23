package openaiadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/openai/openai-go/v3/responses"
	"github.com/tokenmp/v3/services/executor/internal/responsecontract"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

const (
	maxResponseWireResponseBytes = responsecontract.MaxWireResponseBytes
)

var errResponseTooLarge = errors.New("openaiadapter: response exceeds limit")

// completeResponse performs exactly one non-streaming official OpenAI SDK
// Responses operation.
func (c *Client) completeResponse(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Completion{}, classifyContextError(err)
	}
	baseURL, err := parseBaseURL(call.Target.BaseURL)
	if err != nil {
		return sdk.Completion{}, ErrInvalidBaseURL
	}
	if strings.TrimSpace(call.Target.UpstreamModel) == "" {
		return sdk.Completion{}, ErrMissingUpstreamModel
	}
	if err := validateInjection(call.Request.InjectionPlan); err != nil {
		return sdk.Completion{}, ErrInvalidInjection
	}
	params, err := decodeResponseParams(ctx, call.Request.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return sdk.Completion{}, classifyContextError(ctxErr)
		}
		return sdk.Completion{}, ErrInvalidRequest
	}
	params.Model = openai.ResponsesModel(call.Target.UpstreamModel)

	var apiKey string
	if err := call.Secret.Use(func(key []byte) error {
		if strings.TrimSpace(string(key)) == "" {
			return ErrMissingAPIKey
		}
		apiKey = string(key)
		return nil
	}); err != nil {
		return sdk.Completion{}, err
	}

	capture := &imageResponseCapture{}
	base := c.perCallHTTPClient(call, apiKey)
	perCall := &http.Client{Transport: imageCapTransport{next: base.Transport, capture: capture}, CheckRedirect: base.CheckRedirect, Jar: base.Jar, Timeout: base.Timeout}
	opts := []option.RequestOption{option.WithBaseURL(baseURL.String()), option.WithHTTPClient(perCall), option.WithMaxRetries(0), option.WithAPIKey(apiKey)}
	opts = append(opts, injectionOptions(call.Request.InjectionPlan)...)
	opts = append(opts, option.WithHeader("Authorization", "Bearer "+apiKey))
	opts = append(opts, option.WithJSONSet("stream", false))
	client := openai.NewClient(opts...)

	var response *http.Response
	res, err := client.Responses.New(ctx, params, option.WithResponseInto(&response))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return sdk.Completion{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return sdk.Completion{}, sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
		}
		if errors.Is(err, errResponseTooLarge) {
			return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrProtocol, capture.status(), capture.requestID(), "", "")
		}
		return sdk.Completion{}, classifyError(err, response)
	}
	if err := ctx.Err(); err != nil {
		return sdk.Completion{}, classifyContextError(err)
	}
	raw := []byte(res.RawJSON())
	if err := validateResponseResponse(ctx, raw); err != nil {
		return sdk.Completion{}, sdk.NewClassifiedError(sdk.ErrProtocol, capture.status(), capture.requestID(), "", "")
	}
	completion := sdk.Completion{RawJSON: json.RawMessage(raw), Status: capture.status(), RequestID: sdk.SafeRequestID(capture.requestID())}
	completion.Usage, completion.Known = extractOpenAIResponseUsage(completion.RawJSON)
	return completion, nil
}

// streamResponse opens exactly one official OpenAI SDK Responses stream.
func (c *Client) streamResponse(ctx context.Context, call sdk.StreamCall) (sdk.StreamOpen, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return sdk.StreamOpen{}, classifyContextError(err)
	}
	baseURL, err := parseBaseURL(call.Target.BaseURL)
	if err != nil {
		return sdk.StreamOpen{}, ErrInvalidBaseURL
	}
	if strings.TrimSpace(call.Target.UpstreamModel) == "" {
		return sdk.StreamOpen{}, ErrMissingUpstreamModel
	}
	if err := validateInjection(call.Request.InjectionPlan); err != nil {
		return sdk.StreamOpen{}, ErrInvalidInjection
	}
	params, err := decodeResponseParams(ctx, call.Request.Body)
	if err != nil {
		return sdk.StreamOpen{}, ErrInvalidRequest
	}
	params.Model = openai.ResponsesModel(call.Target.UpstreamModel)

	var apiKey string
	if err := call.Secret.Use(func(key []byte) error {
		if strings.TrimSpace(string(key)) == "" {
			return ErrMissingAPIKey
		}
		apiKey = string(key)
		return nil
	}); err != nil {
		return sdk.StreamOpen{}, err
	}

	openingCtx, cancel := context.WithCancel(ctx)
	observer := &sseObserver{}
	capture := &openingCapture{observer: observer}
	base := c.perCallHTTPClient(sdk.Call{Candidate: call.Candidate, Target: call.Target, Request: call.Request, Secret: call.Secret}, apiKey)
	perCall := &http.Client{Transport: captureTransport{capture: capture, next: base.Transport}, CheckRedirect: base.CheckRedirect, Jar: base.Jar, Timeout: base.Timeout}
	opts := []option.RequestOption{option.WithBaseURL(baseURL.String()), option.WithHTTPClient(perCall), option.WithMaxRetries(0), option.WithAPIKey(apiKey)}
	opts = append(opts, injectionOptions(call.Request.InjectionPlan)...)
	opts = append(opts, option.WithHeader("Authorization", "Bearer "+apiKey))
	client := openai.NewClient(opts...)
	stream := client.Responses.NewStreaming(openingCtx, params)
	response := capture.get()
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 || stream.Err() != nil {
		cancel()
		if stream != nil {
			_ = stream.Close()
		}
		if err := ctx.Err(); err != nil {
			return sdk.StreamOpen{}, classifyContextError(err)
		}
		if response != nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
			kind := kindForHTTPStatus(response.StatusCode)
			reqID := response.Header.Get("x-request-id")
			if isRetryableHTTPStatus(response.StatusCode) {
				if ra, ok := sdk.ParseRetryAfter(response.Header); ok {
					return sdk.StreamOpen{}, sdk.NewClassifiedErrorWithRetryAfter(kind, response.StatusCode, reqID, "", "", ra, true)
				}
			}
			return sdk.StreamOpen{}, sdk.NewClassifiedError(kind, response.StatusCode, reqID, "", "")
		}
		if stream != nil && stream.Err() != nil {
			return sdk.StreamOpen{}, classifyStreamOpenError(stream.Err(), response)
		}
		return sdk.StreamOpen{}, sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	return sdk.StreamOpen{Source: newResponseChunkSource(stream, cancel, observer), Status: response.StatusCode, RequestID: sdk.SafeRequestID(response.Header.Get("x-request-id"))}, nil
}

func decodeResponseParams(ctx context.Context, body []byte) (responses.ResponseNewParams, error) {
	var p responses.ResponseNewParams
	if len(body) == 0 || len(body) > maxParamBodyBytes || !utf8.Valid(body) {
		return p, ErrInvalidRequest
	}
	v, err := parseStrictJSON(ctx, body)
	if err != nil {
		return p, err
	}
	r, ok := v.(map[string]any)
	if !ok {
		return p, ErrInvalidRequest
	}
	if !responsecontract.ValidateRequest(r) {
		return p, ErrInvalidRequest
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, err
	}
	return p, nil
}

func validateResponseResponse(ctx context.Context, raw []byte) error {
	if len(raw) == 0 || len(raw) > maxResponseWireResponseBytes || !utf8.Valid(raw) {
		return errResponseTooLarge
	}
	v, err := parseStrictJSON(ctx, raw)
	if err != nil {
		return errResponseTooLarge
	}
	r, ok := v.(map[string]any)
	if !ok || !responsecontract.ValidateResponse(r) {
		return errResponseTooLarge
	}
	return nil
}

// extractOpenAIResponseUsage extracts usage counters from the raw OpenAI
// Responses response JSON. All three counters must be present as
// non-negative integers, each ≤ 1e6, and input+output==total.
func extractOpenAIResponseUsage(raw json.RawMessage) (sdk.Usage, bool) {
	if len(raw) == 0 {
		return sdk.Usage{}, false
	}
	var wrapper struct {
		Usage *struct {
			InputTokens  *int64 `json:"input_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
			TotalTokens  *int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return sdk.Usage{}, false
	}
	if wrapper.Usage == nil {
		return sdk.Usage{}, false
	}
	u := wrapper.Usage
	if u.InputTokens == nil || u.OutputTokens == nil || u.TotalTokens == nil {
		return sdk.Usage{}, false
	}
	if *u.InputTokens < 0 || *u.OutputTokens < 0 || *u.TotalTokens < 0 {
		return sdk.Usage{}, false
	}
	usage := sdk.Usage{
		PromptTokens:     uint64(*u.InputTokens),
		CompletionTokens: uint64(*u.OutputTokens),
		TotalTokens:      uint64(*u.TotalTokens),
	}
	if !usage.Valid() {
		return sdk.Usage{}, false
	}
	return usage, true
}

// Known Responses streaming event type prefixes. The type field is required
// and must match one of these patterns for the chunk to be accepted.
var responseEventTypes = map[string]streaming.EventKind{
	// Terminal success.
	"response.completed": streaming.EventFinish,
	// Terminal failure.
	"response.failed":     streaming.EventNativeError,
	"response.incomplete": streaming.EventNativeError,
	"error":               streaming.EventNativeError,
	// Semantic content deltas.
	"response.output_text.delta":             streaming.EventSemantic,
	"response.output_text.done":              streaming.EventLifecycle,
	"response.reasoning_text.delta":          streaming.EventSemantic,
	"response.reasoning_text.done":           streaming.EventLifecycle,
	"response.reasoning_summary_text.delta":  streaming.EventSemantic,
	"response.reasoning_summary_text.done":   streaming.EventLifecycle,
	"response.refusal.delta":                 streaming.EventSemantic,
	"response.refusal.done":                  streaming.EventLifecycle,
	"response.audio.delta":                   streaming.EventSemantic,
	"response.audio.done":                    streaming.EventLifecycle,
	"response.audio.transcript.delta":        streaming.EventSemantic,
	"response.audio.transcript.done":         streaming.EventLifecycle,
	"response.function_call_arguments.delta": streaming.EventSemantic,
	"response.function_call_arguments.done":  streaming.EventLifecycle,
	"response.mcp_call_arguments.delta":      streaming.EventSemantic,
	"response.mcp_call_arguments.done":       streaming.EventLifecycle,
	"response.custom_tool_call_input.delta":  streaming.EventSemantic,
	"response.custom_tool_call_input.done":   streaming.EventLifecycle,
	// Lifecycle events (no semantic content, no idle-timer reset).
	"response.created":                             streaming.EventLifecycle,
	"response.in_progress":                         streaming.EventLifecycle,
	"response.queued":                              streaming.EventLifecycle,
	"response.output_item.added":                   streaming.EventLifecycle,
	"response.output_item.done":                    streaming.EventLifecycle,
	"response.content_part.added":                  streaming.EventLifecycle,
	"response.content_part.done":                   streaming.EventLifecycle,
	"response.file_search_call.in_progress":        streaming.EventLifecycle,
	"response.file_search_call.searching":          streaming.EventLifecycle,
	"response.file_search_call.completed":          streaming.EventLifecycle,
	"response.web_search_call.in_progress":         streaming.EventLifecycle,
	"response.web_search_call.searching":           streaming.EventLifecycle,
	"response.web_search_call.completed":           streaming.EventLifecycle,
	"response.image_generation_call.in_progress":   streaming.EventLifecycle,
	"response.image_generation_call.generating":    streaming.EventLifecycle,
	"response.image_generation_call.completed":     streaming.EventLifecycle,
	"response.image_generation_call.partial_image": streaming.EventLifecycle,
	"response.code_interpreter_call.in_progress":   streaming.EventLifecycle,
	"response.code_interpreter_call.interpreting":  streaming.EventLifecycle,
	"response.code_interpreter_call.completed":     streaming.EventLifecycle,
	"response.code_interpreter_call_code.delta":    streaming.EventSemantic,
	"response.code_interpreter_call_code.done":     streaming.EventLifecycle,
	"response.mcp_call.in_progress":                streaming.EventLifecycle,
	"response.mcp_call.completed":                  streaming.EventLifecycle,
	"response.mcp_call.failed":                     streaming.EventNativeError,
	"response.mcp_list_tools.in_progress":          streaming.EventLifecycle,
	"response.mcp_list_tools.completed":            streaming.EventLifecycle,
	"response.mcp_list_tools.failed":               streaming.EventNativeError,
	"response.reasoning_summary_part.added":        streaming.EventLifecycle,
	"response.reasoning_summary_part.done":         streaming.EventLifecycle,
	"response.output_text.annotation.added":        streaming.EventLifecycle,
}

// parseResponseChunk validates one OpenAI Responses SSE JSON payload and
// returns an owned canonical JSON representation. Unlike parseChunk (which
// validates chat.completion.chunk structure), this parser accepts the
// Responses streaming event format: a top-level object with a required "type"
// field naming the event variant, optional "response" object, and other
// variant-specific fields. Errors carry no provider data.
func parseResponseChunk(raw []byte) (streaming.Event, json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxChunkBytes || !utf8.Valid(raw) {
		return streaming.Event{}, nil, errChunkProtocol
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	nodes := 0
	if err := walkJSON(dec, 0, &nodes); err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	if _, err := dec.Token(); err != io.EOF {
		return streaming.Event{}, nil, errChunkProtocol
	}
	var value any
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	root, ok := value.(map[string]any)
	if !ok {
		return streaming.Event{}, nil, errChunkProtocol
	}

	// In-band error ("error" type or top-level error key) is terminal.
	if _, hasErr := root["error"]; hasErr {
		return streaming.Event{Kind: streaming.EventNativeError, EventType: "error"}, nil, nil
	}

	// type is required and must be a known Responses event type.
	typeVal, ok := root["type"]
	if !ok {
		return streaming.Event{}, nil, errChunkProtocol
	}
	typeStr, ok := typeVal.(string)
	if !ok || len(typeStr) == 0 || len(typeStr) > maxStringBytes {
		return streaming.Event{}, nil, errChunkProtocol
	}

	kind, known := responseEventTypes[typeStr]
	if !known {
		return streaming.Event{}, nil, errChunkProtocol
	}

	ev := streaming.Event{Kind: kind, EventType: typeStr}

	// Extract usage from response.completed events.
	if typeStr == "response.completed" {
		if resp, ok := root["response"]; ok && resp != nil {
			if respMap, ok := resp.(map[string]any); ok {
				if usageVal, ok := respMap["usage"]; ok && usageVal != nil {
					usage, hasUsage, err := parseResponseStreamUsage(usageVal)
					if err == nil && hasUsage {
						ev.Usage = &usage
					}
				}
				// Extract status for finish classification.
				if statusVal, ok := respMap["status"]; ok {
					if statusStr, ok := statusVal.(string); ok {
						ev.FinishReason = statusStr
					}
				}
			}
		}
	}

	// For error-type events, extract safe code if present.
	if kind == streaming.EventNativeError {
		if codeVal, ok := root["code"]; ok {
			if codeStr, ok := codeVal.(string); ok && len(codeStr) <= maxStringBytes {
				ev.EventType = typeStr // preserve the event type for classification
			}
		}
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	return ev, ownedRaw(canonical), nil
}

// parseResponseStreamUsage extracts usage counters from a Responses stream
// event's response.usage object. The field names differ from chat completions
// (input_tokens/output_tokens/total_tokens).
func parseResponseStreamUsage(v any) (streaming.Usage, bool, error) {
	if v == nil {
		return streaming.Usage{}, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return streaming.Usage{}, false, errChunkProtocol
	}
	u := streaming.Usage{}
	for _, x := range []struct {
		k string
		p *int64
	}{{"input_tokens", &u.PromptTokens}, {"output_tokens", &u.CompletionTokens}, {"total_tokens", &u.TotalTokens}} {
		value, yes := m[x.k]
		if !yes {
			return u, false, nil
		}
		n, ok := integer(value)
		if !ok || n < 0 || n > streaming.MaxTotalHardCap {
			return u, false, nil
		}
		*x.p = n
	}
	return u, true, nil
}

// responseChunkSource adapts the SDK's Responses stream into the
// sdk.StreamSource interface, parsing each chunk through a dedicated
// Responses-specific validator (not the Chat Completions parseChunk,
// since Responses events have a completely different structure).
type responseChunkSource struct {
	stream   *ssestream.Stream[responses.ResponseStreamEventUnion]
	cancel   context.CancelFunc
	observer *sseObserver

	mu       sync.Mutex
	closed   bool
	terminal error
	sequence uint64
	nextMu   sync.Mutex
}

func newResponseChunkSource(stream *ssestream.Stream[responses.ResponseStreamEventUnion], options ...any) *responseChunkSource {
	var cancel context.CancelFunc
	var observer *sseObserver
	for _, option := range options {
		switch value := option.(type) {
		case context.CancelFunc:
			cancel = value
		case *sseObserver:
			observer = value
		}
	}
	return &responseChunkSource{stream: stream, cancel: cancel, observer: observer}
}

func (s *responseChunkSource) Next(ctx context.Context) (sdk.StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return sdk.StreamEvent{}, err
	}
	if !s.nextMu.TryLock() {
		return sdk.StreamEvent{}, errConcurrentNext
	}
	defer s.nextMu.Unlock()
	if err := s.getTerminal(); err != nil {
		return sdk.StreamEvent{}, err
	}
	if s.isClosed() {
		return sdk.StreamEvent{}, streaming.ErrEndOfStream
	}

	result := make(chan struct {
		ok  bool
		pan any
	}, 1)
	go func() {
		var r struct {
			ok  bool
			pan any
		}
		func() {
			defer func() { r.pan = recover() }()
			if s.stream != nil {
				r.ok = s.stream.Next()
			}
		}()
		result <- r
	}()
	select {
	case r := <-result:
		if r.pan != nil || !r.ok {
			err := s.classifyTerminal(ctx)
			s.setTerminal(err)
			return sdk.StreamEvent{}, err
		}
		ev, data, err := parseResponseChunk([]byte(s.stream.Current().RawJSON()))
		if err != nil {
			safe := sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
			s.setTerminal(safe)
			return sdk.StreamEvent{}, safe
		}
		s.mu.Lock()
		s.sequence++
		sequence := s.sequence
		s.mu.Unlock()
		ev.Sequence = sequence
		streamEvent := sdk.StreamEvent{Sequence: sequence, Meta: ev, Data: data}
		if ev.Kind == streaming.EventNativeError {
			streamEvent.Classified = sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "stream_error", "protocol")
		}
		return streamEvent, nil
	case <-ctx.Done():
		_ = s.Close()
		return sdk.StreamEvent{}, ctx.Err()
	}
}

func (s *responseChunkSource) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel, stream := s.cancel, s.stream
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stream != nil {
		_ = stream.Close()
	}
	return nil
}

func (s *responseChunkSource) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
func (s *responseChunkSource) getTerminal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal
}
func (s *responseChunkSource) setTerminal(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal == nil {
		s.terminal = err
	}
}

func (s *responseChunkSource) classifyTerminal(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.observer != nil && s.observer.terminalErr() != nil {
		return sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
	}
	if s.stream != nil && s.stream.Err() != nil {
		if errors.Is(s.stream.Err(), context.Canceled) {
			return context.Canceled
		}
		if errors.Is(s.stream.Err(), context.DeadlineExceeded) {
			return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
		}
		return sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	if s.observer == nil || s.observer.cleanEOF() {
		return streaming.ErrEndOfStream
	}
	return sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
}
