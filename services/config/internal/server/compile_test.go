package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/config/internal/repository"
)

// ---- Fake implementations for compile tests ----

type fakeAdminReader struct {
	models    []repository.Model
	providers []repository.Provider
	routes    []repository.RouteMapping
	adapters  []repository.Adapter
	// credentials by provider ID
	credsByProvider map[string][]repository.UpstreamCredential
	// route credentials by route ID
	routeCredsByRoute map[string][]repository.RouteCredential
}

func (f *fakeAdminReader) ListProviders(_ context.Context, _, _ int) ([]repository.Provider, int64, error) {
	return f.providers, int64(len(f.providers)), nil
}
func (f *fakeAdminReader) GetProvider(_ context.Context, id string) (repository.Provider, error) {
	for _, p := range f.providers {
		if p.ID == id {
			return p, nil
		}
	}
	return repository.Provider{}, repository.ErrNotFound
}
func (f *fakeAdminReader) ListModels(_ context.Context, _, _ int) ([]repository.Model, int64, error) {
	return f.models, int64(len(f.models)), nil
}
func (f *fakeAdminReader) GetModel(_ context.Context, id string) (repository.Model, error) {
	for _, m := range f.models {
		if m.ID == id {
			return m, nil
		}
	}
	return repository.Model{}, repository.ErrNotFound
}
func (f *fakeAdminReader) ListModelIDs(_ context.Context) ([]string, error) {
	ids := make([]string, 0, len(f.models))
	for _, m := range f.models {
		if m.Status == "active" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}
func (f *fakeAdminReader) ListAdapters(_ context.Context, _, _ int) ([]repository.Adapter, int64, error) {
	return f.adapters, int64(len(f.adapters)), nil
}
func (f *fakeAdminReader) GetAdapter(_ context.Context, id string) (repository.Adapter, error) {
	for _, a := range f.adapters {
		if a.ID == id {
			return a, nil
		}
	}
	return repository.Adapter{}, repository.ErrNotFound
}
func (f *fakeAdminReader) ListEndpoints(_ context.Context, _ string) ([]repository.UpstreamEndpoint, error) {
	return nil, nil
}
func (f *fakeAdminReader) ListCredentials(_ context.Context, providerID string) ([]repository.UpstreamCredential, error) {
	if f.credsByProvider != nil {
		return f.credsByProvider[providerID], nil
	}
	return nil, nil
}
func (f *fakeAdminReader) ListRoutes(_ context.Context, _, _ int) ([]repository.RouteMapping, int64, error) {
	return f.routes, int64(len(f.routes)), nil
}
func (f *fakeAdminReader) GetRoute(_ context.Context, id string) (repository.RouteMapping, error) {
	for _, r := range f.routes {
		if r.ID == id {
			return r, nil
		}
	}
	return repository.RouteMapping{}, repository.ErrNotFound
}
func (f *fakeAdminReader) ListRouteCredentials(_ context.Context, routeID string) ([]repository.RouteCredential, error) {
	if f.routeCredsByRoute != nil {
		return f.routeCredsByRoute[routeID], nil
	}
	return nil, nil
}
func (f *fakeAdminReader) ListAllActiveModels(_ context.Context) ([]repository.Model, error) {
	var active []repository.Model
	for _, m := range f.models {
		if m.Status != "deleted" {
			active = append(active, m)
		}
	}
	return active, nil
}
func (f *fakeAdminReader) ListAllActiveProviders(_ context.Context) ([]repository.Provider, error) {
	var active []repository.Provider
	for _, p := range f.providers {
		if p.Status != "deleted" {
			active = append(active, p)
		}
	}
	return active, nil
}
func (f *fakeAdminReader) ListAllActiveRoutes(_ context.Context) ([]repository.RouteMapping, error) {
	var active []repository.RouteMapping
	for _, r := range f.routes {
		if r.Status != "deleted" {
			active = append(active, r)
		}
	}
	return active, nil
}
func (f *fakeAdminReader) ListAllActiveAdapters(_ context.Context) ([]repository.Adapter, error) {
	var active []repository.Adapter
	for _, a := range f.adapters {
		if a.Status != "deleted" {
			active = append(active, a)
		}
	}
	return active, nil
}

func (f *fakeAdminReader) GetGlobalPolicy(_ context.Context) (repository.GlobalPolicy, error) {
	return repository.GlobalPolicy{}, nil
}

func (f *fakeAdminReader) GetGlobalConfigEntry(_ context.Context, _ string) (repository.GlobalConfig, error) {
	return repository.GlobalConfig{}, repository.ErrGlobalConfigNotFound
}

type fakeWriter struct {
	draftID   int64
	drafts    map[int64]repository.DraftRevision
	snapshots map[int64]json.RawMessage
}

func newFakeWriter() *fakeWriter {
	return &fakeWriter{
		drafts:    make(map[int64]repository.DraftRevision),
		snapshots: make(map[int64]json.RawMessage),
	}
}

func (w *fakeWriter) CreateDraft(_ context.Context, revision, createdBy, changeLog string, _ *int64) (int64, error) {
	w.draftID++
	id := w.draftID
	w.drafts[id] = repository.DraftRevision{
		RevisionID: id,
		Revision:   revision,
		Status:     "draft",
		ChangeLog:  changeLog,
		CreatedAt:  time.Now(),
	}
	return id, nil
}
func (w *fakeWriter) UpdateDraftJSON(_ context.Context, revisionID int64, snapshotJSON json.RawMessage) error {
	w.snapshots[revisionID] = snapshotJSON
	return nil
}
func (w *fakeWriter) GetDraft(_ context.Context, revisionID int64) (repository.DraftRevision, error) {
	d, ok := w.drafts[revisionID]
	if !ok {
		return repository.DraftRevision{}, repository.ErrNotFound
	}
	if snap, ok := w.snapshots[revisionID]; ok {
		d.SnapshotJSON = snap
	}
	return d, nil
}
func (w *fakeWriter) PublishRevision(_ context.Context, revisionID int64) error {
	d, ok := w.drafts[revisionID]
	if !ok {
		return repository.ErrNotFound
	}
	if d.Status != "draft" {
		return repository.ErrConflict
	}
	d.Status = "published"
	w.drafts[revisionID] = d
	return nil
}
func (w *fakeWriter) ListRevisions(_ context.Context, _, _ int) ([]repository.RevisionSummary, int, error) {
	return nil, 0, nil
}
func (w *fakeWriter) RollbackRevision(_ context.Context, _ int64) (int64, error) {
	return 0, repository.ErrNotFound
}

// CreateProvider/Model/etc. stubs for AdminWriter interface
func (w *fakeWriter) CreateProvider(_ context.Context, _ *repository.Provider) error     { return nil }
func (w *fakeWriter) UpdateProvider(_ context.Context, _ string, _ map[string]any) error { return nil }
func (w *fakeWriter) DeleteProvider(_ context.Context, _ string) error                   { return nil }
func (w *fakeWriter) CreateModel(_ context.Context, _ *repository.Model) error           { return nil }
func (w *fakeWriter) UpdateModel(_ context.Context, _ string, _ map[string]any) error    { return nil }
func (w *fakeWriter) DeleteModel(_ context.Context, _ string) error                      { return nil }
func (w *fakeWriter) CreateAdapter(_ context.Context, _ *repository.Adapter) error       { return nil }
func (w *fakeWriter) UpdateAdapter(_ context.Context, _ string, _ map[string]any) error  { return nil }
func (w *fakeWriter) DeleteAdapter(_ context.Context, _ string) error                    { return nil }
func (w *fakeWriter) CreateEndpoint(_ context.Context, _ *repository.UpstreamEndpoint) error {
	return nil
}
func (w *fakeWriter) UpdateEndpoint(_ context.Context, _ int64, _ map[string]any) error { return nil }
func (w *fakeWriter) DeleteEndpoint(_ context.Context, _ int64) error                   { return nil }
func (w *fakeWriter) CreateCredential(_ context.Context, _ *repository.UpstreamCredential) error {
	return nil
}
func (w *fakeWriter) UpdateCredential(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (w *fakeWriter) DeleteCredential(_ context.Context, _ string) error              { return nil }
func (w *fakeWriter) CreateRoute(_ context.Context, _ *repository.RouteMapping) error { return nil }
func (w *fakeWriter) UpdateRoute(_ context.Context, _ string, _ map[string]any) error { return nil }
func (w *fakeWriter) DeleteRoute(_ context.Context, _ string) error                   { return nil }
func (w *fakeWriter) SetRouteCredentials(_ context.Context, _ string, _ []repository.RouteCredential) error {
	return nil
}

func (w *fakeWriter) SetGlobalConfigEntry(_ context.Context, _ string, _ []byte, _ string) error {
	return nil
}

// ---- compileSnapshot unit tests ----

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func sampleData() (*fakeAdminReader, map[string][]repository.UpstreamCredential, map[string][]repository.RouteCredential) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{
				ID:                "chat-default",
				DisplayName:       "Default Chat",
				Capabilities:      repository.StringArray{"chat", "streaming", "tools"},
				ThinkingSupported: true,
				Status:            "active",
			},
		},
		providers: []repository.Provider{
			{
				ID:       "openai-default",
				Name:     "OpenAI Default",
				Selector: "openai",
				BaseURL:  "https://api.openai.example/v1",
				SDKKind:  "openai",
				Protocol: "openai_chat",
				Status:   "active",
			},
		},
		routes: []repository.RouteMapping{
			{
				ID:            "route-chat-default",
				ModelID:       "chat-default",
				ProviderID:    "openai-default",
				UpstreamModel: "gpt-default",
				Priority:      100,
				Enabled:       true,
				Protocol:      "openai_chat",
				Status:        "active",
			},
		},
		adapters: []repository.Adapter{},
	}

	credsByProvider := map[string][]repository.UpstreamCredential{
		"openai-default": {
			{
				ID:            "default",
				ProviderID:    "openai-default",
				CredentialRef: "vault://openai-default/credential/default",
				Priority:      100,
				Status:        "active",
			},
		},
	}

	routeCredsByRoute := map[string][]repository.RouteCredential{}

	return reader, credsByProvider, routeCredsByRoute
}

