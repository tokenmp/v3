package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/services/api/internal/config"
)

// TestConfigProxy_CASAndHeaderBoundary spins up a real TLS httptest Config
// Service and verifies the Edge admin config proxy:
//   - the client If-Match reaches the Config Service verbatim (on PATCH);
//   - the X-Admin-Token is injected solely by the client from its configured
//     secret and cannot be overridden by a spoofed client X-Admin-Token;
//   - Authorization/Cookie and any other arbitrary client headers are never
//     forwarded;
//   - only the allowlisted upstream response headers (ETag, Cache-Control)
//     are surfaced to the edge client; sensitive/arbitrary upstream headers
//     (Set-Cookie, X-Internal) are dropped;
//   - the redirect-rejection and timeout policy are preserved (via WithHTTPClient).
func TestConfigProxy_CASAndHeaderBoundary(t *testing.T) {
	var (
		mu         sync.Mutex
		gotIfMatch string
		gotToken   string
		gotAuth    bool
		gotCookie  bool
		gotXCustom bool
	)

	cfgSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotIfMatch = r.Header.Get("If-Match")
		gotToken = r.Header.Get("X-Admin-Token")
		gotAuth = r.Header.Get("Authorization") != ""
		gotCookie = r.Header.Get("Cookie") != ""
		gotXCustom = r.Header.Get("X-Custom-Client") != ""
		mu.Unlock()

		// Config Service responds with the allowlisted headers AND sensitive
		// headers that must NOT leak back to the edge client.
		w.Header().Set("ETag", "4")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Set-Cookie", "leak=1; HttpOnly")
		w.Header().Set("X-Internal", "config-secret")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":1,"version":4,"updated":true}`)
	}))
	defer cfgSrv.Close()

	// Edge client: configured shared secret only. WithHTTPClient wires the TLS
	// trust store of the test server while re-applying redirect rejection +
	// timeout.
	client := config.NewClient(cfgSrv.URL, "edge-admin-token",
		config.WithHTTPClient(cfgSrv.Client()))
	h := NewConfigHandlers(client, true)

	r := chi.NewRouter()
	h.Routes(r)

	// Inbound edge request carries If-Match plus spoofed sensitive headers.
	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/admin/config/drafts/1", strings.NewReader(`{"y":2}`))
	req.Header.Set("If-Match", "3")
	req.Header.Set("Authorization", "Bearer client-jwt")
	req.Header.Set("Cookie", "session=abc")
	req.Header.Set("X-Admin-Token", "evil-client-token")
	req.Header.Set("X-Custom-Client", "anything")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// If-Match reached the Config Service verbatim.
	if gotIfMatch != "3" {
		t.Fatalf("Config If-Match = %q, want 3", gotIfMatch)
	}
	// X-Admin-Token is the configured secret, not the spoofed client value.
	if gotToken != "edge-admin-token" {
		t.Fatalf("Config X-Admin-Token = %q, want edge-admin-token (client-injected, not overridable)", gotToken)
	}
	// Authorization/Cookie/arbitrary client headers never forwarded.
	if gotAuth {
		t.Fatal("client Authorization header leaked to Config Service")
	}
	if gotCookie {
		t.Fatal("client Cookie header leaked to Config Service")
	}
	if gotXCustom {
		t.Fatal("arbitrary client X-Custom-Client header leaked to Config Service")
	}

	// Only ETag + Cache-Control surfaced to the edge client.
	if rec.Header().Get("ETag") != "4" {
		t.Fatalf("ETag not surfaced: %v", rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control not surfaced: %v", rec.Header())
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("upstream Set-Cookie leaked to edge client: %v", rec.Header())
	}
	if rec.Header().Get("X-Internal") != "" {
		t.Fatalf("upstream X-Internal leaked to edge client: %v", rec.Header())
	}
	// Content-Type is set by the handler, not forwarded from upstream.
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (handler-set)", ct)
	}
	// Body passed through.
	if !strings.Contains(rec.Body.String(), `"version":4`) {
		t.Fatalf("response body not passed through: %s", rec.Body.String())
	}
}

// TestConfigProxy_NoIfMatchStillWorks verifies a request without If-Match
// reaches the Config Service with no If-Match header (Config falls back to
// the current version) and that nothing else is injected.
func TestConfigProxy_NoIfMatchStillWorks(t *testing.T) {
	var gotIfMatch, gotToken string
	cfgSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfMatch = r.Header.Get("If-Match")
		gotToken = r.Header.Get("X-Admin-Token")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer cfgSrv.Close()

	client := config.NewClient(cfgSrv.URL, "edge-admin-token",
		config.WithHTTPClient(cfgSrv.Client()))
	h := NewConfigHandlers(client, true)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/config/revisions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotIfMatch != "" {
		t.Fatalf("Config If-Match = %q, want empty", gotIfMatch)
	}
	if gotToken != "edge-admin-token" {
		t.Fatalf("Config X-Admin-Token = %q, want edge-admin-token", gotToken)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control not surfaced: %v", rec.Header())
	}
}

// TestConfigProxy_412PreconditionSurfaced verifies a 412 from the Config
// Service (version mismatch) is surfaced to the edge client with its body and
// no-store directive, not masked as a 502.
func TestConfigProxy_412PreconditionSurfaced(t *testing.T) {
	cfgSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `{"error":{"code":"version_mismatch","message":"revision version changed"}}`)
	}))
	defer cfgSrv.Close()

	client := config.NewClient(cfgSrv.URL, "edge-admin-token",
		config.WithHTTPClient(cfgSrv.Client()))
	h := NewConfigHandlers(client, true)
	r := chi.NewRouter()
	h.Routes(r)

	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/admin/config/drafts/1", strings.NewReader(`{"y":2}`))
	req.Header.Set("If-Match", "999")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control not surfaced on 412: %v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), "version_mismatch") {
		t.Fatalf("412 body not surfaced: %s", rec.Body.String())
	}
}
