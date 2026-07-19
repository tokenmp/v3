// Package server wires the auth service HTTP server: Chi router, middleware,
// health routes, auth identity flow routes and graceful shutdown.
package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/handler"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
)

// Pinger is the readiness contract injected into /readyz.
type Pinger = handler.Pinger

// Server wraps an *http.Server with the auth service routes.
type Server struct {
	httpSrv *http.Server
}

// New builds a Chi router and the configured http.Server. The router exposes
// the health endpoints plus the auth identity flow routes under
// /api/v1/auth/*. jwtVerifier and userStore are wired for the authenticated
// routes (me / password / logout-all); authService backs all routes.
func New(addr string, pinger Pinger, jwtVerifier *jwt.Verifier, authService *auth.Service, userStore handler.UserStore) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Health endpoints (liveness / readiness).
	r.Get("/healthz", handler.Healthz)
	r.Head("/healthz", handler.Healthz)
	r.Get("/readyz", handler.Readyz(pinger))
	r.Head("/readyz", handler.Readyz(pinger))

	// Auth identity flow routes.
	authH := handler.NewAuthHandler(authService)
	r.Route("/api/v1/auth", func(r chi.Router) {
		// Public endpoints (no Bearer required).
		r.Post("/register", authH.Register)
		r.Post("/login", authH.Login)
		r.Post("/refresh", authH.Refresh)
		r.Post("/logout", authH.Logout)

		// Authenticated endpoints (Bearer required).
		if jwtVerifier != nil && userStore != nil {
			r.Group(func(r chi.Router) {
				r.Use(handler.RequireUser(jwtVerifier, userStore))
				r.Get("/me", authH.Me)
				r.Put("/password", authH.ChangePassword)
				r.Post("/logout-all", authH.LogoutAll)
			})
		}
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return &Server{httpSrv: srv}
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server within the given timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// Router exposes the underlying mux for testing.
func (s *Server) Router() http.Handler {
	return s.httpSrv.Handler
}
