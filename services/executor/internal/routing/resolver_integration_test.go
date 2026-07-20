package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

func integrationDefaultConfig(t *testing.T) snapshot.ConfigSnapshot {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(thisFile))), "fixtures", "configs", "default.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config snapshot.ConfigSnapshot
	if err := decoder.Decode(&config); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return config
}

func compileRoutingIntegrationSnapshot(t *testing.T, config snapshot.ConfigSnapshot, generation uint64) *snapshot.CompiledSnapshot {
	t.Helper()
	compiled, err := snapshot.Compile(config)
	if err != nil {
		t.Fatalf("snapshot.Compile(%q): %v", config.Revision, err)
	}
	frozen, err := snapshot.NewCompiledSnapshot(config.Revision, &compiled, generation)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(%q): %v", config.Revision, err)
	}
	return frozen
}

func routingIntegrationConfig(t *testing.T) snapshot.ConfigSnapshot {
	t.Helper()
	config := integrationDefaultConfig(t)
	config.Revision = "routing-integration"
	config.Global.AutoModelIDs = []string{"chat-default", "model-b"}

	primary := config.Routes[0]
	primary.ID = "primary"
	primary.Priority = 10
	primary.RouteGroup = "g"
	primary.FallbackRouteIDs = []string{"secondary"}
	primary.Credentials = []snapshot.CredentialConfig{
		{ID: "primary-1", CredentialRef: "vault://routing/primary-one", Priority: 1, Enabled: true},
		{ID: "primary-2", CredentialRef: "vault://routing/primary-two", Priority: 2, Enabled: true},
	}
	config.Routes = []snapshot.RouteConfig{primary}
	adapterConfig := config.Adapters[primary.AdapterID]
	adapterConfig.Auth.CredentialRef = ""
	config.Adapters[primary.AdapterID] = adapterConfig

	modelB := config.Models["chat-default"]
	modelB.ID = "model-b"
	config.Models["chat-default"] = snapshot.ModelConfig{
		ID:               "chat-default",
		DisplayName:      config.Models["chat-default"].DisplayName,
		Capabilities:     config.Models["chat-default"].Capabilities,
		Thinking:         config.Models["chat-default"].Thinking,
		FallbackModelIDs: []string{"model-b"},
	}
	config.Models["model-b"] = modelB

	providerOther := config.Providers["openai-default"]
	providerOther.ID = "provider-other"
	providerOther.Name = "Other Provider"
	providerOther.Selector = "other"
	providerOther.BaseURL = "https://other.example/v1"
	config.Providers[providerOther.ID] = providerOther

	config.Routes = append(config.Routes,
		snapshot.RouteConfig{ID: "secondary", ModelID: "chat-default", ProviderID: "openai-default", AdapterID: primary.AdapterID, UpstreamModel: "secondary", Priority: 20, Enabled: true, Protocol: primary.Protocol, RouteGroup: "g", FallbackRouteIDs: []string{"other-provider"}, Credentials: []snapshot.CredentialConfig{{ID: "secondary-1", CredentialRef: "vault://routing/secondary", Enabled: true}}},
		snapshot.RouteConfig{ID: "route-b", ModelID: "model-b", ProviderID: "openai-default", AdapterID: primary.AdapterID, UpstreamModel: "b", Priority: 20, Enabled: true, Protocol: primary.Protocol, RouteGroup: "g", Credentials: []snapshot.CredentialConfig{{ID: "b-1", CredentialRef: "vault://routing/b", Enabled: true}}},
		snapshot.RouteConfig{ID: "other-provider", ModelID: "chat-default", ProviderID: providerOther.ID, AdapterID: primary.AdapterID, UpstreamModel: "other", Priority: 30, Enabled: true, Protocol: primary.Protocol, RouteGroup: "g", Credentials: []snapshot.CredentialConfig{{ID: "other-1", CredentialRef: "vault://routing/other", Enabled: true}}},
		snapshot.RouteConfig{ID: "other-group", ModelID: "chat-default", ProviderID: "openai-default", AdapterID: primary.AdapterID, UpstreamModel: "other-group", Priority: 40, Enabled: true, Protocol: primary.Protocol, RouteGroup: "other", Credentials: []snapshot.CredentialConfig{{ID: "group-1", CredentialRef: "vault://routing/group", Enabled: true}}},
	)
	return config
}

