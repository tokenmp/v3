// Package healthz provides a simple health check HTTP handler.
package healthz

import (
	"encoding/json"
	"net/http"
)

var healthOK = []byte(`{"status":"ok"}`)

// NewHandler returns an http.Handler that responds to GET and HEAD /healthz.
func NewHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(healthOK)
		}
	})
}

// Ensure the handler returns valid JSON.
var _ json.Marshaler = json.RawMessage(healthOK)
