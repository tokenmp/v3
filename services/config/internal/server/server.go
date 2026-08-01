// Package server wires the config service HTTP handlers.
//
// The skeleton exposes three endpoints:
//   - GET /healthz : liveness, always 200
//   - GET /readyz  : readiness, 200 if DB ping succeeds, 503 otherwise
//   - GET /v1/config/snapshots/latest : latest published config snapshot JSON
//
// The snapshot endpoint serves the raw ConfigSnapshot JSON (the config DB is
// the source of truth); compilation into a runtime snapshot happens
// executor-side via snapshot.Compile, so the config service never depends on
// the executor internal package.
//
// Errors are protocol-native JSON; no DSN, SQL, or credential is ever echoed.
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/config/internal/adminauth"
	"github.com/tokenmp/v3/services/config/internal/database"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

// Server holds the shared dependencies for the config service HTTP handlers.
type Server struct {
	reader      repository.Reader
	writer      repository.Writer
	adminReader repository.AdminReader
	adminWriter repository.AdminWriter
	pinger      database.Pinger
	logger      *slog.Logger
	adminAuth   *adminauth.Middleware
}

// New returns a Server wired with the given reader (snapshot source), writer
// (draft/publish lifecycle) and pinger (DB readiness). logger must be non-nil.
// adminReader/adminWriter can be the same *GormRepository. adminAuth may be
// nil (write/admin routes then fail 503 via the middleware's fail-closed
// path unless dev no-auth is enabled).
func New(reader repository.Reader, writer repository.Writer, pinger database.Pinger, logger *slog.Logger) *Server {
	return NewWithAdminAuth(reader, writer, pinger, logger, nil)
}

// NewWithAdminAuth is like New but wires the service-to-service admin/write
// authorization middleware. When mw is non-nil and configured, write/admin
// routes require the shared secret; reads remain anonymous.
func NewWithAdminAuth(reader repository.Reader, writer repository.Writer, pinger database.Pinger, logger *slog.Logger, mw *adminauth.Middleware) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{reader: reader, writer: writer, pinger: pinger, logger: logger, adminAuth: mw}
	if ar, ok := writer.(repository.AdminReader); ok {
		s.adminReader = ar
	}
	if aw, ok := writer.(repository.AdminWriter); ok {
		s.adminWriter = aw
	}
	if s.adminReader == nil {
		if ar, ok := reader.(repository.AdminReader); ok {
			s.adminReader = ar
		}
	}
	if s.adminWriter == nil {
		if aw, ok := reader.(repository.AdminWriter); ok {
			s.adminWriter = aw
		}
	}
	return s
}

// Router returns the configured chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(s.logMiddleware)
	r.Get("/healthz", s.handleHealthz)
	r.Get("/readyz", s.handleReadyz)
	r.Get("/v1/config/snapshots/latest", s.handleLatestSnapshot)
	// Write path (draft/publish/archive/revert/audit) — protected by
	// service-to-service admin auth. Reads (/snapshots/latest, models catalog)
	// stay anonymous.
	s.registerWriteRoutes(r)
	// Admin CRUD for config tables (providers/models/routes/...) — admin auth.
	s.registerAdminRoutes(r)
	// Models catalog (model IDs for plan allowedModels selector) — anonymous read.
	r.Get("/v1/config/models/catalog", s.handleModelsCatalog)
	return r
}

