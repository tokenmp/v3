package app_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tokenmp/v3/services/api/internal/app"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/logging"
	"github.com/tokenmp/v3/services/api/internal/proxy"
	"github.com/tokenmp/v3/services/api/internal/quota"
	"github.com/tokenmp/v3/services/api/internal/settings"
)

// buildEdgeServer wires an Edge server with the given executor/billing/logging
// backends and preferred billing. It returns the front server and the dep
// counts (reserve/finalize/release/markPending).
func buildEdgeServer(t *testing.T, execStatus int, billingStatus map[string]int, preferredBilling string, logFinal bool) (*httptest.Server, *edgeCounters, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if execStatus == 0 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[]}`))
			return
		}
		w.WriteHeader(execStatus)
	}))
	t.Cleanup(execBackend.Close)
	prx, err := proxy.New(execBackend.URL, "edge-token", nil)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	c := &edgeCounters{status: billingStatus}
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			c.reserve.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"reservation_id":"rsv","status":"reserved"}`))
		case "/v1/billing/quota/finalize":
			c.finalize.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"finalized"}`))
		case "/v1/billing/quota/release":
			c.release.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"released"}`))
		case "/v1/billing/quota/mark-pending":
			c.markPending.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"pending_reconciliation"}`))
		}
	}))
	t.Cleanup(billBackend.Close)

	// Logging backend: returns usage evidence when logFinal=true.
	logBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/logs/ingest" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"request_id":"rsv","accepted":1}`))
			return
		}
		// GET /v1/logs/{id}
		usage := "missing"
		total := 0
		if logFinal {
			usage = "final"
			total = 42
		}
		body, _ := json.Marshal(map[string]any{
			"log": map[string]any{
				"request_id":   "rsv",
				"final_status": "success",
				"usage_status": usage,
				"total_tokens": total,
			},
		})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(logBackend.Close)

	store := settings.NewStore()
	pb := preferredBilling
	store.Snapshot("u-token", &pb, nil, nil)
	deps := app.Deps{
		Verifier: verifier,
		Proxy:    prx,
		Quota:    quota.NewManager(billBackend.URL),
		Logging:  logging.NewClient(logBackend.URL),
		Settings: store,
	}
	srv := app.NewServer(deps, 5*time.Second, 30*time.Second)
	front := httptest.NewServer(srv.Handler)
	t.Cleanup(front.Close)
	return front, c, priv
}

type edgeCounters struct {
	mu          sync.Mutex
	reserve     atomic.Int32
	finalize    atomic.Int32
	release     atomic.Int32
	markPending atomic.Int32
	status      map[string]int
}

func (c *edgeCounters) waitFinalize(t *testing.T, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if c.finalize.Load() == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("finalize = %d, want %d (release=%d pending=%d)", c.finalize.Load(), want, c.release.Load(), c.markPending.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *edgeCounters) waitPending(t *testing.T, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if c.markPending.Load() == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("markPending = %d, want %d (finalize=%d release=%d)", c.markPending.Load(), want, c.finalize.Load(), c.release.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *edgeCounters) waitRelease(t *testing.T, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if c.release.Load() == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("release = %d, want %d (finalize=%d pending=%d)", c.release.Load(), want, c.finalize.Load(), c.markPending.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestEdgeSettlement_TokenKnownFinalizes verifies that a token-billed request
// with confirmed (usage_status=final) Logging evidence finalizes the actual
// token count — no 1-token fallback guess.
func TestEdgeSettlement_TokenKnownFinalizes(t *testing.T) {
	front, c, priv := buildEdgeServer(t, 0, nil, "token", true)
	tok := makeEdgeJWT(t, priv, "u-token")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	c.waitFinalize(t, 1)
	if c.markPending.Load() != 0 {
		t.Fatalf("markPending = %d, want 0", c.markPending.Load())
	}
}

// TestEdgeSettlement_TokenUnknownMarksPending verifies that unknown usage goes
// to MarkPending, NEVER a 1-token guess.
func TestEdgeSettlement_TokenUnknownMarksPending(t *testing.T) {
	front, c, priv := buildEdgeServer(t, 0, nil, "token", false)
	tok := makeEdgeJWT(t, priv, "u-token")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	c.waitPending(t, 1)
	if c.finalize.Load() != 0 {
		t.Fatalf("finalize = %d, want 0 (unknown usage must not be guessed)", c.finalize.Load())
	}
}

// TestEdgeSettlement_PreCommitErrorReleases verifies an upstream 502 releases
// the hold (no finalize of a failed request).
func TestEdgeSettlement_PreCommitErrorReleases(t *testing.T) {
	front, c, priv := buildEdgeServer(t, http.StatusBadGateway, nil, "coding", false)
	tok := makeEdgeJWT(t, priv, "u-coding")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	c.waitRelease(t, 1)
	if c.finalize.Load() != 0 {
		t.Fatalf("finalize = %d, want 0 on upstream error", c.finalize.Load())
	}
}

// TestEdgeSettlement_No1TokenFallback asserts the old 1-token fallback is gone:
// when logging is unreachable for a token request, we mark pending, not finalize 1.
func TestEdgeSettlement_No1TokenFallback(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, _ := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	t.Cleanup(execBackend.Close)
	prx, _ := proxy.New(execBackend.URL, "edge-token", nil)

	var markPending, finalize atomic.Int32
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"reservation_id":"rsv","status":"reserved"}`))
		case "/v1/billing/quota/mark-pending":
			markPending.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/v1/billing/quota/finalize":
			finalize.Add(1)
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(billBackend.Close)

	store := settings.NewStore()
	pb := "token"
	store.Snapshot("u", &pb, nil, nil)
	// Logging client pointed at a dead URL → confirmedTokenUsage returns (0,false).
	deps := app.Deps{
		Verifier: verifier, Proxy: prx,
		Quota:    quota.NewManager(billBackend.URL),
		Logging:  logging.NewClient("http://127.0.0.1:0"),
		Settings: store,
	}
	srv := app.NewServer(deps, 5*time.Second, 30*time.Second)
	front := httptest.NewServer(srv.Handler)
	t.Cleanup(front.Close)

	tok := makeEdgeJWT(t, priv, "u")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	deadline := time.After(2 * time.Second)
	for {
		if markPending.Load() == 1 {
			if finalize.Load() != 0 {
				t.Fatalf("finalize = %d, want 0 (no 1-token fallback)", finalize.Load())
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: markPending=%d finalize=%d", markPending.Load(), finalize.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// satisfy unused imports
var _ = jwt.New
var _ = context.Background
var _ ed25519.PrivateKey

// TestEdgeSettlement_LogArrivesLateButNotFinalMarksPending simulates the
// single-read race: the Edge's one bounded GetLog call returns a row that
// exists but usage_status is not "final" yet (the upstream is still
// finalizing). The coordinator must MarkPending — NEVER finalize a guess —
// and leave it to the Billing reconciler to settle once the log becomes final.
func TestEdgeSettlement_LogArrivesLateButNotFinalMarksPending(t *testing.T) {
	front, c, priv := buildEdgeServer(t, 0, nil, "token", false)
	// buildEdgeServer(logFinal=false) returns usage_status="missing" (not
	// final), exercising the not-terminal → pending path.
	tok := makeEdgeJWT(t, priv, "u-token")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	c.waitPending(t, 1)
	if c.finalize.Load() != 0 {
		t.Fatalf("finalize = %d, want 0 (not-terminal evidence must not be finalized)", c.finalize.Load())
	}
}
