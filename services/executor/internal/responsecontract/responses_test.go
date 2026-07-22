package responsecontract

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func mustParseStrict(t *testing.T, body []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := dec.Token(); err == nil {
		t.Fatal("trailing content")
	}
	root, ok := v.(map[string]any)
	if !ok {
		t.Fatal("not an object")
	}
	return root
}

func TestValidateRequestAcceptsMinimalAndComplex(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, body string }{
		{"string input", `{"model":"gpt-4o","input":"hello"}`},
		{"array input message", `{"model":"gpt-4o","input":[{"type":"message","role":"user","content":"hi"}]}`},
		{"array input content parts", `{"model":"gpt-4o","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"https://example.com/a.png"}]}]}`},
		{"full request", `{"model":"gpt-4o","input":"hi","instructions":"sys","max_output_tokens":1024,"metadata":{"user_id":"u1"},"reasoning":{"effort":"high","summary":"auto"},"stream":false,"temperature":0.7,"text":{"format":{"type":"json_object"}},"tool_choice":"auto","tools":[{"type":"function","name":"get_weather","description":"weather","parameters":{"type":"object"},"strict":true}],"top_p":0.9}`},
		{"output_text part", `{"model":"gpt-4o","input":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"prev"}]}]}`},
		{"role developer", `{"model":"gpt-4o","input":[{"type":"message","role":"developer","content":"hi"}]}`},
		{"reasoning none disables", `{"model":"gpt-4o","input":"hi","reasoning":{"effort":"none"}}`},
		{"metadata nil", `{"model":"gpt-4o","input":"hi","metadata":null}`},
		{"tools nil", `{"model":"gpt-4o","input":"hi","tools":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := mustParseStrict(t, []byte(tc.body))
			if !ValidateRequest(root) {
				t.Fatalf("expected valid: %s", tc.body)
			}
		})
	}
}

func TestValidateRequestRejectsStatefulFields(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"model":"gpt-4o","input":"hi","previous_response_id":"resp_abc"}`,
		`{"model":"gpt-4o","input":"hi","conversation":{"id":"c"}}`,
		`{"model":"gpt-4o","input":"hi","store":true}`,
		`{"model":"gpt-4o","input":"hi","background":true}`,
		`{"model":"gpt-4o","input":"hi","include":["reasoning.encrypted_content"]}`,
		`{"model":"gpt-4o","input":"hi","moderation":{}}`,
		`{"model":"gpt-4o","input":"hi","prompt":{"id":"p"}}`,
		`{"model":"gpt-4o","input":"hi","truncation":"auto"}`,
		`{"model":"gpt-4o","input":"hi","service_tier":"default"}`,
	}
	for _, body := range cases {
		root := mustParseStrict(t, []byte(body))
		if ValidateRequest(root) {
			t.Fatalf("expected stateful field rejected: %s", body)
		}
	}
}