func TestCompileSnapshot_BasicStructure(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	// Must be valid JSON.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Must have top-level keys with uppercase names.
	requiredKeys := []string{"Revision", "CreatedAt", "Global", "Models", "Providers", "Routes", "Adapters"}
	for _, key := range requiredKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}

	// Must NOT have lowercase keys (executor uses DisallowUnknownFields).
	forbiddenKeys := []string{"revision", "createdAt", "global", "models", "providers", "routes", "adapters"}
	for _, key := range forbiddenKeys {
		if _, ok := raw[key]; ok {
			t.Errorf("forbidden lowercase key %q found in snapshot", key)
		}
	}
}

func TestCompileSnapshot_ModelsUppercase(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(snap.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(snap.Models))
	}
	m := snap.Models["chat-default"]
	if m.ID != "chat-default" {
		t.Errorf("model ID = %q, want %q", m.ID, "chat-default")
	}
	if m.DisplayName != "Default Chat" {
		t.Errorf("model DisplayName = %q, want %q", m.DisplayName, "Default Chat")
	}
	if !m.Thinking.Supported {
		t.Error("model Thinking.Supported = false, want true")
	}
	if m.Thinking.DefaultEffort != "medium" {
		t.Errorf("model Thinking.DefaultEffort = %q, want %q", m.Thinking.DefaultEffort, "medium")
	}
	if m.Thinking.MaxEffort != "high" {
		t.Errorf("model Thinking.MaxEffort = %q, want %q", m.Thinking.MaxEffort, "high")
	}
}

