package executorv1api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
	"github.com/tokenmp/v3/services/executor/internal/imagecontract"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

const (
	internalErrorCode             = "INTERNAL_ERROR"
	internalErrorMessage          = "An internal error occurred."
	invalidErrorCode              = "INVALID_REQUEST"
	invalidErrorMessage           = "Invalid request."
	unauthorizedChatCode          = "invalid_api_key"
	unauthorizedChatMessage       = "Invalid API key provided."
	unauthorizedAnthropicCode     = "authentication_error"
	unauthorizedAnthropicMessage  = "invalid x-api-key"
	streamErrorCode               = "NOT_IMPLEMENTED"
	streamErrorMessage            = "Streaming is not supported."
	modelNotFoundChatCode         = "model_not_found"
	modelNotFoundChatMessage      = "The requested model does not exist."
	modelNotFoundAnthropicCode    = "not_found_error"
	modelNotFoundAnthropicMessage = "model: not found"
	upstreamErrorCode             = "UPSTREAM_ERROR"
	upstreamErrorMessage          = "Upstream request failed."

	maxRenderTokenBytes   = 128
	maxRenderMessageBytes = 512
	maxImageRenderBytes   = 16 << 20
)

// RenderChatCompletion converts a terminal non-stream execution outcome into
// the native Chat Completions response object. A successful completion is
// passed through byte-for-byte only after a bounded local contract check.
func RenderChatCompletion(result NonStreamResult) executorv1.CreateChatCompletionResponseObject {
	if validCompletion(result, validChatCompletion) {
		return chatRawResponse{body: append([]byte(nil), result.Completion.RawJSON...)}
	}
	if result.Failure != nil && completionAbsent(result) {
		return chatFailure(*result.Failure)
	}
	return chatError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

// RenderMessage converts a terminal non-stream execution outcome into the
// native Messages response object. request_id is emitted only from the SDK
// sanitized completion metadata, never from an error or raw provider body.
// RenderImage passes through a provider result only after a dedicated bounded
// Images response validation. The SDK repeats stronger provider validation,
// but this boundary must also reject malformed injected port results.
func RenderImage(result NonStreamResult, effectiveResponseFormat string) executorv1.CreateImageResponseObject {
	if result.Failure == nil && result.Completion.Status == http.StatusOK && validImageSuccess(result.Completion.RawJSON, effectiveResponseFormat) {
		// Completion.RawJSON is already exclusively owned by the execution result.
		// Keep that one validated slice; serialization writes it exactly once.
		return imageRawResponse{body: result.Completion.RawJSON}
	}
	if result.Failure != nil && completionAbsent(result) {
		return imageFailure(*result.Failure)
	}
	return imageError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

func RenderMessage(result NonStreamResult) executorv1.CreateMessageResponseObject {
	return RenderMessageWithRequestID(result, "")
}

// RenderMessageWithRequestID is RenderMessage with the trusted request ID
// issued by this service. It is used for Anthropic errors because their
// protocol body carries request_id; it never accepts an upstream raw value.
func RenderMessageWithRequestID(result NonStreamResult, requestID string) executorv1.CreateMessageResponseObject {
	if validCompletion(result, validMessage) {
		return messageRawResponse{body: append([]byte(nil), result.Completion.RawJSON...)}
	}
	if result.Failure != nil && completionAbsent(result) {
		return messageFailure(*result.Failure, requestID)
	}
	return messageError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage, requestID)
}

// RenderChatStreamResult renders the only pre-commit terminal stream outcome
// that may be sent as JSON. Once an SSE sink has committed, the hybrid handler
// deliberately ignores this response and never appends JSON to the stream.
func RenderChatStreamResult(result StreamResult) executorv1.CreateChatCompletionResponseObject {
	if result.Failure != nil {
		return chatFailure(*result.Failure)
	}
	return chatError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

// RenderMessageStreamResult is the Anthropic-native counterpart of
// RenderChatStreamResult. request_id is trusted service-local data only.
func RenderMessageStreamResult(result StreamResult, requestID string) executorv1.CreateMessageResponseObject {
	if result.Failure != nil {
		return messageFailure(*result.Failure, requestID)
	}
	return messageError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage, requestID)
}

func writeChatResponse(w http.ResponseWriter, response executorv1.CreateChatCompletionResponseObject) {
	if response != nil {
		_ = response.VisitCreateChatCompletionResponse(w)
	}
}

func writeMessageResponse(w http.ResponseWriter, response executorv1.CreateMessageResponseObject) {
	if response != nil {
		_ = response.VisitCreateMessageResponse(w)
	}
}

func writeResponseResponse(w http.ResponseWriter, response executorv1.CreateResponseResponseObject) {
	if response != nil {
		_ = response.VisitCreateResponseResponse(w)
	}
}

// RenderChatError renders a safe local transport error. A canceled or expired
// request returns nil: the HTTP adapter owns those request-lifecycle outcomes
// and must not try to write a second response.
func RenderChatError(err error) executorv1.CreateChatCompletionResponseObject {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if errors.Is(err, nonstream.ErrInvalidRequest) {
		return chatError(http.StatusBadRequest, invalidErrorCode, "invalid_request_error", invalidErrorMessage)
	}
	if errors.Is(err, nonstream.ErrUnauthorized) {
		return chatError(http.StatusUnauthorized, unauthorizedChatCode, "authentication_error", unauthorizedChatMessage)
	}
	if errors.Is(err, nonstream.ErrModelNotFound) {
		return chatError(http.StatusNotFound, modelNotFoundChatCode, "invalid_request_error", modelNotFoundChatMessage)
	}
	if errors.Is(err, ErrStreamingUnsupported) {
		return chatError(http.StatusNotImplemented, streamErrorCode, "api_error", streamErrorMessage)
	}
	return chatError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

// RenderMessageError is RenderChatError's Anthropic-native counterpart.
func RenderImageError(err error) executorv1.CreateImageResponseObject {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if errors.Is(err, nonstream.ErrInvalidRequest) {
		return imageError(http.StatusBadRequest, invalidErrorCode, "invalid_request_error", invalidErrorMessage)
	}
	if errors.Is(err, nonstream.ErrUnauthorized) {
		return imageError(http.StatusUnauthorized, unauthorizedChatCode, "authentication_error", unauthorizedChatMessage)
	}
	if errors.Is(err, nonstream.ErrModelNotFound) {
		return imageError(http.StatusNotFound, modelNotFoundChatCode, "invalid_request_error", modelNotFoundChatMessage)
	}
	return imageError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

func RenderMessageError(err error) executorv1.CreateMessageResponseObject {
	return RenderMessageErrorWithRequestID(err, "")
}

// RenderMessageErrorWithRequestID emits Anthropic's request_id only from the
// trusted service-generated request ID. It does not inspect err for an ID.
func RenderMessageErrorWithRequestID(err error, requestID string) executorv1.CreateMessageResponseObject {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if errors.Is(err, nonstream.ErrInvalidRequest) {
		return messageError(http.StatusBadRequest, invalidErrorCode, "invalid_request_error", invalidErrorMessage, requestID)
	}
	if errors.Is(err, nonstream.ErrUnauthorized) {
		return messageError(http.StatusUnauthorized, unauthorizedAnthropicCode, "authentication_error", unauthorizedAnthropicMessage, requestID)
	}
	if errors.Is(err, nonstream.ErrModelNotFound) {
		return messageError(http.StatusNotFound, modelNotFoundAnthropicCode, "not_found_error", modelNotFoundAnthropicMessage, requestID)
	}
	if errors.Is(err, ErrStreamingUnsupported) {
		return messageError(http.StatusNotImplemented, streamErrorCode, "api_error", streamErrorMessage, requestID)
	}
	return messageError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage, requestID)
}

type chatRawResponse struct{ body []byte }

func (r chatRawResponse) VisitCreateChatCompletionResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

type imageRawResponse struct{ body []byte }

func (r imageRawResponse) VisitCreateImageResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

type messageRawResponse struct{ body []byte }

func (r messageRawResponse) VisitCreateMessageResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

func validCompletion(result NonStreamResult, validate func(map[string]any) bool) bool {
	return result.Failure == nil && result.Completion.Status == http.StatusOK && len(result.Completion.RawJSON) != 0 && validSuccessJSON(result.Completion.RawJSON, validate)
}

func completionAbsent(result NonStreamResult) bool {
	return result.Completion.Status == 0 && len(result.Completion.RawJSON) == 0 && result.Completion.RequestID == ""
}

func validImageSuccess(raw []byte, wanted string) bool {
	if len(raw) == 0 || len(raw) > maxImageRenderBytes || !utf8.Valid(raw) {
		return false
	}
	value, err := parseSuccessJSON(raw)
	if err != nil {
		return false
	}
	root, ok := value.(map[string]any)
	return ok && imagecontract.ValidateResponse(root, wanted)
}

func validSuccessJSON(raw []byte, validate func(map[string]any) bool) bool {
	if len(raw) == 0 || len(raw) > int(MaxCapturedBodyBytes) || !utf8.Valid(raw) {
		return false
	}
	value, err := parseSuccessJSON(raw)
	if err != nil {
		return false
	}
	root, ok := value.(map[string]any)
	return ok && validate(root)
}

// parseSuccessJSON deliberately reuses the request-side depth, node and
// duplicate-key protections. It has no provider-specific dependency and keeps
// an untrusted success body out of generated response serialization.
func parseSuccessJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	value, err := parseJSONValue(context.Background(), decoder, 1, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, err
	}
	return value, nil
}

func validChatCompletion(root map[string]any) bool {
	if !isString(root["id"]) || root["object"] != "chat.completion" || !isString(root["model"]) ||
		!optionalInteger(root, "created") || !validChatUsage(root) {
		return false
	}
	choices, ok := root["choices"].([]any)
	if !ok {
		return false
	}
	for _, value := range choices {
		choice, ok := value.(map[string]any)
		if !ok || !requiredInteger(choice, "index") || !nullableOneOf(choice, "finish_reason", "stop", "length", "tool_calls", "content_filter") {
			return false
		}
		if message, exists := choice["message"]; exists && !validChatMessage(message) {
			return false
		}
		if logprobs, exists := choice["logprobs"]; exists && logprobs != nil {
			if _, ok := logprobs.(map[string]any); !ok {
				return false
			}
		}
	}
	return true
}

func validChatMessage(value any) bool {
	message, ok := value.(map[string]any)
	if !ok || !optionalString(message, "name") || !optionalString(message, "tool_call_id") || !nullableString(message, "reasoning_content") {
		return false
	}
	role, ok := message["role"].(string)
	if !ok || (role != "system" && role != "user" && role != "assistant" && role != "tool") || !validChatContent(message["content"]) {
		return false
	}
	if calls, exists := message["tool_calls"]; exists && calls != nil && !validChatToolCalls(calls) {
		return false
	}
	return true
}

func validChatContent(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	parts, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range parts {
		part, ok := value.(map[string]any)
		if !ok || !requiredOneOf(part, "type", "text", "image_url") || !optionalString(part, "text") {
			return false
		}
		if image, exists := part["image_url"]; exists {
			object, ok := image.(map[string]any)
			if !ok || !optionalString(object, "url") || !optionalOneOf(object, "detail", "auto", "low", "high") {
				return false
			}
		}
	}
	return true
}

func validChatToolCalls(value any) bool {
	calls, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range calls {
		call, ok := value.(map[string]any)
		function, functionOK := call["function"].(map[string]any)
		if !ok || !isString(call["id"]) || call["type"] != "function" || !functionOK ||
			!isString(function["name"]) || !isString(function["arguments"]) {
			return false
		}
	}
	return true
}

func validChatUsage(root map[string]any) bool {
	usage, exists := root["usage"]
	if !exists {
		return true
	}
	value, ok := usage.(map[string]any)
	if !ok || !requiredInteger(value, "prompt_tokens") || !requiredInteger(value, "completion_tokens") || !requiredInteger(value, "total_tokens") {
		return false
	}
	for _, name := range []string{"prompt_tokens_details", "completion_tokens_details"} {
		if details, exists := value[name]; exists {
			object, ok := details.(map[string]any)
			if !ok {
				return false
			}
			for _, known := range []string{"cached_tokens", "reasoning_tokens", "accepted_prediction_tokens", "rejected_prediction_tokens"} {
				if number, exists := object[known]; exists && !jsonNumberInteger(number) {
					return false
				}
			}
		}
	}
	return true
}

func validMessage(root map[string]any) bool {
	if !isString(root["id"]) || root["type"] != "message" || root["role"] != "assistant" || !isString(root["model"]) ||
		!nullableOneOf(root, "stop_reason", "end_turn", "max_tokens", "stop_sequence", "tool_use") || !nullableString(root, "stop_sequence") {
		return false
	}
	content, ok := root["content"].([]any)
	if !ok {
		return false
	}
	for _, block := range content {
		if !validMessageBlock(block) {
			return false
		}
	}
	usage, ok := root["usage"].(map[string]any)
	if !ok || !requiredInteger(usage, "input_tokens") || !requiredInteger(usage, "output_tokens") {
		return false
	}
	for _, name := range []string{"cache_creation_input_tokens", "cache_read_input_tokens"} {
		if _, exists := usage[name]; exists && !optionalInteger(usage, name) {
			return false
		}
	}
	return true
}

func validMessageBlock(value any) bool {
	block, ok := value.(map[string]any)
	if !ok || !requiredOneOf(block, "type", "text", "tool_use", "thinking") ||
		!optionalString(block, "text") || !optionalString(block, "id") || !optionalString(block, "name") ||
		!optionalString(block, "thinking") || !optionalString(block, "signature") {
		return false
	}
	if input, exists := block["input"]; exists {
		if _, ok := input.(map[string]any); !ok {
			return false
		}
	}
	return true
}

func requiredInteger(value map[string]any, name string) bool {
	item, ok := value[name]
	return ok && jsonNumberInteger(item)
}
func optionalInteger(value map[string]any, name string) bool {
	item, ok := value[name]
	return !ok || jsonNumberInteger(item)
}
func nullableString(value map[string]any, name string) bool {
	item, ok := value[name]
	return !ok || item == nil || isString(item)
}
func nullableOneOf(value map[string]any, name string, allowed ...string) bool {
	item, ok := value[name]
	if !ok || item == nil {
		return ok
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
func optionalOneOf(value map[string]any, name string, allowed ...string) bool {
	item, ok := value[name]
	return !ok || oneOf(item, allowed...)
}
func requiredOneOf(value map[string]any, name string, allowed ...string) bool {
	item, ok := value[name]
	return ok && oneOf(item, allowed...)
}
func oneOf(item any, allowed ...string) bool {
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
func jsonNumberInteger(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := number.Int64()
	return err == nil
}
func nonnegativeJSONInteger(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	integer, err := number.Int64()
	return err == nil && integer >= 0
}
func nonEmptyString(value any) bool { text, ok := value.(string); return ok && text != "" }

func chatFailure(failure adapter.MappedResponse) executorv1.CreateChatCompletionResponseObject {
	status, code, typ, message := safeFailure(failure, false)
	return chatErrorWithRetryAfter(status, code, typ, message, retryAfterForMappedStatus(status, failure.RetryAfterSeconds))
}
func imageFailure(failure adapter.MappedResponse) executorv1.CreateImageResponseObject {
	status, code, typ, message := safeFailure(failure, false)
	return imageErrorWithRetryAfter(status, code, typ, message, retryAfterForMappedStatus(status, failure.RetryAfterSeconds))
}
func messageFailure(failure adapter.MappedResponse, requestID string) executorv1.CreateMessageResponseObject {
	status, code, typ, message := safeFailure(failure, true)
	return messageErrorWithRetryAfter(status, code, typ, message, requestID, retryAfterForMappedStatus(status, failure.RetryAfterSeconds))
}

// retryAfterForMappedStatus returns a non-nil *int for 429/529 mapped HTTP
// statuses that carry a positive RetryAfterSeconds. The status is the
// mapped (safe) status from safeFailure, not the raw upstream status.
// Local executor errors never set RetryAfterSeconds, so their retryAfter is
// always nil. Only rate-limit (429) and overloaded (529) mapped statuses are
// eligible; other statuses are ignored even if RetryAfterSeconds is set.
// Values are defensively clamped to [1, 300] (HardMaxRetryAfter seconds).
func retryAfterForMappedStatus(mappedStatus int, retryAfterSeconds int) *int {
	if retryAfterSeconds < 1 || (mappedStatus != 429 && mappedStatus != 529) {
		return nil
	}
	v := retryAfterSeconds
	if v > 300 {
		v = 300
	}
	return &v
}

func safeFailure(failure adapter.MappedResponse, anthropic bool) (int, string, string, string) {
	status := failure.HTTPStatus
	if anthropic {
		if status != 400 && status != 401 && status != 403 && status != 404 && status != 429 && status != 500 && status != 501 && status != 529 {
			return 529, upstreamErrorCode, "overloaded_error", upstreamErrorMessage
		}
	} else if status != 400 && status != 401 && status != 403 && status != 404 && status != 429 && status != 500 && status != 501 && status != 502 {
		return 502, upstreamErrorCode, "api_error", upstreamErrorMessage
	}
	if !safeToken(failure.ErrorCode) || !safeMessage(failure.Message) {
		return fallbackFailure(status, anthropic)
	}
	if anthropic {
		typ := anthropicType(status, failure.ErrorType)
		return status, failure.ErrorCode, typ, failure.Message
	}
	return status, failure.ErrorCode, openAIType(status, failure.ErrorType), failure.Message
}
func fallbackFailure(status int, anthropic bool) (int, string, string, string) {
	if anthropic {
		return status, upstreamErrorCode, anthropicType(status, ""), upstreamErrorMessage
	}
	return status, upstreamErrorCode, openAIType(status, ""), upstreamErrorMessage
}
func safeToken(value string) bool {
	if value == "" || len(value) > maxRenderTokenBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func safeMessage(value string) bool {
	if value == "" || len(value) > maxRenderMessageBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r == '\r' || r == '\n' || r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
func openAIType(status int, value string) string {
	switch status {
	case 400:
		if value == "invalid_request_error" {
			return value
		}
	case 401:
		if value == "authentication_error" {
			return value
		}
	case 403:
		if value == "permission_error" {
			return value
		}
	case 429:
		if value == "rate_limit_error" {
			return value
		}
	}
	return "api_error"
}
func anthropicType(status int, value string) string {
	switch status {
	case 400:
		if value == "invalid_request_error" {
			return value
		}
	case 401:
		if value == "authentication_error" {
			return value
		}
	case 403:
		if value == "permission_error" {
			return value
		}
	case 404:
		if value == "not_found_error" {
			return value
		}
	case 429:
		if value == "rate_limit_error" {
			return value
		}
	case 529:
		if value == "overloaded_error" {
			return value
		}
	}
	return "api_error"
}

func chatError(status int, code, typ, message string) executorv1.CreateChatCompletionResponseObject {
	return chatProtocolError{status: status, body: openAIError(status, code, typ, message)}
}
func chatErrorWithRetryAfter(status int, code, typ, message string, retryAfter *int) executorv1.CreateChatCompletionResponseObject {
	return chatProtocolError{status: status, body: openAIError(status, code, typ, message), retryAfter: retryAfter}
}
func imageError(status int, code, typ, message string) executorv1.CreateImageResponseObject {
	return imageProtocolError{status: status, body: openAIError(status, code, typ, message)}
}
func imageErrorWithRetryAfter(status int, code, typ, message string, retryAfter *int) executorv1.CreateImageResponseObject {
	return imageProtocolError{status: status, body: openAIError(status, code, typ, message), retryAfter: retryAfter}
}
func messageError(status int, code, typ, message, requestID string) executorv1.CreateMessageResponseObject {
	return messageProtocolError{status: status, body: anthropicError(code, typ, message, requestID)}
}
func messageErrorWithRetryAfter(status int, code, typ, message, requestID string, retryAfter *int) executorv1.CreateMessageResponseObject {
	return messageProtocolError{status: status, body: anthropicError(code, typ, message, requestID), retryAfter: retryAfter}
}

type chatProtocolError struct {
	status     int
	body       executorv1.OpenAIErrorResponse
	retryAfter *int
}

func (r chatProtocolError) VisitCreateChatCompletionResponse(w http.ResponseWriter) error {
	if r.retryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*r.retryAfter))
	}
	return writeJSON(w, r.status, r.body)
}

type imageProtocolError struct {
	status     int
	body       executorv1.OpenAIErrorResponse
	retryAfter *int
}

func (r imageProtocolError) VisitCreateImageResponse(w http.ResponseWriter) error {
	w.Header().Set("Cache-Control", "no-store")
	if r.retryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*r.retryAfter))
	}
	return writeJSON(w, r.status, r.body)
}

