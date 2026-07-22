package sdk

import "testing"

func TestUsageValid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage Usage
		valid bool
	}{
		{"zero", Usage{}, true},
		{"consistent", Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}, true},
		{"max tokens", Usage{PromptTokens: maxSDKUsageTokens, CompletionTokens: 0, TotalTokens: maxSDKUsageTokens}, true},
		{"inconsistent total too high", Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 31}, false},
		{"inconsistent total too low", Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 29}, false},
		{"prompt exceeds cap", Usage{PromptTokens: maxSDKUsageTokens + 1, CompletionTokens: 0, TotalTokens: maxSDKUsageTokens + 1}, false},
		{"completion exceeds cap", Usage{PromptTokens: 0, CompletionTokens: maxSDKUsageTokens + 1, TotalTokens: maxSDKUsageTokens + 1}, false},
		{"total exceeds cap", Usage{PromptTokens: maxSDKUsageTokens, CompletionTokens: 1, TotalTokens: maxSDKUsageTokens + 1}, false},
		{"overflow sum", Usage{PromptTokens: maxSDKUsageTokens, CompletionTokens: maxSDKUsageTokens, TotalTokens: maxSDKUsageTokens + maxSDKUsageTokens}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.usage.Valid(); got != tc.valid {
				t.Fatalf("Valid() = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestMaxSDKUsageTokensAlignsWithStreamingHardCap(t *testing.T) {
	// The non-stream hard cap must equal the streaming hard cap so both paths
	// share the same upper bound on token counters.
	if maxSDKUsageTokens != 1_000_000 {
		t.Fatalf("maxSDKUsageTokens = %d, want 1_000_000", maxSDKUsageTokens)
	}
}
