// Package jwtverifier provides Ed25519 (EdDSA) JWT verification for the
// Executor service. It validates access tokens issued by the Auth service
// using only the public key — it never needs (and never imports) the private
// key or any Auth internal package.
//
// Tokens are verified locally: signature, iss, aud, exp, nbf, and the presence
// of sub/jti/role/token_version>=1. Revocation is not checked; tokens within
// their TTL window remain valid even if the user's session has been revoked.
// This is an accepted trade-off documented in the module AGENTS.md.
//
// The role claim is mapped to the identity.Role domain: "user"→RoleService,
// "admin"→RoleAdmin. All other role values are rejected.
package jwtverifier

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

// Sentinel errors. None echo the key, path, or PEM content.
var (
	ErrPublicKeyFileRequired = errors.New("jwtverifier: EXECUTOR_JWT_PUBLIC_KEY_FILE is required")
	ErrPublicKeyReadFailed   = errors.New("jwtverifier: public key file could not be read")
	ErrPublicKeyParseFailed  = errors.New("jwtverifier: public key is not a valid Ed25519 PEM (PKIX)")
	ErrInvalidToken          = errors.New("jwtverifier: invalid token")
	ErrExpired               = errors.New("jwtverifier: token expired")
	ErrUnknownRole           = errors.New("jwtverifier: unknown role")
)

// Claims is the JWT claims payload. Registered claims are validated by the
// jwt/v5 parser; Verify additionally requires sub, jti, role, token_version >= 1
// and that all registered claims (iss, aud, exp, nbf, iat) are present.
type Claims struct {
	jwtv5.RegisteredClaims
	Role         string `json:"role"`
	TokenVersion int    `json:"token_version"`
}

// Verifier validates access tokens and returns the parsed claims. It checks
// the EdDSA signature, iss, aud, exp and nbf. It does NOT check the user's
// current status or token_version against a database — the Executor performs
// purely local verification.
type Verifier struct {
	publicKey ed25519.PublicKey
	issuer    string
	audience  string
}

// NewVerifier builds a Verifier from the public key file path, issuer, and
// audience. The file must contain a PKIX PEM-encoded Ed25519 public key.
// The file path is never echoed in errors.
func NewVerifier(publicKeyFile, issuer, audience string) (*Verifier, error) {
	if publicKeyFile == "" {
		return nil, ErrPublicKeyFileRequired
	}
	if issuer == "" {
		return nil, fmt.Errorf("jwtverifier: issuer is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("jwtverifier: audience is required")
	}
	pub, err := loadPublicKey(publicKeyFile)
	if err != nil {
		return nil, err
	}
	return &Verifier{publicKey: pub, issuer: issuer, audience: audience}, nil
}

// loadPublicKey reads and parses a PKIX PEM-encoded Ed25519 public key from
// the given file path. The path and PEM content are never returned in errors.
func loadPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrPublicKeyReadFailed
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, ErrPublicKeyParseFailed
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrPublicKeyParseFailed
	}
	ed, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, ErrPublicKeyParseFailed
	}
	return ed, nil
}

// Verify parses and validates the token. A returned error is a stable
// sentinel; it never contains the token string or signing error details.
func (v *Verifier) Verify(raw string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwtv5.ParseWithClaims(raw, claims, func(t *jwtv5.Token) (any, error) {
		if _, ok := t.Method.(*jwtv5.SigningMethodEd25519); !ok {
			return nil, ErrInvalidToken // alg confusion rejected
		}
		return v.publicKey, nil
	}, jwtv5.WithIssuer(v.issuer), jwtv5.WithAudience(v.audience), jwtv5.WithValidMethods([]string{"EdDSA"}))
	if err != nil {
		if errors.Is(err, jwtv5.ErrTokenExpired) {
			return nil, ErrExpired
		}
		return nil, ErrInvalidToken
	}
	if !parsed.Valid {
		return nil, ErrInvalidToken
	}
	// Strict claim validation: sub, jti, role, token_version must be present
	// and token_version >= 1.
	if claims.RegisteredClaims.Subject == "" {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.ID == "" {
		return nil, ErrInvalidToken
	}
	if claims.Role == "" {
		return nil, ErrInvalidToken
	}
	if claims.TokenVersion < 1 {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.IssuedAt == nil {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.ExpiresAt == nil {
		return nil, ErrInvalidToken
	}
	if claims.RegisteredClaims.NotBefore == nil {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// PublicKey returns the loaded Ed25519 public key. This is exposed for
// test helpers that need to issue tokens with a matching key pair.
func (v *Verifier) PublicKey() ed25519.PublicKey {
	return v.publicKey
}
