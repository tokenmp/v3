package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/tokenmp/v3/services/config/internal/repository"
)

// ---- Local marshal structs for ConfigSnapshot JSON ----
// These mirror the executor's snapshot types with uppercase JSON keys.
// The config service MUST NOT import the executor package; field names must
// match exactly what executor's parseConfig (DisallowUnknownFields) expects.

type wireConfigSnapshot struct {
	Revision  string                  `json:"Revision"`
	CreatedAt string                  `json:"CreatedAt"`
	Global    wireGlobal              `json:"Global"`
	Models    map[string]wireModel    `json:"Models"`
	Providers map[string]wireProvider `json:"Providers"`
	Routes    []wireRoute             `json:"Routes"`
	Adapters  map[string]wireAdapter  `json:"Adapters"`
}

type wireGlobal struct {
	Retry        wireRetryPolicy   `json:"Retry"`
	Timeout      wireTimeoutPolicy `json:"Timeout"`
	AutoModelIDs []string          `json:"AutoModelIDs"`
}

type wireRetryPolicy struct {
	MaxTotalAttempts      *int            `json:"MaxTotalAttempts,omitempty"`
	MaxSameTargetAttempts *int            `json:"MaxSameTargetAttempts,omitempty"`
	MaxTotalDuration      string          `json:"MaxTotalDuration,omitempty"`
	Backoff               string          `json:"Backoff,omitempty"`
	Rules                 []wireRetryRule `json:"Rules,omitempty"`
}

type wireRetryRule struct {
	ID           string   `json:"ID"`
	Priority     int      `json:"Priority"`
	HTTPStatuses []int    `json:"HTTPStatuses"`
	ErrorCodes   []string `json:"ErrorCodes,omitempty"`
	ErrorTypes   []string `json:"ErrorTypes,omitempty"`
	Action       string   `json:"Action"`
}

type wireTimeoutPolicy struct {
	RequestTimeout    string `json:"RequestTimeout,omitempty"`
	TTFTTimeout       string `json:"TTFTTimeout,omitempty"`
	StreamIdleTimeout string `json:"StreamIdleTimeout,omitempty"`
	StreamMaxLifetime string `json:"StreamMaxLifetime,omitempty"`
	RetryBackoff      string `json:"RetryBackoff,omitempty"`
}

type wireModel struct {
	ID                string            `json:"ID"`
	DisplayName       string            `json:"DisplayName"`
	Capabilities      []string          `json:"Capabilities"`
	Thinking          wireModelThinking `json:"Thinking"`
	BillingMultiplier float64           `json:"BillingMultiplier,omitempty"`
	FallbackModelIDs  []string          `json:"FallbackModelIDs,omitempty"`
}

type wireModelThinking struct {
	Supported      bool   `json:"Supported"`
	DefaultEffort  string `json:"DefaultEffort"`
	MaxEffort      string `json:"MaxEffort"`
	MinBudgetToken int    `json:"MinBudgetToken"`
	MaxBudgetToken int    `json:"MaxBudgetToken"`
}

type wireProvider struct {
	ID       string            `json:"ID"`
	Selector string            `json:"Selector"`
	Name     string            `json:"Name"`
	BaseURL  string            `json:"BaseURL"`
	SDKKind  string            `json:"SDKKind"`
	Protocol string            `json:"Protocol"`
	Retry    wireRetryPolicy   `json:"Retry"`
	Timeout  wireTimeoutPolicy `json:"Timeout"`
}

type wireRoute struct {
	ID               string            `json:"ID"`
	ModelID          string            `json:"ModelID"`
	ProviderID       string            `json:"ProviderID"`
	AdapterID        string            `json:"AdapterID"`
	UpstreamModel    string            `json:"UpstreamModel"`
	BaseURL          string            `json:"BaseURL,omitempty"`
	Priority         int               `json:"Priority"`
	Enabled          bool              `json:"Enabled"`
	Protocol         string            `json:"Protocol"`
	Retry            wireRetryPolicy   `json:"Retry"`
	Timeout          wireTimeoutPolicy `json:"Timeout"`
	Credentials      []wireCredential  `json:"Credentials"`
	FallbackRouteIDs []string          `json:"FallbackRouteIDs,omitempty"`
	RouteGroup       string            `json:"RouteGroup,omitempty"`
}

