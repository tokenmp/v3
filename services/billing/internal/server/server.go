// Package server wires the billing service HTTP handlers.
//
// Billing owns plan reads, the reserve-then-finalize quota lifecycle, and
// usage-ledger reads. It is called by Edge/BFF; executor does not connect to
// this service directly. Errors are stable protocol-native JSON codes and
// never expose SQL, DSNs, or credentials.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/billing/internal/database"
	"github.com/tokenmp/v3/services/billing/internal/repository"
)

const maxQuotaBodyBytes = 2 << 20 // 2 MiB

// Server holds the shared dependencies for billing HTTP handlers.
type Server struct {
	plans        repository.PlanReader
	userPlans    repository.UserPlanReader
	quota        repository.QuotaManager
	settlement   repository.SettlementManager
	ledger       repository.LedgerReader
	balance      repository.BalanceReader
	usageWindows repository.UsageWindowsReader
	admin        AdminStore
	pinger       database.Pinger
	logger       *slog.Logger
}

// AdminStore provides admin write/query methods on the billing repository.
type AdminStore interface {
	CreatePlan(ctx context.Context, p *repository.Plan) error
	UpdatePlan(ctx context.Context, id int64, fields map[string]any) error
	DeletePlan(ctx context.Context, id int64) error
	ListAllUserPlans(ctx context.Context, limit, offset int) ([]repository.UserPlan, int, error)
	AssignUserPlan(ctx context.Context, up *repository.UserPlan) error
	RenewUserPlan(ctx context.Context, id int64, extendDays int, expiresAt *time.Time) (repository.UserPlan, error)
	UpgradeUserPlan(ctx context.Context, id int64, newPlanID int64, expiresAt *time.Time) (repository.UserPlan, error)
	SwitchUserPlan(ctx context.Context, id int64, newPlanID int64, expiresAt *time.Time) (repository.UserPlan, error)
	CancelUserPlan(ctx context.Context, id int64) error
	GetUsageStats(ctx context.Context, days int, groupBy string) ([]repository.UsageStatRow, error)
	CreateLimitOverride(ctx context.Context, o *repository.UserPlanLimitOverride) error
	ListLimitOverrides(ctx context.Context, userPlanID int64) ([]repository.UserPlanLimitOverride, error)
	RevokeLimitOverride(ctx context.Context, id int64) error
}

// New returns a billing Server. A nil logger falls back to slog.Default.
// balance/usageWindows may be nil only in tests that do not exercise those
// routes; production wiring always supplies the GormRepository. When nil,
// the corresponding endpoint returns 503.
func New(plans repository.PlanReader, userPlans repository.UserPlanReader, quota repository.QuotaManager, settlement repository.SettlementManager, ledger repository.LedgerReader, balance repository.BalanceReader, usageWindows repository.UsageWindowsReader, admin AdminStore, pinger database.Pinger, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{plans: plans, userPlans: userPlans, quota: quota, settlement: settlement, ledger: ledger, balance: balance, usageWindows: usageWindows, admin: admin, pinger: pinger, logger: logger}
}

// Router returns the configured chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(s.cacheControlMiddleware)
	r.Use(s.logMiddleware)
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/v1/billing/plans", s.handleListPlans)
	r.Get("/v1/billing/plans/{id}", s.handleGetPlan)
	r.Get("/v1/billing/users/{user_id}/plan", s.handleGetUserPlan)
	r.Get("/v1/billing/users/{user_id}/plans", s.handleListUserPlans)
	r.Get("/v1/billing/users/{user_id}/balance", s.handleGetBalance)
	r.Get("/v1/billing/users/{user_id}/usage-windows", s.handleGetUsageWindows)
	r.Post("/v1/billing/quota/reserve", s.handleReserve)
	r.Post("/v1/billing/quota/finalize", s.handleFinalize)
	r.Post("/v1/billing/quota/release", s.handleRelease)
	r.Get("/v1/billing/quota/reservations/{reservation_id}", s.handleGetReservation)
	r.Post("/v1/billing/quota/mark-pending", s.handleMarkPending)
	r.Post("/v1/billing/quota/reconcile", s.handleReconcile)
	r.Get("/v1/billing/users/{user_id}/ledger", s.handleListLedger)
	// Admin endpoints
	r.Post("/v1/billing/admin/plans", s.handleAdminCreatePlan)
	r.Patch("/v1/billing/admin/plans/{id}", s.handleAdminUpdatePlan)
	r.Delete("/v1/billing/admin/plans/{id}", s.handleAdminDeletePlan)
	r.Get("/v1/billing/admin/user-plans", s.handleAdminListUserPlans)
	r.Post("/v1/billing/admin/user-plans", s.handleAdminAssignUserPlan)
	r.Post("/v1/billing/admin/user-plans/{id}/renew", s.handleAdminRenewUserPlan)
	r.Post("/v1/billing/admin/user-plans/{id}/switch", s.handleAdminSwitchUserPlan)
	r.Post("/v1/billing/admin/user-plans/{id}/upgrade", s.handleAdminUpgradeUserPlan)
	r.Post("/v1/billing/admin/user-plans/{id}/cancel", s.handleAdminCancelUserPlan)
	r.Get("/v1/billing/admin/usage/stats", s.handleAdminUsageStats)
	r.Post("/v1/billing/admin/user-plans/{id}/limit-overrides", s.handleAdminCreateLimitOverride)
	r.Get("/v1/billing/admin/user-plans/{id}/limit-overrides", s.handleAdminListLimitOverrides)
	r.Post("/v1/billing/admin/limit-overrides/{id}/revoke", s.handleAdminRevokeLimitOverride)
	return r
}