func TestCompileSnapshot_AutoGeneratedAdapter(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	adapterID := "adapter-openai-default"
	a, ok := snap.Adapters[adapterID]
	if !ok {
		t.Fatalf("auto-generated adapter %q not found", adapterID)
	}
	if a.Auth.Kind != "bearer_header" {
		t.Errorf("adapter Auth.Kind = %q, want %q", a.Auth.Kind, "bearer_header")
	}
	if a.Auth.Header != "Authorization" {
		t.Errorf("adapter Auth.Header = %q, want %q", a.Auth.Header, "Authorization")
	}
	if a.Auth.Prefix != "Bearer" {
		t.Errorf("adapter Auth.Prefix = %q, want %q", a.Auth.Prefix, "Bearer")
	}
	if a.Auth.CredentialRef != "" {
		t.Errorf("adapter Auth.CredentialRef = %q, want empty (credentials via route.Credentials)", a.Auth.CredentialRef)
	}
	if a.SDKKind != "openai" {
		t.Errorf("adapter SDKKind = %q, want %q", a.SDKKind, "openai")
	}
	if !a.Thinking.Supported {
		t.Error("adapter Thinking.Supported = false, want true")
	}
	if len(a.Response.Rules) != 1 {
		t.Errorf("adapter Response.Rules count = %d, want 1", len(a.Response.Rules))
	}
	// Auto-generated adapter must NOT hardcode retry rules: a non-empty
	// adapter.Retry.Rules overrides the global policy in the executor's
	// compile chain (global -> adapter -> provider -> route), defeating
	// admin-configured global retry rules. It must be empty so the
	// executor inherits the global policy.
	if len(a.Retry.Rules) != 0 {
		t.Errorf("auto-adapter Retry.Rules = %d, want 0 (inherit global)", len(a.Retry.Rules))
	}
	if a.Retry.MaxTotalAttempts != nil {
		t.Errorf("auto-adapter Retry.MaxTotalAttempts = %v, want nil (inherit global)", a.Retry.MaxTotalAttempts)
	}
	if len(a.Timeout.RequestTimeout) != 0 {
		t.Errorf("auto-adapter Timeout.RequestTimeout = %q, want empty (inherit global)", a.Timeout.RequestTimeout)
	}
}

func TestCompileSnapshot_AnthropicAutoAdapter(t *testing.T) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "chat-anthropic", DisplayName: "Anthropic Chat", Capabilities: repository.StringArray{"chat", "streaming", "tools", "thinking"}, ThinkingSupported: true, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "anthropic-default", Name: "Anthropic Default", Selector: "anthropic", BaseURL: "https://api.anthropic.example", SDKKind: "anthropic", Protocol: "anthropic_messages", Status: "active"},
		},
		routes: []repository.RouteMapping{
			{ID: "route-chat-anthropic", ModelID: "chat-anthropic", ProviderID: "anthropic-default", UpstreamModel: "claude-default", Priority: 100, Enabled: true, Protocol: "anthropic_messages", Status: "active"},
		},
		adapters: []repository.Adapter{},
	}

	credsByProvider := map[string][]repository.UpstreamCredential{
		"anthropic-default": {
			{ID: "default", ProviderID: "anthropic-default", CredentialRef: "vault://anthropic-default/credential/default", Priority: 100, Status: "active"},
		},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	a := snap.Adapters["adapter-anthropic-default"]
	if a.Auth.Kind != "api_key_header" {
		t.Errorf("adapter Auth.Kind = %q, want %q", a.Auth.Kind, "api_key_header")
	}
	if a.Auth.Header != "x-api-key" {
		t.Errorf("adapter Auth.Header = %q, want %q", a.Auth.Header, "x-api-key")
	}
	if a.Auth.Prefix != "" {
		t.Errorf("adapter Auth.Prefix = %q, want empty", a.Auth.Prefix)
	}
	// Anthropic adapter should have 529→429 response rule.
	if len(a.Response.Rules) != 2 {
		t.Errorf("adapter Response.Rules count = %d, want 2", len(a.Response.Rules))
	}
}

func TestCompileSnapshot_RouteWithCredentials(t *testing.T) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "m1", DisplayName: "M1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "p1", Name: "P1", Selector: "openai", BaseURL: "https://api.example.com/v1", SDKKind: "openai", Protocol: "openai_chat", Status: "active"},
		},
		routes: []repository.RouteMapping{
			{ID: "r1", ModelID: "m1", ProviderID: "p1", UpstreamModel: "gpt-4", Priority: 100, Enabled: true, Protocol: "openai_chat", Status: "active"},
		},
		adapters: []repository.Adapter{},
	}

	credsByProvider := map[string][]repository.UpstreamCredential{
		"p1": {
			{ID: "c1", ProviderID: "p1", CredentialRef: "vault://p1/credential/c1", Priority: 100, Status: "active"},
			{ID: "c2", ProviderID: "p1", CredentialRef: "vault://p1/credential/c2", Priority: 50, Status: "active"},
		},
	}

	// No route_credentials → fallback to all provider credentials.
	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(snap.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(snap.Routes))
	}
	if len(snap.Routes[0].Credentials) != 2 {
		t.Errorf("route credentials count = %d, want 2", len(snap.Routes[0].Credentials))
	}
}

func TestCompileSnapshot_RouteCredentialsOverride(t *testing.T) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "m1", DisplayName: "M1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "p1", Name: "P1", Selector: "openai", BaseURL: "https://api.example.com/v1", SDKKind: "openai", Protocol: "openai_chat", Status: "active"},
		},
		routes: []repository.RouteMapping{
			{ID: "r1", ModelID: "m1", ProviderID: "p1", UpstreamModel: "gpt-4", Priority: 100, Enabled: true, Protocol: "openai_chat", Status: "active"},
		},
		adapters: []repository.Adapter{},
	}

	credsByProvider := map[string][]repository.UpstreamCredential{
		"p1": {
			{ID: "c1", ProviderID: "p1", CredentialRef: "vault://p1/credential/c1", Priority: 100, Status: "active"},
			{ID: "c2", ProviderID: "p1", CredentialRef: "vault://p1/credential/c2", Priority: 50, Status: "active"},
		},
	}

	// Route credentials only include c2.
	routeCredsByRoute := map[string][]repository.RouteCredential{
		"r1": {
			{RouteID: "r1", CredentialID: "c2", Priority: 200, Enabled: true},
		},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(snap.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(snap.Routes))
	}
	if len(snap.Routes[0].Credentials) != 1 {
		t.Fatalf("route credentials count = %d, want 1", len(snap.Routes[0].Credentials))
	}
	if snap.Routes[0].Credentials[0].ID != "c2" {
		t.Errorf("route credential ID = %q, want %q", snap.Routes[0].Credentials[0].ID, "c2")
	}
}

