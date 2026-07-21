package sdk

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestCredentialSecret_Redacted(t *testing.T) {
	t.Parallel()
	secret := NewCredentialSecret([]byte("super-secret-value"))
	if got := secret.String(); got != "[REDACTED]" {
		t.Fatalf("String() = %q, want [REDACTED]", got)
	}
	if got := secret.GoString(); !strings.Contains(got, "[REDACTED]") || strings.Contains(got, "super-secret-value") {
		t.Fatalf("GoString() = %q, must be redacted", got)
	}
	// %v, %+v, %#v and %s on the secret itself must never surface the value.
	for _, fmtStr := range []string{"%v", "%+v", "%#v", "%s"} {
		if out := fmt.Sprintf(fmtStr, secret); strings.Contains(out, "super-secret-value") {
			t.Fatalf("Sprintf(%q, secret) leaked: %q", fmtStr, out)
		}
	}
	// A Call containing the secret must not leak it through formatting either.
	call := Call{Secret: secret}
	if out := fmt.Sprintf("%+v", call); strings.Contains(out, "super-secret-value") {
		t.Fatalf("Call %+v leaked secret", out)
	}
}

func TestCredentialSecret_CopyAndClear(t *testing.T) {
	t.Parallel()
	// Mutations to the caller's slice after construction must not affect the
	// secret held inside CredentialSecret.
	raw := []byte("original")
	secret := NewCredentialSecret(raw)
	raw[0] = 'X'
	var seen string
	if err := secret.Use(func(b []byte) error { seen = string(b); return nil }); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if seen != "original" {
		t.Fatalf("Use saw %q, want original (caller mutation must not propagate)", seen)
	}
	// Use must reject a nil callback.
	if err := secret.Use(nil); err == nil {
		t.Fatal("Use(nil) must error")
	}
}

func TestCredentialSecret_NilReceiverSafe(t *testing.T) {
	t.Parallel()
	var zero CredentialSecret
	// A zero-value secret formats to a redacted marker and does not panic.
	if got := fmt.Sprintf("%v", zero); !strings.Contains(got, "REDACTED") {
		t.Fatalf("zero secret String = %q", got)
	}
	// Use on a zero secret sees an empty value (cleared copy), not a panic.
	if err := zero.Use(func(b []byte) error {
		if len(b) != 0 {
			return fmt.Errorf("expected empty, got %d bytes", len(b))
		}
		return nil
	}); err != nil {
		t.Fatalf("zero Use: %v", err)
	}
}

func TestNewClassifiedError_Categories(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind error
		want error
	}{
		{ErrTimeout, ErrTimeout},
		{context.DeadlineExceeded, ErrTimeout},
		{ErrProtocol, ErrProtocol},
		{ErrTransport, ErrTransport},
		{ErrUnauthorized, ErrUnauthorized},
		{ErrForbidden, ErrForbidden},
		{ErrNotFound, ErrNotFound},
		{ErrRateLimited, ErrRateLimited},
		{ErrUnavailable, ErrUnavailable},
		// A wrapped sentinel is reduced to its canonical category.
		{fmt.Errorf("ctx: %w", ErrRateLimited), ErrRateLimited},
		// Unknown kinds collapse to ErrUpstream, never retained.
		{errors.New("arbitrary provider boom"), ErrUpstream},
		{nil, ErrUpstream},
	}
	for _, c := range cases {
		ce := NewClassifiedError(c.kind, 0, "", "", "")
		if !errors.Is(ce, c.want) {
			t.Fatalf("kind=%v: got=%v, want %v", c.kind, ce, c.want)
		}
	}
	deadline := NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
	if !errors.Is(deadline, ErrTimeout) || !errors.Is(deadline, context.DeadlineExceeded) {
		t.Fatalf("deadline classification must match both timeout and deadline: %v", deadline)
	}
}

func TestNewClassifiedError_NilReceiver(t *testing.T) {
	t.Parallel()
	var ce *ClassifiedError
	if got := ce.Error(); got != ErrUpstream.Error() {
		t.Fatalf("nil Error() = %q", got)
	}
	if ce.Status() != 0 || ce.RequestID() != "" || ce.Code() != "" || ce.Type() != "" {
		t.Fatalf("nil receiver getters must be zero")
	}
	if r := ce.ToUpstreamResponse(); r != (adapter.UpstreamResponse{}) {
		t.Fatalf("nil ToUpstreamResponse = %#v, want zero", r)
	}
}

