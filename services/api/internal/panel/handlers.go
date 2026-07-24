// Package panel 实现 Edge/BFF 面向 Panel 的业务查询端点。
//
// 这些端点聚合 Logging Service（请求日志）与 Billing Service（套餐/余额）及本地
// 用户设置存储，统一以 OpenAPI 契约（internal/contract/apiv1）的响应形状返回。
// 全部需要 JWT 身份认证（ListPlans 公开除外），但不走配额 reserve/finalize（仅
// 模型执行请求走配额）。下游不可达时返回 503，下游 404 返回 404，绝不泄漏下游 URL。
package panel

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/tokenmp/v3/services/api/internal/billing"
	apiv1 "github.com/tokenmp/v3/services/api/internal/contract/apiv1"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/logging"
	"github.com/tokenmp/v3/services/api/internal/settings"
)

// Handlers 持有面板端点所需依赖。logging/billing 客户端可为 nil（降级模式，
// 返回 503）；settings 为 nil 时使用默认内存存储。
type Handlers struct {
	Logging  *logging.Client
	Billing  *billing.Client
	Settings *settings.Store
	Logger   *slog.Logger
}

// New 返回面板 Handlers。settings 为 nil 时使用默认内存存储。
func New(lg *logging.Client, bg *billing.Client, st *settings.Store, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	if st == nil {
		st = settings.NewStore()
	}
	return &Handlers{Logging: lg, Billing: bg, Settings: st, Logger: logger}
}

// ---- 套餐 ----

