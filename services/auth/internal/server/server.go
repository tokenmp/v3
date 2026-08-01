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

	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
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

// APIKeyStore is the persistence port for API key management endpoints.
type APIKeyStore = authv1api.APIKeyStore

// RateLimitMiddleware is the optional StrictMiddlewareFunc that enforces the
// shared token-bucket limits on login/register/refresh.
type RateLimitMiddleware = authv1.StrictMiddlewareFunc

// TrustedIPResolver resolves the client IP from a trusted-proxy allowlist.
type TrustedIPResolver = trustedip.Resolver

// New builds a Chi router and the configured http.Server. The router exposes
// the health endpoints plus the auth identity flow routes, all registered by
// the generated Chi strict handler from the OpenAPI contract.
// jwtVerifier and userStore are wired for the authenticated
// routes (me / password / logout-all); authService backs all routes.
// apiKeyStore 启用 /api/v1/auth/keys* 管理端点；nil 时这些端点返回 500。
func New(addr string, pinger Pinger, jwtVerifier *jwt.Verifier, authService *auth.Service, userStore UserStore, apiKeyStore APIKeyStore) *Server {
	return authv1api.NewServer(authv1api.ServerConfig{
		Addr:        addr,
		Pinger:      pinger,
		JWTVerifier: jwtVerifier,
		UserStore:   userStore,
		AuthService: authService,
		AccessTTL:   15 * time.Minute,
		APIKeyStore: apiKeyStore,
	})
}

// NewWithRateLimit builds a server identical to New but additionally wires
// the optional rate-limit StrictMiddlewareFunc and trusted-IP resolver. When
// rlMW is nil rate limiting is disabled; when resolver is nil the legacy
// unconditional chi RealIP middleware is used.
func NewWithRateLimit(addr string, pinger Pinger, jwtVerifier *jwt.Verifier, authService *auth.Service, userStore UserStore, apiKeyStore APIKeyStore, rlMW RateLimitMiddleware, resolver *TrustedIPResolver) *Server {
	return authv1api.NewServer(authv1api.ServerConfig{
		Addr:                addr,
		Pinger:              pinger,
		JWTVerifier:         jwtVerifier,
		UserStore:           userStore,
		AuthService:         authService,
		AccessTTL:           15 * time.Minute,
		APIKeyStore:         apiKeyStore,
		RateLimitMiddleware: rlMW,
		TrustedIPResolver:   resolver,
	})
}
