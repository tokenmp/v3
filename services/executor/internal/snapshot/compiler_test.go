package snapshot

import (
	"reflect"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func intp(v int) *int { return &v }

func TestCompileFixturePoliciesApplyDefaultsAndOverrides(t *testing.T) {
	_, cfg := loadRawConfig(t, "default")
	adapterID := "adapter-openai-default"
	providerID := "openai-default"

	// An omitted policy at every layer must resolve to compiler defaults.
	cfg.Global = GlobalPolicy{}
	a := cfg.Adapters[adapterID]
	a.Retry, a.Timeout = adapter.RetryPolicy{}, adapter.TimeoutPolicy{}
	cfg.Adapters[adapterID] = a
	p := cfg.Providers[providerID]
	p.Retry, p.Timeout = adapter.RetryPolicy{}, adapter.TimeoutPolicy{}
	cfg.Providers[providerID] = p
	cfg.Routes[0].Retry, cfg.Routes[0].Timeout = adapter.RetryPolicy{}, adapter.TimeoutPolicy{}
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile defaults: %v", err)
	}
	route := compiled.Routes[0]
	if route.Retry.MaxTotalAttempts != adapter.DefaultMaxTotalAttempts || route.Retry.MaxSameTargetAttempts != adapter.DefaultMaxSameTargetAttempts || route.Retry.MaxTotalDuration != adapter.DefaultMaxTotalDuration || route.Retry.Backoff != adapter.DefaultRetryBackoff {
		t.Errorf("effective retry defaults = %#v", route.Retry)
	}
	if route.Timeout.Request != adapter.DefaultRequestTimeout || route.Timeout.TTFT != adapter.DefaultTTFTTimeout || route.Timeout.StreamIdle != adapter.DefaultStreamIdleTimeout || route.Timeout.StreamMaxLifetime != adapter.DefaultStreamMaxLifetime || route.Timeout.RetryBackoff != adapter.DefaultRetryBackoff {
		t.Errorf("effective timeout defaults = %#v", route.Timeout)
	}

	// Route values win over provider, adapter, and global values in the compiled
	// effective policy; route rules also replace lower-precedence rule sets.
	cfg.Global.Retry = adapter.RetryPolicy{MaxTotalAttempts: intp(2), Backoff: "300ms", Rules: []adapter.RetryRule{{ID: "global", Priority: 30, Action: adapter.RetryNextRoute}}}
	cfg.Global.Timeout = adapter.TimeoutPolicy{RequestTimeout: "70s", TTFTTimeout: "20s", StreamIdleTimeout: "20s", StreamMaxLifetime: "100s", RetryBackoff: "300ms"}
	a = cfg.Adapters[adapterID]
	a.Retry = adapter.RetryPolicy{MaxTotalAttempts: intp(3), Backoff: "400ms", Rules: []adapter.RetryRule{{ID: "adapter-late", Priority: 20, Action: adapter.RetryNextRoute}, {ID: "adapter-early", Priority: 10, Action: adapter.RetryNextCredential}}}
	a.Timeout = adapter.TimeoutPolicy{RequestTimeout: "80s", TTFTTimeout: "25s", StreamIdleTimeout: "25s", StreamMaxLifetime: "120s", RetryBackoff: "400ms"}
	cfg.Adapters[adapterID] = a
	p = cfg.Providers[providerID]
	p.Retry = adapter.RetryPolicy{MaxTotalAttempts: intp(4), Backoff: "500ms"}
	p.Timeout = adapter.TimeoutPolicy{RequestTimeout: "90s", TTFTTimeout: "30s", StreamIdleTimeout: "30s", StreamMaxLifetime: "140s", RetryBackoff: "500ms"}
	cfg.Providers[providerID] = p
	cfg.Routes[0].Retry = adapter.RetryPolicy{MaxTotalAttempts: intp(5), MaxSameTargetAttempts: intp(2), MaxTotalDuration: "50s", Backoff: "600ms", Rules: []adapter.RetryRule{{ID: "route-late", Priority: 20, Action: adapter.RetryNextRoute}, {ID: "route-early", Priority: 10, Action: adapter.RetryNextCredential}}}
	cfg.Routes[0].Timeout = adapter.TimeoutPolicy{RequestTimeout: "100s", TTFTTimeout: "35s", StreamIdleTimeout: "35s", StreamMaxLifetime: "160s", RetryBackoff: "600ms"}
	compiled, err = Compile(cfg)
	if err != nil {
		t.Fatalf("Compile overrides: %v", err)
	}
	route = compiled.Routes[0]
	if route.Retry.MaxTotalAttempts != 5 || route.Retry.MaxSameTargetAttempts != 2 || route.Retry.MaxTotalDuration != 50*time.Second || route.Retry.Backoff != 600*time.Millisecond {
		t.Errorf("effective retry overrides = %#v", route.Retry)
	}
	if len(route.Retry.Rules) != 2 || route.Retry.Rules[0].ID != "route-early" || route.Retry.Rules[1].ID != "route-late" {
		t.Errorf("effective retry rules = %#v", route.Retry.Rules)
	}
	if route.Timeout.Request != 100*time.Second || route.Timeout.TTFT != 35*time.Second || route.Timeout.StreamIdle != 35*time.Second || route.Timeout.StreamMaxLifetime != 160*time.Second || route.Timeout.RetryBackoff != 600*time.Millisecond {
		t.Errorf("effective timeout overrides = %#v", route.Timeout)
	}
}

