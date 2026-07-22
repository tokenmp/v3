package modelcatalogfacade

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/modelcatalog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// trustedPrincipal is the trusted authenticated Principal a transport auth
// boundary would carry on a catalog request.
func trustedPrincipal() modelcatalog.Principal {
	return modelcatalog.Principal{Subject: "svc-1", KeyID: "key-1", Role: modelcatalog.RoleService, Status: modelcatalog.StatusActive}
}

// noopQuarantine is a QuarantineReader that consults state and finds nothing
// excluded for any target: it returns routing.ErrNotFound.
type noopQuarantine struct{}

func (noopQuarantine) GetQuarantine(_ context.Context, _ routing.QuarantineTarget) (routing.Quarantine, error) {
	return routing.Quarantine{}, routing.ErrNotFound
}

// stubErrQuarantine is a QuarantineReader that always fails with the given error.
type stubErrQuarantine struct{ err error }

func (s stubErrQuarantine) GetQuarantine(context.Context, routing.QuarantineTarget) (routing.Quarantine, error) {
	return routing.Quarantine{}, s.err
}

// activeQuarantine is a QuarantineReader that returns an active quarantine
// for the specified model ID.
type activeQuarantine struct {
	modelID string
	until   time.Time
}

func (a *activeQuarantine) GetQuarantine(_ context.Context, target routing.QuarantineTarget) (routing.Quarantine, error) {
	if target.ModelID == a.modelID {
		return routing.Quarantine{Until: a.until}, nil
	}
	return routing.Quarantine{}, routing.ErrNotFound
}

// expiredQuarantine is a QuarantineReader that returns an expired quarantine
// for the specified model ID.
type expiredQuarantine struct {
	modelID string
}

func (e *expiredQuarantine) GetQuarantine(_ context.Context, target routing.QuarantineTarget) (routing.Quarantine, error) {
	if target.ModelID == e.modelID {
		return routing.Quarantine{Until: time.Now().Add(-time.Hour)}, nil
	}
	return routing.Quarantine{}, routing.ErrNotFound
}

// stubClock is a deterministic Clock for testing.
type stubClock struct{ t time.Time }

func (c *stubClock) Now() time.Time { return c.t }

// buildStoreWithModels compiles a config with the given models and routes,
// and publishes generation 1 with a known CreatedAt.
func buildStoreWithModels(t *testing.T, models map[string]adapter.ModelInput, routes []adapter.RouteInput, createdAt time.Time) *snapshot.Store {
	t.Helper()
	compiled, err := adapter.Compile(adapter.ConfigInput{
		Revision:  "catalog-test",
		Models:    models,
		Providers: map[string]adapter.ProviderInput{
			"openai":    {ID: "openai", Name: "openai", Selector: "openai", BaseURL: "https://openai.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
			"anthropic": {ID: "anthropic", Name: "anthropic", Selector: "anthropic", BaseURL: "https://anthropic.example/v1", SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic},
			"openai-img": {ID: "openai-img", Name: "OpenAI Images", Selector: "openai-img", BaseURL: "https://openai.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIImages},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"chat-adapter": {
				ID: "chat-adapter", Name: "chat-adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat,
				Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
			},
			"anthropic-adapter": {
				ID: "anthropic-adapter", Name: "anthropic-adapter", Version: 1, SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic,
				Auth: adapter.AuthRule{Kind: adapter.AuthAPIKeyHeader, Header: "x-api-key"},
			},
			"image-adapter": {
				ID: "image-adapter", Name: "image-adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIImages,
				Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
			},
		},
		Routes: routes,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshotWithTime(compiled.Revision, &compiled, 1, createdAt)
	if err != nil {
		t.Fatalf("NewCompiledSnapshotWithTime: %v", err)
	}
	store := &snapshot.Store{}
	if err := store.Publish(source); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return store
}

func defaultRoutes() []adapter.RouteInput {
	return []adapter.RouteInput{
		{
			ID: "chat-route", ModelID: "chat-model", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "gpt-upstream",
			Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat,
			Credentials: []adapter.CredentialInput{{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true}},
		},
		{
			ID: "anthropic-route", ModelID: "anthropic-model", ProviderID: "anthropic", AdapterID: "anthropic-adapter", UpstreamModel: "claude-upstream",
			Priority: 1, Enabled: true, Protocol: adapter.ProtocolAnthropic,
			Credentials: []adapter.CredentialInput{{ID: "cred-b", CredentialRef: "vault://private/cred-b", Priority: 1, Enabled: true}},
		},
	}
}

func defaultModels() map[string]adapter.ModelInput {
	return map[string]adapter.ModelInput{
		"chat-model":      {ID: "chat-model", Capabilities: []adapter.Capability{adapter.CapabilityChat, adapter.CapabilityStreaming}},
		"anthropic-model": {ID: "anthropic-model", Capabilities: []adapter.Capability{adapter.CapabilityMessages, adapter.CapabilityChat}},
	}
}

func TestListModelsReturnsEnabledModels(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), createdAt)
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 2 {
		t.Fatalf("len(Models) = %d, want 2", len(result.Models))
	}

	// Models should be sorted by ID.
	if result.Models[0].ID != "anthropic-model" {
		t.Errorf("Models[0].ID = %q, want %q", result.Models[0].ID, "anthropic-model")
	}
	if result.Models[1].ID != "chat-model" {
		t.Errorf("Models[1].ID = %q, want %q", result.Models[1].ID, "chat-model")
	}

	// Check capabilities: chat→text, messages→omitted, streaming→omitted.
	if len(result.Models[0].Capabilities) != 1 || result.Models[0].Capabilities[0] != "text" {
		t.Errorf("anthropic-model Capabilities = %v, want [text]", result.Models[0].Capabilities)
	}
	if len(result.Models[1].Capabilities) != 1 || result.Models[1].Capabilities[0] != "text" {
		t.Errorf("chat-model Capabilities = %v, want [text]", result.Models[1].Capabilities)
	}

	// Check created timestamp.
	wantCreated := int(createdAt.Unix())
	for _, m := range result.Models {
		if m.Created != wantCreated {
			t.Errorf("Created = %d, want %d", m.Created, wantCreated)
		}
	}
}

func TestListModelsExcludesQuarantinedModels(t *testing.T) {
	t.Parallel()

	store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), time.Time{})
	q := &activeQuarantine{modelID: "chat-model", until: time.Now().Add(time.Hour)}
	facade := New(Options{Store: store, Quarantine: q})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1 (quarantined model excluded)", len(result.Models))
	}
	if result.Models[0].ID != "anthropic-model" {
		t.Errorf("Models[0].ID = %q, want anthropic-model", result.Models[0].ID)
	}
}

