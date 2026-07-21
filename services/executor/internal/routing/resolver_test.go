package routing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type quarantineReader struct {
	byTarget map[QuarantineTarget]Quarantine
	err      error
	seen     []QuarantineTarget
}

func (q *quarantineReader) GetQuarantine(_ context.Context, target QuarantineTarget) (Quarantine, error) {
	q.seen = append(q.seen, target)
	if q.err != nil {
		return Quarantine{}, q.err
	}
	value, ok := q.byTarget[target]
	if !ok {
		return Quarantine{}, ErrNotFound
	}
	return value, nil
}

func testSnapshot(t *testing.T, revision string, generation uint64) *snapshot.CompiledSnapshot {
	t.Helper()
	config := &snapshot.CompiledConfig{
		Revision:     revision,
		AutoModelIDs: []string{"b", "a"},
		Models: map[string]adapter.CompiledModel{
			"a": {ID: "a", FallbackModelIDs: []string{"b"}},
			"b": {ID: "b"},
		},
		Providers: map[string]adapter.CompiledProvider{
			"p": {ID: "p", Selector: "openai"},
			"q": {ID: "q", Selector: "anthropic"},
		},
		Routes: []adapter.CompiledRoute{
			{ID: "primary", ModelID: "a", ProviderID: "p", UpstreamModel: "up-primary", Priority: 10, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "g", FallbackRouteIDs: []string{"fallback"}, Credentials: []adapter.CompiledCredential{{ID: "p-1", CredentialRef: "vault://p/one", Priority: 1, Enabled: true}, {ID: "p-2", CredentialRef: "vault://p/two", Priority: 2, Enabled: true}}},
			{ID: "fallback", ModelID: "a", ProviderID: "p", UpstreamModel: "up-fallback", Priority: 20, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "g", Credentials: []adapter.CompiledCredential{{ID: "f-1", CredentialRef: "vault://p/fallback", Enabled: true}}},
			{ID: "other-provider", ModelID: "a", ProviderID: "q", UpstreamModel: "up-q", Priority: 30, Enabled: true, Protocol: adapter.ProtocolAnthropic, RouteGroup: "g", Credentials: []adapter.CompiledCredential{{ID: "q-1", CredentialRef: "vault://q/one", Enabled: true}}},
			{ID: "other-group", ModelID: "a", ProviderID: "p", UpstreamModel: "up-other", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "other", Credentials: []adapter.CompiledCredential{{ID: "other-1", CredentialRef: "vault://p/other", Enabled: true}}},
			{ID: "model-b", ModelID: "b", ProviderID: "p", UpstreamModel: "up-b", Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "g", Credentials: []adapter.CompiledCredential{{ID: "b-1", CredentialRef: "vault://p/b", Enabled: true}}},
			{ID: "disabled", ModelID: "a", ProviderID: "p", Enabled: false, Protocol: adapter.ProtocolOpenAIChat, Credentials: []adapter.CompiledCredential{{ID: "disabled", CredentialRef: "vault://p/no", Enabled: true}}},
		},
	}
	result, err := snapshot.NewCompiledSnapshot(revision, config, generation)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot() error = %v", err)
	}
	return result
}

func authNoneSnapshot(t *testing.T) *snapshot.CompiledSnapshot {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "auth-none",
		Models: map[string]adapter.ModelInput{
			"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		},
		Providers: map[string]adapter.ProviderInput{
			"provider": {ID: "provider", Name: "provider", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"adapter": {ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthNone}},
		},
		Routes: []adapter.RouteInput{{ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "upstream", Enabled: true, Protocol: adapter.ProtocolOpenAIChat}},
	})
	if err != nil {
		t.Fatalf("Compile(AuthNone) error = %v", err)
	}
	result, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(AuthNone) error = %v", err)
	}
	return result
}