type wireCredential struct {
	ID            string `json:"ID"`
	CredentialRef string `json:"CredentialRef"`
	Priority      int    `json:"Priority"`
	Enabled       bool   `json:"Enabled"`
}

type wireAdapter struct {
	ID         string              `json:"ID"`
	Name       string              `json:"Name"`
	Version    int                 `json:"Version"`
	SDKKind    string              `json:"SDKKind"`
	Protocol   string              `json:"Protocol"`
	Auth       wireAuth            `json:"Auth"`
	Capability wireCapability      `json:"Capability"`
	Thinking   wireAdapterThinking `json:"Thinking"`
	Request    wireRequest         `json:"Request"`
	Response   wireResponse        `json:"Response"`
	Retry      wireRetryPolicy     `json:"Retry"`
	Timeout    wireTimeoutPolicy   `json:"Timeout"`
}

type wireAuth struct {
	Kind          string `json:"Kind"`
	Header        string `json:"Header"`
	Query         string `json:"Query,omitempty"`
	Prefix        string `json:"Prefix"`
	CredentialRef string `json:"CredentialRef"`
}

type wireCapability struct {
	Require []string `json:"Require"`
	Deny    []string `json:"Deny"`
}

type wireAdapterThinking struct {
	Supported      bool              `json:"Supported"`
	DefaultEffort  string            `json:"DefaultEffort"`
	EffortMapping  map[string]string `json:"EffortMapping"`
	BudgetMapping  map[string]int    `json:"BudgetMapping"`
	MinBudgetToken int               `json:"MinBudgetToken"`
	MaxBudgetToken int               `json:"MaxBudgetToken"`
}

type wireRequest struct {
	AllowedHeaders []string          `json:"AllowedHeaders"`
	AllowedQuery   []string          `json:"AllowedQuery"`
	Rules          []wireRequestRule `json:"Rules"`
}

type wireRequestRule struct {
	ID       string            `json:"ID"`
	Action   string            `json:"Action"`
	Path     string            `json:"Path,omitempty"`
	From     string            `json:"From,omitempty"`
	To       string            `json:"To,omitempty"`
	Value    json.RawMessage   `json:"Value,omitempty"`
	EnumMap  map[string]string `json:"EnumMap,omitempty"`
	Min      *float64          `json:"Min,omitempty"`
	Max      *float64          `json:"Max,omitempty"`
	Name     string            `json:"Name,omitempty"`
	ValueRef string            `json:"ValueRef,omitempty"`
}

type wireResponse struct {
	Rules []wireResponseRule `json:"Rules"`
}

type wireResponseRule struct {
	ID       string             `json:"ID"`
	Priority int                `json:"Priority"`
	Match    wireResponseMatch  `json:"Match"`
	Output   wireResponseOutput `json:"Output"`
}

type wireResponseMatch struct {
	HTTPStatuses     []int    `json:"HTTPStatuses"`
	ErrorCodes       []string `json:"ErrorCodes,omitempty"`
	ErrorTypes       []string `json:"ErrorTypes,omitempty"`
	MessageContains  []string `json:"MessageContains,omitempty"`
	FinishReasons    []string `json:"FinishReasons,omitempty"`
	StreamEventTypes []string `json:"StreamEventTypes,omitempty"`
}

type wireResponseOutput struct {
	HTTPStatus int    `json:"HTTPStatus"`
	ErrorCode  string `json:"ErrorCode"`
	ErrorType  string `json:"ErrorType"`
	Message    string `json:"Message"`
}

// ---- Capability mapping ----
// mapCapability translates admin/DB capability vocabulary to executor's
// adapter.Capability vocabulary. The admin UI and database use "text" and
// "image", but the executor only accepts "chat" and "images" respectively.
// Unknown values are mapped to empty string and must be filtered out.
func mapCapability(adminCap string) string {
	switch adminCap {
	case "text":
		return "chat"
	case "image":
		return "images"
	case "chat", "responses", "messages", "images", "streaming", "tools", "vision", "thinking":
		return adminCap
	default:
		return ""
	}
}

