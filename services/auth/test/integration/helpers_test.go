//go:build integration

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// ed25519GenerateKey generates an Ed25519 key pair in-memory for the
// integration test process. No key is read from disk and no private key is
// ever written to the repository. The CI needs neither openssl nor a
// pre-provisioned key file.
func ed25519GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// mustBcryptHash generates a legacy bcrypt hash ($2a) for seeding legacy rows
// directly via SQL.
func mustBcryptHash(t *testing.T, pw string) string {
	t.Helper()
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(b)
}
