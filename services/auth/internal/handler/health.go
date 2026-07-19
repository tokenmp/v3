// Package handler exposes the HTTP handlers for the auth service.
//
// In this foundation only the health endpoints exist: /healthz reports liveness
// (process up), /readyz reports readiness and depends on an injectable Pinger
// so readiness can be controlled in tests without a real database.
//
// Readiness failures return 503 and never leak the underlying error message.
// HEAD requests on either endpoint write the response headers (including
// Cache-Control) and the status code but no body, per HTTP semantics; GET
// requests return the JSON HealthResponse.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger is the readiness contract the /readyz handler depends on.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthResponse is the uniform JSON shape returned by /healthz (GET) and
// /readyz (GET). HEAD responses carry the same status and headers but no body.
type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

const (
	statusOK      = "ok"
	statusUnready = "unready"
	serviceName   = "auth"
)

// writeHealthJSON writes the JSON payload for GET requests. For HEAD requests it
// writes only the headers and status code, with no body. Cache-Control:
// no-store is always set so intermediaries never cache health probes.
func writeHealthJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// Healthz returns 200 with the liveness payload. It never depends on external
// resources; it only confirms the process and HTTP server are alive. HEAD
// returns 200 with the same headers and an empty body.
func Healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeHealthJSON(w, r, http.StatusOK, HealthResponse{
		Status:    statusOK,
		Service:   serviceName,
		Timestamp: nowRFC3339(),
	})
}

// Readyz returns 200 when the readiness Pinger succeeds, 503 otherwise. The
// underlying error is never returned to the client to avoid leaking
// internals. HEAD returns 200/503 with the same headers and an empty body.
func Readyz(pinger Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		status := statusOK
		httpStatus := http.StatusOK
		if err := pinger.Ping(ctx); err != nil {
			status = statusUnready
			httpStatus = http.StatusServiceUnavailable
		}
		writeHealthJSON(w, r, httpStatus, HealthResponse{
			Status:    status,
			Service:   serviceName,
			Timestamp: nowRFC3339(),
		})
	}
}
