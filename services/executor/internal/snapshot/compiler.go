package snapshot

import "github.com/tokenmp/v3/services/executor/internal/adapter"

// CompiledConfig is the normalized runtime configuration produced from a raw
// ConfigSnapshot. It is an alias so callers can use it directly as snapshot.CompiledConfig
// with snapshot.Store and adapter.CloneCompiledConfig.
type CompiledConfig = adapter.CompiledConfig

// Compile validates and normalizes a raw configuration snapshot.
func Compile(raw ConfigSnapshot) (CompiledConfig, error) {
	models := make(map[string]adapter.ModelInput, len(raw.Models))
	for k, v := range raw.Models {
		models[k] = adapter.ModelInput{ID: v.ID, Capabilities: v.Capabilities, Thinking: adapter.ThinkingInput{Supported: v.Thinking.Supported, DefaultEffort: v.Thinking.DefaultEffort, MaxEffort: v.Thinking.MaxEffort, MinBudgetToken: v.Thinking.MinBudgetToken, MaxBudgetToken: v.Thinking.MaxBudgetToken}, FallbackModelIDs: v.FallbackModelIDs}
	}
	providers := make(map[string]adapter.ProviderInput, len(raw.Providers))
	for k, v := range raw.Providers {
		providers[k] = adapter.ProviderInput{ID: v.ID, Name: v.Name, Selector: v.Selector, BaseURL: v.BaseURL, SDKKind: v.SDKKind, Protocol: v.Protocol, Retry: v.Retry, Timeout: v.Timeout}
	}
	routes := make([]adapter.RouteInput, len(raw.Routes))
	for i, v := range raw.Routes {
		credentials := make([]adapter.CredentialInput, len(v.Credentials))
		for j, credential := range v.Credentials {
			credentials[j] = adapter.CredentialInput{ID: credential.ID, CredentialRef: credential.CredentialRef, Priority: credential.Priority, Enabled: credential.Enabled}
		}
		routes[i] = adapter.RouteInput{ID: v.ID, ModelID: v.ModelID, ProviderID: v.ProviderID, AdapterID: v.AdapterID, UpstreamModel: v.UpstreamModel, Priority: v.Priority, Enabled: v.Enabled, Protocol: v.Protocol, Retry: v.Retry, Timeout: v.Timeout, FallbackRouteIDs: v.FallbackRouteIDs, RouteGroup: v.RouteGroup, Credentials: credentials}
	}
	return adapter.Compile(adapter.ConfigInput{Revision: raw.Revision, Models: models, Providers: providers, Routes: routes, Adapters: raw.Adapters, Global: adapter.GlobalInput{Retry: raw.Global.Retry, Timeout: raw.Global.Timeout, AutoModelIDs: raw.Global.AutoModelIDs}})
}
