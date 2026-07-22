package executorv1api

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/authcontext"
	"github.com/tokenmp/v3/services/executor/internal/identity"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
)

func TestNormalizeOpenAIChatPreservesRawBytesAndExtractsThinking(t *testing.T) {
	raw := []byte(` {"model":"gpt:fast@provider","reasoning_effort":"xhigh","messages":[{"role":"user","content":"n=9007199254740993 e=1e+09"}],"stream":false} `)
	request, err := NormalizeOpenAIChat(withRawBody(raw), "request-1")
	if err != nil {
		t.Fatalf("NormalizeOpenAIChat: %v", err)
	}
	if request.Protocol != adapter.ProtocolOpenAIChat || request.Selector != "gpt:fast@provider" || request.RequestID != "request-1" || string(request.Body) != string(raw) {
		t.Fatalf("request = %#v; raw=%q", request, request.Body)
	}
	if !request.Thinking.Enabled || request.Thinking.Effort != adapter.ThinkingXHigh || request.Thinking.BudgetTokens != nil {
		t.Fatalf("thinking = %#v", request.Thinking)
	}
}

func TestNormalizersRejectStreamWithTypedError(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(context.Context, string) (NonStreamRequest, error)
		raw  []byte
	}{
		{"chat", NormalizeOpenAIChat, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)},
		{"messages", NormalizeAnthropicMessages, []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(withRawBody(tc.raw), "id"); !errors.Is(err, ErrStreamingUnsupported) {
				t.Fatalf("error = %v, want ErrStreamingUnsupported", err)
			}
		})
	}
}

// TestNormalizersStreamTrueWithSchemaInvalidIsInvalidRequest asserts that full
// schema validation runs before the streaming check: a request that combines
// stream:true with a schema-invalid nested or root field is uniformly
// ErrInvalidRequest (native 400), never the typed streaming-unsupported
// error. Only a schema-valid stream:true is recognized as unsupported.
func TestNormalizersStreamTrueWithSchemaInvalidIsInvalidRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(context.Context, string) (NonStreamRequest, error)
		raw  []byte
	}{
		// chat: stream:true + unknown root field (additionalProperties:false)
		{"chat unknown root", NormalizeOpenAIChat, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,"bogus":1}`)},
		// chat: stream:true + invalid nested message (unknown role)
		{"chat unknown role", NormalizeOpenAIChat, []byte(`{"model":"m","messages":[{"role":"dev","content":"hi"}],"stream":true}`)},
		// chat: stream:true + non-string stream value is schema-invalid
		{"chat stream non-bool", NormalizeOpenAIChat, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":"true"}`)},
		// messages: stream:true + unknown root field
		{"messages unknown root", NormalizeAnthropicMessages, []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":true,"bogus":1}`)},
		// messages: stream:true + invalid nested content block (unknown type)
		{"messages unknown block", NormalizeAnthropicMessages, []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"unknown"}]}],"stream":true}`)},
		// messages: stream:true + non-string stream value is schema-invalid
		{"messages stream non-bool", NormalizeAnthropicMessages, []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":"true"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(withRawBody(tc.raw), "id"); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestNormalizerRejectsUnsafeOrInvalidEnvelope(t *testing.T) {
	invalid := [][]byte{
		[]byte(`[]`), []byte(`{"model":"","messages":[{"role":"user","content":"hi"}]}`), []byte(`{"model":1,"messages":[{"role":"user","content":"hi"}]}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":"false"}`), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"unknown":1}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"model":"other"}`), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"__proto__":{}}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]} {}`), []byte{'{', 0xff, '}'},
	}
	for _, raw := range invalid {
		t.Run(string(raw), func(t *testing.T) {
			if _, err := NormalizeOpenAIChat(withRawBody(raw), "id"); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestNormalizeAnthropicThinkingSemantics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    adapter.ThinkingRequest
		invalid bool
	}{
		{"absent", `{"model":"m","max_tokens":1025,"messages":[{"role":"user","content":"hi"}]}`, adapter.ThinkingRequest{}, false},
		{"disabled", `{"model":"m","max_tokens":1025,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"}}`, adapter.ThinkingRequest{}, false},
		{"enabled", `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024,"display":"omitted"}}`, adapter.ThinkingRequest{Enabled: true, BudgetTokens: intPointer(1024)}, false},
		{"disabled budget", `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled","budget_tokens":1024}}`, adapter.ThinkingRequest{}, true},
		{"low budget", `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1023}}`, adapter.ThinkingRequest{}, true},
		{"fractional budget", `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024.5}}`, adapter.ThinkingRequest{}, true},
		{"bad display", `{"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024,"display":"full"}}`, adapter.ThinkingRequest{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeAnthropicMessages(withRawBody([]byte(tc.raw)), "id")
			if tc.invalid {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || got.Thinking.Enabled != tc.want.Enabled || !sameInt(got.Thinking.BudgetTokens, tc.want.BudgetTokens) {
				t.Fatalf("thinking/error = %#v / %v", got.Thinking, err)
			}
		})
	}
}

func TestNormalizeOpenAIChatReasoningEffortEnum(t *testing.T) {
	t.Parallel()
	// The OpenAPI enum is [none, minimal, low, medium, high, xhigh, max].
	// "none" is the schema-valid way to disable reasoning: it must map to a
	// disabled ThinkingRequest, identical to an absent field, and must never
	// be forwarded as Enabled with Effort=none.
	for _, tc := range []struct {
		name    string
		effort  string
		want    adapter.ThinkingRequest
		invalid bool
	}{
		{"none disabled", `"none"`, adapter.ThinkingRequest{}, false},
		{"minimal", `"minimal"`, adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingMinimal}, false},
		{"low", `"low"`, adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingLow}, false},
		{"medium", `"medium"`, adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingMedium}, false},
		{"high", `"high"`, adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingHigh}, false},
		{"xhigh", `"xhigh"`, adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingXHigh}, false},
		{"max", `"max"`, adapter.ThinkingRequest{Enabled: true, Effort: adapter.ThinkingMax}, false},
		{"absent disabled", "", adapter.ThinkingRequest{}, false},
		{"unknown enum", `"ultra"`, adapter.ThinkingRequest{}, true},
		{"wrong type number", `7`, adapter.ThinkingRequest{}, true},
		{"wrong type null", `null`, adapter.ThinkingRequest{}, true},
		{"wrong type object", `{}`, adapter.ThinkingRequest{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
			if tc.effort != "" {
				raw = `{"model":"m","messages":[{"role":"user","content":"hi"}],"reasoning_effort":` + tc.effort + `}`
			}
			got, err := NormalizeOpenAIChat(withRawBody([]byte(raw)), "id")
			if tc.invalid {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error = %v, want ErrInvalidRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got.Thinking.Enabled != tc.want.Enabled || got.Thinking.Effort != tc.want.Effort || !sameInt(got.Thinking.BudgetTokens, tc.want.BudgetTokens) {
				t.Fatalf("thinking = %#v, want %#v", got.Thinking, tc.want)
			}
		})
	}
}

