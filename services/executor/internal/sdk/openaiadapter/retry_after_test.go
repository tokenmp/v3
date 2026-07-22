package openaiadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func TestComplete_RetryAfterOnRetryableStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		kind   error
		ra     string // Retry-After header value
		wantRA time.Duration
		wantOK bool
	}{
		{"429 with RA", http.StatusTooManyRequests, sdk.ErrRateLimited, "30", 30 * time.Second, true},
		{"429 no RA", http.StatusTooManyRequests, sdk.ErrRateLimited, "", 0, false},
		{"429 invalid RA", http.StatusTooManyRequests, sdk.ErrRateLimited, "not-a-number", 0, false},
		{"503 with RA", http.StatusServiceUnavailable, sdk.ErrUnavailable, "60", 60 * time.Second, true},
		{"503 RA over cap", http.StatusServiceUnavailable, sdk.ErrUnavailable, "999", sdk.HardMaxRetryAfter, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-request-id", "req_ra")
				if tc.ra != "" {
					w.Header().Set("Retry-After", tc.ra)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":"rate","type":"rate_error"}}`))
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "key"))
			if !errors.Is(err, tc.kind) {
				t.Fatalf("error = %v, want %v", err, tc.kind)
			}
			ce, ok := err.(*sdk.ClassifiedError)
			if !ok {
				t.Fatalf("not a ClassifiedError: %T", err)
			}
			ra, hasRA := ce.RetryAfter()
			if hasRA != tc.wantOK {
				t.Fatalf("RetryAfter ok = %v, want %v", hasRA, tc.wantOK)
			}
			if hasRA && ra != tc.wantRA {
				t.Fatalf("RetryAfter = %v, want %v", ra, tc.wantRA)
			}
		})
	}
}

func TestComplete_NonRetryableStatusNoRetryAfter(t *testing.T) {
	// 401 with Retry-After header must NOT parse it.
	for _, tc := range []struct {
		name   string
		status int
		kind   error
	}{
		{"unauthorized", http.StatusUnauthorized, sdk.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, sdk.ErrForbidden},
		{"not found", http.StatusNotFound, sdk.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-request-id", "req_no")
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":"auth","type":"auth_error"}}`))
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "key"))
			if !errors.Is(err, tc.kind) {
				t.Fatalf("error = %v, want %v", err, tc.kind)
			}
			ce := err.(*sdk.ClassifiedError)
			if _, ok := ce.RetryAfter(); ok {
				t.Fatal("non-retryable status parsed Retry-After, want none")
			}
		})
	}
}

func TestComplete_ImagesRetryAfterOnRetryableStatus(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "45")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	_, err := newTestClient(t, ts, nil).Complete(context.Background(), imageCall(ts.URL, "key", `{"model":"m","prompt":"x"}`))
	if !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	ce, ok := err.(*sdk.ClassifiedError)
	if !ok {
		t.Fatalf("not ClassifiedError: %T", err)
	}
	ra, hasRA := ce.RetryAfter()
	if !hasRA || ra != 45*time.Second {
		t.Fatalf("Images RetryAfter = (%v,%v), want (45s,true)", ra, hasRA)
	}
}

func TestComplete_ResponseRetryAfterOnRetryableStatus(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()
	_, err := newTestClient(t, ts, nil).Complete(context.Background(), responseCall(ts.URL, "key", `{"model":"m","input":"x"}`))
	if !errors.Is(err, sdk.ErrRateLimited) {
		t.Fatalf("error = %v", err)
	}
	ce := err.(*sdk.ClassifiedError)
	ra, hasRA := ce.RetryAfter()
	if !hasRA || ra != 20*time.Second {
		t.Fatalf("Response RetryAfter = (%v,%v), want (20s,true)", ra, hasRA)
	}
}
