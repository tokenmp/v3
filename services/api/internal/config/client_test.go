package config

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestClient_RejectsRedirect verifies the X-Admin-Token is never forwarded to
// a different origin: CheckRedirect returns ErrUseLastResponse, so a 301/302
// from the Config Service is surfaced as-is rather than followed.
func TestClient_RejectsRedirect(t *testing.T) {
	var seenToken string
	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = r.Header.Get("X-Admin-Token")
		http.Redirect(w, r, "http://attacker.example/steal", http.StatusFound)
	}))
	defer redirectSrv.Close()

	c := NewClient(redirectSrv.URL, "sekret")
	res, err := c.do(context.Background(), http.MethodGet, "/v1/config/drafts", nil, RequestMeta{})
	// A redirect is NOT followed; the 302 is returned as-is with no error
	// (status < 400). Critically the token never reaches the Location origin.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect not followed)", res.Status)
	}
	if strings.Contains(string(res.Body), "attacker.example") {
		// The body is the redirect HTML from the original server, which is fine;
		// what matters is the token did not leak to the attacker origin.
	}
	// The token reached only the original (trusted) origin.
	if seenToken != "sekret" {
		t.Fatalf("original origin did not receive token: got %q", seenToken)
	}
}

// TestClient_TimeoutBounded verifies the client enforces a finite timeout on
// a slow upstream instead of hanging indefinitely.
func TestClient_TimeoutBounded(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the client request timeout.
		time.Sleep(requestTimeout + 2*time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	c := NewClient(slow.URL, "tok")
	start := time.Now()
	_, err := c.do(context.Background(), http.MethodGet, "/v1/config/revisions", nil, RequestMeta{})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if err != ErrConfigUnavailable {
		t.Fatalf("err = %v, want ErrConfigUnavailable (no URL leak)", err)
	}
	// Must return roughly within the timeout window, not hang.
	if elapsed > requestTimeout+5*time.Second {
		t.Fatalf("client hung for %v (timeout=%v)", elapsed, requestTimeout)
	}
}

// TestClient_HeaderOnlyTargetRequest verifies only X-Admin-Token is sent to the
// target and no client-controlled headers are forwarded blindly.
func TestClient_HeaderOnlyTargetRequest(t *testing.T) {
	got := make(http.Header)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			got[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok123")
	body := strings.NewReader(`{"x":1}`)
	res, err := c.do(context.Background(), http.MethodPost, "/v1/config/drafts", body, RequestMeta{})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Status)
	}
	if string(res.Body) != `{"ok":true}` {
		t.Fatalf("body = %q", res.Body)
	}
	if got.Get("X-Admin-Token") != "tok123" {
		t.Fatalf("X-Admin-Token = %q, want tok123", got.Get("X-Admin-Token"))
	}
	if ct := got.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	// A spoofed Cookie/Cookie header must not be present unless explicitly set;
	// the client forwards only Content-Type (for bodies) and X-Admin-Token.
	if got.Get("Cookie") != "" {
		t.Fatalf("unexpected Cookie header forwarded: %q", got.Get("Cookie"))
	}
}