func TestCompileSnapshot_NoSecrets(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	// The snapshot must not contain any api_key, plaintext key, or secret.
	dataStr := string(data)
	forbidden := []string{"api_key", "APIKey", "\"sk-", "secret", "password"}
	for _, frag := range forbidden {
		if containsStr(dataStr, frag) {
			t.Errorf("snapshot contains forbidden fragment %q", frag)
		}
	}

	// CredentialRef must only contain vault:// references.
	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, route := range snap.Routes {
		for _, cred := range route.Credentials {
			if cred.CredentialRef != "" && !containsStr(cred.CredentialRef, "vault://") {
				t.Errorf("credential ref %q is not vault://", cred.CredentialRef)
			}
		}
	}
	for _, adapter := range snap.Adapters {
		if adapter.Auth.CredentialRef != "" && !containsStr(adapter.Auth.CredentialRef, "vault://") {
			t.Errorf("adapter auth credential ref %q is not vault://", adapter.Auth.CredentialRef)
		}
	}
}

func TestCompileSnapshot_GlobalDefaults(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(snap.Global.AutoModelIDs) != 1 || snap.Global.AutoModelIDs[0] != "chat-default" {
		t.Errorf("Global.AutoModelIDs = %v, want [chat-default]", snap.Global.AutoModelIDs)
	}
	if snap.Global.Retry.MaxTotalDuration != "45s" {
		t.Errorf("Global.Retry.MaxTotalDuration = %q, want %q", snap.Global.Retry.MaxTotalDuration, "45s")
	}
	if snap.Global.Timeout.RequestTimeout != "120s" {
		t.Errorf("Global.Timeout.RequestTimeout = %q, want %q", snap.Global.Timeout.RequestTimeout, "120s")
	}
}

// TestCompileSnapshot_GlobalOverride verifies that a configured global_config
// default_retry row overrides the built-in defaults, and that an invalid
// row falls back to defaults.
func TestCompileSnapshot_GlobalOverride(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	customRetry, _ := json.Marshal(wireRetryPolicy{
		Backoff:          "1s",
		MaxTotalDuration: "90s",
		Rules: []wireRetryRule{
			{ID: "custom-429", Priority: 5, HTTPStatuses: []int{429}, Action: "same_credential"},
			{ID: "custom-503", Priority: 10, HTTPStatuses: []int{503}, Action: "next_route"},
		},
	})

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{DefaultRetry: customRetry},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}
	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if snap.Global.Retry.Backoff != "1s" {
		t.Errorf("Backoff = %q, want 1s", snap.Global.Retry.Backoff)
	}
	if snap.Global.Retry.MaxTotalDuration != "90s" {
		t.Errorf("MaxTotalDuration = %q, want 90s", snap.Global.Retry.MaxTotalDuration)
	}
	if len(snap.Global.Retry.Rules) != 2 {
		t.Fatalf("Rules len = %d, want 2", len(snap.Global.Retry.Rules))
	}
	if snap.Global.Retry.Rules[0].ID != "custom-429" || snap.Global.Retry.Rules[0].Action != "same_credential" {
		t.Errorf("rule0 = %+v", snap.Global.Retry.Rules[0])
	}
	if snap.Global.Retry.Rules[1].ID != "custom-503" || snap.Global.Retry.Rules[1].Action != "next_route" {
		t.Errorf("rule1 = %+v", snap.Global.Retry.Rules[1])
	}

	// Invalid action row → fallback to defaults.
	badRetry, _ := json.Marshal(wireRetryPolicy{
		Rules: []wireRetryRule{{ID: "bad", Action: "bogus_action", HTTPStatuses: []int{500}}},
	})
	data2, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{DefaultRetry: badRetry},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}
	var snap2 wireConfigSnapshot
	if err := json.Unmarshal(data2, &snap2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Defaults have MaxTotalDuration "45s".
	if snap2.Global.Retry.MaxTotalDuration != "45s" {
		t.Errorf("invalid row did not fall back: MaxTotalDuration = %q", snap2.Global.Retry.MaxTotalDuration)
	}
	if len(snap2.Global.Retry.Rules) != 2 {
		t.Errorf("invalid row did not fall back: rules len = %d", len(snap2.Global.Retry.Rules))
	}
}

func TestCompileSnapshot_ThinkingNotSupported(t *testing.T) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "m1", DisplayName: "M1", Capabilities: repository.StringArray{"chat"}, ThinkingSupported: false, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "p1", Name: "P1", Selector: "openai", BaseURL: "https://api.example.com/v1", SDKKind: "openai", Protocol: "openai_chat", Status: "active"},
		},
		routes:   []repository.RouteMapping{},
		adapters: []repository.Adapter{},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		map[string][]repository.UpstreamCredential{},
		map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	m := snap.Models["m1"]
	if m.Thinking.Supported {
		t.Error("model Thinking.Supported = true, want false")
	}
	if m.Thinking.DefaultEffort != "" {
		t.Errorf("model Thinking.DefaultEffort = %q, want empty", m.Thinking.DefaultEffort)
	}
}

