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
	Retry   adapter.RetryPolicy
	Timeout adapter.TimeoutPolicy
}

// ModelConfig declares the public model identity and its normalized features.
type ModelConfig struct {
	ID           string
	DisplayName  string
	Capabilities []adapter.Capability
	Thinking     ModelThinkingConfig
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

// ProviderConfig identifies an upstream provider and its default transport
// configuration. BaseURL contains no credentials.
type ProviderConfig struct {
	ID       string
	Name     string
	BaseURL  string
	SDKKind  adapter.SDKKind
	Protocol adapter.Protocol
	Retry    adapter.RetryPolicy
	Timeout  adapter.TimeoutPolicy
}

// RouteConfig maps one public model to an upstream provider and adapter.
type RouteConfig struct {
	ID               string
	ModelID          string
	ProviderID       string
	AdapterID        string
	UpstreamModel    string
	Priority         int
	Enabled          bool
	Protocol         adapter.Protocol
	Retry            adapter.RetryPolicy
	Timeout          adapter.TimeoutPolicy
	FallbackRouteIDs []string
}
