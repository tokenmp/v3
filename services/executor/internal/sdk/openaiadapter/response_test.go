package openaiadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func responseCall(base, key, body string) sdk.Call {
	return sdk.Call{
		Candidate: sdk.CandidateIdentity{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c", AdapterID: "a"},
		Target:    sdk.Target{BaseURL: base, UpstreamModel: "forced-response-model", Protocol: adapter.ProtocolOpenAIResponses},
		Request:   adapter.AppliedRequest{Body: json.RawMessage(body), InjectionPlan: adapter.InjectionPlan{Headers: map[string]string{}, Query: map[string]string{}}},
		Secret:    sdk.NewCredentialSecret([]byte(key)),
	}
}

func TestCompleteResponseCallLocalAuthority(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "environment-key")
	t.Setenv("OPENAI_BASE_URL", "https://environment.invalid")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Environment: leak")
	var seen atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/prefix/responses") {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Values("Authorization"); len(got) != 1 || got[0] != "Bearer call-key" {
			t.Errorf("authorization = %q", got)
		}
		if r.Header.Get("X-Environment") != "" {
			t.Error("environment header leaked")
		}
		w.Header().Set("x-request-id", "req_resp")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3},"model":"forced-response-model"}`))
	}))
	defer ts.Close()
	result, err := newTestClient(t, ts, nil).Complete(context.Background(), responseCall(ts.URL+"/prefix", "call-key", `{"model":"caller","input":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 || result.Status != 200 || result.RequestID != "req_resp" || len(result.RawJSON) == 0 {
		t.Fatalf("result = %#v, calls=%d", result, seen.Load())
	}
	if !result.Known || result.Usage.PromptTokens != 2 || result.Usage.CompletionTokens != 1 || result.Usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v, known=%v", result.Usage, result.Known)
	}
}

func TestCompleteResponseRejectsInvalidRequestBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer ts.Close()
	client := newTestClient(t, ts, nil)
	for _, body := range []string{
		`{"model":"m"}`,
		`{"model":"m","input":"hi","previous_response_id":"resp_1"}`,
		`{"model":"m","input":"hi","store":true}`,
		`{"model":"m","input":"hi","conversation":{"id":"c"}}`,
		`{"model":"m","input":123}`,
		`{"input":"hi"}`,
		`{"model":"m","input":"hi","tools":[{"type":"web_search"}]}`,
		`{"model":"m","input":"hi","tool_choice":"never"}`,
		`{"model":"m","input":"hi","extra":true}`,
	} {
		if _, err := client.Complete(context.Background(), responseCall(ts.URL, "key", body)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("body %s: %v", body, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d", calls.Load())
	}
}

func TestCompleteResponseRejectsInvalidResponse(t *testing.T) {
	for name, response := range map[string]string{
		"missing id":     `{"object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		"wrong object":   `{"id":"r","object":"chat.completion","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		"bad usage":      `{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":4}}`,
		"negative usage": `{"id":"r","object":"response","status":"completed","output":[],"usage":{"input_tokens":-1,"output_tokens":1,"total_tokens":0}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(response))
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), responseCall(ts.URL, "safe-key", `{"model":"m","input":"hello"}`))
			if !errors.Is(err, sdk.ErrProtocol) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCompleteResponseNoRetryOrRedirect(t *testing.T) {
	var calls atomic.Int32
	failure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer failure.Close()
	_, err := newTestClient(t, failure, nil).Complete(context.Background(), responseCall(failure.URL, "key", `{"model":"m","input":"x"}`))
	if !errors.Is(err, sdk.ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, failure.URL, http.StatusFound) }))
	defer redirect.Close()
	_, err = newTestClient(t, redirect, nil).Complete(context.Background(), responseCall(redirect.URL, "key", `{"model":"m","input":"x"}`))
	if !errors.Is(err, sdk.ErrUpstream) || calls.Load() != 1 {
		t.Fatalf("redirect err=%v calls=%d", err, calls.Load())
	}
}

func TestExtractOpenAIResponseUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		usage sdk.Usage
		known bool
	}{
		{"normal", `{"id":"r","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`, sdk.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}, true},
		{"zero", `{"id":"r","usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`, sdk.Usage{}, true},
		{"missing usage", `{"id":"r"}`, sdk.Usage{}, false},
		{"inconsistent", `{"id":"r","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":31}}`, sdk.Usage{}, false},
		{"over cap", `{"id":"r","usage":{"input_tokens":500000,"output_tokens":500001,"total_tokens":1000001}}`, sdk.Usage{}, false},
		{"at cap", `{"id":"r","usage":{"input_tokens":1000000,"output_tokens":0,"total_tokens":1000000}}`, sdk.Usage{PromptTokens: 1000000, CompletionTokens: 0, TotalTokens: 1000000}, true},
		{"negative", `{"id":"r","usage":{"input_tokens":-1,"output_tokens":1,"total_tokens":0}}`, sdk.Usage{}, false},
		{"empty raw", ``, sdk.Usage{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage, known := extractOpenAIResponseUsage(json.RawMessage(tc.raw))
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if usage != tc.usage {
				t.Fatalf("usage = %+v, want %+v", usage, tc.usage)
			}
		})
	}
}
