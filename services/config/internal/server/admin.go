package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

const maxAdminJSON = 4 << 20 // 4 MiB

// registerAdminRoutes wires all config admin CRUD routes.
func (s *Server) registerAdminRoutes(r chi.Router) {
	if s.adminReader == nil || s.adminWriter == nil {
		return
	}

	// Models
	r.Get("/v1/config/admin/models", s.handleAdminListModels)
	r.Post("/v1/config/admin/models", s.handleAdminCreateModel)
	r.Get("/v1/config/admin/models/{id}", s.handleAdminGetModel)
	r.Patch("/v1/config/admin/models/{id}", s.handleAdminUpdateModel)
	r.Delete("/v1/config/admin/models/{id}", s.handleAdminDeleteModel)

	// Providers
	r.Get("/v1/config/admin/providers", s.handleAdminListProviders)
	r.Post("/v1/config/admin/providers", s.handleAdminCreateProvider)
	r.Get("/v1/config/admin/providers/{id}", s.handleAdminGetProvider)
	r.Patch("/v1/config/admin/providers/{id}", s.handleAdminUpdateProvider)
	r.Delete("/v1/config/admin/providers/{id}", s.handleAdminDeleteProvider)

	// Adapters
	r.Get("/v1/config/admin/adapters", s.handleAdminListAdapters)
	r.Post("/v1/config/admin/adapters", s.handleAdminCreateAdapter)
	r.Get("/v1/config/admin/adapters/{id}", s.handleAdminGetAdapter)
	r.Patch("/v1/config/admin/adapters/{id}", s.handleAdminUpdateAdapter)
	r.Delete("/v1/config/admin/adapters/{id}", s.handleAdminDeleteAdapter)

	// Endpoints (provider sub-resource)
	r.Get("/v1/config/admin/providers/{id}/endpoints", s.handleAdminListEndpoints)
	r.Post("/v1/config/admin/providers/{id}/endpoints", s.handleAdminCreateEndpoint)
	r.Patch("/v1/config/admin/endpoints/{eid}", s.handleAdminUpdateEndpoint)
	r.Delete("/v1/config/admin/endpoints/{eid}", s.handleAdminDeleteEndpoint)

	// Credentials (provider sub-resource)
	r.Get("/v1/config/admin/providers/{id}/credentials", s.handleAdminListCredentials)
	r.Post("/v1/config/admin/providers/{id}/credentials", s.handleAdminCreateCredential)
	r.Patch("/v1/config/admin/credentials/{cid}", s.handleAdminUpdateCredential)
	r.Delete("/v1/config/admin/credentials/{cid}", s.handleAdminDeleteCredential)

	// Routes
	r.Get("/v1/config/admin/routes", s.handleAdminListRoutes)
	r.Post("/v1/config/admin/routes", s.handleAdminCreateRoute)
	r.Get("/v1/config/admin/routes/{id}", s.handleAdminGetRoute)
	r.Patch("/v1/config/admin/routes/{id}", s.handleAdminUpdateRoute)
	r.Delete("/v1/config/admin/routes/{id}", s.handleAdminDeleteRoute)
	r.Get("/v1/config/admin/routes/{id}/credentials", s.handleAdminListRouteCredentials)
	r.Put("/v1/config/admin/routes/{id}/credentials", s.handleAdminSetRouteCredentials)

	// Compile
	r.Post("/v1/config/admin/compile", s.handleAdminCompile)
}

// ---- Models ----

func (s *Server) handleAdminListModels(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListModels(r.Context(), limit, offset)
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateModel(w http.ResponseWriter, r *http.Request) {
	var m repository.Model
	if err := decodeAdminBody(w, r, &m); err != nil {
		return
	}
	if m.ID == "" || m.DisplayName == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "id and display name required")
		return
	}
	if err := s.adminWriter.CreateModel(r.Context(), &m); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.Created(w, m)
}

func (s *Server) handleAdminGetModel(w http.ResponseWriter, r *http.Request) {
	m, err := s.adminReader.GetModel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	httpresp.OK(w, m)
}

