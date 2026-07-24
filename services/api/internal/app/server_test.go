package app_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tokenmp/v3/services/api/internal/app"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/proxy"
	"github.com/tokenmp/v3/services/api/internal/quota"
)

func genEdgeKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func writeEdgePubPEM(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := t.TempDir() + "/pub.pem"
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func makeEdgeJWT(t *testing.T, priv ed25519.PrivateKey, sub string) string {
	t.Helper()
	claims := &jwt.RegisteredClaims{
		Subject:   sub,
		Issuer:    "tokenmp-auth",
		Audience:  jwt.ClaimStrings{"tokenmp-web"},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// TestEdgeFullFlow_AuthQuotaProxyFinalize verifies the complete request
// flow: client JWT → identity middleware → quota reserve → proxy forward →
// quota finalize.
func TestEdgeFullFlow_AuthQuotaProxyFinalize(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Fake executor backend.
	var execAuth, execPath string
	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		execAuth = r.Header.Get("Authorization")
		execPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[]}`))
	}))
	defer execBackend.Close()

	prx, err := proxy.New(execBackend.URL, "edge-svc-token", nil)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	// Fake billing backend.
	reserveHits := 0
	finalizeHits := 0
	releaseHits := 0
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			reserveHits++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"reservation_id":"rsv_1","status":"reserved"}`))
		case "/v1/billing/quota/finalize":
			finalizeHits++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"finalized"}`))
		case "/v1/billing/quota/release":
			releaseHits++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"released"}`))
		}
	}))
	defer billBackend.Close()

	deps := app.Deps{
		Verifier: verifier,
		Proxy:    prx,
		Quota:    quota.NewManager(billBackend.URL),
	}
	srv := app.NewServer(deps, 10*time.Second, 60*time.Second)
	front := httptest.NewServer(srv.Handler)
	defer front.Close()

	tok := makeEdgeJWT(t, priv, "user-123")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if execAuth != "Bearer edge-svc-token" {
		t.Errorf("executor auth = %q, want 'Bearer edge-svc-token'", execAuth)
	}
	if execPath != "/v1/chat/completions" {
		t.Errorf("executor path = %q", execPath)
	}
	if reserveHits != 1 {
		t.Errorf("reserve hits = %d, want 1", reserveHits)
	}
	if finalizeHits != 1 {
		t.Errorf("finalize hits = %d, want 1", finalizeHits)
	}
	if releaseHits != 0 {
		t.Errorf("release hits = %d, want 0 (success path)", releaseHits)
	}
}

// TestEdgeAuthRejectsMissingToken verifies unauthenticated requests get 401.
func TestEdgeAuthRejectsMissingToken(t *testing.T) {
	pub, _ := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, _ := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)

	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("executor should not be called for unauthenticated request")
	}))
	defer execBackend.Close()

	prx, _ := proxy.New(execBackend.URL, "tok", nil)
	deps := app.Deps{Verifier: verifier, Proxy: prx, Quota: quota.NewManager("")}
	srv := app.NewServer(deps, 10*time.Second, 60*time.Second)
	front := httptest.NewServer(srv.Handler)
	defer front.Close()

	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestEdgeQuotaReleaseOnUpstreamError verifies release is called when the
// executor returns an error status.
func TestEdgeQuotaReleaseOnUpstreamError(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, _ := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)

	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer execBackend.Close()
	prx, _ := proxy.New(execBackend.URL, "tok", nil)

	releaseHits := 0
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/quota/release":
			releaseHits++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"released"}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"reservation_id":"rsv_1","status":"reserved"}`))
		}
	}))
	defer billBackend.Close()

	deps := app.Deps{Verifier: verifier, Proxy: prx, Quota: quota.NewManager(billBackend.URL)}
	srv := app.NewServer(deps, 10*time.Second, 60*time.Second)
	front := httptest.NewServer(srv.Handler)
	defer front.Close()

	tok := makeEdgeJWT(t, priv, "u")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if releaseHits != 1 {
		t.Errorf("release hits = %d, want 1", releaseHits)
	}
}

// TestEdgeHealthzAnonymous verifies healthz is accessible without auth.
func TestEdgeHealthzAnonymous(t *testing.T) {
	pub, _ := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, _ := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer execBackend.Close()
	prx, _ := proxy.New(execBackend.URL, "tok", nil)
	deps := app.Deps{Verifier: verifier, Proxy: prx, Quota: quota.NewManager("")}
	srv := app.NewServer(deps, 10*time.Second, 60*time.Second)
	front := httptest.NewServer(srv.Handler)
	defer front.Close()

	resp, err := http.Get(front.URL + "/healthz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestEdgeQuotaUnavailableReturns503 verifies that when billing is
// unreachable, the edge returns 503 instead of forwarding.
func TestEdgeQuotaUnavailableReturns503(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, _ := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("executor should not be called when quota reserve fails")
	}))
	defer execBackend.Close()
	prx, _ := proxy.New(execBackend.URL, "tok", nil)

	// Unreachable billing.
	mgr := quota.NewManager("http://127.0.0.1:1")
	deps := app.Deps{Verifier: verifier, Proxy: prx, Quota: mgr}
	srv := app.NewServer(deps, 10*time.Second, 60*time.Second)
	front := httptest.NewServer(srv.Handler)
	defer front.Close()

	tok := makeEdgeJWT(t, priv, "u")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// _ keeps context import for future test extensions.
var _ = context.Background
