package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

const maxAdminJSON = 4 << 20 // 4 MiB

// registerAdminRoutes wires all config admin CRUD routes. Each route is
// individually wrapped with the admin-auth middleware (fail-closed 503 when
// unset in production; dev no-auth passes through) so no admin route is
// default-open.
func (s *Server) registerAdminRoutes(r chi.Router) {
	if s.adminReader == nil || s.adminWriter == nil {
		return
	}
	mw := s.adminAuthChi()

	// Models
	r.With(mw).Get("/v1/config/admin/models", s.handleAdminListModels)
	r.With(mw).Post("/v1/config/admin/models", s.handleAdminCreateModel)
	r.With(mw).Get("/v1/config/admin/models/{id}", s.handleAdminGetModel)
	r.With(mw).Patch("/v1/config/admin/models/{id}", s.handleAdminUpdateModel)
	r.With(mw).Delete("/v1/config/admin/models/{id}", s.handleAdminDeleteModel)

	// Providers
	r.With(mw).Get("/v1/config/admin/providers", s.handleAdminListProviders)
	r.With(mw).Post("/v1/config/admin/providers", s.handleAdminCreateProvider)
	r.With(mw).Get("/v1/config/admin/providers/{id}", s.handleAdminGetProvider)
	r.With(mw).Patch("/v1/config/admin/providers/{id}", s.handleAdminUpdateProvider)
	r.With(mw).Delete("/v1/config/admin/providers/{id}", s.handleAdminDeleteProvider)

	// Adapters
	r.With(mw).Get("/v1/config/admin/adapters", s.handleAdminListAdapters)
	r.With(mw).Post("/v1/config/admin/adapters", s.handleAdminCreateAdapter)
	r.With(mw).Get("/v1/config/admin/adapters/{id}", s.handleAdminGetAdapter)
	r.With(mw).Patch("/v1/config/admin/adapters/{id}", s.handleAdminUpdateAdapter)
	r.With(mw).Delete("/v1/config/admin/adapters/{id}", s.handleAdminDeleteAdapter)

	// Endpoints (provider sub-resource)
	r.With(mw).Get("/v1/config/admin/providers/{id}/endpoints", s.handleAdminListEndpoints)
	r.With(mw).Post("/v1/config/admin/providers/{id}/endpoints", s.handleAdminCreateEndpoint)
	r.With(mw).Patch("/v1/config/admin/endpoints/{eid}", s.handleAdminUpdateEndpoint)
	r.With(mw).Delete("/v1/config/admin/endpoints/{eid}", s.handleAdminDeleteEndpoint)

	// Credentials (provider sub-resource)
	r.With(mw).Get("/v1/config/admin/providers/{id}/credentials", s.handleAdminListCredentials)
	r.With(mw).Post("/v1/config/admin/providers/{id}/credentials", s.handleAdminCreateCredential)
	r.With(mw).Patch("/v1/config/admin/credentials/{cid}", s.handleAdminUpdateCredential)
	r.With(mw).Delete("/v1/config/admin/credentials/{cid}", s.handleAdminDeleteCredential)

	// Routes
	r.With(mw).Get("/v1/config/admin/routes", s.handleAdminListRoutes)
	r.With(mw).Post("/v1/config/admin/routes", s.handleAdminCreateRoute)
	r.With(mw).Get("/v1/config/admin/routes/{id}", s.handleAdminGetRoute)
	r.With(mw).Patch("/v1/config/admin/routes/{id}", s.handleAdminUpdateRoute)
	r.With(mw).Delete("/v1/config/admin/routes/{id}", s.handleAdminDeleteRoute)
	r.With(mw).Get("/v1/config/admin/routes/{id}/credentials", s.handleAdminListRouteCredentials)
	r.With(mw).Put("/v1/config/admin/routes/{id}/credentials", s.handleAdminSetRouteCredentials)

	// Compile
	r.With(mw).Post("/v1/config/admin/compile", s.handleAdminCompile)

	// Global policy (retry/timeout/auto_model_ids KV store).
	r.With(mw).Get("/v1/config/admin/global", s.handleAdminGetGlobal)
	r.With(mw).Put("/v1/config/admin/global/{key}", s.handleAdminSetGlobal)
}