type messageProtocolError struct {
	status     int
	body       executorv1.AnthropicErrorResponse
	retryAfter *int
}

func (r messageProtocolError) VisitCreateMessageResponse(w http.ResponseWriter) error {
	if r.retryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*r.retryAfter))
	}
	return writeJSON(w, r.status, r.body)
}
func writeJSON(w http.ResponseWriter, status int, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(encoded)
	return err
}
func openAIError(status int, code, typ, message string) executorv1.OpenAIErrorResponse {
	body := executorv1.OpenAIErrorResponse{Status: &status}
	body.Error.Code = &code
	body.Error.Message = message
	body.Error.Type = executorv1.OpenAIErrorResponseErrorType(typ)
	return body
}

// RenderResponse converts a terminal non-stream execution outcome into the
// native OpenAI Responses response object. A successful completion is passed
// through byte-for-byte only after a bounded local contract check.
func RenderResponse(result NonStreamResult) executorv1.CreateResponseResponseObject {
	if validCompletion(result, validResponse) {
		return responseRawResponse{body: append([]byte(nil), result.Completion.RawJSON...)}
	}
	if result.Failure != nil && completionAbsent(result) {
		return responseFailure(*result.Failure)
	}
	return responseError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

// RenderResponseError renders a safe local transport error for the Responses
// endpoint. Cancellation and deadline expiry return nil.
func RenderResponseError(err error) executorv1.CreateResponseResponseObject {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	if errors.Is(err, nonstream.ErrInvalidRequest) {
		return responseError(http.StatusBadRequest, invalidErrorCode, "invalid_request_error", invalidErrorMessage)
	}
	if errors.Is(err, nonstream.ErrUnauthorized) {
		return responseError(http.StatusUnauthorized, unauthorizedChatCode, "authentication_error", unauthorizedChatMessage)
	}
	if errors.Is(err, nonstream.ErrModelNotFound) {
		return responseError(http.StatusNotFound, modelNotFoundChatCode, "invalid_request_error", modelNotFoundChatMessage)
	}
	if errors.Is(err, ErrStreamingUnsupported) {
		return responseError(http.StatusNotImplemented, streamErrorCode, "api_error", streamErrorMessage)
	}
	return responseError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

// RenderResponseStreamResult renders the only pre-commit terminal stream
// outcome that may be sent as JSON. Once an SSE sink has committed, the hybrid
// handler deliberately ignores this response.
func RenderResponseStreamResult(result StreamResult) executorv1.CreateResponseResponseObject {
	if result.Failure != nil {
		return responseFailure(*result.Failure)
	}
	return responseError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage)
}

type responseRawResponse struct{ body []byte }

func (r responseRawResponse) VisitCreateResponseResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(r.body)
	return err
}

