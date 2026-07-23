package jwtverifier

import (
	"context"
	"errors"
	"fmt"

	"github.com/tokenmp/v3/services/executor/internal/identity"
)

// Source implements identity.Port by verifying JWT access tokens. It resolves
// a raw Bearer token to an identity.Identity using local EdDSA verification
// — no network call to the Auth service is made.
//
// Role mapping: "user"→RoleService, "admin"→RoleAdmin. Unknown roles yield
// ErrUnknownKey. KeyID is left empty because JWTs carry no kid. Status is
// always StatusActive because the Executor does not check revocation status;
// tokens within their TTL window remain valid even if the user's session has
// been revoked. This is an accepted trade-off.
type Source struct {
	verifier *Verifier
}

var _ identity.Port = (*Source)(nil)

// NewSource builds a JWT identity source from the public key file path,
// issuer, and audience. The public key file is read and parsed at
// construction time; startup fails closed if the file is missing or
// malformed.
func NewSource(publicKeyFile, issuer, audience string) (*Source, error) {
	v, err := NewVerifier(publicKeyFile, issuer, audience)
	if err != nil {
		return nil, err
	}
	return &Source{verifier: v}, nil
}

// LookupByKey verifies the raw JWT token and maps its claims to an
// identity.Identity. It implements identity.Port.
func (s *Source) LookupByKey(ctx context.Context, rawToken string) (identity.Identity, error) {
	if ctx == nil {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	if err := ctx.Err(); err != nil {
		return identity.Identity{}, err
	}
	if s == nil || s.verifier == nil || rawToken == "" {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	claims, err := s.verifier.Verify(rawToken)
	if err != nil {
		if errors.Is(err, ErrExpired) || errors.Is(err, ErrInvalidToken) {
			return identity.Identity{}, identity.ErrUnknownKey
		}
		return identity.Identity{}, identity.ErrUnknownKey
	}
	role, err := mapRole(claims.Role)
	if err != nil {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	return identity.Identity{
		Subject: claims.Subject,
		KeyID:   "", // JWT has no kid
		Role:    role,
		Status:  identity.StatusActive,
	}, nil
}

// mapRole maps the JWT role claim to the identity.Role domain.
func mapRole(role string) (identity.Role, error) {
	switch role {
	case "user":
		return identity.RoleService, nil
	case "admin":
		return identity.RoleAdmin, nil
	default:
		return "", ErrUnknownRole
	}
}

func (Source) String() string   { return "jwtverifier.Source([REDACTED])" }
func (Source) GoString() string { return "jwtverifier.Source([REDACTED])" }
func (Source) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("jwtverifier.Source([REDACTED])"))
}
