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

// auditMetaFrom extracts a safe AuditMeta from the request. The actor is
// sourced from X-User-ID (forwarded by the trusted Edge admin proxy) or falls
// back to a fixed service marker. It never carries a secret.
func (s *Server) auditMetaFrom(r *http.Request, fallbackActor string) repository.AuditMeta {
	actor := r.Header.Get("X-User-ID")
	if actor == "" {
		actor = fallbackActor
	}
	kind := r.Header.Get("X-Actor-Kind")
	if kind == "" {
		kind = "service"
	}
	return repository.AuditMeta{
		Actor:     actor,
		ActorKind: kind,
		RequestID: r.Header.Get("X-Request-ID"),
	}
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
	createdBy := body.CreatedBy
	if createdBy == "" {
		createdBy = "admin"
	}
	res, err := s.writer.CreateDraftWithSnapshot(r.Context(), repository.DraftInput{
		Revision:     body.Revision,
		CreatedBy:    createdBy,
		ChangeLog:    body.ChangeLog,
		SnapshotJSON: body.SnapshotJSON,
	}, s.auditMetaFrom(r, createdBy))
	if err != nil {
		writeWriteErr(w, err)
		return
	}
	httpresp.Created(w, res)
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
	dr, err := s.writer.GetRevision(r.Context(), id)
	if err != nil {
		writeWriteErr(w, err)
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
	// CAS: If-Match carries the expected version. Missing If-Match defaults to
	// the current version (no optimistic lock) for compatibility.
	expectedVersion, _ := strconv.Atoi(r.Header.Get("If-Match"))
	if expectedVersion <= 0 {
		cur, err := s.writer.GetRevision(r.Context(), id)
		if err != nil {
			writeWriteErr(w, err)
			return
		}
		expectedVersion = cur.Version
	}
	newVersion, err := s.writer.UpdateDraftSnapshot(r.Context(), id, expectedVersion, json.RawMessage(raw), s.auditMetaFrom(r, "admin"))
	if err != nil {
		writeWriteErr(w, err)
		return
	}
	w.Header().Set("ETag", strconv.Itoa(newVersion))
	httpresp.OK(w, map[string]any{"id": id, "version": newVersion, "updated": true})
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
	if err := s.writer.PublishRevision(r.Context(), id, s.auditMetaFrom(r, "admin")); err != nil {
		writeWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "published": true})
}

func (s *Server) handleArchiveRevision(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	if err := s.writer.ArchiveRevision(r.Context(), id, s.auditMetaFrom(r, "admin")); err != nil {
		writeWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": id, "archived": true})
}

func (s *Server) handleRevertRevision(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	var body struct {
		Note      string `json:"note,omitempty"`
		CreatedBy string `json:"created_by,omitempty"`
	}
	// Body optional; ignore decode errors for empty bodies.
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	createdBy := body.CreatedBy
	if createdBy == "" {
		createdBy = "admin"
	}
	newID, err := s.writer.RollbackAsNew(r.Context(), id, body.Note, createdBy, s.auditMetaFrom(r, createdBy))
	if err != nil {
		writeWriteErr(w, err)
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

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "write not configured")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var revID *int64
	if v := q.Get("revision_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil && id > 0 {
			revID = &id
		}
	}
	entries, total, err := s.writer.ListAudit(r.Context(), revID, limit, offset)
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": entries, "total": total})
}

// writeWriteErr maps repository sentinel errors to HTTP responses. It never
// leaks DSN/SQL or a snapshot body; all messages are fixed strings.
func writeWriteErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		httpresp.Error(w, httpresp.CodeNotFound, "not found")
	case errors.Is(err, repository.ErrConflict):
		httpresp.Error(w, httpresp.CodeConflict, "conflicting state")
	case errors.Is(err, repository.ErrCASMismatch):
		// 412 Precondition Failed for optimistic-concurrency mismatch.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":{"code":"version_mismatch","message":"revision version changed"}}`))
	case errors.Is(err, repository.ErrEmptySnapshot):
		httpresp.Error(w, httpresp.CodeConflict, "empty snapshot")
	case errors.Is(err, repository.ErrSecretRejected):
		httpresp.Error(w, httpresp.CodeBadRequest, "plaintext secret rejected")
	case errors.Is(err, repository.ErrEmptyAuditMeta):
		httpresp.Error(w, httpresp.CodeBadRequest, "actor required")
	default:
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
	}
}
