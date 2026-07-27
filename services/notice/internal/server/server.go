// Package server wires the notice service HTTP routes: health/readiness
// probes, JWT-authenticated announcement/changelog/notification endpoints.
//
// Errors are stable, classified JSON bodies of the form
// {"error":{"code","message"}} and never leak driver/DSN details. All
// responses carry Cache-Control: no-store.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/notice/internal/jwtverifier"
	"github.com/tokenmp/v3/services/notice/internal/models"
	"github.com/tokenmp/v3/services/notice/internal/repository"
)

// Pinger is the readiness contract.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Store is the data-access contract the server depends on. It is satisfied by
// *repository.Repository and by test doubles.
type Store interface {
	ListAnnouncements(ctx context.Context, limit, offset int) ([]models.Announcement, int, error)
	GetAnnouncement(ctx context.Context, id string) (models.Announcement, error)
	ListChangelogs(ctx context.Context, limit, offset int) ([]models.Changelog, int, error)
	GetChangelog(ctx context.Context, id string) (models.Changelog, error)
	ListNotifications(ctx context.Context, userID string, limit, offset int) ([]models.Notification, int, error)
	UnreadCount(ctx context.Context, userID string) (int, error)
	MarkRead(ctx context.Context, userID, id string) error
	MarkAllRead(ctx context.Context, userID string) error
	// Admin methods
	ListAllAnnouncements(ctx context.Context, limit, offset int) ([]models.Announcement, int, error)
	CreateAnnouncement(ctx context.Context, a *models.Announcement) error
	UpdateAnnouncement(ctx context.Context, id string, fields map[string]any) error
	DeleteAnnouncement(ctx context.Context, id string) error
	PublishAnnouncement(ctx context.Context, id string) error
	ListAllChangelogs(ctx context.Context, limit, offset int) ([]models.Changelog, int, error)
	CreateChangelog(ctx context.Context, c *models.Changelog) error
	UpdateChangelog(ctx context.Context, id string, fields map[string]any) error
	DeleteChangelog(ctx context.Context, id string) error
	PublishChangelog(ctx context.Context, id string) error
	ListAllNotifications(ctx context.Context, limit, offset int) ([]models.Notification, int, error)
	CreateNotification(ctx context.Context, n *models.Notification) error
	DeleteNotification(ctx context.Context, id string) error
}

// AuthVerifier is the JWT verification contract. It is satisfied by
// *jwtverifier.Verifier and by test doubles.
type AuthVerifier interface {
	Verify(raw string) (jwtverifier.Subject, error)
}

// Server holds the notice service dependencies.
type Server struct {
	addr     string
	pinger   Pinger
	verifier AuthVerifier
	store    Store
	logger   *slog.Logger
	now      func() time.Time
}

// ServerConfig assembles a Server.
type ServerConfig struct {
	Addr     string
	Pinger   Pinger
	Verifier AuthVerifier
	Store    Store
	Logger   *slog.Logger
}

// New returns a configured *http.Server with all routes registered.
func New(cfg ServerConfig) *http.Server {
	mux := http.NewServeMux()
	s := &Server{
		addr:     cfg.Addr,
		pinger:   cfg.Pinger,
		verifier: cfg.Verifier,
		store:    cfg.Store,
		logger:   cfg.Logger,
		now:      time.Now,
	}

	// Health / readiness (anonymous).
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("HEAD /healthz", s.handleHealthzNoBody)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// Authenticated notice endpoints.
	authMux := http.NewServeMux()
	authMux.HandleFunc("GET /api/v1/notice/announcements", s.handleListAnnouncements)
	authMux.HandleFunc("GET /api/v1/notice/announcements/{id}", s.handleGetAnnouncement)
	authMux.HandleFunc("GET /api/v1/notice/changelogs", s.handleListChangelogs)
	authMux.HandleFunc("GET /api/v1/notice/changelogs/{id}", s.handleGetChangelog)
	authMux.HandleFunc("GET /api/v1/notice/notifications", s.handleListNotifications)
	authMux.HandleFunc("GET /api/v1/notice/notifications/unread-count", s.handleUnreadCount)
	authMux.HandleFunc("POST /api/v1/notice/notifications/{id}/read", s.handleMarkRead)
	authMux.HandleFunc("POST /api/v1/notice/notifications/read-all", s.handleMarkAllRead)

	// Admin endpoints (require role=admin, checked by adminMiddleware).
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /api/v1/notice/admin/announcements", s.handleAdminListAnnouncements)
	adminMux.HandleFunc("POST /api/v1/notice/admin/announcements", s.handleAdminCreateAnnouncement)
	adminMux.HandleFunc("PATCH /api/v1/notice/admin/announcements/{id}", s.handleAdminUpdateAnnouncement)
	adminMux.HandleFunc("DELETE /api/v1/notice/admin/announcements/{id}", s.handleAdminDeleteAnnouncement)
	adminMux.HandleFunc("POST /api/v1/notice/admin/announcements/{id}/publish", s.handleAdminPublishAnnouncement)
	adminMux.HandleFunc("GET /api/v1/notice/admin/changelogs", s.handleAdminListChangelogs)
	adminMux.HandleFunc("POST /api/v1/notice/admin/changelogs", s.handleAdminCreateChangelog)
	adminMux.HandleFunc("PATCH /api/v1/notice/admin/changelogs/{id}", s.handleAdminUpdateChangelog)
	adminMux.HandleFunc("DELETE /api/v1/notice/admin/changelogs/{id}", s.handleAdminDeleteChangelog)
	adminMux.HandleFunc("POST /api/v1/notice/admin/changelogs/{id}/publish", s.handleAdminPublishChangelog)
	adminMux.HandleFunc("GET /api/v1/notice/admin/notifications", s.handleAdminListNotifications)
	adminMux.HandleFunc("POST /api/v1/notice/admin/notifications/send", s.handleAdminSendNotification)
	adminMux.HandleFunc("DELETE /api/v1/notice/admin/notifications/{id}", s.handleAdminDeleteNotification)

	mux.Handle("/api/v1/notice/admin/", s.authMiddleware(s.adminMiddleware(adminMux)))
	mux.Handle("/api/v1/notice/", s.authMiddleware(authMux))

	return &http.Server{
		Addr:              s.addr,
		Handler:           s.noStoreMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// ---- Middleware ----

// noStoreMiddleware sets Cache-Control: no-store on every response.
func (s *Server) noStoreMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// authMiddleware verifies the bearer token and stores the subject in the
// request context. On any failure it returns a protocol-native 401 fail
// closed, without leaking which check failed.
func (s *Server) authMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
			return
		}
		raw := strings.TrimPrefix(authHeader, "Bearer ")
		subject, err := s.verifier.Verify(raw)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
			return
		}
		ctx := context.WithValue(r.Context(), subjectKey{}, subject)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

