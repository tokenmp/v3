// Package admin implements Edge/BFF admin endpoints that aggregate downstream
// services for the admin dashboard. All endpoints require role=admin (enforced
// by identity.RequireAdmin middleware in the router).
//
// Currently wired to real downstream data:
//   - GET /api/v1/admin/request-logs      → Logging (cross-user)
//   - GET /api/v1/admin/request-logs/{id} → Logging
//   - GET /api/v1/admin/request-logs/stats → Logging
//   - GET /api/v1/admin/plans             → Billing
//   - GET /api/v1/admin/user-plans        → Billing (cross-user)
//   - GET /api/v1/admin/user/balance      → Billing (cross-user, by query user_id)
//
// Users/keys management endpoints proxy to Auth (TBD: Auth has no admin list
// endpoint yet). Config endpoints proxy to Config Service (TBD: write path).
package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/logging"
)

// Handlers aggregates downstream service clients for admin endpoints.
type Handlers struct {
	Logging *logging.Client
	Billing *billing.Client
	Logger  *slog.Logger
}

// New returns admin Handlers.
func New(lg *logging.Client, bg *billing.Client, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{Logging: lg, Billing: bg, Logger: logger}
}

// Routes registers admin routes on the given chi Router. The caller must wrap
// with identity.Middleware + identity.RequireAdmin.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/api/v1/admin/request-logs", h.ListRequestLogs)
	r.Get("/api/v1/admin/request-logs/{requestId}", h.GetRequestLog)
	r.Get("/api/v1/admin/request-logs/stats", h.GetRequestLogStats)
	r.Get("/api/v1/admin/plans", h.ListPlans)
	r.Get("/api/v1/admin/user-plans", h.ListUserPlans)
}

// ---- Request logs (cross-user) ----

func (h *Handlers) ListRequestLogs(w http.ResponseWriter, r *http.Request) {
	if h.Logging == nil || !h.Logging.Available() {
		writeErr(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	q := r.URL.Query()
	page := parsePositiveInt(q.Get("page"), 1)
	pageSize := parsePositiveInt(q.Get("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	statuses := mapStatusFilter(q.Get("status"))
	result, err := h.Logging.ListLogs(r.Context(), logging.ListFilter{
		Model:    q.Get("model"),
		Statuses: statuses,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		h.logger().Warn("admin list logs failed", "error", err)
		writeErr(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) GetRequestLog(w http.ResponseWriter, r *http.Request) {
	if h.Logging == nil || !h.Logging.Available() {
		writeErr(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	id := chi.URLParam(r, "requestId")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing_request_id")
		return
	}
	detail, err := h.Logging.GetLog(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handlers) GetRequestLogStats(w http.ResponseWriter, r *http.Request) {
	if h.Logging == nil || !h.Logging.Available() {
		writeErr(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	days := parsePositiveInt(r.URL.Query().Get("days"), 7)
	if days > 90 {
		days = 90
	}
	stats, err := h.Logging.GetStats(r.Context(), "", days)
	if err != nil {
		h.logger().Warn("admin stats failed", "error", err)
		writeErr(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// ---- Plans (cross-user) ----

func (h *Handlers) ListPlans(w http.ResponseWriter, r *http.Request) {
	if h.Billing == nil || !h.Billing.Available() {
		writeErr(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	plans, err := h.Billing.ListPlans(r.Context())
	if err != nil {
		h.logger().Warn("admin list plans failed", "error", err)
		writeErr(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (h *Handlers) ListUserPlans(w http.ResponseWriter, r *http.Request) {
	if h.Billing == nil || !h.Billing.Available() {
		writeErr(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	// Billing has no cross-user list endpoint yet; return empty for now.
	// This will be replaced when Billing adds an admin list endpoint.
	userPlans, err := h.Billing.ListAllUserPlans(r.Context())
	if err != nil {
		// Graceful degradation: return empty list instead of 503 when the
		// cross-user endpoint is not available.
		writeJSON(w, http.StatusOK, map[string]any{"plans": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": userPlans})
}

// ---- helpers ----

func (h *Handlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func parsePositiveInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func mapStatusFilter(status string) []string {
	switch status {
	case "success":
		return []string{"success"}
	case "error":
		return []string{"client_error", "upstream_error", "timeout", "transport_error"}
	default:
		return nil
	}
}
