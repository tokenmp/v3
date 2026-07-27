package server

import (
	"encoding/json"
	"fmt"
	"sort"
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
	ID               string            `json:"ID"`
	DisplayName      string            `json:"DisplayName"`
	Capabilities     []string          `json:"Capabilities"`
	Thinking         wireModelThinking `json:"Thinking"`
	FallbackModelIDs []string          `json:"FallbackModelIDs,omitempty"`
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
	now := time.Now().UTC()
	revision := fmt.Sprintf("compile-%d", now.Unix())

	// ---- AutoModelIDs ----
	autoModelIDs := make([]string, 0, len(models))
	for _, m := range models {
		if m.Status == "active" {
			autoModelIDs = append(autoModelIDs, m.ID)
		}
	}
	sort.Strings(autoModelIDs)

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
			ID:           m.ID,
			DisplayName:  m.DisplayName,
			Capabilities: caps,
			Thinking:     thinking,
		}
	}

	// ---- Adapters: build from DB adapters + auto-generate for providers without one ----
	// Index DB adapters by (sdk_kind, protocol) for provider lookup.
	dbAdapterBySDKProto := make(map[string]repository.Adapter) // key = "sdk_kind|protocol"
	for _, a := range adapters {
		if a.Status == "deleted" {
			continue
		}
		key := a.SDKKind + "|" + a.Protocol
		dbAdapterBySDKProto[key] = a
	}

	// Track which providers have a DB adapter (via provider_id or sdk_kind+protocol match).
	providerHasDBAdapter := make(map[string]bool)
	for _, a := range adapters {
		if a.Status == "deleted" {
			continue
		}
		if a.ProviderID != nil && *a.ProviderID != "" {
			providerHasDBAdapter[*a.ProviderID] = true
		}
	}

	wireAdapters := make(map[string]wireAdapter)

	// First, include DB adapters.
	for _, a := range adapters {
		if a.Status == "deleted" {
			continue
		}
		wa, err := dbAdapterToWire(a, credentialsByProvider)
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", a.ID, err)
		}
		wireAdapters[a.ID] = wa
	}

	// Auto-generate adapters for providers that don't have a DB adapter.
	for _, p := range providers {
		if p.Status == "deleted" {
			continue
		}
		if providerHasDBAdapter[p.ID] {
			continue
		}
		adapterID := "adapter-" + p.ID
		if _, exists := wireAdapters[adapterID]; exists {
			continue
		}
		wa := autoGenerateAdapter(p, credentialsByProvider)
		wireAdapters[adapterID] = wa
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

	// ---- Routes ----
	wireRoutes := make([]wireRoute, 0, len(routes))
	for _, rm := range routes {
		if rm.Status == "deleted" {
			continue
		}

		// Determine adapter ID.
		adapterID := "adapter-" + rm.ProviderID
		if rm.AdapterID != nil && *rm.AdapterID != "" {
			adapterID = *rm.AdapterID
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

		wireRoutes = append(wireRoutes, wireRoute{
			ID:            rm.ID,
			ModelID:       rm.ModelID,
			ProviderID:    rm.ProviderID,
			AdapterID:     adapterID,
			UpstreamModel: rm.UpstreamModel,
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

// autoGenerateAdapter creates a default adapter for a provider that has no
// DB adapter record. The adapter ID is "adapter-<provider_id>".
func autoGenerateAdapter(p repository.Provider, credentialsByProvider map[string][]repository.UpstreamCredential) wireAdapter {
	adapterID := "adapter-" + p.ID

	// Determine auth config based on SDK kind and protocol.
	auth := wireAuth{}
	capability := wireCapability{}
	allowedHeaders := []string{"Content-Type", "Accept"}
	var responseRules []wireResponseRule

	switch p.SDKKind {
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

	// Retry config: default values.
	mta := 3
	msta := 2
	retry := wireRetryPolicy{
		MaxTotalAttempts:      &mta,
		MaxSameTargetAttempts: &msta,
		MaxTotalDuration:      "45s",
		Backoff:               "500ms",
		Rules: []wireRetryRule{
			{ID: "retry-429", Priority: 10, HTTPStatuses: []int{429}, Action: "next_credential"},
			{ID: "retry-5xx", Priority: 20, HTTPStatuses: []int{500, 502, 503, 504}, Action: "next_route"},
		},
	}

	// Anthropic retry includes 529.
	if p.SDKKind == "anthropic" {
		retry.Rules = []wireRetryRule{
			{ID: "retry-429", Priority: 10, HTTPStatuses: []int{429}, Action: "next_credential"},
			{ID: "retry-5xx", Priority: 20, HTTPStatuses: []int{500, 502, 503, 529}, Action: "next_route"},
		}
	}

	timeout := wireTimeoutPolicy{
		RequestTimeout:    "120s",
		TTFTTimeout:       "20s",
		StreamIdleTimeout: "30s",
		StreamMaxLifetime: "300s",
		RetryBackoff:      "500ms",
	}

	return wireAdapter{
		ID:         adapterID,
		Name:       p.Name + " Adapter",
		Version:    1,
		SDKKind:    p.SDKKind,
		Protocol:   p.Protocol,
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
