package jwtverifier

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
	"github.com/tokenmp/v3/services/executor/internal/identity"
)

const (
	testIssuer   = "tokenmp-auth"
	testAudience = "tokenmp-web"
)

// generateKeyPair creates an Ed25519 key pair for testing.
func generateKeyPair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	return priv, pub
}

// writePublicKeyPEM writes the public key as PKIX PEM to a temp file and
// returns the path.
func writePublicKeyPEM(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	return writePublicKeyPEMToDir(t, pub, t.TempDir())
}

// writePublicKeyPEMToDir writes the public key as PKIX PEM to a specified
// directory and returns the path.
func writePublicKeyPEMToDir(t *testing.T, pub ed25519.PublicKey, dir string) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	block := &pem.Block{Type: "PUBLIC KEY", Bytes: der}
	path := filepath.Join(dir, "public.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o644); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	return path
}

// issueToken signs a JWT with the given private key and claims.
func issueToken(t *testing.T, priv ed25519.PrivateKey, claims *Claims) string {
	t.Helper()
	token := jwtv5.NewWithClaims(jwtv5.SigningMethodEdDSA, claims)
	signed, err := token.SignedString(priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// validClaims returns a valid Claims for the standard test issuer/audience.
func validClaims(now time.Time) *Claims {
	return &Claims{
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    testIssuer,
			Subject:   "user-123",
			Audience:  jwtv5.ClaimStrings{testAudience},
			ExpiresAt: jwtv5.NewNumericDate(now.Add(15 * time.Minute)),
			NotBefore: jwtv5.NewNumericDate(now),
			IssuedAt:  jwtv5.NewNumericDate(now),
			ID:        "jti-abc123",
		},
		Role:         "user",
		TokenVersion: 1,
	}
}

// buildVerifier creates a Verifier from the given public key PEM file.
func buildVerifier(t *testing.T, pubKeyFile string) *Verifier {
	t.Helper()
	v, err := NewVerifier(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// ─── Verifier tests ──────────────────────────────────────────────────

func TestVerifierValidRoundTrip(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	raw := issueToken(t, priv, validClaims(now))
	claims, err := v.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Errorf("Subject = %q, want %q", claims.Subject, "user-123")
	}
	if claims.Role != "user" {
		t.Errorf("Role = %q, want %q", claims.Role, "user")
	}
	if claims.TokenVersion != 1 {
		t.Errorf("TokenVersion = %d, want 1", claims.TokenVersion)
	}
	if claims.ID != "jti-abc123" {
		t.Errorf("ID = %q, want %q", claims.ID, "jti-abc123")
	}
}

func TestVerifierExpired(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.ExpiresAt = jwtv5.NewNumericDate(now.Add(-1 * time.Hour))
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("error = %v, want ErrExpired", err)
	}
}

func TestVerifierNbfFuture(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.NotBefore = jwtv5.NewNumericDate(now.Add(1 * time.Hour))
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierSignatureTampered(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	raw := issueToken(t, priv, validClaims(now))
	// Split the token and replace the entire signature with garbage.
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	tampered := parts[0] + "." + parts[1] + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	_, err := v.Verify(tampered)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierWrongPublicKey(t *testing.T) {
	t.Parallel()
	priv, _ := generateKeyPair(t)
	_, otherPub := generateKeyPair(t)
	otherPubKeyFile := writePublicKeyPEM(t, otherPub)
	v := buildVerifier(t, otherPubKeyFile)

	now := time.Now()
	raw := issueToken(t, priv, validClaims(now))
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierWrongIssuer(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.Issuer = "wrong-issuer"
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierWrongAudience(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.Audience = jwtv5.ClaimStrings{"wrong-audience"}
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierAlgNone(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	// Manually craft an "alg: none" token (unsigned).
	parts := []string{
		`{"alg":"none","typ":"JWT"}`,
		`{"sub":"user-123","iss":"` + testIssuer + `","aud":"` + testAudience + `","exp":9999999999,"nbf":1,"iat":1,"jti":"jti","role":"user","token_version":1}`,
		"",
	}
	var encoded []string
	for _, p := range parts {
		encoded = append(encoded, base64url(p))
	}
	raw := strings.Join(encoded, ".")
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
	_ = priv // suppress unused
}

func TestVerifierMissingSub(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.Subject = ""
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierMissingJTI(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.ID = ""
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierMissingRole(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.Role = ""
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierTokenVersionZero(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	claims := validClaims(now)
	claims.TokenVersion = 0
	raw := issueToken(t, priv, claims)
	_, err := v.Verify(raw)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierEmptyToken(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	_, err := v.Verify("")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
	_, err = v.Verify("   ")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifierConcurrent(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	now := time.Now()
	raw := issueToken(t, priv, validClaims(now))

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claims, err := v.Verify(raw)
			if err != nil {
				t.Errorf("Verify: %v", err)
				return
			}
			if claims.Subject != "user-123" {
				t.Errorf("Subject = %q, want %q", claims.Subject, "user-123")
			}
		}()
	}
	wg.Wait()
}

func TestVerifierFuzz(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	v := buildVerifier(t, pubKeyFile)

	// Fuzz with random strings — all must fail with ErrInvalidToken.
	for i := 0; i < 100; i++ {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		raw := fmt.Sprintf("%x", b)
		_, err := v.Verify(raw)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("fuzz input %d: error = %v, want ErrInvalidToken", i, err)
		}
	}
}

// ─── Public key file loading tests ───────────────────────────────────

func TestNewVerifierMissingFile(t *testing.T) {
	t.Parallel()
	_, err := NewVerifier(filepath.Join(t.TempDir(), "missing.pem"), testIssuer, testAudience)
	if !errors.Is(err, ErrPublicKeyReadFailed) {
		t.Fatalf("error = %v, want ErrPublicKeyReadFailed", err)
	}
}

func TestNewVerifierMalformedPEM(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(path, []byte("not a pem"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewVerifier(path, testIssuer, testAudience)
	if !errors.Is(err, ErrPublicKeyParseFailed) {
		t.Fatalf("error = %v, want ErrPublicKeyParseFailed", err)
	}
}

func TestNewVerifierWrongKeyType(t *testing.T) {
	t.Parallel()
	// Write an RSA public key instead of Ed25519.
	dir := t.TempDir()
	path := filepath.Join(dir, "rsa.pem")
	// A minimal RSA public key PEM (won't parse as Ed25519).
	rsaPEM := `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA0Z3VS5JJcds3xfn/ygWy
B8C6P5iM2j1f5QXw8L5bL5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L5L
wIDAQAB
-----END PUBLIC KEY-----`
	if err := os.WriteFile(path, []byte(rsaPEM), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewVerifier(path, testIssuer, testAudience)
	if !errors.Is(err, ErrPublicKeyParseFailed) {
		t.Fatalf("error = %v, want ErrPublicKeyParseFailed", err)
	}
}

func TestNewVerifierEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := NewVerifier("", testIssuer, testAudience)
	if !errors.Is(err, ErrPublicKeyFileRequired) {
		t.Fatalf("error = %v, want ErrPublicKeyFileRequired", err)
	}
}

func TestNewVerifierEmptyIssuer(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	_, err := NewVerifier(pubKeyFile, "", testAudience)
	if err == nil {
		t.Fatal("error = nil, want error for empty issuer")
	}
}

func TestNewVerifierEmptyAudience(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	_, err := NewVerifier(pubKeyFile, testIssuer, "")
	if err == nil {
		t.Fatal("error = nil, want error for empty audience")
	}
}

// ─── Source tests ────────────────────────────────────────────────────

func TestSourceValidToken(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	now := time.Now()
	raw := issueToken(t, priv, validClaims(now))
	id, err := src.LookupByKey(context.Background(), raw)
	if err != nil {
		t.Fatalf("LookupByKey: %v", err)
	}
	if id.Subject != "user-123" {
		t.Errorf("Subject = %q, want %q", id.Subject, "user-123")
	}
	if id.KeyID != "" {
		t.Errorf("KeyID = %q, want empty", id.KeyID)
	}
	if id.Role != identity.RoleService {
		t.Errorf("Role = %q, want %q", id.Role, identity.RoleService)
	}
	if id.Status != identity.StatusActive {
		t.Errorf("Status = %q, want %q", id.Status, identity.StatusActive)
	}
}

func TestSourceAdminRole(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	now := time.Now()
	claims := validClaims(now)
	claims.Role = "admin"
	raw := issueToken(t, priv, claims)
	id, err := src.LookupByKey(context.Background(), raw)
	if err != nil {
		t.Fatalf("LookupByKey: %v", err)
	}
	if id.Role != identity.RoleAdmin {
		t.Errorf("Role = %q, want %q", id.Role, identity.RoleAdmin)
	}
}

func TestSourceUnknownRole(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	now := time.Now()
	claims := validClaims(now)
	claims.Role = "superadmin"
	raw := issueToken(t, priv, claims)
	_, err = src.LookupByKey(context.Background(), raw)
	if !errors.Is(err, identity.ErrUnknownKey) {
		t.Fatalf("error = %v, want ErrUnknownKey", err)
	}
}

func TestSourceExpiredToken(t *testing.T) {
	t.Parallel()
	priv, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	now := time.Now()
	claims := validClaims(now)
	claims.ExpiresAt = jwtv5.NewNumericDate(now.Add(-1 * time.Hour))
	raw := issueToken(t, priv, claims)
	_, err = src.LookupByKey(context.Background(), raw)
	if !errors.Is(err, identity.ErrUnknownKey) {
		t.Fatalf("error = %v, want ErrUnknownKey", err)
	}
}

func TestSourceInvalidSignature(t *testing.T) {
	t.Parallel()
	priv, _ := generateKeyPair(t)
	_, otherPub := generateKeyPair(t)
	otherPubKeyFile := writePublicKeyPEM(t, otherPub)
	src, err := NewSource(otherPubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	now := time.Now()
	raw := issueToken(t, priv, validClaims(now))
	_, err = src.LookupByKey(context.Background(), raw)
	if !errors.Is(err, identity.ErrUnknownKey) {
		t.Fatalf("error = %v, want ErrUnknownKey", err)
	}
}

func TestSourceEmptyToken(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	_, err = src.LookupByKey(context.Background(), "")
	if !errors.Is(err, identity.ErrUnknownKey) {
		t.Fatalf("error = %v, want ErrUnknownKey", err)
	}
}

func TestSourceContextCanceled(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = src.LookupByKey(ctx, "some-token")
	if err == nil {
		t.Fatal("error = nil, want error for canceled context")
	}
}

func TestSourceNilContext(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}

	_, err = src.LookupByKey(nil, "some-token")
	if !errors.Is(err, identity.ErrUnknownKey) {
		t.Fatalf("error = %v, want ErrUnknownKey", err)
	}
}

func TestSourceStringRedacted(t *testing.T) {
	t.Parallel()
	_, pub := generateKeyPair(t)
	pubKeyFile := writePublicKeyPEM(t, pub)
	src, err := NewSource(pubKeyFile, testIssuer, testAudience)
	if err != nil {
		t.Fatalf("NewSource: %v", err)
	}
	for _, s := range []string{src.String(), src.GoString(), fmt.Sprintf("%v", src)} {
		if strings.Contains(s, testIssuer) || strings.Contains(s, testAudience) {
			t.Errorf("string representation %q leaks issuer/audience", s)
		}
		if !strings.Contains(s, "REDACTED") {
			t.Errorf("string representation %q missing REDACTED", s)
		}
	}
}

// base64url is a minimal base64url encoder without padding for the alg=none test.
func base64url(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_"
	var result []byte
	buf := make([]byte, 3)
	b := []byte(s)
	for i := 0; i < len(b); {
		remaining := len(b) - i
		n := 3
		if remaining < 3 {
			n = remaining
		}
		copy(buf[:], b[i:])
		for j := n; j < 3; j++ {
			buf[j] = 0
		}
		switch n {
		case 3:
			result = append(result, alphabet[buf[0]>>2])
			result = append(result, alphabet[(buf[0]&0x03)<<4|buf[1]>>4])
			result = append(result, alphabet[(buf[1]&0x0f)<<2|buf[2]>>6])
			result = append(result, alphabet[buf[2]&0x3f])
		case 2:
			result = append(result, alphabet[buf[0]>>2])
			result = append(result, alphabet[(buf[0]&0x03)<<4|buf[1]>>4])
		case 1:
			result = append(result, alphabet[buf[0]>>2])
		}
		i += n
	}
	return string(result)
}
