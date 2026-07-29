package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/billing/internal/repository"
)

// ---- Plan CRUD ----

type createPlanBody struct {
	Name          string          `json:"name"`
	PlanType      string          `json:"plan_type"`
	Price         float64         `json:"price"`
	Category      string          `json:"category"`
	MonthlyLimit  *int            `json:"monthly_limit,omitempty"`
	TokenLimit    *int64          `json:"token_limit,omitempty"`
	AllowedModels json.RawMessage `json:"allowed_models,omitempty"`
}

func (s *Server) handleAdminCreatePlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	var body createPlanBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	if body.Name == "" || body.PlanType == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "name and plan type required")
		return
	}
	plan := &repository.Plan{
		Name:          body.Name,
		PlanType:      body.PlanType,
		Price:         body.Price,
		Category:      body.Category,
		MonthlyLimit:  body.MonthlyLimit,
		TokenLimit:    body.TokenLimit,
		AllowedModels: body.AllowedModels,
		Status:        "active",
	}
	if err := s.admin.CreatePlan(r.Context(), plan); err != nil {
		s.logger.Warn("admin create plan failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.Created(w, plan)
}

func (s *Server) handleAdminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid plan id")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	// Only allow mutable fields.
	allowed := map[string]bool{"name": true, "price": true, "category": true, "monthly_limit": true, "token_limit": true, "allowed_models": true, "status": true}
	fields := make(map[string]any)
	for k, v := range body {
		if allowed[k] {
			fields[k] = v
		}
	}
	if err := s.admin.UpdatePlan(r.Context(), id, fields); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "plan not found")
			return
		}
		s.logger.Warn("admin update plan failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": id})
}

func (s *Server) handleAdminDeletePlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid plan id")
		return
	}
	if err := s.admin.DeletePlan(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "plan not found")
			return
		}
		s.logger.Warn("admin delete plan failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "status": "deleted"})
}

// ---- User Plans (cross-user) ----