// mapCapabilities applies mapCapability to a slice, deduplicates, and
// filters out empty (unknown) values. Returns a sorted slice.
func mapCapabilities(caps []string) []string {
	seen := make(map[string]bool, len(caps))
	result := make([]string, 0, len(caps))
	for _, c := range caps {
		mapped := mapCapability(c)
		if mapped == "" || seen[mapped] {
			continue
		}
		seen[mapped] = true
		result = append(result, mapped)
	}
	sort.Strings(result)
	return result
}

// ---- Default adapter templates ----

var defaultEffortMapping = map[string]string{
	"none":    "none",
	"minimal": "minimal",
	"low":     "low",
	"medium":  "medium",
	"high":    "high",
	"xhigh":   "high",
	"max":     "high",
}

func defaultBudgetMapping(maxBudget int) map[string]int {
	low := maxBudget / 4
	medium := maxBudget / 2
	if low == 0 {
		low = 2000
	}
	if medium == 0 {
		medium = 4000
	}
	return map[string]int{
		"low":    low,
		"medium": medium,
		"high":   maxBudget,
	}
}

func defaultGlobalRetry() wireRetryPolicy {
	mta := 3
	msta := 2
	return wireRetryPolicy{
		MaxTotalAttempts:      &mta,
		MaxSameTargetAttempts: &msta,
		MaxTotalDuration:      "45s",
		Backoff:               "500ms",
		Rules: []wireRetryRule{
			{ID: "global-retry-429", Priority: 10, HTTPStatuses: []int{429}, Action: "next_credential"},
			{ID: "global-retry-5xx", Priority: 20, HTTPStatuses: []int{500, 502, 503, 504}, Action: "next_route"},
		},
	}
}

// resolveGlobalRetry decodes the global_config default_retry jsonb value.
// When the value is absent or fails to decode, the safe built-in default is
// returned so a misconfigured row can never produce an empty policy.
func resolveGlobalRetry(raw json.RawMessage) wireRetryPolicy {
	if len(raw) == 0 {
		return defaultGlobalRetry()
	}
	var p wireRetryPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return defaultGlobalRetry()
	}
	// Sanitize: enforce non-empty rules and valid actions. An invalid row is
	// treated as missing so the executor never receives a half-formed policy.
	for i := range p.Rules {
		if !validRetryAction(p.Rules[i].Action) {
			return defaultGlobalRetry()
		}
		if p.Rules[i].ID == "" {
			return defaultGlobalRetry()
		}
	}
	if len(p.Rules) == 0 {
		return defaultGlobalRetry()
	}
	return p
}

// resolveGlobalTimeout decodes the global_config default_timeout jsonb value,
// falling back to the built-in default when absent or invalid.
func resolveGlobalTimeout(raw json.RawMessage) wireTimeoutPolicy {
	if len(raw) == 0 {
		return defaultGlobalTimeout()
	}
	var p wireTimeoutPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return defaultGlobalTimeout()
	}
	return p
}

// resolveAutoModelIDs honors the admin-configured global auto_model_ids list
// when it is a non-empty JSON array of strings that all reference existing
// active models. The configured order is significant: the executor picks the
// first eligible candidate for the reserved "auto" selector. When the config
// is absent, empty, references unknown/inactive models, or is malformed, it
// falls back to all active models sorted by ID for deterministic output.
func resolveAutoModelIDs(raw json.RawMessage, models []repository.Model) []string {
	active := make(map[string]bool, len(models))
	for _, m := range models {
		if m.Status == "active" {
			active[m.ID] = true
		}
	}
	if len(raw) > 0 {
		var ids []string
		if err := json.Unmarshal(raw, &ids); err == nil {
			ok := len(ids) > 0
			for _, id := range ids {
				if !active[id] {
					ok = false
					break
				}
			}
			if ok {
				return ids
			}
		}
	}
	// Fall back: all active models sorted by ID.
	fallback := make([]string, 0, len(models))
	for _, m := range models {
		if m.Status == "active" {
			fallback = append(fallback, m.ID)
		}
	}
	sort.Strings(fallback)
	return fallback
}

