// Package adminauth implements service-to-service authorization for the Config
// Service write/admin endpoints using a shared secret loaded from a file with
// constant-time comparison.
//
// Design (publish hardening):
//   - The secret is loaded once at startup from a file path (CONFIG_ADMIN_TOKEN_FILE).
//     A missing/empty file in production fails fast: the write path is disabled
//     and all admin/write endpoints return 503. This prevents a default-open
//     half-secure write path.
//   - Authorization uses Authorization: Bearer <token> or X-Admin-Token: <token>,
//     compared with crypto/subtle.ConstantTimeCompare to avoid timing oracles.
//   - Errors never echo the token, the file path, or whether the secret is
//     configured; the response is a protocol-native 401/403 with a fixed message.
//   - Read-only snapshot endpoints (/healthz, /readyz, /v1/config/snapshots/latest,
//     /v1/config/models/catalog) are NOT protected by this middleware — they
//     remain anonymous/public reads. The middleware is applied only to write
//     and admin routes so a misconfiguration cannot accidentally lock reads.
package adminauth

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
)

// Sentinel errors. They do not echo the file path or token.
var (
	ErrTokenFileRequired = errors.New("adminauth: admin token file required")
	ErrTokenLoadFailed   = errors.New("adminauth: admin token load failed")
)

// Middleware authorizes write/admin requests using a shared secret compared
// with constant time. When allowNoAuth is true (dev-only), it is a no-op.
type Middleware struct {
	token       []byte
	allowNoAuth bool
}

// New loads the shared secret from the file path. When allowNoAuth is true,
// the file may be empty (dev mode). Otherwise a non-empty file is required
// and a read/format error fails fast with a sentinel that never leaks the path.
func New(tokenFile string, allowNoAuth bool) (*Middleware, error) {
	if allowNoAuth {
		return &Middleware{allowNoAuth: true}, nil
	}
	if strings.TrimSpace(tokenFile) == "" {
		return nil, ErrTokenFileRequired
	}
	b, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil, ErrTokenLoadFailed
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return nil, ErrTokenLoadFailed
	}
	return &Middleware{token: []byte(t)}, nil
}

// Wrap returns a middleware that authorizes each request before delegating to
// next. Unauthorized requests get a protocol-native 401 JSON error.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	if m == nil || m.allowNoAuth {
		return next
	}
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {}) // no-op; kept for future lazy init hooks
		if !m.authorized(r) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"unauthorized"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized extracts the candidate token and compares it in constant time.
func (m *Middleware) authorized(r *http.Request) bool {
	if len(m.token) == 0 {
		return false
	}
	candidate := extractToken(r)
	if len(candidate) == 0 {
		return false
	}
	// Constant-time compare; length differences are not leaked via early return
	// because ConstantTimeCompare handles unequal lengths securely (returns 0).
	return subtle.ConstantTimeCompare([]byte(candidate), m.token) == 1
}

// extractToken pulls the shared secret from Bearer or X-Admin-Token headers.
func extractToken(r *http.Request) string {
	if v := r.Header.Get("X-Admin-Token"); v != "" {
		return strings.TrimSpace(v)
	}
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return strings.TrimSpace(v[len(prefix):])
	}
	return ""
}

// Configured reports whether the middleware enforces authorization. Returns
// false in dev no-auth mode.
func (m *Middleware) Configured() bool {
	if m == nil {
		return false
	}
	return !m.allowNoAuth
}
