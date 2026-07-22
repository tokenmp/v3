// Package executorv1api adapts Executor's Foundation behavior to its v1 HTTP contract.
package executorv1api

import (
	"context"
	"errors"
	"net/http"

	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
)

const notImplementedMessage = "This endpoint is not implemented."

// errFailClosed is returned when a non-stream Adapter has no usable executor.
// It is never serialized verbatim: the renderer reduces it to a safe internal
// error that carries no upstream, request, or credential detail.
var errFailClosed = errSentinel("executorv1api: non-stream executor unavailable")

// errSentinel is a private error type so fail-closed and transport-local
// sentinel errors cannot be matched or unwrapped by callers outside this
// package; only their category (non-nil error) reaches the renderer.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// Adapter implements the generated Executor v1 strict server interface.
//
// The Foundation Adapter (returned by New) only provides health checks; model
// operations return protocol-native 501 errors and never start an event
// stream. A non-stream Adapter (returned by NewNonStream) executes
// CreateChatCompletion and CreateMessage through an injected NonStreamExecutor
// while ListModels, CreateResponse and CreateImage remain 501.
type Adapter struct {
	foundation bool
	nonStream  NonStreamExecutor
	requestIDs RequestIDSource
}

var _ executorv1.StrictServerInterface = (*Adapter)(nil)

// New creates the Foundation Executor v1 strict server adapter.
func New() *Adapter {
	return &Adapter{foundation: true}
}

// Options configures a non-stream Adapter.
type Options struct {
	// Executor runs a single non-stream chat or messages request. A nil or
	// typed-nil value fails closed: no execution is attempted and a safe
	// internal error is rendered.
	Executor NonStreamExecutor
	// RequestIDs supplies the trusted, service-generated request identifier.
	// When nil, an opaque locally-generated identifier is used. The source is
	// invoked at most once per request.
	RequestIDs RequestIDSource
}

// NewNonStream creates an Executor v1 strict server adapter that executes
// non-stream chat/messages/images through the injected executor. ListModels
// and CreateResponse remain protocol-native 501. A nil or typed-nil Executor
// fails closed to a safe internal error.
func NewNonStream(opts Options) *Adapter {
	return &Adapter{nonStream: opts.Executor, requestIDs: opts.RequestIDs}
}

// ExecutorGetHealthz reports the process liveness without accessing external resources.
func (*Adapter) ExecutorGetHealthz(context.Context, executorv1.ExecutorGetHealthzRequestObject) (executorv1.ExecutorGetHealthzResponseObject, error) {
	cacheControl := "no-store"
	return executorv1.ExecutorGetHealthz200JSONResponse{
		Body: executorv1.HealthResponse{Status: executorv1.Ok},
		Headers: executorv1.ExecutorGetHealthz200ResponseHeaders{
			CacheControl: &cacheControl,
		},
	}, nil
}

// ExecutorHeadHealthz reports process liveness without a response body.
func (*Adapter) ExecutorHeadHealthz(context.Context, executorv1.ExecutorHeadHealthzRequestObject) (executorv1.ExecutorHeadHealthzResponseObject, error) {
	cacheControl := "no-store"
	return executorv1.ExecutorHeadHealthz200Response{
		Headers: executorv1.ExecutorHeadHealthz200ResponseHeaders{
			CacheControl: &cacheControl,
		},
	}, nil
}

// ListModels returns an OpenAI-compatible indication that model APIs are not implemented.
func (*Adapter) ListModels(context.Context, executorv1.ListModelsRequestObject) (executorv1.ListModelsResponseObject, error) {
	return executorv1.ListModels501JSONResponse{OpenAINotImplementedJSONResponse: openAINotImplemented()}, nil
}

// CreateChatCompletion either returns the Foundation 501 response, fails closed
// when no executor is wired, or normalizes the captured raw body and executes
// exactly one non-stream chat completion. Streaming requests get a native 501
// without execution; invalid requests get a native 400; the trusted request ID
// is generated at most once and the renderer owns the protocol-native body.
// Cancellation and deadline expiry are request-lifecycle outcomes: after an
// executor call they return the original context error with no response. The
// safe strict handlers intentionally do not write for those errors.
func (a *Adapter) CreateChatCompletion(ctx context.Context, _ executorv1.CreateChatCompletionRequestObject) (executorv1.CreateChatCompletionResponseObject, error) {
	if a.foundation {
		return executorv1.CreateChatCompletion501JSONResponse{OpenAINotImplementedJSONResponse: openAINotImplemented()}, nil
	}
	requestID := a.requestID(ctx)
	if isNilExecutor(a.nonStream) {
		return RenderChatError(errFailClosed), nil
	}
	normalized, err := NormalizeOpenAIChat(ctx, requestID)
	if lifecycleErr := contextLifecycleError(ctx, err); lifecycleErr != nil {
		return nil, lifecycleErr
	}
	if err != nil {
		return RenderChatError(err), nil
	}
	result, err := a.nonStream.Execute(ctx, normalized)
	if lifecycleErr := contextLifecycleError(ctx, err); lifecycleErr != nil {
		return nil, lifecycleErr
	}
	if err != nil {
		return RenderChatError(err), nil
	}
	return RenderChatCompletion(result), nil
}

