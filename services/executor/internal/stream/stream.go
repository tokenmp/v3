// Package stream is the transport-neutral boundary for one streaming Executor
// request. It owns the normalized request shape, narrow Executor port, and
// safe sentinel aliases shared with the non-stream boundary.
package stream

import (
	"context"
	"encoding/json"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
)

// Principal is the secret-free authenticated caller value shared with the
// non-stream boundary. It never carries API key material.
type Principal = nonstream.Principal

const (
	RoleService    = nonstream.RoleService
	RoleAdmin      = nonstream.RoleAdmin
	StatusActive   = nonstream.StatusActive
	StatusDisabled = nonstream.StatusDisabled
)

// Request is the protocol-normalized input to one streaming execution. Sink
// owns protocol rendering; it is deliberately outside the streaming core.
type Request struct {
	Protocol  adapter.Protocol
	Selector  string
	Body      json.RawMessage
	Thinking  adapter.ThinkingRequest
	RequestID string
	Principal Principal
	Sink      execution.ProtocolSink
}

// Result aliases the internal stream lifecycle result.
type Result = execution.StreamResult

// Executor is the narrow streaming execution boundary used by transports.
type Executor interface {
	Execute(context.Context, Request) (Result, error)
}

// Safe sentinel aliases avoid divergent transport error classifications.
var (
	ErrModelNotFound  = nonstream.ErrModelNotFound
	ErrInvalidRequest = nonstream.ErrInvalidRequest
	ErrUnauthorized   = nonstream.ErrUnauthorized
	ErrMisconfigured  = nonstream.ErrMisconfigured
)
