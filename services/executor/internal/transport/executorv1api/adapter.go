// Package executorv1api adapts Executor's Foundation behavior to its v1 HTTP contract.
package executorv1api

import (
	"context"
	"net/http"

	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
)

const notImplementedMessage = "This endpoint is not implemented."

// Adapter implements the generated Executor v1 strict server interface.
//
// The Foundation only provides health checks. Model operations deliberately
// return protocol-native 501 errors and never start an event stream.
type Adapter struct{}

var _ executorv1.StrictServerInterface = (*Adapter)(nil)

// New creates an Executor v1 strict server adapter.
func New() *Adapter {
	return &Adapter{}
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

// CreateChatCompletion returns an OpenAI-compatible indication that chat completion is not implemented.
func (*Adapter) CreateChatCompletion(context.Context, executorv1.CreateChatCompletionRequestObject) (executorv1.CreateChatCompletionResponseObject, error) {
	return executorv1.CreateChatCompletion501JSONResponse{OpenAINotImplementedJSONResponse: openAINotImplemented()}, nil
}

// CreateMessage returns an Anthropic-compatible indication that messages are not implemented.
func (*Adapter) CreateMessage(context.Context, executorv1.CreateMessageRequestObject) (executorv1.CreateMessageResponseObject, error) {
	return executorv1.CreateMessage501JSONResponse{AnthropicNotImplementedJSONResponse: anthropicNotImplemented()}, nil
}

// CreateResponse returns an OpenAI-compatible indication that responses are not implemented.
func (*Adapter) CreateResponse(context.Context, executorv1.CreateResponseRequestObject) (executorv1.CreateResponseResponseObject, error) {
	return executorv1.CreateResponse501JSONResponse{OpenAINotImplementedJSONResponse: openAINotImplemented()}, nil
}

// CreateImage returns an OpenAI-compatible indication that image generation is not implemented.
func (*Adapter) CreateImage(context.Context, executorv1.CreateImageRequestObject) (executorv1.CreateImageResponseObject, error) {
	return executorv1.CreateImage501JSONResponse{OpenAINotImplementedJSONResponse: openAINotImplemented()}, nil
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
