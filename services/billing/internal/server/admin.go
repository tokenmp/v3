package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
	up := &repository.UserPlan{
		UserID: body.UserID,
		PlanID: body.PlanID,
		Status: "active",
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

// ---- helpers ----

func parsePositiveInt(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return def
	}
	return n
}
