package sdk

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter_DeltaSeconds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		val  string
		want time.Duration
	}{
		{"zero", "0", 0},
		{"small", "5", 5 * time.Second},
		{"large within cap", "120", 120 * time.Second},
		{"exact cap", "300", 300 * time.Second},
		{"over cap clamped", "600", HardMaxRetryAfter},
		{"huge clamped", "9999999999", HardMaxRetryAfter},
		{"negative clamped to zero", "-10", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("Retry-After", tc.val)
			got, ok := ParseRetryAfter(h)
			if !ok {
				t.Fatalf("ParseRetryAfter(%q) ok=false, want true", tc.val)
			}
			if got != tc.want {
				t.Fatalf("ParseRetryAfter(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	t.Parallel()
	// Future date within cap.
	within := time.Now().Add(30 * time.Second).UTC().Format(time.RFC1123)
	h := http.Header{}
	h.Set("Retry-After", within)
	got, ok := ParseRetryAfter(h)
	if !ok {
		t.Fatalf("ParseRetryAfter(http-date) ok=false, want true")
	}
	if got <= 0 || got > 30*time.Second {
		t.Fatalf("ParseRetryAfter(http-date) = %v, want ~30s (positive, <=30s)", got)
	}

	// Future date exceeding cap is clamped.
	far := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC1123)
	h = http.Header{}
	h.Set("Retry-After", far)
	got, ok = ParseRetryAfter(h)
	if !ok {
		t.Fatalf("ParseRetryAfter(far http-date) ok=false, want true")
	}
	if got != HardMaxRetryAfter {
		t.Fatalf("ParseRetryAfter(far http-date) = %v, want %v (clamped)", got, HardMaxRetryAfter)
	}

	// Past date yields false (<=0).
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC1123)
	h = http.Header{}
	h.Set("Retry-After", past)
	_, ok = ParseRetryAfter(h)
	if ok {
		t.Fatalf("ParseRetryAfter(past http-date) ok=true, want false")
	}
}

func TestParseRetryAfter_InvalidAndMissing(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",     // absent
		"abc",  // not a number or date
		"1.5",  // not an integer
		" 10 ", // whitespace
		"10x",  // trailing garbage
	}
	for _, v := range cases {
		h := http.Header{}
		if v != "" {
			h.Set("Retry-After", v)
		}
		got, ok := ParseRetryAfter(h)
		if ok {
			t.Fatalf("ParseRetryAfter(%q) ok=true, want false", v)
		}
		if got != 0 {
			t.Fatalf("ParseRetryAfter(%q) = %v, want 0", v, got)
		}
	}
}

func TestParseRetryAfter_NilHeader(t *testing.T) {
	t.Parallel()
	got, ok := ParseRetryAfter(nil)
	if ok || got != 0 {
		t.Fatalf("ParseRetryAfter(nil) = (%v,%v), want (0,false)", got, ok)
	}
}

func TestNewClassifiedErrorWithRetryAfter_ClampsAndSets(t *testing.T) {
	t.Parallel()
	// Valid within cap.
	ce := NewClassifiedErrorWithRetryAfter(ErrRateLimited, 429, "req", "code", "type", 10*time.Second, true)
	if ra, ok := ce.RetryAfter(); !ok || ra != 10*time.Second {
		t.Fatalf("RetryAfter = (%v,%v), want (10s,true)", ra, ok)
	}
	// Over cap clamped.
	ce = NewClassifiedErrorWithRetryAfter(ErrUnavailable, 503, "req", "code", "type", 2*time.Hour, true)
	if ra, ok := ce.RetryAfter(); !ok || ra != HardMaxRetryAfter {
		t.Fatalf("RetryAfter = (%v,%v), want (%v,true)", ra, ok, HardMaxRetryAfter)
	}
	// Negative clamped to zero, still hasRetryAfter.
	ce = NewClassifiedErrorWithRetryAfter(ErrRateLimited, 429, "req", "code", "type", -5*time.Second, true)
	if ra, ok := ce.RetryAfter(); !ok || ra != 0 {
		t.Fatalf("RetryAfter = (%v,%v), want (0,true)", ra, ok)
	}
	// hasRetryAfter=false yields no RetryAfter.
	ce = NewClassifiedErrorWithRetryAfter(ErrRateLimited, 429, "req", "code", "type", 10*time.Second, false)
	if _, ok := ce.RetryAfter(); ok {
		t.Fatal("RetryAfter ok=true, want false when hasRetryAfter=false")
	}
	// Sanitization of requestID/code/typ still applied.
	ce = NewClassifiedErrorWithRetryAfter(ErrRateLimited, 429, "req.abc:1", "rate_code", "rate_type", 1*time.Second, true)
	if ce.RequestID() != "req.abc:1" || ce.Code() != "rate_code" || ce.Type() != "rate_type" {
		t.Fatalf("sanitization regressed: %+v", ce)
	}
	if ce.Status() != 429 {
		t.Fatalf("Status = %d, want 429", ce.Status())
	}
	if !errors.Is(ce, ErrRateLimited) {
		t.Fatalf("kind mismatch: %v", ce)
	}
}

func TestNewClassifiedError_NoRetryAfterByDefault(t *testing.T) {
	t.Parallel()
	ce := NewClassifiedError(ErrRateLimited, 429, "req", "code", "type")
	if _, ok := ce.RetryAfter(); ok {
		t.Fatal("NewClassifiedError RetryAfter ok=true, want false (backward compatible)")
	}
	// Nil receiver is safe.
	var nilCE *ClassifiedError
	if _, ok := nilCE.RetryAfter(); ok {
		t.Fatal("nil RetryAfter ok=true, want false")
	}
}

func TestCloneClassifiedError_PreservesRetryAfter(t *testing.T) {
	t.Parallel()
	ce := NewClassifiedErrorWithRetryAfter(ErrUnavailable, 503, "req", "code", "type", 42*time.Second, true)
	clone := CloneClassifiedError(ce)
	if clone == nil {
		t.Fatal("clone is nil")
	}
	ra, ok := clone.RetryAfter()
	if !ok || ra != 42*time.Second {
		t.Fatalf("clone RetryAfter = (%v,%v), want (42s,true)", ra, ok)
	}
	// Clone of an error without RetryAfter also has none.
	plain := NewClassifiedError(ErrRateLimited, 429, "req", "code", "type")
	clone2 := CloneClassifiedError(plain)
	if _, ok := clone2.RetryAfter(); ok {
		t.Fatal("clone of plain error has RetryAfter, want none")
	}
	// Clone of nil is nil.
	if got := CloneClassifiedError(nil); got != nil {
		t.Fatalf("CloneClassifiedError(nil) = %v, want nil", got)
	}
}

func TestHardMaxRetryAfter_Value(t *testing.T) {
	t.Parallel()
	if HardMaxRetryAfter != 5*time.Minute {
		t.Fatalf("HardMaxRetryAfter = %v, want 5m", HardMaxRetryAfter)
	}
}