func TestListModelsIncludesExpiredQuarantine(t *testing.T) {
	t.Parallel()

	store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), time.Time{})
	q := &expiredQuarantine{modelID: "chat-model"}
	facade := New(Options{Store: store, Quarantine: q})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 2 {
		t.Fatalf("len(Models) = %d, want 2 (expired quarantine should not exclude)", len(result.Models))
	}
}

func TestListModelsQuarantineUnavailableFailsClosed(t *testing.T) {
	t.Parallel()

	store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), time.Time{})
	q := stubErrQuarantine{err: errors.New("state unavailable")}
	facade := New(Options{Store: store, Quarantine: q})

	_, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if !errors.Is(err, modelcatalog.ErrQuarantineUnavailable) {
		t.Fatalf("error = %v, want ErrQuarantineUnavailable", err)
	}
}

func TestListModelsExcludesModelsWithNoEnabledRoute(t *testing.T) {
	t.Parallel()

	models := map[string]adapter.ModelInput{
		"enabled-model":  {ID: "enabled-model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		"disabled-model": {ID: "disabled-model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
	}
	routes := []adapter.RouteInput{
		{
			ID: "enabled-route", ModelID: "enabled-model", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "gpt-upstream",
			Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat,
			Credentials: []adapter.CredentialInput{{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true}},
		},
		{
			ID: "disabled-route", ModelID: "disabled-model", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "gpt-upstream",
			Priority: 2, Enabled: false, Protocol: adapter.ProtocolOpenAIChat,
			Credentials: []adapter.CredentialInput{{ID: "cred-b", CredentialRef: "vault://private/cred-b", Priority: 1, Enabled: true}},
		},
	}
	store := buildStoreWithModels(t, models, routes, time.Time{})
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1 (model with no enabled route excluded)", len(result.Models))
	}
	if result.Models[0].ID != "enabled-model" {
		t.Errorf("Models[0].ID = %q, want enabled-model", result.Models[0].ID)
	}
}

func TestListModelsUnauthenticatedFails(t *testing.T) {
	t.Parallel()

	store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), time.Time{})
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	_, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: modelcatalog.Principal{}})
	if !errors.Is(err, modelcatalog.ErrUnauthenticated) {
		t.Fatalf("error = %v, want ErrUnauthenticated", err)
	}
}

