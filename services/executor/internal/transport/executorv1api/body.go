package executorv1api

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

const (
	// MaxCapturedBodyBytes is deliberately no larger than the strict JSON and
	// provider-adapter limits. Keep the body boundary before generated decoding.
	MaxCapturedBodyBytes  int64 = 2 << 20
	openAIChatPath              = "/v1/chat/completions"
	openAIImagesPath            = "/v1/images/generations"
	openAIResponsesPath         = "/v1/responses"
	anthropicMessagesPath       = "/v1/messages"
)

type rawBodyContextKey struct{}

// CaptureRawBody bounds and copies business POST request bodies before the
// generated handler decodes them. The immutable context copy is restored into
// r.Body so generated decoding continues to see the original bytes.
//
// It deliberately avoids http.MaxBytesReader: under a real net/http Server,
// MaxBytesReader dispatches the internal response.requestTooLarge() and
// pre-writes a generic 413 to the live ResponseWriter before this package can
// emit its protocol-native 400. Instead an explicit bounded read of at most
// Max+1 bytes is performed, and an oversized body is detected purely by
// length so the package retains full ownership of the status and body.
func CaptureRawBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !isCapturedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, MaxCapturedBodyBytes+1))
		if err != nil {
			writeBodyCaptureError(w, r.URL.Path)
			return
		}
		if int64(len(body)) > MaxCapturedBodyBytes {
			writeBodyCaptureError(w, r.URL.Path)
			return
		}
		// Generated decoding/defaulting is not trusted to preserve its input
		// bytes. Keep the capture-owned context slice isolated from r.Body so the
		// normalizer always sees exact client bytes.
		r.Body = io.NopCloser(bytes.NewReader(append([]byte(nil), body...)))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), rawBodyContextKey{}, body)))
	})
}

func isCapturedPath(path string) bool {
	return path == openAIChatPath || path == openAIImagesPath || path == anthropicMessagesPath || path == openAIResponsesPath
}

// RawBody returns an independent copy of the body captured by CaptureRawBody.
// It is intentionally unavailable for uncaptured paths.
func RawBody(ctx context.Context) ([]byte, bool) {
	body, ok := rawBodyView(ctx)
	if !ok {
		return nil, false
	}
	return append([]byte(nil), body...), true
}

// rawBodyView returns the capture-owned immutable slice without copying. It is
// private so only this package can use it; callers that cross the package
// boundary must use RawBody, which returns an independent copy.
func rawBodyView(ctx context.Context) ([]byte, bool) {
	body, ok := ctx.Value(rawBodyContextKey{}).([]byte)
	return body, ok
}

func writeBodyCaptureError(w http.ResponseWriter, path string) {
	w.Header().Set("Content-Type", "application/json")
	if path == openAIImagesPath {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(http.StatusBadRequest)
	switch path {
	case openAIChatPath, openAIImagesPath, openAIResponsesPath:
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid request body.","type":"invalid_request_error","code":"INVALID_REQUEST"},"status":400}`)
	case anthropicMessagesPath:
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid request body."}}`)
	}
}
