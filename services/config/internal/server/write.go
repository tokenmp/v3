package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

const maxConfigBodyBytes = 4 << 20 // 4 MiB

type createDraftBody struct {
	Revision     string          `json:"revision"`
	CreatedBy    string          `json:"created_by,omitempty"`
	ChangeLog    string          `json:"change_log,omitempty"`
	SnapshotJSON json.RawMessage `json:"snapshot_json,omitempty"`
}

func (s *Server) handleCreateDraft(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "write_not_configured")
		return
	}
	var body createDraftBody
	if err := json.NewDecoder(io.LimitReader(r.Body, maxConfigBodyBytes)).Decode(&body); err != nil {
		writeConfigErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if body.Revision == "" {
		writeConfigErr(w, http.StatusBadRequest, "revision_required")
		return
	}
	id, err := s.writer.CreateDraft(r.Context(), body.Revision, body.CreatedBy, body.ChangeLog, nil)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			writeConfigErr(w, http.StatusConflict, "revision_exists")
			return
		}
		s.logger.Warn("create draft failed", "error", err)
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	// If snapshot JSON was provided, store it immediately.
	if len(body.SnapshotJSON) > 0 {
		if err := s.writer.UpdateDraftJSON(r.Context(), id, body.SnapshotJSON); err != nil {
			s.logger.Warn("update draft json failed", "error", err)
		}
	}
	writeConfigJSON(w, http.StatusCreated, map[string]any{"id": id, "revision": body.Revision, "status": "draft"})
}

func (s *Server) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "write_not_configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeConfigErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	dr, err := s.writer.GetDraft(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeConfigErr(w, http.StatusNotFound, "not_found")
			return
		}
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, dr)
}

func (s *Server) handleUpdateDraft(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "write_not_configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeConfigErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes))
	if err != nil {
		writeConfigErr(w, http.StatusBadRequest, "invalid_body")
		return
	}
	if err := s.writer.UpdateDraftJSON(r.Context(), id, json.RawMessage(raw)); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeConfigErr(w, http.StatusNotFound, "not_found")
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			writeConfigErr(w, http.StatusConflict, "not_draft")
			return
		}
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

func (s *Server) handlePublishRevision(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "write_not_configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeConfigErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	if err := s.writer.PublishRevision(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeConfigErr(w, http.StatusNotFound, "not_found")
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			writeConfigErr(w, http.StatusConflict, "not_draft_or_empty")
			return
		}
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": id, "published": true})
}

func (s *Server) handleRollbackRevision(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "write_not_configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeConfigErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	newID, err := s.writer.RollbackRevision(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeConfigErr(w, http.StatusNotFound, "not_found")
			return
		}
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": newID, "rolled_back_from": id, "published": true})
}

func (s *Server) handleListRevisions(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "write_not_configured")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	revs, total, err := s.writer.ListRevisions(r.Context(), limit, offset)
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": revs, "total": total})
}

// ---- helpers ----

func writeConfigJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeConfigErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code},
	})
}
