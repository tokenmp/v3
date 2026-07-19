// Package password implements the auth service password hashing and
// validation contract.
//
// New passwords are hashed with Argon2id using OWASP-recommended parameters
// (memory 64 MiB / 65536 KiB, iterations 3, parallelism 2, salt 16 bytes,
// key 32 bytes) and encoded in the PHC string format produced by
// github.com/alexedwards/argon2id. Legacy bcrypt hashes ($2a/$2b) are
// accepted for verification so existing rows can log in; on a successful
// bcrypt login the caller re-hashes the password with Argon2id in the same
// transaction (progressive upgrade, see ADR 0004/0005). The upgrade does NOT
// bump token_version.
//
// Passwords are treated as raw UTF-8 byte sequences: they are never trimmed
// or NFKC-normalized. Valid passwords must be 12..128 Unicode code points,
// valid UTF-8, and must not contain NUL or other C0/C1 control characters.
package password

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
	"golang.org/x/crypto/bcrypt"
)

// OWASP-recommended Argon2id parameters. Memory is expressed in KiB by the
// argon2 package; 64 MiB = 64 * 1024 KiB.
const (
	DefaultMemoryKiB   = 64 * 1024
	DefaultIterations  = 3
	DefaultParallelism = 2
	DefaultSaltLength  = 16
	DefaultKeyLength   = 32
)

// Params is the Argon2id parameter set used for new hashes.
var Params = argon2id.Params{
	Memory:      DefaultMemoryKiB,
	Iterations:  DefaultIterations,
	Parallelism: DefaultParallelism,
	SaltLength:  DefaultSaltLength,
	KeyLength:   DefaultKeyLength,
}

// Sentinel errors. Callers map these to stable HTTP error codes; they never
// carry password or hash material.
var (
	ErrEmptyPassword      = errors.New("password: password is empty")
	ErrInvalidLength      = errors.New("password: length must be 12..128 runes")
	ErrInvalidEncoding    = errors.New("password: password is not valid UTF-8")
	ErrControlChar        = errors.New("password: password must not contain NUL or control characters")
	ErrMismatch           = errors.New("password: password does not match")
	ErrInvalidHash        = errors.New("password: stored hash is invalid or unsupported")
	ErrHashParamsExceeded = errors.New("password: stored hash parameters exceed safe limits")
)

// Argon2id parameter upper bounds for verification. A malicious or
// misconfigured stored hash with extreme memory/iterations/parallelism
// could cause a DoS during Compare. These limits are enforced before
// the expensive Argon2id computation runs. They are set well above the
// OWASP-recommended defaults but well below what would cause resource
// exhaustion on a typical server.
const (
	MaxMemoryKiB   = 256 * 1024 // 256 MiB (4× OWASP default)
	MaxIterations  = 10
	MaxParallelism = 8
	MaxKeyLength   = 128
	MaxSaltLength  = 128
)

// Validate enforces the password policy on a raw UTF-8 byte sequence. The
// password is NOT trimmed and NOT NFKC-normalized: the exact bytes the user
// submitted are validated and later hashed. Length is measured in Unicode
// code points (runes) and must be 12..128. Invalid UTF-8, NUL bytes and C0/C1
// control characters are rejected.
func Validate(pw string) error {
	if !utf8.ValidString(pw) {
		return ErrInvalidEncoding
	}
	n := utf8.RuneCountInString(pw)
	if n == 0 {
		return ErrEmptyPassword
	}
	if n < 12 || n > 128 {
		return ErrInvalidLength
	}
	for _, r := range pw {
		// Reject NUL and C0/C1 control characters except nothing — passwords
		// should not contain control chars at all. Newlines/tabs are control
		// chars and are rejected too; a legitimate password will not use them.
		if r == 0 || r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ErrControlChar
		}
	}
	return nil
}

// HashArgon2id hashes a raw password with Argon2id and returns the PHC string.
func HashArgon2id(pw string) (string, error) {
	if err := Validate(pw); err != nil {
		return "", err
	}
	h, err := argon2id.CreateHash(pw, &Params)
	if err != nil {
		return "", fmt.Errorf("password: argon2id hash failed: %w", err)
	}
	return h, nil
}

