// Package jwtverifier verifies Ed25519 (EdDSA) JWT access tokens issued by
// the Auth Service, using the Auth public key. The notice service verifies
// tokens but never issues them.
//
// Key file paths are never echoed in errors; the package returns stable
// classified sentinels. A successful verification yields the authenticated
// subject (the `sub` claim), which is the Auth users.id.
package jwtverifier

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Stable classified errors. They never embed the key file path or contents.
var (
	ErrVerifier     = errors.New("jwt verifier: initialization failed")
	ErrInvalidToken = errors.New("jwt verifier: invalid token")
)

// Verifier verifies Ed25519 JWTs against a fixed public key, issuer and
// audience.
type Verifier struct {
	publicKey ed25519.PublicKey
	issuer    string
	audience  string
}

// New loads the Ed25519 public key from a PKIX PEM file and returns a
// Verifier. The file path is never echoed in the returned error.
func New(publicKeyFile, issuer, audience string) (*Verifier, error) {
	if publicKeyFile == "" {
		return nil, fmt.Errorf("%w: public key file path is empty", ErrVerifier)
	}
	raw, err := os.ReadFile(publicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read public key file", ErrVerifier)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%w: public key is not PEM-encoded", ErrVerifier)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot parse public key", ErrVerifier)
	}
	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: public key is not Ed25519", ErrVerifier)
	}
	if issuer == "" || audience == "" {
		return nil, fmt.Errorf("%w: issuer and audience are required", ErrVerifier)
	}
	return &Verifier{publicKey: edPub, issuer: issuer, audience: audience}, nil
}

// Subject is the authenticated principal extracted from a verified token.
type Subject struct {
	UserID string // the `sub` claim (Auth users.id)
	Role   string // the `role` claim, if present
}

// Verify parses and validates a raw JWT. Returns ErrInvalidToken on any
// failure (signature, expiry, issuer, audience). The error does not disclose
// which check failed.
func (v *Verifier) Verify(raw string) (Subject, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Subject{}, ErrInvalidToken
	}
	// Strip an optional "Bearer " prefix defensively; the middleware already
	// does this, but Verify is safe to call with a raw token too.
	raw = strings.TrimPrefix(raw, "Bearer ")
	// EdDSA with Ed25519. jwt/v5 resolves the algorithm from the JOSE header.
	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrInvalidToken)
		}
		return v.publicKey, nil
	},
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jotWithIssuedAtPresent(),
		jotWithExpiry(),
	)
	if err != nil || token == nil || !token.Valid {
		return Subject{}, ErrInvalidToken
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Subject{}, ErrInvalidToken
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return Subject{}, ErrInvalidToken
	}
	role, _ := claims["role"].(string)
	return Subject{UserID: sub, Role: role}, nil
}

// jotWithIssuedAtPresent requires the nbf (and implicitly iat) to be sane.
func jotWithIssuedAtPresent() jwt.ParserOption {
	return jwt.WithValidMethods([]string{"EdDSA"})
}

// jotWithExpiry enforces exp. jwt/v5 checks exp by default when present.
func jotWithExpiry() jwt.ParserOption {
	return jwt.WithExpirationRequired()
}