func TestValidateRequestRejectsInvalidNested(t *testing.T) {
	t.Parallel()
	cases := []string{
		// missing model
		`{"input":"hi"}`,
		// missing input
		`{"model":"gpt-4o"}`,
		// input not string/array
		`{"model":"gpt-4o","input":123}`,
		// empty input array
		`{"model":"gpt-4o","input":[]}`,
		// input item wrong type
		`{"model":"gpt-4o","input":[{"type":"reasoning","content":"hi"}]}`,
		// input item unknown field
		`{"model":"gpt-4o","input":[{"type":"message","role":"user","content":"hi","extra":1}]}`,
		// bad role
		`{"model":"gpt-4o","input":[{"type":"message","role":"attacker","content":"hi"}]}`,
		// content part wrong type
		`{"model":"gpt-4o","input":[{"type":"message","role":"user","content":[{"type":"file","file":"x"}]}]}`,
		// input_text missing text
		`{"model":"gpt-4o","input":[{"type":"message","role":"user","content":[{"type":"input_text"}]}]}`,
		// input_image wrong url type
		`{"model":"gpt-4o","input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":123}]}]}`,
		// tools not function
		`{"model":"gpt-4o","input":"hi","tools":[{"type":"web_search"}]}`,
		// tool missing name
		`{"model":"gpt-4o","input":"hi","tools":[{"type":"function","parameters":{}}]}`,
		// tool missing parameters
		`{"model":"gpt-4o","input":"hi","tools":[{"type":"function","name":"f"}]}`,
		// tool_choice bad enum
		`{"model":"gpt-4o","input":"hi","tool_choice":"never"}`,
		// metadata unknown field
		`{"model":"gpt-4o","input":"hi","metadata":{"team":"x"}}`,
		// reasoning bad effort
		`{"model":"gpt-4o","input":"hi","reasoning":{"effort":"ultra"}}`,
		// temperature out of range
		`{"model":"gpt-4o","input":"hi","temperature":5}`,
		// top_p out of range
		`{"model":"gpt-4o","input":"hi","top_p":2}`,
		// max_output_tokens zero
		`{"model":"gpt-4o","input":"hi","max_output_tokens":0}`,
		// stream not bool
		`{"model":"gpt-4o","input":"hi","stream":"true"}`,
		// text unknown field
		`{"model":"gpt-4o","input":"hi","text":{"format":{"type":"json_object"},"extra":1}}`,
		// unknown root field
		`{"model":"gpt-4o","input":"hi","unknown":1}`,
	}
	for _, body := range cases {
		root := mustParseStrict(t, []byte(body))
		if ValidateRequest(root) {
			t.Fatalf("expected invalid: %s", body)
		}
	}
}

func TestValidateResponseAcceptsValid(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"model":"gpt-4o"}`,
		`{"id":"resp_1","object":"response","status":"in_progress","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		`{"id":"resp_1","object":"response","status":"failed","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`,
		`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"function_call","id":"fc_1","name":"f","arguments":"{}","call_id":"c1"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
		`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"s"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`,
	}
	for _, body := range cases {
		root := mustParseStrict(t, []byte(body))
		if !ValidateResponse(root) {
			t.Fatalf("expected valid response: %s", body)
		}
	}
}

func TestValidateResponseRejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []string{
		// missing id
		`{"object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		// wrong object
		`{"id":"r","object":"chat.completion","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		// bad status
		`{"id":"r","object":"response","status":"weird","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		// output not array
		`{"id":"r","object":"response","status":"completed","output":"x","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		// output item bad type
		`{"id":"r","object":"response","status":"completed","output":[{"type":"unknown"}],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		// usage inconsistent
		`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":4}}`,
		// usage negative
		`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":-1,"output_tokens":1,"total_tokens":0}}`,
		// usage over cap
		`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":1000001,"output_tokens":0,"total_tokens":1000001}}`,
		// usage missing field
		`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0}}`,
		// usage not object
		`{"id":"r","object":"response","status":"completed","output":[],"usage":[]}`,
		// extension value too large
		`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0},"provider_ext":"` + strings.Repeat("x", 65537) + `"}`,
	}
	for _, body := range cases {
		root := mustParseStrict(t, []byte(body))
		if ValidateResponse(root) {
			t.Fatalf("expected invalid response: %s", body)
		}
	}
}

func TestValidateResponseUsageConsistencyAtCap(t *testing.T) {
	t.Parallel()
	body := `{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":1000000,"output_tokens":0,"total_tokens":1000000}}`
	root := mustParseStrict(t, []byte(body))
	if !ValidateResponse(root) {
		t.Fatal("expected valid at exact cap")
	}
}

func FuzzValidateRequest(f *testing.F) {
	f.Add([]byte(`{"model":"gpt-4o","input":"hi"}`))
	f.Add([]byte(`{"model":"gpt-4o","input":[{"type":"message","role":"user","content":"hi"}],"stream":true}`))
	f.Add([]byte(`{"model":"gpt-4o","input":"hi","previous_response_id":"resp_1"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return
		}
		root, ok := v.(map[string]any)
		if !ok {
			return
		}
		_ = ValidateRequest(root)
	})
}

func FuzzValidateResponse(f *testing.F) {
	f.Add([]byte(`{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
	f.Add([]byte(`{"id":"r","object":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return
		}
		root, ok := v.(map[string]any)
		if !ok {
			return
		}
		_ = ValidateResponse(root)
	})
}

// keep context import referenced for parity with other contract packages.
var _ = context.Background
