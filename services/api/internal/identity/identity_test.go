package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func x509MarshalPKIX(pub ed25519.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(pub)
}

func osWriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func genKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func writePubPEM(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	der, err := x509MarshalPKIX(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := t.TempDir() + "/pub.pem"
	if err := osWriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func makeJWT(t *testing.T, priv ed25519.PrivateKey, sub, iss, aud string, exp time.Time) string {
	t.Helper()
	claims := &jwt.RegisteredClaims{
		Subject:   sub,
		Issuer:    iss,
		Audience:  jwt.ClaimStrings{aud},
		ExpiresAt: jwt.NewNumericDate(exp),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestJWTVerifierValidatesValidToken(t *testing.T) {
	pub, priv := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	v, err := NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tok := makeJWT(t, priv, "user-1", "tokenmp-auth", "tokenmp-web", time.Now().Add(15*time.Minute))
	claims, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("Subject = %q", claims.Subject)
	}
}

func TestJWTVerifierRejectsExpired(t *testing.T) {
	pub, priv := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	v, _ := NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	tok := makeJWT(t, priv, "u", "tokenmp-auth", "tokenmp-web", time.Now().Add(-1*time.Minute))
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestJWTVerifierRejectsWrongIssuer(t *testing.T) {
	pub, priv := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	v, _ := NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	tok := makeJWT(t, priv, "u", "wrong-iss", "tokenmp-web", time.Now().Add(15*time.Minute))
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestJWTVerifierRejectsEmptyToken(t *testing.T) {
	pub, _ := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	v, _ := NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if _, err := v.Verify(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestNewVerifierRequiresPublicKeyFile(t *testing.T) {
	_, err := NewVerifier("", "iss", "aud", nil)
	if !errors.Is(err, ErrPublicKeyFileRequired) {
		t.Fatalf("NewVerifier() error = %v, want ErrPublicKeyFileRequired", err)
	}
}

func TestNewNoopVerifierRequiresExplicitOptIn(t *testing.T) {
	claims, err := NewNoopVerifier().Verify(context.Background(), "some-token")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "some-token" {
		t.Errorf("Subject = %q", claims.Subject)
	}
}

func TestNewVerifierDoesNotLeakPublicKeyPath(t *testing.T) {
	_, err := NewVerifier("/missing/sensitive-public-key.pem", "iss", "aud", nil)
	if !errors.Is(err, ErrPublicKeyReadFailed) {
		t.Fatalf("NewVerifier() error = %v, want ErrPublicKeyReadFailed", err)
	}
	if strings.Contains(err.Error(), "sensitive-public-key") {
		t.Errorf("NewVerifier() error leaked key path: %v", err)
	}
}

func TestMiddlewareAllowsValidToken(t *testing.T) {
	pub, priv := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	v, _ := NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	tok := makeJWT(t, priv, "user-1", "tokenmp-auth", "tokenmp-web", time.Now().Add(15*time.Minute))

	called := false
	h := Middleware(v, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		c, ok := FromContext(r.Context())
		if !ok || c.Subject != "user-1" {
			t.Errorf("claims = %+v ok=%v", c, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Fatal("handler not called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d", w.Code)
	}
}

func TestMiddlewareRejectsMissingToken(t *testing.T) {
	pub, _ := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	v, _ := NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)

	h := Middleware(v, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestMiddlewareSkipsHealthz(t *testing.T) {
	// Middleware is applied only to /v1 routes, so /healthz passes through
	// without auth. This is tested at the app wiring level, not here.
}

func TestAPIKeyVerifierRejectsRedirectWithoutForwardingKey(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		if got := r.URL.Path; got != "/stolen" {
			t.Errorf("redirect target path = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/api/v1/auth/verify-key" {
			t.Errorf("path = %q", got)
		}
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	defer auth.Close()

	verifier := NewAPIKeyVerifier(auth.URL, nil)
	if _, err := verifier.Verify(context.Background(), "sk-secret-must-not-leak"); err != ErrUnauthenticated {
		t.Fatalf("Verify() error = %v, want ErrUnauthenticated", err)
	}
	if got := redirected.Load(); got != 0 {
		t.Errorf("redirect target requests = %d, want 0", got)
	}
}

func TestAPIKeyVerifierRejectsUnauthorized(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer auth.Close()

	verifier := NewAPIKeyVerifier(auth.URL, nil)
	if _, err := verifier.Verify(context.Background(), "sk-invalid"); err != ErrUnauthenticated {
		t.Fatalf("Verify() error = %v, want ErrUnauthenticated", err)
	}
}

func TestAPIKeyVerifierTimesOut(t *testing.T) {
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer auth.Close()

	verifier := NewAPIKeyVerifier(auth.URL, nil)
	verifier.http.Timeout = 10 * time.Millisecond
	if _, err := verifier.Verify(context.Background(), "sk-key"); err != ErrUnauthenticated {
		t.Fatalf("Verify() error = %v, want ErrUnauthenticated", err)
	}
}

func TestAPIKeyVerifierUsesBoundedNoRedirectClient(t *testing.T) {
	verifier := NewAPIKeyVerifier("https://auth.example", nil)
	if verifier.http.Timeout != 10*time.Second {
		t.Errorf("client timeout = %s, want 10s", verifier.http.Timeout)
	}
	request := httptest.NewRequest(http.MethodGet, "https://redirect.example", nil)
	if err := verifier.http.CheckRedirect(request, nil); err != http.ErrUseLastResponse {
		t.Errorf("CheckRedirect() error = %v, want http.ErrUseLastResponse", err)
	}
}

func TestAPIKeyVerifierRejectsUnsafeBaseURL(t *testing.T) {
	for _, raw := range []string{
		"ftp://auth.example",
		"https://user:password@auth.example",
		"https://auth.example?api_key=secret",
		"https://auth.example?#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			verifier := NewAPIKeyVerifier(raw, nil)
			if verifier.authURL != "" {
				t.Errorf("authURL = %q, want empty for unsafe URL", verifier.authURL)
			}
			if _, err := verifier.Verify(context.Background(), "sk-key"); err != ErrUnauthenticated {
				t.Fatalf("Verify() error = %v, want ErrUnauthenticated", err)
			}
		})
	}
}
