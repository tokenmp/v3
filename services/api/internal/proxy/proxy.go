// Package proxy implements a reverse proxy that forwards client requests to
// the Executor service.
//
// Auth mode: by default the proxy transparently forwards the client's
// Authorization header so the executor can verify the same JWT the edge
// verified (both use the Auth service's public key). When ServiceToken is
// set, the proxy instead injects a fixed service-level Bearer token,
// overriding the client header — used when the executor runs in API-key
// (identityenv) mode and only needs to confirm the request came from a
// trusted edge.
//
// The proxy is transport-only: it does not inspect or modify the request body.
// Identity, quota reserve/finalize, and logging are handled by surrounding
// middleware in the app layer.
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/settings"
)

// Proxy is a reverse proxy to the Executor service.
type Proxy struct {
	rp           *httputil.ReverseProxy
	serviceToken string          // when non-empty, overrides client Authorization
	settings     *settings.Store // when non-nil, injects per-user auto model pool
}

// New creates a Proxy forwarding to the given executor base URL. When
// serviceToken is non-empty, it is injected as the Authorization Bearer
// header on every forwarded request (identityenv mode). When empty, the
// client's Authorization header is forwarded as-is (JWT passthrough mode).
func New(executorURL, serviceToken string, logger *slog.Logger) (*Proxy, error) {
	return NewWithSettings(executorURL, serviceToken, nil, logger)
}

// NewWithSettings is like New but also injects the caller's per-user auto
// model pool (from the settings store) as the X-Auto-Model-IDs header on
// forwarded /v1/* requests. A nil store disables the injection (auto falls
// back to the executor's global pool).
func NewWithSettings(executorURL, serviceToken string, st *settings.Store, logger *slog.Logger) (*Proxy, error) {
	target, err := url.Parse(strings.TrimSuffix(executorURL, "/"))
	if err != nil {
		return nil, err
	}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			if serviceToken != "" {
				// Inject the edge service token for executor auth (identityenv mode).
				req.Header.Set("Authorization", "Bearer "+serviceToken)
			}
			// When serviceToken is empty, the client's Authorization header
			// is forwarded as-is (JWT passthrough mode).

			// Forward the verified end-user id so the executor records the
			// real caller in its request log instead of the edge service
			// identity. The edge has already authenticated the user (JWT or
			// API key via Auth verify-key); the executor trusts this header
			// because it only accepts requests bearing the edge service
			// token. Strip any client-supplied value first to prevent
			// spoofing in JWT-passthrough mode.
			req.Header.Del("X-User-ID")
			if claims, ok := identity.FromContext(req.Context()); ok && claims.Subject != "" {
				req.Header.Set("X-User-ID", claims.Subject)
				// Inject the per-user auto model pool override when configured.
				// The executor honors it only for model=auto requests and ignores
				// the header otherwise. Strip any client-supplied value first to
				// prevent spoofing in JWT-passthrough mode.
				req.Header.Del("X-Auto-Model-IDs")
				if st != nil {
					if pool := st.AutoModelIDs(claims.Subject); len(pool) > 0 {
						req.Header.Set("X-Auto-Model-IDs", strings.Join(pool, ","))
					}
				}
			}

			// Remove hop-by-hop headers.
			req.Header.Del("X-Forwarded-For")
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if logger != nil {
				logger.Error("proxy error", "error", err)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"upstream_unavailable","message":"Executor service is unavailable"}}`))
		},
	}
	return &Proxy{rp: rp, serviceToken: serviceToken, settings: st}, nil
}

// ServeHTTP forwards the request to the executor.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.rp.ServeHTTP(w, r)
}