func TestClassifiedError_RequestIDValidation(t *testing.T) {
	t.Parallel()
	// Accepted: any printable rune (ASCII punctuation + printable Unicode),
	// explicitly including '.', ':', '/'. The old [A-Za-z0-9_-] allowlist is not
	// imposed.
	allowed := []string{
		"req_123",
		"req.abc:123/xyz",
		"a/b:c.d-e_f",
		strings.Repeat("a", 128),
		"/req/abc:",
	}
	for _, id := range allowed {
		ce := NewClassifiedError(ErrUpstream, 0, id, "", "")
		if got := ce.RequestID(); got != id {
			t.Fatalf("RequestID %q: got %q, want retained", id, got)
		}
	}
	// Rejected: empty, control characters, invalid UTF-8, over length bound.
	rejected := []string{
		"",                               // empty
		"req\t123",                       // control char (tab)
		"req\n123",                       // control char (LF)
		"req\r123",                       // control char (CR)
		"req\x00",                        // NUL control
		"req\x7f",                        // DEL control
		"req\x85",                        // C1 control (NEL, U+0085)
		"req\x9f",                        // C1 control (U+009F)
		strings.Repeat("a", 129),         // over length bound
		string([]byte{0xff, 0xfe, 0xfd}), // invalid UTF-8
	}
	for _, id := range rejected {
		ce := NewClassifiedError(ErrUpstream, 0, id, "", "")
		if got := ce.RequestID(); got != "" {
			t.Fatalf("RequestID %q: got %q, want empty (rejected)", id, got)
		}
	}
	// Accepted printable ASCII punctuation and printable Unicode: the request-ID
	// policy accepts all printable runes, not the old [A-Za-z0-9_-] allowlist.
	accepted := []string{
		"café",           // non-ASCII letter
		"with;semicolon", // ';' printable punctuation
		"query?x=1",      // '?' printable punctuation
		"a=b/c:d.e",      // mixed printable punctuation
		"bad<>code",      // '<' '>' printable punctuation (was rejected by old allowlist)
		"req 123",        // interior space is printable, not a control char
		"中文-请求.123",      // printable Unicode + punctuation
		"req@host",       // '@' printable punctuation
	}
	for _, id := range accepted {
		ce := NewClassifiedError(ErrUpstream, 0, id, "", "")
		if got := ce.RequestID(); got != id {
			t.Fatalf("RequestID %q: got %q, want retained (accepted)", id, got)
		}
	}
}

func TestClassifiedError_CodeTypeSanitized(t *testing.T) {
	t.Parallel()
	// Code/Type use the stricter [A-Za-z0-9_-] set: '.', ':', '/' are rejected.
	ce := NewClassifiedError(ErrUpstream, 500, "", "rate_limit_exceeded", "rate_limit_error")
	if ce.Code() != "rate_limit_exceeded" {
		t.Fatalf("Code = %q", ce.Code())
	}
	if ce.Type() != "rate_limit_error" {
		t.Fatalf("Type = %q", ce.Type())
	}
	bad := NewClassifiedError(ErrUpstream, 500, "", "bad<>code!@#", "type.with.dots")
	if bad.Code() != "" {
		t.Fatalf("Code = %q, want empty (sanitized)", bad.Code())
	}
	if bad.Type() != "" {
		t.Fatalf("Type = %q, want empty (sanitized)", bad.Type())
	}
}

func TestClassifiedError_NoRemoteMessageLeak(t *testing.T) {
	t.Parallel()
	// Error() is the fixed category only: it must never carry the upstream
	// message, code, type, request ID, or status.
	ce := NewClassifiedError(ErrRateLimited, 429, "req.abc:123", "rate_limit_exceeded", "rate_limit_error")
	for _, leak := range []string{"req.abc:123", "rate_limit_exceeded", "rate_limit_error", "429"} {
		if strings.Contains(ce.Error(), leak) {
			t.Fatalf("Error() leaked %q: %q", leak, ce.Error())
		}
	}
	// %+v on the classified error must not surface remote content either.
	if out := fmt.Sprintf("%+v", ce); strings.Contains(out, "rate_limit_exceeded") || strings.Contains(out, "req.abc:123") {
		t.Fatalf("classified error %+v leaked remote content", out)
	}
}