// validRetryAction reports whether action is a supported retry action.
func validRetryAction(action string) bool {
	switch action {
	case "none", "same_credential", "next_credential", "next_route", "next_provider", "next_model":
		return true
	}
	return false
}

// validRetryRules reports whether every rule has a non-empty ID and a valid
// action. Empty rule lists are valid (the route inherits the global policy).
func validRetryRules(rules []wireRetryRule) bool {
	for _, r := range rules {
		if r.ID == "" || !validRetryAction(r.Action) {
			return false
		}
	}
	return true
}

func defaultGlobalTimeout() wireTimeoutPolicy {
	return wireTimeoutPolicy{
		RequestTimeout:    "120s",
		TTFTTimeout:       "20s",
		StreamIdleTimeout: "30s",
		StreamMaxLifetime: "300s",
		RetryBackoff:      "500ms",
	}
}

// compileSnapshot reads all active admin data and produces a ConfigSnapshot
// JSON byte slice that is compatible with the executor's parseConfig strict
// decoder (uppercase JSON keys, DisallowUnknownFields, no secrets).
func compileSnapshot(
	models []repository.Model,
	providers []repository.Provider,
	routes []repository.RouteMapping,
	credentialsByProvider map[string][]repository.UpstreamCredential,
	routeCredentialsByRoute map[string][]repository.RouteCredential,
	adapters []repository.Adapter,
	global repository.GlobalPolicy,
) ([]byte, error) {
	return compileSnapshotWithEndpoints(models, providers, routes, credentialsByProvider, routeCredentialsByRoute, nil, adapters, global)
}