func TestCompileSnapshot_RevisionTimestamp(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if snap.Revision == "" {
		t.Error("Revision is empty")
	}
	if !containsStr(snap.Revision, "compile-") {
		t.Errorf("Revision = %q, want compile-<unix> format", snap.Revision)
	}
	if _, err := time.Parse(time.RFC3339, snap.CreatedAt); err != nil {
		t.Errorf("CreatedAt = %q, not valid RFC3339: %v", snap.CreatedAt, err)
	}
}

func TestCompileSnapshot_JSONKeysMatchDefaultFixture(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	// Verify all top-level keys match the default.json fixture format.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	expectedTopLevel := []string{"Revision", "CreatedAt", "Global", "Models", "Providers", "Routes", "Adapters"}
	for _, key := range expectedTopLevel {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing top-level key %q", key)
		}
	}

	// Verify Global sub-keys.
	var global map[string]json.RawMessage
	if err := json.Unmarshal(raw["Global"], &global); err != nil {
		t.Fatalf("unmarshal Global: %v", err)
	}
	for _, key := range []string{"Retry", "Timeout", "AutoModelIDs"} {
		if _, ok := global[key]; !ok {
			t.Errorf("Global missing key %q", key)
		}
	}

	// Verify Route sub-keys have uppercase names.
	var routes []map[string]json.RawMessage
	if err := json.Unmarshal(raw["Routes"], &routes); err != nil {
		t.Fatalf("unmarshal Routes: %v", err)
	}
	if len(routes) > 0 {
		expectedRouteKeys := []string{"ID", "ModelID", "ProviderID", "AdapterID", "UpstreamModel", "Priority", "Enabled", "Protocol", "Retry", "Timeout", "Credentials"}
		for _, key := range expectedRouteKeys {
			if _, ok := routes[0][key]; !ok {
				t.Errorf("Route missing key %q", key)
			}
		}
	}
}

func TestCompileSnapshot_EmptyData(t *testing.T) {
	reader := &fakeAdminReader{
		models:    []repository.Model{},
		providers: []repository.Provider{},
		routes:    []repository.RouteMapping{},
		adapters:  []repository.Adapter{},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		map[string][]repository.UpstreamCredential{},
		map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(snap.Models) != 0 {
		t.Errorf("expected 0 models, got %d", len(snap.Models))
	}
	if len(snap.Providers) != 0 {
		t.Errorf("expected 0 providers, got %d", len(snap.Providers))
	}
	if len(snap.Routes) != 0 {
		t.Errorf("expected 0 routes, got %d", len(snap.Routes))
	}
	if len(snap.Adapters) != 0 {
		t.Errorf("expected 0 adapters, got %d", len(snap.Adapters))
	}
}

func TestCompileSnapshot_DBAdapterPreferred(t *testing.T) {
	capRequire, _ := json.Marshal([]string{"chat", "streaming", "tools", "vision"})
	thinking, _ := json.Marshal(wireAdapterThinking{
		Supported:      true,
		DefaultEffort:  "low",
		EffortMapping:  defaultEffortMapping,
		BudgetMapping:  defaultBudgetMapping(16000),
		MinBudgetToken: 0,
		MaxBudgetToken: 16000,
	})

	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "m1", DisplayName: "M1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "p1", Name: "P1", Selector: "openai", BaseURL: "https://api.example.com/v1", SDKKind: "openai", Protocol: "openai_chat", Status: "active"},
		},
		routes: []repository.RouteMapping{
			{ID: "r1", ModelID: "m1", ProviderID: "p1", UpstreamModel: "gpt-4", Priority: 100, Enabled: true, Protocol: "openai_chat", Status: "active", AdapterID: strPtr("my-custom-adapter")},
		},
		adapters: []repository.Adapter{
			{
				ID:                "my-custom-adapter",
				Name:              "Custom Adapter",
				Version:           2,
				ProviderID:        strPtr("p1"),
				SDKKind:           "openai",
				Protocol:          "openai_chat",
				CapabilityRequire: capRequire,
				Thinking:          thinking,
				Status:            "active",
			},
		},
	}

	credsByProvider := map[string][]repository.UpstreamCredential{
		"p1": {
			{ID: "c1", ProviderID: "p1", CredentialRef: "vault://p1/credential/c1", Priority: 100, Status: "active"},
		},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// DB adapter should be present.
	if _, ok := snap.Adapters["my-custom-adapter"]; !ok {
		t.Error("DB adapter my-custom-adapter not found")
	}
	// Auto-generated adapter should NOT exist since provider has a DB adapter.
	if _, ok := snap.Adapters["adapter-p1"]; ok {
		t.Error("auto-generated adapter adapter-p1 should not exist when DB adapter with provider_id exists")
	}
	// Route should reference the DB adapter.
	if len(snap.Routes) != 1 || snap.Routes[0].AdapterID != "my-custom-adapter" {
		t.Errorf("route AdapterID = %q, want %q", snap.Routes[0].AdapterID, "my-custom-adapter")
	}
}

// ---- HTTP handler tests ----

