// Package authcontext owns the request-scoped authenticated caller identity
// carried through the Executor request pipeline. It centralizes the private
// context key and the public read accessor so the transport auth boundary
// (executorv1api.AuthMiddleware) and the transport normalizer share one
// canonical, secret-free identity channel without either package exposing a
// forgeable writer on the transport surface.
//
// Boundary note: WithIdentity is the narrow writer exposed for internal-only
// composition. The package lives under services/executor/internal, so it is
// unreachable outside the module; only AuthMiddleware is intended to call
// WithIdentity, because Go does not permit a cross-package middleware to write
// an unexported context key directly. A downstream facade must consume the
// Principal from the normalized nonstream.Request, never from this context.
// The package imports no transport code.
package authcontext

import (
	"context"

	"github.com/tokenmp/v3/services/executor/internal/identity"
)

// contextKey is an unexported type so no other package can construct a value
// that collides with the identity channel. Only this package may write it.
type contextKey struct{}

// WithIdentity attaches a trusted, secret-free identity to ctx using this
// package's private context key. It is the narrow writer exposed for
// internal-only composition: the transport auth boundary (AuthMiddleware) is
// the sole intended caller, because Go does not permit a cross-package
// middleware to write an unexported context key directly. A nil ctx is
// treated as context.Background. The attached value is a defensive copy
// carrying no key material.
//
// This function must not be called by any package other than the transport
// auth boundary; in particular a facade must read the Principal from the
// normalized nonstream.Request rather than reach back into this context.
func WithIdentity(ctx context.Context, id identity.Identity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, cloneIdentity(id))
}

// IdentityFromContext returns a copy of the authenticated caller identity. It
// never exposes API key material; false means authentication did not run or
// did not succeed. A nil ctx returns the zero identity and false.
func IdentityFromContext(ctx context.Context) (identity.Identity, bool) {
	if ctx == nil {
		return identity.Identity{}, false
	}
	resolved, ok := ctx.Value(contextKey{}).(identity.Identity)
	if !ok {
		return identity.Identity{}, false
	}
	return cloneIdentity(resolved), true
}

// cloneIdentity returns a copy of value restricted to its safe, secret-free
// fields. It drops any incidental key material a caller might have attached.
func cloneIdentity(value identity.Identity) identity.Identity {
	return identity.Identity{Subject: value.Subject, KeyID: value.KeyID, Role: value.Role, Status: value.Status}
}
