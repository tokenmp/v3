// Package apikey generates and hashes opaque Auth API keys.
//
// An API key is "sk-" followed by 43 base62 characters (0-9A-Za-z) encoding
// 32 crypto/rand bytes. PostgreSQL stores only SHA-256 of the complete key
// string; the complete key is returned to its caller only at creation time.
//
// Legacy TokenMP prod keys also used the "sk-" prefix but with a different
// payload encoding (base64url, containing - and _), and were hashed as
// SHA-256(API_KEY_PEPPER + rawKey). HashCandidates therefore accepts both
// shapes by returning unpeppered and peppered hash candidates for any valid
// sk- key.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"unicode/utf8"
)

// PrefixMarker distinguishes Auth API keys from refresh tokens and other
// opaque credentials.
const PrefixMarker = "sk-"

// TokenLength is the number of random bytes of entropy encoded after
// PrefixMarker.
const TokenLength = 32

// EncodedLength is the number of base62 characters produced from TokenLength
// bytes: ceil(256 / log2(62)) = 43.
const EncodedLength = 43

const (
	prefixLength = 12
	suffixLength = 4
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var (
	// ErrGenerate indicates crypto/rand failed to provide API-key entropy.
	ErrGenerate = errors.New("apikey: failed to generate key entropy")

	// ErrMalformedKey indicates a supplied API key is not a valid sk- key.
	ErrMalformedKey = errors.New("apikey: malformed key")
)

// Generate creates a new API key and its SHA-256 hash. The full key must be
// returned only once to the caller and must never be persisted or logged.
func Generate() (fullKey string, hash []byte, err error) {
	payload, err := randomBase62(EncodedLength)
	if err != nil {
		return "", nil, err
	}
	fullKey = PrefixMarker + payload
	return fullKey, hashFullKey(fullKey), nil
}

// Hash validates a V3 API key (sk- + 43 base62 chars) and returns SHA-256 of
// its full string. It does not accept legacy prod keys; use HashCandidates
// for verify-key. Invalid input is rejected before any lookup.
func Hash(fullKey string) ([]byte, error) {
	if err := validateGeneratedKey(fullKey); err != nil {
		return nil, err
	}
	return hashFullKey(fullKey), nil
}

// HashCandidates returns all safe hash candidates for a supplied API key.
//
// V3 keys use sk- + base62 payload and are stored as SHA-256(raw key). Legacy
// TokenMP prod keys also used sk-* strings but were stored as
// SHA-256(API_KEY_PEPPER + raw key). Since both share the sk- prefix, any valid
// sk- key returns both unpeppered and (when pepper is configured) peppered
// candidates, and the repository resolves the first match.
// The legacy pepper is read from AUTH_LEGACY_API_KEY_PEPPER, with API_KEY_PEPPER
// as a compatibility fallback for ops migrations.
func HashCandidates(fullKey string) ([][]byte, error) {
	if !validSKKey(fullKey) {
		return nil, ErrMalformedKey
	}
	candidates := [][]byte{hashFullKey(fullKey)}
	if pepper := legacyPepper(); pepper != "" {
		candidates = append(candidates, hashPepperedKey(pepper, fullKey))
	}
	return candidates, nil
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

// validateGeneratedKey validates the strict V3 format: sk- + exactly
// EncodedLength base62 characters.
func validateGeneratedKey(fullKey string) error {
	if !strings.HasPrefix(fullKey, PrefixMarker) {
		return ErrMalformedKey
	}
	payload := fullKey[len(PrefixMarker):]
	if len(payload) != EncodedLength {
		return ErrMalformedKey
	}
	for i := 0; i < len(payload); i++ {
		if !isBase62(payload[i]) {
			return ErrMalformedKey
		}
	}
	return nil
}

// validSKKey accepts any non-empty printable ASCII sk-* string. It is used by
// HashCandidates to admit both strict V3 keys and legacy prod keys of varying
// length, while rejecting non-sk- tokens (e.g. refresh tokens, JWTs).
func validSKKey(fullKey string) bool {
	if !strings.HasPrefix(fullKey, PrefixMarker) || len(fullKey) <= len(PrefixMarker) || len(fullKey) > 512 || !utf8.ValidString(fullKey) {
		return false
	}
	for _, r := range fullKey {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// randomBase62 returns n base62 characters using rejection sampling to avoid
// modulo bias: a byte >= 248 (256 - 256%62) is discarded.
func randomBase62(n int) (string, error) {
	const maxByte = byte(256 - 256%len(alphabet)) // 248
	buf := make([]byte, 0, n)
	randBuf := make([]byte, n+8)
	for len(buf) < n {
		if _, err := rand.Read(randBuf); err != nil {
			return "", ErrGenerate
		}
		for _, b := range randBuf {
			if b >= maxByte {
				continue
			}
			buf = append(buf, alphabet[int(b)%len(alphabet)])
			if len(buf) == n {
				break
			}
		}
	}
	return string(buf), nil
}

func isBase62(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func legacyPepper() string {
	if v := os.Getenv("AUTH_LEGACY_API_KEY_PEPPER"); v != "" {
		return v
	}
	return os.Getenv("API_KEY_PEPPER")
}

func hashFullKey(fullKey string) []byte {
	hash := sha256.Sum256([]byte(fullKey))
	return hash[:]
}

func hashPepperedKey(pepper, fullKey string) []byte {
	hash := sha256.Sum256([]byte(pepper + fullKey))
	return hash[:]
}