func TestCompileC24RawCompiledSnapshotStoreCurrentAndOldViewHaveNoNestedAliases(t *testing.T) {
	_, raw := loadRawConfig(t, "default")
	adapterID, providerID, routeID := "adapter-openai-default", "openai-default", raw.Routes[0].ID
	a := raw.Adapters[adapterID]
	a.Request.AllowedHeaders = append(a.Request.AllowedHeaders, "X-Alias-Test")
	a.Response.Rules[0].Match.HTTPStatuses = append(a.Response.Rules[0].Match.HTTPStatuses, 503)
	a.Retry.Rules[0].HTTPStatuses = append(a.Retry.Rules[0].HTTPStatuses, 502)
	raw.Adapters[adapterID] = a
	p := raw.Providers[providerID]
	p.Retry.Rules = []adapter.RetryRule{{ID: "provider-alias", Priority: 1, HTTPStatuses: []int{503}, Action: adapter.RetryNextRoute}}
	raw.Providers[providerID] = p
	raw.Routes[0].FallbackRouteIDs = []string{"fallback"}
	raw.Routes = append(raw.Routes, RouteConfig{ID: "fallback", ModelID: raw.Routes[0].ModelID, ProviderID: providerID, AdapterID: adapterID, UpstreamModel: "fallback-model", Protocol: raw.Routes[0].Protocol})

	compiled, err := Compile(raw)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	first, err := NewCompiledSnapshot(compiled.Revision, &compiled, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(first): %v", err)
	}
	var store Store
	if err := store.Publish(first); err != nil {
		t.Fatalf("Publish(first): %v", err)
	}
	old, err := store.Current()
	if err != nil {
		t.Fatalf("Current(old): %v", err)
	}

	// Mutate every raw nested collection after compilation and publication.
	a.Request.AllowedHeaders[0] = "X-Mutated"
	a.Response.Rules[0].Match.HTTPStatuses[0] = 418
	a.Retry.Rules[0].HTTPStatuses[0] = 418
	raw.Adapters[adapterID] = a
	p.Retry.Rules[0].HTTPStatuses[0] = 418
	raw.Providers[providerID] = p
	raw.Routes[0].FallbackRouteIDs[0] = "mutated"

	oldValue := old.Value()
	oldAdapter := oldValue.Adapters[adapterID]
	var oldRoute adapter.CompiledRoute
	for _, route := range oldValue.Routes {
		if route.ID == routeID {
			oldRoute = route
			break
		}
	}
	if oldAdapter.Request.AllowedHeaders[0] == "X-Mutated" || oldAdapter.ResponseRules[0].Match.HTTPStatuses[0] != 429 || oldAdapter.Retry.Rules[0].HTTPStatuses[0] != 429 || oldValue.Providers[providerID].Retry.Rules[0].HTTPStatuses[0] != 503 || len(oldRoute.FallbackRouteIDs) != 1 || oldRoute.FallbackRouteIDs[0] != "fallback" {
		t.Fatalf("raw nested mutation leaked through raw -> compiled -> snapshot -> store -> current: %#v", oldValue)
	}

	// Mutating a returned view must not alter the retained old view or Store.
	oldAdapter.Request.AllowedHeaders[0] = "caller-mutation"
	oldValue.Adapters[adapterID] = oldAdapter
	for i := range oldValue.Routes {
		if oldValue.Routes[i].ID == routeID {
			oldValue.Routes[i].FallbackRouteIDs[0] = "caller-mutation"
			break
		}
	}
	if old.Value().Adapters[adapterID].Request.AllowedHeaders[0] == "caller-mutation" {
		t.Fatal("Current view retained a nested alias")
	}

	secondConfig := adapter.CloneCompiledConfig(compiled)
	secondConfig.Revision = "second"
	second, err := NewCompiledSnapshot(secondConfig.Revision, &secondConfig, 2)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(second): %v", err)
	}
	if err := store.Publish(second); err != nil {
		t.Fatalf("Publish(second): %v", err)
	}
	oldAfterPublish := old.Value()
	for _, route := range oldAfterPublish.Routes {
		if route.ID == routeID && route.FallbackRouteIDs[0] != "fallback" {
			t.Fatalf("old view changed after later publication: %#v", oldAfterPublish)
		}
	}
	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current(second): %v", err)
	}
	if current.Revision() != "second" || current.Generation() != 2 {
		t.Fatalf("current view = revision %q generation %d", current.Revision(), current.Generation())
	}
}