func compileSnapshotWithEndpoints(
	models []repository.Model,
	providers []repository.Provider,
	routes []repository.RouteMapping,
	credentialsByProvider map[string][]repository.UpstreamCredential,
	routeCredentialsByRoute map[string][]repository.RouteCredential,
	endpointsByProvider map[string][]repository.UpstreamEndpoint,
	adapters []repository.Adapter,
	global repository.GlobalPolicy,
) ([]byte, error) {
	now := time.Now().UTC()
	revision := fmt.Sprintf("compile-%d", now.Unix())

	// ---- AutoModelIDs ----
	// Honor the admin-configured global auto_model_ids when it is a non-empty
	// list of model IDs that all exist as active models; the order is
	// significant (the executor picks the first eligible candidate). When
	// unset/empty/invalid, fall back to all active models sorted by ID for
	// deterministic output.
	autoModelIDs := resolveAutoModelIDs(global.AutoModelIDs, models)

	// ---- Models ----
	wireModels := make(map[string]wireModel, len(models))
	for _, m := range models {
		caps := mapCapabilities(m.Capabilities)

		thinking := wireModelThinking{Supported: false}
		if m.ThinkingSupported {
			thinking.Supported = true
			thinking.DefaultEffort = "medium"
			thinking.MaxEffort = "high"
			if m.ThinkingDefaultEffort != nil && *m.ThinkingDefaultEffort != "" {
				thinking.DefaultEffort = *m.ThinkingDefaultEffort
			}
			if m.ThinkingMaxEffort != nil && *m.ThinkingMaxEffort != "" {
				thinking.MaxEffort = *m.ThinkingMaxEffort
			}
			thinking.MinBudgetToken = 0
			thinking.MaxBudgetToken = 8000
			if m.ThinkingMinBudgetToken != nil {
				thinking.MinBudgetToken = *m.ThinkingMinBudgetToken
			}
			if m.ThinkingMaxBudgetToken != nil {
				thinking.MaxBudgetToken = *m.ThinkingMaxBudgetToken
			}
		}
		wireModels[m.ID] = wireModel{
			ID:                m.ID,
			DisplayName:       m.DisplayName,
			Capabilities:      caps,
			Thinking:          thinking,
			BillingMultiplier: m.BillingMultiplier,
		}
	}

	// ---- Adapters: build from DB adapters + auto-generate protocol-scoped defaults ----
	// Providers are protocol-neutral. A route without an explicit adapter uses a
	// provider-specific adapter for its protocol when one exists, then a generic
	// DB adapter for the route SDK/protocol, and finally an auto-generated
	// adapter for that provider+protocol.
	wireAdapters := make(map[string]wireAdapter)
	providerAdapterByProtocol := make(map[string]map[string]string)
	genericAdapterBySDKProtocol := make(map[string]string)

	// First, include DB adapters and index them for default route lookup.
	for _, a := range adapters {
		if a.Status == "deleted" {
			continue
		}
		wa, err := dbAdapterToWire(a, credentialsByProvider)
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", a.ID, err)
		}
		wireAdapters[a.ID] = wa
		if a.ProviderID != nil && *a.ProviderID != "" {
			byProtocol := providerAdapterByProtocol[*a.ProviderID]
			if byProtocol == nil {
				byProtocol = make(map[string]string)
				providerAdapterByProtocol[*a.ProviderID] = byProtocol
			}
			if _, exists := byProtocol[a.Protocol]; !exists {
				byProtocol[a.Protocol] = a.ID
			}
			continue
		}
		key := a.SDKKind + "|" + a.Protocol
		if _, exists := genericAdapterBySDKProtocol[key]; !exists {
			genericAdapterBySDKProtocol[key] = a.ID
		}
	}

	protocolsByProvider := routeProtocolsByProvider(routes)
	for _, p := range providers {
		if p.Status == "deleted" {
			continue
		}
		protocols := protocolsByProvider[p.ID]
		if len(protocols) == 0 && p.Protocol != "" {
			// Backward-compatible behavior for legacy provider-scoped protocol
			// rows with no routes yet: still expose the historical default adapter.
			protocols = []string{p.Protocol}
		}
		for _, protocol := range protocols {
			if existingDefaultAdapterID(p.ID, protocol, providerAdapterByProtocol, genericAdapterBySDKProtocol) != "" {
				continue
			}
			adapterID := autoAdapterID(p.ID, protocol, len(protocols) > 1)
			if _, exists := wireAdapters[adapterID]; exists {
				continue
			}
			wa := autoGenerateAdapter(p, protocol, sdkKindForProtocol(protocol), adapterID)
			wireAdapters[adapterID] = wa
		}
	}

	// ---- Providers ----
	wireProviders := make(map[string]wireProvider, len(providers))
	for _, p := range providers {
		if p.Status == "deleted" {
			continue
		}
		var retry wireRetryPolicy
		var timeout wireTimeoutPolicy
		if len(p.DefaultRetry) > 0 {
			_ = json.Unmarshal(p.DefaultRetry, &retry)
		}
		if len(p.DefaultTimeout) > 0 {
			_ = json.Unmarshal(p.DefaultTimeout, &timeout)
		}
		wireProviders[p.ID] = wireProvider{
			ID:       p.ID,
			Selector: p.Selector,
			Name:     p.Name,
			BaseURL:  p.BaseURL,
			SDKKind:  p.SDKKind,
			Protocol: p.Protocol,
			Retry:    retry,
			Timeout:  timeout,
		}
	}

	providerByID := make(map[string]repository.Provider, len(providers))
	for _, p := range providers {
		providerByID[p.ID] = p
	}

	// ---- Routes ----
	wireRoutes := make([]wireRoute, 0, len(routes))
	for _, rm := range routes {
		if rm.Status == "deleted" {
			continue
		}

		// Determine adapter ID. Providers are protocol-neutral, so implicit
		// adapters are selected per route protocol rather than per provider row.
		adapterID := ""
		if rm.AdapterID != nil && *rm.AdapterID != "" {
			adapterID = *rm.AdapterID
		}
		if adapterID == "" {
			adapterID = existingDefaultAdapterID(rm.ProviderID, rm.Protocol, providerAdapterByProtocol, genericAdapterBySDKProtocol)
		}
		if adapterID == "" {
			protocolCount := len(protocolsByProvider[rm.ProviderID])
			adapterID = autoAdapterID(rm.ProviderID, rm.Protocol, protocolCount > 1)
		}

		// Determine credentials for this route.
		routeCreds := routeCredentialsByRoute[rm.ID]
		wireCreds := make([]wireCredential, 0)

		if len(routeCreds) > 0 {
			// Use route_credentials join table.
			providerCreds := credentialsByProvider[rm.ProviderID]
			credByID := make(map[string]repository.UpstreamCredential)
			for _, c := range providerCreds {
				credByID[c.ID] = c
			}
			for _, rc := range routeCreds {
				if c, ok := credByID[rc.CredentialID]; ok && c.Status != "deleted" {
					wireCreds = append(wireCreds, wireCredential{
						ID:            c.ID,
						CredentialRef: c.CredentialRef,
						Priority:      rc.Priority,
						Enabled:       rc.Enabled,
					})
				}
			}
		} else {
			// Fallback: all active credentials for the provider.
			providerCreds := credentialsByProvider[rm.ProviderID]
			for _, c := range providerCreds {
				if c.Status != "deleted" {
					wireCreds = append(wireCreds, wireCredential{
						ID:            c.ID,
						CredentialRef: c.CredentialRef,
						Priority:      c.Priority,
						Enabled:       true,
					})
				}
			}
		}

		var retry wireRetryPolicy
		var timeout wireTimeoutPolicy
		if len(rm.RetryPolicy) > 0 {
			var parsed wireRetryPolicy
			if err := json.Unmarshal(rm.RetryPolicy, &parsed); err == nil && validRetryRules(parsed.Rules) {
				retry = parsed
			}
			// An invalid route retry_policy is silently dropped: the route then
			// inherits the global retry policy downstream, never a malformed one.
		}
		if len(rm.TimeoutPolicy) > 0 {
			_ = json.Unmarshal(rm.TimeoutPolicy, &timeout)
		}

		routeGroup := ""
		if rm.RouteGroup != nil {
			routeGroup = *rm.RouteGroup
		}

		baseURL := ""
		if provider, ok := providerByID[rm.ProviderID]; ok {
			baseURL = routeBaseURL(provider.BaseURL, endpointForProtocol(endpointsByProvider[rm.ProviderID], rm.Protocol))
		}

		wireRoutes = append(wireRoutes, wireRoute{
			ID:            rm.ID,
			ModelID:       rm.ModelID,
			ProviderID:    rm.ProviderID,
			AdapterID:     adapterID,
			UpstreamModel: rm.UpstreamModel,
			BaseURL:       baseURL,
			Priority:      rm.Priority,
			Enabled:       rm.Enabled,
			Protocol:      rm.Protocol,
			Retry:         retry,
			Timeout:       timeout,
			Credentials:   wireCreds,
			RouteGroup:    routeGroup,
		})
	}

	snap := wireConfigSnapshot{
		Revision:  revision,
		CreatedAt: now.Format(time.RFC3339),
		Global: wireGlobal{
			Retry:        resolveGlobalRetry(global.DefaultRetry),
			Timeout:      resolveGlobalTimeout(global.DefaultTimeout),
			AutoModelIDs: autoModelIDs,
		},
		Models:    wireModels,
		Providers: wireProviders,
		Routes:    wireRoutes,
		Adapters:  wireAdapters,
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal snapshot: %w", err)
	}
	return data, nil
}

