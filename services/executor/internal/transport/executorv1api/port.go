package executorv1api

import (
	"context"

	"github.com/tokenmp/v3/services/executor/internal/nonstream"
	"github.com/tokenmp/v3/services/executor/internal/stream"
)

// NonStreamExecutor is the transport-side adapter alias for the
// transport-neutral nonstream.Executor port. The HTTP transport accepts and
// returns the transport-neutral shapes; routing, identity, and quota
// ownership remain on the facade side of this boundary.
type NonStreamExecutor = nonstream.Executor

// NonStreamRequest is the protocol-normalized input to one non-streaming
// execution, as produced by the normalizer. It is an alias of the
// transport-neutral nonstream.Request so the transport does not duplicate or
// reconstruct routing and execution state.
type NonStreamRequest = nonstream.Request

// NonStreamResult aliases the internal execution result so the transport does
// not duplicate or construct routing and execution state.
type NonStreamResult = nonstream.Result

// RequestIDSource supplies a request-scoped, safe request identifier. Identity
// and idempotency are intentionally outside this transport-only package.
type RequestIDSource interface {
	RequestID(context.Context) string
}

// RequestIDSourceFunc adapts a function to RequestIDSource.
type RequestIDSourceFunc func(context.Context) string

// RequestID implements RequestIDSource.
func (f RequestIDSourceFunc) RequestID(ctx context.Context) string { return f(ctx) }

// Safe sentinel aliases. They are the transport-visible spellings of the
// transport-neutral nonstream errors so normalizer/renderer/test code can
// reference them unqualified while the renderer matches via errors.Is against
// nonstream.* (the canonical values). They never carry selector, routing,
// request, response, credential, or upstream detail.
var (
	ErrInvalidRequest = nonstream.ErrInvalidRequest
	ErrModelNotFound  = nonstream.ErrModelNotFound
	ErrUnauthorized   = nonstream.ErrUnauthorized
	ErrMisconfigured  = nonstream.ErrMisconfigured
)

// StreamExecutor and related aliases expose the future transport-neutral
// streaming boundary without wiring an HTTP sink or changing current adapter
// behavior.
type StreamExecutor = stream.Executor
type StreamRequest = stream.Request
type StreamResult = stream.Result
