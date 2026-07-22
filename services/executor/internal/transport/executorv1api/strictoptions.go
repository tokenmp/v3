package executorv1api

import (
	"context"
	"errors"
	"net/http"

	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
)

// SafeStrictOptions returns StrictHTTPServerOptions whose request and response
// error handlers render protocol-native, non-leaking errors. The generated
// strict server's default handlers write err.Error() verbatim as plain text,
// which would leak decode/transport detail and violate the OpenAPI error
// contract. Context cancellation and deadline expiry are the sole exception:
// they are lifecycle outcomes and deliberately write nothing. This includes
// server-owned deadlines because this transport cannot distinguish ownership.
//
// The handlers are path-aware so /v1/messages receives an Anthropic-native
// body and every other model path receives an OpenAI-native body. They are
// safe to use with any Adapter (Foundation or non-stream).
func SafeStrictOptions() executorv1.StrictHTTPServerOptions {
	return executorv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  renderStrictRequestError,
		ResponseErrorHandlerFunc: renderStrictResponseError,
	}
}

func renderStrictRequestError(w http.ResponseWriter, r *http.Request, err error) {
	if isContextLifecycleError(err) {
		return
	}
	if r.URL.Path == openAIImagesPath {
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.URL.Path == anthropicMessagesPath {
		_ = writeJSON(w, http.StatusBadRequest, anthropicError(invalidErrorCode, "invalid_request_error", invalidErrorMessage, ""))
		return
	}
	_ = writeJSON(w, http.StatusBadRequest, openAIError(http.StatusBadRequest, invalidErrorCode, "invalid_request_error", invalidErrorMessage))
}

func renderStrictResponseError(w http.ResponseWriter, r *http.Request, err error) {
	if isContextLifecycleError(err) {
		return
	}
	if r.URL.Path == openAIImagesPath {
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.URL.Path == anthropicMessagesPath {
		_ = writeJSON(w, http.StatusInternalServerError, anthropicError(internalErrorCode, "api_error", internalErrorMessage, ""))
		return
	}
	_ = writeJSON(w, http.StatusInternalServerError, openAIError(http.StatusInternalServerError, internalErrorCode, "api_error", internalErrorMessage))
}

func isContextLifecycleError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
