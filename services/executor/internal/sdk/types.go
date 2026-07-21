// Package sdk defines the executor upstream SDK port: the stable,
// protocol-safe data types and the [Client] interface that a provider
// adapter (such as internal/sdk/openaiadapter) implements.
//
// The port is deliberately narrow. It carries no provider response body,
// request body, URL, header, or secret in any observer-facing or
// error-facing surface, and it intentionally exposes no streaming entry
// point: a [Client] can only [Client.Complete] one non-streaming call.
// Provider failures are reduced to a [ClassifiedError] whose Error() string
// is a fixed category only and never echoes an upstream message, code, type,
// URL, or raw JSON body.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// Target is the provider-specific destination selected for an attempt.
// BaseURL is not emitted to observers; it is supplied only to the SDK client.
type Target struct {
	BaseURL       string
	UpstreamModel string
	Protocol      adapter.Protocol
}

// CandidateIdentity is the non-secret identity of a selected route. It is
// intentionally independent from routing.Candidate so this port does not make
// the adapter implementation depend on routing's resolver and plan internals.
type CandidateIdentity struct {
	ModelID      string
	ProviderID   string
	RouteID      string
	CredentialID string
	AdapterID    string
}

// AttemptMetadata is the complete information an attempt observer may receive.
// It contains identifiers and protocol only: no URL, request content, response
// content, headers, credentials, or credential references.
type AttemptMetadata struct {
	CandidateIdentity
	Protocol adapter.Protocol
}

// CredentialSecret is an opaque credential value. Construct it with
// NewCredentialSecret; the input is copied. It intentionally has no method
// that returns a string or byte slice. Use permits a provider implementation to
// use a temporary copy and clears that copy when the callback returns.
//
// Callers must not retain or mutate the callback value.
type CredentialSecret struct{ value []byte }

// NewCredentialSecret copies value so future mutations of the caller's slice
// cannot affect an SDK call.
func NewCredentialSecret(value []byte) CredentialSecret {
	return CredentialSecret{value: append([]byte(nil), value...)}
}

// Use invokes fn with a temporary credential copy, then clears that copy. It
// is the only way an adapter implementation can access the secret material.
func (s CredentialSecret) Use(fn func([]byte) error) error {
	if fn == nil {
		return errors.New("sdk: credential use callback is nil")
	}
	value := append([]byte(nil), s.value...)
	defer clear(value)
	return fn(value)
}

// String, GoString, and Format prevent accidental secret disclosure through
// ordinary formatting, including formatting a Call with %+v or %#v. They always
// return a fixed redacted marker and never the secret material.
func (CredentialSecret) String() string   { return "[REDACTED]" }
func (CredentialSecret) GoString() string { return "sdk.CredentialSecret([REDACTED])" }
func (CredentialSecret) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("[REDACTED]"))
}

// Call is the complete input for one non-streaming upstream SDK invocation.
// AppliedRequest is the already-transformed adapter result; the adapter must
// not re-run the adapter DSL.
type Call struct {
	Candidate CandidateIdentity
	Target    Target
	Request   adapter.AppliedRequest
	Secret    CredentialSecret
}

// Completion is the raw successful non-streaming provider result. RawJSON is
// retained for the protocol renderer; callers must not put it in errors or
// attempt-observer metadata. Status and RequestID are copied from the HTTP
// response metadata only; no response body or *http.Response escapes.
type Completion struct {
	RawJSON   json.RawMessage
	Status    int
	RequestID string
}

// Client is the upstream SDK port. A provider adapter implements it to perform
// exactly one non-streaming call per [Client.Complete]. The port intentionally
// exposes no streaming entry point.
type Client interface {
	Complete(ctx context.Context, call Call) (Completion, error)
}