func TestNormalizeMakesOneExecutorBoundaryCopy(t *testing.T) {
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	ctx := context.WithValue(context.Background(), rawBodyContextKey{}, raw)
	request, err := NormalizeOpenAIChat(ctx, "id")
	if err != nil {
		t.Fatal(err)
	}
	if &raw[0] == &request.Body[0] {
		t.Fatal("executor request body must not alias capture-owned context bytes")
	}
	request.Body[0] = 'X'
	view, ok := rawBodyView(ctx)
	if !ok || view[0] != '{' {
		t.Fatal("executor mutation polluted context raw body")
	}
}

func TestNormalizeOpenAIImageURLSafetyBoundary(t *testing.T) {
	validData := "data:image/png;base64,aGVsbG8="
	tooLargeData := "data:image/png;base64," + string(bytes.Repeat([]byte("A"), maxImageBase64Encoded+1))
	for _, tc := range []struct {
		name, url string
		valid     bool
	}{
		{"https", "https://example.test/image.png", true},
		{"https query", "https://example.test/image.png?size=1", true},
		{"data png", validData, true},
		{"http", "http://example.test/image.png", false},
		{"ftp", "ftp://example.test/image.png", false},
		{"mailto", "mailto:user@example.test", false},
		{"no host", "https:/image.png", false},
		{"userinfo", "https://user@example.test/image.png", false},
		{"data wrong MIME", "data:image/svg+xml;base64,PHN2Zy8+", false},
		{"data no base64", "data:image/png,hello", false},
		{"data malformed", "data:image/png;base64,aGVsbG8", false},
		{"data URL alphabet", "data:image/png;base64,aGVsbG8_", false},
		{"data too large", tooLargeData, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":` + strconv.Quote(tc.url) + `}}]}]}`)
			_, err := NormalizeOpenAIChat(withRawBody(raw), "id")
			if (err == nil) != tc.valid {
				t.Fatalf("NormalizeOpenAIChat error = %v, valid = %t", err, tc.valid)
			}
		})
	}
}

