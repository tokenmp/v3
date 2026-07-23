package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns an http.Handler that serves Prometheus metrics on GET only.
// POST returns 405; any subpath returns 404. The response carries
// Cache-Control: no-store and content-type text/plain; version=0.0.4.
// When disabled is true, the handler returns 404 for all requests.
func Handler(registry *prometheus.Registry, disabled bool) http.Handler {
	if disabled {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}

	inner := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject subpaths: /metrics/foo → 404.
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			inner.ServeHTTP(w, r)
		default:
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}
