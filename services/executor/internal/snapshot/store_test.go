package snapshot

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestStoreCurrentWithoutPublishedSnapshot(t *testing.T) {
	var store Store

	got, err := store.Current()
	if got != nil {
		t.Errorf("Current() snapshot = %#v, want nil", got)
	}
	if !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Current() error = %v, want ErrNoSnapshot", err)
	}
	if ErrNoSnapshot.Error() != "compiled snapshot unavailable" {
		t.Errorf("ErrNoSnapshot = %q, want stable error text", ErrNoSnapshot)
	}
	if ErrStaleSnapshot.Error() != "stale snapshot generation" {
		t.Errorf("ErrStaleSnapshot = %q, want stable error text", ErrStaleSnapshot)
	}
}

func TestStorePublishIsolatedFromSourceMutation(t *testing.T) {
	var store Store
	generation := uint64(1)
	source := adapter.CompiledConfig{
		Revision: "rev-1",
		Models: map[string]adapter.CompiledModel{
			"model-1": {ID: "model-1"},
		},
		Routes: []adapter.CompiledRoute{{ID: "route-1", ModelID: "model-1"}},
	}
	snapshot, err := NewCompiledSnapshot(source.Revision, &source, generation)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}

	source.Models["injected"] = adapter.CompiledModel{ID: "injected"}
	source.Routes[0].ID = "mutated"
	if err := store.Publish(snapshot); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	view, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	value := view.Value()
	if _, ok := value.Models["injected"]; ok {
		t.Error("source model mutation leaked into store")
	}
	if got := value.Routes[0].ID; got != "route-1" {
		t.Errorf("route ID = %q, want route-1; source mutation leaked", got)
	}
}

func TestStoreCurrentIsolatedFromReturnedMutation(t *testing.T) {
	var store Store
	generation := uint64(1)
	config := adapter.CompiledConfig{
		Revision: "rev-1",
		Models: map[string]adapter.CompiledModel{
			"model-1": {ID: "model-1"},
		},
		Routes: []adapter.CompiledRoute{{ID: "route-1", ModelID: "model-1"}},
	}
	snapshot, err := NewCompiledSnapshot(config.Revision, &config, generation)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	if err := store.Publish(snapshot); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	view, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	value := view.Value()
	value.Models["injected"] = adapter.CompiledModel{ID: "injected"}
	value.Routes[0].ID = "mutated"

	view, err = store.Current()
	if err != nil {
		t.Fatalf("Current after returned mutation: %v", err)
	}
	value = view.Value()
	if _, ok := value.Models["injected"]; ok {
		t.Error("returned model mutation leaked into store")
	}
	if got := value.Routes[0].ID; got != "route-1" {
		t.Errorf("route ID = %q, want route-1; returned mutation leaked", got)
	}
}

