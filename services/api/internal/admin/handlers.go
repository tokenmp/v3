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
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/services/api/internal/billing"
	"github.com/tokenmp/v3/services/api/internal/logging"
)

// Handlers aggregates downstream service clients for admin endpoints.
type Handlers struct {
	Logging *logging.Client
	Billing *billing.Client
	Auth    *AuthClient
	Logger  *slog.Logger
}

// New returns admin Handlers.
func New(lg *logging.Client, bg *billing.Client, ac *AuthClient, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{Logging: lg, Billing: bg, Auth: ac, Logger: logger}
}

// Routes registers admin routes on the given chi Router. The caller must wrap
// with identity.Middleware + identity.RequireAdmin.
func (h *Handlers) Routes(r chi.Router) {
	r.Get("/api/v1/admin/request-logs", h.ListRequestLogs)
	r.Get("/api/v1/admin/request-logs/{requestId}", h.GetRequestLog)
	r.Get("/api/v1/admin/request-logs/stats", h.GetRequestLogStats)
	r.Get("/api/v1/admin/plans", h.ListPlans)
	r.Get("/api/v1/admin/user-plans", h.ListUserPlans)
	r.Get("/api/v1/admin/users", h.AdminListUsers)
	r.Get("/api/v1/admin/users/{userId}", h.AdminGetUser)
	r.Patch("/api/v1/admin/users/{userId}", h.AdminUpdateUser)
	r.Get("/api/v1/admin/keys", h.AdminListKeys)
	// Billing admin
	r.Post("/api/v1/admin/plans", h.AdminCreatePlan)
	r.Patch("/api/v1/admin/plans/{planId}", h.AdminUpdatePlan)
	r.Delete("/api/v1/admin/plans/{planId}", h.AdminDeletePlan)
	r.Post("/api/v1/admin/user-plans", h.AdminAssignUserPlan)
	r.Post("/api/v1/admin/user-plans/{userPlanId}/cancel", h.AdminCancelUserPlan)
	r.Get("/api/v1/admin/usage/stats", h.AdminUsageStats)
	r.Get("/api/v1/admin/stats", h.AdminDashboardStats)
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

// ---- Auth admin: users + cross-user keys ----

func (h *Handlers) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		writeErr(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	q := r.URL.Query()
	result, err := h.Auth.ListUsers(r.Context(), bearer, q.Get("search"),
		parsePositiveInt(q.Get("page"), 1), parsePositiveInt(q.Get("pageSize"), 20))
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) AdminGetUser(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		writeErr(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "missing_user_id")
		return
	}
	result, err := h.Auth.GetUser(r.Context(), bearer, userID)
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		writeErr(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		writeErr(w, http.StatusBadRequest, "missing_user_id")
		return
	}
	var body UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	result, err := h.Auth.UpdateUser(r.Context(), bearer, userID, body)
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) AdminListKeys(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		writeErr(w, http.StatusServiceUnavailable, "auth_unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	q := r.URL.Query()
	result, err := h.Auth.ListKeys(r.Context(), bearer,
		parsePositiveInt(q.Get("page"), 1), parsePositiveInt(q.Get("pageSize"), 20))
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleAuthErr maps Auth client errors to HTTP responses.
func (h *Handlers) handleAuthErr(w http.ResponseWriter, err error) {
	if se, ok := err.(*StatusError); ok {
		switch se.StatusCode {
		case http.StatusUnauthorized:
			writeErr(w, http.StatusUnauthorized, "unauthorized")
		case http.StatusForbidden:
			writeErr(w, http.StatusForbidden, "forbidden")
		case http.StatusNotFound:
			writeErr(w, http.StatusNotFound, "not_found")
		case http.StatusBadRequest:
			writeErr(w, http.StatusBadRequest, "bad_request")
		default:
			h.logger().Warn("admin auth error", "status", se.StatusCode)
			writeErr(w, http.StatusServiceUnavailable, "auth_unavailable")
		}
		return
	}
	h.logger().Warn("admin auth call failed", "error", err)
	writeErr(w, http.StatusServiceUnavailable, "auth_unavailable")
}

// bearerFromRequest extracts the Bearer token from the Authorization header.
func bearerFromRequest(r *http.Request) string {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return v[len(prefix):]
	}
	return ""
}

// ---- Billing admin: plans CRUD + user-plans + usage stats ----

func (h *Handlers) AdminCreatePlan(w http.ResponseWriter, r *http.Request) {
	h.proxyBillingAdmin(w, r, http.MethodPost, "/v1/billing/admin/plans")
}

func (h *Handlers) AdminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planId")
	if planID == "" {
		writeErr(w, http.StatusBadRequest, "missing_plan_id")
		return
	}
	h.proxyBillingAdmin(w, r, http.MethodPatch, "/v1/billing/admin/plans/"+planID)
}