// CreateMessage is the Anthropic-native counterpart of CreateChatCompletion.
// The trusted request ID is generated at most once and is the only request_id
// ever placed on an Anthropic error body. Canceled or expired requests return
// no response and a lifecycle error, so no request ID or provider data is
// serialized after the client has gone away.
func (a *Adapter) CreateMessage(ctx context.Context, _ executorv1.CreateMessageRequestObject) (executorv1.CreateMessageResponseObject, error) {
	if a.foundation {
		return executorv1.CreateMessage501JSONResponse{AnthropicNotImplementedJSONResponse: anthropicNotImplemented()}, nil
	}
	requestID := a.requestID(ctx)
	if isNilExecutor(a.nonStream) {
		return RenderMessageErrorWithRequestID(errFailClosed, requestID), nil
	}
	normalized, err := NormalizeAnthropicMessages(ctx, requestID)
	if lifecycleErr := contextLifecycleError(ctx, err); lifecycleErr != nil {
		return nil, lifecycleErr
	}
	if err != nil {
		return RenderMessageErrorWithRequestID(err, requestID), nil
	}
	result, err := a.nonStream.Execute(ctx, normalized)
	if lifecycleErr := contextLifecycleError(ctx, err); lifecycleErr != nil {
		return nil, lifecycleErr
	}
	if err != nil {
		return RenderMessageErrorWithRequestID(err, requestID), nil
	}
	return RenderMessageWithRequestID(result, requestID), nil
}

// contextLifecycleError makes cancellation ownership explicit. A server-owned
// deadline is indistinguishable here from a client cancellation, so both are
// deliberately no-write outcomes. Never return an arbitrary executor error:
// generated strict handling must receive only the context sentinel/wrapper.
func contextLifecycleError(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// CreateResponse returns an OpenAI-compatible indication that responses are not implemented.
func (*Adapter) CreateResponse(context.Context, executorv1.CreateResponseRequestObject) (executorv1.CreateResponseResponseObject, error) {
	return executorv1.CreateResponse501JSONResponse{OpenAINotImplementedJSONResponse: openAINotImplemented()}, nil
}

// CreateImage executes one legacy OpenAI Images request through the same
// transport-neutral executor boundary as Chat. It intentionally has no stream
// mode and uses the dedicated 16 MiB response renderer cap.
func (a *Adapter) CreateImage(ctx context.Context, _ executorv1.CreateImageRequestObject) (executorv1.CreateImageResponseObject, error) {
	if a.foundation {
		return imageError(http.StatusNotImplemented, "NOT_IMPLEMENTED", "api_error", notImplementedMessage), nil
	}
	requestID := a.requestID(ctx)
	if isNilExecutor(a.nonStream) {
		return RenderImageError(errFailClosed), nil
	}
	normalized, err := NormalizeOpenAIImages(ctx, requestID)
	if lifecycleErr := contextLifecycleError(ctx, err); lifecycleErr != nil {
		return nil, lifecycleErr
	}
	if err != nil {
		return RenderImageError(err), nil
	}
	result, err := a.nonStream.Execute(ctx, normalized.Request)
	if lifecycleErr := contextLifecycleError(ctx, err); lifecycleErr != nil {
		return nil, lifecycleErr
	}
	if err != nil {
		return RenderImageError(err), nil
	}
	return RenderImage(result, normalized.EffectiveResponseFormat), nil
}

func openAINotImplemented() executorv1.OpenAINotImplementedJSONResponse {
	status := http.StatusNotImplemented
	code := "NOT_IMPLEMENTED"
	response := executorv1.OpenAIErrorResponse{Status: &status}
	response.Error.Message = notImplementedMessage
	response.Error.Type = executorv1.OpenAIErrorResponseErrorTypeApiError
	response.Error.Code = &code
	return executorv1.OpenAINotImplementedJSONResponse(response)
}

func anthropicNotImplemented() executorv1.AnthropicNotImplementedJSONResponse {
	response := executorv1.AnthropicErrorResponse{Type: executorv1.Error}
	response.Error.Message = notImplementedMessage
	response.Error.Type = executorv1.AnthropicErrorResponseErrorTypeApiError
	return executorv1.AnthropicNotImplementedJSONResponse(response)
}
