package jwtverifier_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/tokenmp/v3/services/notice/internal/jwtverifier"
)

func writePubKeyRemoved() {}

func signToken(t *testing.T, priv ed25519.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNew_MissingFile(t *testing.T) {
	_, err := jwtverifier.New("", "iss", "aud")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
	_, err = jwtverifier.New("/nonexistent/pub.pem", "iss", "aud")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestVerify_OK(t *testing.T) {
	// We need the private key to sign; generate a pair and write only the pub.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubFile := filepath.Join(t.TempDir(), "pub.pem")
	_ = os.WriteFile(pubFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)

	v, err := jwtverifier.New(pubFile, "tokenmp-auth", "tokenmp-web")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tok := signToken(t, priv, jwt.MapClaims{
		"iss": "tokenmp-auth", "aud": "tokenmp-web",
		"sub": "user-123", "role": "user",
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(15 * time.Minute).Unix(),
	})
	sub, err := v.Verify(tok)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if sub.UserID != "user-123" || sub.Role != "user" {
		t.Errorf("subject = %+v", sub)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubFile := filepath.Join(t.TempDir(), "pub.pem")
	_ = os.WriteFile(pubFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)
	v, _ := jwtverifier.New(pubFile, "tokenmp-auth", "tokenmp-web")

	// signed by a different key
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().UTC()
	tok := signToken(t, otherPriv, jwt.MapClaims{
		"iss": "tokenmp-auth", "aud": "tokenmp-web", "sub": "x",
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(15 * time.Minute).Unix(),
	})
	_, err := v.Verify(tok)
	if err != jwtverifier.ErrInvalidToken {
		t.Fatalf("got %v, want ErrInvalidToken", err)
	}
}

func TestVerify_Expired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubFile := filepath.Join(t.TempDir(), "pub.pem")
	_ = os.WriteFile(pubFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)
	v, _ := jwtverifier.New(pubFile, "tokenmp-auth", "tokenmp-web")

	now := time.Now().UTC()
	tok := signToken(t, priv, jwt.MapClaims{
		"iss": "tokenmp-auth", "aud": "tokenmp-web", "sub": "x",
		"iat": now.Add(-2 * time.Hour).Unix(),
		"exp": now.Add(-1 * time.Hour).Unix(),
	})
	_, err := v.Verify(tok)
	if err != jwtverifier.ErrInvalidToken {
		t.Fatalf("got %v, want ErrInvalidToken", err)
	}
}

func TestVerify_WrongIssuer(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubFile := filepath.Join(t.TempDir(), "pub.pem")
	_ = os.WriteFile(pubFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)
	v, _ := jwtverifier.New(pubFile, "tokenmp-auth", "tokenmp-web")

	now := time.Now().UTC()
	tok := signToken(t, priv, jwt.MapClaims{
		"iss": "evil", "aud": "tokenmp-web", "sub": "x",
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(15 * time.Minute).Unix(),
	})
	_, err := v.Verify(tok)
	if err != jwtverifier.ErrInvalidToken {
		t.Fatalf("got %v, want ErrInvalidToken", err)
	}
}

func TestVerify_Empty(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pubFile := filepath.Join(t.TempDir(), "pub.pem")
	_ = os.WriteFile(pubFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600)
	v, _ := jwtverifier.New(pubFile, "tokenmp-auth", "tokenmp-web")
	_, err := v.Verify("")
	if err != jwtverifier.ErrInvalidToken {
		t.Fatalf("got %v", err)
	}
	_, err = v.Verify("Bearer ")
	if err != jwtverifier.ErrInvalidToken {
		t.Fatalf("got %v", err)
	}
}
