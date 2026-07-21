package anthropicadapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

func TestDecodeMessageParams_AcceptsContractAndRebuildsAuthoritativeFields(t *testing.T) {
	body := []byte(`{
		"model":"caller-selector","max_tokens":4096,"stream":false,"temperature":0,"top_p":1,"top_k":1,
		"system":[{"type":"text","text":"rules","cache_control":{"type":"ephemeral"}}],
		"thinking":{"type":"enabled","budget_tokens":1024,"display":"omitted"},
		"stop_sequences":["END"],"metadata":{"user_id":"user-1"},
		"tools":[{"name":"weather","description":"lookup","input_schema":{"type":"object","properties":{"city":{"type":"string"}}},"cache_control":{"type":"ephemeral"}}],
		"tool_choice":{"type":"tool","name":"weather","disable_parallel_tool_use":true},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}},{"type":"tool_result","tool_use_id":"tool-1","content":[{"type":"text","text":"result"}]}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"weather","input":{"city":"Paris"}},{"type":"thinking","thinking":"reasoning","signature":"signature"}]}
		]
	}`)
	params, err := decodeMessageParams(body, adapter.EffectiveThinking{EffectiveBudget: 1024}, "claude-target")
	if err != nil {
		t.Fatalf("decodeMessageParams: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode rebuilt params: %v", err)
	}
	if got["model"] != "claude-target" {
		t.Fatalf("model = %#v", got["model"])
	}
	thinking, ok := got["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(1024) || thinking["display"] != "omitted" {
		t.Fatalf("thinking = %#v", got["thinking"])
	}
	if _, exists := got["stream"]; exists {
		t.Fatalf("non-stream SDK params must omit stream, got %#v", got["stream"])
	}
}

func TestDecodeMessageParams_RejectsInvalidRequests(t *testing.T) {
	deep := `{"model":"m","max_tokens":1,"messages":` + strings.Repeat(`[`, maxJSONDepth) + `"x"` + strings.Repeat(`]`, maxJSONDepth) + `}`
	valid := `"model":"m","max_tokens":2048,"messages":[{"role":"user","content":"x"}]`
	cases := map[string]string{
		"missing required":                 `{"model":"m","messages":[]}`,
		"empty messages":                   `{"model":"m","max_tokens":1,"messages":[]}`,
		"unknown root":                     `{` + valid + `,"extra":true}`,
		"duplicate root":                   `{"model":"m","model":"m2","max_tokens":1,"messages":[{"role":"user","content":"x"}]}`,
		"prototype key":                    `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":{"__proto__":"x"}}]}`,
		"stream true":                      `{` + valid + `,"stream":true}`,
		"bad temperature":                  `{` + valid + `,"temperature":1.1}`,
		"bad top p":                        `{` + valid + `,"top_p":-0.1}`,
		"bad top k":                        `{` + valid + `,"top_k":0}`,
		"bad stop":                         `{` + valid + `,"stop_sequences":[1]}`,
		"bad system":                       `{` + valid + `,"system":[{"type":"text","text":"x","unknown":true}]}`,
		"disabled thinking budget":         `{` + valid + `,"thinking":{"type":"disabled","budget_tokens":1024}}`,
		"thinking minimum":                 `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1023},"messages":[{"role":"user","content":"x"}]}`,
		"thinking must fit max":            `{"model":"m","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"x"}]}`,
		"bad message role":                 `{"model":"m","max_tokens":1,"messages":[{"role":"system","content":"x"}]}`,
		"unknown message":                  `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"x","name":"n"}]}`,
		"unknown content block":            `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"x","unknown":true}]}]}`,
		"image needs strict base64 source": `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","media_type":"image/png","data":"x"}}]}]}`,
		"image requires valid base64":      `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"not base64"}}]}]}`,
		"tool use needs input":             `{"model":"m","max_tokens":1,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"id","name":"name"}]}]}`,
		"tool result needs content":        `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"id"}]}]}`,
		"thinking block needs signature":   `{"model":"m","max_tokens":1,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"x"}]}]}`,
		"tool needs input schema":          `{` + valid + `,"tools":[{"name":"tool"}]}`,
		"tool choice tool needs name":      `{` + valid + `,"tool_choice":{"type":"tool"}}`,
		"tool choice unknown":              `{` + valid + `,"tool_choice":{"type":"none"}}`,
		"metadata unknown":                 `{` + valid + `,"metadata":{"unknown":"x"}}`,
		"depth":                            deep,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeMessageParams([]byte(body), adapter.EffectiveThinking{}, "target"); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}

	t.Run("node limit", func(t *testing.T) {
		body := `{"model":"m","max_tokens":1,"messages":[` + strings.Repeat(`null,`, maxJSONNodes) + `null]}`
		if _, err := decodeMessageParams([]byte(body), adapter.EffectiveThinking{}, "target"); err == nil {
			t.Fatal("over-limit JSON tree accepted")
		}
	})
}

func TestDecodeMessageParams_ThinkingCannotDrift(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		effective adapter.EffectiveThinking
	}{
		{"disabled rejects body enabled", `{"model":"m","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"x"}]}`, adapter.EffectiveThinking{}},
		{"enabled rejects body disabled", `{"model":"m","max_tokens":2048,"thinking":{"type":"disabled"},"messages":[{"role":"user","content":"x"}]}`, adapter.EffectiveThinking{EffectiveBudget: 1024}},
		{"enabled rejects wrong budget", `{"model":"m","max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"x"}]}`, adapter.EffectiveThinking{EffectiveBudget: 2048}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeMessageParams([]byte(tc.body), tc.effective, "target"); err == nil {
				t.Fatal("thinking drift accepted")
			}
		})
	}
}

func TestDecodeMessageParams_RejectsInvalidUTF8AndOversize(t *testing.T) {
	if _, err := decodeMessageParams([]byte{'{', 0xff, '}'}, adapter.EffectiveThinking{}, "target"); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	if _, err := decodeMessageParams(make([]byte, maxParamBodyBytes+1), adapter.EffectiveThinking{}, "target"); err == nil {
		t.Fatal("oversize body accepted")
	}
}

func FuzzDecodeMessageParams(f *testing.F) {
	f.Add([]byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`))
	f.Add([]byte(`{"model":"m","max_tokens":1,"messages":[{"role":"user","content":{"constructor":"x"}}]}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = decodeMessageParams(body, adapter.EffectiveThinking{}, "target")
	})
}