func TestHandleAdminCompile(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()
	reader.credsByProvider = credsByProvider
	reader.routeCredsByRoute = routeCredsByRoute

	writer := newFakeWriter()
	s := New(nil, writer, fakePinger{}, nil)
	s.adminReader = reader

	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/compile", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Revision  string `json:"revision"`
			Published bool   `json:"published"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Data.Published {
		t.Error("published = false, want true")
	}
	if !containsStr(resp.Data.Revision, "compile-") {
		t.Errorf("revision = %q, want compile-<unix> format", resp.Data.Revision)
	}

	// Verify the draft was created and published.
	if len(writer.drafts) != 1 {
		t.Fatalf("expected 1 draft, got %d", len(writer.drafts))
	}
	for _, d := range writer.drafts {
		if d.Status != "published" {
			t.Errorf("draft status = %q, want %q", d.Status, "published")
		}
	}

	// Verify snapshot JSON was stored.
	if len(writer.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(writer.snapshots))
	}
	for _, snap := range writer.snapshots {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(snap, &raw); err != nil {
			t.Fatalf("snapshot unmarshal: %v", err)
		}
		if _, ok := raw["Revision"]; !ok {
			t.Error("snapshot missing Revision key")
		}
	}
}

func TestHandleAdminCompile_NoAdmin(t *testing.T) {
	s := New(nil, nil, fakePinger{}, nil)
	// adminReader is nil, writer is nil → registerAdminRoutes returns early,
	// so the route is not registered and chi returns 404.

	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/compile", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	// Route not registered → 404.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// ---- Strict decode validation (mirrors executor parseConfig) ----

// strictConfigSnapshot mirrors the executor's snapshot.ConfigSnapshot with
// uppercase JSON keys. Used to verify the compiled JSON passes the same
// DisallowUnknownFields check that executor's parseConfig performs.
type strictConfigSnapshot struct {
	Revision  string                    `json:"Revision"`
	CreatedAt string                    `json:"CreatedAt"`
	Global    strictGlobal              `json:"Global"`
	Models    map[string]strictModel    `json:"Models"`
	Providers map[string]strictProvider `json:"Providers"`
	Routes    []strictRoute             `json:"Routes"`
	Adapters  map[string]strictAdapter  `json:"Adapters"`
}

type strictGlobal struct {
	Retry        strictRetryPolicy   `json:"Retry"`
	Timeout      strictTimeoutPolicy `json:"Timeout"`
	AutoModelIDs []string            `json:"AutoModelIDs"`
}

type strictRetryPolicy struct {
	MaxTotalAttempts      *int              `json:"MaxTotalAttempts,omitempty"`
	MaxSameTargetAttempts *int              `json:"MaxSameTargetAttempts,omitempty"`
	MaxTotalDuration      string            `json:"MaxTotalDuration,omitempty"`
	Backoff               string            `json:"Backoff,omitempty"`
	Rules                 []strictRetryRule `json:"Rules,omitempty"`
}

type strictRetryRule struct {
	ID           string   `json:"ID"`
	Priority     int      `json:"Priority"`
	HTTPStatuses []int    `json:"HTTPStatuses"`
	ErrorCodes   []string `json:"ErrorCodes,omitempty"`
	ErrorTypes   []string `json:"ErrorTypes,omitempty"`
	Action       string   `json:"Action"`
}

type strictTimeoutPolicy struct {
	RequestTimeout    string `json:"RequestTimeout,omitempty"`
	TTFTTimeout       string `json:"TTFTTimeout,omitempty"`
	StreamIdleTimeout string `json:"StreamIdleTimeout,omitempty"`
	StreamMaxLifetime string `json:"StreamMaxLifetime,omitempty"`
	RetryBackoff      string `json:"RetryBackoff,omitempty"`
}

type strictModel struct {
	ID               string              `json:"ID"`
	DisplayName      string              `json:"DisplayName"`
	Capabilities     []string            `json:"Capabilities"`
	Thinking         strictModelThinking `json:"Thinking"`
	FallbackModelIDs []string            `json:"FallbackModelIDs,omitempty"`
}

type strictModelThinking struct {
	Supported      bool   `json:"Supported"`
	DefaultEffort  string `json:"DefaultEffort"`
	MaxEffort      string `json:"MaxEffort"`
	MinBudgetToken int    `json:"MinBudgetToken"`
	MaxBudgetToken int    `json:"MaxBudgetToken"`
}

type strictProvider struct {
	ID       string              `json:"ID"`
	Selector string              `json:"Selector"`
	Name     string              `json:"Name"`
	BaseURL  string              `json:"BaseURL"`
	SDKKind  string              `json:"SDKKind"`
	Protocol string              `json:"Protocol"`
	Retry    strictRetryPolicy   `json:"Retry"`
	Timeout  strictTimeoutPolicy `json:"Timeout"`
}

type strictRoute struct {
	ID               string              `json:"ID"`
	ModelID          string              `json:"ModelID"`
	ProviderID       string              `json:"ProviderID"`
	AdapterID        string              `json:"AdapterID"`
	UpstreamModel    string              `json:"UpstreamModel"`
	Priority         int                 `json:"Priority"`
	Enabled          bool                `json:"Enabled"`
	Protocol         string              `json:"Protocol"`
	Retry            strictRetryPolicy   `json:"Retry"`
	Timeout          strictTimeoutPolicy `json:"Timeout"`
	Credentials      []strictCredential  `json:"Credentials"`
	FallbackRouteIDs []string            `json:"FallbackRouteIDs,omitempty"`
	RouteGroup       string              `json:"RouteGroup,omitempty"`
}

type strictCredential struct {
	ID            string `json:"ID"`
	CredentialRef string `json:"CredentialRef"`
	Priority      int    `json:"Priority"`
	Enabled       bool   `json:"Enabled"`
}

type strictAdapter struct {
	ID         string                `json:"ID"`
	Name       string                `json:"Name"`
	Version    int                   `json:"Version"`
	SDKKind    string                `json:"SDKKind"`
	Protocol   string                `json:"Protocol"`
	Auth       strictAuth            `json:"Auth"`
	Capability strictCapability      `json:"Capability"`
	Thinking   strictAdapterThinking `json:"Thinking"`
	Request    strictRequest         `json:"Request"`
	Response   strictResponse        `json:"Response"`
	Retry      strictRetryPolicy     `json:"Retry"`
	Timeout    strictTimeoutPolicy   `json:"Timeout"`
}

type strictAuth struct {
	Kind          string `json:"Kind"`
	Header        string `json:"Header"`
	Query         string `json:"Query,omitempty"`
	Prefix        string `json:"Prefix"`
	CredentialRef string `json:"CredentialRef"`
}

type strictCapability struct {
	Require []string `json:"Require"`
	Deny    []string `json:"Deny"`
}

type strictAdapterThinking struct {
	Supported      bool              `json:"Supported"`
	DefaultEffort  string            `json:"DefaultEffort"`
	EffortMapping  map[string]string `json:"EffortMapping"`
	BudgetMapping  map[string]int    `json:"BudgetMapping"`
	MinBudgetToken int               `json:"MinBudgetToken"`
	MaxBudgetToken int               `json:"MaxBudgetToken"`
}

type strictRequest struct {
	AllowedHeaders []string            `json:"AllowedHeaders"`
	AllowedQuery   []string            `json:"AllowedQuery"`
	Rules          []strictRequestRule `json:"Rules"`
}

type strictRequestRule struct {
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

type strictResponse struct {
	Rules []strictResponseRule `json:"Rules"`
}

type strictResponseRule struct {
	ID       string               `json:"ID"`
	Priority int                  `json:"Priority"`
	Match    strictResponseMatch  `json:"Match"`
	Output   strictResponseOutput `json:"Output"`
}

type strictResponseMatch struct {
	HTTPStatuses     []int    `json:"HTTPStatuses"`
	ErrorCodes       []string `json:"ErrorCodes,omitempty"`
	ErrorTypes       []string `json:"ErrorTypes,omitempty"`
	MessageContains  []string `json:"MessageContains,omitempty"`
	FinishReasons    []string `json:"FinishReasons,omitempty"`
	StreamEventTypes []string `json:"StreamEventTypes,omitempty"`
}

type strictResponseOutput struct {
	HTTPStatus int    `json:"HTTPStatus"`
	ErrorCode  string `json:"ErrorCode"`
	ErrorType  string `json:"ErrorType"`
	Message    string `json:"Message"`
}

func TestCompileSnapshot_StrictDecode(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	// Verify the compiled JSON passes DisallowUnknownFields strict decode,
	// just like executor's parseConfig does.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg strictConfigSnapshot
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("DisallowUnknownFields decode: %v", err)
	}

	// Verify no trailing data (executor also checks this).
	rest := data[dec.InputOffset():]
	if len(bytes.TrimLeft(rest, " \t\n\r")) > 0 {
		t.Error("trailing data after JSON value")
	}

	// Spot-check decoded values.
	if cfg.Revision == "" {
		t.Error("Revision is empty")
	}
	if len(cfg.Models) != 1 {
		t.Errorf("Models count = %d, want 1", len(cfg.Models))
	}
	if len(cfg.Providers) != 1 {
		t.Errorf("Providers count = %d, want 1", len(cfg.Providers))
	}
	if len(cfg.Routes) != 1 {
		t.Errorf("Routes count = %d, want 1", len(cfg.Routes))
	}
	if len(cfg.Adapters) != 1 {
		t.Errorf("Adapters count = %d, want 1", len(cfg.Adapters))
	}
}

// ---- Capability mapping tests ----

func TestMapCapability(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"text", "chat"},
		{"image", "images"},
		{"chat", "chat"},
		{"responses", "responses"},
		{"messages", "messages"},
		{"images", "images"},
		{"streaming", "streaming"},
		{"tools", "tools"},
		{"vision", "vision"},
		{"thinking", "thinking"},
		{"unknown", ""},
		{"", ""},
		{"Text", ""}, // case-sensitive
	}
	for _, tt := range tests {
		got := mapCapability(tt.input)
		if got != tt.want {
			t.Errorf("mapCapability(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestMapCapabilities(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "text to chat",
			input: []string{"text"},
			want:  []string{"chat"},
		},
		{
			name:  "image to images",
			input: []string{"image"},
			want:  []string{"images"},
		},
		{
			name:  "deduplicate text and chat",
			input: []string{"text", "chat"},
			want:  []string{"chat"},
		},
		{
			name:  "deduplicate image and images",
			input: []string{"image", "images"},
			want:  []string{"images"},
		},
		{
			name:  "unknown values dropped",
			input: []string{"text", "unknown", "tools"},
			want:  []string{"chat", "tools"},
		},
		{
			name:  "all unknown dropped",
			input: []string{"foo", "bar"},
			want:  []string{},
		},
		{
			name:  "empty input",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "nil input",
			input: nil,
			want:  []string{},
		},
		{
			name:  "mixed admin and executor caps",
			input: []string{"text", "tools", "vision", "thinking"},
			want:  []string{"chat", "thinking", "tools", "vision"},
		},
		{
			name:  "glm-5.2 style capabilities",
			input: []string{"text", "tools", "vision"},
			want:  []string{"chat", "tools", "vision"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapCapabilities(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("mapCapabilities(%v) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("mapCapabilities(%v)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestCompileSnapshot_CapabilityMapping(t *testing.T) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{
				ID:           "glm-5.2",
				DisplayName:  "GLM 5.2",
				Capabilities: repository.StringArray{"text", "tools", "vision"},
				Status:       "active",
			},
			{
				ID:           "dall-e-4",
				DisplayName:  "DALL-E 4",
				Capabilities: repository.StringArray{"image"},
				Status:       "active",
			},
			{
				ID:           "model-unknown",
				DisplayName:  "Unknown Caps",
				Capabilities: repository.StringArray{"text", "bogus", "chat"},
				Status:       "active",
			},
		},
		providers: []repository.Provider{},
		routes:    []repository.RouteMapping{},
		adapters:  []repository.Adapter{},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		map[string][]repository.UpstreamCredential{},
		map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// glm-5.2: text→chat, tools/vision pass through
	glm := snap.Models["glm-5.2"]
	if !capsEqual(glm.Capabilities, []string{"chat", "tools", "vision"}) {
		t.Errorf("glm-5.2 Capabilities = %v, want [chat tools vision]", glm.Capabilities)
	}

	// dall-e-4: image→images
	dalle := snap.Models["dall-e-4"]
	if !capsEqual(dalle.Capabilities, []string{"images"}) {
		t.Errorf("dall-e-4 Capabilities = %v, want [images]", dalle.Capabilities)
	}

	// model-unknown: text→chat, bogus dropped, chat deduplicated
	mu := snap.Models["model-unknown"]
	if !capsEqual(mu.Capabilities, []string{"chat"}) {
		t.Errorf("model-unknown Capabilities = %v, want [chat]", mu.Capabilities)
	}
}

func TestCompileSnapshot_DBAdapterCapabilityMapping(t *testing.T) {
	capRequire, _ := json.Marshal([]string{"text", "streaming", "image"})
	capDeny, _ := json.Marshal([]string{"bogus"})

	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "m1", DisplayName: "M1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "p1", Name: "P1", Selector: "openai", BaseURL: "https://api.example.com/v1", SDKKind: "openai", Protocol: "openai_chat", Status: "active"},
		},
		routes: []repository.RouteMapping{
			{ID: "r1", ModelID: "m1", ProviderID: "p1", UpstreamModel: "gpt-4", Priority: 100, Enabled: true, Protocol: "openai_chat", Status: "active", AdapterID: strPtr("my-adapter")},
		},
		adapters: []repository.Adapter{
			{
				ID:                "my-adapter",
				Name:              "My Adapter",
				Version:           1,
				ProviderID:        strPtr("p1"),
				SDKKind:           "openai",
				Protocol:          "openai_chat",
				CapabilityRequire: capRequire,
				CapabilityDeny:    capDeny,
				Status:            "active",
			},
		},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		map[string][]repository.UpstreamCredential{},
		map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	a := snap.Adapters["my-adapter"]
	// text→chat, streaming passes, image→images; sorted
	if !capsEqual(a.Capability.Require, []string{"chat", "images", "streaming"}) {
		t.Errorf("adapter Require = %v, want [chat images streaming]", a.Capability.Require)
	}
	// bogus→empty, filtered out → empty deny
	if len(a.Capability.Deny) != 0 {
		t.Errorf("adapter Deny = %v, want empty", a.Capability.Deny)
	}
}

func TestCompileSnapshot_AnthropicAutoAdapterCapability(t *testing.T) {
	reader := &fakeAdminReader{
		models: []repository.Model{
			{ID: "m1", DisplayName: "M1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		},
		providers: []repository.Provider{
			{ID: "anthropic-default", Name: "Anthropic Default", Selector: "anthropic", BaseURL: "https://api.anthropic.example", SDKKind: "anthropic", Protocol: "anthropic_messages", Status: "active"},
		},
		routes:   []repository.RouteMapping{},
		adapters: []repository.Adapter{},
	}

	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		map[string][]repository.UpstreamCredential{},
		map[string][]repository.RouteCredential{},
		reader.adapters,
		repository.GlobalPolicy{},
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}

	var snap wireConfigSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	a := snap.Adapters["adapter-anthropic-default"]
	if !capsEqual(a.Capability.Require, []string{"messages"}) {
		t.Errorf("anthropic adapter Require = %v, want [messages]", a.Capability.Require)
	}
}

// helper
func capsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// helper
func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestCompileSnapshot_AutoModelIDsHonored verifies that when the global
// auto_model_ids config is a non-empty list of existing active model IDs,
// the compiled snapshot uses it verbatim (order preserved), rather than the
// default all-active-models-sorted fallback.
func TestCompileSnapshot_AutoModelIDsHonored(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()
	// Add two more active models so the fallback (sorted) would differ from
	// the configured order.
	reader.models = append(reader.models,
		repository.Model{ID: "glm-5.1", DisplayName: "GLM 5.1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		repository.Model{ID: "glm-5.2", DisplayName: "GLM 5.2", Capabilities: repository.StringArray{"chat"}, Status: "active"},
	)
	global := repository.GlobalPolicy{
		AutoModelIDs: json.RawMessage(`["glm-5.2","glm-5.1","chat-default"]`),
	}
	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		global,
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}
	var snap struct {
		Global struct {
			AutoModelIDs []string `json:"AutoModelIDs"`
		} `json:"Global"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"glm-5.2", "glm-5.1", "chat-default"}
	if len(snap.Global.AutoModelIDs) != len(want) {
		t.Fatalf("AutoModelIDs len = %d, want %d (%v)", len(snap.Global.AutoModelIDs), len(want), snap.Global.AutoModelIDs)
	}
	for i, id := range want {
		if snap.Global.AutoModelIDs[i] != id {
			t.Errorf("AutoModelIDs[%d] = %q, want %q (full=%v)", i, snap.Global.AutoModelIDs[i], id, snap.Global.AutoModelIDs)
		}
	}
}

