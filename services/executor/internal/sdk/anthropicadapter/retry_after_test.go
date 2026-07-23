package anthropicadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func TestComplete_RetryAfterOnRetryableStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		kind   error
		ra     string
		wantRA time.Duration
		wantOK bool
	}{
		{"429 with RA", http.StatusTooManyRequests, sdk.ErrRateLimited, "30", 30 * time.Second, true},
		{"429 no RA", http.StatusTooManyRequests, sdk.ErrRateLimited, "", 0, false},
		{"429 invalid RA", http.StatusTooManyRequests, sdk.ErrRateLimited, "garbage", 0, false},
		{"529 overloaded with RA", 529, sdk.ErrUnavailable, "20", 20 * time.Second, true},
		{"503 with RA", http.StatusServiceUnavailable, sdk.ErrUnavailable, "60", 60 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("request-id", "req_ra")
				if tc.ra != "" {
					w.Header().Set("Retry-After", tc.ra)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error"}}`))
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
				w.Header().Set("request-id", "req_no")
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"auth_error"}}`))
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