func TestCompileC27SnapshotMapAndRoutePermutationsAreDeepEqual(t *testing.T) {
	_, raw := loadRawConfig(t, "default")
	first := raw.Routes[0]
	second := first
	second.ID, second.UpstreamModel, second.Priority = "route-second", "second-model", first.Priority+1
	raw.Routes = []RouteConfig{first, second}
	want, err := Compile(raw)
	if err != nil {
		t.Fatalf("Compile canonical: %v", err)
	}
	for i := 0; i < 50; i++ {
		permuted := raw
		permuted.Models = make(map[string]ModelConfig, len(raw.Models))
		for key, value := range raw.Models {
			permuted.Models[key] = value
		}
		permuted.Providers = make(map[string]ProviderConfig, len(raw.Providers))
		for key, value := range raw.Providers {
			permuted.Providers[key] = value
		}
		permuted.Adapters = make(map[string]adapter.AdapterConfig, len(raw.Adapters))
		for key, value := range raw.Adapters {
			permuted.Adapters[key] = value
		}
		permuted.Routes = []RouteConfig{second, first}
		got, err := Compile(permuted)
		if err != nil {
			t.Fatalf("Compile permutation %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d compiled non-deterministically\n got: %#v\nwant: %#v", i, got, want)
		}
	}
}

func TestCompileFixturesProducesStoreReadyConfig(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			_, raw := loadRawConfig(t, name)
			compiled, err := Compile(raw)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			frozen, err := NewCompiledSnapshot(compiled.Revision, &compiled, 1)
			if err != nil {
				t.Fatalf("NewCompiledSnapshot: %v", err)
			}
			var store Store
			if err := store.Publish(frozen); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			view, err := store.Current()
			if err != nil {
				t.Fatalf("Current: %v", err)
			}
			if view.Revision() != raw.Revision || len(view.Value().Routes) == 0 {
				t.Fatalf("compiled view is incomplete: %#v", view.Value())
			}
		})
	}
}
