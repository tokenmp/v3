package adapter

import (
	"context"
	"errors"
	"testing"
)

func thinkingEngineAdapter() CompiledAdapter {
	return CompiledAdapter{Thinking: ThinkingPolicy{
		Supported:      true,
		DefaultEffort:  ThinkingLow,
		EffortMapping:  fullEffortMap(ThinkingHigh),
		BudgetMapping:  map[ThinkingEffort]int{ThinkingHigh: 50},
		MinBudgetToken: 1,
		MaxBudgetToken: 100,
	}}
}

func applyThinking(adapter CompiledAdapter, model ThinkingInput, request ThinkingRequest) (AppliedRequest, error) {
	return (Engine{}).Apply(context.Background(), ApplyInput{
		Adapter:       adapter,
		ModelThinking: model,
		Body:          []byte(`{}`),
		Thinking:      request,
	})
}

func TestEngineThinkingUsesSelectedModelBounds(t *testing.T) {
	adapter := thinkingEngineAdapter()
	wide := ThinkingInput{Supported: true, DefaultEffort: ThinkingLow, MaxEffort: ThinkingHigh, MinBudgetToken: 1, MaxBudgetToken: 100}
	narrow := ThinkingInput{Supported: true, DefaultEffort: ThinkingLow, MaxEffort: ThinkingHigh, MinBudgetToken: 1, MaxBudgetToken: 10}

	// The same adapter is valid for the wide selected model, but its default
	// mapped budget cannot escape the narrow selected model's bounds.
	if got, err := applyThinking(adapter, wide, ThinkingRequest{Enabled: true}); err != nil || got.Thinking.EffectiveBudget != 50 {
		t.Fatalf("wide model result = %#v, %v", got.Thinking, err)
	}
	if _, err := applyThinking(adapter, narrow, ThinkingRequest{Enabled: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("narrow model accepted adapter default budget 50: %v", err)
	}
	budget50 := 50
	if _, err := applyThinking(adapter, narrow, ThinkingRequest{Enabled: true, BudgetTokens: &budget50}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("narrow model accepted explicit budget 50: %v", err)
	}

	// A caller-supplied value at the adapter/model intersection boundary remains valid.
	budget := 10
	got, err := applyThinking(adapter, narrow, ThinkingRequest{Enabled: true, BudgetTokens: &budget})
	if err != nil || got.Thinking.EffectiveBudget != budget {
		t.Fatalf("intersection boundary result = %#v, %v", got.Thinking, err)
	}
}

func TestEngineThinkingRejectsModelBudgetAndEffortEscapes(t *testing.T) {
	adapter := thinkingEngineAdapter()
	budgetBounded := ThinkingInput{Supported: true, DefaultEffort: ThinkingLow, MaxEffort: ThinkingHigh, MinBudgetToken: 5, MaxBudgetToken: 10}

	for _, budget := range []int{4, 11, 50} {
		if _, err := applyThinking(adapter, budgetBounded, ThinkingRequest{Enabled: true, BudgetTokens: &budget}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("budget %d escaped model/adapter intersection: %v", budget, err)
		}
	}
	// The effective effort is adapter-mapped high, which exceeds this model's
	// max medium even though the requested effort is low.
	effortBounded := budgetBounded
	effortBounded.MaxEffort = ThinkingMedium
	if _, err := applyThinking(adapter, effortBounded, ThinkingRequest{Enabled: true, Effort: ThinkingLow}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("model accepted effective effort beyond max: %v", err)
	}
}

func TestEngineThinkingRequiresModelWhenEnabled(t *testing.T) {
	adapter := thinkingEngineAdapter()
	if _, err := applyThinking(adapter, ThinkingInput{}, ThinkingRequest{Enabled: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("enabled thinking accepted zero ModelThinking: %v", err)
	}
	// Disabled thinking intentionally needs no model bounds.
	got, err := applyThinking(adapter, ThinkingInput{}, ThinkingRequest{})
	if err != nil || got.Thinking != (EffectiveThinking{}) {
		t.Fatalf("disabled thinking without model = %#v, %v", got.Thinking, err)
	}
}

func TestEngineThinkingRequiresAdapterSupport(t *testing.T) {
	model := ThinkingInput{Supported: true, DefaultEffort: ThinkingLow, MaxEffort: ThinkingHigh, MinBudgetToken: 0, MaxBudgetToken: 10}
	if _, err := applyThinking(CompiledAdapter{}, model, ThinkingRequest{Enabled: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("enabled thinking accepted unsupported adapter: %v", err)
	}
}