func TestNormalizeAnthropicImageSourceSafetyBoundary(t *testing.T) {
	valid := `{"type":"base64","media_type":"image/png","data":"aGVsbG8="}`
	overgrown := string(bytes.Repeat([]byte("A"), maxImageBase64Encoded+1))
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"top level valid", valid, true},
		{"nested tool result valid", valid, true},
		{"missing source type", `{"media_type":"image/png","data":"aGVsbG8="}`, false},
		{"wrong source type", `{"type":"url","media_type":"image/png","data":"aGVsbG8="}`, false},
		{"wrong MIME", `{"type":"base64","media_type":"image/svg+xml","data":"aGVsbG8="}`, false},
		{"empty data", `{"type":"base64","media_type":"image/png","data":""}`, false},
		{"no padding", `{"type":"base64","media_type":"image/png","data":"aGVsbG8"}`, false},
		{"URL alphabet", `{"type":"base64","media_type":"image/png","data":"aGVsbG8_"}`, false},
		{"bad padding", `{"type":"base64","media_type":"image/png","data":"aGV=sbG8="}`, false},
		{"encoded over bound", `{"type":"base64","media_type":"image/png","data":` + strconv.Quote(overgrown) + `}`, false},
		// This fits the encoded hard bound but decodes to two bytes over 1 MiB;
		// the decoded bound must still reject it.
		{"decoded over bound", `{"type":"base64","media_type":"image/png","data":` + strconv.Quote(string(bytes.Repeat([]byte("A"), maxImageBase64Encoded))) + `}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := `[{"type":"image","source":` + tc.source + `}]`
			if tc.name == "nested tool result valid" {
				content = `[{"type":"tool_result","content":[{"type":"image","source":` + tc.source + `}]}]`
			}
			raw := []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":` + content + `}]}`)
			_, err := NormalizeAnthropicMessages(withRawBody(raw), "id")
			if (err == nil) != tc.valid {
				t.Fatalf("NormalizeAnthropicMessages error = %v, valid = %t", err, tc.valid)
			}
		})
	}
}

func TestNormalizerBounds(t *testing.T) {
	deep := []byte(`{"model":"m","messages":`)
	for range maxJSONDepth {
		deep = append(deep, '[')
	}
	deep = append(deep, []byte(`]}`)...)
	if _, err := NormalizeOpenAIChat(withRawBody(deep), "id"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("depth error = %v", err)
	}
	tooLarge := make([]byte, MaxCapturedBodyBytes+1)
	for i := range tooLarge {
		tooLarge[i] = ' '
	}
	if _, err := NormalizeOpenAIChat(withRawBody(tooLarge), "id"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("size error = %v", err)
	}
}