// dbAdapterToWire converts a DB Adapter record to a wire adapter.
func dbAdapterToWire(a repository.Adapter, credentialsByProvider map[string][]repository.UpstreamCredential) (wireAdapter, error) {
	var auth wireAuth
	// DB adapters do not store auth directly; derive from SDK kind/protocol
	// and fill CredentialRef from provider's first active credential.
	switch a.SDKKind {
	case "openai", "generic_http":
		auth = wireAuth{
			Kind:   "bearer_header",
			Header: "Authorization",
			Prefix: "Bearer",
		}
	case "anthropic":
		auth = wireAuth{
			Kind:   "api_key_header",
			Header: "x-api-key",
			Prefix: "",
		}
	}

	// CredentialRef on DB adapter.Auth is intentionally left empty:
	// credentials are provided via route.Credentials (explicit candidates).
	// Setting both is rejected by the executor compiler.

	var cap_ wireCapability
	if len(a.CapabilityRequire) > 0 {
		var require []string
		_ = json.Unmarshal(a.CapabilityRequire, &require)
		cap_.Require = mapCapabilities(require)
	}
	if len(a.CapabilityDeny) > 0 {
		var deny []string
		_ = json.Unmarshal(a.CapabilityDeny, &deny)
		cap_.Deny = mapCapabilities(deny)
	}

	var thinking wireAdapterThinking
	if len(a.Thinking) > 0 {
		_ = json.Unmarshal(a.Thinking, &thinking)
	}

	var req wireRequest
	if len(a.RequestPolicy) > 0 {
		_ = json.Unmarshal(a.RequestPolicy, &req)
	}

	var resp wireResponse
	if len(a.ResponsePolicy) > 0 {
		_ = json.Unmarshal(a.ResponsePolicy, &resp)
	}

	var retry wireRetryPolicy
	if len(a.RetryPolicy) > 0 {
		_ = json.Unmarshal(a.RetryPolicy, &retry)
	}

	var timeout wireTimeoutPolicy
	if len(a.TimeoutPolicy) > 0 {
		_ = json.Unmarshal(a.TimeoutPolicy, &timeout)
	}

	return wireAdapter{
		ID:         a.ID,
		Name:       a.Name,
		Version:    a.Version,
		SDKKind:    a.SDKKind,
		Protocol:   a.Protocol,
		Auth:       auth,
		Capability: cap_,
		Thinking:   thinking,
		Request:    req,
		Response:   resp,
		Retry:      retry,
		Timeout:    timeout,
	}, nil
}

