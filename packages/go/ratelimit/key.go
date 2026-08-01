package ratelimit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
)

// KeyPrefix is the versioned namespace prefix for all rate-limit keys. The
// version segment lets the key format change without colliding with old keys.
const KeyPrefix = "rl:v1:"

// minSecretLen is the minimum acceptable HMAC secret length. Shorter secrets
// are rejected at construction time so a weak or accidentally-empty secret can
// never silently weaken the key derivation.
const minSecretLen = 32

// ErrInvalidSecret is returned when the HMAC secret is empty or too short.
// It never echoes the secret value.
var ErrInvalidSecret = errors.New("ratelimit: hmac secret must be at least 32 bytes")

// KeyDeriver derives opaque, versioned Redis bucket keys from arbitrary
// dimension strings (client IP, normalized email, subject, opaque token) using
// HMAC-SHA256. Raw dimensions are never placed in the key; only the hex
// digest is. Dimensions are joined with a NUL byte separator so that
// "a","b" and "a\x00b" cannot collide.
type KeyDeriver struct {
	secret []byte
}

// NewKeyDeriver builds a KeyDeriver. secret must be at least 32 bytes and is
// copied so the caller may zero its own copy. An empty or short secret is a
// hard error — callers must fail fast at startup.
func NewKeyDeriver(secret []byte) (*KeyDeriver, error) {
	if len(secret) < minSecretLen {
		return nil, ErrInvalidSecret
	}
	cp := make([]byte, len(secret))
	copy(cp, secret)
	return &KeyDeriver{secret: cp}, nil
}

// Derive returns the opaque bucket key for the given scope and dimensions:
//
//	rl:v1:<hex(hmac_sha256(secret, len-prefix(scope, dims...)))>
//
// The scope namespaces independent limit policies (e.g. "auth.login.ip",
// "edge.v1.subject"). The scope and each dimension are length-prefixed
// (uint32 big-endian) before being fed to the MAC so that no two distinct
// (scope, dims) tuples can produce the same byte stream — e.g. ("a","b")
// and ("a\x00b",) cannot collide. No raw dimension value is ever placed in
// the key.
func (d *KeyDeriver) Derive(scope string, dims ...string) string {
	mac := hmac.New(sha256.New, d.secret)
	writeLP := func(s string) {
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(s)))
		mac.Write(lb[:])
		mac.Write([]byte(s))
	}
	writeLP(scope)
	for _, dim := range dims {
		writeLP(dim)
	}
	return KeyPrefix + hex.EncodeToString(mac.Sum(nil))
}
