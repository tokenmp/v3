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

// AuthMiddleware is the outer authentication boundary for generated Executor
// handlers. Compose it outside CaptureRawBody: AuthMiddleware(source)(
// CaptureRawBody(handler)). This ensures rejected requests never read or parse
// their body. /healthz is deliberately anonymous; every /v1 path, including
// unknown paths that will become 404 downstream, is protected.
func AuthMiddleware(source identity.Port) func(http.Handler) http.Handler {
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

func writeUnauthorized(w http.ResponseWriter, path string) {
	w.Header().Set("Cache-Control", "no-store")
	if path == anthropicMessagesPath {
		_ = writeJSON(w, http.StatusUnauthorized, anthropicError(invalidAPIKeyCode, "authentication_error", invalidAPIKeyMessage, ""))
		return
	}
	_ = writeJSON(w, http.StatusUnauthorized, openAIError(http.StatusUnauthorized, invalidAPIKeyCode, "authentication_error", invalidAPIKeyMessage))
}