// Classified error kinds are safe categories for retry and response mapping.
// They intentionally carry no provider response, request, body, or secret.
//
// The HTTP-status categories (ErrUnauthorized, ErrForbidden, ErrNotFound,
// ErrRateLimited, ErrUnavailable, ErrUpstream) classify an upstream HTTP
// response. ErrTimeout, ErrProtocol, and ErrTransport classify non-HTTP
// failures (deadline exceeded, protocol violation, transport/connection
// failure) so a retry layer can distinguish them from HTTP-status outcomes.
var (
	ErrTimeout      = errors.New("upstream timeout")
	ErrProtocol     = errors.New("upstream protocol error")
	ErrTransport    = errors.New("upstream transport error")
	ErrUnauthorized = errors.New("upstream unauthorized")
	ErrForbidden    = errors.New("upstream forbidden")
	ErrNotFound     = errors.New("upstream not found")
	ErrRateLimited  = errors.New("upstream rate limited")
	ErrUnavailable  = errors.New("upstream unavailable")
	ErrUpstream     = errors.New("upstream error")
)

// ClassifiedError carries only safe, sanitized upstream metadata: a fixed
// category, HTTP status, provider request ID, and the upstream error code and
// type. Its fields are private so callers cannot attach a response, request,
// body, secret, or arbitrary provider error to it. Error() returns the fixed
// category string only and never echoes the upstream message, raw JSON body,
// code, type, URL, or secret; Code() and Type() expose the sanitized upstream
// code/type identifiers ([A-Za-z0-9_-]) for retry/response mapping.
type ClassifiedError struct {
	kind       error
	causeClass error
	status     int
	requestID  string
	code       string
	typ        string
}

// NewClassifiedError returns a classified error. Unknown kinds are reduced to
// ErrUpstream rather than retained, so an arbitrary provider error cannot leak
// through Error or Unwrap. requestID is sanitized by [SafeRequestID] (valid
// UTF-8, no control characters, bounded by [maxSafeTokenBytes]; it accepts
// printable ASCII punctuation and printable Unicode, explicitly including '.',
// ':', '/'); code and typ are sanitized by [safeToken] to the stricter
// identifier subset ([A-Za-z0-9_-]). Anything outside the respective policy is
// reduced to empty so a misbehaving upstream cannot flood or inject arbitrary
// content into classified metadata. The upstream Message is never a parameter
// and is therefore never retained.
func NewClassifiedError(kind error, status int, requestID, code, typ string) *ClassifiedError {
	var causeClass error
	switch {
	case errors.Is(kind, context.DeadlineExceeded):
		// Keep the deadline signal available to control flow while exposing the
		// safe retry category. The original error itself is never retained.
		kind = ErrTimeout
		causeClass = context.DeadlineExceeded
	case errors.Is(kind, ErrTimeout):
		kind = ErrTimeout
	case errors.Is(kind, ErrProtocol):
		kind = ErrProtocol
	case errors.Is(kind, ErrTransport):
		kind = ErrTransport
	case errors.Is(kind, ErrUnauthorized):
		kind = ErrUnauthorized
	case errors.Is(kind, ErrForbidden):
		kind = ErrForbidden
	case errors.Is(kind, ErrNotFound):
		kind = ErrNotFound
	case errors.Is(kind, ErrRateLimited):
		kind = ErrRateLimited
	case errors.Is(kind, ErrUnavailable):
		kind = ErrUnavailable
	default:
		kind = ErrUpstream
	}
	return &ClassifiedError{
		kind:       kind,
		causeClass: causeClass,
		status:     status,
		requestID:  SafeRequestID(requestID),
		code:       safeToken(code),
		typ:        safeToken(typ),
	}
}

func (e *ClassifiedError) Error() string {
	if e == nil || e.kind == nil {
		return ErrUpstream.Error()
	}
	return e.kind.Error()
}

// Unwrap makes classified errors usable with errors.Is for their safe kind.
func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.kind
}

// Is additionally preserves context.DeadlineExceeded classification without
// retaining the original error (which may include unsafe transport details).
func (e *ClassifiedError) Is(target error) bool {
	if e == nil {
		return false
	}
	return errors.Is(e.kind, target) || (e.causeClass != nil && target == e.causeClass)
}

// Status reports the safe upstream HTTP status, if one is known.
func (e *ClassifiedError) Status() int {
	if e == nil {
		return 0
	}
	return e.status
}

