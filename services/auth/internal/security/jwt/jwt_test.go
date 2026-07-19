package jwt

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// newTestKeyPair generates an Ed25519 key pair in-memory for tests. It never
// touches disk and never writes a private key to the repository.
func newTestKeyPair(t *testing.T) *KeyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 GenerateKey: %v", err)
	}
	return &KeyPair{Private: priv, Public: pub}
}

func mustIssuer(t *testing.T, kp *KeyPair) *Issuer {
	t.Helper()
	iss, err := NewIssuer(kp, "tokenmp-auth", "tokenmp-web", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return iss
}

func mustVerifier(t *testing.T, kp *KeyPair) *Verifier {
	t.Helper()
	v, err := NewVerifier(kp, "tokenmp-auth", "tokenmp-web")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestIssueAndVerify_RoundTrip(t *testing.T) {
	kp := newTestKeyPair(t)
	iss := mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	now := time.Now().UTC().Add(-1 * time.Second)
	tok, exp, err := iss.IssueAccessToken("user-1", "user", 3, now)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !exp.After(now) {
		t.Error("exp not after now")
	}
	c, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.RegisteredClaims.Subject != "user-1" {
		t.Errorf("sub=%q want user-1", c.RegisteredClaims.Subject)
	}
	if c.Role != "user" {
		t.Errorf("role=%q want user", c.Role)
	}
	if c.TokenVersion != 3 {
		t.Errorf("token_version=%d want 3", c.TokenVersion)
	}
	if c.RegisteredClaims.Issuer != "tokenmp-auth" {
		t.Errorf("iss=%q", c.RegisteredClaims.Issuer)
	}
	if len(c.RegisteredClaims.Audience) == 0 || c.RegisteredClaims.Audience[0] != "tokenmp-web" {
		t.Errorf("aud=%v", c.RegisteredClaims.Audience)
	}
	if c.RegisteredClaims.ID == "" {
		t.Error("jti is empty")
	}
	if c.RegisteredClaims.IssuedAt == nil {
		t.Error("iat is nil")
	}
	if c.RegisteredClaims.ExpiresAt == nil {
		t.Error("exp is nil")
	}
	if c.RegisteredClaims.NotBefore == nil {
		t.Error("nbf is nil")
	}
}

func TestVerify_Expired(t *testing.T) {
	kp := newTestKeyPair(t)
	iss := mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	old := time.Now().UTC().Add(-2 * time.Hour)
	tok, _, err := iss.IssueAccessToken("u", "user", 1, old)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Verify(tok); !errors.Is(err, ErrExpired) {
		t.Errorf("err=%v want ErrExpired", err)
	}
}

func TestVerify_NotYetValid(t *testing.T) {
	kp := newTestKeyPair(t)
	iss := mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	future := time.Now().UTC().Add(2 * time.Hour)
	tok, _, err := iss.IssueAccessToken("u", "user", 1, future)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := v.Verify(tok); err == nil {
		t.Error("expected error for future nbf, got nil")
	}
}

func TestVerify_WrongAudience(t *testing.T) {
	kp := newTestKeyPair(t)
	iss, err := NewIssuer(kp, "tokenmp-auth", "tokenmp-web", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(kp, "tokenmp-auth", "different-aud")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tok, _, _ := iss.IssueAccessToken("u", "user", 1, now)
	if _, err := v.Verify(tok); err == nil {
		t.Error("expected aud mismatch error, got nil")
	}
}

func TestVerify_WrongIssuer(t *testing.T) {
	kp := newTestKeyPair(t)
	iss, err := NewIssuer(kp, "other-issuer", "tokenmp-web", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v := mustVerifier(t, kp)
	now := time.Now().UTC()
	tok, _, _ := iss.IssueAccessToken("u", "user", 1, now)
	if _, err := v.Verify(tok); err == nil {
		t.Error("expected iss mismatch error, got nil")
	}
}

func TestVerify_TamperedSignature(t *testing.T) {
	kp := newTestKeyPair(t)
	iss := mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	tok, _, _ := iss.IssueAccessToken("u", "user", 1, time.Now().UTC())
	// Tamper a character near the start of the signature segment so the
	// decoded signature actually changes (the last base64 char can carry
	// ignored padding bits, so flipping it may not change the bytes).
	idx := strings.LastIndex(tok, ".")
	if idx < 0 || idx+2 >= len(tok) {
		t.Fatal("token shape unexpected")
	}
	r := tok[idx+1]
	tampered := tok[:idx+1] + string(flipByte(r)) + tok[idx+2:]
	if _, err := v.Verify(tampered); err == nil {
		t.Error("expected verification error for tampered token")
	}
}

func TestVerify_WrongKeyPair(t *testing.T) {
	kp1 := newTestKeyPair(t)
	kp2 := newTestKeyPair(t)
	iss := mustIssuer(t, kp1)
	v := mustVerifier(t, kp2)
	tok, _, _ := iss.IssueAccessToken("u", "user", 1, time.Now().UTC())
	if _, err := v.Verify(tok); err == nil {
		t.Error("expected signature mismatch error")
	}
}

func TestLoadKeyPair_MismatchedKeys(t *testing.T) {
	kp1 := newTestKeyPair(t)
	kp2 := newTestKeyPair(t)
	// Write each to a temp file with mismatched pairs.
	privPath := writePrivatePEM(t, kp1.Private)
	pubPath := writePublicPEM(t, kp2.Public)
	_, err := LoadKeyPair(privPath, pubPath)
	if err == nil {
		t.Error("expected mismatch error")
	}
}

func TestLoadKeyPair_MissingPaths(t *testing.T) {
	if _, err := LoadKeyPair("", "x"); err == nil {
		t.Error("expected ErrPrivateKeyFileRequired")
	}
	if _, err := LoadKeyPair("x", ""); err == nil {
		t.Error("expected ErrPublicKeyFileRequired")
	}
}

func TestVerify_MissingSub(t *testing.T) {
	kp := newTestKeyPair(t)
	_ = mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	// Build a token with no sub by manually constructing MapClaims.
	claims := jwtv5.MapClaims{
		"iss":           "tokenmp-auth",
		"aud":           "tokenmp-web",
		"jti":           "test-jti",
		"iat":           time.Now().UTC().Unix(),
		"nbf":           time.Now().UTC().Unix(),
		"exp":           time.Now().UTC().Add(15 * time.Minute).Unix(),
		"role":          "user",
		"token_version": 1,
	}
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(kp.Private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := v.Verify(signed); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v want ErrInvalidToken for missing sub", err)
	}
}

func TestVerify_MissingRole(t *testing.T) {
	kp := newTestKeyPair(t)
	_ = mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	claims := jwtv5.MapClaims{
		"iss":           "tokenmp-auth",
		"aud":           "tokenmp-web",
		"sub":           "user-1",
		"jti":           "test-jti",
		"iat":           time.Now().UTC().Unix(),
		"nbf":           time.Now().UTC().Unix(),
		"exp":           time.Now().UTC().Add(15 * time.Minute).Unix(),
		"token_version": 1,
	}
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(kp.Private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := v.Verify(signed); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v want ErrInvalidToken for missing role", err)
	}
}

func TestVerify_TokenVersionZero(t *testing.T) {
	kp := newTestKeyPair(t)
	_ = mustIssuer(t, kp)
	v := mustVerifier(t, kp)
	claims := jwtv5.MapClaims{
		"iss":           "tokenmp-auth",
		"aud":           "tokenmp-web",
		"sub":           "user-1",
		"jti":           "test-jti",
		"iat":           time.Now().UTC().Unix(),
		"nbf":           time.Now().UTC().Unix(),
		"exp":           time.Now().UTC().Add(15 * time.Minute).Unix(),
		"role":          "user",
		"token_version": 0,
	}
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(kp.Private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := v.Verify(signed); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v want ErrInvalidToken for token_version=0", err)
	}
}

func TestVerify_AlgConfusion(t *testing.T) {
	kp := newTestKeyPair(t)
	v := mustVerifier(t, kp)
	// Craft a token with "none" alg — must be rejected by WithValidMethods.
	// Build the header+payload manually and append an empty signature.
	header := base64urlEncode([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64urlEncode([]byte(`{"iss":"tokenmp-auth","aud":"tokenmp-web","sub":"user-1","jti":"test-jti","iat":0,"nbf":0,"exp":9999999999,"role":"user","token_version":1}`))
	noneToken := header + "." + payload + "."
	if _, err := v.Verify(noneToken); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v want ErrInvalidToken for alg=none", err)
	}
}

func TestVerify_TokenVersionFloatTruncated(t *testing.T) {
	// JSON numbers decode as float64; 1.5 in token_version must be rejected
	// because jwt/v5's json deserializer will fail to unmarshal a non-integer
	// into an int field, causing the entire token to fail verification.
	kp := newTestKeyPair(t)
	v := mustVerifier(t, kp)
	claims := jwtv5.MapClaims{
		"iss":           "tokenmp-auth",
		"aud":           "tokenmp-web",
		"sub":           "user-1",
		"jti":           "test-jti",
		"iat":           time.Now().UTC().Unix(),
		"nbf":           time.Now().UTC().Unix(),
		"exp":           time.Now().UTC().Add(15 * time.Minute).Unix(),
		"role":          "user",
		"token_version": 1.5,
	}
	tok := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := tok.SignedString(kp.Private)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// 1.5 cannot unmarshal into int → token is invalid.
	if _, err := v.Verify(signed); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("err=%v want ErrInvalidToken for non-integer token_version", err)
	}
}

func TestLoadKeyPair_ReadFail(t *testing.T) {
	if _, err := LoadKeyPair("/nonexistent/private.key", "/nonexistent/public.key"); err == nil {
		t.Error("expected read failure")
	}
}

func TestLoadKeyPair_RealRoundTrip(t *testing.T) {
	kp := newTestKeyPair(t)
	priv := writePrivatePEM(t, kp.Private)
	pub := writePublicPEM(t, kp.Public)
	loaded, err := LoadKeyPair(priv, pub)
	if err != nil {
		t.Fatalf("LoadKeyPair: %v", err)
	}
	if !bytes.Equal(loaded.Public, kp.Public) {
		t.Error("loaded public key mismatch")
	}
	iss := mustIssuer(t, loaded)
	v := mustVerifier(t, loaded)
	tok, _, _ := iss.IssueAccessToken("u", "user", 1, time.Now().UTC())
	c, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.RegisteredClaims.Subject != "u" {
		t.Errorf("sub=%q want u", c.RegisteredClaims.Subject)
	}
}

func writePrivatePEM(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8: %v", err)
	}
	return writePEM(t, "PRIVATE KEY", der)
}

func writePublicPEM(t *testing.T, key ed25519.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("MarshalPKIX: %v", err)
	}
	return writePEM(t, "PUBLIC KEY", der)
}

func writePEM(t *testing.T, typ string, der []byte) string {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
	f := t.TempDir() + "/key.pem"
	if err := writeFile(f, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	return f
}

func flipByte(b byte) byte {
	if b == 'a' {
		return 'b'
	}
	return 'a'
}

func base64urlEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
