package anthropicadapter

import (
	"encoding/json"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func TestExtractAnthropicMessagesUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		usage sdk.Usage
		known bool
	}{
		{
			name:  "normal",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":3}}`,
			usage: sdk.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			known: true,
		},
		{
			name:  "zero usage",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":0,"output_tokens":0}}`,
			usage: sdk.Usage{},
			known: true,
		},
		{
			name:  "missing usage object",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[]}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "missing input_tokens",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"output_tokens":3}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "missing output_tokens",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "negative input_tokens",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":-1,"output_tokens":3}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "negative output_tokens",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":-3}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "cache fields ignored",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":3,"cache_creation_input_tokens":100,"cache_read_input_tokens":50}}`,
			usage: sdk.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			known: true,
		},
		{
			name:  "input exceeds cap",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1000001,"output_tokens":0}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "output exceeds cap",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":0,"output_tokens":1000001}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "sum exceeds cap",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":500000,"output_tokens":500001}}`,
			usage: sdk.Usage{},
			known: false,
		},
		{
			name:  "at exact cap",
			raw:   `{"id":"msg_01","type":"message","role":"assistant","content":[],"usage":{"input_tokens":1000000,"output_tokens":0}}`,
			usage: sdk.Usage{PromptTokens: 1000000, CompletionTokens: 0, TotalTokens: 1000000},
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, known := extractAnthropicMessagesUsage(json.RawMessage(tc.raw))
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if usage != tc.usage {
				t.Fatalf("usage = %+v, want %+v", usage, tc.usage)
			}
		})
	}
}