func (s *Server) cacheControlMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.logger.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "bytes", ww.BytesWritten(), "req_id", middleware.GetReqID(r.Context()))
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.pinger.Ping(r.Context()); err != nil {
		s.logger.Warn("readyz ping failed", "error", err)
		httpresp.Error(w, httpresp.CodeNotReady, "not ready")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	plans, err := s.plans.ListPlans(r.Context(), status)
	if err != nil {
		s.logger.Warn("plan list failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "plans unavailable")
		return
	}
	if plans == nil {
		plans = []repository.Plan{}
	}
	httpresp.OK(w, struct {
		Plans []repository.Plan `json:"plans"`
	}{Plans: plans})
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeNotFound, "not found")
		return
	}
	plan, err := s.plans.GetPlan(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("plan query failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "plan unavailable")
		return
	}
	httpresp.OK(w, plan)
}

func (s *Server) handleGetUserPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.userPlans.GetActiveUserPlan(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("user plan query failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "plan unavailable")
		return
	}
	httpresp.OK(w, plan)
}

func (s *Server) handleListUserPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.userPlans.ListActiveUserPlans(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		s.logger.Warn("user plans query failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "plans unavailable")
		return
	}
	if plans == nil {
		plans = []repository.UserPlanDetail{}
	}
	httpresp.OK(w, struct {
		Plans []repository.UserPlanDetail `json:"plans"`
	}{Plans: plans})
}

