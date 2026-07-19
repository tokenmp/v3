// Package handler: middleware for bearer-token authentication.
//
// RequireUser validates an Ed25519 access token, loads the user from the DB on
// every request (so a disabled account or a bumped token_version invalidates
// outstanding tokens immediately), and injects the authenticated user id
// into the request context. A token whose token_version does not match the
// current users.token_version is rejected with 401 (this is the strong
// revocation path — logout-all / password change bump token_version).
package handler

import (
	"context"
	"net/http"

	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
)

type ctxKey string

const (
	ctxUserID ctxKey = "auth_user_id"
)

// UserStore is the minimal port the middleware needs to load a user on each
// request. It mirrors auth.UserRepository but is declared here so the
// middleware does not depend on the concrete repository package.
type UserStore interface {
	FindByID(ctx context.Context, id string) (status string, tokenVersion int, role string, err error)
}

// RequireUser returns middleware that validates the Bearer access token and
// loads the user. On failure it writes a uniform 401 and stops the chain.
// On success it injects the user id into the context.
func RequireUser(verifier *jwt.Verifier, store UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerFromHeader(r.Header)
			if raw == "" {
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, "authentication required")
				return
			}
			claims, err := verifier.Verify(raw)
			if err != nil {
				writeError(w, http.StatusUnauthorized, CodeInvalidToken, "invalid or expired access token")
				return
			}
			status, tv, _, sErr := store.FindByID(r.Context(), claims.RegisteredClaims.Subject)
			if sErr != nil {
				// Not found or internal error → 401. Do not leak which.
				writeError(w, http.StatusUnauthorized, CodeInvalidToken, "invalid or expired access token")
				return
			}
			if status != "active" {
				writeError(w, http.StatusUnauthorized, CodeInvalidToken, "invalid or expired access token")
				return
			}
			if claims.TokenVersion != tv {
				writeError(w, http.StatusUnauthorized, CodeInvalidToken, "token has been revoked")
				return
			}
			ctx := WithUserID(r.Context(), claims.RegisteredClaims.Subject)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithUserID injects the authenticated user id into the context.
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxUserID, userID)
}

// UserIDFromContext returns the authenticated user id, or "" if unset.
func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxUserID).(string); ok {
		return v
	}
	return ""
}
