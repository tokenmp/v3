// Package identity handles client authentication for the Edge/BFF layer.
// It verifies JWT access tokens (EdDSA/Ed25519) issued by the Auth service
// using a local public key, and extracts the authenticated subject identity
// into the request context for downstream middleware.
package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
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

// noopVerifier accepts any non-empty Bearer token. It is used when
// API_JWT_PUBLIC_KEY_FILE is unset (dev-only).
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

// ErrUnauthenticated is returned when the token is missing, malformed, or
// fails verification. It never embeds the token or key material.
var ErrUnauthenticated = errors.New("identity: unauthenticated")

// NewVerifier loads the Ed25519 public key from the given PEM file path. If
// keyFile is empty, a noop verifier is returned (dev-only; production must
// set a key file).
func NewVerifier(keyFile, issuer, audience string, logger *slog.Logger) (Verifier, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if keyFile == "" {
		logger.Warn("identity: JWT public key not configured; using noop verifier (dev-only)")
		return noopVerifier{}, nil
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("identity: read public key file: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("identity: public key file is not valid PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: parse public key: %w", err)
	}
	edPub, ok := pub.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("identity: public key is not Ed25519")
	}
	return &jwtVerifier{pub: edPub, issuer: issuer, audience: audience, logger: logger}, nil
}

func (v *jwtVerifier) Verify(ctx context.Context, tokenStr string) (Claims, error) {
	if tokenStr == "" {
		return Claims{}, ErrUnauthenticated
	}
	claims := &jwt.RegisteredClaims{}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
	}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		return v.pub, nil
	}, opts...)
	if err != nil {
		return Claims{}, ErrUnauthenticated
	}
	role := "user"
	if claims.Subject == "" {
		return Claims{}, ErrUnauthenticated
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
