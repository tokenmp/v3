package adapter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultMaxTotalAttempts      = 3
	DefaultMaxSameTargetAttempts = 2
	DefaultMaxTotalDuration      = 90 * time.Second
	HardMaxTotalAttempts         = 10
	HardMaxSameTargetAttempts    = 5
	HardMaxTotalDuration         = 90 * time.Second
	DefaultRequestTimeout        = 2 * time.Minute
	DefaultTTFTTimeout           = 45 * time.Second
	DefaultStreamIdleTimeout     = 30 * time.Second
	DefaultStreamMaxLifetime     = 10 * time.Minute
	DefaultRetryBackoff          = 200 * time.Millisecond
	HardMaxRequestTimeout        = 30 * time.Minute
	HardMaxTTFTTimeout           = 5 * time.Minute
	HardMaxStreamIdleTimeout     = 5 * time.Minute
	HardMaxStreamMaxLifetime     = time.Hour
	HardMaxRetryBackoff          = time.Minute
	maxJSONPointerLength         = 1024
	maxJSONPointerDepth          = 32
	maxRequestRules              = 256
	maxResponseRules             = 256
	maxResponseMatchers          = 64
	maxResponseTokenBytes        = 128
	maxResponseMessageContains   = 256
	maxResponseMessageBytes      = 512
	maxDSLLiteralBytes           = 64 << 10
	maxEnumEntries               = 256
	maxDSLJSONDepth              = 64
	maxDSLJSONNodes              = 10000
	maxFallbackNodes             = 10000
)

// ConfigInput is the raw, package-local representation passed by a snapshot
// decoder. Keeping this type independent prevents adapter from importing its
// caller and preserves the one-way snapshot -> adapter dependency.
type ConfigInput struct {
	Revision  string
	Models    map[string]ModelInput
	Providers map[string]ProviderInput
	Routes    []RouteInput
	Adapters  map[string]AdapterConfig
	Global    GlobalInput
}
type GlobalInput struct {
	Retry   RetryPolicy
	Timeout TimeoutPolicy
	// AutoModelIDs is an ordered list of configured models eligible for the
	// reserved "auto" model selection.
	AutoModelIDs []string
}
type ModelInput struct {
	ID               string
	Capabilities     []Capability
	Thinking         ThinkingInput
	FallbackModelIDs []string
}
type ThinkingInput struct {
	Supported                      bool
	DefaultEffort, MaxEffort       ThinkingEffort
	MinBudgetToken, MaxBudgetToken int
}
type ProviderInput struct {
	ID, Name, BaseURL, Selector string
	SDKKind                     SDKKind
	Protocol                    Protocol
	Retry                       RetryPolicy
	Timeout                     TimeoutPolicy
}
type RouteInput struct {
	ID, ModelID, ProviderID, AdapterID, UpstreamModel string
	Priority                                          int
	Enabled                                           bool
	Protocol                                          Protocol
	Retry                                             RetryPolicy
	Timeout                                           TimeoutPolicy
	FallbackRouteIDs                                  []string
	RouteGroup                                        string
	Credentials                                       []CredentialInput
}
type CredentialInput struct {
	ID, CredentialRef string
	Priority          int
	Enabled           bool
}

// CompiledConfig is a normalized immutable-in-practice configuration value.
// Use CloneCompiledConfig before retaining a caller-visible value.
type CompiledConfig struct {
	Revision     string
	AutoModelIDs []string
	Models       map[string]CompiledModel
	Providers    map[string]CompiledProvider
	Adapters     map[string]CompiledAdapter
	Routes       []CompiledRoute
}
type CompiledModel struct {
	ID               string
	Capabilities     []Capability
	Thinking         ThinkingInput
	FallbackModelIDs []string
}
type CompiledProvider struct {
	ID, Name, BaseURL, Selector string
	SDKKind                     SDKKind
	Protocol                    Protocol
	Retry                       CompiledRetry
	Timeout                     CompiledTimeout
}
type CompiledRoute struct {
	ID, ModelID, ProviderID, AdapterID, UpstreamModel string
	Priority                                          int
	Enabled                                           bool
	Protocol                                          Protocol
	Retry                                             CompiledRetry
	Timeout                                           CompiledTimeout
	FallbackRouteIDs                                  []string
	RouteGroup                                        string
	Credentials                                       []CompiledCredential
}
type CompiledCredential struct {
	ID, CredentialRef string
	Priority          int
	Enabled           bool
}
type CompiledAdapter struct {
	ID, Name      string
	Version       int
	SDKKind       SDKKind
	Protocol      Protocol
	Auth          AuthRule
	Capability    CapabilityPolicy
	Thinking      ThinkingPolicy
	Request       RequestPolicy
	ResponseRules []ResponseRule
	Retry         CompiledRetry
	Timeout       CompiledTimeout
}
type CompiledRetry struct {
	MaxTotalAttempts, MaxSameTargetAttempts int
	MaxTotalDuration, Backoff               time.Duration
	Rules                                   []RetryRule
	set                                     bool
}
type CompiledTimeout struct{ Request, TTFT, StreamIdle, StreamMaxLifetime, RetryBackoff time.Duration }

