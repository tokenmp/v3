// Package proxy implements a reverse proxy that forwards client requests to
// the Executor service. It injects the edge's service-level Bearer token so
// the executor can verify the request originates from a trusted edge.
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
)

// Proxy is a reverse proxy to the Executor service.
type Proxy struct {
	rp    *httputil.ReverseProxy
	token string
}

// New creates a Proxy forwarding to the given executor base URL. The token
// is injected as the Authorization Bearer header on every forwarded request.
func New(executorURL, token string, logger *slog.Logger) (*Proxy, error) {
	target, err := url.Parse(strings.TrimSuffix(executorURL, "/"))
	if err != nil {
		return nil, err
	}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// Inject the edge service token for executor auth.
			req.Header.Set("Authorization", "Bearer "+token)
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
	return &Proxy{rp: rp, token: token}, nil
}

// ServeHTTP forwards the request to the executor.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.rp.ServeHTTP(w, r)
}
