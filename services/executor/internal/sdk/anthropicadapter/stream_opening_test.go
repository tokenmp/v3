package anthropicadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func streamCall(baseURL, secret string) sdk.StreamCall {
	return sdk.StreamCall{
		Candidate: sdk.CandidateIdentity{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c", AdapterID: "a"},
		Target:    sdk.Target{BaseURL: baseURL, UpstreamModel: "stream-model", Protocol: adapter.ProtocolAnthropic},
		Request:   adapter.AppliedRequest{Body: json.RawMessage(`{"model":"caller-model","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`), Thinking: adapter.EffectiveThinking{EffectiveBudget: 1024}, InjectionPlan: adapter.InjectionPlan{Headers: map[string]string{"X-Plan": "yes"}, Query: map[string]string{"trace": "one"}}},
		Secret:    sdk.NewCredentialSecret([]byte(secret)),
	}
}

func TestOpenStream_TransportBoundaryAndOpeningMetadata(t *testing.T) {
	const secret = "call-local-key"
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "env-token")
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.invalid")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Evil: env")
	var requests atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/prefix/v1/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("Accept = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("version = %q", got)
		}
		if values := r.Header.Values("x-api-key"); len(values) != 1 || values[0] != secret {
			t.Errorf("x-api-key = %q", values)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Evil") != "" {
			t.Errorf("unscrubbed headers: %#v", r.Header)
		}
		if r.Header.Get("X-Plan") != "yes" || r.URL.RawQuery != "trace=one" {
			t.Errorf("plan injection lost: header=%q query=%q", r.Header.Get("X-Plan"), r.URL.RawQuery)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["model"] != "stream-model" || payload["stream"] != true {
			t.Errorf("authoritative payload = %#v", payload)
		}
		thinking, _ := payload["thinking"].(map[string]any)
		if thinking["budget_tokens"] != float64(1024) {
			t.Errorf("thinking authority lost: %#v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("request-id", "req_stream_123")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, ": ping\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer ts.Close()

	opening, err := newTestClient(t, ts, nil).openStream(context.Background(), streamCall(ts.URL+"/prefix", secret))
	if err != nil {
		t.Fatal(err)
	}
	if opening.Status != http.StatusCreated || opening.RequestID != "req_stream_123" {
		t.Fatalf("opening = %#v", opening)
	}
	if got := fmt.Sprintf("%+v", opening); strings.Contains(got, secret) || strings.Contains(got, "req_stream") || strings.Contains(got, "prefix") {
		t.Fatalf("opening formatting leaked: %q", got)
	}
	opening.cleanup()
	opening.cleanup()
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestOpenStream_RedirectRetryAndHTTPClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{"redirect", http.StatusFound, sdk.ErrUpstream},
		{"rate", http.StatusTooManyRequests, sdk.ErrRateLimited},
		{"overloaded", 529, sdk.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				if tc.status == http.StatusFound {
					http.Redirect(w, r, "https://elsewhere.invalid/", tc.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("request-id", "req_safe")
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"do not expose"}}`)
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).openStream(context.Background(), streamCall(ts.URL, "key"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if count.Load() != 1 {
				t.Fatalf("calls = %d, want exactly one", count.Load())
			}
			if strings.Contains(fmt.Sprint(err), "do not expose") {
				t.Fatal("provider error leaked")
			}
		})
	}
}

func TestOpenStream_CleanupCancelsAndSDKDoesNotReadAhead(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: ping\ndata: ignored\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer ts.Close()
	opening, err := newTestClient(t, ts, nil).openStream(context.Background(), streamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not open")
	}
	// Public SDK behavior proof: NewStreaming returns after headers; it does not
	// consume the ping/body until a future parser invokes stream.Next.
	if err := opening.Stream.Err(); err != nil {
		t.Fatalf("opening unexpectedly consumed stream: %v", err)
	}
	opening.cleanup()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not cancel upstream read")
	}
}

func TestOpenStream_OfficialSDKPingAndErrorBehaviorProof(t *testing.T) {
	// This records v1.58.0 public behavior for the future parser: NewStreaming
	// itself reads neither ping nor error. Its first Next skips ping, consumes
	// the error event, and reports the SDK error through Err. This adapter does
	// not inspect that error or implement an event source in this phase.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: ping\ndata: ignored\n\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"provider text\"}}\n\n")
	}))
	defer ts.Close()
	opening, err := newTestClient(t, ts, nil).openStream(context.Background(), streamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer opening.cleanup()
	if opening.Stream.Next() {
		t.Fatal("ping/error stream unexpectedly yielded a message")
	}
	if opening.Stream.Err() == nil {
		t.Fatal("SDK did not expose in-band error after Next")
	}
}

func TestDecodeMessageParamsMode_ForceStreamAndPreserveNonStreamValidation(t *testing.T) {
	body := []byte(`{"model":"caller","max_tokens":1024,"messages":[{"role":"user","content":"x"}],"stream":true}`)
	if _, err := decodeMessageParams(body, adapter.EffectiveThinking{}, "authoritative"); !errors.Is(err, errInvalidMessageParams) {
		t.Fatalf("nonstream accepted stream=true: %v", err)
	}
	params, err := decodeMessageParamsMode(body, adapter.EffectiveThinking{}, "authoritative", true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"model":"authoritative"`) {
		t.Fatalf("model not authoritative: %s", raw)
	}
}
