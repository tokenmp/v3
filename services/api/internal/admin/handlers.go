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
//   - GET /api/v1/admin/users             → Auth (list)
//   - GET /api/v1/admin/users/{id}        → Auth + Billing + Logging (aggregated detail)
//   - GET /api/v1/admin/keys              → Auth (cross-user)
//
// Config endpoints proxy to Config Service.
package admin

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/packages/go/httpresp"
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
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "logging unavailable")
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
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "logging unavailable")
		return
	}
	httpresp.OK(w, map[string]any{
		"logs":     h.enrichLogEmails(r, result.Logs),
		"total":    result.Total,
		"page":     result.Page,
		"pageSize": result.PageSize,
	})
}

func (h *Handlers) GetRequestLog(w http.ResponseWriter, r *http.Request) {
	if h.Logging == nil || !h.Logging.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "logging unavailable")
		return
	}
	id := chi.URLParam(r, "requestId")
	if id == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "missing request id")
		return
	}
	detail, err := h.Logging.GetLog(r.Context(), id)
	if err != nil {
		httpresp.Error(w, httpresp.CodeNotFound, "not found")
		return
	}
	detail.Log = h.enrichLogEmails(r, []logging.RequestLog{detail.Log})[0]
	httpresp.OK(w, detail)
}

func (h *Handlers) GetRequestLogStats(w http.ResponseWriter, r *http.Request) {
	if h.Logging == nil || !h.Logging.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "logging unavailable")
		return
	}
	days := parsePositiveInt(r.URL.Query().Get("days"), 7)
	if days > 90 {
		days = 90
	}
	stats, err := h.Logging.GetStats(r.Context(), "", days)
	if err != nil {
		h.logger().Warn("admin stats failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "logging unavailable")
		return
	}
	httpresp.OK(w, stats)
}

// ---- Plans (cross-user) ----

// enrichLogEmails best-effort resolves user_id → user_email for a slice of
// request logs by calling Auth's admin GetUser per distinct user_id. Failures
// leave user_email empty so the UI falls back to user_id. The lookup is
// best-effort and never fails the request.
func (h *Handlers) enrichLogEmails(r *http.Request, logs []logging.RequestLog) []logging.RequestLog {
	if h.Auth == nil || !h.Auth.Available() || len(logs) == 0 {
		return logs
	}
	bearer := bearerFromRequest(r)
	emails := make(map[string]string, len(logs))
	for i := range logs {
		uid := logs[i].UserID
		if uid == "" {
			continue
		}
		if _, ok := emails[uid]; ok {
			continue
		}
		if u, err := h.Auth.GetUser(r.Context(), bearer, uid); err == nil {
			emails[uid] = u.Email
		} else {
			emails[uid] = ""
		}
	}
	for i := range logs {
		if email, ok := emails[logs[i].UserID]; ok && email != "" {
			logs[i].UserEmail = email
		}
	}
	return logs
}

func (h *Handlers) ListPlans(w http.ResponseWriter, r *http.Request) {
	if h.Billing == nil || !h.Billing.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "billing unavailable")
		return
	}
	plans, err := h.Billing.ListPlans(r.Context())
	if err != nil {
		h.logger().Warn("admin list plans failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "billing unavailable")
		return
	}
	httpresp.OK(w, map[string]any{"plans": plans})
}

func (h *Handlers) ListUserPlans(w http.ResponseWriter, r *http.Request) {
	if h.Billing == nil || !h.Billing.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "billing unavailable")
		return
	}
	// Billing has no cross-user list endpoint yet; return empty for now.
	// This will be replaced when Billing adds an admin list endpoint.
	userPlans, err := h.Billing.ListAllUserPlans(r.Context())
	if err != nil {
		// Graceful degradation: return empty list instead of 503 when the
		// cross-user endpoint is not available.
		httpresp.OK(w, map[string]any{"plans": []any{}})
		return
	}
	httpresp.OK(w, map[string]any{"plans": h.enrichUserPlans(r, userPlans)})
}

// enrichUserPlans best-effort resolves user_id → user_email and plan_id →
// plan_name for a slice of user plans. Failures leave the field empty so the
// UI falls back to the raw id. The lookup is best-effort and never fails the
// request.
func (h *Handlers) enrichUserPlans(r *http.Request, plans []billing.UserPlan) []map[string]any {
	if len(plans) == 0 {
		return []map[string]any{}
	}
	// plan_id → plan_name from a single ListPlans call.
	planNames := make(map[int64]string, 8)
	if h.Billing != nil && h.Billing.Available() {
		if allPlans, err := h.Billing.ListPlans(r.Context()); err == nil {
			for _, p := range allPlans {
				planNames[p.ID] = p.Name
			}
		}
	}
	// user_id → user_email from Auth admin GetUser (best-effort).
	emails := make(map[string]string, len(plans))
	if h.Auth != nil && h.Auth.Available() {
		bearer := bearerFromRequest(r)
		for _, up := range plans {
			if up.UserID == "" {
				continue
			}
			if _, ok := emails[up.UserID]; ok {
				continue
			}
			if u, err := h.Auth.GetUser(r.Context(), bearer, up.UserID); err == nil {
				emails[up.UserID] = u.Email
			} else {
				emails[up.UserID] = ""
			}
		}
	}
	out := make([]map[string]any, 0, len(plans))
	for _, up := range plans {
		entry := map[string]any{
			"id":           up.ID,
			"user_id":      up.UserID,
			"plan_id":      up.PlanID,
			"plan_type":    up.PlanType,
			"status":       up.Status,
			"activated_at": up.ActivatedAt,
		}
		if up.ExpiresAt != nil {
			entry["expires_at"] = *up.ExpiresAt
		}
		if email, ok := emails[up.UserID]; ok && email != "" {
			entry["user_email"] = email
		}
		if name, ok := planNames[up.PlanID]; ok && name != "" {
			entry["plan_name"] = name
		}
		out = append(out, entry)
	}
	return out
}