func TestStoreC24DetachesEveryNestedCollectionAcrossSnapshotCurrentAndOldView(t *testing.T) {
	min, max := 0.1, 0.9
	source := adapter.CompiledConfig{
		Revision:  "rev-1",
		Models:    map[string]adapter.CompiledModel{"m": {ID: "m", Capabilities: []adapter.Capability{adapter.CapabilityChat}}},
		Providers: map[string]adapter.CompiledProvider{"p": {ID: "p", Retry: adapter.CompiledRetry{Rules: []adapter.RetryRule{{ID: "p", HTTPStatuses: []int{503}, ErrorCodes: []string{"busy"}, ErrorTypes: []string{"temporary"}}}}}},
		Adapters: map[string]adapter.CompiledAdapter{"a": {
			ID: "a", Capability: adapter.CapabilityPolicy{Require: []adapter.Capability{adapter.CapabilityChat}, Deny: []adapter.Capability{adapter.CapabilityImages}},
			Thinking:      adapter.ThinkingPolicy{EffortMapping: map[adapter.ThinkingEffort]adapter.ThinkingEffort{adapter.ThinkingLow: adapter.ThinkingMinimal}, BudgetMapping: map[adapter.ThinkingEffort]int{adapter.ThinkingLow: 1}},
			Request:       adapter.RequestPolicy{AllowedHeaders: []string{"X-Safe"}, AllowedQuery: []string{"mode"}, Rules: []adapter.RequestRule{{ID: "rule", Value: []byte("1"), EnumMap: map[string]string{"one": "1"}, Min: &min, Max: &max}}},
			ResponseRules: []adapter.ResponseRule{{ID: "response", Match: adapter.ResponseMatch{HTTPStatuses: []int{500}, ErrorCodes: []string{"busy"}, ErrorTypes: []string{"temporary"}, MessageContains: []string{"retry"}, FinishReasons: []string{"length"}, StreamEventTypes: []string{"error"}}}},
			Retry:         adapter.CompiledRetry{Rules: []adapter.RetryRule{{ID: "adapter", HTTPStatuses: []int{429}, ErrorCodes: []string{"rate"}, ErrorTypes: []string{"limited"}}}},
		}},
		Routes: []adapter.CompiledRoute{{ID: "r", FallbackRouteIDs: []string{"fallback"}, Retry: adapter.CompiledRetry{Rules: []adapter.RetryRule{{ID: "route", HTTPStatuses: []int{502}, ErrorCodes: []string{"retry"}, ErrorTypes: []string{"temporary"}}}}}},
	}
	want := adapter.CloneCompiledConfig(source)
	snapshot, err := NewCompiledSnapshot(source.Revision, &source, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}

	// Exercise every map, slice, and pointer reachable through CompiledConfig.
	source.Models["m"] = adapter.CompiledModel{ID: "mutated"}
	source.Providers["p"] = adapter.CompiledProvider{ID: "mutated"}
	source.Adapters["a"] = adapter.CompiledAdapter{ID: "mutated"}
	source.Routes[0] = adapter.CompiledRoute{ID: "mutated"}
	if got := snapshot.Value(); !reflect.DeepEqual(*got, want) {
		t.Fatalf("source mutation leaked into frozen snapshot\n got: %#v\nwant: %#v", *got, want)
	}

	var store Store
	if err := store.Publish(snapshot); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	old, err := store.Current()
	if err != nil {
		t.Fatalf("Current(old): %v", err)
	}
	caller := old.Value()
	caller.Models["m"].Capabilities[0] = adapter.CapabilityImages
	provider := caller.Providers["p"]
	provider.Retry.Rules[0].HTTPStatuses[0] = 418
	caller.Providers["p"] = provider
	compiledAdapter := caller.Adapters["a"]
	compiledAdapter.Capability.Require[0] = adapter.CapabilityImages
	compiledAdapter.Capability.Deny[0] = adapter.CapabilityChat
	compiledAdapter.Thinking.EffortMapping[adapter.ThinkingLow] = adapter.ThinkingMax
	compiledAdapter.Thinking.BudgetMapping[adapter.ThinkingLow] = 99
	compiledAdapter.Request.AllowedHeaders[0] = "X-Caller"
	compiledAdapter.Request.AllowedQuery[0] = "caller"
	compiledAdapter.Request.Rules[0].Value[0] = '9'
	compiledAdapter.Request.Rules[0].EnumMap["one"] = "caller"
	*compiledAdapter.Request.Rules[0].Min = 0.2
	*compiledAdapter.Request.Rules[0].Max = 0.8
	compiledAdapter.ResponseRules[0].Match.HTTPStatuses[0] = 418
	compiledAdapter.ResponseRules[0].Match.ErrorCodes[0] = "caller"
	compiledAdapter.ResponseRules[0].Match.ErrorTypes[0] = "caller"
	compiledAdapter.ResponseRules[0].Match.MessageContains[0] = "caller"
	compiledAdapter.ResponseRules[0].Match.FinishReasons[0] = "caller"
	compiledAdapter.ResponseRules[0].Match.StreamEventTypes[0] = "caller"
	compiledAdapter.Retry.Rules[0].HTTPStatuses[0] = 418
	caller.Adapters["a"] = compiledAdapter
	caller.Routes[0].FallbackRouteIDs[0] = "caller"
	caller.Routes[0].Retry.Rules[0].HTTPStatuses[0] = 418
	if got, err := store.Current(); err != nil || !reflect.DeepEqual(*got.Value(), want) {
		t.Fatalf("caller mutation leaked into Store: got=%#v err=%v", got, err)
	}

	secondValue := adapter.CloneCompiledConfig(want)
	secondValue.Revision = "rev-2"
	second, err := NewCompiledSnapshot(secondValue.Revision, &secondValue, 2)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(second): %v", err)
	}
	if err := store.Publish(second); err != nil {
		t.Fatalf("Publish(second): %v", err)
	}
	if got := old.Value(); !reflect.DeepEqual(*got, want) {
		t.Fatalf("old view changed after newer publication\n got: %#v\nwant: %#v", *got, want)
	}
}

func TestStoreOldRevisionViewRemainsStable(t *testing.T) {
	var store Store
	firstGeneration := uint64(1)
	firstConfig := adapter.CompiledConfig{Revision: "rev-1"}
	first, err := NewCompiledSnapshot(firstConfig.Revision, &firstConfig, firstGeneration)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(rev-1): %v", err)
	}
	if err := store.Publish(first); err != nil {
		t.Fatalf("Publish(rev-1): %v", err)
	}

	oldView, err := store.Current()
	if err != nil {
		t.Fatalf("Current(old): %v", err)
	}

	secondGeneration := uint64(2)
	secondConfig := adapter.CompiledConfig{Revision: "rev-2"}
	second, err := NewCompiledSnapshot(secondConfig.Revision, &secondConfig, secondGeneration)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(rev-2): %v", err)
	}
	if err := store.Publish(second); err != nil {
		t.Fatalf("Publish(rev-2): %v", err)
	}

	if got := oldView.Revision(); got != "rev-1" {
		t.Errorf("old view Revision() = %q, want rev-1", got)
	}
	if got := oldView.Value().Revision; got != "rev-1" {
		t.Errorf("old view Value().Revision = %q, want rev-1", got)
	}
	if got := oldView.Generation(); got != firstGeneration {
		t.Errorf("old view Generation() = %d, want %d", got, firstGeneration)
	}

	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current(new): %v", err)
	}
	if got := current.Revision(); got != "rev-2" {
		t.Errorf("current Revision() = %q, want rev-2", got)
	}
	if got := current.Generation(); got != secondGeneration {
		t.Errorf("current Generation() = %d, want %d", got, secondGeneration)
	}
}

