// Package server wires the logging service HTTP handlers.
//
// The logging service is the single durable write path for request lifecycle
// records pushed by executor/edge. It exposes:
//   - GET  /healthz              : liveness, always 200
//   - GET  /readyz               : readiness, 200 iff DB ping ok, else 503
//   - POST /v1/logs/ingest       : atomic batch ingestion for one request
//   - GET  /v1/logs/{request_id} : a single request's log + attempts + events
//
// All responses carry Cache-Control: no-store. Errors are protocol-native
// JSON with stable codes; no SQL, DSN, driver text or credential is ever
// echoed — the repository/database layers classify failures into sentinels.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tokenmp/v3/services/logging/internal/database"
	"github.com/tokenmp/v3/services/logging/internal/repository"
)

// maxIngestBodyBytes bounds the ingestion request body so a misbehaving
// producer cannot exhaust server memory.
const maxIngestBodyBytes = 2 << 20 // 2 MiB

// Server holds the shared dependencies for the logging service HTTP handlers.
type Server struct {
	writer repository.Writer
	reader repository.Reader
	pinger database.Pinger
	logger *slog.Logger
}

// New returns a Server wired with the given writer, reader, DB readiness
// pinger and logger. logger falls back to slog.Default() when nil. The
// writer is type-asserted to repository.BatchIngestor per request so the
// atomic batch path is used; GormRepository implements both Writer and
// BatchIngestor, so a single repo value can be passed for writer and reader
// in production.
func New(writer repository.Writer, reader repository.Reader, pinger database.Pinger, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{writer: writer, reader: reader, pinger: pinger, logger: logger}
}

// Router returns the configured chi router with cache-control and logging
// middleware applied to every route.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(s.cacheControlMiddleware)
	r.Use(s.logMiddleware)
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Post("/v1/logs/ingest", s.handleIngest)
	r.Get("/v1/logs", s.handleListLogs)
	r.Get("/v1/logs/stats", s.handleStats)
	r.Get("/v1/logs/{request_id}", s.handleGetLog)
	return r
}

