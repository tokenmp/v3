// Package server wires the auth service HTTP server: Chi router, middleware,
// health routes and graceful shutdown.
package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tokenmp/v3/services/auth/internal/handler"
)

// Pinger is the readiness contract injected into /readyz.
type Pinger = handler.Pinger

// Server wraps an *http.Server with the auth service routes.
type Server struct {
	httpSrv *http.Server
}

// New builds a Chi router and the configured http.Server.
func New(addr string, pinger Pinger) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handler.Healthz)
	r.Head("/healthz", handler.Healthz)
	r.Get("/readyz", handler.Readyz(pinger))
	r.Head("/readyz", handler.Readyz(pinger))

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
