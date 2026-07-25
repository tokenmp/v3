package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/tokenmp/v3/services/notice/internal/models"
	"github.com/tokenmp/v3/services/notice/internal/repository"
)

const maxAdminBodyBytes = 2 << 20 // 2 MiB

// ---- Announcement admin ----

func (s *Server) handleAdminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListAllAnnouncements(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

type adminAnnouncementBody struct {
	Title    string `json:"title"`
	Summary  string `json:"summary"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
}

func (s *Server) handleAdminCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	var body adminAnnouncementBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body.")
		return
	}
	if body.Title == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Title is required.")
		return
	}
	a := &models.Announcement{
		Title:    body.Title,
		Summary:  body.Summary,
		Body:     body.Body,
		Severity: defaultIfEmpty(body.Severity, "info"),
	}
	if err := s.store.CreateAnnouncement(r.Context(), a); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSONStatus(w, http.StatusCreated, a)
}

func (s *Server) handleAdminUpdateAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body.")
		return
	}
	allowed := map[string]bool{"title": true, "summary": true, "body": true, "severity": true}
	fields := make(map[string]any)
	for k, v := range body {
		if allowed[k] {
			fields[k] = v
		}
	}
	if err := s.store.UpdateAnnouncement(r.Context(), id, fields); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Announcement not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleAdminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	if err := s.store.DeleteAnnouncement(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Announcement not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (s *Server) handleAdminPublishAnnouncement(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	if err := s.store.PublishAnnouncement(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Announcement not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "published": true})
}

// ---- Changelog admin ----

func (s *Server) handleAdminListChangelogs(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListAllChangelogs(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

type adminChangelogBody struct {
	Version string `json:"version"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

func (s *Server) handleAdminCreateChangelog(w http.ResponseWriter, r *http.Request) {
	var body adminChangelogBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body.")
		return
	}
	if body.Version == "" || body.Title == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Version and title are required.")
		return
	}
	c := &models.Changelog{
		Version: body.Version,
		Title:   body.Title,
		Body:    body.Body,
	}
	if err := s.store.CreateChangelog(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSONStatus(w, http.StatusCreated, c)
}

func (s *Server) handleAdminUpdateChangelog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	var body map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body.")
		return
	}
	allowed := map[string]bool{"version": true, "title": true, "body": true}
	fields := make(map[string]any)
	for k, v := range body {
		if allowed[k] {
			fields[k] = v
		}
	}
	if err := s.store.UpdateChangelog(r.Context(), id, fields); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Changelog not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleAdminDeleteChangelog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	if err := s.store.DeleteChangelog(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Changelog not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (s *Server) handleAdminPublishChangelog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	if err := s.store.PublishChangelog(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Changelog not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "published": true})
}

// ---- Notification admin ----

func (s *Server) handleAdminListNotifications(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListAllNotifications(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

type adminSendNotificationBody struct {
	UserID string                     `json:"userId"`
	Type   string                     `json:"type"`
	Title  string                     `json:"title"`
	Body   string                     `json:"body"`
	Action *models.NotificationAction `json:"action,omitempty"`
}

func (s *Server) handleAdminSendNotification(w http.ResponseWriter, r *http.Request) {
	var body adminSendNotificationBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body.")
		return
	}
	if body.Title == "" || body.Body == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Title and body are required.")
		return
	}
	if body.Type == "" {
		body.Type = "info"
	}

	// Async send: if userID is empty (broadcast), we insert a single row
	// with user_id="" which clients can match as a broadcast. Otherwise we
	// insert directly. This is a synchronous DB insert for now; a background
	// worker queue can be layered in later without changing the API.
	n := &models.Notification{
		UserID: body.UserID,
		Type:   body.Type,
		Title:  body.Title,
		Body:   body.Body,
		Action: models.NotificationActionPtr{Action: body.Action},
	}
	if err := s.store.CreateNotification(r.Context(), n); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{
		"id":       n.ID,
		"accepted": true,
		"queuedAt": time.Now().UTC(),
	})
}

func (s *Server) handleAdminDeleteNotification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Missing id.")
		return
	}
	if err := s.store.DeleteNotification(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Notification not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// ---- helpers ----

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