// Compile validates and normalizes a decoded configuration snapshot.
func Compile(in ConfigInput) (CompiledConfig, error) {
	if strings.TrimSpace(in.Revision) == "" {
		return CompiledConfig{}, fmt.Errorf("revision is required")
	}
	globalRetry, err := compileRetry(in.Global.Retry, CompiledRetry{})
	if err != nil {
		return CompiledConfig{}, fmt.Errorf("global retry: %w", err)
	}
	globalTimeout, err := compileTimeout(in.Global.Timeout, CompiledTimeout{})
	if err != nil {
		return CompiledConfig{}, fmt.Errorf("global timeout: %w", err)
	}
	out := CompiledConfig{Revision: in.Revision, AutoModelIDs: append([]string(nil), in.Global.AutoModelIDs...), Models: make(map[string]CompiledModel, len(in.Models)), Providers: make(map[string]CompiledProvider, len(in.Providers)), Adapters: make(map[string]CompiledAdapter, len(in.Adapters))}
	modelIDs := map[string]bool{}
	for key, m := range in.Models {
		if modelIDs[m.ID] {
			return CompiledConfig{}, fmt.Errorf("duplicate model ID %q", m.ID)
		}
		modelIDs[m.ID] = true
		if key == "" || m.ID == "" || key != m.ID {
			return CompiledConfig{}, fmt.Errorf("model key/id mismatch %q/%q", key, m.ID)
		}
		if m.ID == "auto" {
			return CompiledConfig{}, fmt.Errorf("model ID %q is reserved", m.ID)
		}
		if err := capabilities(m.Capabilities); err != nil {
			return CompiledConfig{}, fmt.Errorf("model %q: %w", key, err)
		}
		if err := thinkingInput(m.Thinking); err != nil {
			return CompiledConfig{}, fmt.Errorf("model %q: %w", key, err)
		}
		out.Models[key] = CompiledModel{ID: m.ID, Capabilities: append([]Capability(nil), m.Capabilities...), Thinking: m.Thinking, FallbackModelIDs: append([]string(nil), m.FallbackModelIDs...)}
	}
	if err := validateModelReferences(out.Models, in.Global.AutoModelIDs); err != nil {
		return CompiledConfig{}, err
	}
	providerNames := map[string]bool{}
	providerSelectors := map[string]bool{}
	for key, p := range in.Providers {
		if providerNames[p.Name] {
			return CompiledConfig{}, fmt.Errorf("duplicate provider name %q", p.Name)
		}
		providerNames[p.Name] = true
		if key == "" || p.ID == "" || key != p.ID || strings.TrimSpace(p.Name) == "" || !p.SDKKind.Valid() || !p.Protocol.Valid() {
			return CompiledConfig{}, fmt.Errorf("invalid provider %q", key)
		}
		selector := p.Selector
		if selector == "" {
			selector = p.ID
		}
		if !safeSegment(selector) || providerSelectors[selector] {
			return CompiledConfig{}, fmt.Errorf("invalid or duplicate provider selector %q", selector)
		}
		providerSelectors[selector] = true
		baseURL, err := url.Parse(p.BaseURL)
		if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil {
			return CompiledConfig{}, fmt.Errorf("provider %q has invalid base URL", key)
		}
		r, err := compileRetry(p.Retry, globalRetry)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("provider %q retry: %w", key, err)
		}
		t, err := compileTimeout(p.Timeout, globalTimeout)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("provider %q timeout: %w", key, err)
		}
		out.Providers[key] = CompiledProvider{ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, Selector: selector, SDKKind: p.SDKKind, Protocol: p.Protocol, Retry: r, Timeout: t}
	}
	adapterNames := map[string]bool{}
	for key, raw := range in.Adapters {
		if adapterNames[raw.Name] {
			return CompiledConfig{}, fmt.Errorf("duplicate adapter name %q", raw.Name)
		}
		adapterNames[raw.Name] = true
		if key == "" || raw.ID == "" || key != raw.ID || strings.TrimSpace(raw.Name) == "" || raw.Version < 1 || !raw.SDKKind.Valid() || !raw.Protocol.Valid() {
			return CompiledConfig{}, fmt.Errorf("invalid adapter %q", key)
		}
		if err := validateAdapter(raw); err != nil {
			return CompiledConfig{}, fmt.Errorf("adapter %q: %w", key, err)
		}
		r, err := compileRetry(raw.Retry, globalRetry)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("adapter %q retry: %w", key, err)
		}
		t, err := compileTimeout(raw.Timeout, globalTimeout)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("adapter %q timeout: %w", key, err)
		}
		out.Adapters[key] = CompiledAdapter{ID: raw.ID, Name: raw.Name, Version: raw.Version, SDKKind: raw.SDKKind, Protocol: raw.Protocol, Auth: cloneAuth(raw.Auth), Capability: cloneCapability(raw.Capability), Thinking: cloneThinking(raw.Thinking), Request: cloneRequest(raw.Request), ResponseRules: cloneResponseRules(sortedResponse(raw.Response.Rules)), Retry: r, Timeout: t}
	}
	seen := map[string]bool{}
	for _, route := range in.Routes {
		if route.ID == "" || seen[route.ID] {
			return CompiledConfig{}, fmt.Errorf("duplicate or empty route ID %q", route.ID)
		}
		seen[route.ID] = true
		m, ok := out.Models[route.ModelID]
		if !ok {
			return CompiledConfig{}, fmt.Errorf("route %q references unknown model %q", route.ID, route.ModelID)
		}
		p, ok := out.Providers[route.ProviderID]
		if !ok {
			return CompiledConfig{}, fmt.Errorf("route %q references unknown provider %q", route.ID, route.ProviderID)
		}
		a, ok := out.Adapters[route.AdapterID]
		if !ok {
			return CompiledConfig{}, fmt.Errorf("route %q references unknown adapter %q", route.ID, route.AdapterID)
		}
		if strings.TrimSpace(route.UpstreamModel) == "" || !route.Protocol.Valid() || route.Protocol != p.Protocol || route.Protocol != a.Protocol || p.SDKKind != a.SDKKind {
			return CompiledConfig{}, fmt.Errorf("route %q has incompatible provider/adapter/protocol", route.ID)
		}
		if err := compatible(m, a); err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q: %w", route.ID, err)
		}
		// Policies always merge code defaults -> global -> adapter -> provider -> route.
		adapterRetry, err := compileRetry(in.Adapters[route.AdapterID].Retry, globalRetry)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q adapter retry: %w", route.ID, err)
		}
		adapterTimeout, err := compileTimeout(in.Adapters[route.AdapterID].Timeout, globalTimeout)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q adapter timeout: %w", route.ID, err)
		}
		providerRetry, err := compileRetry(in.Providers[route.ProviderID].Retry, adapterRetry)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q provider retry: %w", route.ID, err)
		}
		providerTimeout, err := compileTimeout(in.Providers[route.ProviderID].Timeout, adapterTimeout)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q provider timeout: %w", route.ID, err)
		}
		r, err := compileRetry(route.Retry, providerRetry)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q retry: %w", route.ID, err)
		}
		// An explicit zero at adapter or provider level is an opt-out, not a
		// value a more-specific layer may re-enable. Global remains a default
		// layer and is intentionally overridable.
		if retriesDisabled(in.Adapters[route.AdapterID].Retry) || retriesDisabled(in.Providers[route.ProviderID].Retry) {
			r.MaxTotalAttempts = 0
			r.MaxSameTargetAttempts = 0
		}
		t, err := compileTimeout(route.Timeout, providerTimeout)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q timeout: %w", route.ID, err)
		}
		credentials, err := compileCredentials(route, a.Auth)
		if err != nil {
			return CompiledConfig{}, fmt.Errorf("route %q: %w", route.ID, err)
		}
		out.Routes = append(out.Routes, CompiledRoute{ID: route.ID, ModelID: route.ModelID, ProviderID: route.ProviderID, AdapterID: route.AdapterID, UpstreamModel: route.UpstreamModel, Priority: route.Priority, Enabled: route.Enabled, Protocol: route.Protocol, Retry: r, Timeout: t, FallbackRouteIDs: append([]string(nil), route.FallbackRouteIDs...), RouteGroup: route.RouteGroup, Credentials: credentials})
	}
	if err := validateRouteCredentials(out.Routes); err != nil {
		return CompiledConfig{}, err
	}
	if err := validateFallbacks(out.Routes); err != nil {
		return CompiledConfig{}, err
	}
	sort.SliceStable(out.Routes, func(i, j int) bool {
		if out.Routes[i].Priority != out.Routes[j].Priority {
			return out.Routes[i].Priority < out.Routes[j].Priority
		}
		if out.Routes[i].RouteGroup != out.Routes[j].RouteGroup {
			return out.Routes[i].RouteGroup < out.Routes[j].RouteGroup
		}
		return out.Routes[i].ID < out.Routes[j].ID
	})
	return out, nil
}

