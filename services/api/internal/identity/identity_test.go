package identity

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

func TestNoopVerifierWhenNoKeyFile(t *testing.T) {
	v, err := NewVerifier("", "iss", "aud", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	claims, err := v.Verify(context.Background(), "some-token")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "some-token" {
		t.Errorf("Subject = %q", claims.Subject)
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