func (s *Server) handleAdminUpdateModel(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	if err := s.adminWriter.UpdateModel(r.Context(), chi.URLParam(r, "id"), fields); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteModel(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteModel(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

// ---- Providers ----

func (s *Server) handleAdminListProviders(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListProviders(r.Context(), limit, offset)
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p repository.Provider
	if err := decodeAdminBody(w, r, &p); err != nil {
		return
	}
	if p.ID == "" || p.Name == "" || p.BaseURL == "" || p.SDKKind == "" || p.Protocol == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "missing required fields")
		return
	}
	if err := s.adminWriter.CreateProvider(r.Context(), &p); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.Created(w, p)
}

func (s *Server) handleAdminGetProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminReader.GetProvider(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	httpresp.OK(w, p)
}

func (s *Server) handleAdminUpdateProvider(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	if err := s.adminWriter.UpdateProvider(r.Context(), chi.URLParam(r, "id"), fields); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteProvider(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

// ---- Adapters ----

func (s *Server) handleAdminListAdapters(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListAdapters(r.Context(), limit, offset)
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateAdapter(w http.ResponseWriter, r *http.Request) {
	var a repository.Adapter
	if err := decodeAdminBody(w, r, &a); err != nil {
		return
	}
	if a.ID == "" || a.Name == "" || a.SDKKind == "" || a.Protocol == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "missing required fields")
		return
	}
	if err := s.adminWriter.CreateAdapter(r.Context(), &a); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.Created(w, a)
}

func (s *Server) handleAdminGetAdapter(w http.ResponseWriter, r *http.Request) {
	a, err := s.adminReader.GetAdapter(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	httpresp.OK(w, a)
}

func (s *Server) handleAdminUpdateAdapter(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	if err := s.adminWriter.UpdateAdapter(r.Context(), chi.URLParam(r, "id"), fields); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteAdapter(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteAdapter(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

// ---- Endpoints ----

func (s *Server) handleAdminListEndpoints(w http.ResponseWriter, r *http.Request) {
	items, err := s.adminReader.ListEndpoints(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var e repository.UpstreamEndpoint
	if err := decodeAdminBody(w, r, &e); err != nil {
		return
	}
	e.ProviderID = chi.URLParam(r, "id")
	if e.Path == "" || e.Protocol == "" || e.AuthKind == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "missing required fields")
		return
	}
	if err := s.adminWriter.CreateEndpoint(r.Context(), &e); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.Created(w, e)
}

func (s *Server) handleAdminUpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	eid, err := strconv.ParseInt(chi.URLParam(r, "eid"), 10, 64)
	if err != nil {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	if err := s.adminWriter.UpdateEndpoint(r.Context(), eid, fields); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": eid, "updated": true})
}

func (s *Server) handleAdminDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	eid, err := strconv.ParseInt(chi.URLParam(r, "eid"), 10, 64)
	if err != nil {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid id")
		return
	}
	if err := s.adminWriter.DeleteEndpoint(r.Context(), eid); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": eid, "deleted": true})
}

// ---- Credentials ----

func (s *Server) handleAdminListCredentials(w http.ResponseWriter, r *http.Request) {
	items, err := s.adminReader.ListCredentials(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	// Strip plaintext api_key from list responses.
	for i := range items {
		items[i].APIKey = nil
	}
	httpresp.OK(w, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateCredential(w http.ResponseWriter, r *http.Request) {
	var c repository.UpstreamCredential
	if err := decodeAdminBody(w, r, &c); err != nil {
		return
	}
	c.ProviderID = chi.URLParam(r, "id")
	if c.ID == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "id required")
		return
	}
	// Plaintext API key is required; auto-derive prefix/suffix and credential_ref.
	if c.APIKey == nil || *c.APIKey == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "api_key required")
		return
	}
	apiKey := *c.APIKey
	prefix, suffix := deriveKeyParts(apiKey)
	c.KeyPrefix = &prefix
	c.KeySuffix = &suffix
	if c.CredentialRef == "" {
		ref := "vault://" + c.ProviderID + "/credential/" + c.ID
		c.CredentialRef = ref
	}
	if err := s.adminWriter.CreateCredential(r.Context(), &c); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	// Do not return the plaintext api_key in the list response for safety.
	c.APIKey = nil
	httpresp.Created(w, c)
}

func (s *Server) handleAdminUpdateCredential(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	if err := s.adminWriter.UpdateCredential(r.Context(), chi.URLParam(r, "cid"), fields); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "cid"), "updated": true})
}

func (s *Server) handleAdminDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteCredential(r.Context(), chi.URLParam(r, "cid")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "cid"), "deleted": true})
}

// ---- Routes ----

func (s *Server) handleAdminListRoutes(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListRoutes(r.Context(), limit, offset)
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateRoute(w http.ResponseWriter, r *http.Request) {
	var rm repository.RouteMapping
	if err := decodeAdminBody(w, r, &rm); err != nil {
		return
	}
	if rm.ID == "" || rm.ModelID == "" || rm.ProviderID == "" || rm.UpstreamModel == "" || rm.Protocol == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "missing required fields")
		return
	}
	if err := s.adminWriter.CreateRoute(r.Context(), &rm); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.Created(w, rm)
}