func TestListModelsMisconfiguredFails(t *testing.T) {
	t.Parallel()

	t.Run("nil store", func(t *testing.T) {
		t.Parallel()
		facade := New(Options{Store: nil, Quarantine: noopQuarantine{}})
		_, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
		if !errors.Is(err, modelcatalog.ErrMisconfigured) {
			t.Fatalf("error = %v, want ErrMisconfigured", err)
		}
	})

	t.Run("nil quarantine", func(t *testing.T) {
		t.Parallel()
		store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), time.Time{})
		facade := New(Options{Store: store, Quarantine: nil})
		_, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
		if !errors.Is(err, modelcatalog.ErrMisconfigured) {
			t.Fatalf("error = %v, want ErrMisconfigured", err)
		}
	})
}

func TestListModelsNoSnapshotFails(t *testing.T) {
	t.Parallel()

	store := &snapshot.Store{} // empty store, no published snapshot
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	_, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if !errors.Is(err, modelcatalog.ErrNoSnapshot) {
		t.Fatalf("error = %v, want ErrNoSnapshot", err)
	}
}

func TestListModelsEmptyConfigReturnsEmpty(t *testing.T) {
	t.Parallel()

	compiled, err := adapter.Compile(adapter.ConfigInput{
		Revision: "empty-test",
		Models:   map[string]adapter.ModelInput{},
		Providers: map[string]adapter.ProviderInput{
			"openai": {ID: "openai", Name: "openai", Selector: "openai", BaseURL: "https://openai.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"chat-adapter": {
				ID: "chat-adapter", Name: "chat-adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat,
				Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
			},
		},
		Routes: []adapter.RouteInput{},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(compiled.Revision, &compiled, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	store := &snapshot.Store{}
	if err := store.Publish(source); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})
	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 0 {
		t.Fatalf("len(Models) = %d, want 0", len(result.Models))
	}
}

func TestListModelsMultipleModelsSorted(t *testing.T) {
	t.Parallel()

	models := map[string]adapter.ModelInput{
		"zebra":  {ID: "zebra", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		"alpha":  {ID: "alpha", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		"middle": {ID: "middle", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
	}
	routes := []adapter.RouteInput{
		{ID: "r1", ModelID: "zebra", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "z", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "c1", CredentialRef: "vault://p/c1", Priority: 1, Enabled: true}}},
		{ID: "r2", ModelID: "alpha", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "a", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "c2", CredentialRef: "vault://p/c2", Priority: 1, Enabled: true}}},
		{ID: "r3", ModelID: "middle", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "m", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "c3", CredentialRef: "vault://p/c3", Priority: 1, Enabled: true}}},
	}
	store := buildStoreWithModels(t, models, routes, time.Time{})
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 3 {
		t.Fatalf("len(Models) = %d, want 3", len(result.Models))
	}
	want := []string{"alpha", "middle", "zebra"}
	for i, m := range result.Models {
		if m.ID != want[i] {
			t.Errorf("Models[%d].ID = %q, want %q", i, m.ID, want[i])
		}
	}
}

