package admin

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/packages/go/httpresp"
	"github.com/tokenmp/v3/services/api/internal/config"
)

// ConfigHandlers proxies admin CRUD routes to the Config Service.
type ConfigHandlers struct {
	cfg *config.Client
}

// NewConfigHandlers returns config admin handlers. cfg may be nil (routes
// return 503 when not wired).
func NewConfigHandlers(cfg *config.Client) *ConfigHandlers {
	return &ConfigHandlers{cfg: cfg}
}

// Routes registers all config admin proxy routes on the given router.
func (h *ConfigHandlers) Routes(r chi.Router) {
	// Models catalog (for plan allowedModels selector) — issue #89
	r.Get("/api/v1/admin/models/catalog", h.handleModelsCatalog)

	// Config admin CRUD — transparent proxy to Config Service
	cfgRoutes := []struct {
		method, edgePath, cfgPath string
	}{
		// Models
		{http.MethodGet, "/api/v1/admin/models", "/v1/config/admin/models"},
		{http.MethodPost, "/api/v1/admin/models", "/v1/config/admin/models"},
		{http.MethodGet, "/api/v1/admin/models/{id}", "/v1/config/admin/models/{id}"},
		{http.MethodPatch, "/api/v1/admin/models/{id}", "/v1/config/admin/models/{id}"},
		{http.MethodDelete, "/api/v1/admin/models/{id}", "/v1/config/admin/models/{id}"},
		// Providers
		{http.MethodGet, "/api/v1/admin/providers", "/v1/config/admin/providers"},
		{http.MethodPost, "/api/v1/admin/providers", "/v1/config/admin/providers"},
		{http.MethodGet, "/api/v1/admin/providers/{id}", "/v1/config/admin/providers/{id}"},
		{http.MethodPatch, "/api/v1/admin/providers/{id}", "/v1/config/admin/providers/{id}"},
		{http.MethodDelete, "/api/v1/admin/providers/{id}", "/v1/config/admin/providers/{id}"},
		// Adapters
		{http.MethodGet, "/api/v1/admin/adapters", "/v1/config/admin/adapters"},
		{http.MethodPost, "/api/v1/admin/adapters", "/v1/config/admin/adapters"},
		{http.MethodGet, "/api/v1/admin/adapters/{id}", "/v1/config/admin/adapters/{id}"},
		{http.MethodPatch, "/api/v1/admin/adapters/{id}", "/v1/config/admin/adapters/{id}"},
		{http.MethodDelete, "/api/v1/admin/adapters/{id}", "/v1/config/admin/adapters/{id}"},
		// Endpoints
		{http.MethodGet, "/api/v1/admin/providers/{id}/endpoints", "/v1/config/admin/providers/{id}/endpoints"},
		{http.MethodPost, "/api/v1/admin/providers/{id}/endpoints", "/v1/config/admin/providers/{id}/endpoints"},
		{http.MethodPatch, "/api/v1/admin/endpoints/{eid}", "/v1/config/admin/endpoints/{eid}"},
		{http.MethodDelete, "/api/v1/admin/endpoints/{eid}", "/v1/config/admin/endpoints/{eid}"},
		// Credentials
		{http.MethodGet, "/api/v1/admin/providers/{id}/credentials", "/v1/config/admin/providers/{id}/credentials"},
		{http.MethodPost, "/api/v1/admin/providers/{id}/credentials", "/v1/config/admin/providers/{id}/credentials"},
		{http.MethodPatch, "/api/v1/admin/credentials/{cid}", "/v1/config/admin/credentials/{cid}"},
		{http.MethodDelete, "/api/v1/admin/credentials/{cid}", "/v1/config/admin/credentials/{cid}"},
		// Routes
		{http.MethodGet, "/api/v1/admin/routes", "/v1/config/admin/routes"},
		{http.MethodPost, "/api/v1/admin/routes", "/v1/config/admin/routes"},
		{http.MethodGet, "/api/v1/admin/routes/{id}", "/v1/config/admin/routes/{id}"},
		{http.MethodPatch, "/api/v1/admin/routes/{id}", "/v1/config/admin/routes/{id}"},
		{http.MethodDelete, "/api/v1/admin/routes/{id}", "/v1/config/admin/routes/{id}"},
		{http.MethodGet, "/api/v1/admin/routes/{id}/credentials", "/v1/config/admin/routes/{id}/credentials"},
		{http.MethodPut, "/api/v1/admin/routes/{id}/credentials", "/v1/config/admin/routes/{id}/credentials"},
		// Global policy (retry/timeout)
		{http.MethodGet, "/api/v1/admin/global", "/v1/config/admin/global"},
		{http.MethodGet, "/api/v1/admin/global/{key}", "/v1/config/admin/global/{key}"},
		{http.MethodPut, "/api/v1/admin/global/{key}", "/v1/config/admin/global/{key}"},
		// Compile
		{http.MethodPost, "/api/v1/admin/compile", "/v1/config/admin/compile"},
	}

	for _, rt := range cfgRoutes {
		// Capture loop variables correctly.
		method, edgePath, cfgPath := rt.method, rt.edgePath, rt.cfgPath
		h.handleProxyRoute(r, method, edgePath, cfgPath)
	}
}