func validResponse(root map[string]any) bool {
	if !isString(root["id"]) || root["object"] != "response" {
		return false
	}
	if status, ok := root["status"]; !ok {
		return false
	} else if _, ok := status.(string); !ok {
		return false
	}
	if _, ok := root["output"].([]any); !ok {
		return false
	}
	return validResponseUsage(root["usage"])
}

func validResponseUsage(v any) bool {
	u, ok := v.(map[string]any)
	if !ok {
		return false
	}
	return requiredInteger(u, "input_tokens") && requiredInteger(u, "output_tokens") && requiredInteger(u, "total_tokens")
}

func responseFailure(failure adapter.MappedResponse) executorv1.CreateResponseResponseObject {
	status, code, typ, message := safeFailure(failure, false)
	return responseErrorWithRetryAfter(status, code, typ, message, retryAfterForMappedStatus(status, failure.RetryAfterSeconds))
}

func responseError(status int, code, typ, message string) executorv1.CreateResponseResponseObject {
	return responseProtocolError{status: status, body: openAIError(status, code, typ, message)}
}
func responseErrorWithRetryAfter(status int, code, typ, message string, retryAfter *int) executorv1.CreateResponseResponseObject {
	return responseProtocolError{status: status, body: openAIError(status, code, typ, message), retryAfter: retryAfter}
}

type responseProtocolError struct {
	status     int
	body       executorv1.OpenAIErrorResponse
	retryAfter *int
}

func (r responseProtocolError) VisitCreateResponseResponse(w http.ResponseWriter) error {
	if r.retryAfter != nil {
		w.Header().Set("Retry-After", strconv.Itoa(*r.retryAfter))
	}
	return writeJSON(w, r.status, r.body)
}

func anthropicError(code, typ, message, requestID string) executorv1.AnthropicErrorResponse {
	body := executorv1.AnthropicErrorResponse{Type: executorv1.Error}
	body.Error.Message = message
	body.Error.Type = executorv1.AnthropicErrorResponseErrorType(typ)
	if requestID = sdk.SafeRequestID(requestID); requestID != "" {
		body.RequestId = &requestID
	}
	return body
}
