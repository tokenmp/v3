package openaiadapter

import (
	"encoding/json"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func TestExtractOpenAIChatUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		usage sdk.Usage
		known bool
	}{
		{
			name:  "normal",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
			usage: sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			known: true,
		},
		{
			name:  "zero usage",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			usage: sdk.Usage{},
			known: true,
		},
		{
			name:  "missing usage object",
			raw:   `{"id":"chatcmpl-1","choices":[]}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "missing prompt_tokens",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"completion_tokens":20,"total_tokens":30}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "missing completion_tokens",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"total_tokens":30}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "missing total_tokens",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "negative prompt_tokens",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":-1,"completion_tokens":20,"total_tokens":19}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "negative completion_tokens",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":-5,"total_tokens":5}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "negative total_tokens",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":-1}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "inconsistent total",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":31}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "total exceeds cap",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":500000,"completion_tokens":500001,"total_tokens":1000001}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "prompt exceeds cap",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":1000001,"completion_tokens":0,"total_tokens":1000001}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "extra fields ignored",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":5}}}`,
			usage: sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			known: true,
		},
		{
			name:  "empty raw",
			raw:   ``,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "invalid json",
			raw:   `{broken`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "at exact cap",
			raw:   `{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":1000000,"completion_tokens":0,"total_tokens":1000000}}`,
			usage: sdk.Usage{PromptTokens: 1000000, CompletionTokens: 0, TotalTokens: 1000000},
			known: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, known := extractOpenAIChatUsage(json.RawMessage(tc.raw))
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if usage != tc.usage {
				t.Fatalf("usage = %+v, want %+v", usage, tc.usage)
			}
		})
	}
}
