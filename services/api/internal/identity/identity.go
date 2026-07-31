// Package identity handles client authentication for the Edge/BFF layer.
// It verifies JWT access tokens (EdDSA/Ed25519) issued by the Auth service
// using a local public key, and extracts the authenticated subject identity
// into the request context for downstream middleware.
package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Claims holds the verified JWT claims used by the edge.
type Claims struct {
	Subject string
	Role    string
}

type contextKey struct{}

var claimsKey contextKey

// Verifier verifies a raw JWT string and returns the verified Claims.
type Verifier interface {
	Verify(ctx context.Context, token string) (Claims, error)
}

// noopVerifier accepts any non-empty Bearer token. It is available only via
// NewNoopVerifier, which callers must explicitly opt into for local development
// and tests.
type noopVerifier struct{}

func (noopVerifier) Verify(_ context.Context, token string) (Claims, error) {
	if token == "" {
		return Claims{}, ErrUnauthenticated
	}
	return Claims{Subject: token, Role: "user"}, nil
}

// jwtVerifier verifies Ed25519 (EdDSA) JWTs against a loaded public key.
type jwtVerifier struct {
	pub      ed25519.PublicKey
	issuer   string
	audience string
	logger   *slog.Logger
}

// Sentinel errors never embed key paths, PEM content, or token material.
var (
	ErrUnauthenticated       = errors.New("identity: unauthenticated")
	ErrPublicKeyFileRequired = errors.New("identity: API_JWT_PUBLIC_KEY_FILE is required")
	ErrPublicKeyReadFailed   = errors.New("identity: public key file could not be read")
	ErrPublicKeyParseFailed  = errors.New("identity: public key is not a valid Ed25519 PEM (PKIX)")
)

// NewNoopVerifier returns the development-only verifier that accepts any
// non-empty Bearer token. Production callers must use NewVerifier instead.
func NewNoopVerifier() Verifier {
	return noopVerifier{}
}

// NewVerifier loads an Ed25519 public key from a PKIX PEM file. Empty,
// unreadable, or malformed public-key configuration fails closed.
func NewVerifier(keyFile, issuer, audience string, logger *slog.Logger) (Verifier, error) {
	if keyFile == "" {
		return nil, ErrPublicKeyFileRequired
	}
	if issuer == "" {
		return nil, errors.New("identity: JWT issuer is required")
	}
	if audience == "" {
		return nil, errors.New("identity: JWT audience is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, ErrPublicKeyReadFailed
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, ErrPublicKeyParseFailed
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrPublicKeyParseFailed
	}
	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, ErrPublicKeyParseFailed
	}
	return &jwtVerifier{pub: edPub, issuer: issuer, audience: audience, logger: logger}, nil
}

// edgeClaims extends RegisteredClaims with the role private claim issued by
// the Auth service. Using a typed struct (not MapClaims) prevents zero-value
// silent pass-through.
type edgeClaims struct {
	jwt.RegisteredClaims
	Role string `json:"role"`
}

func (v *jwtVerifier) Verify(ctx context.Context, tokenStr string) (Claims, error) {
	if tokenStr == "" {
		return Claims{}, ErrUnauthenticated
	}
	claims := &edgeClaims{}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
	}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, ErrUnauthenticated
		}
		return v.pub, nil
	}, opts...)
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	if claims.Subject == "" {
		return Claims{}, ErrUnauthenticated
	}
	// Auth issues "user" or "admin"; default to "user" when absent so
	// non-admin tokens are still accepted for user-scoped endpoints.
	role := "user"
	if claims.Role != "" {
		role = claims.Role
	}
	return Claims{Subject: claims.Subject, Role: role}, nil
}

// Middleware returns an http middleware that extracts and verifies the
// Bearer token from the Authorization header. On success, Claims are stored
// in the request context. On failure, a 401 JSON response is returned.
func Middleware(v Verifier, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearer(r)
			claims, err := v.Verify(r.Context(), token)
			if err != nil {
				logger.Debug("auth failed", "error", err)
				writeUnauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext extracts the verified Claims from the request context. Returns
// false if no claims are present.
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey).(Claims)
	return c, ok
}

// WithClaims returns a copy of ctx carrying the given claims. It is intended
// for middleware and tests that need to inject a verified identity without
// running the full JWT verification chain.
func WithClaims(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// RequireAdmin is a middleware that rejects requests from non-admin users
// with 403. It must be placed after Middleware (which populates Claims in
// context).
func RequireAdmin(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok || claims.Role != "admin" {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractBearer pulls the raw token from the Authorization header.
func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	// Support "Bearer <token>".
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// writeUnauthorized writes a protocol-native 401 JSON error.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"Missing or invalid credentials"}}`))
}