func endpointForProtocol(endpoints []repository.UpstreamEndpoint, protocol string) *repository.UpstreamEndpoint {
	for i := range endpoints {
		if endpoints[i].Status != "deleted" && endpoints[i].Protocol == protocol {
			return &endpoints[i]
		}
	}
	return nil
}

func routeBaseURL(providerBaseURL string, endpoint *repository.UpstreamEndpoint) string {
	if endpoint == nil || strings.TrimSpace(endpoint.Path) == "" {
		return ""
	}
	base, err := url.Parse(providerBaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ""
	}
	prefix := sdkBasePathForEndpoint(endpoint.Protocol, endpoint.Path)
	if prefix == "" {
		return ""
	}
	basePath := strings.TrimRight(base.Path, "/")
	if basePath == prefix || strings.HasSuffix(basePath, prefix) {
		base.Path = basePath
	} else {
		base.Path = basePath + prefix
	}
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
}

func sdkBasePathForEndpoint(protocol, endpointPath string) string {
	path := "/" + strings.TrimLeft(endpointPath, "/")
	operationSuffix := map[string]string{
		"openai_chat":        "/chat/completions",
		"openai_responses":   "/responses",
		"openai_images":      "/images/generations",
		"anthropic_messages": "/messages",
	}[protocol]
	if operationSuffix == "" || !strings.HasSuffix(path, operationSuffix) {
		return ""
	}
	prefix := strings.TrimSuffix(path, operationSuffix)
	if prefix == "" {
		return ""
	}
	return prefix
}

func routeProtocolsByProvider(routes []repository.RouteMapping) map[string][]string {
	seen := make(map[string]map[string]bool)
	for _, route := range routes {
		if route.Status == "deleted" || route.ProviderID == "" || route.Protocol == "" || route.AdapterID != nil && *route.AdapterID != "" {
			continue
		}
		byProtocol := seen[route.ProviderID]
		if byProtocol == nil {
			byProtocol = make(map[string]bool)
			seen[route.ProviderID] = byProtocol
		}
		byProtocol[route.Protocol] = true
	}
	out := make(map[string][]string, len(seen))
	for providerID, byProtocol := range seen {
		protocols := make([]string, 0, len(byProtocol))
		for protocol := range byProtocol {
			protocols = append(protocols, protocol)
		}
		sort.Strings(protocols)
		out[providerID] = protocols
	}
	return out
}

func sdkKindForProtocol(protocol string) string {
	switch protocol {
	case "anthropic_messages":
		return "anthropic"
	default:
		return "openai"
	}
}