type subjectKey struct{}

func subjectFromCtx(r *http.Request) jwtverifier.Subject {
	if v, ok := r.Context().Value(subjectKey{}).(jwtverifier.Subject); ok {
		return v
	}
	return jwtverifier.Subject{}
}

// adminMiddleware wraps the handler with a role=admin check. It must be
// used inside authMiddleware so the subject is already verified.
func (s *Server) adminMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := subjectFromCtx(r)
		if sub.Role != "admin" {
			writeError(w, http.StatusForbidden, "forbidden", "Admin role required.")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ---- Helpers ----

func writeJSON(w http.ResponseWriter, status int, body any) {
	// All success responses use the httpresp envelope.
	httpresp.OK(w, body)
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	// For non-200 success (e.g. 201 Created, 202 Accepted).
	switch status {
	case http.StatusCreated:
		httpresp.Created(w, body)
	case http.StatusAccepted:
		// httpresp has no Accepted helper; write envelope directly.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(struct {
			Code    int    `json:"code"`
			Data    any    `json:"data"`
			Message string `json:"message"`
		}{Code: 0, Data: body, Message: "success"})
	default:
		// Other non-200 — use OK with the body.
		httpresp.OK(w, body)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	// Map string code to numeric httpresp.Code based on HTTP status.
	var c httpresp.Code
	switch status {
	case http.StatusBadRequest:
		c = httpresp.CodeBadRequest
	case http.StatusUnauthorized:
		c = httpresp.CodeUnauthorized
	case http.StatusForbidden:
		c = httpresp.CodeForbidden
	case http.StatusNotFound:
		c = httpresp.CodeNotFound
	case http.StatusConflict:
		c = httpresp.CodeConflict
	case http.StatusServiceUnavailable:
		c = httpresp.CodeServiceUnavailable
	default:
		c = httpresp.CodeInternalError
	}
	httpresp.ErrorWithStatus(w, status, c, message)
}

// parsePaging extracts the limit/offset query params, applying sensible
// defaults when omitted. A missing limit defaults to defaultPageSize (20)
// rather than 0 — the repository clamps 0 down to 1, which would silently
// return only a single row for callers that omit paging (e.g. the admin
// list endpoints invoked without query params).
func parsePaging(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 {
		limit = defaultPageSize
	}
	return limit, offset
}

// notificationOut maps a model to its JSON shape, exposing the action as a
// nullable object.
type notificationOut struct {
	ID        string                     `json:"id"`
	Type      string                     `json:"type"`
	Title     string                     `json:"title"`
	Body      string                     `json:"body"`
	Action    *models.NotificationAction `json:"action"`
	ReadAt    *time.Time                 `json:"read_at"`
	CreatedAt time.Time                  `json:"created_at"`
}

func toNotificationOut(n models.Notification) notificationOut {
	return notificationOut{
		ID:        n.ID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Action:    n.Action.Action,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}

// ---- Handlers ----

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   "notice",
		"timestamp": s.now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleHealthzNoBody(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.pinger == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "unready",
			"service": "notice",
		})
		return
	}
	pingCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.pinger.Ping(pingCtx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "unready",
			"service": "notice",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   "notice",
		"timestamp": s.now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleListAnnouncements(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListAnnouncements(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	writeJSON(w, http.StatusOK, models.Page[models.Announcement]{Items: items, Total: total})
}

func (s *Server) handleGetAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := repository.ParseUUID(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Announcement not found.")
		return
	}
	a, err := s.store.GetAnnouncement(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Announcement not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleListChangelogs(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListChangelogs(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	writeJSON(w, http.StatusOK, models.Page[models.Changelog]{Items: items, Total: total})
}

func (s *Server) handleGetChangelog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := repository.ParseUUID(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Changelog not found.")
		return
	}
	c, err := s.store.GetChangelog(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Changelog not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	sub := subjectFromCtx(r)
	if sub.UserID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListNotifications(r.Context(), sub.UserID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	outs := make([]notificationOut, 0, len(items))
	for _, n := range items {
		outs = append(outs, toNotificationOut(n))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []notificationOut `json:"items"`
		Total int               `json:"total"`
	}{Items: outs, Total: total})
}

func (s *Server) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	sub := subjectFromCtx(r)
	if sub.UserID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	count, err := s.store.UnreadCount(r.Context(), sub.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	sub := subjectFromCtx(r)
	if sub.UserID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	id := r.PathValue("id")
	if err := repository.ParseUUID(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Notification not found.")
		return
	}
	if err := s.store.MarkRead(r.Context(), sub.UserID, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Notification not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	sub := subjectFromCtx(r)
	if sub.UserID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	if err := s.store.MarkAllRead(r.Context(), sub.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
