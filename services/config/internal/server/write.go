package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/packages/go/httpresp"
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
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	var body createDraftBody
	if err := json.NewDecoder(io.LimitReader(r.Body, maxConfigBodyBytes)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid body")
		return
	}
	if body.Revision == "" {
		httpresp.Error(w, httpresp.CodeMissingField, "revision required")
		return
	}
	id, err := s.writer.CreateDraft(r.Context(), body.Revision, body.CreatedBy, body.ChangeLog, nil)
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			httpresp.Error(w, httpresp.CodeConflict, "revision exists")
			return
		}
		s.logger.Warn("create draft failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	// If snapshot JSON was provided, store it immediately.
	if len(body.SnapshotJSON) > 0 {
		if err := s.writer.UpdateDraftJSON(r.Context(), id, body.SnapshotJSON); err != nil {
			s.logger.Warn("update draft json failed", "error", err)
		}
	}
	httpresp.Created(w, map[string]any{"id": id, "revision": body.Revision, "status": "draft"})
}

func (s *Server) handleGetDraft(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	dr, err := s.writer.GetDraft(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, dr)
}

func (s *Server) handleUpdateDraft(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes))
	if err != nil {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid body")
		return
	}
	if err := s.writer.UpdateDraftJSON(r.Context(), id, json.RawMessage(raw)); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			httpresp.Error(w, httpresp.CodeConflict, "not draft")
			return
		}
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "updated": true})
}

func (s *Server) handlePublishRevision(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	if err := s.writer.PublishRevision(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		if errors.Is(err, repository.ErrConflict) {
			httpresp.Error(w, httpresp.CodeConflict, "not draft or empty")
			return
		}
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "published": true})
}

func (s *Server) handleRollbackRevision(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	newID, err := s.writer.RollbackRevision(r.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			httpresp.Error(w, httpresp.CodeNotFound, "not found")
			return
		}
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"id": newID, "rolled_back_from": id, "published": true})
}

func (s *Server) handleListRevisions(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
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
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": revs, "total": total})
}