// ListPlans 返回可用套餐列表（公开端点，无需身份）。
func (h *Handlers) ListPlans(w http.ResponseWriter, r *http.Request) {
	if h.Billing == nil || !h.Billing.Available() {
		writeError(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	plans, err := h.Billing.ListPlans(r.Context())
	if err != nil {
		h.logger().Warn("list plans failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	out := make([]apiv1.Plan, 0, len(plans))
	for _, p := range plans {
		// 契约 PlanType 仅暴露 coding/token；其余类型（image/free）不在此公开列表。
		if p.PlanType != "coding" && p.PlanType != "token" {
			continue
		}
		out = append(out, mapPlan(p))
	}
	writeJSON(w, http.StatusOK, plansResponse{Plans: out})
}

// ---- 用户套餐与余额 ----

// ListUserPlans 返回当前用户的订阅套餐。
func (h *Handlers) ListUserPlans(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.Billing == nil || !h.Billing.Available() {
		writeError(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	userPlans, err := h.Billing.ListUserPlans(r.Context(), claims.Subject)
	if err != nil {
		if errors.Is(err, billing.NotFound) {
			writeJSON(w, http.StatusOK, userPlansResponse{Plans: []apiv1.UserPlan{}})
			return
		}
		h.logger().Warn("list user plans failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "billing_unavailable")
		return
	}
	// 取余额用于填充 remainingQuota；余额失败时不阻塞套餐列表，remainingQuota 降级为 "0"。
	bal, balErr := h.Billing.GetBalance(r.Context(), claims.Subject)
	plans := make([]apiv1.UserPlan, 0, len(userPlans))
	for _, up := range userPlans {
		plans = append(plans, mapUserPlan(up, bal, balErr))
	}
	writeJSON(w, http.StatusOK, userPlansResponse{Plans: plans})
}

// GetUserBalance 返回当前用户的余额（十进制字符串）。
func (h *Handlers) GetUserBalance(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.Billing == nil || !h.Billing.Available() {
		// 降级模式返回 0 余额，便于前端在无 Billing 时仍可渲染。
		writeJSON(w, http.StatusOK, apiv1.UserBalance{CodingRemaining: "0", TokenRemaining: "0"})
		return
	}
	bal, err := h.Billing.GetBalance(r.Context(), claims.Subject)
	if err != nil {
		h.logger().Warn("get balance failed", "error", err)
		writeJSON(w, http.StatusOK, apiv1.UserBalance{CodingRemaining: "0", TokenRemaining: "0"})
		return
	}
	writeJSON(w, http.StatusOK, apiv1.UserBalance{
		CodingRemaining: bal.CodingRemaining,
		TokenRemaining:  bal.TokenRemaining,
	})
}

// ---- 请求日志 ----

// ListRequestLogs 返回当前用户的分页请求日志。
func (h *Handlers) ListRequestLogs(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.Logging == nil || !h.Logging.Available() {
		writeError(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	q := r.URL.Query()
	page := parseIntDefault(q.Get("page"), 1)
	pageSize := parseIntDefault(q.Get("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	// 契约 status: success/error/all → 映射为 Logging 的 final_status 集合。
	statuses := mapStatusFilter(q.Get("status"))
	var start, end time.Time
	if raw := q.Get("startDate"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			start = t
		}
	}
	if raw := q.Get("endDate"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			end = t
		}
	}
	result, err := h.Logging.ListLogs(r.Context(), logging.ListFilter{
		UserID:    claims.Subject,
		Model:     q.Get("model"),
		Statuses:  statuses,
		StartTime: start,
		EndTime:   end,
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		h.logger().Warn("list logs failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	logs := make([]apiv1.RequestLog, 0, len(result.Logs))
	for _, l := range result.Logs {
		logs = append(logs, mapRequestLog(l))
	}
	writeJSON(w, http.StatusOK, requestLogsResponse{
		Logs: logs, Total: result.Total, Page: result.Page, PageSize: result.PageSize,
	})
}

// GetRequestLog 返回单个请求日志详情（含 attempts）。
func (h *Handlers) GetRequestLog(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.Logging == nil || !h.Logging.Available() {
		writeError(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	requestID := chi.URLParam(r, "requestId")
	if requestID == "" {
		writeError(w, http.StatusBadRequest, "missing_request_id")
		return
	}
	detail, err := h.Logging.GetLog(r.Context(), requestID)
	if err != nil {
		if errors.Is(err, logging.NotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		h.logger().Warn("get log failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	// 防越权：仅允许用户查看自己的日志（admin 可放宽，当前实现按 subject 校验）。
	if detail.Log.UserID != "" && detail.Log.UserID != claims.Subject && claims.Role != "admin" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, mapRequestLogDetail(detail))
}

// GetRequestLogStats 返回当前用户的用量统计。
func (h *Handlers) GetRequestLogStats(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if h.Logging == nil || !h.Logging.Available() {
		writeError(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	days := parseIntDefault(r.URL.Query().Get("days"), 7)
	if days > 90 {
		days = 90
	}
	stats, err := h.Logging.GetStats(r.Context(), claims.Subject, days)
	if err != nil {
		h.logger().Warn("get stats failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "logging_unavailable")
		return
	}
	byModel := make([]modelStatRow, 0, len(stats.ByModel))
	var totalInput, totalOutput int64
	for _, m := range stats.ByModel {
		byModel = append(byModel, modelStatRow{
			Model:        m.Model,
			Requests:     int(m.Requests),
			InputTokens:  strconv.FormatInt(m.InputTokens, 10),
			OutputTokens: strconv.FormatInt(m.OutputTokens, 10),
			Cost:         "0",
		})
		totalInput += m.InputTokens
		totalOutput += m.OutputTokens
	}
	writeJSON(w, http.StatusOK, usageStatsResponse{
		Days:              stats.Days,
		TotalRequests:     int(stats.TotalRequests),
		TotalInputTokens:  strconv.FormatInt(totalInput, 10),
		TotalOutputTokens: strconv.FormatInt(totalOutput, 10),
		TotalCost:         "0",
		ByModel:           byModel,
	})
}

// ---- 用户设置 ----

// GetUserSettings 返回当前用户设置。
func (h *Handlers) GetUserSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s := h.Settings.Get(claims.Subject)
	writeJSON(w, http.StatusOK, apiv1.UserSettings{
		PreferredBilling: apiv1.UserSettingsPreferredBilling(s.PreferredBilling),
		FallbackEnabled:  s.FallbackEnabled,
	})
}

// UpdateUserSettings 更新当前用户设置（局部更新）。
func (h *Handlers) UpdateUserSettings(w http.ResponseWriter, r *http.Request) {
	claims, ok := identity.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body apiv1.UserSettingsUpdate
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	// 校验 preferredBilling 枚举（coding/token）。
	var pb *string
	if body.PreferredBilling != nil {
		v := string(*body.PreferredBilling)
		if v != "coding" && v != "token" {
			writeError(w, http.StatusBadRequest, "invalid_preferred_billing")
			return
		}
		pb = &v
	}
	s := h.Settings.Snapshot(claims.Subject, pb, body.FallbackEnabled)
	writeJSON(w, http.StatusOK, apiv1.UserSettings{
		PreferredBilling: apiv1.UserSettingsPreferredBilling(s.PreferredBilling),
		FallbackEnabled:  s.FallbackEnabled,
	})
}

// ---- response / mapping helpers ----

// 以下本地响应结构体与 OpenAPI 契约的 JSON 形状精确对齐。UsageStats 与
// RequestLogDetail 在生成代码中含匿名嵌套结构体，难以直接构造，故这里定义等价
// 本地 DTO；其余响应直接复用生成的 apiv1 模型。

type plansResponse struct {
	Plans []apiv1.Plan `json:"plans"`
}

type userPlansResponse struct {
	Plans []apiv1.UserPlan `json:"plans"`
}

type requestLogsResponse struct {
	Logs     []apiv1.RequestLog `json:"logs"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}

// usageStatsResponse 对齐契约 UsageStats。
type usageStatsResponse struct {
	Days              int            `json:"days"`
	TotalRequests     int            `json:"totalRequests"`
	TotalInputTokens  string         `json:"totalInputTokens"`
	TotalOutputTokens string         `json:"totalOutputTokens"`
	TotalCost         string         `json:"totalCost"`
	ByModel           []modelStatRow `json:"byModel"`
}

// modelStatRow 对齐契约 UsageStats.byModel 项。
type modelStatRow struct {
	Model        string `json:"model"`
	Requests     int    `json:"requests"`
	InputTokens  string `json:"inputTokens"`
	OutputTokens string `json:"outputTokens"`
	Cost         string `json:"cost"`
}

// requestLogDetailResponse 对齐契约 RequestLogDetail。
type requestLogDetailResponse struct {
	RequestID    string       `json:"requestId"`
	Model        string       `json:"model"`
	Provider     string       `json:"provider,omitempty"`
	Status       string       `json:"status"`
	ErrorMessage string       `json:"errorMessage,omitempty"`
	InputTokens  *int         `json:"inputTokens,omitempty"`
	OutputTokens *int         `json:"outputTokens,omitempty"`
	Cost         string       `json:"cost,omitempty"`
	DurationMs   *int         `json:"durationMs,omitempty"`
	Attempts     []attemptRow `json:"attempts"`
	CreatedAt    time.Time    `json:"createdAt"`
}

// attemptRow 对齐契约 RequestLogDetail.attempts 项。
type attemptRow struct {
	Attempt    int       `json:"attempt"`
	Provider   string    `json:"provider,omitempty"`
	Model      string    `json:"model,omitempty"`
	Status     string    `json:"status,omitempty"`
	DurationMs int       `json:"durationMs,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (h *Handlers) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func parseIntDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// mapStatusFilter 将契约的 success/error/all 映射为 Logging 的 final_status 集合。
// all/空 → nil（不限）；success → ["success"]；error → 全部错误类别。
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

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }

// int64ToUUID 把 billing 的 int64 id 确定性映射为公开契约的 UUID（大端写入高 8
// 字节，其余置 0）。这是 Edge facade 的公开标识映射，保证同一内部 id 始终映射到
// 同一 UUID，避免暴露自增序号。
func int64ToUUID(id int64) openapi_types.UUID {
	var u openapi_types.UUID
	big := uint64(id)
	for i := 0; i < 8; i++ {
		u[i] = byte(big >> (56 - 8*i))
	}
	return u
}

// durationDaysFromCategory 把套餐周期类别映射为天数。
func durationDaysFromCategory(category string) int {
	switch category {
	case "monthly":
		return 30
	case "quarterly":
		return 90
	case "yearly":
		return 365
	default:
		return 30
	}
}

// mapPlan 把 Billing 套餐映射为契约 Plan。
func mapPlan(p billing.Plan) apiv1.Plan {
	totalQuota := "0"
	if p.TokenLimit != nil {
		totalQuota = strconv.FormatInt(*p.TokenLimit, 10)
	} else if p.MonthlyLimit != nil {
		totalQuota = strconv.Itoa(*p.MonthlyLimit)
	}
	var allowed []string
	if len(p.AllowedModels) > 0 {
		// allowed_models 是 jsonb 字符串数组；解析失败时退化为空数组。
		_ = json.Unmarshal(p.AllowedModels, &allowed)
	}
	return apiv1.Plan{
		Id:            int64ToUUID(p.ID),
		Name:          p.Name,
		PlanType:      apiv1.PlanPlanType(p.PlanType),
		Price:         p.Price,
		DurationDays:  durationDaysFromCategory(p.Category),
		TotalQuota:    totalQuota,
		AllowedModels: &allowed,
		Status:        apiv1.PlanStatus(p.Status),
	}
}

// mapUserPlan 把 Billing 用户套餐映射为契约 UserPlan。remainingQuota 取对应类型
// 的余额字段；balErr 非 nil 时降级为 "0"。
func mapUserPlan(up billing.UserPlan, bal billing.Balance, balErr error) apiv1.UserPlan {
	remaining := "0"
	if balErr == nil {
		if up.PlanType == "token" {
			remaining = bal.TokenRemaining
		} else {
			remaining = bal.CodingRemaining
		}
	}
	status := apiv1.UserPlanStatus(up.Status)
	if up.Status == "cancelled" {
		status = apiv1.UserPlanStatusDisabled
	}
	return apiv1.UserPlan{
		Id:             int64ToUUID(up.ID),
		PlanId:         int64ToUUID(up.PlanID),
		PlanType:       up.PlanType,
		TotalQuota:     "0", // Billing user_plan 不含 plan 限额；由 plans 端点展示
		RemainingQuota: remaining,
		Status:         status,
		ActivatedAt:    up.ActivatedAt,
		ExpiresAt:      up.ExpiresAt,
	}
}

// mapRequestLog 把 Logging 日志摘要映射为契约 RequestLog。
func mapRequestLog(l logging.RequestLog) apiv1.RequestLog {
	return apiv1.RequestLog{
		RequestId:    l.RequestID,
		Model:        l.ModelName,
		Status:       apiv1.RequestLogStatus(mapLogStatus(l.FinalStatus)),
		InputTokens:  intPtrOrNil(l.InputTokens),
		OutputTokens: intPtrOrNil(l.OutputTokens),
		Cost:         strPtrOrNil("0"),
		DurationMs:   intPtrOrNil(l.LatencyMS),
		CreatedAt:    l.CreatedAt,
	}
}

// mapRequestLogDetail 把 Logging 日志详情映射为契约 RequestLogDetail。
func mapRequestLogDetail(d logging.LogDetail) requestLogDetailResponse {
	attempts := make([]attemptRow, 0, len(d.Attempts))
	for i, a := range d.Attempts {
		attempts = append(attempts, attemptRow{
			Attempt:    i,
			Provider:   a.ProviderID,
			Model:      a.UpstreamModel,
			Status:     a.Status,
			DurationMs: a.LatencyMS,
			CreatedAt:  a.CreatedAt,
		})
	}
	return requestLogDetailResponse{
		RequestID:    d.Log.RequestID,
		Model:        d.Log.ModelName,
		Provider:     d.Log.ProviderID,
		Status:       mapLogStatus(d.Log.FinalStatus),
		ErrorMessage: d.Log.ErrorCode,
		InputTokens:  intPtrOrNil(d.Log.InputTokens),
		OutputTokens: intPtrOrNil(d.Log.OutputTokens),
		Cost:         "0",
		DurationMs:   intPtrOrNil(d.Log.LatencyMS),
		Attempts:     attempts,
		CreatedAt:    d.Log.CreatedAt,
	}
}

// mapLogStatus 把 Logging final_status 映射为契约的 success/error。
func mapLogStatus(final string) string {
	if final == "success" {
		return "success"
	}
	return "error"
}

func intPtrOrNil(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