// IsBcrypt reports whether hash is a legacy bcrypt PHC string ($2a/$2b/$2y).
func IsBcrypt(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$")
}

// IsArgon2id reports whether hash is an Argon2id PHC string.
func IsArgon2id(hash string) bool {
	return strings.HasPrefix(hash, "$argon2id$")
}

// Compare verifies a raw password against a stored PHC hash. It supports
// Argon2id and legacy bcrypt ($2a/$2b/$2y). On a successful bcrypt match the
// caller is responsible for re-hashing with Argon2id (see UpgradeNeeded).
//
// For Argon2id hashes, the parameters embedded in the PHC string are
// validated against safe upper bounds before the expensive computation runs.
// A hash with parameters exceeding those bounds returns ErrHashParamsExceeded
// to prevent DoS from a maliciously crafted stored hash.
//
// A returned error is always one of the typed sentinels; it never
// leaks the hash or password.
func Compare(stored, pw string) error {
	switch {
	case IsArgon2id(stored):
		params, salt, key, err := argon2id.DecodeHash(stored)
		if err != nil {
			return ErrInvalidHash
		}
		if err := validateArgon2Params(params, salt, key); err != nil {
			return err
		}
		match, cmpErr := argon2id.ComparePasswordAndHash(pw, stored)
		if cmpErr != nil {
			return ErrInvalidHash
		}
		if !match {
			return ErrMismatch
		}
		return nil
	case IsBcrypt(stored):
		err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(pw))
		if err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				return ErrMismatch
			}
			return ErrInvalidHash
		}
		return nil
	default:
		return ErrInvalidHash
	}
}

// validateArgon2Params checks that the decoded Argon2id parameters and
// salt/key lengths are within safe bounds. This prevents a maliciously
// crafted stored hash from causing resource exhaustion during verification.
func validateArgon2Params(p *argon2id.Params, salt, key []byte) error {
	if p.Memory > MaxMemoryKiB {
		return ErrHashParamsExceeded
	}
	if p.Iterations > MaxIterations {
		return ErrHashParamsExceeded
	}
	if p.Parallelism > MaxParallelism {
		return ErrHashParamsExceeded
	}
	if p.KeyLength > MaxKeyLength {
		return ErrHashParamsExceeded
	}
	if p.SaltLength > MaxSaltLength {
		return ErrHashParamsExceeded
	}
	if len(salt) > MaxSaltLength {
		return ErrHashParamsExceeded
	}
	if len(key) > MaxKeyLength {
		return ErrHashParamsExceeded
	}
	return nil
}

// UpgradeNeeded reports whether a successfully-verified stored hash should be
// re-hashed with Argon2id. Only bcrypt hashes are eligible for upgrade.
func UpgradeNeeded(stored string) bool {
	return IsBcrypt(stored)
}

// dummyArgonHash is a pre-generated Argon2id hash of a fixed throwaway
// password. It is computed once at package init and reused on every
// user-not-found / password-mismatch path so an attacker cannot distinguish
// "no such user" from "wrong password" by timing. Per-request dummy hash
// generation is forbidden because it would add variable latency.
//
// bcrypt and Argon2id have inherently different timing profiles; complete
// uniformity is not achievable. This is an honest documented limitation.
var dummyArgonHash string

func init() {
	h, err := argon2id.CreateHash("dummy-password-not-a-real-secret", &Params)
	if err != nil {
		panic("password: failed to pre-generate dummy Argon2id hash: " + err.Error())
	}
	dummyArgonHash = h
}

// CompareDummy performs a single Argon2id comparison against the pre-generated
// dummy hash. It burns comparable CPU on the user-not-found path so an attacker
// cannot distinguish "no such user" from "wrong password" by timing. The result
// is always a mismatch; the error is discarded.
func CompareDummy() {
	_, _ = argon2id.ComparePasswordAndHash("wrong-password-dummy", dummyArgonHash)
}
