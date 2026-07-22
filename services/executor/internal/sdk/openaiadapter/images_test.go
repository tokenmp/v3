package openaiadapter

import (
	"bytes"
	"context"
	"encoding/base64"
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

func imageCall(base, key, body string) sdk.Call {
	return sdk.Call{Candidate: sdk.CandidateIdentity{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c", AdapterID: "a"}, Target: sdk.Target{BaseURL: base, UpstreamModel: "forced-image-model", Protocol: adapter.ProtocolOpenAIImages}, Request: adapter.AppliedRequest{Body: json.RawMessage(body), InjectionPlan: adapter.InjectionPlan{Headers: map[string]string{}, Query: map[string]string{}}}, Secret: sdk.NewCredentialSecret([]byte(key))}
}

func TestCompleteImagesGenerateCallLocalAuthority(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "environment-key")
	t.Setenv("OPENAI_BASE_URL", "https://environment.invalid")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Environment: leak")
	var seen atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if r.URL.Path != "/prefix/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Values("Authorization"); len(got) != 1 || got[0] != "Bearer call-key" {
			t.Errorf("authorization = %q", got)
		}
		if r.Header.Get("X-Environment") != "" {
			t.Error("environment header leaked")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "forced-image-model" || body["prompt"] != "draw" || body["response_format"] != "b64_json" {
			t.Errorf("body = %#v", body)
		}
		w.Header().Set("x-request-id", "req_images")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aGVsbG8="}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`))
	}))
	defer ts.Close()
	if err := validateImageResponse(context.Background(), []byte(`{"created":1,"data":[{"b64_json":"aGVsbG8="}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`), "b64_json"); err != nil {
		t.Fatalf("fixture validation: %v", err)
	}
	result, err := newTestClient(t, ts, nil).Complete(context.Background(), imageCall(ts.URL+"/prefix", "call-key", `{"model":"caller","prompt":"draw","response_format":"b64_json"}`))
	if err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 1 || result.Status != 200 || result.RequestID != "req_images" || len(result.RawJSON) == 0 {
		t.Fatalf("result = %#v, calls=%d", result, seen.Load())
	}
}

func TestCompleteImagesRejectsInvalidRequestBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer ts.Close()
	client := newTestClient(t, ts, nil)
	for _, body := range []string{
		`{"model":"m","prompt":""}`,
		`{"model":"m","prompt":"x","n":11}`,
		`{"model":"m","prompt":"x","size":"auto"}`,
		`{"model":"m","prompt":"x","user":"bad\nuser"}`,
		`{"model":"m","prompt":"x","extra":true}`,
	} {
		if _, err := client.Complete(context.Background(), imageCall(ts.URL, "key", body)); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("body %s: %v", body, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("HTTP calls = %d", calls.Load())
	}
}

func TestCompleteImagesDefaultResponseFormatIsURLOnWireAndResponse(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body["response_format"]; got != "url" {
			t.Fatalf("response_format = %#v, want url", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://images.example/result?q=ok"}]}`))
	}))
	defer ts.Close()
	result, err := newTestClient(t, ts, nil).Complete(context.Background(), imageCall(ts.URL, "key", `{"model":"m","prompt":"x"}`))
	if err != nil || len(result.RawJSON) == 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCompleteImagesDefaultResponseFormatRejectsBase64(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aA=="}]}`))
	}))
	defer ts.Close()
	_, err := newTestClient(t, ts, nil).Complete(context.Background(), imageCall(ts.URL, "safe-key", `{"model":"m","prompt":"secret prompt"}`))
	if !errors.Is(err, sdk.ErrProtocol) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "safe-key") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompleteImagesInjectionAndURLResponse(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://images.example/result?q=ok"}]}`))
	}))
	defer ts.Close()
	call := imageCall(ts.URL, "key", `{"model":"m","prompt":"x"}`)
	call.Request.InjectionPlan.Headers = map[string]string{"Authorization": "Bearer injected"}
	if _, err := newTestClient(t, ts, nil).Complete(context.Background(), call); !errors.Is(err, ErrInvalidInjection) {
		t.Fatalf("injection error = %v", err)
	}
	call.Request.InjectionPlan.Headers = map[string]string{}
	result, err := newTestClient(t, ts, nil).Complete(context.Background(), call)
	if err != nil || len(result.RawJSON) == 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCompleteImagesRejectsUnsafeResponse(t *testing.T) {
	for name, response := range map[string]string{
		"mixed formats":   `{"created":1,"data":[{"url":"https://x.example/a"},{"b64_json":"aGVsbG8="}]}`,
		"format mismatch": `{"created":1,"data":[{"url":"https://x.example/a"}]}`,
		"unsafe url":      `{"created":1,"data":[{"url":"http://x.example/a"}]}`,
		"invalid base64":  `{"created":1,"data":[{"b64_json":"%"}]}`,
		"control revised": `{"created":1,"data":[{"b64_json":"aA==","revised_prompt":"a\nb"}]}`,
		"bad usage":       `{"created":1,"data":[{"b64_json":"aA=="}],"usage":{"total_tokens":-1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(response)) }))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), imageCall(ts.URL, "safe-key", `{"model":"m","prompt":"secret prompt","response_format":"b64_json"}`))
			if !errors.Is(err, sdk.ErrProtocol) {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "safe-key") || strings.Contains(err.Error(), "https") {
				t.Fatalf("error leaked input: %v", err)
			}
		})
	}
}

func TestCompleteImagesNoRetryOrRedirect(t *testing.T) {
	var calls atomic.Int32
	failure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer failure.Close()
	_, err := newTestClient(t, failure, nil).Complete(context.Background(), imageCall(failure.URL, "key", `{"model":"m","prompt":"x"}`))
	if !errors.Is(err, sdk.ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, failure.URL, http.StatusFound) }))
	defer redirect.Close()
	_, err = newTestClient(t, redirect, nil).Complete(context.Background(), imageCall(redirect.URL, "key", `{"model":"m","prompt":"x"}`))
	if !errors.Is(err, sdk.ErrUpstream) || calls.Load() != 1 {
		t.Fatalf("redirect err=%v calls=%d", err, calls.Load())
	}
}

func TestCompleteImagesContentLengthCap(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "16777217")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aA=="}]}`))
	}))
	defer ts.Close()
	_, err := newTestClient(t, ts, nil).Complete(context.Background(), imageCall(ts.URL, "key", `{"model":"m","prompt":"x"}`))
	if !errors.Is(err, sdk.ErrProtocol) {
		t.Fatalf("error = %v", err)
	}
}

