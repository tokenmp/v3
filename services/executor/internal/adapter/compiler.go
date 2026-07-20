package adapter

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
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
}
type ModelInput struct {
	ID           string
	Capabilities []Capability
	Thinking     ThinkingInput
}
type ThinkingInput struct {
	Supported                      bool
	DefaultEffort, MaxEffort       ThinkingEffort
	MinBudgetToken, MaxBudgetToken int
}
type ProviderInput struct {
	ID, Name, BaseURL string
	SDKKind           SDKKind
	Protocol          Protocol
	Retry             RetryPolicy
	Timeout           TimeoutPolicy
}
type RouteInput struct {
	ID, ModelID, ProviderID, AdapterID, UpstreamModel string
	Priority                                          int
	Enabled                                           bool
	Protocol                                          Protocol
	Retry                                             RetryPolicy
	Timeout                                           TimeoutPolicy
	FallbackRouteIDs                                  []string
}

// CompiledConfig is a normalized immutable-in-practice configuration value.
// Use CloneCompiledConfig before retaining a caller-visible value.
type CompiledConfig struct {
	Revision  string
	Models    map[string]CompiledModel
	Providers map[string]CompiledProvider
	Adapters  map[string]CompiledAdapter
	Routes    []CompiledRoute
}
type CompiledModel struct {
	ID           string
	Capabilities []Capability
	Thinking     ThinkingInput
}
type CompiledProvider struct {
	ID, Name, BaseURL string
	SDKKind           SDKKind
	Protocol          Protocol
	Retry             CompiledRetry
	Timeout           CompiledTimeout
}
type CompiledRoute struct {
	ID, ModelID, ProviderID, AdapterID, UpstreamModel string
	Priority                                          int
	Enabled                                           bool
	Protocol                                          Protocol
	Retry                                             CompiledRetry
	Timeout                                           CompiledTimeout
	FallbackRouteIDs                                  []string
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
	out := CompiledConfig{Revision: in.Revision, Models: make(map[string]CompiledModel, len(in.Models)), Providers: make(map[string]CompiledProvider, len(in.Providers)), Adapters: make(map[string]CompiledAdapter, len(in.Adapters))}
	modelIDs := map[string]bool{}
	for key, m := range in.Models {
		if modelIDs[m.ID] {
			return CompiledConfig{}, fmt.Errorf("duplicate model ID %q", m.ID)
		}
		modelIDs[m.ID] = true
		if key == "" || m.ID == "" || key != m.ID {
			return CompiledConfig{}, fmt.Errorf("model key/id mismatch %q/%q", key, m.ID)
		}
		if err := capabilities(m.Capabilities); err != nil {
			return CompiledConfig{}, fmt.Errorf("model %q: %w", key, err)
		}
		if err := thinkingInput(m.Thinking); err != nil {
			return CompiledConfig{}, fmt.Errorf("model %q: %w", key, err)
		}
		out.Models[key] = CompiledModel{ID: m.ID, Capabilities: append([]Capability(nil), m.Capabilities...), Thinking: m.Thinking}
	}
	providerNames := map[string]bool{}
	for key, p := range in.Providers {
		if providerNames[p.Name] {
			return CompiledConfig{}, fmt.Errorf("duplicate provider name %q", p.Name)
		}
		providerNames[p.Name] = true
		if key == "" || p.ID == "" || key != p.ID || strings.TrimSpace(p.Name) == "" || !p.SDKKind.Valid() || !p.Protocol.Valid() {
			return CompiledConfig{}, fmt.Errorf("invalid provider %q", key)
		}
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
		out.Providers[key] = CompiledProvider{ID: p.ID, Name: p.Name, BaseURL: p.BaseURL, SDKKind: p.SDKKind, Protocol: p.Protocol, Retry: r, Timeout: t}
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
		out.Routes = append(out.Routes, CompiledRoute{ID: route.ID, ModelID: route.ModelID, ProviderID: route.ProviderID, AdapterID: route.AdapterID, UpstreamModel: route.UpstreamModel, Priority: route.Priority, Enabled: route.Enabled, Protocol: route.Protocol, Retry: r, Timeout: t, FallbackRouteIDs: append([]string(nil), route.FallbackRouteIDs...)})
	}
	if err := validateFallbacks(out.Routes); err != nil {
		return CompiledConfig{}, err
	}
	sort.SliceStable(out.Routes, func(i, j int) bool {
		if out.Routes[i].Priority == out.Routes[j].Priority {
			return out.Routes[i].ID < out.Routes[j].ID
		}
		return out.Routes[i].Priority < out.Routes[j].Priority
	})
	return out, nil
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
	if a.Auth.Kind == AuthBearerHeader || a.Auth.Kind == AuthAPIKeyHeader {
		if a.Auth.Header == "" {
			return fmt.Errorf("header auth requires header")
		}
	}
	if a.Auth.Kind == AuthAPIKeyQuery && a.Auth.Query == "" {
		return fmt.Errorf("query auth requires query")
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
	return nil
}
func requestPolicy(p RequestPolicy) error {
	headers := headerSet(p.AllowedHeaders)
	query := exactSet(p.AllowedQuery)
	ids, writes := map[string]bool{}, map[string]string{}
	for _, r := range p.Rules {
		if r.ID == "" || ids[r.ID] || !r.Action.Valid() {
			return fmt.Errorf("invalid or duplicate request rule %q", r.ID)
		}
		if r.ValueRef != "" {
			return fmt.Errorf("request rule %q uses unsupported value reference", r.ID)
		}
		ids[r.ID] = true
		write := func(path string) error {
			if err := validateJSONPointer(path); err != nil {
				return err
			}
			if protectedPath(path) {
				return fmt.Errorf("protected request path %q", path)
			}
			if old, ok := writes[path]; ok {
				return fmt.Errorf("request rules %q and %q write %q", old, r.ID, path)
			}
			writes[path] = r.ID
			return nil
		}
		switch r.Action {
		case RequestSetHeader:
			name := strings.TrimSpace(r.Name)
			if deniedHeader(name) || !headers[canonicalHeader(name)] {
				return fmt.Errorf("header %q is not allowlisted", r.Name)
			}
		case RequestSetQuery:
			if !query[r.Name] {
				return fmt.Errorf("query %q is not allowlisted", r.Name)
			}
		case RequestSet:
			if !json.Valid(r.Value) {
				return fmt.Errorf("set value is not JSON")
			}
			if err := write(r.Path); err != nil {
				return err
			}
		case RequestCopy:
			if err := validateWritablePath(r.From); err != nil {
				return fmt.Errorf("copy source: %w", err)
			}
			if err := write(r.To); err != nil {
				return err
			}
		case RequestRename:
			if err := validateWritablePath(r.From); err != nil {
				return fmt.Errorf("rename source: %w", err)
			}
			if err := write(r.From); err != nil {
				return err
			}
			if err := write(r.To); err != nil {
				return err
			}
		case RequestRemove:
			if err := write(r.Path); err != nil {
				return err
			}
		case RequestMapEnum:
			if len(r.EnumMap) == 0 {
				return fmt.Errorf("empty enum map")
			}
			if err := write(r.Path); err != nil {
				return err
			}
		case RequestClampNumber:
			if r.Min == nil || r.Max == nil || *r.Min > *r.Max {
				return fmt.Errorf("invalid clamp")
			}
			if err := write(r.Path); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateJSONPointer(p string) error {
	if len(p) == 0 || len(p) > maxJSONPointerLength || p[0] != '/' {
		return fmt.Errorf("invalid JSON pointer")
	}
	parts := strings.Split(p[1:], "/")
	if len(parts) > maxJSONPointerDepth {
		return fmt.Errorf("JSON pointer too deep")
	}
	for _, part := range parts {
		for i := 0; i < len(part); i++ {
			if part[i] == '~' && (i+1 == len(part) || (part[i+1] != '0' && part[i+1] != '1')) {
				return fmt.Errorf("invalid JSON pointer escape")
			}
		}
	}
	return nil
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
func canonicalHeader(v string) string { return strings.ToLower(strings.TrimSpace(v)) }
func headerSet(xs []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range xs {
		if x != "" {
			out[canonicalHeader(x)] = true
		}
	}
	return out
}
func exactSet(xs []string) map[string]bool {
	out := map[string]bool{}
	for _, x := range xs {
		if x != "" {
			out[x] = true
		}
	}
	return out
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
	ids, conflicts := map[string]bool{}, map[string]bool{}
	for _, r := range rs {
		key := fmt.Sprintf("%d/%d/%s", r.Priority, responseSpecificity(r.Match), responseScope(r.Match))
		if r.ID == "" || ids[r.ID] || conflicts[key] || r.Output.HTTPStatus < 100 || r.Output.HTTPStatus > 599 {
			return fmt.Errorf("invalid/conflicting response rule %q", r.ID)
		}
		ids[r.ID] = true
		conflicts[key] = true
	}
	return nil
}
func validateRetryRules(rs []RetryRule) error {
	ids, conflicts := map[string]bool{}, map[string]bool{}
	for _, r := range rs {
		key := fmt.Sprintf("%d/%d/%s", r.Priority, retrySpecificity(r), retryScope(r))
		if r.ID == "" || ids[r.ID] || conflicts[key] || !r.Action.Valid() {
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
			if nr.ModelID != r.ModelID {
				return fmt.Errorf("route %q fallback %q targets another model", r.ID, next)
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
	out.Models = make(map[string]CompiledModel, len(c.Models))
	for k, v := range c.Models {
		v.Capabilities = append([]Capability(nil), v.Capabilities...)
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
