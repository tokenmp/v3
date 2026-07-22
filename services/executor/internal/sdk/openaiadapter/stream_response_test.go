package openaiadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func validChunk(extra string) []byte {
	return []byte(`{"id":"chat-1","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}],"provider_extension":{"nested":[true,1]}` + extra + `}`)
}

func TestParseChunkClassifiesCanonicalOwnedPayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		kind      streaming.EventKind
		finish    string
		usage     int64
	}{
		{"lifecycle role", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`, streaming.EventLifecycle, "", 0},
		{"content", string(validChunk("")), streaming.EventSemantic, "", 0},
		{"reasoning", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`, streaming.EventSemantic, "", 0},
		{"tool", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":null}]}`, streaming.EventSemantic, "", 0},
		{"usage only", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`, streaming.EventUsage, "", 3},
		{"finish", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"function_call"}]}`, streaming.EventFinish, "function_call", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, data, err := parseChunk([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind != tc.kind || ev.FinishReason != tc.finish {
				t.Fatalf("event=%+v", ev)
			}
			if tc.kind == streaming.EventUsage && ev.Usage.TotalTokens != tc.usage {
				t.Fatalf("usage=%+v", ev.Usage)
			}
			if !bytes.Equal(data, []byte(tc.raw)) && !bytes.Contains(data, []byte(`"provider_extension"`)) && tc.name == "content" {
				t.Fatalf("lost extension: %s", data)
			}
			data[0] = 'X'
			if data2 := mustParse(t, []byte(tc.raw)); data2[0] == 'X' {
				t.Fatal("payload aliases caller or parser state")
			}
		})
	}
}
func mustParse(t *testing.T, raw []byte) []byte {
	t.Helper()
	_, d, err := parseChunk(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseChunkRejectsInvalidWithoutLeakingPayload(t *testing.T) {
	t.Parallel()
	secret := "raw-provider-secret"
	cases := map[string]string{
		"blank": "", "trailing": string(validChunk("")) + `x`, "array": `[]`,
		"duplicate":       `{"id":"c","id":"` + secret + `","object":"chat.completion.chunk","created":1,"model":"m","choices":[]}`,
		"prototype":       `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"__proto__":{}}`,
		"bad required":    `{"id":"","object":"wrong","created":-1,"model":"","choices":[]}`,
		"empty no usage":  `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[]}`,
		"multi":           `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}`,
		"index":           `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":1,"delta":{}}]}`,
		"finish semantic": `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`,
		"negative usage":  `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"total_tokens":-1}}`,
		"bad tool":        `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"id":"x"}]},"finish_reason":null}]}`,
		"bad role":        `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"attacker"},"finish_reason":null}]}`,
		"bad delta":       `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":[],"finish_reason":null}]}`,
		"bad finish":      `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"unknown"}]}`,
		"missing finish":  `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{}}]}`,
		"partial usage":   `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"total_tokens":1}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseChunk([]byte(raw))
			if !errors.Is(err, errChunkProtocol) || strings.Contains(err.Error(), secret) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func FuzzParseChunk(f *testing.F) {
	f.Add(validChunk(""))
	f.Add([]byte(`{"error":{"message":"secret"}}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, data, err := parseChunk(raw)
		if err == nil && len(data) != 0 && !jsonValid(data) {
			t.Fatal("accepted non-json output")
		}
	})
}
func jsonValid(b []byte) bool { var x any; return len(b) > 0 && json.Unmarshal(b, &x) == nil }
