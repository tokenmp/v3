// Package server wires the auth service HTTP server via the generated
// contract-first Chi strict handler. It re-exports the transport layer's
// Server type for backward compatibility with existing callers (main.go,
// tests).
//
// The actual routing is defined by the OpenAPI contract at
// packages/contracts/openapi/auth/v1.yaml and generated into
// internal/contract/authv1/server.gen.go (with models.gen.go). The strict implementation lives in
// internal/transport/authv1api. Body validation (size limits,
// DisallowUnknownFields, trailing rejection, logout normalization) is
// enforced by Chi middleware in the transport layer before the generated
// handler decodes JSON bodies.
package server

import (
	"time"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/transport/authv1api"
)

// Pinger is the readiness contract injected into /readyz.
type Pinger = authv1api.Pinger

// Server wraps an *http.Server with the auth service routes.
type Server = authv1api.Server

// UserStore is the minimal port the middleware needs to load a user on each
// request.
type UserStore = authv1api.UserStore

// New builds a Chi router and the configured http.Server. The router exposes
// the health endpoints plus the auth identity flow routes, all registered by
// the generated Chi strict handler from the OpenAPI contract.
// jwtVerifier and userStore are wired for the authenticated
// routes (me / password / logout-all); authService backs all routes.
func New(addr string, pinger Pinger, jwtVerifier *jwt.Verifier, authService *auth.Service, userStore UserStore) *Server {
	return authv1api.NewServer(authv1api.ServerConfig{
		Addr:        addr,
		Pinger:      pinger,
		JWTVerifier: jwtVerifier,
		UserStore:   userStore,
		AuthService: authService,
		AccessTTL:   15 * time.Minute,
	})
}