type reserveRequest struct {
	ReservationID    string     `json:"reservation_id"`
	UserID           string     `json:"user_id"`
	RequestID        string     `json:"request_id"`
	BillingPlan      string     `json:"billing_plan"`
	ReservedRequests *int       `json:"reserved_requests"`
	ReservedTokens   *int64     `json:"reserved_tokens"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) handleReserve(w http.ResponseWriter, r *http.Request) {
	var req reserveRequest
	if !decodeBoundedJSON(w, r, &req) {
		return
	}
	if req.ReservationID == "" || req.UserID == "" || req.RequestID == "" || req.BillingPlan == "" || req.ReservedRequests == nil || req.ReservedTokens == nil {
		httpresp.Error(w, httpresp.CodeMissingField, "missing field")
		return
	}
	err := s.quota.Reserve(r.Context(), req.ReservationID, req.UserID, req.RequestID, req.BillingPlan, *req.ReservedRequests, *req.ReservedTokens, req.ExpiresAt)
	if err != nil && !errors.Is(err, repository.ErrConflict) {
		// Quota exceeded: distinct 429 with a secret-free scope tag. The
		// scope (hour5/weekly/period) is the only extra detail exposed.
		var qe *repository.QuotaExceededError
		if errors.As(err, &qe) {
			s.logger.Info("quota reserve rejected", "scope", qe.Scope, "limit", qe.Limit, "consumed", qe.Consumed, "wanted", qe.Wanted)
			httpresp.ErrorWithStatus(w, http.StatusTooManyRequests, httpresp.CodeConflict, "quota_exceeded: "+string(qe.Scope))
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("quota reserve failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "reserve failed")
		return
	}
	httpresp.OK(w, struct {
		ReservationID string `json:"reservation_id"`
		Status        string `json:"status"`
	}{ReservationID: req.ReservationID, Status: "reserved"})
}

type finalizeRequest struct {
	ReservationID string `json:"reservation_id"`
	FinalRequests *int   `json:"final_requests"`
	FinalTokens   *int64 `json:"final_tokens"`
	UsageKnown    *bool  `json:"usage_known"`
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	var req finalizeRequest
	if !decodeBoundedJSON(w, r, &req) {
		return
	}
	if req.ReservationID == "" || req.FinalRequests == nil || req.FinalTokens == nil || req.UsageKnown == nil {
		httpresp.Error(w, httpresp.CodeMissingField, "missing field")
		return
	}
	if err := s.quota.Finalize(r.Context(), req.ReservationID, *req.FinalRequests, *req.FinalTokens, *req.UsageKnown); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			httpresp.ErrorWithStatus(w, http.StatusConflict, httpresp.CodeConflict, "conflict")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("quota finalize failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "finalize failed")
		return
	}
	httpresp.OK(w, map[string]string{"status": "finalized"})
}

type releaseRequest struct {
	ReservationID string `json:"reservation_id"`
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req releaseRequest
	if !decodeBoundedJSON(w, r, &req) {
		return
	}
	if req.ReservationID == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "missing field")
		return
	}
	if err := s.quota.Release(r.Context(), req.ReservationID); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			httpresp.ErrorWithStatus(w, http.StatusConflict, httpresp.CodeConflict, "conflict")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("quota release failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "release failed")
		return
	}
	httpresp.OK(w, map[string]string{"status": "released"})
}

func (s *Server) handleGetReservation(w http.ResponseWriter, r *http.Request) {
	if s.settlement == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "settlement unavailable")
		return
	}
	status, err := s.settlement.GetReservation(r.Context(), chi.URLParam(r, "reservation_id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("reservation status query failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "status unavailable")
		return
	}
	httpresp.OK(w, status)
}

type markPendingRequest struct {
	ReservationID string `json:"reservation_id"`
}

func (s *Server) handleMarkPending(w http.ResponseWriter, r *http.Request) {
	if s.settlement == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "settlement unavailable")
		return
	}
	var req markPendingRequest
	if !decodeBoundedJSON(w, r, &req) {
		return
	}
	if req.ReservationID == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "missing field")
		return
	}
	if err := s.settlement.MarkPending(r.Context(), req.ReservationID); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			httpresp.ErrorWithStatus(w, http.StatusConflict, httpresp.CodeConflict, "conflict")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("mark pending failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "mark pending failed")
		return
	}
	httpresp.OK(w, map[string]string{"status": "pending_reconciliation"})
}

type reconcileRequest struct {
	ReservationID string `json:"reservation_id"`
	FinalRequests *int   `json:"final_requests"`
	FinalTokens   *int64 `json:"final_tokens"`
	UsageKnown    *bool  `json:"usage_known"`
}

func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if s.settlement == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "settlement unavailable")
		return
	}
	var req reconcileRequest
	if !decodeBoundedJSON(w, r, &req) {
		return
	}
	if req.ReservationID == "" || req.FinalRequests == nil || req.FinalTokens == nil || req.UsageKnown == nil {
		httpresp.Error(w, httpresp.CodeMissingField, "missing field")
		return
	}
	if err := s.settlement.Reconcile(r.Context(), req.ReservationID, *req.FinalRequests, *req.FinalTokens, *req.UsageKnown); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			httpresp.ErrorWithStatus(w, http.StatusConflict, httpresp.CodeConflict, "conflict")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		s.logger.Warn("reconcile failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "reconcile failed")
		return
	}
	status := "finalized"
	if !*req.UsageKnown {
		status = "released"
	}
	httpresp.OK(w, map[string]string{"status": status})
}

func (s *Server) handleListLedger(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			httpresp.Error(w, httpresp.CodeBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	entries, err := s.ledger.ListLedger(r.Context(), chi.URLParam(r, "user_id"), limit)
	if err != nil {
		s.logger.Warn("ledger query failed", "error", err)
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "ledger unavailable")
		return
	}
	if entries == nil {
		entries = []repository.UsageLedgerEntry{}
	}
	httpresp.OK(w, struct {
		Entries []repository.UsageLedgerEntry `json:"entries"`
	}{Entries: entries})
}

// balanceResponse is the wire shape of GET /v1/billing/users/{user_id}/balance.
// Quota amounts are decimal strings so downstream consumers preserve full
// precision without floating-point loss.
type balanceResponse struct {
	CodingRemaining string `json:"coding_remaining"`
	TokenRemaining  string `json:"token_remaining"`
}

func (s *Server) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	if s.balance == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "balance unavailable")
		return
	}
	bal, err := s.balance.GetBalance(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		s.logger.Warn("balance query failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "balance unavailable")
		return
	}
	httpresp.OK(w, balanceResponse{
		CodingRemaining: strconv.FormatInt(bal.CodingRemaining, 10),
		TokenRemaining:  strconv.FormatInt(bal.TokenRemaining, 10),
	})
}

func (s *Server) handleGetUsageWindows(w http.ResponseWriter, r *http.Request) {
	if s.usageWindows == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "usage windows unavailable")
		return
	}
	windows, err := s.usageWindows.GetUsageWindows(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		s.logger.Warn("usage windows query failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "usage windows unavailable")
		return
	}
	if windows == nil {
		windows = []repository.UsageWindow{}
	}
	httpresp.OK(w, struct {
		Windows []repository.UsageWindow `json:"windows"`
	}{Windows: windows})
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid JSON")
		return false
	}
	if err := ensureEOF(dec); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid JSON")
		return false
	}
	return true
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}
