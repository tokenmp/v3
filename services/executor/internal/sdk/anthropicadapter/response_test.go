package anthropicadapter

import (
	"strings"
	"testing"
)

const minimalMessageResponse = `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3}}`

func TestValidateMessageResponse_AcceptsContractShapes(t *testing.T) {
	for name, raw := range map[string]string{
		"minimal":                       minimalMessageResponse,
		"null stops":                    strings.Replace(minimalMessageResponse, `"stop_reason":"end_turn"`, `"stop_reason":null,"stop_sequence":null`, 1),
		"tool thinking and cache usage": `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"weather","input":{"city":"Paris"}},{"type":"thinking","thinking":"reasoning","signature":"sig_01"}],"model":"claude-test","stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":3,"cache_creation_input_tokens":2,"cache_read_input_tokens":1}}`,
		// Bug #54 regression: provider extra top-level fields and usage fields must not cause rejection.
		"minimax with base_resp and service_tier": `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":3,"service_tier":"standard"},"base_resp":{"status_code":0,"status_msg":""}}`,
		"extra top-level field":                   strings.TrimSuffix(minimalMessageResponse, "}") + `,"extra":true}`,
		"extra usage field":                       strings.Replace(minimalMessageResponse, `"output_tokens":3`, `"output_tokens":3,"service_tier":"auto"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateMessageResponse([]byte(raw)); err != nil {
				t.Fatalf("validateMessageResponse: %v", err)
			}
		})
	}
}

func TestValidateMessageResponse_RejectsInvalidShapes(t *testing.T) {
	cases := map[string]string{
		"missing id":                 `{"type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"m","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}`,
		"wrong type":                 strings.Replace(minimalMessageResponse, `"type":"message"`, `"type":"error"`, 1),
		"content not array":          strings.Replace(minimalMessageResponse, `"content":[{"type":"text","text":"hi"}]`, `"content":"not-array"`, 1),
		"usage input noninteger":     strings.Replace(minimalMessageResponse, `"input_tokens":5`, `"input_tokens":1.5`, 1),
		"wrong stop enum":            strings.Replace(minimalMessageResponse, `"end_turn"`, `"pause_turn"`, 1),
		"unknown block":              strings.Replace(minimalMessageResponse, `"text":"hi"`, `"text":"hi","extra":true`, 1),
		"tool input nonobject":       strings.Replace(minimalMessageResponse, `{"type":"text","text":"hi"}`, `{"type":"tool_use","id":"x","name":"n","input":[]}`, 1),
		"thinking missing signature": strings.Replace(minimalMessageResponse, `{"type":"text","text":"hi"}`, `{"type":"thinking","thinking":"x"}`, 1),
		"prototype":                  strings.Replace(minimalMessageResponse, `"text":"hi"`, `"text":"hi","__proto__":{}`, 1),
		"duplicate":                  strings.Replace(minimalMessageResponse, `"id":"msg_01"`, `"id":"msg_01","id":"msg_02"`, 1),
		"malformed":                  `{"id":`,
		"invalid utf8":               string([]byte{0xff}),
		"over byte limit":            strings.Repeat(" ", maxParamBodyBytes+1),
		"over depth limit":           strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1),
		"over node limit":            "[" + strings.Repeat("0,", maxJSONNodes) + "0]",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateMessageResponse([]byte(raw)); err == nil {
				t.Fatal("validateMessageResponse accepted invalid response")
			}
		})
	}
}

func FuzzValidateMessageResponse(f *testing.F) {
	f.Add([]byte(minimalMessageResponse))
	f.Add([]byte(`{"id":`))
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_ = validateMessageResponse(raw)
	})
}
