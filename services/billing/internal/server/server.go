// Package server wires the billing service HTTP handlers.
//
// Billing owns plan reads, the reserve-then-finalize quota lifecycle, and
// usage-ledger reads. It is called by Edge/BFF; executor does not connect to
// this service directly. Errors are stable protocol-native JSON codes and
// never expose SQL, DSNs, or credentials.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tokenmp/v3/services/billing/internal/database"
	"github.com/tokenmp/v3/services/billing/internal/repository"
)

const maxQuotaBodyBytes = 2 << 20 // 2 MiB

// Server holds the shared dependencies for billing HTTP handlers.
type Server struct {
	plans     repository.PlanReader
	userPlans repository.UserPlanReader
	quota     repository.QuotaManager
	ledger    repository.LedgerReader
	pinger    database.Pinger
	logger    *slog.Logger
}

// New returns a billing Server. A nil logger falls back to slog.Default.
func New(plans repository.PlanReader, userPlans repository.UserPlanReader, quota repository.QuotaManager, ledger repository.LedgerReader, pinger database.Pinger, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{plans: plans, userPlans: userPlans, quota: quota, ledger: ledger, pinger: pinger, logger: logger}
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
	r.Post("/v1/billing/quota/reserve", s.handleReserve)
	r.Post("/v1/billing/quota/finalize", s.handleFinalize)
	r.Post("/v1/billing/quota/release", s.handleRelease)
	r.Get("/v1/billing/users/{user_id}/ledger", s.handleListLedger)
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
		writeError(w, http.StatusServiceUnavailable, "not_ready")
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
		writeError(w, http.StatusInternalServerError, "plans_unavailable")
		return
	}
	if plans == nil {
		plans = []repository.Plan{}
	}
	writeJSON(w, http.StatusOK, struct {
		Plans []repository.Plan `json:"plans"`
	}{Plans: plans})
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	plan, err := s.plans.GetPlan(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		s.logger.Warn("plan query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "plan_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleGetUserPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.userPlans.GetActiveUserPlan(r.Context(), chi.URLParam(r, "user_id"))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		s.logger.Warn("user plan query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "plan_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, plan)
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
		writeError(w, http.StatusBadRequest, "missing_field")
		return
	}
	if err := s.quota.Reserve(r.Context(), req.ReservationID, req.UserID, req.RequestID, req.BillingPlan, *req.ReservedRequests, *req.ReservedTokens, req.ExpiresAt); err != nil && !errors.Is(err, repository.ErrConflict) {
		s.logger.Warn("quota reserve failed", "error", err)
		writeError(w, http.StatusInternalServerError, "reserve_failed")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ReservationID string `json:"reservation_id"`
		Status        string `json:"status"`
	}{ReservationID: req.ReservationID, Status: "reserved"})
}

type finalizeRequest struct {
	ReservationID string `json:"reservation_id"`
	FinalRequests *int   `json:"final_requests"`
	FinalTokens   *int64 `json:"final_tokens"`
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	var req finalizeRequest
	if !decodeBoundedJSON(w, r, &req) {
		return
	}
	if req.ReservationID == "" || req.FinalRequests == nil || req.FinalTokens == nil {
		writeError(w, http.StatusBadRequest, "missing_field")
		return
	}
	if err := s.quota.Finalize(r.Context(), req.ReservationID, *req.FinalRequests, *req.FinalTokens); err != nil && !errors.Is(err, repository.ErrConflict) {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		s.logger.Warn("quota finalize failed", "error", err)
		writeError(w, http.StatusInternalServerError, "finalize_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "finalized"})
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
		writeError(w, http.StatusBadRequest, "missing_field")
		return
	}
	if err := s.quota.Release(r.Context(), req.ReservationID); err != nil && !errors.Is(err, repository.ErrConflict) {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		s.logger.Warn("quota release failed", "error", err)
		writeError(w, http.StatusInternalServerError, "release_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "released"})
}

func (s *Server) handleListLedger(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = parsed
	}
	entries, err := s.ledger.ListLedger(r.Context(), chi.URLParam(r, "user_id"), limit)
	if err != nil {
		s.logger.Warn("ledger query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "ledger_unavailable")
		return
	}
	if entries == nil {
		entries = []repository.UsageLedgerEntry{}
	}
	writeJSON(w, http.StatusOK, struct {
		Entries []repository.UsageLedgerEntry `json:"entries"`
	}{Entries: entries})
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxQuotaBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	if err := ensureEOF(dec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		// Response encoding failures cannot be rendered safely after headers
		// have been committed; retain only a safe server-side log record.
		slog.Default().Error("response encode failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
