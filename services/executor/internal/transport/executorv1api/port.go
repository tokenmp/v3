package executorv1api

import (
	"context"
	"encoding/json"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
)

// NonStreamExecutor is the narrow execution boundary used by the HTTP
// transport. Routing, identity, and quota ownership deliberately remain on
// the other side of this port.
type NonStreamExecutor interface {
	Execute(context.Context, NonStreamRequest) (NonStreamResult, error)
}

// NonStreamRequest is the protocol-normalized input to one non-streaming
// execution. Body retains the exact validated client bytes; callers must not
// use a typed request value to recreate it.
type NonStreamRequest struct {
	Protocol  adapter.Protocol
	Selector  string
	Body      json.RawMessage
	Thinking  adapter.ThinkingRequest
	RequestID string
}

// NonStreamResult aliases the internal execution result so the transport does
// not duplicate or construct routing and execution state.
type NonStreamResult = execution.Result

// RequestIDSource supplies a request-scoped, safe request identifier. Identity
// and idempotency are intentionally outside this transport-only package.
type RequestIDSource interface {
	RequestID(context.Context) string
}

// RequestIDSourceFunc adapts a function to RequestIDSource.
type RequestIDSourceFunc func(context.Context) string

// RequestID implements RequestIDSource.
func (f RequestIDSourceFunc) RequestID(ctx context.Context) string { return f(ctx) }