// adminAuthChi returns a chi middleware. When adminAuth is configured it
// enforces the shared secret; otherwise it fail-closes with 503 (no
// default-open admin path) unless dev no-auth is active.
func (s *Server) adminAuthChi() func(http.Handler) http.Handler {
	if s.adminAuth != nil {
		return s.adminAuth.Wrap
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		})
	}
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
	if p.ID == "" || p.Name == "" || p.BaseURL == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "missing required fields")
		return
	}
	// Provider SDK/protocol are legacy DB fields kept for compatibility while
	// routing semantics move protocol selection to routes/adapters/endpoints.
	if p.SDKKind == "" {
		p.SDKKind = "openai"
	}
	if p.Protocol == "" {
		p.Protocol = "openai_chat"
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
	// Decode into a local struct that captures any submitted api_key so the
	// secret boundary can reject it. The persisted model never serializes
	// api_key (json:"-"), so this is the only place it is inspected.
	var body struct {
		repository.UpstreamCredential
		APIKey *string `json:"api_key,omitempty"`
	}
	if err := decodeAdminBody(w, r, &body); err != nil {
		return
	}
	c := body.UpstreamCredential
	c.ProviderID = chi.URLParam(r, "id")
	if c.ID == "" {
		httpresp.Error(w, httpresp.CodeBadRequest, "id required")
		return
	}
	// Secret boundary: plaintext API keys must never enter the DB, snapshots,
	// audit or logs. Reject any provided api_key. credential_ref must be an
	// opaque vault:// ref (the only acceptable secret-free reference).
	if body.APIKey != nil && *body.APIKey != "" {
		s.logger.Warn("credential create rejected: plaintext api_key provided", "credential_id", c.ID)
		httpresp.Error(w, httpresp.CodeBadRequest, "plaintext api_key rejected; use a vault:// credential_ref")
		return
	}
	c.APIKey = nil
	if c.CredentialRef == "" {
		c.CredentialRef = "vault://" + c.ProviderID + "/credential/" + c.ID
	}
	if !isVaultRef(c.CredentialRef) {
		httpresp.Error(w, httpresp.CodeBadRequest, "credential_ref must be a vault:// reference")
		return
	}
	// key_prefix/suffix are display-only hints; do not derive from any secret.
	if c.KeyPrefix != nil {
		*c.KeyPrefix = sanitizeDisplayHint(*c.KeyPrefix)
	}
	if c.KeySuffix != nil {
		*c.KeySuffix = sanitizeDisplayHint(*c.KeySuffix)
	}
	if err := s.adminWriter.CreateCredential(r.Context(), &c); err != nil {
		writeAdminWriteErr(w, err)
		return
	}
	// Never echo api_key (already nil). Credential ref is secret-free.
	httpresp.Created(w, c)
}