// RequestID reports the safe provider request identifier, if one is known. It
// is restricted to valid UTF-8 with no control characters and bounded in length
// by [maxSafeTokenBytes]; it accepts printable ASCII punctuation and printable
// Unicode (explicitly including '.', ':', '/') so common request-ID formats
// (dot-separated, namespaced, path-like, and non-ASCII identifiers) are
// retained. Anything outside that policy is reduced to empty.
func (e *ClassifiedError) RequestID() string {
	if e == nil {
		return ""
	}
	return e.requestID
}

// Code reports the sanitized upstream error code, if one is known. It is
// restricted to [A-Za-z0-9_-] and bounded in length; an upstream value outside
// that set is reduced to empty. It never echoes the upstream Message or raw
// JSON body.
func (e *ClassifiedError) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

// Type reports the sanitized upstream error type, if one is known. It is
// restricted to [A-Za-z0-9_-] and bounded in length; an upstream value outside
// that set is reduced to empty. It never echoes the upstream Message or raw
// JSON body.
func (e *ClassifiedError) Type() string {
	if e == nil {
		return ""
	}
	return e.typ
}

// ToUpstreamResponse maps the classified error into an [adapter.UpstreamResponse]
// for the response-mapping engine. The upstream Message is intentionally left
// empty: a classified error never retains or surfaces the upstream message, so
// no remote user data or PII can flow into the mapped response.
func (e *ClassifiedError) ToUpstreamResponse() adapter.UpstreamResponse {
	if e == nil {
		return adapter.UpstreamResponse{}
	}
	code, typ := e.code, e.typ
	switch e.kind {
	case ErrTimeout:
		code, typ = "timeout", "timeout"
	case ErrTransport:
		code, typ = "transport", "transport"
	case ErrProtocol:
		code, typ = "protocol", "protocol"
	}
	return adapter.UpstreamResponse{
		HTTPStatus: e.status,
		ErrorCode:  code,
		ErrorType:  typ,
		Message:    "",
	}
}

// AttemptObserver observes the start of an actual upstream HTTP attempt.
type AttemptObserver interface {
	OnAttempt(context.Context, AttemptMetadata)
}

// maxSafeTokenBytes bounds the length of a sanitized upstream
// code/type/request-ID token so a misbehaving upstream can never flood observer
// or error metadata.
const maxSafeTokenBytes = 128

// safeToken sanitizes an upstream-supplied code or type to the safe identifier
// subset consisting only of [A-Za-z0-9_-]. Anything outside that set reduces
// the result to empty rather than risk surfacing arbitrary remote content.
func safeToken(v string) string {
	if len(v) == 0 || len(v) > maxSafeTokenBytes {
		return ""
	}
	for _, r := range v {
		if !isSafeIdentRune(r) {
			return ""
		}
	}
	return v
}

// isSafeIdentRune reports whether r is permitted in a safe upstream-supplied
// code/type identifier. The set is deliberately small (only [A-Za-z0-9_-]).
func isSafeIdentRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return r == '-' || r == '_'
}

// SafeRequestID sanitizes an upstream-supplied request ID (such as an
// x-request-id response header). It is the shared request-ID policy for both
// the success path ([sdk.Completion].RequestID) and the failure path
// ([ClassifiedError].RequestID via [NewClassifiedError]): valid UTF-8, no
// control characters, and byte length bounded by [maxSafeTokenBytes] (non-empty).
// It does NOT impose the old [A-Za-z0-9_-] identifier allowlist: all printable
// ASCII punctuation and printable Unicode are accepted, explicitly including
// '.', ':', '/' so common request-ID formats (dot-separated, namespaced,
// path-like, and non-ASCII identifiers) are retained. Anything outside that
// policy is reduced to empty so a misbehaving upstream cannot flood or inject
// arbitrary content into classified metadata or the completion RequestID.
func SafeRequestID(v string) string {
	if len(v) == 0 || len(v) > maxSafeTokenBytes || !utf8.ValidString(v) {
		return ""
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return v
}