func integrationResolver(t *testing.T, source *snapshot.CompiledSnapshot, quarantine QuarantineReader) *Resolver {
	t.Helper()
	resolver, err := NewResolver(source, quarantine, fixedClock{now: time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func integrationCandidateIDs(candidates []Candidate) []string {
	ids := make([]string, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.ModelID + "/" + candidate.RouteID + "/" + candidate.Credential.ID
	}
	return ids
}

func requireCandidateIDs(t *testing.T, candidates []Candidate, want ...string) {
	t.Helper()
	got := integrationCandidateIDs(candidates)
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidates[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestResolverIntegrationCompileFixtureAndSelectors(t *testing.T) {
	legacy := integrationDefaultConfig(t)
	legacySnapshot := compileRoutingIntegrationSnapshot(t, legacy, 11)
	legacyPlan, err := integrationResolver(t, legacySnapshot, nil).Resolve(context.Background(), Selector{Model: "chat-default"})
	if err != nil {
		t.Fatalf("Resolve legacy fixture: %v", err)
	}
	if len(legacyPlan.Candidates) != 1 || legacyPlan.Candidates[0].Credential.ID == "" || legacyPlan.Candidates[0].Credential.Ref != "vault://openai-default/credential/default" {
		t.Fatalf("legacy fixture candidate = %+v, want synthesized non-secret legacy credential", legacyPlan.Candidates)
	}

	config := routingIntegrationConfig(t)
	resolver := integrationResolver(t, compileRoutingIntegrationSnapshot(t, config, 12), nil)
	cases := []struct {
		name     string
		selector Selector
		want     []string
	}{
		{"model with explicit credentials", Selector{Model: "chat-default"}, []string{"chat-default/primary/primary-1", "chat-default/primary/primary-2", "chat-default/secondary/secondary-1", "chat-default/other-provider/other-1", "chat-default/other-group/group-1"}},
		{"auto", Selector{Model: "auto", Auto: true}, []string{"chat-default/primary/primary-1", "chat-default/primary/primary-2", "chat-default/secondary/secondary-1", "chat-default/other-provider/other-1", "chat-default/other-group/group-1", "model-b/route-b/b-1"}},
		{"group", Selector{Model: "chat-default", Group: "g"}, []string{"chat-default/primary/primary-1", "chat-default/primary/primary-2", "chat-default/secondary/secondary-1", "chat-default/other-provider/other-1"}},
		{"provider selector", Selector{Model: "chat-default", Provider: "other"}, []string{"chat-default/other-provider/other-1"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			plan, err := resolver.Resolve(context.Background(), test.selector)
			if err != nil {
				t.Fatalf("Resolve(%s): %v", test.selector, err)
			}
			requireCandidateIDs(t, plan.Candidates, test.want...)
		})
	}
}

func TestResolverIntegrationQuarantineDimensionsOnlyFilterMatches(t *testing.T) {
	config := routingIntegrationConfig(t)
	source := compileRoutingIntegrationSnapshot(t, config, 13)
	unfiltered, err := integrationResolver(t, source, nil).Resolve(context.Background(), Selector{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	for _, target := range []QuarantineTarget{
		{ModelID: "chat-default"},
		{ProviderID: "provider-other"},
		{RouteID: "primary"},
		{CredentialID: "primary-1"},
	} {
		t.Run("active_"+target.ModelID+target.ProviderID+target.RouteID+target.CredentialID, func(t *testing.T) {
			reader := &quarantineReader{byTarget: map[QuarantineTarget]Quarantine{target: {Until: now.Add(time.Minute)}}}
			plan, err := integrationResolver(t, source, reader).Resolve(context.Background(), Selector{})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			for _, candidate := range plan.Candidates {
				if (target.ModelID != "" && candidate.ModelID == target.ModelID) || (target.ProviderID != "" && candidate.Provider.ID == target.ProviderID) || (target.RouteID != "" && candidate.RouteID == target.RouteID) || (target.CredentialID != "" && candidate.Credential.ID == target.CredentialID) {
					t.Errorf("active quarantine %v retained matching candidate %+v", target, candidate)
				}
			}
			for _, candidate := range unfiltered.Candidates {
				matches := (target.ModelID != "" && candidate.ModelID == target.ModelID) || (target.ProviderID != "" && candidate.Provider.ID == target.ProviderID) || (target.RouteID != "" && candidate.RouteID == target.RouteID) || (target.CredentialID != "" && candidate.Credential.ID == target.CredentialID)
				if !matches && !containsTarget(plan.Candidates, candidate.target()) {
					t.Errorf("active quarantine %v incorrectly filtered non-matching candidate %+v", target, candidate)
				}
			}
		})
	}
}

func containsTarget(candidates []Candidate, want QuarantineTarget) bool {
	for _, candidate := range candidates {
		if candidate.target() == want {
			return true
		}
	}
	return false
}

func TestResolverIntegrationNextUsesFrozenPrivateUniverse(t *testing.T) {
	resolver := integrationResolver(t, compileRoutingIntegrationSnapshot(t, routingIntegrationConfig(t), 14), nil)
	plan, err := resolver.Resolve(context.Background(), Selector{Model: "chat-default", Group: "g"})
	if err != nil {
		t.Fatal(err)
	}
	current := plan.Candidates[0].target()

	// Public candidates are caller-owned output. Replacing them must not alter
	// any retry action's private, selector-scoped universe.
	plan.Candidates = []Candidate{{ModelID: "attacker", RouteID: "attacker", Credential: Credential{ID: "attacker"}}}
	for _, test := range []struct {
		action adapter.RetryAction
		want   string
	}{
		{adapter.RetrySameCredential, "chat-default/primary/primary-1"},
		{adapter.RetryNextCredential, "chat-default/primary/primary-2"},
		{adapter.RetryNextRoute, "chat-default/secondary/secondary-1"},
		{adapter.RetryNextProvider, "chat-default/other-provider/other-1"},
		{adapter.RetryNextModel, "model-b/route-b/b-1"},
	} {
		got, ok := plan.Next(test.action, current, nil)
		if !ok || integrationCandidateIDs([]Candidate{got})[0] != test.want {
			t.Errorf("Next(%s) = (%+v, %t), want %s", test.action, got, ok, test.want)
		}
		if ok && got.ModelID == "attacker" {
			t.Errorf("Next(%s) used malicious public Candidates", test.action)
		}
	}

	visited := map[QuarantineTarget]struct{}{
		{ModelID: "chat-default", ProviderID: "openai-default", RouteID: "primary", CredentialID: "primary-2"}:      {},
		{ModelID: "chat-default", ProviderID: "openai-default", RouteID: "secondary", CredentialID: "secondary-1"}:  {},
		{ModelID: "chat-default", ProviderID: "provider-other", RouteID: "other-provider", CredentialID: "other-1"}: {},
	}
	if _, ok := plan.Next(adapter.RetryNextCredential, current, visited); ok {
		t.Error("NextCredential ignored visited target")
	}
	if _, ok := plan.Next(adapter.RetryNextRoute, current, visited); ok {
		t.Error("NextRoute escaped its group or ignored exhausted candidates")
	}
	if _, ok := plan.Next(adapter.RetryNextProvider, current, visited); ok {
		t.Error("NextProvider ignored visited target")
	}
	if _, ok := plan.Next(adapter.RetryNextModel, current, map[QuarantineTarget]struct{}{{ModelID: "model-b", ProviderID: "openai-default", RouteID: "route-b", CredentialID: "b-1"}: {}}); ok {
		t.Error("NextModel ignored exhausted candidate")
	}
	if _, ok := plan.Next(adapter.RetryNextRoute, QuarantineTarget{ModelID: "attacker"}, nil); ok {
		t.Error("Next accepted a target outside its frozen scope")
	}
}
