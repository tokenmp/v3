package openaiadapter

import (
	"context"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestDecodeChatParams_AcceptsContractToolsChoiceReasoningAndVision(t *testing.T) {
	body := []byte(`{"model":"caller","stream":false,"temperature":2,"top_p":0,"max_tokens":1,"max_completion_tokens":1,"reasoning_effort":"high","stop":["END"],"response_format":{"type":"json_object"},"user":"user-1","tools":[{"type":"function","function":{"name":"weather","description":"Look up weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]},"strict":true}}],"tool_choice":{"type":"function","function":{"name":"weather"}},"messages":[{"role":"system","content":"rules"},{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"https://example.test/image.png","detail":"low"}}]},{"role":"assistant","content":"calling a tool","reasoning_content":"private reasoning","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"sunny"}]}`)
	value, parseErr := parseStrictJSON(context.Background(), body)
	if parseErr != nil || validateChatRequest(value) != nil {
		t.Fatalf("strict validation: parse=%v validation=%v", parseErr, validateChatRequest(value))
	}
	params, err := decodeChatParams(context.Background(), body, adapter.EffectiveThinking{})
	if err != nil {
		t.Fatalf("decodeChatParams: %v", err)
	}
	if len(params.Messages) != 4 {
		t.Fatalf("messages = %d", len(params.Messages))
	}
}

func TestDecodeChatParams_RejectsInvalidRequests(t *testing.T) {
	deep := `{"messages":` + strings.Repeat(`[`, maxJSONDepth) + `"x"` + strings.Repeat(`]`, maxJSONDepth) + `}`
	cases := map[string]string{
		"missing messages":                         `{}`,
		"empty messages":                           `{"messages":[]}`,
		"messages not array":                       `{"messages":{}}`,
		"invalid role":                             `{"messages":[{"role":"other","content":"x"}]}`,
		"message not object":                       `{"messages":["x"]}`,
		"unknown message field":                    `{"messages":[{"role":"user","content":"x","extra":true}]}`,
		"unknown nested part field":                `{"messages":[{"role":"user","content":[{"type":"text","text":"x","extra":true}]}]}`,
		"unknown part type":                        `{"messages":[{"role":"user","content":[{"type":"video","video":"x"}]}]}`,
		"bad content union":                        `{"messages":[{"role":"user","content":null}]}`,
		"tool without ID":                          `{"messages":[{"role":"tool","content":"x"}]}`,
		"bad tool calls":                           `{"messages":[{"role":"assistant","tool_calls":[{"id":"x","type":"not-function","function":{"name":"f","arguments":"{}"}}]}]}`,
		"root duplicate":                           `{"messages":[],"messages":[]}`,
		"nested duplicate":                         `{"messages":[{"role":"user","content":"x","content":"y"}]}`,
		"prototype nested":                         `{"messages":[{"role":"user","content":{"__proto__":"x"}}]}`,
		"stream options":                           `{"stream_options":{},"messages":[{"role":"user","content":"x"}]}`,
		"missing model":                            `{"messages":[{"role":"user","content":"x"}]}`,
		"developer role outside contract":          `{"model":"m","messages":[{"role":"developer","content":"x"}]}`,
		"assistant function call outside contract": `{"model":"m","messages":[{"role":"assistant","content":"x","function_call":{"name":"f","arguments":"{}"}}]}`,
		"tool parameters required":                 `{"model":"m","tools":[{"type":"function","function":{"name":"f"}}],"messages":[{"role":"user","content":"x"}]}`,
		"tool unknown nested field":                `{"model":"m","tools":[{"type":"function","function":{"name":"f","parameters":{},"extra":true}}],"messages":[{"role":"user","content":"x"}]}`,
		"tool parameters not object":               `{"model":"m","tools":[{"type":"function","function":{"name":"f","parameters":[]}}],"messages":[{"role":"user","content":"x"}]}`,
		"tool choice bad string":                   `{"model":"m","tool_choice":"function","messages":[{"role":"user","content":"x"}]}`,
		"tool choice unknown nested field":         `{"model":"m","tool_choice":{"type":"function","function":{"name":"f","extra":true}},"messages":[{"role":"user","content":"x"}]}`,
		"tool calls unknown nested field":          `{"model":"m","messages":[{"role":"assistant","content":"x","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}","extra":true}}]}]}`,
		"tool calls wrong argument type":           `{"model":"m","messages":[{"role":"assistant","content":"x","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":{}}}]}]}`,
		"reasoning content not string":             `{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":{}}]}`,
		"image URL not URI":                        `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"not a uri"}}]}]}`,
		"image detail invalid":                     `{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/a","detail":"full"}}]}]}`,
		"temperature over maximum":                 `{"model":"m","temperature":2.1,"messages":[{"role":"user","content":"x"}]}`,
		"max tokens not integer":                   `{"model":"m","max_tokens":1.5,"messages":[{"role":"user","content":"x"}]}`,
		"root field outside contract":              `{"model":"m","frequency_penalty":1,"messages":[{"role":"user","content":"x"}]}`,
		"model is not string":                      `{"model":1,"messages":[{"role":"user","content":"x"}]}`,
		"depth":                                    deep,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeChatParams(context.Background(), []byte(body), adapter.EffectiveThinking{}); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}

	t.Run("node limit", func(t *testing.T) {
		body := `{"messages":[` + strings.Repeat(`null,`, maxJSONNodes) + `null]}`
		if _, err := decodeChatParams(context.Background(), []byte(body), adapter.EffectiveThinking{}); err == nil {
			t.Fatal("over-limit JSON tree accepted")
		}
	})
}

func TestDecodeChatParams_RejectsInvalidUTF8AndOversize(t *testing.T) {
	if _, err := decodeChatParams(context.Background(), []byte{'{', 0xff, '}'}, adapter.EffectiveThinking{}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	body := make([]byte, maxParamBodyBytes+1)
	if _, err := decodeChatParams(context.Background(), body, adapter.EffectiveThinking{}); err == nil {
		t.Fatal("oversize body accepted")
	}
}

func FuzzDecodeChatParams(f *testing.F) {
	f.Add([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	f.Add([]byte(`{"messages":[{"role":"user","content":{"__proto__":"x"}}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = decodeChatParams(context.Background(), body, adapter.EffectiveThinking{})
	})
}