func resolver(t *testing.T, source *snapshot.CompiledSnapshot, reader QuarantineReader) *Resolver {
	t.Helper()
	result, err := NewResolver(source, reader, fixedClock{time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("NewResolver() error = %v", err)
	}
	return result
}

func routeIDs(plan Plan) []string {
	result := make([]string, len(plan.Candidates))
	for i, candidate := range plan.Candidates {
		result[i] = candidate.RouteID + "/" + candidate.Credential.ID
	}
	return result
}

func TestPlanAndCandidateFormattingExcludeVaultLocator(t *testing.T) {
	credentialType := reflect.TypeFor[Credential]()
	if credentialType.NumField() != 2 || credentialType.Field(0).Name != "ID" || credentialType.Field(1).Name != "Priority" {
		t.Fatalf("public Credential fields = %v, want only ID and Priority", credentialType)
	}

	plan, err := resolver(t, testSnapshot(t, "credential-shape", 1), nil).Resolve(context.Background(), Selector{Model: "a"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", plan), "vault://") {
		t.Fatalf("Plan formatting exposed a vault locator: %+v", plan)
	}
	for _, candidate := range plan.Candidates {
		if candidate.Credential.ID == "" {
			t.Fatal("authenticated candidate omitted credential ID")
		}
		if strings.Contains(fmt.Sprintf("%+v", candidate), "vault://") {
			t.Fatalf("Candidate formatting exposed a vault locator: %+v", candidate)
		}
	}
}

func TestCandidateTargetIsCompleteQuarantineIdentity(t *testing.T) {
	candidate := Candidate{
		ModelID: "model-id",
		Provider: Provider{
			ID:       "provider-id",
			Selector: "provider-selector",
		},
		RouteID:    "route-id",
		Credential: Credential{ID: "credential-id"},
	}
	if got, want := candidate.Target(), (QuarantineTarget{ModelID: "model-id", ProviderID: "provider-id", RouteID: "route-id", CredentialID: "credential-id"}); got != want {
		t.Fatalf("Candidate.Target() = %+v, want %+v", got, want)
	}

	authNone := Candidate{ModelID: "model-id", Provider: Provider{ID: "provider-id", Selector: "provider-selector"}, RouteID: "route-id"}
	if got, want := authNone.Target(), (QuarantineTarget{ModelID: "model-id", ProviderID: "provider-id", RouteID: "route-id"}); got != want {
		t.Fatalf("AuthNone Candidate.Target() = %+v, want %+v", got, want)
	}
}

func TestResolveSelectorsOrderAndSafeCandidates(t *testing.T) {
	tests := []struct {
		name     string
		selector Selector
		want     []string
	}{
		{"exact model keeps compiled route order", Selector{Model: "a"}, []string{"primary/p-1", "primary/p-2", "fallback/f-1", "other-provider/q-1", "other-group/other-1"}},
		{"auto uses configured model rank", Selector{Model: "auto", Auto: true}, []string{"model-b/b-1", "primary/p-1", "primary/p-2", "fallback/f-1", "other-provider/q-1", "other-group/other-1"}},
		{"effective provider selector", Selector{Model: "a", Provider: "openai"}, []string{"primary/p-1", "primary/p-2", "fallback/f-1", "other-group/other-1"}},
		{"exact group", Selector{Model: "a", Group: "g"}, []string{"primary/p-1", "primary/p-2", "fallback/f-1", "other-provider/q-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := resolver(t, testSnapshot(t, "rev-1", 7), nil).Resolve(context.Background(), test.selector)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			got := routeIDs(plan)
			if len(got) != len(test.want) {
				t.Fatalf("route IDs = %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Errorf("routeIDs()[%d] = %q, want %q", i, got[i], test.want[i])
				}
				candidate := plan.Candidates[i]
				if candidate.Revision != "rev-1" || candidate.Generation != 7 || candidate.Provider.Selector == "" || candidate.Credential.ID == "" {
					t.Errorf("candidate pin/safe fields = %+v", candidate)
				}
			}
		})
	}
}

func TestResolveAuthNoneCandidateAndQuarantineScopes(t *testing.T) {
	t.Run("resolves an explicit no credential candidate", func(t *testing.T) {
		plan, err := resolver(t, authNoneSnapshot(t), nil).Resolve(context.Background(), Selector{Model: "model"})
		if err != nil || len(plan.Candidates) != 1 {
			t.Fatalf("Resolve(AuthNone) candidate count = %d, error = %v; want one candidate", len(plan.Candidates), err)
		}
		candidate := plan.Candidates[0]
		if candidate.Credential != (Credential{}) {
			t.Fatalf("AuthNone credential = %+v, want explicit empty credential", candidate.Credential)
		}
		if same, ok := plan.Next(adapter.RetrySameCredential, candidate.target(), map[QuarantineTarget]struct{}{candidate.target(): {}}); !ok || same != candidate {
			t.Fatalf("Next(same credential) returned current candidate = %t, want true", ok && same == candidate)
		}
		if _, ok := plan.Next(adapter.RetryNextCredential, candidate.target(), nil); ok {
			t.Fatal("Next(next credential) returned a candidate for AuthNone")
		}
	})

	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	t.Run("does not query an empty credential dimension", func(t *testing.T) {
		reader := &quarantineReader{}
		if _, err := resolver(t, authNoneSnapshot(t), reader).Resolve(context.Background(), Selector{Model: "model"}); err != nil {
			t.Fatalf("Resolve(AuthNone) error = %v", err)
		}
		want := []QuarantineTarget{{ModelID: "model"}, {ProviderID: "provider"}, {RouteID: "route"}}
		if len(reader.seen) != len(want) {
			t.Fatalf("AuthNone quarantine targets = %v, want %v", reader.seen, want)
		}
		for i := range want {
			if reader.seen[i] != want[i] {
				t.Errorf("AuthNone quarantine target[%d] = %v, want %v", i, reader.seen[i], want[i])
			}
		}
	})
	for _, target := range []QuarantineTarget{{ModelID: "model"}, {ProviderID: "provider"}, {RouteID: "route"}} {
		t.Run("quarantines "+fmt.Sprintf("%+v", target), func(t *testing.T) {
			reader := &quarantineReader{byTarget: map[QuarantineTarget]Quarantine{target: {Until: now.Add(time.Second)}}}
			_, err := resolver(t, authNoneSnapshot(t), reader).Resolve(context.Background(), Selector{Model: "model"})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Resolve(AuthNone, %v) error = %v, want ErrNotFound", target, err)
			}
		})
	}
}

func TestResolveQuarantineEachDimensionAndFailures(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	reader := &quarantineReader{byTarget: map[QuarantineTarget]Quarantine{
		{ModelID: "a"}:        {Until: now.Add(time.Second)},
		{CredentialID: "b-1"}: {Until: now}, // expired is usable
	}}
	if plan, err := resolver(t, testSnapshot(t, "rev", 1), reader).Resolve(context.Background(), Selector{Model: "b"}); err != nil || len(plan.Candidates) != 1 {
		t.Fatalf("expired quarantine Resolve() candidate count = %d, error = %v; want one candidate", len(plan.Candidates), err)
	}
	if _, err := resolver(t, testSnapshot(t, "rev", 1), reader).Resolve(context.Background(), Selector{Model: "a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active dimension Resolve() error = %v, want ErrNotFound", err)
	}
	if len(reader.seen) < 4 {
		t.Fatalf("quarantine checks = %v, want each dimension", reader.seen)
	}
	if _, err := resolver(t, testSnapshot(t, "rev", 1), &quarantineReader{err: errors.New("down")}).Resolve(context.Background(), Selector{Model: "a"}); !errors.Is(err, ErrQuarantineUnavailable) {
		t.Fatalf("reader failure error = %v, want ErrQuarantineUnavailable", err)
	}
	for _, readerErr := range []error{
		context.Canceled,
		fmt.Errorf("quarantine read: %w", context.Canceled),
	} {
		_, err := resolver(t, testSnapshot(t, "rev", 1), &quarantineReader{err: readerErr}).Resolve(context.Background(), Selector{Model: "a"})
		if !errors.Is(err, readerErr) || errors.Is(err, ErrQuarantineUnavailable) {
			t.Fatalf("reader context error = %v, want propagated %v without ErrQuarantineUnavailable", err, readerErr)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver(t, testSnapshot(t, "rev", 1), nil).Resolve(cancelled, Selector{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Resolve() error = %v", err)
	}
}

func TestNewResolverRejectsInvalidSnapshotAndPinsValue(t *testing.T) {
	if _, err := NewResolver(nil, nil, nil); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("NewResolver(nil) error = %v", err)
	}
	first := testSnapshot(t, "first", 1)
	result := resolver(t, first, nil)
	first.Value().Routes[0].Credentials[0].CredentialRef = "vault-secret"
	plan, err := result.Resolve(context.Background(), Selector{Model: "a"})
	if err != nil || plan.Revision != "first" || len(plan.Candidates) == 0 || plan.Candidates[0].Credential.ID != "p-1" {
		t.Fatalf("pinned result revision = %q, candidate count = %d, error = %v", plan.Revision, len(plan.Candidates), err)
	}
}

func TestPlanNextActions(t *testing.T) {
	plan, err := resolver(t, testSnapshot(t, "rev", 1), nil).Resolve(context.Background(), Selector{Model: "a", Group: "g"})
	if err != nil {
		t.Fatal(err)
	}
	current := plan.Candidates[0].target()
	tests := []struct {
		action  adapter.RetryAction
		visited map[QuarantineTarget]struct{}
		want    string
		ok      bool
	}{
		{adapter.RetrySameCredential, map[QuarantineTarget]struct{}{current: {}}, "primary/p-1", true},
		{adapter.RetryNextCredential, nil, "primary/p-2", true},
		{adapter.RetryNextRoute, nil, "fallback/f-1", true},
		{adapter.RetryNextProvider, nil, "other-provider/q-1", true},
		{adapter.RetryNextModel, nil, "model-b/b-1", true},
		{adapter.RetryNextRoute, map[QuarantineTarget]struct{}{plan.Candidates[1].target(): {}, plan.Candidates[2].target(): {}}, "other-provider/q-1", true},
	}
	for _, test := range tests {
		got, ok := plan.Next(test.action, current, test.visited)
		if ok != test.ok || (ok && got.RouteID+"/"+got.Credential.ID != test.want) {
			t.Errorf("Next(%s) = (%s/%s, %t), want (%s, %t)", test.action, got.RouteID, got.Credential.ID, ok, test.want, test.ok)
		}
		if ok && test.action != adapter.RetryNextModel && !planContainsCandidate(plan, got) {
			t.Errorf("Next(%s) returned candidate absent from Plan.Candidates: %+v", test.action, got)
		}
	}
	auto, err := resolver(t, testSnapshot(t, "rev", 1), nil).Resolve(context.Background(), Selector{Model: "auto", Auto: true, Group: "g"})
	if err != nil {
		t.Fatal(err)
	}
	current = auto.Candidates[len(auto.Candidates)-1].target() // model a is last configured auto model
	if _, ok := auto.Next(adapter.RetryNextModel, current, nil); ok {
		t.Error("last auto model unexpectedly had a next model")
	}
}

func TestPlanNextModelUsesFrozenPrivateFallbackCandidates(t *testing.T) {
	result := resolver(t, testSnapshot(t, "rev-1", 7), nil)
	plan, err := result.Resolve(context.Background(), Selector{Model: "a", Group: "g"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 4 {
		t.Fatalf("public exact-model candidates = %d, want 4", len(plan.Candidates))
	}

	current := plan.Candidates[0]
	next, ok := plan.Next(adapter.RetryNextModel, current.target(), nil)
	if !ok || next.ModelID != "b" || next.RouteID != "model-b" || next.Credential.ID != "b-1" {
		t.Fatalf("exact model fallback NextModel = (%+v, %t), want model b", next, ok)
	}
	if planContainsCandidate(plan, next) {
		t.Fatalf("fallback candidate was exposed in public Candidates: %+v", next)
	}
	if next.Revision != "rev-1" || next.Generation != 7 {
		t.Fatalf("fallback candidate pin = (%q, %d), want (rev-1, 7)", next.Revision, next.Generation)
	}

	// A plan has already copied its complete retry universe. Subsequent resolver
	// config mutation cannot create a live-config retry candidate or change pins.
	result.config.Models["a"] = adapter.CompiledModel{ID: "a"}
	result.config.Routes = nil
	next, ok = plan.Next(adapter.RetryNextModel, current.target(), nil)
	if !ok || next.ModelID != "b" || next.Revision != "rev-1" || next.Generation != 7 {
		t.Fatalf("mutated resolver changed frozen fallback = (%+v, %t)", next, ok)
	}
}

func planContainsCandidate(plan Plan, want Candidate) bool {
	for _, candidate := range plan.Candidates {
		if candidate.target() == want.target() {
			return true
		}
	}
	return false
}

func TestResolverConcurrentResolve(t *testing.T) {
	result := resolver(t, testSnapshot(t, "race", 9), nil)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan, err := result.Resolve(context.Background(), Selector{Model: "a"})
			if err != nil || plan.Revision != "race" || plan.Generation != 9 || len(plan.Candidates) != 5 {
				errs <- errors.New("concurrent plan lost pin or candidates")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
