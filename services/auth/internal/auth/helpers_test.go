package auth

import (
	"strings"

	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/bcrypt"
)

// bcryptHashFor generates a legacy bcrypt $2a hash for service-level tests.
func bcryptHashFor(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ed25519GenerateKey mirrors jwt's test helper so the fake issuer can mint a
// key pair in-memory without touching disk.
func ed25519GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// silence unused import in some build configurations.
var _ = strings.TrimSpace