// ---- helpers ----

func (h *Handlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
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
	case "processing":
		return []string{"processing"}
	case "cancelled", "canceled":
		return []string{"client_cancelled"}
	default:
		return nil
	}
}

// ---- Auth admin: users + cross-user keys ----

func (h *Handlers) AdminListUsers(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "auth unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	q := r.URL.Query()
	result, err := h.Auth.ListUsers(r.Context(), bearer, q.Get("search"), q.Get("status"), q.Get("role"),
		parsePositiveInt(q.Get("page"), 1), parsePositiveInt(q.Get("pageSize"), 20))
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}
	httpresp.OK(w, result)
}

func (h *Handlers) AdminGetUser(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "auth unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "missing user id")
		return
	}
	h.adminGetUserDetail(w, r, bearer, userID)
}

func (h *Handlers) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "auth unavailable")
		return
	}
	bearer := bearerFromRequest(r)
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "missing user id")
		return
	}
	var body UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	result, err := h.Auth.UpdateUser(r.Context(), bearer, userID, body)
	if err != nil {
		h.handleAuthErr(w, err)
		return
	}
	httpresp.OK(w, result)
}

func (h *Handlers) AdminListKeys(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Available() {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "auth unavailable")
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
	// Best-effort resolve user_id → user_email so the admin keys table can
	// show the owner without a separate column fetch.
	if h.Auth != nil && h.Auth.Available() && len(result.Keys) > 0 {
		emails := make(map[string]string, len(result.Keys))
		for _, k := range result.Keys {
			if k.UserID == "" {
				continue
			}
			if _, ok := emails[k.UserID]; ok {
				continue
			}
			if u, err := h.Auth.GetUser(r.Context(), bearer, k.UserID); err == nil {
				emails[k.UserID] = u.Email
			}
		}
		for i := range result.Keys {
			if email, ok := emails[result.Keys[i].UserID]; ok {
				result.Keys[i].UserEmail = email
			}
		}
	}
	httpresp.OK(w, result)
}

// handleAuthErr maps Auth client errors to HTTP responses.
func (h *Handlers) handleAuthErr(w http.ResponseWriter, err error) {
	if se, ok := err.(*StatusError); ok {
		switch se.StatusCode {
		case http.StatusUnauthorized:
			httpresp.Error(w, httpresp.CodeUnauthorized, "unauthorized")
		case http.StatusForbidden:
			httpresp.Error(w, httpresp.CodeForbidden, "forbidden")
		case http.StatusNotFound:
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
		case http.StatusBadRequest:
			httpresp.Error(w, httpresp.CodeBadRequest, "bad request")
		default:
			h.logger().Warn("admin auth error", "status", se.StatusCode)
			httpresp.Error(w, httpresp.CodeServiceUnavailable, "auth unavailable")
		}
		return
	}
	h.logger().Warn("admin auth call failed", "error", err)
	httpresp.Error(w, httpresp.CodeServiceUnavailable, "auth unavailable")
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
		httpresp.Error(w, httpresp.CodeMissingField, "missing plan id")
		return
	}
	h.proxyBillingAdmin(w, r, http.MethodPatch, "/v1/billing/admin/plans/"+planID)
}

func (h *Handlers) AdminDeletePlan(w http.ResponseWriter, r *http.Request) {
	planID := chi.URLParam(r, "planId")
	if planID == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "missing plan id")
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
		httpresp.Error(w, httpresp.CodeMissingField, "missing user plan id")
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
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "billing unavailable")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	status, respBody, err := h.Billing.ProxyAdmin(r.Context(), method, path, body)
	if err != nil {
		h.logger().Warn("admin billing proxy failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "billing unavailable")
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
		result, err := h.Auth.ListUsers(r.Context(), bearer, "", "", "", 1, 1)
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
				"email":        email,
				"requests":     tu.Requests,
				"inputTokens":  tu.InputTokens,
				"outputTokens": tu.OutputTokens,
				"tokens":       tu.TotalTokens,
			})
		}
	} else {
		for _, tu := range dash.TodayTopUsers {
			topUsers = append(topUsers, map[string]any{
				"email":        tu.UserID,
				"requests":     tu.Requests,
				"inputTokens":  tu.InputTokens,
				"outputTokens": tu.OutputTokens,
				"tokens":       tu.TotalTokens,
			})
		}
	}

	// Compute success rate (rounded to 1 decimal place).
	var successRate float64
	if dash.TodayRequests > 0 {
		successRate = float64(dash.TodaySuccess) / float64(dash.TodayRequests) * 100
		successRate = float64(int(successRate*10)) / 10 // round to 1 decimal
	}

	// Map today model usage.
	modelUsage := make([]map[string]any, 0, len(dash.TodayModelUsage))
	for _, m := range dash.TodayModelUsage {
		modelUsage = append(modelUsage, map[string]any{
			"model":    m.Model,
			"requests": m.Requests,
			"success":  m.Success,
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

	httpresp.OK(w, map[string]any{
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