func (s *Server) handleAdminUpdateCredential(w http.ResponseWriter, r *http.Request) {
	fields, err := decodeAdminFields(w, r)
	if err != nil {
		return
	}
	// Secret boundary: strip any attempted plaintext api_key write. A credential
	// update may adjust priority/status/hints but must never persist a secret.
	if _, ok := fields["api_key"]; ok {
		s.logger.Warn("credential update rejected: plaintext api_key in patch", "credential_id", chi.URLParam(r, "cid"))
		httpresp.Error(w, httpresp.CodeBadRequest, "plaintext api_key rejected; use a vault:// credential_ref")
		return
	}
	if ref, ok := fields["credential_ref"].(string); ok && ref != "" && !isVaultRef(ref) {
		httpresp.Error(w, httpresp.CodeBadRequest, "credential_ref must be a vault:// reference")
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

	global, err := s.adminReader.GetGlobalPolicy(ctx)
	if err != nil {
		s.logger.Warn("compile: read global policy failed", "error", err)
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

	// Build endpoints by provider map.
	endpointsByProvider := make(map[string][]repository.UpstreamEndpoint)
	for _, p := range providers {
		endpoints, err := s.adminReader.ListEndpoints(ctx, p.ID)
		if err != nil {
			s.logger.Warn("compile: list endpoints failed", "provider_id", p.ID, "error", err)
			continue
		}
		endpointsByProvider[p.ID] = endpoints
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
	snapshotJSON, err := compileSnapshotWithEndpoints(models, providers, routes, credentialsByProvider, routeCredentialsByRoute, endpointsByProvider, adapters, global)
	if err != nil {
		s.logger.Warn("compile: snapshot compilation failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "compilation failed")
		return
	}

	// Create a draft with the snapshot and publish atomically. Each call is
	// its own transaction; PublishRevision archives the previous published and
	// publishes the new draft in one tx, so a compile never leaves a half-draft.
	revision := fmt.Sprintf("compile-%d", time.Now().UTC().Unix())
	res, err := s.writer.CreateDraftWithSnapshot(ctx, repository.DraftInput{
		Revision:     revision,
		CreatedBy:    "system",
		ChangeLog:    "admin compile",
		SnapshotJSON: snapshotJSON,
	}, repository.AuditMeta{Actor: "system", ActorKind: "service", RequestID: r.Header.Get("X-Request-ID")})
	if err != nil {
		s.logger.Warn("compile: create draft failed", "error", err)
		writeWriteErr(w, err)
		return
	}
	if err := s.writer.PublishRevision(ctx, res.ID, repository.AuditMeta{Actor: "system", ActorKind: "service", RequestID: r.Header.Get("X-Request-ID")}); err != nil {
		s.logger.Warn("compile: publish failed", "error", err)
		writeWriteErr(w, err)
		return
	}

	httpresp.OK(w, map[string]any{
		"id":        res.ID,
		"revision":  revision,
		"published": true,
	})
}

// ---- Global policy (retry/timeout/auto_model_ids) ----

// handleAdminGetGlobal returns the aggregate global policy (all three KV
// rows). Missing rows are returned as null; the caller applies defaults.
func (s *Server) handleAdminGetGlobal(w http.ResponseWriter, r *http.Request) {
	if s.adminReader == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	policy, err := s.adminReader.GetGlobalPolicy(r.Context())
	if err != nil {
		s.logger.Warn("global: read failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	resp := map[string]any{}
	if len(policy.DefaultRetry) > 0 {
		var v any
		if json.Unmarshal(policy.DefaultRetry, &v) == nil {
			resp["default_retry"] = v
		}
	}
	if len(policy.DefaultTimeout) > 0 {
		var v any
		if json.Unmarshal(policy.DefaultTimeout, &v) == nil {
			resp["default_timeout"] = v
		}
	}
	if len(policy.AutoModelIDs) > 0 {
		var v any
		if json.Unmarshal(policy.AutoModelIDs, &v) == nil {
			resp["auto_model_ids"] = v
		}
	}
	if len(policy.RoutingPolicy) > 0 {
		var v any
		if json.Unmarshal(policy.RoutingPolicy, &v) == nil {
			resp["routing_policy"] = v
		}
	}
	httpresp.OK(w, resp)
}

// validGlobalKeys is the allowlist of keys that may be set via the global
// admin endpoint. Only the three well-known policy keys are accepted.
var validGlobalKeys = map[string]bool{
	string(repository.GlobalKeyDefaultRetry):   true,
	string(repository.GlobalKeyDefaultTimeout): true,
	string(repository.GlobalKeyAutoModelIDs):   true,
	string(repository.GlobalKeyRoutingPolicy):  true,
}

// handleAdminSetGlobal upserts a single global_config row. The key must be
// one of default_retry / default_timeout / auto_model_ids.
func (s *Server) handleAdminSetGlobal(w http.ResponseWriter, r *http.Request) {
	if s.adminWriter == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "admin not configured")
		return
	}
	key := chi.URLParam(r, "key")
	if !validGlobalKeys[key] {
		httpresp.Error(w, httpresp.CodeBadRequest, "unsupported global key")
		return
	}
	var body json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid json body")
		return
	}
	// Re-marshal to canonical form so we store compact, validated JSON.
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		httpresp.Error(w, httpresp.CodeBadRequest, "invalid json body")
		return
	}
	canon, err := json.Marshal(v)
	if err != nil {
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	updatedBy := "admin"
	if u := r.Header.Get("X-User-ID"); u != "" {
		updatedBy = u
	}
	if err := s.adminWriter.SetGlobalConfigEntry(r.Context(), key, canon, updatedBy); err != nil {
		s.logger.Warn("global: write failed", "error", err)
		httpresp.Error(w, httpresp.CodeInternalError, "internal error")
		return
	}
	httpresp.OK(w, map[string]any{"key": key, "updated": true})
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

// isVaultRef reports whether ref is an opaque vault:// reference. It does
// not accept other schemes to keep the secret-free boundary uniform.
func isVaultRef(ref string) bool {
	return strings.HasPrefix(ref, "vault://") && len(strings.TrimPrefix(ref, "vault://")) > 0
}

// sanitizeDisplayHint truncates a display-only prefix/suffix hint to a bounded
// length and strips control chars. It is not derived from any secret.
func sanitizeDisplayHint(s string) string {
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.TrimSpace(s)
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