func (h *Handlers) AdminDeletePlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planId")
	if planID == "" {
		writeErr(w, http.StatusBadRequest, "missing_plan_id")
		return
	}
	h.proxyBillingAdmin(w, r, http.MethodDelete, "/v1/billing/admin/plans/"+planID)
}

func (h *Handlers) AdminAssignUserPlan(w http.ResponseWriter, r *http.Request) {
	h.proxyBillingAdmin(w, r, http.MethodPost, "/v1/billing/admin/user-plans")
}

func (h *Handlers) AdminCancelUserPlan(w http.ResponseWriter, r *http.Request) {
	upID := chi.URLParam(r, "userPlanId")
	if upID == "" {
		writeErr(w, http.StatusBadRequest, "missing_user_plan_id")
		return
	}
	h.proxyBillingAdmin(w, r, http.MethodPost, "/v1/billing/admin/user-plans/"+upID+"/cancel")
}

func (h *Handlers) AdminUsageStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path := "/v1/billing/admin/usage/stats?days=" + q.Get("days") + "&groupBy=" + q.Get("groupBy")
	h.proxyBillingAdmin(w, r, http.MethodGet, path)
}

// proxyBillingAdmin forwards the request body to the Billing Service admin
// endpoint and relays the response. It is a transparent proxy.
func (h *Handlers) proxyBillingAdmin(w http.ResponseWriter, r *http.Request, method, path string) {
	if h.Billing == nil || !h.Billing.Available() {
		writeErr(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	status, respBody, err := h.Billing.ProxyAdmin(r.Context(), method, path, body)
	if err != nil {
		h.logger().Warn("admin billing proxy failed", "error", err)
		writeErr(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

// ---- Dashboard stats aggregation (#87) ----

func (h *Handlers) AdminDashboardStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := parsePositiveInt(q.Get("days"), 15)

	// Fetch logging dashboard data (today metrics, trend, top users, model usage).
	var dash logging.DashboardStats
	if h.Logging != nil && h.Logging.Available() {
		dd, err := h.Logging.GetDashboard(r.Context(), days)
		if err != nil {
			h.logger().Warn("admin dashboard logging failed", "error", err)
		} else {
			dash = dd
		}
	}

	// Fetch total users from Auth.
	var totalUsers int
	if h.Auth != nil && h.Auth.Available() {
		bearer := bearerFromRequest(r)
		result, err := h.Auth.ListUsers(r.Context(), bearer, "", 1, 1)
		if err == nil {
			totalUsers = result.Total
		}
	}

	// For top users, we need emails from Auth. Fetch user details for each
	// top user's user_id (best-effort; if Auth is unavailable, return user_id).
	topUsers := make([]map[string]any, 0, len(dash.TodayTopUsers))
	if h.Auth != nil && h.Auth.Available() && len(dash.TodayTopUsers) > 0 {
		bearer := bearerFromRequest(r)
		for _, tu := range dash.TodayTopUsers {
			email := tu.UserID
			if u, err := h.Auth.GetUser(r.Context(), bearer, tu.UserID); err == nil {
				email = u.Email
			}
			topUsers = append(topUsers, map[string]any{
				"email":    email,
				"requests": tu.Requests,
				"tokens":   tu.TotalTokens,
			})
		}
	} else {
		for _, tu := range dash.TodayTopUsers {
			topUsers = append(topUsers, map[string]any{
				"email":    tu.UserID,
				"requests": tu.Requests,
				"tokens":   tu.TotalTokens,
			})
		}
	}

	// Compute success rate.
	var successRate float64
	if dash.TodayRequests > 0 {
		successRate = float64(dash.TodaySuccess) / float64(dash.TodayRequests) * 100
	}

	// Map today model usage.
	modelUsage := make([]map[string]any, 0, len(dash.TodayModelUsage))
	for _, m := range dash.TodayModelUsage {
		modelUsage = append(modelUsage, map[string]any{
			"model":    m.Model,
			"requests": m.Requests,
			"tokens":   m.InputTokens + m.OutputTokens,
		})
	}

	// Map trend.
	trend := make([]map[string]any, 0, len(dash.Trend))
	for _, t := range dash.Trend {
		trend = append(trend, map[string]any{
			"date":         t.Date,
			"requests":     t.Requests,
			"success":      t.Success,
			"inputTokens":  t.InputTokens,
			"outputTokens": t.OutputTokens,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"totalUsers":       totalUsers,
		"totalRequests":    dash.TotalRequests,
		"todayRequests":    dash.TodayRequests,
		"todaySuccess":     dash.TodaySuccess,
		"todayActiveUsers": dash.TodayActiveUsers,
		"todayTokens":      dash.TodayTokens,
		"successRate":      successRate,
		"trend":            trend,
		"todayModelUsage":  modelUsage,
		"todayTopUsers":    topUsers,
	})
}