// cacheControlMiddleware sets Cache-Control: no-store on every response,
// including errors, so intermediaries never cache log reads or ingestion
// acknowledgements.
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
		s.logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"req_id", middleware.GetReqID(r.Context()),
		)
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.pinger.Ping(r.Context()); err != nil {
		s.logger.Warn("readyz ping failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

// ingestRequest is the wire shape of POST /v1/logs/ingest: a single required
// request log plus its optional attempts and timeline events. The repository
// structs carry safe JSON tags (no plaintext body fields, id is json:"-");
// they are reused directly so the server never redefines the schema shape.
type ingestRequest struct {
	Log      repository.RequestLog `json:"log"`
	Attempts []repository.Attempt  `json:"attempts,omitempty"`
	Events   []repository.Event    `json:"events,omitempty"`
}

// ingestResponse acknowledges a committed batch.
type ingestResponse struct {
	RequestID string `json:"request_id"`
	Accepted  int    `json:"accepted"`
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBodyBytes)
	var req ingestRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if req.Log.RequestID == "" {
		writeError(w, http.StatusBadRequest, "missing_request_id")
		return
	}

	ingestor, ok := s.writer.(repository.BatchIngestor)
	if !ok {
		s.logger.Error("writer does not implement BatchIngestor")
		writeError(w, http.StatusInternalServerError, "ingest_failed")
		return
	}
	if err := ingestor.IngestBatch(r.Context(), repository.Batch{
		Log:      req.Log,
		Attempts: req.Attempts,
		Events:   req.Events,
	}); err != nil {
		s.logger.Warn("ingest failed", "error", err)
		writeError(w, http.StatusInternalServerError, "ingest_failed")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ingestResponse{
		RequestID: req.Log.RequestID,
		Accepted:  1 + len(req.Attempts) + len(req.Events),
	})
}

// logResponse is the wire shape of GET /v1/logs/{request_id}: the request
// log together with its attempts and timeline events.
type logResponse struct {
	Log      repository.RequestLog `json:"log"`
	Attempts []repository.Attempt  `json:"attempts"`
	Events   []repository.Event    `json:"events"`
}

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	requestID := chi.URLParam(r, "request_id")
	log, err := s.reader.GetRequestLog(r.Context(), requestID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		s.logger.Warn("request log query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	attempts, err := s.reader.ListAttempts(r.Context(), requestID)
	if err != nil {
		s.logger.Warn("attempts query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	events, err := s.reader.ListEvents(r.Context(), requestID)
	if err != nil {
		s.logger.Warn("events query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if attempts == nil {
		attempts = []repository.Attempt{}
	}
	if events == nil {
		events = []repository.Event{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(logResponse{Log: log, Attempts: attempts, Events: events})
}

// listLogsResponse is the wire shape of GET /v1/logs: a page of request-level
// summaries with the total match count and pagination echo.
type listLogsResponse struct {
	Logs     []repository.RequestLog `json:"logs"`
	Total    int                     `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
}

// validFinalStatuses is the set of request_logs.final_status CHECK values. A
// status filter value outside this set is dropped rather than failing the
// whole query, so a caller passing an unknown status simply gets no filter.
var validFinalStatuses = map[string]bool{
	"success":         true,
	"client_error":    true,
	"upstream_error":  true,
	"timeout":         true,
	"transport_error": true,
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := repository.ListFilter{
		UserID:   q.Get("user_id"),
		Model:    q.Get("model"),
		Page:     parsePositiveInt(q.Get("page"), 1),
		PageSize: parsePositiveInt(q.Get("page_size"), 20),
	}
	// status is a comma-separated list of final_status enum values. Unknown
	// values are dropped; an empty list means "all statuses".
	for _, st := range splitAndTrim(q.Get("status"), ",") {
		if validFinalStatuses[st] {
			filter.Statuses = append(filter.Statuses, st)
		}
	}
	if raw := q.Get("start_date"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_start_date")
			return
		}
		filter.StartTime = t
	}
	if raw := q.Get("end_date"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_end_date")
			return
		}
		filter.EndTime = t
	}
	logs, total, err := s.reader.ListRequestLogs(r.Context(), filter)
	if err != nil {
		s.logger.Warn("log list query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if logs == nil {
		logs = []repository.RequestLog{}
	}
	writeJSON(w, http.StatusOK, listLogsResponse{
		Logs:     logs,
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	})
}

// statsResponse is the wire shape of GET /v1/logs/stats.
type statsResponse struct {
	Days              int                    `json:"days"`
	TotalRequests     int64                  `json:"total_requests"`
	TotalInputTokens  int64                  `json:"total_input_tokens"`
	TotalOutputTokens int64                  `json:"total_output_tokens"`
	ByModel           []repository.ModelStat `json:"by_model"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := repository.StatsFilter{
		UserID: q.Get("user_id"),
		Days:   parsePositiveInt(q.Get("days"), 7),
	}
	stats, err := s.reader.GetStats(r.Context(), filter)
	if err != nil {
		s.logger.Warn("stats query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if stats.ByModel == nil {
		stats.ByModel = []repository.ModelStat{}
	}
	writeJSON(w, http.StatusOK, statsResponse{
		Days:              filter.Days,
		TotalRequests:     stats.TotalRequests,
		TotalInputTokens:  stats.TotalInputTokens,
		TotalOutputTokens: stats.TotalOutputTokens,
		ByModel:           stats.ByModel,
	})
}

// writeJSON encodes value as a JSON response with Cache-Control: no-store
// already set by the middleware. An encode failure is logged server-side and
// not surfaced to the client.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Default().Error("response encode failed", "error", err)
	}
}

// parsePositiveInt parses a query int, falling back to def when missing or
// non-positive. It never returns an error; invalid values use the default so
// a malformed pagination param degrades gracefully instead of 400ing.
func parsePositiveInt(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	return n
}

// splitAndTrim splits s on sep and trims whitespace from each part, dropping
// empty parts. It returns nil when s is empty.
func splitAndTrim(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// writeError emits a protocol-native JSON error body with a stable code. It
// must never echo SQL, DSN, driver text or credentials — callers only pass
// fixed code strings derived from classified sentinels.
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
