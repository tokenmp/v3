package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
)

// buildRouterWithStore assembles a Chi router with the auth routes + the
// RequireUser middleware wired to an env-backed UserStore so the
// authenticated endpoints resolve the current user against the fake store.
func (e *testEnv) routerWithStore(t *testing.T) http.Handler {
	t.Helper()
	store := &envUserStore{e: e}
	r := chi.NewRouter()
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/register", e.authH.Register)
		r.Post("/login", e.authH.Login)
		r.Post("/refresh", e.authH.Refresh)
		r.Post("/logout", e.authH.Logout)
		r.Group(func(r chi.Router) {
			r.Use(RequireUser(e.verifier, store))
			r.Get("/me", e.authH.Me)
			r.Put("/password", e.authH.ChangePassword)
			r.Post("/logout-all", e.authH.LogoutAll)
		})
	})
	return r
}

// envUserStore adapts the in-memory fakeStore into the handler.UserStore
// interface required by RequireUser.
type envUserStore struct{ e *testEnv }

func (s *envUserStore) FindByID(ctx context.Context, id string) (string, int, string, error) {
	u, ok := s.e.users.byID[id]
	if !ok {
		return "", 0, "", errNotFound
	}
	return string(u.Status), u.TokenVersion, string(u.Role), nil
}

// errNotFound is a stand-in so the middleware's "any error → 401" path works
// without importing the repository sentinel. RequireUser maps every error to a
// uniform 401 regardless of the concrete type.
var errNotFound = notFoundErr{}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }
