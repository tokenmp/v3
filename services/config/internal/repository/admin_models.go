package repository

import (
	"context"
	"time"
)

// Provider maps to the config.providers table.
type Provider struct {
	ID              string    `gorm:"column:id;primaryKey" json:"id"`
	Name            string    `gorm:"column:name" json:"name"`
	DisplayLabel    string    `gorm:"column:display_label" json:"display_label"`
	Selector        string    `gorm:"column:selector" json:"selector"`
	BaseURL         string    `gorm:"column:base_url" json:"base_url"`
	SDKKind         string    `gorm:"column:sdk_kind" json:"sdk_kind"`
	Protocol        string    `gorm:"column:protocol" json:"protocol"`
	DefaultRetry    []byte    `gorm:"column:default_retry;type:jsonb" json:"default_retry,omitempty"`
	DefaultTimeout  []byte    `gorm:"column:default_timeout;type:jsonb" json:"default_timeout,omitempty"`
	ContextWindow   *int      `gorm:"column:context_window" json:"context_window,omitempty"`
	MaxOutputTokens *int      `gorm:"column:max_output_tokens" json:"max_output_tokens,omitempty"`
	RPM             *int      `gorm:"column:rpm" json:"rpm,omitempty"`
	TPM             *int      `gorm:"column:tpm" json:"tpm,omitempty"`
	Status          string    `gorm:"column:status" json:"status"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Provider) TableName() string { return "providers" }

// Model maps to the config.models table.
type Model struct {
	ID                     string      `gorm:"column:id;primaryKey" json:"id"`
	DisplayName            string      `gorm:"column:display_name" json:"display_name"`
	InputModalities        StringArray `gorm:"column:input_modalities;type:jsonb" json:"input_modalities"`
	OutputModalities       StringArray `gorm:"column:output_modalities;type:jsonb" json:"output_modalities"`
	Capabilities           StringArray `gorm:"column:capabilities;type:jsonb" json:"capabilities"`
	ContextWindow          *int        `gorm:"column:context_window" json:"context_window,omitempty"`
	MaxOutputTokens        *int        `gorm:"column:max_output_tokens" json:"max_output_tokens,omitempty"`
	ThinkingSupported      bool        `gorm:"column:thinking_supported" json:"thinking_supported"`
	ThinkingDefaultEffort  *string     `gorm:"column:thinking_default_effort" json:"thinking_default_effort,omitempty"`
	ThinkingMaxEffort      *string     `gorm:"column:thinking_max_effort" json:"thinking_max_effort,omitempty"`
	ThinkingMinBudgetToken *int        `gorm:"column:thinking_min_budget_token" json:"thinking_min_budget_token,omitempty"`
	ThinkingMaxBudgetToken *int        `gorm:"column:thinking_max_budget_token" json:"thinking_max_budget_token,omitempty"`
	Status                 string      `gorm:"column:status" json:"status"`
	CreatedAt              time.Time   `gorm:"column:created_at" json:"created_at"`
	UpdatedAt              time.Time   `gorm:"column:updated_at" json:"updated_at"`
}

func (Model) TableName() string { return "models" }

// Adapter maps to the config.adapters table.
type Adapter struct {
	ID                string    `gorm:"column:id;primaryKey" json:"id"`
	Name              string    `gorm:"column:name" json:"name"`
	Version           int       `gorm:"column:version" json:"version"`
	ProviderID        *string   `gorm:"column:provider_id" json:"provider_id,omitempty"`
	SDKKind           string    `gorm:"column:sdk_kind" json:"sdk_kind"`
	Protocol          string    `gorm:"column:protocol" json:"protocol"`
	CapabilityRequire []byte    `gorm:"column:capability_require;type:jsonb" json:"capability_require,omitempty"`
	CapabilityDeny    []byte    `gorm:"column:capability_deny;type:jsonb" json:"capability_deny,omitempty"`
	Thinking          []byte    `gorm:"column:thinking;type:jsonb" json:"thinking,omitempty"`
	RequestPolicy     []byte    `gorm:"column:request_policy;type:jsonb" json:"request_policy,omitempty"`
	ResponsePolicy    []byte    `gorm:"column:response_policy;type:jsonb" json:"response_policy,omitempty"`
	RetryPolicy       []byte    `gorm:"column:retry_policy;type:jsonb" json:"retry_policy,omitempty"`
	TimeoutPolicy     []byte    `gorm:"column:timeout_policy;type:jsonb" json:"timeout_policy,omitempty"`
	Status            string    `gorm:"column:status" json:"status"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Adapter) TableName() string { return "adapters" }