func (h *ConfigHandlers) handleProxyRoute(r chi.Router, method, edgePath, cfgPath string) {
	switch method {
	case http.MethodGet:
		r.Get(edgePath, func(w http.ResponseWriter, req *http.Request) {
			h.proxyToConfig(w, req, cfgPath)
		})
	case http.MethodPost:
		r.Post(edgePath, func(w http.ResponseWriter, req *http.Request) {
			h.proxyToConfig(w, req, cfgPath)
		})
	case http.MethodPatch:
		r.Patch(edgePath, func(w http.ResponseWriter, req *http.Request) {
			h.proxyToConfig(w, req, cfgPath)
		})
	case http.MethodPut:
		r.Put(edgePath, func(w http.ResponseWriter, req *http.Request) {
			h.proxyToConfig(w, req, cfgPath)
		})
	case http.MethodDelete:
		r.Delete(edgePath, func(w http.ResponseWriter, req *http.Request) {
			h.proxyToConfig(w, req, cfgPath)
		})
	}
}

// proxyToConfig forwards the request to the Config Service, substituting
// chi URL params ({id}, {eid}, {cid}) with the actual values.
func (h *ConfigHandlers) proxyToConfig(w http.ResponseWriter, r *http.Request, cfgPath string) {
	if h.cfg == nil {
		httpresp.Error(w, httpresp.CodeServiceUnavailable, "config service not configured")
		return
	}
	// Substitute path params.
	path := cfgPath
	for _, param := range []string{"id", "eid", "cid", "key"} {
		val := chi.URLParam(r, param)
		if val != "" {
			path = strings.ReplaceAll(path, "{"+param+"}", val)
		}
	}
	// For list routes, preserve query params.
	if r.URL.RawQuery != "" {
		path = config.BuildListPath(path, r.URL.RawQuery)
	}

	var body io.Reader
	if r.Body != nil {
		body = r.Body
	}
	data, status, err := h.cfg.Proxy(r.Context(), r.Method, path, body)
	if err != nil && status >= 500 {
		httpresp.Error(w, httpresp.CodeBadGateway, "config service unavailable")
		return
	}
	if err != nil && status == 0 {
		httpresp.Error(w, httpresp.CodeBadGateway, "config service unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// handleModelsCatalog returns active model IDs for plan allowedModels (#89).
func (h *ConfigHandlers) handleModelsCatalog(w http.ResponseWriter, r *http.Request) {
	if h.cfg == nil {
		// Fallback: return empty list instead of erroring.
		httpresp.OK(w, []string{})
		return
	}
	ids, err := h.cfg.GetModelIDs(r.Context())
	if err != nil {
		httpresp.Error(w, httpresp.CodeBadGateway, "config service unavailable")
		return
	}
	httpresp.OK(w, ids)
}
