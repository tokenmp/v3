package routing

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSelector(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      Selector
		wantError bool
	}{
		{name: "model", input: "gpt-4.1", want: Selector{Model: "gpt-4.1"}},
		{name: "model group", input: "gpt-4.1:premium", want: Selector{Model: "gpt-4.1", Group: "premium"}},
		{name: "model provider", input: "gpt-4.1@openai", want: Selector{Model: "gpt-4.1", Provider: "openai"}},
		{name: "all segments", input: "gpt-4.1:premium@openai", want: Selector{Model: "gpt-4.1", Group: "premium", Provider: "openai"}},
		{name: "auto", input: "auto", want: Selector{Model: "auto", Auto: true}},
		{name: "auto provider", input: "auto@openai", want: Selector{Model: "auto", Provider: "openai", Auto: true}},
		{name: "unicode non space segment", input: "模型:组@提供商", want: Selector{Model: "模型", Group: "组", Provider: "提供商"}},
		{name: "empty", input: "", wantError: true},
		{name: "empty model", input: ":group", wantError: true},
		{name: "empty group", input: "model:", wantError: true},
		{name: "empty provider", input: "model@", wantError: true},
		{name: "provider before group", input: "model@provider:group", wantError: true},
		{name: "multiple providers", input: "model@one@two", wantError: true},
		{name: "multiple groups", input: "model:one:two", wantError: true},
		{name: "auto group", input: "auto:group", wantError: true},
		{name: "auto group provider", input: "auto:group@provider", wantError: true},
		{name: "ascii space", input: "model group", wantError: true},
		{name: "ascii tab", input: "model\tgroup", wantError: true},
		{name: "ascii control", input: "model\x7fgroup", wantError: true},
		{name: "unicode space", input: "model\u00a0group", wantError: true},
		{name: "unicode control", input: "model\u0085group", wantError: true},
		{name: "invalid utf8", input: string([]byte{'m', 0xff}), wantError: true},
		{name: "total too long", input: strings.Repeat("m", maxSelectorLength+1), wantError: true},
		{name: "model too long", input: strings.Repeat("m", maxSegmentLength+1), wantError: true},
		{name: "group too long", input: "m:" + strings.Repeat("g", maxSegmentLength+1), wantError: true},
		{name: "provider too long", input: "m@" + strings.Repeat("p", maxSegmentLength+1), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSelector(tt.input)
			if tt.wantError {
				if !errors.Is(err, ErrInvalidSelector) {
					t.Fatalf("ParseSelector(%q) error = %v, want ErrInvalidSelector", tt.input, err)
				}
				if err != ErrInvalidSelector {
					t.Fatalf("ParseSelector(%q) error = %v, want sentinel directly", tt.input, err)
				}
				if strings.Contains(err.Error(), tt.input) && tt.input != "" {
					t.Fatalf("error unexpectedly reflects selector %q: %v", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSelector(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseSelector(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSelectorCanonicalRoundTrip(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"model", "model:group", "model@provider", "model:group@provider", "auto", "auto@provider", "模型:组@提供商"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			parsed, err := ParseSelector(input)
			if err != nil {
				t.Fatalf("ParseSelector(%q): %v", input, err)
			}
			if got := parsed.Canonical(); got != input {
				t.Fatalf("Canonical() = %q, want %q", got, input)
			}
			if got := parsed.String(); got != input {
				t.Fatalf("String() = %q, want %q", got, input)
			}
			reparsed, err := ParseSelector(parsed.Canonical())
			if err != nil {
				t.Fatalf("reparse canonical: %v", err)
			}
			if reparsed != parsed {
				t.Fatalf("reparsed canonical = %#v, want %#v", reparsed, parsed)
			}
		})
	}
}

func FuzzParseSelector(f *testing.F) {
	for _, input := range []string{"model", "model:group@provider", "auto@provider", "", "@", "\xff", "模型:组@提供商"} {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		selector, err := ParseSelector(input)
		if err != nil {
			if !errors.Is(err, ErrInvalidSelector) {
				t.Fatalf("ParseSelector returned non-sentinel error %v", err)
			}
			return
		}

		reparsed, err := ParseSelector(selector.Canonical())
		if err != nil {
			t.Fatalf("ParseSelector(Canonical()) = %v", err)
		}
		if reparsed != selector {
			t.Fatalf("ParseSelector(Canonical()) = %#v, want %#v", reparsed, selector)
		}
	})
}
