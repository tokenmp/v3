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
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/tokenmp/v3/packages/go/httpresp"
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
}

// New returns a Server wired with the given reader (snapshot source), writer
// (draft/publish lifecycle) and pinger (DB readiness). logger must be non-nil.
// adminReader/adminWriter can be the same *GormRepository.
func New(reader repository.Reader, writer repository.Writer, pinger database.Pinger, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{reader: reader, writer: writer, pinger: pinger, logger: logger}
	if ar, ok := writer.(repository.AdminReader); ok {
		s.adminReader = ar
	}
	if aw, ok := writer.(repository.AdminWriter); ok {
		s.adminWriter = aw
	}
	// Also try reader for admin interfaces (GormRepository implements all).
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
	// Write path (draft/publish)
	r.Post("/v1/config/drafts", s.handleCreateDraft)
	r.Get("/v1/config/drafts/{id}", s.handleGetDraft)
	r.Patch("/v1/config/drafts/{id}", s.handleUpdateDraft)
	r.Post("/v1/config/revisions/{id}/publish", s.handlePublishRevision)
	r.Post("/v1/config/revisions/{id}/rollback", s.handleRollbackRevision)
	r.Get("/v1/config/revisions", s.handleListRevisions)
	// Admin CRUD for config tables (providers/models/routes/...)
	s.registerAdminRoutes(r)
	// Models catalog (model IDs for plan allowedModels selector)
	r.Get("/v1/config/models/catalog", s.handleModelsCatalog)
	return r
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
	// This endpoint serves raw ConfigSnapshot JSON consumed by the executor's
	// config source — it must NOT be wrapped in the {code,data,message} envelope.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Config-Revision", snap.Revision)
	w.Header().Set("X-Config-SHA256", snap.SHA256)
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(snap); err != nil {
		s.logger.Error("snapshot encode failed", "error", err)
	}
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
