// Package apikey generates and hashes opaque Auth API keys.
//
// An API key is "tmp_" followed by 32 crypto/rand bytes encoded as base64url
// without padding. PostgreSQL stores only SHA-256 of the complete key string;
// the complete key is returned to its caller only at creation time.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// PrefixMarker distinguishes Auth API keys from refresh tokens and other
// opaque credentials.
const PrefixMarker = "tmp_"

// TokenLength is the number of random bytes encoded after PrefixMarker.
const TokenLength = 32

const (
	prefixLength = 12
	suffixLength = 4
)

var (
	// ErrGenerate indicates crypto/rand failed to provide API-key entropy.
	ErrGenerate = errors.New("apikey: failed to generate key entropy")

	// ErrMalformedKey indicates a supplied API key is not a valid tmp_ key with
	// a base64url payload of exactly TokenLength bytes.
	ErrMalformedKey = errors.New("apikey: malformed key")
)

// Generate creates a new API key and its SHA-256 hash. The full key must be
// returned only once to the caller and must never be persisted or logged.
func Generate() (fullKey string, hash []byte, err error) {
	raw := make([]byte, TokenLength)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, ErrGenerate
	}
	fullKey = PrefixMarker + base64.RawURLEncoding.EncodeToString(raw)
	return fullKey, hashFullKey(fullKey), nil
}

// Hash validates a complete API key and returns SHA-256 of its full string.
// Invalid input is rejected before any repository lookup.
func Hash(fullKey string) ([]byte, error) {
	if !strings.HasPrefix(fullKey, PrefixMarker) {
		return nil, ErrMalformedKey
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(fullKey, PrefixMarker))
	if err != nil || len(raw) != TokenLength {
		return nil, ErrMalformedKey
	}
	return hashFullKey(fullKey), nil
}

// Prefix returns the first 12 characters for display. It never validates or
// transforms the input because display helpers must not introduce an error path.
func Prefix(fullKey string) string {
	if len(fullKey) <= prefixLength {
		return fullKey
	}
	return fullKey[:prefixLength]
}

// Suffix returns the final four characters for display.
func Suffix(fullKey string) string {
	if len(fullKey) <= suffixLength {
		return fullKey
	}
	return fullKey[len(fullKey)-suffixLength:]
}

func hashFullKey(fullKey string) []byte {
	hash := sha256.Sum256([]byte(fullKey))
	return hash[:]
}