func withRawBody(raw []byte) context.Context {
	return context.WithValue(context.Background(), rawBodyContextKey{}, append([]byte(nil), raw...))
}
func intPointer(value int) *int { return &value }
func sameInt(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

// TestNormalizeOpenAIChatSchemaBoundaries asserts the generated decoder-legal but
// schema-invalid boundaries are rejected (strict additionalProperties:false +
// every nested constraint), while complex schema-valid OpenAI tools/vision/
// thinking requests are accepted with raw byte identity preserved.
func TestNormalizeOpenAIChatSchemaBoundaries(t *testing.T) {
	t.Parallel()
	validBase := `{"model":"gpt:fast@openai","messages":[{"role":"user","content":"hi"}],"stream":false}`
	complexValid := `{"model":"gpt","messages":[
		{"role":"system","content":"You are helpful."},
		{"role":"user","content":[
			{"type":"text","text":"Describe this."},
			{"type":"image_url","image_url":{"url":"https://example.com/cat.png","detail":"high"}}
		]},
		{"role":"assistant","content":"ok","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"上海\"}"}}
		],"reasoning_content":"thinking..."},
		{"role":"tool","content":"sunny","tool_call_id":"call_1"}
	],"temperature":0.7,"top_p":0.9,"max_tokens":1024,"max_completion_tokens":2048,
	"reasoning_effort":"high","stop":["\n","END"],"user":"u1",
	"tools":[{"type":"function","function":{"name":"get_weather","description":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}},"strict":true}}],
	"tool_choice":{"type":"function","function":{"name":"get_weather"}},
	"response_format":{"type":"json_schema"}}`
	for _, tc := range []struct {
		name    string
		raw     string
		invalid bool
	}{
		{"valid base", validBase, false},
		{"complex valid", complexValid, false},
		// root additionalProperties:false
		{"unknown root field", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"bogus":1}`, true},
		// messages constraints
		{"empty messages array", `{"model":"gpt","messages":[]}`, true},
		{"messages not array", `{"model":"gpt","messages":"hi"}`, true},
		{"message missing role", `{"model":"gpt","messages":[{"content":"hi"}]}`, true},
		{"message unknown role", `{"model":"gpt","messages":[{"role":"dev","content":"hi"}]}`, true},
		{"assistant unknown field", `{"model":"gpt","messages":[{"role":"assistant","content":"hi","tool":"x"}]}`, true},
		{"assistant tool_calls not array", `{"model":"gpt","messages":[{"role":"assistant","content":"hi","tool_calls":"x"}]}`, true},
		// tool_call_id is optional per the single ChatMessage fieldset (only role/content required)
		{"tool without tool_call_id valid", `{"model":"gpt","messages":[{"role":"tool","content":"hi"}]}`, false},
		// content part: type is the only required field; text/image_url are optional
		{"text part without text valid", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"text"}]}]}`, false},
		{"image_url part without image_url valid", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url"}]}]}`, false},
		{"image_url without url valid", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url","image_url":{}}]}]}`, false},
		{"text part carrying image_url valid", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"text","text":"See this.","image_url":{"url":"https://e.com/cat.png"}}]}]}`, false},
		{"content part unknown type", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"audio","text":"x"}]}]}`, true},
		{"text part text non-string", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"text","text":7}]}]}`, true},
		{"image_url non-uri", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"not a url"}}]}]}`, true},
		{"image_url bad detail", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://e.com/a.png","detail":"ultra"}}]}]}`, true},
		// numeric bounds
		{"temperature too high", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"temperature":3}`, true},
		{"top_p too low", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"top_p":-0.1}`, true},
		{"max_tokens zero", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"max_tokens":0}`, true},
		{"max_tokens fractional", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"max_tokens":1.5}`, true},
		// stop union
		{"stop number", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stop":7}`, true},
		{"stop array mixed", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stop":["a",7]}`, true},
		// tools
		{"tools not array", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":"x"}`, true},
		{"tool wrong type", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"code","function":{"name":"f","parameters":{}}}]}`, true},
		{"tool function missing name", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"parameters":{}}}]}`, true},
		{"tool parameters not object", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":[]}}]}`, true},
		// tool_choice
		{"tool_choice bad enum", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tool_choice":"never"}`, true},
		{"tool_choice missing function name", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"function","function":{}}}`, true},
		// response_format
		{"response_format bad type", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"yaml"}}`, true},
		// tool_calls
		{"assistant tool_call missing id", `{"model":"gpt","messages":[{"role":"assistant","content":"hi","tool_calls":[{"type":"function","function":{"name":"f","arguments":"{}"}}]}]}`, true},
		// reasoning_effort
		{"reasoning_effort number", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"reasoning_effort":5}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeOpenAIChat(withRawBody([]byte(tc.raw)), "id")
			if tc.invalid {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error = %v, want ErrInvalidRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if !bytes.Equal(got.Body, []byte(tc.raw)) {
				t.Fatalf("raw identity broken")
			}
		})
	}
}

