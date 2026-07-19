// Package refresh generates and hashes opaque refresh tokens.
//
// A refresh token is 32 bytes of crypto/rand encoded as base64url (no padding).
// The DB stores only the SHA-256 hash of the token as BYTEA; the raw token is
// never persisted. On login a new token is minted and its hash stored; on
// refresh the presented token is hashed and looked up against the stored hash.
// Reuse detection compares the hash of a presented-but-revoked token against
// the unique index and revokes the entire token family.
package refresh

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// TokenLength is the raw byte length of a refresh token before base64url
// encoding (32 bytes = 256 bits of entropy).
const TokenLength = 32

// ErrGenerate indicates token generation failed (extremely unlikely from
// crypto/rand).
var ErrGenerate = errors.New("refresh: failed to generate token entropy")

// ErrMalformedToken indicates the presented token is not valid base64url or
// does not decode to TokenLength bytes. Callers must return a unified
// "invalid token" error without performing a DB lookup.
var ErrMalformedToken = errors.New("refresh: malformed token")

// Generate returns a new refresh token: TokenLength random bytes encoded as
// base64url (no padding) plus the raw bytes. The raw bytes are returned so the
// service can compute Hash() for storage without re-decoding.
func Generate() (token string, raw []byte, err error) {
	b := make([]byte, TokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", nil, ErrGenerate
	}
	return base64.RawURLEncoding.EncodeToString(b), b, nil
}

// Hash returns the SHA-256 hash of a refresh token. Both the raw bytes and the
// base64url string hash to the same value because decoding is deterministic.
// The DB stores Hash(raw) as BYTEA.
func Hash(raw []byte) []byte {
	h := sha256.Sum256(raw)
	return h[:]
}

// HashToken hashes a base64url-encoded refresh token string by first decoding
// it. If the input is not valid base64url or does not decode to exactly
// TokenLength bytes, an error is returned — the caller must treat this as an
// invalid token and return a unified error without performing a DB lookup.
// This prevents malformed/empty tokens from producing a hash that could
// accidentally match a real row or cause a meaningless query.
func HashToken(token string) ([]byte, error) {
	if token == "" {
		return nil, ErrMalformedToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, ErrMalformedToken
	}
	if len(raw) != TokenLength {
		return nil, ErrMalformedToken
	}
	return Hash(raw), nil
}