func TestListModelsThinkingMapping(t *testing.T) {
	t.Parallel()

	models := map[string]adapter.ModelInput{
		"thinking-model": {
			ID:           "thinking-model",
			Capabilities: []adapter.Capability{adapter.CapabilityChat, adapter.CapabilityThinking},
			Thinking: adapter.ThinkingInput{
				Supported:      true,
				DefaultEffort:  adapter.ThinkingMedium,
				MaxEffort:      adapter.ThinkingHigh,
				MinBudgetToken: 1024,
				MaxBudgetToken: 64000,
			},
		},
	}
	routes := []adapter.RouteInput{
		{ID: "r1", ModelID: "thinking-model", ProviderID: "openai", AdapterID: "thinking-chat-adapter", UpstreamModel: "t", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "c1", CredentialRef: "vault://p/c1", Priority: 1, Enabled: true}}},
	}

	// Build a custom store with a thinking-capable adapter.
	compiled, err := adapter.Compile(adapter.ConfigInput{
		Revision: "thinking-test",
		Models:   models,
		Providers: map[string]adapter.ProviderInput{
			"openai": {ID: "openai", Name: "openai", Selector: "openai", BaseURL: "https://openai.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"thinking-chat-adapter": {
				ID: "thinking-chat-adapter", Name: "thinking-chat-adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat,
				Auth:       adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
				Capability: adapter.CapabilityPolicy{Require: []adapter.Capability{adapter.CapabilityChat, adapter.CapabilityThinking}, Deny: []adapter.Capability{}},
				Thinking: adapter.ThinkingPolicy{
					Supported:      true,
					DefaultEffort:  adapter.ThinkingMedium,
					EffortMapping:  map[adapter.ThinkingEffort]adapter.ThinkingEffort{adapter.ThinkingNone: adapter.ThinkingNone, adapter.ThinkingMinimal: adapter.ThinkingMinimal, adapter.ThinkingLow: adapter.ThinkingLow, adapter.ThinkingMedium: adapter.ThinkingMedium, adapter.ThinkingHigh: adapter.ThinkingHigh, adapter.ThinkingXHigh: adapter.ThinkingHigh, adapter.ThinkingMax: adapter.ThinkingHigh},
					BudgetMapping:  map[adapter.ThinkingEffort]int{adapter.ThinkingNone: 1024, adapter.ThinkingMinimal: 1024, adapter.ThinkingLow: 2048, adapter.ThinkingMedium: 8192, adapter.ThinkingHigh: 64000, adapter.ThinkingXHigh: 64000, adapter.ThinkingMax: 64000},
					MinBudgetToken: 0,
					MaxBudgetToken: 64000,
				},
				Request:  adapter.RequestPolicy{AllowedHeaders: []string{"Content-Type"}, AllowedQuery: []string{}, Rules: []adapter.RequestRule{}},
				Response: adapter.ResponsePolicy{Rules: []adapter.ResponseRule{}},
				Retry:    adapter.RetryPolicy{},
				Timeout:  adapter.TimeoutPolicy{},
			},
		},
		Routes: routes,
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(compiled.Revision, &compiled, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	store := &snapshot.Store{}
	if err := store.Publish(source); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1", len(result.Models))
	}
	m := result.Models[0]
	if m.Thinking == nil {
		t.Fatal("Thinking is nil, want non-nil")
	}
	if !m.Thinking.Supported {
		t.Error("Thinking.Supported = false, want true")
	}
	if m.Thinking.DefaultEffort != "medium" {
		t.Errorf("DefaultEffort = %q, want medium", m.Thinking.DefaultEffort)
	}
	if m.Thinking.MaxEffort != "high" {
		t.Errorf("MaxEffort = %q, want high", m.Thinking.MaxEffort)
	}
	if m.Thinking.MinBudgetTokens == nil || *m.Thinking.MinBudgetTokens != 1024 {
		t.Errorf("MinBudgetTokens = %v, want 1024", m.Thinking.MinBudgetTokens)
	}
	if m.Thinking.MaxBudgetTokens == nil || *m.Thinking.MaxBudgetTokens != 64000 {
		t.Errorf("MaxBudgetTokens = %v, want 64000", m.Thinking.MaxBudgetTokens)
	}
	// Capabilities should include both text and thinking.
	wantCaps := []string{"text", "thinking"}
	if len(m.Capabilities) != len(wantCaps) {
		t.Fatalf("Capabilities = %v, want %v", m.Capabilities, wantCaps)
	}
	for i, c := range m.Capabilities {
		if c != wantCaps[i] {
			t.Errorf("Capabilities[%d] = %q, want %q", i, c, wantCaps[i])
		}
	}
}

func TestListModelsNoThinkingOmitsField(t *testing.T) {
	t.Parallel()

	models := map[string]adapter.ModelInput{
		"no-thinking": {ID: "no-thinking", Capabilities: []adapter.Capability{adapter.CapabilityChat}, Thinking: adapter.ThinkingInput{Supported: false}},
	}
	routes := []adapter.RouteInput{
		{ID: "r1", ModelID: "no-thinking", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "n", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CredentialInput{{ID: "c1", CredentialRef: "vault://p/c1", Priority: 1, Enabled: true}}},
	}
	store := buildStoreWithModels(t, models, routes, time.Time{})
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(result.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1", len(result.Models))
	}
	if result.Models[0].Thinking != nil {
		t.Errorf("Thinking = %v, want nil", result.Models[0].Thinking)
	}
}

func TestListModelsRace(t *testing.T) {
	store := buildStoreWithModels(t, defaultModels(), defaultRoutes(), time.Time{})
	facade := New(Options{Store: store, Quarantine: noopQuarantine{}})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := facade.ListModels(context.Background(), modelcatalog.CatalogRequest{Principal: trustedPrincipal()})
			if err != nil {
				t.Errorf("ListModels() error = %v", err)
				return
			}
			if len(result.Models) != 2 {
				t.Errorf("len(Models) = %d, want 2", len(result.Models))
			}
		}()
	}
	wg.Wait()
}