const legacyCredentialIDPrefix = "legacy-route-sha256-"

func compileCredentials(route RouteInput, auth AuthRule) ([]CompiledCredential, error) {
	if auth.Kind == AuthNone {
		if len(route.Credentials) != 0 || auth.CredentialRef != "" {
			return nil, fmt.Errorf("auth none must not configure credentials")
		}
		return nil, nil
	}
	if len(route.Credentials) != 0 && auth.CredentialRef != "" {
		return nil, fmt.Errorf("explicit credentials conflict with legacy adapter credential")
	}
	credentials := route.Credentials
	legacy := len(credentials) == 0 && auth.CredentialRef != ""
	if legacy {
		credentials = []CredentialInput{{ID: legacyCredentialID(route.ID), CredentialRef: auth.CredentialRef, Enabled: true}}
	}
	if len(credentials) == 0 {
		return nil, fmt.Errorf("authenticated route requires an enabled credential")
	}
	seenRefs := map[string]bool{}
	out := make([]CompiledCredential, 0, len(credentials))
	enabled := false
	for _, credential := range credentials {
		if !safeSegment(credential.ID) {
			return nil, fmt.Errorf("credential %q: invalid credential ID", credential.ID)
		}
		if !legacy && strings.HasPrefix(credential.ID, legacyCredentialIDPrefix) {
			return nil, fmt.Errorf("credential %q: legacy credential ID namespace is reserved", credential.ID)
		}
		if !safeCredentialRef(credential.CredentialRef) {
			return nil, fmt.Errorf("credential %q: invalid credential reference", credential.ID)
		}
		if seenRefs[credential.CredentialRef] {
			return nil, fmt.Errorf("credential %q: duplicate credential reference", credential.ID)
		}
		seenRefs[credential.CredentialRef] = true
		enabled = enabled || credential.Enabled
		out = append(out, CompiledCredential{ID: credential.ID, CredentialRef: credential.CredentialRef, Priority: credential.Priority, Enabled: credential.Enabled})
	}
	if !enabled {
		return nil, fmt.Errorf("authenticated route requires an enabled credential")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func validateModelReferences(models map[string]CompiledModel, auto []string) error {
	seenAuto := map[string]bool{}
	for _, id := range auto {
		if id == "auto" || seenAuto[id] {
			return fmt.Errorf("invalid or duplicate auto model ID %q", id)
		}
		if _, ok := models[id]; !ok {
			return fmt.Errorf("unknown auto model %q", id)
		}
		seenAuto[id] = true
	}
	state := map[string]uint8{}
	for id, model := range models {
		seen := map[string]bool{}
		for _, fallback := range model.FallbackModelIDs {
			if fallback == "auto" || seen[fallback] {
				return fmt.Errorf("model %q has invalid or duplicate fallback model %q", id, fallback)
			}
			if _, ok := models[fallback]; !ok {
				return fmt.Errorf("model %q references unknown fallback model %q", id, fallback)
			}
			seen[fallback] = true
		}
	}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("fallback model cycle at %q", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, fallback := range models[id].FallbackModelIDs {
			if err := visit(fallback); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range models {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func legacyCredentialID(routeID string) string {
	sum := sha256.Sum256([]byte(routeID))
	return legacyCredentialIDPrefix + hex.EncodeToString(sum[:])
}

func safeSegment(v string) bool {
	if v == "" || v == "." || v == ".." {
		return false
	}
	for _, r := range v {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
func safeCredentialRef(v string) bool {
	u, err := url.ParseRequestURI(v)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(v, "\r\n") {
		return false
	}
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return false
	}
	for _, segment := range segments {
		if !safeSegment(segment) {
			return false
		}
	}
	return true
}

func retriesDisabled(raw RetryPolicy) bool {
	return raw.MaxTotalAttempts != nil && *raw.MaxTotalAttempts == 0
}

func compileRetry(raw RetryPolicy, parent CompiledRetry) (CompiledRetry, error) {
	o := parent
	if !o.set {
		o.MaxTotalAttempts = DefaultMaxTotalAttempts
		o.MaxSameTargetAttempts = DefaultMaxSameTargetAttempts
		o.MaxTotalDuration = DefaultMaxTotalDuration
		o.Backoff = DefaultRetryBackoff
	}
	if raw.MaxTotalAttempts != nil {
		o.MaxTotalAttempts = *raw.MaxTotalAttempts
	}
	if raw.MaxSameTargetAttempts != nil {
		o.MaxSameTargetAttempts = *raw.MaxSameTargetAttempts
	}
	if o.MaxTotalAttempts < 0 || o.MaxTotalAttempts > HardMaxTotalAttempts || o.MaxSameTargetAttempts < 0 || o.MaxSameTargetAttempts > HardMaxSameTargetAttempts {
		return o, fmt.Errorf("attempts exceed bounds")
	}
	if o.MaxTotalAttempts == 0 {
		o.MaxSameTargetAttempts = 0
	}
	if o.MaxSameTargetAttempts > o.MaxTotalAttempts {
		return o, fmt.Errorf("same-target attempts exceed total")
	}
	var err error
	if raw.MaxTotalDuration != "" {
		o.MaxTotalDuration, err = parse(raw.MaxTotalDuration)
		if err != nil {
			return o, err
		}
	}
	if raw.Backoff != "" {
		o.Backoff, err = parse(raw.Backoff)
		if err != nil {
			return o, err
		}
	}
	if !o.set && o.MaxTotalDuration == 0 {
		o.MaxTotalDuration = DefaultMaxTotalDuration
	}
	if !o.set && o.Backoff == 0 {
		o.Backoff = DefaultRetryBackoff
	}
	if o.MaxTotalDuration > HardMaxTotalDuration {
		return o, fmt.Errorf("max total duration exceeds hard limit")
	}
	if raw.Rules != nil {
		o.Rules = cloneRetryRules(sortedRetry(raw.Rules))
	}
	if err = validateRetryRules(o.Rules); err != nil {
		return o, err
	}
	o.set = true
	return o, nil
}
func compileTimeout(raw TimeoutPolicy, parent CompiledTimeout) (CompiledTimeout, error) {
	o := parent
	if o.Request == 0 {
		o = CompiledTimeout{Request: DefaultRequestTimeout, TTFT: DefaultTTFTTimeout, StreamIdle: DefaultStreamIdleTimeout, StreamMaxLifetime: DefaultStreamMaxLifetime, RetryBackoff: DefaultRetryBackoff}
	}
	var err error
	set := func(v RawDuration, d *time.Duration) error {
		if v == "" {
			return nil
		}
		*d, err = parse(v)
		return err
	}
	if err = set(raw.RequestTimeout, &o.Request); err != nil {
		return o, err
	}
	if err = set(raw.TTFTTimeout, &o.TTFT); err != nil {
		return o, err
	}
	if err = set(raw.StreamIdleTimeout, &o.StreamIdle); err != nil {
		return o, err
	}
	if err = set(raw.StreamMaxLifetime, &o.StreamMaxLifetime); err != nil {
		return o, err
	}
	if err = set(raw.RetryBackoff, &o.RetryBackoff); err != nil {
		return o, err
	}
	if o.TTFT > o.Request || o.StreamIdle > o.StreamMaxLifetime {
		return o, fmt.Errorf("timeouts must satisfy TTFT <= request and idle <= stream lifetime")
	}
	if o.Request > HardMaxRequestTimeout || o.TTFT > HardMaxTTFTTimeout || o.StreamIdle > HardMaxStreamIdleTimeout || o.StreamMaxLifetime > HardMaxStreamMaxLifetime || o.RetryBackoff > HardMaxRetryBackoff {
		return o, fmt.Errorf("timeout exceeds hard limit")
	}
	return o, nil
}
func parse(v RawDuration) (time.Duration, error) {
	d, e := time.ParseDuration(string(v))
	if e != nil || d <= 0 {
		if e == nil {
			e = fmt.Errorf("must be positive")
		}
		return 0, e
	}
	return d, nil
}

func validateAdapter(a AdapterConfig) error {
	if !a.Auth.Kind.Valid() {
		return fmt.Errorf("invalid auth kind")
	}
	switch a.Auth.Kind {
	case AuthNone:
		if a.Auth.Header != "" || a.Auth.Query != "" || a.Auth.Prefix != "" || a.Auth.CredentialRef != "" {
			return fmt.Errorf("auth none has inapplicable fields")
		}
	case AuthBearerHeader, AuthAPIKeyHeader:
		if !rfcToken(a.Auth.Header) || compilerForbiddenName(a.Auth.Header) || authDeniedHeader(a.Auth.Header) {
			return fmt.Errorf("invalid auth header")
		}
		if a.Auth.Query != "" || hasCTL(a.Auth.Prefix) {
			return fmt.Errorf("auth has inapplicable or unsafe fields")
		}
	case AuthAPIKeyQuery:
		if !safeSegment(a.Auth.Query) || compilerForbiddenName(a.Auth.Query) {
			return fmt.Errorf("invalid auth query")
		}
		if a.Auth.Header != "" || a.Auth.Prefix != "" {
			return fmt.Errorf("auth has inapplicable fields")
		}
	}
	if err := capabilities(a.Capability.Require); err != nil {
		return err
	}
	if err := capabilities(a.Capability.Deny); err != nil {
		return err
	}
	for _, x := range a.Capability.Require {
		for _, y := range a.Capability.Deny {
			if x == y {
				return fmt.Errorf("capability %q both required and denied", x)
			}
		}
	}
	if err := thinkingPolicy(a.Thinking); err != nil {
		return err
	}
	if err := requestPolicy(a.Request); err != nil {
		return err
	}
	if err := responseRules(a.Response.Rules); err != nil {
		return err
	}
	return nil
}
func capabilities(xs []Capability) error {
	seen := map[Capability]bool{}
	for _, x := range xs {
		if !x.Valid() || seen[x] {
			return fmt.Errorf("invalid or duplicate capability %q", x)
		}
		seen[x] = true
	}
	return nil
}
func thinkingInput(t ThinkingInput) error {
	if !t.Supported {
		return nil
	}
	if !t.DefaultEffort.Valid() || !t.MaxEffort.Valid() || rank(t.DefaultEffort) > rank(t.MaxEffort) || t.MinBudgetToken < 0 || t.MaxBudgetToken < t.MinBudgetToken {
		return fmt.Errorf("invalid thinking limits")
	}
	return nil
}
func thinkingPolicy(t ThinkingPolicy) error {
	if !t.Supported {
		return nil
	}
	if !t.DefaultEffort.Valid() {
		return fmt.Errorf("invalid default thinking effort")
	}
	for _, e := range []ThinkingEffort{ThinkingNone, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax} {
		v, ok := t.EffortMapping[e]
		if !ok || !v.Valid() {
			return fmt.Errorf("missing mapping for %q", e)
		}
	}
	if t.MinBudgetToken < 0 || t.MaxBudgetToken < t.MinBudgetToken {
		return fmt.Errorf("invalid thinking budget")
	}
	for _, v := range t.BudgetMapping {
		if v < t.MinBudgetToken || v > t.MaxBudgetToken {
			return fmt.Errorf("budget outside bounds")
		}
	}
	// Runtime resolves a request effort through EffortMapping, then looks up
	// its budget by that effective effort. A positive minimum cannot silently
	// default to zero, so every effective effort must be explicitly mapped.
	// With a zero minimum, an omitted mapping intentionally means budget zero.
	if t.MinBudgetToken > 0 {
		for _, effective := range t.EffortMapping {
			if _, ok := t.BudgetMapping[effective]; !ok {
				return fmt.Errorf("missing budget mapping for effective effort")
			}
		}
	}
	return nil
}
func requestPolicy(p RequestPolicy) error {
	if len(p.Rules) > maxRequestRules {
		return fmt.Errorf("request rule limit exceeded")
	}
	headers, err := validatedHeaders(p.AllowedHeaders)
	if err != nil {
		return err
	}
	query, err := validatedQuery(p.AllowedQuery)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	var reads, writes []string
	for _, r := range p.Rules {
		if !safeSegment(r.ID) || ids[r.ID] || !r.Action.Valid() {
			return fmt.Errorf("invalid or duplicate request rule %q", r.ID)
		}
		if r.ValueRef != "" {
			return fmt.Errorf("request rule %q uses unsupported value reference", r.ID)
		}
		ids[r.ID] = true
		write := func(path string, allowAppend bool) error {
			if err := validateWritablePath(path); err != nil {
				return err
			}
			if !allowAppend && pointerTerminal(path) == "-" {
				return fmt.Errorf("invalid JSON pointer target")
			}
			for _, prior := range writes {
				if pointerOverlaps(path, prior) {
					return fmt.Errorf("request rule write conflict")
				}
			}
			for _, prior := range reads {
				if pointerOverlaps(path, prior) {
					return fmt.Errorf("request rule read/write conflict")
				}
			}
			writes = append(writes, path)
			return nil
		}
		read := func(path string) error {
			if err := validateWritablePath(path); err != nil {
				return err
			}
			if pointerTerminal(path) == "-" {
				return fmt.Errorf("invalid JSON pointer source")
			}
			for _, prior := range writes {
				if pointerOverlaps(path, prior) {
					return fmt.Errorf("request rule read/write conflict")
				}
			}
			reads = append(reads, path)
			return nil
		}
		switch r.Action {
		case RequestSetHeader:
			if r.Path != "" || r.From != "" || r.To != "" || len(r.EnumMap) != 0 || r.Min != nil || r.Max != nil {
				return fmt.Errorf("header action has inapplicable fields")
			}
			if !rfcToken(r.Name) || compilerForbiddenName(r.Name) || deniedHeader(r.Name) || !headers[canonicalHeader(r.Name)] {
				return fmt.Errorf("invalid or unallowlisted header")
			}
			if err := validateJSONStringLiteral(r.Value); err != nil {
				return fmt.Errorf("invalid header value")
			}
		case RequestSetQuery:
			if r.Path != "" || r.From != "" || r.To != "" || len(r.EnumMap) != 0 || r.Min != nil || r.Max != nil {
				return fmt.Errorf("query action has inapplicable fields")
			}
			if !safeSegment(r.Name) || compilerForbiddenName(r.Name) || !query[r.Name] {
				return fmt.Errorf("invalid or unallowlisted query")
			}
			if err := validateJSONStringLiteral(r.Value); err != nil {
				return fmt.Errorf("invalid query value")
			}
		case RequestSet:
			if r.From != "" || r.To != "" || r.Name != "" || len(r.EnumMap) != 0 || r.Min != nil || r.Max != nil {
				return fmt.Errorf("set action has inapplicable fields")
			}
			if err := validateJSONLiteral(r.Value); err != nil {
				return fmt.Errorf("invalid set value")
			}
			if err := write(r.Path, true); err != nil {
				return err
			}
		case RequestCopy:
			if r.Path != "" || r.Name != "" || len(r.Value) != 0 || len(r.EnumMap) != 0 || r.Min != nil || r.Max != nil {
				return fmt.Errorf("copy action has inapplicable fields")
			}
			if err := read(r.From); err != nil {
				return err
			}
			if err := write(r.To, true); err != nil {
				return err
			}
		case RequestRename:
			if r.Path != "" || r.Name != "" || len(r.Value) != 0 || len(r.EnumMap) != 0 || r.Min != nil || r.Max != nil {
				return fmt.Errorf("rename action has inapplicable fields")
			}
			if err := validateWritablePath(r.From); err != nil {
				return err
			}
			if pointerTerminal(r.From) == "-" {
				return fmt.Errorf("invalid JSON pointer source")
			}
			if err := write(r.From, false); err != nil {
				return err
			}
			if err := write(r.To, true); err != nil {
				return err
			}
		case RequestRemove:
			if r.From != "" || r.To != "" || r.Name != "" || len(r.Value) != 0 || len(r.EnumMap) != 0 || r.Min != nil || r.Max != nil {
				return fmt.Errorf("remove action has inapplicable fields")
			}
			if err := write(r.Path, false); err != nil {
				return err
			}
		case RequestMapEnum:
			if r.From != "" || r.To != "" || r.Name != "" || len(r.Value) != 0 || r.Min != nil || r.Max != nil || len(r.EnumMap) == 0 || len(r.EnumMap) > maxEnumEntries {
				return fmt.Errorf("invalid enum map")
			}
			if err := write(r.Path, false); err != nil {
				return err
			}
		case RequestClampNumber:
			if r.From != "" || r.To != "" || r.Name != "" || len(r.Value) != 0 || len(r.EnumMap) != 0 || r.Min == nil || r.Max == nil || math.IsNaN(*r.Min) || math.IsNaN(*r.Max) || math.IsInf(*r.Min, 0) || math.IsInf(*r.Max, 0) || *r.Min > *r.Max {
				return fmt.Errorf("invalid clamp")
			}
			if err := write(r.Path, false); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateJSONPointer(p string) error {
	if len(p) == 0 || p == "/" || len(p) > maxJSONPointerLength || p[0] != '/' {
		return fmt.Errorf("invalid JSON pointer")
	}
	parts := strings.Split(p[1:], "/")
	if len(parts) > maxJSONPointerDepth {
		return fmt.Errorf("JSON pointer too deep")
	}
	for _, part := range parts {
		decoded, err := unescapeCompilerPointerToken(part)
		if err != nil {
			return err
		}
		if compilerForbiddenName(decoded) {
			return fmt.Errorf("unsafe JSON pointer token")
		}
	}
	return nil
}
func unescapeCompilerPointerToken(token string) (string, error) {
	var out strings.Builder
	out.Grow(len(token))
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			out.WriteByte(token[i])
			continue
		}
		if i+1 == len(token) || (token[i+1] != '0' && token[i+1] != '1') {
			return "", fmt.Errorf("invalid JSON pointer escape")
		}
		if token[i+1] == '0' {
			out.WriteByte('~')
		} else {
			out.WriteByte('/')
		}
		i++
	}
	return out.String(), nil
}
func compilerForbiddenName(v string) bool {
	return v == "__proto__" || v == "prototype" || v == "constructor"
}
func validateWritablePath(p string) error {
	if err := validateJSONPointer(p); err != nil {
		return err
	}
	if protectedPath(p) {
		return fmt.Errorf("protected request path %q", p)
	}
	return nil
}
func protectedPath(p string) bool {
	for _, q := range []string{"/model", "/messages", "/input", "/prompt"} {
		if p == q || strings.HasPrefix(p, q+"/") {
			return true
		}
	}
	return false
}
func pointerTerminal(p string) string {
	parts := strings.Split(p[1:], "/")
	return parts[len(parts)-1]
}
func pointerOverlaps(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func canonicalHeader(v string) string { return strings.ToLower(v) }
func rfcToken(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}
func hasCTL(v string) bool {
	for _, r := range v {
		if r <= 0x1f || r == 0x7f {
			return true
		}
	}
	return false
}
func validatedHeaders(xs []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, x := range xs {
		if !rfcToken(x) || compilerForbiddenName(x) || out[canonicalHeader(x)] {
			return nil, fmt.Errorf("invalid allowed header")
		}
		out[canonicalHeader(x)] = true
	}
	return out, nil
}
func validatedQuery(xs []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, x := range xs {
		if !safeSegment(x) || compilerForbiddenName(x) || out[x] {
			return nil, fmt.Errorf("invalid allowed query")
		}
		out[x] = true
	}
	return out, nil
}
func authDeniedHeader(v string) bool {
	n := canonicalHeader(v)
	return n == "host" || n == "content-length" || strings.HasPrefix(n, "proxy-") || strings.HasPrefix(n, "forwarded") || n == "forwarded" || strings.HasPrefix(n, "x-forwarded-") || strings.HasPrefix(n, "x-sdk-")
}
func validateJSONLiteral(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > maxDSLLiteralBytes {
		return fmt.Errorf("invalid JSON literal")
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid JSON literal")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	nodes := 0
	if err := scanJSONValue(dec, 0, &nodes); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("invalid JSON literal")
	}
	return nil
}
func validateJSONStringLiteral(raw json.RawMessage) error {
	if err := validateJSONLiteral(raw); err != nil {
		return err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || hasCTL(s) {
		return fmt.Errorf("invalid JSON string")
	}
	return nil
}
func scanJSONValue(dec *json.Decoder, depth int, nodes *int) error {
	if depth > maxDSLJSONDepth {
		return fmt.Errorf("JSON depth limit exceeded")
	}
	*nodes++
	if *nodes > maxDSLJSONNodes {
		return fmt.Errorf("JSON node limit exceeded")
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch d := tok.(type) {
	case json.Delim:
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || compilerForbiddenName(name) {
					return fmt.Errorf("unsafe JSON object key")
				}
				if seen[name] {
					return fmt.Errorf("duplicate JSON object key")
				}
				seen[name] = true
				if err := scanJSONValue(dec, depth+1, nodes); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid JSON object")
			}
		case '[':
			for dec.More() {
				if err := scanJSONValue(dec, depth+1, nodes); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid JSON array")
			}
		default:
			return fmt.Errorf("invalid JSON literal")
		}
	}
	if number, ok := tok.(json.Number); ok {
		value, err := number.Float64()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("invalid JSON number")
		}
	}
	return nil
}
func deniedHeader(v string) bool {
	n := canonicalHeader(v)
	// Header field names are case-insensitive and deployments commonly spell
	// sensitive names with or without separators (for example X-ApiKey).
	// Normalize to alphanumeric characters before applying the credential
	// denylist so an allowlist can never bypass it with alternate punctuation.
	var normalized strings.Builder
	normalized.Grow(len(n))
	for _, r := range n {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			normalized.WriteRune(r)
		}
	}
	credentialName := normalized.String()
	return n == "host" || n == "authorization" || n == "content-length" || strings.HasPrefix(n, "proxy-") || strings.HasPrefix(n, "forwarded") || n == "forwarded" || strings.HasPrefix(n, "x-forwarded-") || strings.HasPrefix(n, "x-sdk-") || strings.Contains(credentialName, "apikey") || strings.Contains(credentialName, "accesskey") || strings.Contains(credentialName, "privatekey") || strings.Contains(credentialName, "secret") || strings.Contains(credentialName, "token") || strings.Contains(credentialName, "credential")
}

func responseRules(rs []ResponseRule) error {
	if len(rs) > maxResponseRules {
		return fmt.Errorf("response rule limit exceeded")
	}
	ids, conflicts := map[string]bool{}, map[string]bool{}
	for _, r := range rs {
		if err := validateResponseMatch(r.Match); err != nil {
			return fmt.Errorf("invalid response matcher")
		}
		if err := validateResponseOutput(r.Output); err != nil {
			return fmt.Errorf("invalid response output")
		}
		key := fmt.Sprintf("%d/%d/%s", r.Priority, responseSpecificity(r.Match), responseScope(r.Match))
		if !safeSegment(r.ID) || ids[r.ID] || conflicts[key] {
			return fmt.Errorf("invalid/conflicting response rule %q", r.ID)
		}
		ids[r.ID] = true
		conflicts[key] = true
	}
	return nil
}

func validateResponseMatch(m ResponseMatch) error {
	if responseSpecificity(m) == 0 {
		return fmt.Errorf("empty response matcher")
	}
	if err := validateResponseStatuses(m.HTTPStatuses); err != nil {
		return err
	}
	for _, values := range [][]string{m.ErrorCodes, m.ErrorTypes, m.FinishReasons, m.StreamEventTypes} {
		if err := validateResponseMatcherStrings(values, maxResponseTokenBytes); err != nil {
			return err
		}
	}
	return validateResponseMatcherStrings(m.MessageContains, maxResponseMessageContains)
}

func validateResponseStatuses(statuses []int) error {
	if len(statuses) > maxResponseMatchers {
		return fmt.Errorf("response matcher limit exceeded")
	}
	seen := make(map[int]bool, len(statuses))
	for _, status := range statuses {
		if status < 400 || status > 599 || seen[status] {
			return fmt.Errorf("invalid response HTTP status matcher")
		}
		seen[status] = true
	}
	return nil
}

func validateResponseMatcherStrings(values []string, maxBytes int) error {
	if len(values) > maxResponseMatchers {
		return fmt.Errorf("response matcher limit exceeded")
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || hasCTL(value) || seen[value] {
			return fmt.Errorf("invalid response string matcher")
		}
		seen[value] = true
	}
	return nil
}

func validateResponseOutput(output ResponseOutput) error {
	if output.HTTPStatus < 400 || output.HTTPStatus > 599 ||
		!responseToken(output.ErrorCode) || !responseToken(output.ErrorType) ||
		output.Message == "" || len(output.Message) > maxResponseMessageBytes || !utf8.ValidString(output.Message) || hasCTL(output.Message) {
		return fmt.Errorf("invalid response output")
	}
	return nil
}

func responseToken(value string) bool {
	return len(value) <= maxResponseTokenBytes && rfcToken(value)
}
func validateRetryRules(rs []RetryRule) error {
	ids, conflicts := map[string]bool{}, map[string]bool{}
	for _, r := range rs {
		key := fmt.Sprintf("%d/%d/%s", r.Priority, retrySpecificity(r), retryScope(r))
		if !safeSegment(r.ID) || ids[r.ID] || conflicts[key] || !r.Action.Valid() {
			return fmt.Errorf("invalid/conflicting retry rule %q", r.ID)
		}
		ids[r.ID] = true
		conflicts[key] = true
	}
	return nil
}
func sortedResponse(in []ResponseRule) []ResponseRule {
	o := append([]ResponseRule(nil), in...)
	sort.Slice(o, func(i, j int) bool {
		a, b := o[i], o[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		sa, sb := responseSpecificity(a.Match), responseSpecificity(b.Match)
		if sa != sb {
			return sa > sb
		}
		return a.ID < b.ID
	})
	return o
}
func sortedRetry(in []RetryRule) []RetryRule {
	o := append([]RetryRule(nil), in...)
	sort.Slice(o, func(i, j int) bool {
		a, b := o[i], o[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		sa, sb := retrySpecificity(a), retrySpecificity(b)
		if sa != sb {
			return sa > sb
		}
		return a.ID < b.ID
	})
	return o
}
func compatible(m CompiledModel, a CompiledAdapter) error {
	have := map[Capability]bool{}
	for _, x := range m.Capabilities {
		have[x] = true
	}
	for _, x := range a.Capability.Require {
		if !have[x] {
			return fmt.Errorf("missing required capability %q", x)
		}
	}
	for _, x := range a.Capability.Deny {
		if have[x] {
			return fmt.Errorf("denied capability %q present", x)
		}
	}
	if m.Thinking.Supported && !a.Thinking.Supported {
		return fmt.Errorf("model thinking unsupported by adapter")
	}
	if a.Thinking.Supported {
		if !m.Thinking.Supported || rank(a.Thinking.DefaultEffort) > rank(m.Thinking.MaxEffort) {
			return fmt.Errorf("adapter default thinking effort unsupported by model")
		}
		for _, effort := range a.Thinking.EffortMapping {
			if rank(effort) > rank(m.Thinking.MaxEffort) {
				return fmt.Errorf("adapter thinking mapping output unsupported by model")
			}
		}
		for _, budget := range a.Thinking.BudgetMapping {
			if budget < m.Thinking.MinBudgetToken || budget > m.Thinking.MaxBudgetToken {
				return fmt.Errorf("adapter thinking budget outside model bounds")
			}
		}
	}
	return nil
}
func validateRouteCredentials(routes []CompiledRoute) error {
	ids := map[string]bool{}
	for _, route := range routes {
		if route.RouteGroup != "" && !safeSegment(route.RouteGroup) {
			return fmt.Errorf("route %q has invalid route group %q", route.ID, route.RouteGroup)
		}
		for _, credential := range route.Credentials {
			if ids[credential.ID] {
				return fmt.Errorf("duplicate credential ID %q", credential.ID)
			}
			ids[credential.ID] = true
		}
	}
	return nil
}

func validateFallbacks(routes []CompiledRoute) error {
	byID := make(map[string]CompiledRoute, len(routes))
	for _, r := range routes {
		byID[r.ID] = r
	}
	state := map[string]uint8{} // 0 unseen, 1 active, 2 complete
	type frame struct {
		id   string
		next int
	}
	visited := 0
	for _, root := range routes {
		if state[root.ID] != 0 {
			continue
		}
		stack := []frame{{id: root.ID}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if state[top.id] == 0 {
				state[top.id] = 1
				visited++
				if visited > maxFallbackNodes {
					return fmt.Errorf("fallback graph exceeds node limit")
				}
			}
			r := byID[top.id]
			if top.next == len(r.FallbackRouteIDs) {
				state[top.id] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			next := r.FallbackRouteIDs[top.next]
			top.next++
			nr, ok := byID[next]
			if !ok {
				return fmt.Errorf("route %q references unknown fallback %q", r.ID, next)
			}
			if nr.ModelID != r.ModelID || nr.RouteGroup != r.RouteGroup {
				return fmt.Errorf("route %q fallback %q targets another model or route group", r.ID, next)
			}
			if state[next] == 1 {
				return fmt.Errorf("fallback cycle at route %q", next)
			}
			if state[next] == 0 {
				stack = append(stack, frame{id: next})
			}
		}
	}
	return nil
}
func responseSpecificity(m ResponseMatch) int {
	return len(m.HTTPStatuses) + len(m.ErrorCodes) + len(m.ErrorTypes) + len(m.MessageContains) + len(m.FinishReasons) + len(m.StreamEventTypes)
}
func responseScope(m ResponseMatch) string {
	return strings.Join([]string{scopeInts(m.HTTPStatuses), scopeStrings(m.ErrorCodes), scopeStrings(m.ErrorTypes), scopeStrings(m.MessageContains), scopeStrings(m.FinishReasons), scopeStrings(m.StreamEventTypes)}, "\x00")
}
func retrySpecificity(r RetryRule) int {
	return len(r.HTTPStatuses) + len(r.ErrorCodes) + len(r.ErrorTypes)
}
func retryScope(r RetryRule) string {
	return strings.Join([]string{scopeInts(r.HTTPStatuses), scopeStrings(r.ErrorCodes), scopeStrings(r.ErrorTypes)}, "\x00")
}
func scopeStrings(in []string) string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return strings.Join(out, "\x01")
}
func scopeInts(in []int) string {
	out := append([]int(nil), in...)
	sort.Ints(out)
	parts := make([]string, len(out))
	for i, v := range out {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, "\x01")
}

func rank(e ThinkingEffort) int {
	for i, v := range []ThinkingEffort{ThinkingNone, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax} {
		if e == v {
			return i
		}
	}
	return -1
}

// CloneCompiledConfig deep-copies every mutable member of c for Store use.
func CloneCompiledConfig(c CompiledConfig) CompiledConfig {
	out := c
	out.AutoModelIDs = append([]string(nil), c.AutoModelIDs...)
	out.Models = make(map[string]CompiledModel, len(c.Models))
	for k, v := range c.Models {
		v.Capabilities = append([]Capability(nil), v.Capabilities...)
		v.FallbackModelIDs = append([]string(nil), v.FallbackModelIDs...)
		out.Models[k] = v
	}
	out.Providers = make(map[string]CompiledProvider, len(c.Providers))
	for k, v := range c.Providers {
		v.Retry.Rules = cloneRetryRules(v.Retry.Rules)
		out.Providers[k] = v
	}
	out.Adapters = make(map[string]CompiledAdapter, len(c.Adapters))
	for k, v := range c.Adapters {
		v.Capability = cloneCapability(v.Capability)
		v.Thinking = cloneThinking(v.Thinking)
		v.Request = cloneRequest(v.Request)
		v.ResponseRules = cloneResponseRules(v.ResponseRules)
		v.Retry.Rules = cloneRetryRules(v.Retry.Rules)
		out.Adapters[k] = v
	}
	out.Routes = append([]CompiledRoute(nil), c.Routes...)
	for i := range out.Routes {
		out.Routes[i].FallbackRouteIDs = append([]string(nil), out.Routes[i].FallbackRouteIDs...)
		out.Routes[i].Credentials = append([]CompiledCredential(nil), out.Routes[i].Credentials...)
		out.Routes[i].Retry.Rules = cloneRetryRules(out.Routes[i].Retry.Rules)
	}
	return out
}
func cloneAuth(v AuthRule) AuthRule { return v }
func cloneCapability(v CapabilityPolicy) CapabilityPolicy {
	v.Require = append([]Capability(nil), v.Require...)
	v.Deny = append([]Capability(nil), v.Deny...)
	return v
}
func cloneThinking(v ThinkingPolicy) ThinkingPolicy {
	v.EffortMapping = maps(v.EffortMapping)
	v.BudgetMapping = mapi(v.BudgetMapping)
	return v
}
func maps(in map[ThinkingEffort]ThinkingEffort) map[ThinkingEffort]ThinkingEffort {
	o := map[ThinkingEffort]ThinkingEffort{}
	for k, v := range in {
		o[k] = v
	}
	return o
}
func mapi(in map[ThinkingEffort]int) map[ThinkingEffort]int {
	o := map[ThinkingEffort]int{}
	for k, v := range in {
		o[k] = v
	}
	return o
}
func cloneRequest(v RequestPolicy) RequestPolicy {
	v.AllowedHeaders = append([]string(nil), v.AllowedHeaders...)
	v.AllowedQuery = append([]string(nil), v.AllowedQuery...)
	v.Rules = append([]RequestRule(nil), v.Rules...)
	for i := range v.Rules {
		v.Rules[i].Value = append([]byte(nil), v.Rules[i].Value...)
		v.Rules[i].EnumMap = cloneStringMap(v.Rules[i].EnumMap)
		if v.Rules[i].Min != nil {
			value := *v.Rules[i].Min
			v.Rules[i].Min = &value
		}
		if v.Rules[i].Max != nil {
			value := *v.Rules[i].Max
			v.Rules[i].Max = &value
		}
	}
	return v
}
func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneResponseRules(in []ResponseRule) []ResponseRule {
	out := append([]ResponseRule(nil), in...)
	for i := range out {
		out[i].Match.HTTPStatuses = append([]int(nil), out[i].Match.HTTPStatuses...)
		out[i].Match.ErrorCodes = append([]string(nil), out[i].Match.ErrorCodes...)
		out[i].Match.ErrorTypes = append([]string(nil), out[i].Match.ErrorTypes...)
		out[i].Match.MessageContains = append([]string(nil), out[i].Match.MessageContains...)
		out[i].Match.FinishReasons = append([]string(nil), out[i].Match.FinishReasons...)
		out[i].Match.StreamEventTypes = append([]string(nil), out[i].Match.StreamEventTypes...)
	}
	return out
}
func cloneRetryRules(in []RetryRule) []RetryRule {
	out := append([]RetryRule(nil), in...)
	for i := range out {
		out[i].HTTPStatuses = append([]int(nil), out[i].HTTPStatuses...)
		out[i].ErrorCodes = append([]string(nil), out[i].ErrorCodes...)
		out[i].ErrorTypes = append([]string(nil), out[i].ErrorTypes...)
	}
	return out
}