func existingDefaultAdapterID(providerID, protocol string, providerAdapters map[string]map[string]string, genericAdapters map[string]string) string {
	if byProtocol := providerAdapters[providerID]; byProtocol != nil {
		if id := byProtocol[protocol]; id != "" {
			return id
		}
	}
	return genericAdapters[sdkKindForProtocol(protocol)+"|"+protocol]
}

func autoAdapterID(providerID, protocol string, providerHasMultipleProtocols bool) string {
	if providerHasMultipleProtocols {
		return "adapter-" + providerID + "-" + protocol
	}
	return "adapter-" + providerID
}

// autoGenerateAdapter creates a default adapter for a provider/protocol pair
// that has no DB adapter record. Providers are protocol-neutral; SDKKind and
// protocol come from the route's protocol, not from the provider row.
func autoGenerateAdapter(p repository.Provider, protocol, sdkKind, adapterID string) wireAdapter {
	// Determine auth config based on SDK kind and protocol.
	auth := wireAuth{}
	capability := wireCapability{}
	allowedHeaders := []string{"Content-Type", "Accept"}
	var responseRules []wireResponseRule

	switch sdkKind {
	case "openai", "generic_http":
		auth = wireAuth{
			Kind:   "bearer_header",
			Header: "Authorization",
			Prefix: "Bearer",
		}
		capability = wireCapability{
			Require: []string{"chat"},
			Deny:    []string{},
		}
		responseRules = []wireResponseRule{
			{
				ID: "resp-429", Priority: 10,
				Match:  wireResponseMatch{HTTPStatuses: []int{429}},
				Output: wireResponseOutput{HTTPStatus: 429, ErrorCode: "rate_limited", ErrorType: "rate_limited", Message: "upstream rate limited"},
			},
		}
	case "anthropic":
		auth = wireAuth{
			Kind:   "api_key_header",
			Header: "x-api-key",
			Prefix: "",
		}
		allowedHeaders = []string{"Content-Type", "Accept", "x-api-key", "anthropic-version"}
		capability = wireCapability{
			Require: []string{"messages"},
			Deny:    []string{},
		}
		responseRules = []wireResponseRule{
			{
				ID: "resp-529-to-429", Priority: 10,
				Match:  wireResponseMatch{HTTPStatuses: []int{529}, ErrorTypes: []string{"overloaded_error"}},
				Output: wireResponseOutput{HTTPStatus: 429, ErrorCode: "rate_limited", ErrorType: "rate_limited", Message: "upstream overloaded; retryable as rate limited"},
			},
			{
				ID: "resp-429", Priority: 20,
				Match:  wireResponseMatch{HTTPStatuses: []int{429}},
				Output: wireResponseOutput{HTTPStatus: 429, ErrorCode: "rate_limited", ErrorType: "rate_limited", Message: "upstream rate limited"},
			},
		}
	}

	// CredentialRef on auto-adapter.Auth is intentionally left empty:
	// credentials are provided via route.Credentials (explicit candidates).
	// Setting both is rejected by the executor compiler.

	// Thinking config: default to supported with medium/high.
	thinking := wireAdapterThinking{
		Supported:      true,
		DefaultEffort:  "medium",
		EffortMapping:  defaultEffortMapping,
		BudgetMapping:  defaultBudgetMapping(8000),
		MinBudgetToken: 0,
		MaxBudgetToken: 8000,
	}

	// Retry/Timeout config: intentionally left empty so the executor compiler
	// inherits the global policy (global_config default_retry/timeout). A
	// non-empty rules slice here would override the global policy at the
	// adapter layer, defeating admin-configured global retry rules.
	retry := wireRetryPolicy{}
	timeout := wireTimeoutPolicy{}

	return wireAdapter{
		ID:         adapterID,
		Name:       p.Name + " " + protocol + " Adapter",
		Version:    1,
		SDKKind:    sdkKind,
		Protocol:   protocol,
		Auth:       auth,
		Capability: capability,
		Thinking:   thinking,
		Request: wireRequest{
			AllowedHeaders: allowedHeaders,
			AllowedQuery:   []string{},
			Rules:          []wireRequestRule{},
		},
		Response: wireResponse{Rules: responseRules},
		Retry:    retry,
		Timeout:  timeout,
	}
}
