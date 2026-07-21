// Package requestid defines the reservation identifier grammar and sources
// shared by the non-stream facade and the execution Runner.
//
// A reservation identifier is service-generated, never accepted from a client
// request, and carries no identity or routing meaning. Its grammar is:
//
//	res_ + <suffix>
//
// where <suffix> is 16-128 URL-safe unreserved characters (A-Z a-z 0-9 - _),
// matching the base64 RawURLEncoding alphabet. The default Random source draws
// 16 cryptographic random bytes and encodes them RawURLEncoding, yielding a
// 22-character unguessable suffix.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"strings"
)

const (
	// ReservationPrefix marks an identifier as service-generated and never
	// accepted from a client request.
	ReservationPrefix = "res_"

	minSuffixLen = 16
	maxSuffixLen = 128

	// RandomBytes is the number of cryptographic random bytes the default
	// Random source draws. 16 bytes (128 bits) encoded RawURLEncoding produce a
	// 22-character suffix, comfortably within the 16-128 grammar range.
	RandomBytes = 16
)

// Source supplies a per-request, unguessable reservation identifier. A nil or
// typed-nil source is treated as absent by callers, which fall back to the
// package default Random source so request handling is never blocked by a
// misconfigured injection.
type Source interface {
	ReservationID(context.Context) string
}

// SourceFunc adapts a function to Source. It is the test injection point for a
// deterministic, error, or short-read source.
type SourceFunc func(context.Context) string

// ReservationID implements Source.
func (f SourceFunc) ReservationID(ctx context.Context) string { return f(ctx) }

// Random is a Source that draws RandomBytes from Reader and encodes them
// RawURLEncoding with the res_ prefix. A nil Reader uses crypto/rand so the
// zero value is the cryptographic default. A read failure (including a
// short read) returns an empty string so the caller fails closed rather than
// reserving under a predictable identifier.
type Random struct {
	// Reader is the entropy source. nil means crypto/rand.Reader.
	Reader io.Reader
}

// ReservationID returns a fresh reservation identifier, or an empty string only
// if the entropy source is unavailable or returns a short read.
func (r Random) ReservationID(_ context.Context) string {
	reader := r.Reader
	if reader == nil {
		reader = rand.Reader
	}
	var buffer [RandomBytes]byte
	// io.ReadFull rejects a short read with io.ErrUnexpectedEOF; a deterministic
	// test reader that returns fewer bytes therefore yields an empty identifier
	// (fail-closed) rather than a truncated, partially-predictable one.
	if _, err := io.ReadFull(reader, buffer[:]); err != nil {
		return ""
	}
	return ReservationPrefix + base64.RawURLEncoding.EncodeToString(buffer[:])
}

// Default is the cryptographic Random source used when a caller has no Source
// injected.
var Default Source = Random{}

// ValidReservationID reports whether id matches the reservation identifier
// grammar: the res_ prefix followed by 16-128 URL-safe unreserved characters.
// It performs no allocation and accepts no client-supplied identifier shape
// outside this grammar.
func ValidReservationID(id string) bool {
	if !strings.HasPrefix(id, ReservationPrefix) {
		return false
	}
	suffix := id[len(ReservationPrefix):]
	if len(suffix) < minSuffixLen || len(suffix) > maxSuffixLen {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		if !isURLSafe(c) {
			return false
		}
	}
	return true
}

// isURLSafe reports whether c is an unreserved URL character per RFC 3986,
// which is exactly the base64 RawURLEncoding alphabet.
func isURLSafe(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_'
}
