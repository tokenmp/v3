package executorv1api

import (
	"context"
	"strings"
	"testing"
)

// nilPointerRequestIDSource is a RequestIDSource whose method receiver is a
// pointer. A typed-nil instance of it must be treated as absent so its method
// is never dispatched.
type nilPointerRequestIDSource struct{}

func (*nilPointerRequestIDSource) RequestID(context.Context) string {
	panic("typed-nil request id source must never be invoked")
}

func TestIsNilRequestIDSource(t *testing.T) {
	t.Parallel()
	real := RequestIDSourceFunc(func(context.Context) string { return "real" })
	cases := []struct {
		name   string
		source RequestIDSource
		want   bool
	}{
		{"untyped nil", nil, true},
		{"typed nil pointer", (*nilPointerRequestIDSource)(nil), true},
		{"typed nil func", RequestIDSourceFunc(nil), true},
		{"real func", real, false},
		{"real value", defaultRequestIDSourceInstance, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNilRequestIDSource(tc.source); got != tc.want {
				t.Fatalf("isNilRequestIDSource(%T) = %v, want %v", tc.source, got, tc.want)
			}
		})
	}
}

func TestRequestIDTypedNilSourceFallsBackSafely(t *testing.T) {
	t.Parallel()
	if !isNilRequestIDSource((*nilPointerRequestIDSource)(nil)) {
		t.Fatal("typed-nil source must be detected as nil")
	}
	// A typed-nil source configured via Options must fall back to the package
	// default rather than panicking when requestID dispatches it.
	a := NewNonStream(Options{Executor: nil, RequestIDs: (*nilPointerRequestIDSource)(nil)})
	got := a.requestID(context.Background())
	if !strings.HasPrefix(got, requestIDPrefix) {
		t.Fatalf("typed-nil fallback request ID = %q, want %s prefix", got, requestIDPrefix)
	}
	if got == "" {
		t.Fatal("fallback request ID must be non-empty")
	}
}

func TestRequestIDRealSourceIsUsed(t *testing.T) {
	t.Parallel()
	a := NewNonStream(Options{Executor: nil, RequestIDs: RequestIDSourceFunc(func(context.Context) string { return "trusted.req/real-1" })})
	if got := a.requestID(context.Background()); got != "trusted.req/real-1" {
		t.Fatalf("request ID = %q, want trusted.req/real-1", got)
	}
}

func TestRequestIDUntypedNilSourceFallsBack(t *testing.T) {
	t.Parallel()
	a := NewNonStream(Options{Executor: nil})
	got := a.requestID(context.Background())
	if !strings.HasPrefix(got, requestIDPrefix) {
		t.Fatalf("fallback request ID = %q, want %s prefix", got, requestIDPrefix)
	}
}
