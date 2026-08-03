package app_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	var reserveHits, finalizeHits, releaseHits atomic.Int32
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			reserveHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"reservation_id":"rsv_1","status":"reserved"}`))
		case "/v1/billing/quota/finalize":
			finalizeHits.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"finalized"}`))
		case "/v1/billing/quota/release":
			releaseHits.Add(1)
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
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// The finalize/release call happens after the response is sent, so we
	// wait briefly for the billing backend to receive it.
	waitForCondition(t, func() bool { return finalizeHits.Load() > 0 || releaseHits.Load() > 0 }, 2*time.Second)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if execAuth != "Bearer edge-svc-token" {
		t.Errorf("executor auth = %q, want 'Bearer edge-svc-token'", execAuth)
	}
	if execPath != "/v1/chat/completions" {
		t.Errorf("executor path = %q", execPath)
	}
	if reserveHits.Load() != 1 {
		t.Errorf("reserve hits = %d, want 1", reserveHits.Load())
	}
	if finalizeHits.Load() != 1 {
		t.Errorf("finalize hits = %d, want 1", finalizeHits.Load())
	}
	if releaseHits.Load() != 0 {
		t.Errorf("release hits = %d, want 0 (success path)", releaseHits.Load())
	}
}

func TestEdgeLogsClientCancelledTerminal(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer execBackend.Close()
	prx, err := proxy.New(execBackend.URL, "edge-token", nil)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	type ingestWire struct {
		Log struct {
			FinalStatus string `json:"final_status"`
			HTTPStatus  int    `json:"http_status"`
			ErrorType   string `json:"error_type"`
		} `json:"log"`
		Events []struct {
			Stage   string `json:"stage"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"events"`
	}
	ingested := make(chan ingestWire, 4)
	logBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body ingestWire
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode ingest: %v", err)
			}
			ingested <- body
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer logBackend.Close()

	deps := app.Deps{Verifier: verifier, Proxy: prx, Quota: quota.NewManager(""), Logging: logging.NewClient(logBackend.URL)}
	front := httptest.NewServer(app.NewServer(deps, 10*time.Second, 60*time.Second).Handler)
	defer front.Close()

	tok := makeEdgeJWT(t, priv, "cancel-test")
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	done := make(chan struct{})
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("client request did not return after cancellation")
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-ingested:
			if got.Log.FinalStatus != "client_cancelled" {
				continue
			}
			if got.Log.HTTPStatus != 499 || got.Log.ErrorType != "client_cancelled" {
				t.Fatalf("cancel log = %+v", got.Log)
			}
			if len(got.Events) != 1 || got.Events[0].Stage != "terminal" || got.Events[0].Message != "client cancelled" {
				t.Fatalf("cancel events = %+v", got.Events)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for client_cancelled ingest")
		}
	}
}

func TestEdgeUsesPreferredTokenBillingForQuota(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[]}`))
	}))
	defer execBackend.Close()
	prx, err := proxy.New(execBackend.URL, "edge-svc-token", nil)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	var reserveBody map[string]any
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/quota/reserve":
			_ = json.NewDecoder(r.Body).Decode(&reserveBody)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"reservation_id":"rsv_1","status":"reserved"}`))
		case "/v1/billing/quota/finalize", "/v1/billing/quota/release":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer billBackend.Close()

	st := settings.NewStore()
	pref := "token"
	st.Snapshot("user-token", &pref, nil, nil)
	deps := app.Deps{Verifier: verifier, Proxy: prx, Quota: quota.NewManager(billBackend.URL), Settings: st}
	srv := app.NewServer(deps, 10*time.Second, 60*time.Second)
	front := httptest.NewServer(srv.Handler)
	defer front.Close()

	body := `{"model":"gpt-test","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+makeEdgeJWT(t, priv, "user-token"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if reserveBody["billing_plan"] != "token" || reserveBody["reserved_requests"] != float64(0) || reserveBody["reserved_tokens"] != float64(0) {
		t.Fatalf("reserveBody=%v", reserveBody)
	}
}

func TestEdgeDoesNotLogOrReserveModelsCatalog(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	var execHits atomic.Int32
	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		execHits.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"object":"list"}`))
	}))
	defer execBackend.Close()
	prx, err := proxy.New(execBackend.URL, "edge-token", nil)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	logBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("logging should not be called for /v1/models")
	}))
	defer logBackend.Close()
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("billing should not be called for /v1/models")
	}))
	defer billBackend.Close()

	deps := app.Deps{
		Verifier: verifier,
		Proxy:    prx,
		Quota:    quota.NewManager(billBackend.URL),
		Logging:  logging.NewClient(logBackend.URL),
	}
	front := httptest.NewServer(app.NewServer(deps, 10*time.Second, 60*time.Second).Handler)
	defer front.Close()

	tok := makeEdgeJWT(t, priv, "models-test")
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if execHits.Load() != 1 {
		t.Fatalf("exec hits = %d", execHits.Load())
	}
}

func TestEdgeIngestsBoundedUserAgentOnReceipt(t *testing.T) {
	pub, priv := genEdgeKeyPair(t)
	keyFile := writeEdgePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	execBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer execBackend.Close()
	prx, err := proxy.New(execBackend.URL, "edge-token", nil)
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	type ingestWire struct {
		Log struct {
			UserAgent string `json:"user_agent"`
		} `json:"log"`
	}
	ingested := make(chan ingestWire, 1)
	logBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body ingestWire
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode ingest: %v", err)
			}
			ingested <- body
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer logBackend.Close()

	deps := app.Deps{
		Verifier: verifier,
		Proxy:    prx,
		Quota:    quota.NewManager(""),
		Logging:  logging.NewClient(logBackend.URL),
	}
	front := httptest.NewServer(app.NewServer(deps, 10*time.Second, 60*time.Second).Handler)
	defer front.Close()

	tok := makeEdgeJWT(t, priv, "user-agent-test")
	req, _ := http.NewRequest(http.MethodPost, front.URL+"/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "TokenMP-Test/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case got := <-ingested:
		if got.Log.UserAgent != "TokenMP-Test/1.0" {
			t.Fatalf("user_agent = %q", got.Log.UserAgent)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for logging ingest")
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

	var releaseHits atomic.Int32
	billBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/quota/release":
			releaseHits.Add(1)
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
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	waitForCondition(t, func() bool { return releaseHits.Load() > 0 }, 2*time.Second)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	if releaseHits.Load() != 1 {
		t.Errorf("release hits = %d, want 1", releaseHits.Load())
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

// waitForCondition polls cond every 5ms until it returns true or timeout
// elapses, at which point the test continues (the assertion will fail if the
// expected condition was not met).
func waitForCondition(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
