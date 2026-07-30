// Package snapshot defines the raw configuration snapshot consumed by the
// future validator and compiler.
package snapshot

import (
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ConfigSnapshot is the versioned, raw configuration input. It is mutable by
// construction; compilation is responsible for producing a separate immutable
// effective snapshot for request execution.
type ConfigSnapshot struct {
	Revision  string
	CreatedAt time.Time
	Global    GlobalPolicy
	Models    map[string]ModelConfig
	Providers map[string]ProviderConfig
	Routes    []RouteConfig
	Adapters  map[string]adapter.AdapterConfig
}

// GlobalPolicy provides the lowest-precedence retry and timeout settings.
type GlobalPolicy struct {
	Retry        adapter.RetryPolicy
	Timeout      adapter.TimeoutPolicy
	AutoModelIDs []string
}

// ModelConfig declares the public model identity and its normalized features.
type ModelConfig struct {
	ID               string
	DisplayName      string
	Capabilities     []adapter.Capability
	Thinking         ModelThinkingConfig
	FallbackModelIDs []string
}

// ModelThinkingConfig describes model-level thinking limits before an adapter
// maps them to provider-specific settings.
type ModelThinkingConfig struct {
	Supported      bool
	DefaultEffort  adapter.ThinkingEffort
	MaxEffort      adapter.ThinkingEffort
	MinBudgetToken int
	MaxBudgetToken int
}

// ProviderConfig identifies an upstream provider and its transport configuration.
// BaseURL contains no credentials. Endpoints advertise the protocols the
// provider supports and the path each protocol targets; protocol selection is
// resolved at runtime per request from these endpoints (routes no longer carry
// a protocol).
type ProviderConfig struct {
	ID        string
	Name      string
	Selector  string
	BaseURL   string
	SDKKind   adapter.SDKKind
	Endpoints []EndpointConfig
	Retry     adapter.RetryPolicy
	Timeout   adapter.TimeoutPolicy
}

// EndpointConfig advertises one protocol the provider can serve and the path
// prefix that protocol targets. Auth is derived from the provider SDKKind,
// not stored per endpoint.
type EndpointConfig struct {
	Protocol adapter.Protocol
	Path     string
}

// RouteConfig maps one public model to an upstream provider. Protocol,
// adapter and base URL are no longer stored on the route; they are resolved at
// runtime from the provider's endpoints and the request protocol.
type RouteConfig struct {
	ID               string
	ModelID          string
	ProviderID       string
	UpstreamModel    string
	Priority         int
	Enabled          bool
	Retry            adapter.RetryPolicy
	Timeout          adapter.TimeoutPolicy
	FallbackRouteIDs []string
	RouteGroup       string
	Credentials      []CredentialConfig
}

// CredentialConfig identifies one non-secret credential candidate for a route.
// CredentialRef is resolved outside the snapshot and must never contain the
// credential material itself.
type CredentialConfig struct {
	ID            string
	CredentialRef string
	Priority      int
	Enabled       bool
}