func (s *Server) handleAdminListUserPlans(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	q := r.URL.Query()
	page := parsePositiveInt(q.Get("page"), 1)
	pageSize := parsePositiveInt(q.Get("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	ups, total, err := s.admin.ListAllUserPlans(r.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		s.logger.Warn("admin list user plans failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{
		"userPlans": ups,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
	})
}

type assignUserPlanBody struct {
	UserID    string  `json:"user_id"`
	PlanID    int64   `json:"plan_id"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (s *Server) handleAdminAssignUserPlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	var body assignUserPlanBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	if body.UserID == "" || body.PlanID <= 0 {
		httpresp.Error(w, httpresp.CodeMissingField, "user id and plan id required")
		return
	}
	expiresAt, ok := parseOptionalRFC3339(w, body.ExpiresAt)
	if !ok {
		return
	}
	up := &repository.UserPlan{
		UserID:    body.UserID,
		PlanID:    body.PlanID,
		Status:    "active",
		ExpiresAt: expiresAt,
	}
	if err := s.admin.AssignUserPlan(r.Context(), up); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "plan not found")
			return
		}
		s.logger.Warn("admin assign user plan failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.Created(w, up)
}

type renewUserPlanBody struct {
	ExtendDays *int    `json:"extend_days,omitempty"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
}

type upgradeUserPlanBody struct {
	PlanID    int64   `json:"plan_id"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

func (s *Server) handleAdminRenewUserPlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid user plan id")
		return
	}
	var body renewUserPlanBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	expiresAt, ok := parseOptionalRFC3339(w, body.ExpiresAt)
	if !ok {
		return
	}
	extendDays := 0
	if body.ExtendDays != nil {
		extendDays = *body.ExtendDays
	}
	if expiresAt == nil && extendDays <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "extend_days or expires_at required")
		return
	}
	up, err := s.admin.RenewUserPlan(r.Context(), id, extendDays, expiresAt)
	if err != nil {
		s.handleUserPlanAdminErr(w, err, "admin renew user plan failed")
		return
	}
	httpresp.OK(w, up)
}

func (s *Server) handleAdminUpgradeUserPlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid user plan id")
		return
	}
	var body upgradeUserPlanBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	if body.PlanID <= 0 {
		httpresp.Error(w, httpresp.CodeMissingField, "plan id required")
		return
	}
	expiresAt, ok := parseOptionalRFC3339(w, body.ExpiresAt)
	if !ok {
		return
	}
	up, err := s.admin.UpgradeUserPlan(r.Context(), id, body.PlanID, expiresAt)
	if err != nil {
		s.handleUserPlanAdminErr(w, err, "admin upgrade user plan failed")
		return
	}
	httpresp.Created(w, up)
}

func parseOptionalRFC3339(w http.ResponseWriter, raw *string) (*time.Time, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid expires_at")
		return nil, false
	}
	t = t.UTC()
	return &t, true
}

func (s *Server) handleUserPlanAdminErr(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, repository.ErrNotFound) {
		httpresp.Error(w, httpresp.CodeNotFound, "user plan or plan not found")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid renewal")
		return
	}
	s.logger.Warn(msg, "error", err)
	httpresp.Error(w, httpresp.CodeInternalError, "internal error")
}

func (s *Server) handleAdminCancelUserPlan(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid user plan id")
		return
	}
	if err := s.admin.CancelUserPlan(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "user plan not found")
			return
		}
		s.logger.Warn("admin cancel user plan failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "status": "cancelled"})
}

// ---- Usage Stats ----

func (s *Server) handleAdminUsageStats(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	q := r.URL.Query()
	days := parsePositiveInt(q.Get("days"), 7)
	groupBy := q.Get("groupBy")
	rows, err := s.admin.GetUsageStats(r.Context(), days, groupBy)
	if err != nil {
		s.logger.Warn("admin usage stats failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{
		"days":    days,
		"groupBy": groupBy,
		"rows":    rows,
	})
}

// ---- Limit Overrides ----

type createLimitOverrideBody struct {
	Kind           string  `json:"kind"`
	Scope          string  `json:"scope"`
	EffectiveFrom  *string `json:"effective_from,omitempty"`
	EffectiveUntil *string `json:"effective_until,omitempty"`
	BonusRequests  *int    `json:"bonus_requests,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	CreatedBy      string  `json:"created_by,omitempty"`
}

func (s *Server) handleAdminCreateLimitOverride(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	userPlanID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || userPlanID <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid user plan id")
		return
	}
	var body createLimitOverrideBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	if body.Kind != "reset" && body.Kind != "bonus" {
		httpresp.Error(w, httpresp.CodeMissingField, "kind must be reset or bonus")
		return
	}
	if body.Scope != "hour5" && body.Scope != "weekly" && body.Scope != "period" {
		httpresp.Error(w, httpresp.CodeMissingField, "scope must be hour5, weekly or period")
		return
	}
	if body.Kind == "bonus" && (body.BonusRequests == nil || *body.BonusRequests < 0) {
		httpresp.Error(w, httpresp.CodeMissingField, "bonus_requests must be a non-negative int for bonus kind")
		return
	}
	o := &repository.UserPlanLimitOverride{
		UserPlanID:    userPlanID,
		Kind:          body.Kind,
		Scope:         body.Scope,
		BonusRequests: body.BonusRequests,
		Reason:        body.Reason,
		CreatedBy:     body.CreatedBy,
	}
	now := time.Now().UTC()
	if body.EffectiveFrom != nil && *body.EffectiveFrom != "" {
		t, err := time.Parse(time.RFC3339Nano, *body.EffectiveFrom)
		if err != nil {
			httpresp.Error(w, httpresp.CodeBadRequest, "invalid effective_from")
			return
		}
		o.EffectiveFrom = t.UTC()
	} else {
		o.EffectiveFrom = now
	}
	if body.EffectiveUntil != nil && *body.EffectiveUntil != "" {
		t, err := time.Parse(time.RFC3339Nano, *body.EffectiveUntil)
		if err != nil {
			httpresp.Error(w, httpresp.CodeBadRequest, "invalid effective_until")
			return
		}
		o.EffectiveUntil = ptrTime(t.UTC())
	}
	if err := s.admin.CreateLimitOverride(r.Context(), o); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "user plan not found")
			return
		}
		s.logger.Warn("admin create limit override failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.Created(w, o)
}

func (s *Server) handleAdminListLimitOverrides(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	userPlanID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || userPlanID <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid user plan id")
		return
	}
	rows, err := s.admin.ListLimitOverrides(r.Context(), userPlanID)
	if err != nil {
		s.logger.Warn("admin list limit overrides failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"overrides": rows})
}

func (s *Server) handleAdminRevokeLimitOverride(w http.ResponseWriter, r *http.Request) {
	if s.admin == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid override id")
		return
	}
	if err := s.admin.RevokeLimitOverride(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "override not found")
			return
		}
		s.logger.Warn("admin revoke limit override failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "status": "revoked"})
}

// ---- helpers ----

func ptrTime(t time.Time) *time.Time { return &t }

func parsePositiveInt(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}
