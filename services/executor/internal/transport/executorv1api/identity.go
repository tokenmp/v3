package executorv1api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/tokenmp/v3/services/executor/internal/authcontext"
	"github.com/tokenmp/v3/services/executor/internal/identity"
)

const (
	invalidAPIKeyCode    = "INVALID_API_KEY"
	invalidAPIKeyMessage = "Invalid API key provided."
)

// AuthOptions configures the authentication trust model. The zero value is
// JWT/API-key passthrough: the authenticated identity is authoritative and
// client supplied identity headers are ignored.
type AuthOptions struct {
	// DelegatedEdgeSubject enables delegated-edge mode only for this already
	// authenticated service identity. It must be supplied from fail-closed
	// runtime configuration, never from an HTTP header.
	DelegatedEdgeSubject string
}

// AuthMiddleware is the outer authentication boundary for generated Executor
// handlers in passthrough mode. Compose it outside CaptureRawBody:
// AuthMiddleware(source)(CaptureRawBody(handler)). This ensures rejected
// requests never read or parse their body. /healthz is deliberately anonymous;
// every /v1 path, including unknown paths that will become 404 downstream, is
// protected.
func AuthMiddleware(source identity.Port) func(http.Handler) http.Handler {
	return AuthMiddlewareWithOptions(source, AuthOptions{})
}

// AuthMiddlewareWithOptions applies an explicit identity trust model. In the
// default passthrough mode, only the authenticated identity is used. In
// delegated-edge mode, X-User-ID is accepted only after the configured Edge
// service identity has authenticated and only when its asserted subject is
// valid. This prevents a directly authenticated user from impersonating a
// different subject with a bare header.
func AuthMiddlewareWithOptions(source identity.Port, options AuthOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || !(r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/")) {
				next.ServeHTTP(w, r)
				return
			}
			if err := r.Context().Err(); err != nil {
				return
			}
			if isNilPort(source) {
				writeUnauthorized(w, r.URL.Path)
				return
			}
			key, ok := bearerToken(r.Header.Values("Authorization"))
			if !ok {
				writeUnauthorized(w, r.URL.Path)
				return
			}
			resolved, err := source.LookupByKey(r.Context(), key)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || r.Context().Err() != nil {
				return
			}
			if err != nil || resolved.Status != identity.StatusActive || (resolved.Role != identity.RoleService && resolved.Role != identity.RoleAdmin) {
				writeUnauthorized(w, r.URL.Path)
				return
			}
			if options.DelegatedEdgeSubject != "" {
				// Delegation is deliberately opt-in and binds the assertion to the
				// configured service identity authenticated above. A user JWT/API
				// key that reaches Executor directly cannot use X-User-ID.
				if resolved.Role != identity.RoleService || resolved.Subject != options.DelegatedEdgeSubject {
					writeUnauthorized(w, r.URL.Path)
					return
				}
				uid := r.Header.Get("X-User-ID")
				if !validDelegatedSubject(uid) {
					writeUnauthorized(w, r.URL.Path)
					return
				}
				resolved.Subject = uid
			}
			next.ServeHTTP(w, r.WithContext(authcontext.WithIdentity(r.Context(), resolved)))
		})
	}
}
func isNilPort(port identity.Port) bool {
	if port == nil {
		return true
	}
	v := reflect.ValueOf(port)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !validBearerKey(parts[1]) {
		return "", false
	}
	return parts[1], true
}

func validBearerKey(value string) bool {
	if len(value) == 0 || len(value) > 512 {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// validDelegatedSubject matches identityenv's bounded printable subject
// grammar. It intentionally has a tighter bound than a bearer credential.
func validDelegatedSubject(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func writeUnauthorized(w http.ResponseWriter, path string) {
	w.Header().Set("Cache-Control", "no-store")
	if path == anthropicMessagesPath {
		_ = writeJSON(w, http.StatusUnauthorized, anthropicError(invalidAPIKeyCode, "authentication_error", invalidAPIKeyMessage, ""))
		return
	}
	_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, invalidAPIKeyCode, "authentication_error", invalidAPIKeyMessage))
}
