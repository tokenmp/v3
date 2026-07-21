// Package nonstream is the transport-neutral boundary for one non-stream
// Executor request. It owns the request/result shapes, the narrow Executor
// port, the authenticated caller Principal, and the safe sentinel errors a
// transport renderer reduces to protocol-native responses.
//
// The package imports no HTTP, chi, generated contract, or transport code: it
// depends only on the adapter (Protocol/ThinkingRequest) and execution (Result)
// domain packages. A transport-facing facade composes against this port and is
// the only caller permitted to construct a Request carrying a trusted Principal.
package nonstream

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
)

// Principal is the secret-free, transport-neutral authenticated caller value
// carried on a Request. It is derived by the transport auth boundary from a
// trusted identity and defensively revalidated by any facade before routing,
// reservation, or execution. It never carries API key material.
//
// Role and Status are plain strings (not the identity package's typed Role/
// Status) so this package remains decoupled from the identity port; the
// canonical values are the Role*/Status* constants below, which mirror
// identity.RoleService/RoleAdmin/StatusActive.
type Principal struct {
	Subject string
	KeyID   string
	Role    string
	Status  string
}

// Canonical role values accepted by a facade's defensive revalidation.
const (
	RoleService = "service"
	RoleAdmin   = "admin"
)

// Canonical status values accepted by a facade's defensive revalidation.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Request is the protocol-normalized input to one non-streaming execution.
// Body retains the exact validated client bytes; callers must not use a typed
// request value to recreate it. Principal is the only authenticated caller
// value the execution path may read; it is never serialized into an upstream
// call and carries no key material.
type Request struct {
	Protocol  adapter.Protocol
	Selector  string
	Body      json.RawMessage
	Thinking  adapter.ThinkingRequest
	RequestID string
	Principal Principal
}

// Result aliases the internal execution result so the transport and facade do
// not duplicate or construct routing and execution state. It is returned only
// after the quota terminal action is confirmed.
type Result = execution.Result

// Executor is the narrow execution boundary used by a transport-facing facade.
// Routing, identity, quota, and reservation ownership deliberately remain on
// the other side of this port: an Executor receives a fully validated,
// principal-carrying Request and returns a Result or a safe sentinel error.
type Executor interface {
	Execute(context.Context, Request) (Result, error)
}

// Safe sentinel errors. None carries a selector, snapshot, routing, request,
// response, credential, or upstream detail. A transport renderer reduces them
// to protocol-native responses; callers must not unwrap or string-match them.
var (
	// ErrModelNotFound means no enabled, non-quarantined route resolved for the
	// requested model and protocol. It is rendered as a protocol-native 404.
	ErrModelNotFound = errors.New("nonstream: model not found")

	// ErrInvalidRequest means the request selector or normalized request was
	// syntactically invalid. It is rendered as a protocol-native 400.
	ErrInvalidRequest = errors.New("nonstream: invalid request")

	// ErrUnauthorized means the request carried no trusted authenticated
	// Principal or the Principal failed defensive revalidation (active status,
	// service/admin role, non-empty bounded subject/keyID). It is rendered as a
	// protocol-native 401. Reaching the execution boundary without a valid
	// Principal is a composition fault: the outer transport auth boundary must
	// already reject unauthenticated traffic, so this is defense-in-depth.
	ErrUnauthorized = errors.New("nonstream: unauthorized")

	// ErrMisconfigured means a required facade or execution dependency is nil or
	// typed-nil. It is returned before any snapshot read, identity check,
	// reservation, routing, or upstream call, and is rendered as a safe internal
	// error.
	ErrMisconfigured = errors.New("nonstream: misconfigured")
)