func (s *Server) handleAdminGetRoute(w http.ResponseWriter, r *http.Request) {
	rm, err := s.adminReader.GetRoute(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	httpresp.OK(w, rm)
}

func (s *Server) handleAdminUpdateRoute(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	if err := s.adminWriter.UpdateRoute(r.Context(), chi.URLParam(r, "id"), fields); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteRoute(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

func (s *Server) handleAdminListRouteCredentials(w http.ResponseWriter, r *http.Request) {
	items, err := s.adminReader.ListRouteCredentials(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"items": items})
}

func (s *Server) handleAdminSetRouteCredentials(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Credentials []repository.RouteCredential `json:"credentials"`
	}
	if err := decodeAdminBody(w, r, &body); err != nil {
		return
	}
	if err := s.adminWriter.SetRouteCredentials(r.Context(), chi.URLParam(r, "id"), body.Credentials); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	httpresp.OK(w, map[string]any{"route_id": chi.URLParam(r, "id"), "set": true})
}

// ---- Compile ----

func (s *Server) handleAdminCompile(w http.ResponseWriter, r *http.Request) {
	if s.adminReader == nil || s.writer == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}

	ctx := r.Context()

	// Read all active admin data.
	models, err := s.adminReader.ListAllActiveModels(ctx)
	if err != nil {
		s.logger.Warn("compile: list models failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	providers, err := s.adminReader.ListAllActiveProviders(ctx)
	if err != nil {
		s.logger.Warn("compile: list providers failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	routes, err := s.adminReader.ListAllActiveRoutes(ctx)
	if err != nil {
		s.logger.Warn("compile: list routes failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	adapters, err := s.adminReader.ListAllActiveAdapters(ctx)
	if err != nil {
		s.logger.Warn("compile: list adapters failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	// Build credentials by provider map.
	credentialsByProvider := make(map[string][]repository.UpstreamCredential)
	for _, p := range providers {
		creds, err := s.adminReader.ListCredentials(ctx, p.ID)
		if err != nil {
			s.logger.Warn("compile: list credentials failed", "provider_id", p.ID, "error", err)
			continue
		}
		credentialsByProvider[p.ID] = creds
	}

	// Build route credentials by route map.
	routeCredentialsByRoute := make(map[string][]repository.RouteCredential)
	for _, rm := range routes {
		rcs, err := s.adminReader.ListRouteCredentials(ctx, rm.ID)
		if err != nil {
			s.logger.Warn("compile: list route credentials failed", "route_id", rm.ID, "error", err)
			continue
		}
		routeCredentialsByRoute[rm.ID] = rcs
	}

	// Compile snapshot JSON.
	snapshotJSON, err := compileSnapshot(models, providers, routes, credentialsByProvider, routeCredentialsByRoute, adapters)
	if err != nil {
		s.logger.Warn("compile: snapshot compilation failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "compilation failed")
		return
	}

	// Create a draft revision, write the snapshot JSON, and publish.
	revision := fmt.Sprintf("compile-%d", time.Now().UTC().Unix())
	draftID, err := s.writer.CreateDraft(ctx, revision, "system", "admin compile", nil)
	if err != nil {
		s.logger.Warn("compile: create draft failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	if err := s.writer.UpdateDraftJSON(ctx, draftID, snapshotJSON); err != nil {
		s.logger.Warn("compile: update draft json failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	if err := s.writer.PublishRevision(ctx, draftID); err != nil {
		s.logger.Warn("compile: publish failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}

	httpresp.OK(w, map[string]any{
		"revision":  revision,
		"published": true,
	})
}

// ---- shared helpers ----

func parseAdminPaging(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func decodeAdminBody(w http.ResponseWriter, r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAdminJSON)).Decode(v); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid JSON body")
		return err
	}
	return nil
}

func decodeAdminFields(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	var fields map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAdminJSON)).Decode(&fields); err != nil {
		httpresp.Error(w, httpresp.CodeInvalidJSON, "invalid JSON body")
		return nil, err
	}
	return fields, nil
}

func writeAdminReadErr(w http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		httpresp.Error(w, httpresp.CodeNotFound, "not found")
		return
	}
	httpresp.Error(w, httpresp.CodeInternalError, "internal error")
}

func writeAdminWriteErr(w http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		httpresp.Error(w, httpresp.CodeNotFound, "not found")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		httpresp.Error(w, httpresp.CodeConflict, "conflict")
		return
	}
	httpresp.Error(w, httpresp.CodeInternalError, "internal error")
}

// handleModelsCatalog returns active model IDs for plan allowedModels selector.
func (s *Server) handleModelsCatalog(w http.ResponseWriter, r *http.Request) {
	if s.adminReader == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	ids, err := s.adminReader.ListModelIDs(r.Context())
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, ids)
}

// deriveKeyParts extracts a safe prefix (first 8 chars) and suffix (last 4 chars)
// from a plaintext API key for display purposes.
func deriveKeyParts(key string) (prefix, suffix string) {
	if len(key) <= 12 {
		// For short keys, show what we can without revealing the full key.
		if len(key) > 8 {
			return key[:8], key[len(key)-4:]
		}
		return key, ""
	}
	return key[:8], key[len(key)-4:]
}