// UpstreamEndpoint maps to the config.upstream_endpoints table.
type UpstreamEndpoint struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ProviderID string    `gorm:"column:provider_id" json:"provider_id"`
	Path       string    `gorm:"column:path" json:"path"`
	Protocol   string    `gorm:"column:protocol" json:"protocol"`
	AuthKind   string    `gorm:"column:auth_kind" json:"auth_kind"`
	AuthHeader *string   `gorm:"column:auth_header" json:"auth_header,omitempty"`
	AuthQuery  *string   `gorm:"column:auth_query" json:"auth_query,omitempty"`
	AuthPrefix *string   `gorm:"column:auth_prefix" json:"auth_prefix,omitempty"`
	Status     string    `gorm:"column:status" json:"status"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (UpstreamEndpoint) TableName() string { return "upstream_endpoints" }

// UpstreamCredential maps to the config.upstream_credentials table.
// api_key is a legacy column retained only to identify historical plaintext
// data; it is never written by the application layer (secret boundary) and
// is excluded from JSON serialization so it never reaches responses, logs
// or audit.
type UpstreamCredential struct {
	ID             string    `gorm:"column:id;primaryKey" json:"id"`
	ProviderID     string    `gorm:"column:provider_id" json:"provider_id"`
	CredentialRef  string    `gorm:"column:credential_ref" json:"credential_ref,omitempty"`
	APIKey         *string   `gorm:"column:api_key" json:"-"`
	KeyPrefix      *string   `gorm:"column:key_prefix" json:"key_prefix,omitempty"`
	KeySuffix      *string   `gorm:"column:key_suffix" json:"key_suffix,omitempty"`
	Priority       int       `gorm:"column:priority" json:"priority"`
	MaxConcurrency *int      `gorm:"column:max_concurrency" json:"max_concurrency,omitempty"`
	DailyQuota     *int      `gorm:"column:daily_quota" json:"daily_quota,omitempty"`
	RPM            *int      `gorm:"column:rpm" json:"rpm,omitempty"`
	TPM            *int      `gorm:"column:tpm" json:"tpm,omitempty"`
	Status         string    `gorm:"column:status" json:"status"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (UpstreamCredential) TableName() string { return "upstream_credentials" }

// RouteMapping maps to the config.route_mappings table.
type RouteMapping struct {
	ID              string    `gorm:"column:id;primaryKey" json:"id"`
	ModelID         string    `gorm:"column:model_id" json:"model_id"`
	ProviderID      string    `gorm:"column:provider_id" json:"provider_id"`
	AdapterID       *string   `gorm:"column:adapter_id" json:"adapter_id,omitempty"`
	UpstreamModel   string    `gorm:"column:upstream_model" json:"upstream_model"`
	Protocol        string    `gorm:"column:protocol" json:"protocol"`
	Priority        int       `gorm:"column:priority" json:"priority"`
	Enabled         bool      `gorm:"column:enabled" json:"enabled"`
	IsDefault       bool      `gorm:"column:is_default" json:"is_default"`
	ContextWindow   *int      `gorm:"column:context_window" json:"context_window,omitempty"`
	MaxOutputTokens *int      `gorm:"column:max_output_tokens" json:"max_output_tokens,omitempty"`
	RPM             *int      `gorm:"column:rpm" json:"rpm,omitempty"`
	TPM             *int      `gorm:"column:tpm" json:"tpm,omitempty"`
	RouteGroup      *string   `gorm:"column:route_group" json:"route_group,omitempty"`
	RetryPolicy     []byte    `gorm:"column:retry_policy;type:jsonb" json:"retry_policy,omitempty"`
	TimeoutPolicy   []byte    `gorm:"column:timeout_policy;type:jsonb" json:"timeout_policy,omitempty"`
	Status          string    `gorm:"column:status" json:"status"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (RouteMapping) TableName() string { return "route_mappings" }

// RouteCredential maps to the config.route_credentials join table.
type RouteCredential struct {
	RouteID      string    `gorm:"column:route_id;primaryKey" json:"route_id"`
	CredentialID string    `gorm:"column:credential_id;primaryKey" json:"credential_id"`
	Priority     int       `gorm:"column:priority" json:"priority"`
	Enabled      bool      `gorm:"column:enabled" json:"enabled"`
	RPM          *int      `gorm:"column:rpm" json:"rpm,omitempty"`
	TPM          *int      `gorm:"column:tpm" json:"tpm,omitempty"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
}

func (RouteCredential) TableName() string { return "route_credentials" }

// clampLimit ensures a sane page size.
func clampLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

// AdminReader is the read contract for config admin data.
type AdminReader interface {
	ListProviders(ctx context.Context, limit, offset int) ([]Provider, int64, error)
	GetProvider(ctx context.Context, id string) (Provider, error)
	ListModels(ctx context.Context, limit, offset int) ([]Model, int64, error)
	GetModel(ctx context.Context, id string) (Model, error)
	ListModelIDs(ctx context.Context) ([]string, error)
	ListAdapters(ctx context.Context, limit, offset int) ([]Adapter, int64, error)
	GetAdapter(ctx context.Context, id string) (Adapter, error)
	ListEndpoints(ctx context.Context, providerID string) ([]UpstreamEndpoint, error)
	ListCredentials(ctx context.Context, providerID string) ([]UpstreamCredential, error)
	ListRoutes(ctx context.Context, limit, offset int) ([]RouteMapping, int64, error)
	GetRoute(ctx context.Context, id string) (RouteMapping, error)
	ListRouteCredentials(ctx context.Context, routeID string) ([]RouteCredential, error)

	// ListAllActiveModels returns all active (non-deleted) models without pagination.
	ListAllActiveModels(ctx context.Context) ([]Model, error)
	// ListAllActiveProviders returns all active (non-deleted) providers without pagination.
	ListAllActiveProviders(ctx context.Context) ([]Provider, error)
	// ListAllActiveRoutes returns all active (non-deleted) routes without pagination.
	ListAllActiveRoutes(ctx context.Context) ([]RouteMapping, error)
	// ListAllActiveAdapters returns all active (non-deleted) adapters without pagination.
	ListAllActiveAdapters(ctx context.Context) ([]Adapter, error)

	// GetGlobalPolicy reads all three global_config KV rows (default_retry,
	// default_timeout, auto_model_ids). Missing rows yield nil bytes.
	GetGlobalPolicy(ctx context.Context) (GlobalPolicy, error)
	// GetGlobalConfigEntry returns a single global_config row by key.
	GetGlobalConfigEntry(ctx context.Context, key string) (GlobalConfig, error)
}