func TestClassifiedError_ToUpstreamResponse_UsesSafeFailureKinds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind error
		code string
		typ  string
	}{
		{ErrTimeout, "timeout", "timeout"},
		{ErrTransport, "transport", "transport"},
		{ErrProtocol, "protocol", "protocol"},
	} {
		r := NewClassifiedError(tc.kind, 0, "remote-request", "remote_code", "remote_type").ToUpstreamResponse()
		if r.ErrorCode != tc.code || r.ErrorType != tc.typ || r.Message != "" {
			t.Fatalf("kind %v response = %#v", tc.kind, r)
		}
	}
}

func TestClassifiedError_ToUpstreamResponse_EmptyMessage(t *testing.T) {
	t.Parallel()
	ce := NewClassifiedError(ErrUnauthorized, 401, "req.id", "auth_error", "auth_failed")
	r := ce.ToUpstreamResponse()
	if r.HTTPStatus != 401 {
		t.Fatalf("HTTPStatus = %d, want 401", r.HTTPStatus)
	}
	if r.ErrorCode != "auth_error" {
		t.Fatalf("ErrorCode = %q", r.ErrorCode)
	}
	if r.ErrorType != "auth_failed" {
		t.Fatalf("ErrorType = %q", r.ErrorType)
	}
	// Message must always be empty: the upstream message is never retained.
	if r.Message != "" {
		t.Fatalf("Message = %q, want empty", r.Message)
	}
	// FinishReason/StreamEventType are not relevant for a classified error.
	if r.FinishReason != "" || r.StreamEventType != "" {
		t.Fatalf("ToUpstreamResponse set unexpected fields: %#v", r)
	}
}

func TestClient_InterfaceOnlyComplete(t *testing.T) {
	t.Parallel()
	// A type with only Complete satisfies Client; the port exposes no Stream.
	var _ Client = onlyCompleteClient{}
	// Confirm the Client interface type declares exactly one method, Complete.
	clientType := reflect.TypeOf((*Client)(nil)).Elem()
	if got := clientType.NumMethod(); got != 1 {
		t.Fatalf("Client interface has %d methods, want 1 (Complete only)", got)
	}
	if _, ok := clientType.MethodByName("Stream"); ok {
		t.Fatal("Client interface must not declare Stream")
	}
	if _, ok := clientType.MethodByName("Complete"); !ok {
		t.Fatal("Client interface must declare Complete")
	}
}

type onlyCompleteClient struct{}

func (onlyCompleteClient) Complete(context.Context, Call) (Completion, error) {
	return Completion{}, nil
}

// SafeRequestID is fully exercised through NewClassifiedError above; this
// direct test guards the shared chokepoint independently and covers the full
// accept/reject policy (printable ASCII punctuation + printable Unicode
// accepted; CTL/invalid UTF-8/over-length rejected).
func TestSafeRequestID_Direct(t *testing.T) {
	t.Parallel()
	// Accepted: alphanumerics, '-', '_', '.', ':', '/', and any other printable
	// ASCII punctuation or printable Unicode.
	accepted := []string{
		"a.b:c/d",
		"req_123",
		"with;semicolon",
		"query?x=1",
		"a=b",
		"café",
		"中文-req.1/2:3",
		"!@#$%^&*()",
		strings.Repeat("a", 128),
	}
	for _, in := range accepted {
		if got := SafeRequestID(in); got != in {
			t.Fatalf("SafeRequestID(%q) = %q, want retained", in, got)
		}
	}
	// Rejected: reduced to empty.
	rejected := []string{
		"",                               // empty
		"bad\x00",                        // NUL control
		"req\t123",                       // control char (tab)
		"req\n123",                       // control char (LF)
		"req\x7f",                        // DEL control
		string([]byte{0xff, 0xfe}),       // invalid UTF-8
		string([]byte{0xff, 0xfe, 0xfd}), // invalid UTF-8
		strings.Repeat("a", 129),         // over length bound
	}
	for _, in := range rejected {
		if got := SafeRequestID(in); got != "" {
			t.Fatalf("SafeRequestID(%q) = %q, want empty (rejected)", in, got)
		}
	}
	// Sanity: the invalid-UTF-8 fixtures above are indeed invalid.
	if utf8.ValidString(string([]byte{0xff, 0xfe})) {
		t.Fatal("test fixture must be invalid UTF-8")
	}
}