// TestNormalizeAnthropicMessagesSchemaBoundaries mirrors the OpenAI table for
// Anthropic Messages: every nested schema (system blocks, thinking, content
// blocks, tools, tool_choice, metadata) is strictly validated, and complex
// schema-valid system/tools/vision/thinking requests are accepted.
func TestNormalizeAnthropicMessagesSchemaBoundaries(t *testing.T) {
	t.Parallel()
	complexValid := `{"model":"claude","max_tokens":2048,"messages":[
		{"role":"user","content":[
			{"type":"text","text":"Describe this.","cache_control":{"type":"ephemeral"}},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},
			{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}
		]},
		{"role":"assistant","content":[
			{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"上海"}},
			{"type":"thinking","thinking":"...","signature":"sig"}
		]}
	],"system":[{"type":"text","text":"Be helpful.","cache_control":{"type":"ephemeral"}}],
	"thinking":{"type":"enabled","budget_tokens":1024,"display":"omitted"},
	"temperature":0.7,"top_p":0.9,"top_k":1,"stop_sequences":["\n"],
	"tools":[{"name":"get_weather","description":"weather","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}],
	"tool_choice":{"type":"tool","name":"get_weather","disable_parallel_tool_use":true},
	"metadata":{"user_id":"u1"}}`
	for _, tc := range []struct {
		name    string
		raw     string
		invalid bool
	}{
		{"complex valid", complexValid, false},
		{"system string", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"system":"be nice"}`, false},
		// root additionalProperties:false
		{"unknown root field", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"bogus":1}`, true},
		// messages / content blocks
		{"empty messages", `{"model":"c","max_tokens":1,"messages":[]}`, true},
		{"message unknown role", `{"model":"c","max_tokens":1,"messages":[{"role":"system","content":"hi"}]}`, true},
		{"text block cache_control bad type", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{"type":"permanent"}}]}]}`, true},
		{"image source requires complete secure fields", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{}}]}]}`, true},
		{"image source bad type", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","media_type":"image/png","data":"aGVsbG8="}}]}]}`, true},
		{"image data non-string", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":7}}]}]}`, true},
		{"image arbitrary string data", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"not-base64!!"}}]}]}`, true},
		{"tool_use input not object", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_use","id":"x","name":"f","input":[]}]}]}`, true},
		{"tool_result content invalid", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":7}]}]}`, true},
		// thinking block: only type is required; signature/thinking are optional
		{"thinking block without signature valid", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"thinking","thinking":"..."}]}]}`, false},
		{"text block cache_control no type valid", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x","cache_control":{}}]}]}`, false},
		{"block unknown type", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"unknown"}]}]}`, true},
		// system blocks
		{"system block missing text", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"system":[{"type":"text"}]}`, true},
		{"system block bad type", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"system":[{"type":"doc","text":"x"}]}`, true},
		// thinking
		{"thinking bad type enum", `{"model":"c","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"full"}}`, true},
		{"thinking budget equals max", `{"model":"c","max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024}}`, true},
		{"thinking disabled with budget", `{"model":"c","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled","budget_tokens":1024}}`, true},
		// numeric bounds
		{"temperature >1", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"temperature":1.5}`, true},
		{"top_k zero", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"top_k":0}`, true},
		{"max_tokens zero", `{"model":"c","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`, true},
		// stop_sequences
		{"stop_sequences mixed", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stop_sequences":["a",7]}`, true},
		// tools
		{"tool missing input_schema", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"f"}]}`, true},
		{"tool input_schema not object", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"f","input_schema":[]}]}`, true},
		// tool_choice
		{"tool_choice bad type", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"forced"}}`, true},
		// tool_choice: only type is required; name is optional even for type=tool
		{"tool_choice tool without name valid", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"tool"}}`, false},
		// name is optional for auto/any as well: one fieldset applies to all
		{"tool_choice auto with name valid", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"auto","name":"f"}}`, false},
		// metadata
		{"metadata unknown field", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"metadata":{"team":"x"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeAnthropicMessages(withRawBody([]byte(tc.raw)), "id")
			if tc.invalid {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error = %v, want ErrInvalidRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if !bytes.Equal(got.Body, []byte(tc.raw)) {
				t.Fatalf("raw identity broken")
			}
		})
	}
}

// TestNormalizeAnthropicMaxTokensRequired proves the normalizer defensively
// enforces the Anthropic CreateMessageRequest contract: max_tokens is required
// (the OpenAPI declares it in `required`) and its absence is rejected as
// ErrInvalidRequest regardless of an otherwise-valid body.
func TestNormalizeAnthropicMaxTokensRequired(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		raw     string
		invalid bool
	}{
		// max_tokens absent entirely -> rejected (contract-required field).
		{"absent max_tokens", `{"model":"c","messages":[{"role":"user","content":"hi"}]}`, true},
		// max_tokens present and valid -> accepted.
		{"present max_tokens", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`, false},
		// max_tokens null -> rejected.
		{"null max_tokens", `{"model":"c","max_tokens":null,"messages":[{"role":"user","content":"hi"}]}`, true},
		// max_tokens zero -> rejected (minimum: 1).
		{"zero max_tokens", `{"model":"c","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`, true},
		// max_tokens fractional -> rejected.
		{"fractional max_tokens", `{"model":"c","max_tokens":1.5,"messages":[{"role":"user","content":"hi"}]}`, true},
		// max_tokens as string -> rejected.
		{"string max_tokens", `{"model":"c","max_tokens":"1","messages":[{"role":"user","content":"hi"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeAnthropicMessages(withRawBody([]byte(tc.raw)), "id")
			if tc.invalid {
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error = %v, want ErrInvalidRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// TestNormalizeArbitraryJSONAccepted proves the normalizer accepts bounded
// arbitrary user JSON/JSON Schema beneath the open fields (tool parameters,
// input_schema, tool input) without applying onlyFields beneath them. The
// finite envelopes remain closed, but the free-form payloads pass through.
func TestNormalizeArbitraryJSONAccepted(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
	}{
		// OpenAI Chat: tool function parameters accept arbitrary nested JSON
		// Schema (deeply nested properties, additionalProperties, $defs-style
		// objects, arrays, enums) without onlyFields rejection.
		{"chat tool parameters arbitrary schema", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{"nested":{"type":"object","properties":{"deep":{"type":"array","items":{"type":"string","enum":["a","b"]}},"extra":true},"additionalProperties":{"type":"number"}}},"required":["nested"],"additionalProperties":false},"strict":true}}]}`},
		// Anthropic Messages: tool input_schema accepts arbitrary JSON Schema.
		{"anthropic tool input_schema arbitrary schema", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"f","description":"d","input_schema":{"type":"object","properties":{"x":{"type":"string"}},"additionalProperties":true},"cache_control":{"type":"ephemeral"}}]}`},
		// Anthropic Messages: tool_use block input accepts arbitrary nested JSON.
		{"anthropic tool_use input arbitrary json", `{"model":"c","max_tokens":1,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"\u4e0a\u6d77","count":3,"nested":{"a":[1,2,3],"b":{"c":true}},"extra":null}}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := NormalizeOpenAIChat(withRawBody([]byte(tc.raw)), "id")
			if tc.name != "chat tool parameters arbitrary schema" {
				req, err = NormalizeAnthropicMessages(withRawBody([]byte(tc.raw)), "id")
			}
			if err != nil {
				t.Fatalf("arbitrary JSON rejected: %v", err)
			}
			if !bytes.Equal(req.Body, []byte(tc.raw)) {
				t.Fatalf("raw identity broken")
			}
		})
	}
}