// TestCompileSnapshot_AutoModelIDsFallback verifies that an empty/invalid
// auto_model_ids config falls back to all active models sorted by ID.
func TestCompileSnapshot_AutoModelIDsFallback(t *testing.T) {
	reader, credsByProvider, routeCredsByRoute := sampleData()
	reader.models = append(reader.models,
		repository.Model{ID: "glm-5.1", DisplayName: "GLM 5.1", Capabilities: repository.StringArray{"chat"}, Status: "active"},
		repository.Model{ID: "glm-5.2", DisplayName: "GLM 5.2", Capabilities: repository.StringArray{"chat"}, Status: "active"},
	)
	// References a non-existent model -> must fall back.
	global := repository.GlobalPolicy{
		AutoModelIDs: json.RawMessage(`["glm-5.2","unknown-model"]`),
	}
	data, err := compileSnapshot(
		reader.models, reader.providers, reader.routes,
		credsByProvider, routeCredsByRoute, reader.adapters,
		global,
	)
	if err != nil {
		t.Fatalf("compileSnapshot: %v", err)
	}
	var snap struct {
		Global struct {
			AutoModelIDs []string `json:"AutoModelIDs"`
		} `json:"Global"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"chat-default", "glm-5.1", "glm-5.2"}
	if len(snap.Global.AutoModelIDs) != len(want) {
		t.Fatalf("AutoModelIDs len = %d, want %d (%v)", len(snap.Global.AutoModelIDs), len(want), snap.Global.AutoModelIDs)
	}
	for i, id := range want {
		if snap.Global.AutoModelIDs[i] != id {
			t.Errorf("AutoModelIDs[%d] = %q, want %q (full=%v)", i, snap.Global.AutoModelIDs[i], id, snap.Global.AutoModelIDs)
		}
	}
}