// registerWriteRoutes wires the draft/publish/archive/revert/audit routes,
// each wrapped with the admin-auth middleware (fail-closed 401 when unset in
// production; dev no-auth passes through). The contract path is /revert
// (see packages/contracts/openapi/config/v1.yaml); the internal repository
// method retains the name RollbackAsNew.
func (s *Server) registerWriteRoutes(r chi.Router) {
	mw := s.adminAuthWrap()
	r.With(mw).Post("/v1/config/drafts", s.handleCreateDraft)
	r.With(mw).Get("/v1/config/drafts/{id}", s.handleGetDraft)
	r.With(mw).Patch("/v1/config/drafts/{id}", s.handleUpdateDraft)
	r.With(mw).Post("/v1/config/revisions/{id}/publish", s.handlePublishRevision)
	r.With(mw).Post("/v1/config/revisions/{id}/archive", s.handleArchiveRevision)
	r.With(mw).Post("/v1/config/revisions/{id}/revert", s.handleRevertRevision)
	r.With(mw).Get("/v1/config/revisions", s.handleListRevisions)
	r.With(mw).Get("/v1/config/audit", s.handleListAudit)
}

// adminAuthWrap returns a chi-compatible middleware. When adminAuth is nil
// (not configured) and not in dev no-auth mode, write/admin routes return 503
// fail-closed rather than exposing a default-open half-secure path.
func (s *Server) adminAuthWrap() func(http.Handler) http.Handler {
	if s.adminAuth != nil {
		return s.adminAuth.Wrap
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		})
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.pinger.Ping(r.Context()); err != nil {
		s.logger.Warn("readyz ping failed", "error", err)
		httpresp.Error(w, httpresp.CodeNotReady, "not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func (s *Server) handleLatestSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.reader.LatestPublished(r.Context())
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			httpresp.Error(w, httpresp.CodeNotFound, "no published snapshot")
		default:
			s.logger.Error("snapshot query failed", "error", err)
			httpresp.Error(w, httpresp.CodeInternalError, "snapshot unavailable")
		}
		return
	}
	// This endpoint serves the RAW ConfigSnapshot JSON consumed by the
	// executor's config source. Per the OpenAPI contract
	// (getConfigSnapshotLatest) the response body IS the raw snapshot JSON —
	// it must NOT be wrapped in the {code,data,message} envelope nor in a
	// revision/sha256 metadata wrapper. The revision identifier and content
	// digest are carried in the X-Config-Revision and X-Config-SHA256 headers
	// for integrity checks (the body itself is opaque to this service).
	//
	// Validate the stored bytes are strict, single-value JSON before serving:
	// a stored wrapper, an empty/null snapshot, or trailing data would be
	// served verbatim to executors and fail their strict decoder. Treat any
	// such row as an internal error rather than emitting a malformed body.
	// The bytes are served verbatim (not re-encoded or trimmed) so the body
	// matches the X-Config-SHA256 digest computed over the stored bytes.
	body := snap.SnapshotJSON
	if len(bytes.TrimSpace(body)) == 0 {
		s.logger.Error("published snapshot body is empty")
		httpresp.Error(w, httpresp.CodeInternalError, "snapshot unavailable")
		return
	}
	// null is a valid single JSON value but not a usable snapshot object.
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		s.logger.Error("published snapshot body is null")
		httpresp.Error(w, httpresp.CodeInternalError, "snapshot unavailable")
		return
	}
	if !json.Valid(body) || !singleJSONValue(body) {
		s.logger.Error("published snapshot body is not strict single-value JSON")
		httpresp.Error(w, httpresp.CodeInternalError, "snapshot unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Config-Revision", snap.Revision)
	w.Header().Set("X-Config-SHA256", snap.SHA256)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(body); err != nil {
		s.logger.Error("snapshot write failed", "error", err)
	}
}

// singleJSONValue reports whether body decodes to exactly one top-level JSON
// value with no trailing non-whitespace data. This rejects a stored document
// that concatenates two JSON values or carries trailing garbage, which the
// executor's strict decoder would reject. json.Valid alone permits a single
// value followed by trailing whitespace but encoding/json still treats only the
// first value as the document, so this guard makes the “single value” contract
// explicit without depending on decoder offset behavior.
func singleJSONValue(body []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(body))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return false
	}
	rest := body[dec.InputOffset():]
	return len(bytes.TrimSpace(rest)) == 0
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
