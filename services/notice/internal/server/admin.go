package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/tokenmp/v3/services/notice/internal/models"
	"github.com/tokenmp/v3/services/notice/internal/repository"
)

const maxAdminBodyBytes = 2 << 20 // 2 MiB

// defaultPageSize is the page size used by parsePaging when the client omits
// the limit query param. It matches the repository's max clamp so a default
// list returns a full page rather than a single row.
const defaultPageSize = 20

// ---- Admin output DTOs ----
// These DTOs expose fields that models hide with json:"-" for public endpoints.

type adminAnnouncementOut struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Body        string     `json:"body"`
	Severity    string     `json:"severity"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toAdminAnnouncementOut(a models.Announcement) adminAnnouncementOut {
	var pa *time.Time
	if !a.PublishedAt.IsZero() {
		pa = &a.PublishedAt
	}
	return adminAnnouncementOut{
		ID:          a.ID,
		Title:       a.Title,
		Summary:     a.Summary,
		Body:        a.Body,
		Severity:    a.Severity,
		PublishedAt: pa,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

type adminChangelogOut struct {
	ID          string     `json:"id"`
	Version     string     `json:"version"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toAdminChangelogOut(c models.Changelog) adminChangelogOut {
	var pa *time.Time
	if !c.PublishedAt.IsZero() {
		pa = &c.PublishedAt
	}
	return adminChangelogOut{
		ID:          c.ID,
		Version:     c.Version,
		Title:       c.Title,
		Body:        c.Body,
		PublishedAt: pa,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

type adminNotificationOut struct {
	ID        string                     `json:"id"`
	UserID    string                     `json:"user_id"`
	Type      string                     `json:"type"`
	Title     string                     `json:"title"`
	Body      string                     `json:"body"`
	Action    *models.NotificationAction `json:"action"`
	ReadAt    *time.Time                 `json:"read_at"`
	CreatedAt time.Time                  `json:"created_at"`
}

func toAdminNotificationOut(n models.Notification) adminNotificationOut {
	return adminNotificationOut{
		ID:        n.ID,
		UserID:    n.UserID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Action:    n.Action.Action,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
}

// ---- Announcement admin ----

func (s *Server) handleAdminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePaging(r)
	items, total, err := s.store.ListAllAnnouncements(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	outs := make([]adminAnnouncementOut, 0, len(items))
	for _, a := range items {
		outs = append(outs, toAdminAnnouncementOut(a))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []adminAnnouncementOut `json:"items"`
		Total int                    `json:"total"`
	}{Items: outs, Total: total})
}

type adminAnnouncementBody struct {
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Body        string     `json:"body"`
	Severity    string     `json:"severity"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
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
	// summary has a NOT NULL CHECK (summary <> '') constraint; default to the
	// title when omitted rather than failing the insert with a 500.
	summary := body.Summary
	if summary == "" {
		summary = body.Title
	}
	a := &models.Announcement{
		Title:    body.Title,
		Summary:  summary,
		Body:     body.Body,
		Severity: defaultIfEmpty(body.Severity, "info"),
	}
	// GORM only auto-fills CreatedAt/UpdatedAt by convention; PublishedAt is
	// not auto-filled, so a zero value would be inserted, overriding the DB
	// DEFAULT now() and producing published_at = 0001-01-01. When the caller
	// omits it, default to now so the row has a real publish timestamp.
	if body.PublishedAt != nil {
		a.PublishedAt = *body.PublishedAt
	} else {
		a.PublishedAt = time.Now()
	}
	if err := s.store.CreateAnnouncement(r.Context(), a); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSONStatus(w, http.StatusCreated, toAdminAnnouncementOut(*a))
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
	allowed := map[string]bool{"title": true, "summary": true, "body": true, "severity": true, "published_at": true}
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
	outs := make([]adminChangelogOut, 0, len(items))
	for _, c := range items {
		outs = append(outs, toAdminChangelogOut(c))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []adminChangelogOut `json:"items"`
		Total int                 `json:"total"`
	}{Items: outs, Total: total})
}

type adminChangelogBody struct {
	Version     string     `json:"version"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
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
	// See announcements: GORM does not auto-fill PublishedAt, so default to
	// now to avoid a 0001-01-01 row overriding the DB DEFAULT.
	if body.PublishedAt != nil {
		c.PublishedAt = *body.PublishedAt
	} else {
		c.PublishedAt = time.Now()
	}
	if err := s.store.CreateChangelog(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Internal error.")
		return
	}
	writeJSONStatus(w, http.StatusCreated, toAdminChangelogOut(*c))
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
	allowed := map[string]bool{"version": true, "title": true, "body": true, "published_at": true}
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
	outs := make([]adminNotificationOut, 0, len(items))
	for _, n := range items {
		outs = append(outs, toAdminNotificationOut(n))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []adminNotificationOut `json:"items"`
		Total int                    `json:"total"`
	}{Items: outs, Total: total})
}

type adminSendNotificationBody struct {
	UserID string          `json:"userId"`
	Type   string          `json:"type"`
	Title  string          `json:"title"`
	Body   string          `json:"body"`
	Action json.RawMessage `json:"action,omitempty"`
}

// decodeStrictAdminJSON rejects malformed, oversized, duplicate-key, unknown,
// and trailing JSON before decoding the request DTO.
func decodeStrictAdminJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAdminBodyBytes))
	if err != nil {
		return err
	}
	if err := rejectDuplicateJSONKeys(body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// rejectDuplicateJSONKeys walks the whole document so duplicate keys cannot
// alter the semantics of either the request envelope or nested action object.
func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return errors.New("invalid JSON object")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON key")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(map[json.Delim]json.Delim{'{': '}', '[': ']'}[delimiter]) {
		return errors.New("unterminated JSON")
	}
	return nil
}

func (s *Server) handleAdminSendNotification(w http.ResponseWriter, r *http.Request) {
	var body adminSendNotificationBody
	if err := decodeStrictAdminJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body.")
		return
	}
	var action *models.NotificationAction
	if len(body.Action) != 0 && !bytes.Equal(bytes.TrimSpace(body.Action), []byte("null")) {
		var err error
		action, err = models.ParseNotificationAction(body.Action)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "Invalid notification action.")
			return
		}
	}
	if body.Title == "" || body.Body == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Title and body are required.")
		return
	}
	if body.Type == "" {
		body.Type = "info"
	}

	// For broadcast notifications (no userId), use the sentinel UUID
	// because user_id column is NOT NULL.
	userID := body.UserID
	if userID == "" {
		userID = models.BroadcastUserID
	}

	// Async send: for broadcast, insert a single sentinel row that user-facing
	// list/unread queries include. This preserves the current API shape; a
	// background worker can later expand broadcasts into per-user rows for
	// independent read state without changing the admin send request.
	n := &models.Notification{
		UserID: userID,
		Type:   body.Type,
		Title:  body.Title,
		Body:   body.Body,
		Action: models.NotificationActionPtr{Action: action},
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
