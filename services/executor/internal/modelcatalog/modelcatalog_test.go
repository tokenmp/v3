package modelcatalog

import (
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestMapCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps []adapter.Capability
		want []string
	}{
		{
			name: "chat maps to text",
			caps: []adapter.Capability{adapter.CapabilityChat},
			want: []string{"text"},
		},
		{
			name: "tools maps to function_calling",
			caps: []adapter.Capability{adapter.CapabilityTools},
			want: []string{"function_calling"},
		},
		{
			name: "vision maps to vision",
			caps: []adapter.Capability{adapter.CapabilityVision},
			want: []string{"vision"},
		},
		{
			name: "thinking maps to thinking",
			caps: []adapter.Capability{adapter.CapabilityThinking},
			want: []string{"thinking"},
		},
		{
			name: "images maps to image",
			caps: []adapter.Capability{adapter.CapabilityImages},
			want: []string{"image"},
		},
		{
			name: "streaming is omitted",
			caps: []adapter.Capability{adapter.CapabilityChat, adapter.CapabilityStreaming},
			want: []string{"text"},
		},
		{
			name: "messages is omitted",
			caps: []adapter.Capability{adapter.CapabilityMessages, adapter.CapabilityChat},
			want: []string{"text"},
		},
		{
			name: "responses is omitted",
			caps: []adapter.Capability{adapter.CapabilityResponses, adapter.CapabilityChat},
			want: []string{"text"},
		},
		{
			name: "multiple capabilities sorted",
			caps: []adapter.Capability{adapter.CapabilityThinking, adapter.CapabilityChat, adapter.CapabilityVision},
			want: []string{"text", "thinking", "vision"},
		},
		{
			name: "empty capabilities",
			caps: []adapter.Capability{},
			want: nil,
		},
		{
			name: "nil capabilities",
			caps: nil,
			want: nil,
		},
		{
			name: "all public capabilities",
			caps: []adapter.Capability{adapter.CapabilityChat, adapter.CapabilityTools, adapter.CapabilityVision, adapter.CapabilityThinking, adapter.CapabilityImages},
			want: []string{"function_calling", "image", "text", "thinking", "vision"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MapCapabilities(tc.caps)
			if len(got) != len(tc.want) {
				t.Fatalf("MapCapabilities(%v) = %v, want %v", tc.caps, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("MapCapabilities(%v)[%d] = %q, want %q", tc.caps, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestMapThinking(t *testing.T) {
	t.Parallel()

	t.Run("unsupported returns nil", func(t *testing.T) {
		t.Parallel()
		got := MapThinking(adapter.ThinkingInput{Supported: false})
		if got != nil {
			t.Fatalf("MapThinking(unsupported) = %v, want nil", got)
		}
	})

	t.Run("supported with no budget", func(t *testing.T) {
		t.Parallel()
		in := adapter.ThinkingInput{
			Supported:     true,
			DefaultEffort: adapter.ThinkingMedium,
			MaxEffort:     adapter.ThinkingMax,
		}
		got := MapThinking(in)
		if got == nil {
			t.Fatal("MapThinking returned nil")
		}
		if !got.Supported {
			t.Error("Supported = false, want true")
		}
		if got.DefaultEffort != "medium" {
			t.Errorf("DefaultEffort = %q, want %q", got.DefaultEffort, "medium")
		}
		if got.MaxEffort != "max" {
			t.Errorf("MaxEffort = %q, want %q", got.MaxEffort, "max")
		}
		if got.MinBudgetTokens != nil {
			t.Errorf("MinBudgetTokens = %v, want nil", *got.MinBudgetTokens)
		}
		if got.MaxBudgetTokens != nil {
			t.Errorf("MaxBudgetTokens = %v, want nil", *got.MaxBudgetTokens)
		}
		wantLevels := []string{"medium", "high", "xhigh", "max"}
		if len(got.EffortLevels) != len(wantLevels) {
			t.Fatalf("EffortLevels = %v, want %v", got.EffortLevels, wantLevels)
		}
		for i, v := range got.EffortLevels {
			if v != wantLevels[i] {
				t.Errorf("EffortLevels[%d] = %q, want %q", i, v, wantLevels[i])
			}
		}
	})

	t.Run("supported with budget tokens", func(t *testing.T) {
		t.Parallel()
		in := adapter.ThinkingInput{
			Supported:      true,
			DefaultEffort:  adapter.ThinkingLow,
			MaxEffort:      adapter.ThinkingHigh,
			MinBudgetToken: 1024,
			MaxBudgetToken: 64000,
		}
		got := MapThinking(in)
		if got == nil {
			t.Fatal("MapThinking returned nil")
		}
		if got.MinBudgetTokens == nil || *got.MinBudgetTokens != 1024 {
			t.Errorf("MinBudgetTokens = %v, want 1024", got.MinBudgetTokens)
		}
		if got.MaxBudgetTokens == nil || *got.MaxBudgetTokens != 64000 {
			t.Errorf("MaxBudgetTokens = %v, want 64000", got.MaxBudgetTokens)
		}
		wantLevels := []string{"low", "medium", "high"}
		if len(got.EffortLevels) != len(wantLevels) {
			t.Fatalf("EffortLevels = %v, want %v", got.EffortLevels, wantLevels)
		}
	})

	t.Run("zero budget tokens are omitted", func(t *testing.T) {
		t.Parallel()
		in := adapter.ThinkingInput{
			Supported:      true,
			DefaultEffort:  adapter.ThinkingMedium,
			MaxEffort:      adapter.ThinkingHigh,
			MinBudgetToken: 0,
			MaxBudgetToken: 0,
		}
		got := MapThinking(in)
		if got == nil {
			t.Fatal("MapThinking returned nil")
		}
		if got.MinBudgetTokens != nil {
			t.Errorf("MinBudgetTokens = %v, want nil", got.MinBudgetTokens)
		}
		if got.MaxBudgetTokens != nil {
			t.Errorf("MaxBudgetTokens = %v, want nil", got.MaxBudgetTokens)
		}
	})

	t.Run("single effort level", func(t *testing.T) {
		t.Parallel()
		in := adapter.ThinkingInput{
			Supported:     true,
			DefaultEffort: adapter.ThinkingMedium,
			MaxEffort:     adapter.ThinkingMedium,
		}
		got := MapThinking(in)
		if got == nil {
			t.Fatal("MapThinking returned nil")
		}
		wantLevels := []string{"medium"}
		if len(got.EffortLevels) != len(wantLevels) {
			t.Fatalf("EffortLevels = %v, want %v", got.EffortLevels, wantLevels)
		}
	})
}

func TestEffortLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		def       adapter.ThinkingEffort
		maxEffort adapter.ThinkingEffort
		want      []string
	}{
		{
			name:      "none to max",
			def:       adapter.ThinkingNone,
			maxEffort: adapter.ThinkingMax,
			want:      []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"},
		},
		{
			name:      "medium to high",
			def:       adapter.ThinkingMedium,
			maxEffort: adapter.ThinkingHigh,
			want:      []string{"medium", "high"},
		},
		{
			name:      "single level",
			def:       adapter.ThinkingLow,
			maxEffort: adapter.ThinkingLow,
			want:      []string{"low"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := effortLevels(tc.def, tc.maxEffort)
			if len(got) != len(tc.want) {
				t.Fatalf("effortLevels(%q, %q) = %v, want %v", tc.def, tc.maxEffort, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("effortLevels[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