func TestStoreFailedPublishPreservesLastKnownGood(t *testing.T) {
	var store Store
	generation := uint64(1)
	config := adapter.CompiledConfig{Revision: "last-known-good"}
	snapshot, err := NewCompiledSnapshot(config.Revision, &config, generation)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	if err := store.Publish(snapshot); err != nil {
		t.Fatalf("Publish(last known good): %v", err)
	}

	if err := store.Publish(nil); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("Publish(nil) error = %v, want ErrInvalidSnapshot", err)
	}

	current, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got := current.Revision(); got != config.Revision {
		t.Errorf("current Revision() = %q, want %q", got, config.Revision)
	}
	if got := current.Generation(); got != generation {
		t.Errorf("current Generation() = %d, want %d", got, generation)
	}
}

func TestStoreRejectsStaleGeneration(t *testing.T) {
	var store Store
	currentGeneration := uint64(2)
	currentConfig := adapter.CompiledConfig{Revision: "rev-2"}
	currentSnapshot, err := NewCompiledSnapshot(currentConfig.Revision, &currentConfig, currentGeneration)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(rev-2): %v", err)
	}
	if err := store.Publish(currentSnapshot); err != nil {
		t.Fatalf("Publish(rev-2): %v", err)
	}

	for _, candidate := range []struct {
		revision   string
		generation uint64
	}{
		{revision: "rev-1", generation: 1},
		{revision: "rev-2-replacement", generation: currentGeneration},
	} {
		candidateConfig := adapter.CompiledConfig{Revision: candidate.revision}
		candidateSnapshot, err := NewCompiledSnapshot(candidateConfig.Revision, &candidateConfig, candidate.generation)
		if err != nil {
			t.Fatalf("NewCompiledSnapshot(%q): %v", candidate.revision, err)
		}
		if err := store.Publish(candidateSnapshot); !errors.Is(err, ErrStaleSnapshot) {
			t.Errorf("Publish(%q, generation=%d) error = %v, want ErrStaleSnapshot", candidate.revision, candidate.generation, err)
		}
	}

	view, err := store.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got := view.Revision(); got != currentConfig.Revision {
		t.Errorf("current Revision() = %q, want %q", got, currentConfig.Revision)
	}
	if got := view.Generation(); got != currentGeneration {
		t.Errorf("current Generation() = %d, want %d", got, currentGeneration)
	}
}

func TestStoreConcurrentReadersAndWriter(t *testing.T) {
	var store Store
	initialGeneration := uint64(1)
	initialConfig := adapter.CompiledConfig{Revision: "rev-1"}
	initial, err := NewCompiledSnapshot(initialConfig.Revision, &initialConfig, initialGeneration)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(initial): %v", err)
	}
	if err := store.Publish(initial); err != nil {
		t.Fatalf("Publish(initial): %v", err)
	}

	const readers = 32
	const writes = 100
	start := make(chan struct{})
	errs := make(chan error, readers+1)
	var wg sync.WaitGroup

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range writes {
				view, err := store.Current()
				if err != nil {
					errs <- fmt.Errorf("Current: %w", err)
					return
				}
				value := view.Value()
				if value == nil || value.Revision != view.Revision() || view.Generation() == 0 {
					errs <- fmt.Errorf("inconsistent view: revision=%q value=%#v generation=%d", view.Revision(), value, view.Generation())
					return
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for generation := uint64(2); generation <= writes+1; generation++ {
			config := adapter.CompiledConfig{Revision: fmt.Sprintf("rev-%d", generation)}
			snapshot, err := NewCompiledSnapshot(config.Revision, &config, generation)
			if err != nil {
				errs <- fmt.Errorf("NewCompiledSnapshot(generation=%d): %w", generation, err)
				return
			}
			if err := store.Publish(snapshot); err != nil {
				errs <- fmt.Errorf("Publish(generation=%d): %w", generation, err)
				return
			}
		}
	}()

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if got := store.Generation(); got != writes+1 {
		t.Errorf("Generation() = %d, want %d", got, writes+1)
	}
}

func TestCompiledSnapshotNilReceiver(t *testing.T) {
	var snapshot *CompiledSnapshot
	if got := snapshot.Revision(); got != "" {
		t.Errorf("nil Revision() = %q, want empty", got)
	}
	if got := snapshot.Value(); got != nil {
		t.Errorf("nil Value() = %v, want nil", got)
	}
	if got := snapshot.Generation(); got != 0 {
		t.Errorf("nil Generation() = %d, want 0", got)
	}
}