func TestImageBase64DecodedCap(t *testing.T) {
	// This is intentionally encoded input: validation must stream decoded bytes
	// rather than allocating an attacker-controlled decoded image buffer.
	tooLarge := strings.Repeat("A", 4*((maxDecodedImageDataBytes/3)+1))
	if _, err := decodedBase64Bytes(tooLarge, maxDecodedImageDataBytes); !errors.Is(err, errImageResponseTooLarge) {
		t.Fatalf("decoded cap error = %v", err)
	}
}

func TestValidateImageResponseExactCapsAndExtensions(t *testing.T) {
	minimal := []byte(`{"created":1,"data":[{"url":"https://x.example/a"}]}`)
	atWireCap := append(bytes.Repeat([]byte(" "), maxImageWireResponseBytes-len(minimal)), minimal...)
	if err := validateImageResponse(context.Background(), atWireCap, "url"); err != nil {
		t.Fatalf("wire exact cap: %v", err)
	}
	if err := validateImageResponse(context.Background(), append([]byte(" "), atWireCap...), "url"); !errors.Is(err, errImageResponseTooLarge) {
		t.Fatalf("wire cap + 1: %v", err)
	}

	atImageCap := base64.StdEncoding.EncodeToString(make([]byte, maxDecodedImageDataBytes))
	if err := validateImageResponse(context.Background(), []byte(`{"created":1,"data":[{"b64_json":"`+atImageCap+`"}]}`), "b64_json"); err != nil {
		t.Fatalf("per-image exact cap: %v", err)
	}
	if !withinDecodedImageAggregate(maxDecodedImageAggregateBytes, 0) ||
		withinDecodedImageAggregate(maxDecodedImageAggregateBytes, 1) {
		t.Fatal("aggregate cap boundary was not enforced")
	}
	atExtensionCap := strings.Repeat("x", maxImageExtensionValueBytes-2)
	if err := validateImageResponse(context.Background(), []byte(`{"created":1,"data":[{"url":"https://x.example/a"}],"provider_extension":"`+atExtensionCap+`"}`), "url"); err != nil {
		t.Fatalf("extension exact cap: %v", err)
	}
	tooLargeExtension := strings.Repeat("x", maxImageExtensionValueBytes-1)
	for name, response := range map[string]string{
		"root":  `{"created":1,"data":[{"url":"https://x.example/a"}],"provider_extension":"` + tooLargeExtension + `"}`,
		"usage": `{"created":1,"data":[{"url":"https://x.example/a"}],"usage":{"provider_extension":"` + tooLargeExtension + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateImageResponse(context.Background(), []byte(response), "url"); !errors.Is(err, errImageResponseTooLarge) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCappedReadCloser(t *testing.T) {
	r := &cappedReadCloser{ReadCloser: ioNopCloser{Reader: bytes.NewReader([]byte("abcd"))}, remaining: 3}
	buf := make([]byte, 8)
	if n, err := r.Read(buf); n != 3 || err != nil {
		t.Fatalf("first read n=%d err=%v", n, err)
	}
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, errImageResponseTooLarge) {
		t.Fatalf("overflow read n=%d err=%v", n, err)
	}
}

type ioNopCloser struct{ *bytes.Reader }

func (ioNopCloser) Close() error { return nil }