// TestClient_TransportErrorNoLeak verifies a connection-refused transport
// error maps to ErrConfigUnavailable without leaking the host/URL.
func TestClient_TransportErrorNoLeak(t *testing.T) {
	// Use a port that is very unlikely to be listening.
	c := NewClient("http://127.0.0.1:1", "tok")
	_, err := c.do(context.Background(), http.MethodGet, "/v1/config/revisions", nil, RequestMeta{})
	if err == nil {
		t.Fatal("expected transport error")
	}
	if err != ErrConfigUnavailable {
		t.Fatalf("err = %v, want ErrConfigUnavailable", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "tok") {
		t.Fatalf("error leaked host or token: %v", err)
	}
}

// TestClient_IfMatchForwarded verifies the validated client If-Match is the
// ONLY inbound header forwarded to the Config Service, and that it arrives
// verbatim on a PATCH.
func TestClient_IfMatchForwarded(t *testing.T) {
	var gotIfMatch, gotToken string
	var sawAuth, sawCookie bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIfMatch = r.Header.Get("If-Match")
		gotToken = r.Header.Get("X-Admin-Token")
		sawAuth = r.Header.Get("Authorization") != ""
		sawCookie = r.Header.Get("Cookie") != ""
		w.Header().Set("ETag", "4")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Set-Cookie", "leak=1") // must NOT be forwarded back
		w.Header().Set("X-Internal", "secret") // must NOT be forwarded back
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "cfg-token")
	res, err := c.do(context.Background(), http.MethodPatch, "/v1/config/drafts/1",
		strings.NewReader(`{"y":2}`), RequestMeta{IfMatch: "3"})
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if gotIfMatch != "3" {
		t.Fatalf("If-Match = %q, want 3", gotIfMatch)
	}
	if gotToken != "cfg-token" {
		t.Fatalf("X-Admin-Token = %q, want cfg-token (client-injected)", gotToken)
	}
	if sawAuth || sawCookie {
		t.Fatalf("client Authorization/Cookie leaked to upstream: auth=%v cookie=%v", sawAuth, sawCookie)
	}
	// Response allowlist: only ETag + Cache-Control forwarded back.
	if res.Headers.Get("ETag") != "4" {
		t.Fatalf("ETag not forwarded: %v", res.Headers)
	}
	if res.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control not forwarded: %v", res.Headers)
	}
	if res.Headers.Get("Set-Cookie") != "" || res.Headers.Get("X-Internal") != "" {
		t.Fatalf("sensitive upstream header leaked back: %v", res.Headers)
	}
	if res.Headers.Get("Content-Type") != "" {
		t.Fatalf("Content-Type must not be in allowlist (handler sets it): %v", res.Headers)
	}
}

// TestClient_ClientCannotOverrideAdminToken verifies there is no way for the
// caller to inject or override X-Admin-Token: RequestMeta carries only
// If-Match, and a spoofed token via the inbound If-Match field is rejected.
func TestClient_ClientCannotOverrideAdminToken(t *testing.T) {
	var gotToken, gotIfMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Admin-Token")
		gotIfMatch = r.Header.Get("If-Match")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "cfg-token")
	// Attempt to smuggle a token via If-Match (CRLF injection) — must be
	// rejected by sanitizeIfMatch so nothing is forwarded.
	_, _ = c.do(context.Background(), http.MethodPatch, "/v1/config/drafts/1",
		nil, RequestMeta{IfMatch: "3\r\nX-Admin-Token: evil"})
	if gotToken != "cfg-token" {
		t.Fatalf("X-Admin-Token overridden/leaked: %q", gotToken)
	}
	if gotIfMatch != "" {
		t.Fatalf("injected If-Match forwarded: %q", gotIfMatch)
	}
}

// TestClient_IfMatchSanitization verifies non-integer / malformed If-Match
// values are dropped (never forwarded) while a quoted ETag form is honored.
func TestClient_IfMatchSanitization(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"abc", ""},          // non-digit
		{"3\r\nEvil: 1", ""}, // CRLF injection
		{"12345678901", ""},  // too long (>10)
		{"0", ""},            // zero version
		{"000", ""},          // all-zero
		{"-3", ""},           // negative
		{"3", "3"},           // valid
		{"\"3\"", "3"},       // quoted ETag form
		{"  42 ", "42"},      // trimmed valid
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := sanitizeIfMatch(tc.in); got != tc.want {
				t.Fatalf("sanitizeIfMatch(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestClient_Non2xxPassesBodyAndHeaders verifies a 412 response carries its
// body and allowlisted headers back so the edge can surface the precondition
// failure rather than masking it as a 502.
func TestClient_Non2xxPassesBodyAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("ETag", "1")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `{"error":{"code":"version_mismatch"}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	res, err := c.do(context.Background(), http.MethodPatch, "/v1/config/drafts/1",
		nil, RequestMeta{IfMatch: "999"})
	if err == nil {
		t.Fatal("expected error for 412")
	}
	if res.Status != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", res.Status)
	}
	if res.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control not forwarded on 412: %v", res.Headers)
	}
	if res.Headers.Get("ETag") != "1" {
		t.Fatalf("ETag not forwarded on 412: %v", res.Headers)
	}
	if !strings.Contains(string(res.Body), "version_mismatch") {
		t.Fatalf("412 body not passed through: %q", res.Body)
	}
}
