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
}

// ---- Models ----

func (s *Server) handleAdminListModels(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListModels(r.Context(), limit, offset)
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateModel(w http.ResponseWriter, r *http.Request) {
	var m repository.Model
	if err := decodeAdminBody(w, r, &m); err != nil {
		return
	}
	if m.ID == "" || m.DisplayName == "" {
		writeConfigErr(w, http.StatusBadRequest, "id_and_display_name_required")
		return
	}
	if err := s.adminWriter.CreateModel(r.Context(), &m); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusCreated, m)
}

func (s *Server) handleAdminGetModel(w http.ResponseWriter, r *http.Request) {
	m, err := s.adminReader.GetModel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, m)
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteModel(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteModel(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

// ---- Providers ----

func (s *Server) handleAdminListProviders(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListProviders(r.Context(), limit, offset)
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p repository.Provider
	if err := decodeAdminBody(w, r, &p); err != nil {
		return
	}
	if p.ID == "" || p.Name == "" || p.BaseURL == "" || p.SDKKind == "" || p.Protocol == "" {
		writeConfigErr(w, http.StatusBadRequest, "missing_required_fields")
		return
	}
	if err := s.adminWriter.CreateProvider(r.Context(), &p); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusCreated, p)
}

func (s *Server) handleAdminGetProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.adminReader.GetProvider(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, p)
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteProvider(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

// ---- Adapters ----

func (s *Server) handleAdminListAdapters(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListAdapters(r.Context(), limit, offset)
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateAdapter(w http.ResponseWriter, r *http.Request) {
	var a repository.Adapter
	if err := decodeAdminBody(w, r, &a); err != nil {
		return
	}
	if a.ID == "" || a.Name == "" || a.SDKKind == "" || a.Protocol == "" {
		writeConfigErr(w, http.StatusBadRequest, "missing_required_fields")
		return
	}
	if err := s.adminWriter.CreateAdapter(r.Context(), &a); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusCreated, a)
}

func (s *Server) handleAdminGetAdapter(w http.ResponseWriter, r *http.Request) {
	a, err := s.adminReader.GetAdapter(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, a)
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteAdapter(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteAdapter(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

// ---- Endpoints ----

func (s *Server) handleAdminListEndpoints(w http.ResponseWriter, r *http.Request) {
	items, err := s.adminReader.ListEndpoints(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var e repository.UpstreamEndpoint
	if err := decodeAdminBody(w, r, &e); err != nil {
		return
	}
	e.ProviderID = chi.URLParam(r, "id")
	if e.Path == "" || e.Protocol == "" || e.AuthKind == "" {
		writeConfigErr(w, http.StatusBadRequest, "missing_required_fields")
		return
	}
	if err := s.adminWriter.CreateEndpoint(r.Context(), &e); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusCreated, e)
}

func (s *Server) handleAdminUpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	eid, err := strconv.ParseInt(chi.URLParam(r, "eid"), 10, 64)
	if err != nil {
		writeConfigErr(w, http.StatusBadRequest, "invalid_id")
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": eid, "updated": true})
}

func (s *Server) handleAdminDeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	eid, err := strconv.ParseInt(chi.URLParam(r, "eid"), 10, 64)
	if err != nil {
		writeConfigErr(w, http.StatusBadRequest, "invalid_id")
		return
	}
	if err := s.adminWriter.DeleteEndpoint(r.Context(), eid); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": eid, "deleted": true})
}

// ---- Credentials ----

func (s *Server) handleAdminListCredentials(w http.ResponseWriter, r *http.Request) {
	items, err := s.adminReader.ListCredentials(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateCredential(w http.ResponseWriter, r *http.Request) {
	var c repository.UpstreamCredential
	if err := decodeAdminBody(w, r, &c); err != nil {
		return
	}
	c.ProviderID = chi.URLParam(r, "id")
	if c.ID == "" || c.CredentialRef == "" {
		writeConfigErr(w, http.StatusBadRequest, "id_and_credential_ref_required")
		return
	}
	if err := s.adminWriter.CreateCredential(r.Context(), &c); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusCreated, c)
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "cid"), "updated": true})
}

func (s *Server) handleAdminDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteCredential(r.Context(), chi.URLParam(r, "cid")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "cid"), "deleted": true})
}

// ---- Routes ----

func (s *Server) handleAdminListRoutes(w http.ResponseWriter, r *http.Request) {
	limit, offset := parseAdminPaging(r)
	items, total, err := s.adminReader.ListRoutes(r.Context(), limit, offset)
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleAdminCreateRoute(w http.ResponseWriter, r *http.Request) {
	var rm repository.RouteMapping
	if err := decodeAdminBody(w, r, &rm); err != nil {
		return
	}
	if rm.ID == "" || rm.ModelID == "" || rm.ProviderID == "" || rm.UpstreamModel == "" || rm.Protocol == "" {
		writeConfigErr(w, http.StatusBadRequest, "missing_required_fields")
		return
	}
	if err := s.adminWriter.CreateRoute(r.Context(), &rm); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusCreated, rm)
}

func (s *Server) handleAdminGetRoute(w http.ResponseWriter, r *http.Request) {
	rm, err := s.adminReader.GetRoute(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAdminReadErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, rm)
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "updated": true})
}

func (s *Server) handleAdminDeleteRoute(w http.ResponseWriter, r *http.Request) {
	if err := s.adminWriter.DeleteRoute(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id"), "deleted": true})
}

func (s *Server) handleAdminListRouteCredentials(w http.ResponseWriter, r *http.Request) {
	items, err := s.adminReader.ListRouteCredentials(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"items": items})
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
	writeConfigJSON(w, http.StatusOK, map[string]any{"route_id": chi.URLParam(r, "id"), "set": true})
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
		writeConfigErr(w, http.StatusBadRequest, "invalid_body")
		return err
	}
	return nil
}

func decodeAdminFields(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	var fields map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAdminJSON)).Decode(&fields); err != nil {
		writeConfigErr(w, http.StatusBadRequest, "invalid_body")
		return nil, err
	}
	return fields, nil
}

func writeAdminReadErr(w http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writeConfigErr(w, http.StatusNotFound, "not_found")
		return
	}
	writeConfigErr(w, http.StatusInternalServerError, "internal_error")
}

func writeAdminWriteErr(w http.ResponseWriter, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		writeConfigErr(w, http.StatusNotFound, "not_found")
		return
	}
	if errors.Is(err, repository.ErrConflict) {
		writeConfigErr(w, http.StatusConflict, "conflict")
		return
	}
	writeConfigErr(w, http.StatusInternalServerError, "internal_error")
}

// handleModelsCatalog returns active model IDs for plan allowedModels selector.
func (s *Server) handleModelsCatalog(w http.ResponseWriter, r *http.Request) {
	if s.adminReader == nil {
		writeConfigErr(w, http.StatusServiceUnavailable, "admin_not_configured")
		return
	}
	ids, err := s.adminReader.ListModelIDs(r.Context())
	if err != nil {
		writeConfigErr(w, http.StatusInternalServerError, "internal_error")
		return
	}
	writeConfigJSON(w, http.StatusOK, ids)
}