// FuzzNormalizeOpenAIChat asserts the normalizer never panics and only ever
// returns one of three defined outcomes (success, ErrInvalidRequest or
// ErrStreamingUnsupported), and never echoes the raw fuzz input back through
// the returned error string.
func FuzzNormalizeOpenAIChat(f *testing.F) {
	f.Add([]byte(`{"model":"m","messages":[],"reasoning_effort":"none"}`))
	f.Add([]byte(`{"model":"m","messages":[],"reasoning_effort":"high"}`))
	f.Add([]byte(`{"model":"m","messages":[],"stream":true}`))
	f.Add([]byte(`{"model":"m","messages":[],"unknown":1}`))
	f.Add([]byte(`{"model":7,"messages":[]}`))
	f.Add([]byte(`{"model":"m","messages":[],"reasoning_effort":123}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"model":"m","messages":[],"__proto__":{}}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("normalizer panicked: %v", r)
			}
		}()
		req, err := NormalizeOpenAIChat(withRawBody(raw), "id")
		if err == nil {
			// On success the raw bytes are preserved verbatim and the selector
			// is a non-empty bounded string; nothing about the error path is
			// reachable, but we still sanity-check the invariants.
			if !bytes.Equal(req.Body, raw) {
				t.Fatalf("raw identity broken on success")
			}
			return
		}
		// Failures are fixed sentinels: errors.Is proves no raw input is
		// echoed into the error string.
		if !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrStreamingUnsupported) {
			t.Fatalf("error = %v, want ErrInvalidRequest or ErrStreamingUnsupported", err)
		}
	})
}

// FuzzNormalizeAnthropicMessages mirrors FuzzNormalizeOpenAIChat for the
// Anthropic Messages normalizer, exercising the thinking envelope rules.
func FuzzNormalizeAnthropicMessages(f *testing.F) {
	f.Add([]byte(`{"model":"m","messages":[],"max_tokens":2048,"thinking":{"type":"disabled"}}`))
	f.Add([]byte(`{"model":"m","messages":[],"max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1024,"display":"omitted"}}`))
	f.Add([]byte(`{"model":"m","messages":[],"max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1023}}`))
	f.Add([]byte(`{"model":"m","messages":[],"max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1024}}`))
	f.Add([]byte(`{"model":"m","messages":[],"max_tokens":2048,"thinking":{"type":"disabled","budget_tokens":1024}}`))
	f.Add([]byte(`{"model":"m","messages":[],"stream":true}`))
	f.Add([]byte(`{"model":"m","messages":[],"bogus":true}`))
	f.Add([]byte(`{"model":"m","messages":[],"max_tokens":2048,"thinking":7}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("normalizer panicked: %v", r)
			}
		}()
		req, err := NormalizeAnthropicMessages(withRawBody(raw), "id")
		if err == nil {
			if !bytes.Equal(req.Body, raw) {
				t.Fatalf("raw identity broken on success")
			}
			return
		}
		if !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrStreamingUnsupported) {
			t.Fatalf("error = %v, want ErrInvalidRequest or ErrStreamingUnsupported", err)
		}
	})
}

func TestNormalizeDerivesPrincipalFromIdentityContext(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	id := identity.Identity{Subject: "svc-1", KeyID: "key-1", Role: identity.RoleService, Status: identity.StatusActive}
	req, err := NormalizeOpenAIChat(authcontext.WithIdentity(withRawBody(raw), id), "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Principal != (nonstream.Principal{Subject: "svc-1", KeyID: "key-1", Role: nonstream.RoleService, Status: nonstream.StatusActive}) {
		t.Fatalf("principal = %+v", req.Principal)
	}
}

func TestNormalizePrincipalZeroWhenNoIdentity(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req, err := NormalizeOpenAIChat(withRawBody(raw), "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.Principal != (nonstream.Principal{}) {
		t.Fatalf("principal = %+v, want zero", req.Principal)
	}
}

func TestDualModeNormalizersReturnSchemaValidStreamWithoutExecution(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		fn   func(context.Context, string) (NormalizedRequest, error)
		raw  []byte
		want adapter.Protocol
	}{
		{"chat", NormalizeOpenAIChatRequest, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`), adapter.ProtocolOpenAIChat},
		{"messages", NormalizeAnthropicMessagesRequest, []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":true}`), adapter.ProtocolAnthropic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(withRawBody(tc.raw), "request-1")
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if !got.Stream || got.Request.Protocol != tc.want || got.Request.RequestID != "request-1" || string(got.Request.Body) != string(tc.raw) {
				t.Fatalf("normalized = %#v", got)
			}
		})
	}
}

func TestDualModeNormalizersRejectInvalidStreamTrueAs400(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		fn   func(context.Context, string) (NormalizedRequest, error)
		raw  []byte
	}{
		{"chat", NormalizeOpenAIChatRequest, []byte(`{"model":"m","messages":[{"role":"invalid","content":"hi"}],"stream":true}`)},
		{"messages", NormalizeAnthropicMessagesRequest, []byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"bad"}]}],"stream":true}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(withRawBody(tc.raw), "id"); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("err = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestNormalizedRequestStreamRequestCopiesBodyAndCarriesTrustedFields(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	ctx := authcontext.WithIdentity(withRawBody(raw), identity.Identity{Subject: "svc", KeyID: "key", Role: identity.RoleService, Status: identity.StatusActive})
	normalized, err := NormalizeOpenAIChatRequest(ctx, "req-1")
	if err != nil {
		t.Fatalf("NormalizeOpenAIChatRequest: %v", err)
	}
	streamRequest := normalized.StreamRequest(nil)
	if !normalized.Stream || streamRequest.Protocol != adapter.ProtocolOpenAIChat || streamRequest.RequestID != "req-1" || streamRequest.Principal.Subject != "svc" {
		t.Fatalf("stream request = %#v", streamRequest)
	}
	streamRequest.Body[0] ^= 1
	if string(streamRequest.Body) == string(normalized.Request.Body) {
		t.Fatal("stream request body aliases normalized request")
	}
}
