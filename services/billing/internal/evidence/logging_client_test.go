package evidence_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/billing/internal/evidence"
)

// TestNilLookup_ReportsUnavailable: an unconfigured lookup never releases.
func TestNilLookup_ReportsUnavailable(t *testing.T) {
	nl := evidence.NilLookup{}
	_, err := nl.TerminalUsage(context.Background(), "r")
	if !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// TestLoggingClient_EmptyURLReturnsNilLookup: empty baseURL degrades to
// NilLookup (keep pending) rather than blind-releasing.
func TestLoggingClient_EmptyURLReturnsNilLookup(t *testing.T) {
	c := evidence.NewClient("", "", 5*time.Second)
	if _, ok := c.(evidence.NilLookup); !ok {
		t.Fatalf("empty baseURL must return NilLookup, got %T", c)
	}
}

// realLoggingHandler builds an httptest handler that mirrors the real Logging
// Service GET /v1/logs/{request_id}: it writes via httpresp.OK (canonical
// {code,data,message} envelope) for a known final/known requestID, returns
// httpresp.Error (envelope + 404) for the not-found id, and otherwise writes a
// non-final usage_status. This is the exact envelope shape the client must
// unwrap in production — no hand-rolled fixtures that could drift.
func realLoggingHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/logs/final-known":
			// Real envelope: data.log with usage_status=final + total_tokens.
			httpresp.OK(w, map[string]any{
				"log": map[string]any{
					"request_id":    "final-known",
					"final_status":  "success",
					"usage_status":  "final",
					"total_tokens":  42,
					"input_tokens":  10,
					"output_tokens": 32,
				},
				"attempts": []any{},
				"events":   []any{},
			})
		case "/v1/logs/final-zero-tokens":
			// final with zero tokens — known terminal, must settle as 0 (never
			// guess a positive count). This guards the zero-token final语义.
			httpresp.OK(w, map[string]any{
				"log": map[string]any{
					"request_id":    "final-zero-tokens",
					"final_status":  "success",
					"usage_status":  "final",
					"total_tokens":  0,
					"input_tokens":  0,
					"output_tokens": 0,
				},
			})
		case "/v1/logs/final-fallback":
			// final with total_tokens=0 but input+output present → fallback sum.
			httpresp.OK(w, map[string]any{
				"log": map[string]any{
					"request_id":    "final-fallback",
					"usage_status":  "final",
					"total_tokens":  0,
					"input_tokens":  10,
					"output_tokens": 20,
				},
			})
		case "/v1/logs/not-final":
			// Row exists but usage_status=pending → retriable ErrNotTerminal.
			httpresp.OK(w, map[string]any{
				"log": map[string]any{
					"request_id":   "not-final",
					"usage_status": "pending",
				},
			})
		case "/v1/logs/missing":
			// No row → real Logging 404 envelope.
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
		case "/v1/logs/malformed":
			// 2xx but body is not a valid envelope → ErrUnavailable (never guess).
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"log":{"request_id":"broken"`) // truncated
		case "/v1/logs/5xx":
			// Logging internal error → ErrUnavailable.
			httpresp.Error(w, httpresp.CodeInternalError, "query failed")
		default:
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
		}
	})
	return mux
}

func TestLoggingClient_KnownFinal(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	ev, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "final-known")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ev.Known || ev.Tokens != 42 {
		t.Fatalf("evidence = %+v, want Known+Tokens=42", ev)
	}
}

func TestLoggingClient_KnownInputOutputFallback(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	ev, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "final-fallback")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ev.Known || ev.Tokens != 30 {
		t.Fatalf("evidence = %+v, want Known+Tokens=30 (input+output fallback)", ev)
	}
}

// TestLoggingClient_ZeroTokenFinalSemantics: a final log with zero tokens is
// known terminal and must report Known=true with Tokens=0 — the reconciler
// settles the actual (zero) count rather than guessing or treating it as
// non-terminal.
func TestLoggingClient_ZeroTokenFinalSemantics(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	ev, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "final-zero-tokens")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !ev.Known {
		t.Fatalf("final with zero tokens must be Known, got %+v", ev)
	}
	if ev.Tokens != 0 {
		t.Fatalf("Tokens = %d, want 0 (never guess a positive count)", ev.Tokens)
	}
}

func TestLoggingClient_NotTerminal(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "not-final")
	if !errors.Is(err, evidence.ErrNotTerminal) {
		t.Fatalf("err = %v, want ErrNotTerminal", err)
	}
}

func TestLoggingClient_NotFound(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "missing")
	if !errors.Is(err, evidence.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestLoggingClient_MalformedEnvelope: a 2xx body that is not a valid
// envelope must not be guessed from — it maps to ErrUnavailable (keep pending).
func TestLoggingClient_MalformedEnvelope(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "malformed")
	if !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable for malformed envelope", err)
	}
}

func TestLoggingClient_UnavailableOn5xx(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "5xx")
	if !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestLoggingClient_NoRedirect(t *testing.T) {
	target := httptest.NewServer(realLoggingHandler(t))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/logs/final-known", http.StatusFound)
	}))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "final-known")
	if !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatalf("redirect must be refused: err = %v", err)
	}
}

func TestLoggingClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 50*time.Millisecond)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "rq7")
	if !errors.Is(err, evidence.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable on timeout", err)
	}
}

func TestLoggingClient_ServiceTokenSent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		httpresp.OK(w, map[string]any{
			"log": map[string]any{"usage_status": "final", "total_tokens": 1},
		})
	}))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "sekret", 5*time.Second)
	if _, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "rq8"); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != "Bearer sekret" {
		t.Fatalf("Authorization = %q, want Bearer sekret", got)
	}
}

// TestLoggingClient_NoURLInError: every error is a bare sentinel — URL/host/
// path/request id must never reach logs through Error()/Unwrap().
func TestLoggingClient_NoURLInError(t *testing.T) {
	srv := httptest.NewServer(realLoggingHandler(t))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "5xx")
	msg := err.Error()
	for _, frag := range []string{"127.0.0.1", "localhost", "/v1/logs", "5xx"} {
		if contains(msg, frag) {
			t.Fatalf("error %q leaks fragment %q", msg, frag)
		}
	}
}

// TestLoggingClient_NoTokenInError: the service token must never appear in an
// error message.
func TestLoggingClient_NoTokenInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := evidence.NewClient(srv.URL, "super-secret-token", 5*time.Second)
	_, err := c.(evidence.Lookup).TerminalUsage(context.Background(), "rq-leak")
	if contains(err.Error(), "super-secret-token") {
		t.Fatalf("error leaks service token: %q", err.Error())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
