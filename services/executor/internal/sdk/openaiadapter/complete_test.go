package openaiadapter

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

func tlsClient(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = ts.Client().Transport.(*http.Transport).TLSClientConfig
	return &http.Client{Transport: tr}
}

func newTestClient(t *testing.T, ts *httptest.Server, observer sdk.AttemptObserver) *Client {
	t.Helper()
	c, err := NewClient(WithHTTPClient(tlsClient(t, ts)), WithAttemptObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func testCall(baseURL, secret string) sdk.Call {
	return sdk.Call{
		Candidate: sdk.CandidateIdentity{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c", AdapterID: "a"},
		Target:    sdk.Target{BaseURL: baseURL, UpstreamModel: "upstream-model", Protocol: adapter.ProtocolOpenAIChat},
		Request:   adapter.AppliedRequest{Body: json.RawMessage(`{"model":"caller","messages":[{"role":"user","content":"hi"}]}`), InjectionPlan: adapter.InjectionPlan{Headers: map[string]string{}, Query: map[string]string{}}},
		Secret:    sdk.NewCredentialSecret([]byte(secret)),
	}
}

func success(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-request-id", "req_123")
	_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"hi"}}],"created":1,"model":"upstream-model","object":"chat.completion"}`))
}

func TestNewClientIsTargetAgnostic(t *testing.T) {
	if _, err := NewClient(); err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := NewClient(nil); err == nil {
		t.Fatal("nil option accepted")
	}
}

func TestComplete_EnvironmentCannotOverrideCallLocalTargetOrHeaders(t *testing.T) {
	// openai-go reads these environment variables during NewClient. Complete
	// must still use only its call-local target and credential, and the final
	// transport boundary must discard environment-derived organization, project,
	// and custom headers.
	const (
		envAPIKey  = "environment-api-key"
		envAdmin   = "environment-admin-key"
		envOrg     = "environment-org-id"
		envProject = "environment-project-id"
		envHeader  = "environment-custom-header"
		callKey    = "call-local-api-key"
	)
	t.Setenv("OPENAI_API_KEY", envAPIKey)
	t.Setenv("OPENAI_ADMIN_KEY", envAdmin)
	t.Setenv("OPENAI_ORG_ID", envOrg)
	t.Setenv("OPENAI_PROJECT_ID", envProject)
	t.Setenv("OPENAI_BASE_URL", "https://env.example/ignored")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Environment-Custom: "+envHeader)

	var gotPath string
	var gotHeader http.Header
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Clone()
		success(w)
	}))
	defer ts.Close()

	completion, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL+"/provider/v1", callKey))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/provider/v1/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if values := gotHeader.Values("Authorization"); len(values) != 1 || values[0] != "Bearer "+callKey {
		t.Fatalf("Authorization values = %q, want exactly call-local bearer", values)
	}
	for name := range map[string]string{
		"OpenAI-Organization":  envOrg,
		"OpenAI-Project":       envProject,
		"X-Environment-Custom": envHeader,
	} {
		if got := gotHeader.Get(name); got != "" {
			t.Errorf("environment header %s = %q, want absent", name, got)
		}
	}
	if completion.Status != http.StatusOK || completion.RequestID != "req_123" {
		t.Fatalf("metadata = %#v", completion)
	}
	if len(completion.RawJSON) == 0 {
		t.Fatal("RawJSON empty")
	}
}

func TestComplete_InvalidChatParamsMakeNoHTTP(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { n.Add(1) }))
	defer ts.Close()
	client := newTestClient(t, ts, nil)
	for name, body := range map[string]string{
		"nested duplicate": `{"model":"m","tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","type":"string"}}}],"messages":[{"role":"user","content":"x"}]}`,
		"nested unknown":   `{"model":"m","tool_choice":{"type":"function","function":{"name":"f","extra":true}},"messages":[{"role":"user","content":"x"}]}`,
		"nested bad type":  `{"model":"m","tools":[{"type":"function","function":{"name":"f","parameters":[]}}],"messages":[{"role":"user","content":"x"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			call := testCall(ts.URL, "key")
			call.Request.Body = json.RawMessage(body)
			if _, err := client.Complete(context.Background(), call); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Complete error = %v", err)
			}
		})
	}
	if n.Load() != 0 {
		t.Fatalf("invalid params made %d HTTP requests", n.Load())
	}
}

func TestComplete_TargetValidationNoHTTP(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { n.Add(1) }))
	defer ts.Close()
	c := newTestClient(t, ts, nil)
	for _, base := range []string{"", "http://example.test", "https://user:pass@example.test", "https://example.test?a=b", "https://example.test#f"} {
		_, err := c.Complete(context.Background(), testCall(base, "key"))
		if !errors.Is(err, ErrInvalidBaseURL) {
			t.Fatalf("base %q: %v", base, err)
		}
	}
	if n.Load() != 0 {
		t.Fatal("invalid target made request")
	}
}

func TestComplete_DifferentSecretsAndExactlyOneAuthorization(t *testing.T) {
	var auths []string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("Authorization"); len(values) != 1 {
			t.Fatalf("Authorization values = %v", values)
		}
		auths = append(auths, r.Header.Get("Authorization"))
		success(w)
	}))
	defer ts.Close()
	c := newTestClient(t, ts, nil)
	for _, secret := range []string{"first", "second"} {
		if _, err := c.Complete(context.Background(), testCall(ts.URL, secret)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(auths, ",") != "Bearer first,Bearer second" {
		t.Fatalf("auths = %v", auths)
	}
}

func TestComplete_NoRedirectAndRetryZero(t *testing.T) {
	var redirects, failures atomic.Int32
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
	}))
	defer redirect.Close()
	if _, err := newTestClient(t, redirect, nil).Complete(context.Background(), testCall(redirect.URL, "key")); !errors.Is(err, sdk.ErrUpstream) {
		t.Fatalf("redirect error = %v", err)
	}
	if redirects.Load() != 1 {
		t.Fatalf("redirect requests = %d", redirects.Load())
	}

	failure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		failures.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"internal","type":"server_error"}}`))
	}))
	defer failure.Close()
	if _, err := newTestClient(t, failure, nil).Complete(context.Background(), testCall(failure.URL, "key")); !errors.Is(err, sdk.ErrUnavailable) {
		t.Fatalf("failure error = %v", err)
	}
	if failures.Load() != 1 {
		t.Fatalf("retry requests = %d", failures.Load())
	}
}

func TestComplete_EnvironmentCustomHeadersCannotWin(t *testing.T) {
	t.Setenv("OPENAI_CUSTOM_HEADERS", "Authorization: Bearer evil\nX-Evil: yes")
	var auth, evil string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, evil = r.Header.Get("Authorization"), r.Header.Get("X-Evil")
		success(w)
	}))
	defer ts.Close()
	if _, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "good")); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer good" || evil != "" {
		t.Fatalf("auth=%q evil=%q", auth, evil)
	}
}

func TestComplete_ClassifiesHTTPFailuresAndDoesNotLeakRemoteContent(t *testing.T) {
	secret := "remote secret message"
	for _, tc := range []struct {
		name   string
		status int
		kind   error
	}{
		{"unauthorized", http.StatusUnauthorized, sdk.ErrUnauthorized},
		{"forbidden", http.StatusForbidden, sdk.ErrForbidden},
		{"not found", http.StatusNotFound, sdk.ErrNotFound},
		{"rate limited", http.StatusTooManyRequests, sdk.ErrRateLimited},
		// Non-special client statuses retain their native upstream category.
		{"bad request", http.StatusBadRequest, sdk.ErrUpstream},
		// Every 5xx status maps to unavailable; 502 is a representative native status.
		{"server error", http.StatusBadGateway, sdk.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-request-id", "req.safe:123")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":"remote_code","type":"remote_type","message":"` + secret + `"}}`))
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "key"))
			if !errors.Is(err, tc.kind) || strings.Contains(err.Error(), secret) {
				t.Fatalf("Complete error = %v, want %v without remote message", err, tc.kind)
			}
			ce, ok := err.(*sdk.ClassifiedError)
			if !ok || ce.Status() != tc.status || ce.RequestID() != "req.safe:123" || ce.Code() != "remote_code" || ce.Type() != "remote_type" {
				t.Fatalf("classified error = %#v", ce)
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts = %d, want 1", attempts.Load())
			}
		})
	}
}

func TestComplete_ClassifiesMalformedSuccessfulResponseAsProtocol(t *testing.T) {
	for _, body := range []string{"not json", `{"choices":`} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("x-request-id", "req.protocol")
				// A 201 confirms malformed handling applies to the whole 2xx range,
				// not merely the usual 200 success status.
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(body))
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "key"))
			if !errors.Is(err, sdk.ErrProtocol) || strings.Contains(err.Error(), body) {
				t.Fatalf("Complete error = %v, want protocol error without body", err)
			}
			ce := err.(*sdk.ClassifiedError)
			if ce.Status() != http.StatusCreated || ce.RequestID() != "req.protocol" {
				t.Fatalf("classified error = %#v", ce)
			}
		})
	}
}

func TestComplete_DeadlineClassifiesTimeoutAndPreservesDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled context remains the native control-flow error.
	_, err := newTestClient(t, httptest.NewTLSServer(http.NotFoundHandler()), nil).Complete(ctx, testCall("https://provider.example/v1", "key"))
	if !errors.Is(err, context.Canceled) || errors.Is(err, sdk.ErrTimeout) {
		t.Fatalf("cancelled Complete error = %v", err)
	}

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	_, err = newTestClient(t, httptest.NewTLSServer(http.NotFoundHandler()), nil).Complete(deadlineCtx, testCall("https://provider.example/v1", "key"))
	if !errors.Is(err, sdk.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("preflight deadline Complete error = %v", err)
	}

	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
	c, err := NewClient(WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Complete(context.Background(), testCall("https://provider.example/v1", "key"))
	if !errors.Is(err, sdk.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline Complete error = %v", err)
	}
}

func TestComplete_ObserverReportsOneSafeAccurateAttempt(t *testing.T) {
	const (
		secret = "observer-secret"
		body   = `{"model":"caller","messages":[{"role":"user","content":"hi"}]}`
	)
	call := testCall("https://provider.example/v1", secret)
	call.Candidate = sdk.CandidateIdentity{ModelID: "model-id", ProviderID: "provider-id", RouteID: "route-id", CredentialID: "credential-id", AdapterID: "adapter-id"}
	call.Request.InjectionPlan.Headers["X-Plan-Header"] = "plan-header-value"
	want := sdk.AttemptMetadata{CandidateIdentity: call.Candidate, Protocol: adapter.ProtocolOpenAIChat}

	assertAttempt := func(t *testing.T, hc *http.Client, wantErr error) {
		t.Helper()
		var attempts atomic.Int32
		var got sdk.AttemptMetadata
		observer := AttemptObserverFunc(func(_ context.Context, metadata sdk.AttemptMetadata) {
			attempts.Add(1)
			got = metadata
		})
		c, err := NewClient(WithHTTPClient(hc), WithAttemptObserver(observer))
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Complete(context.Background(), call)
		if wantErr == nil && err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if wantErr != nil && !errors.Is(err, wantErr) {
			t.Fatalf("Complete error = %v, want %v", err, wantErr)
		}
		if attempts.Load() != 1 {
			t.Fatalf("observer attempts = %d, want 1", attempts.Load())
		}
		if got != want {
			t.Fatalf("observer metadata = %#v, want %#v", got, want)
		}
		for _, forbidden := range []string{secret, call.Target.BaseURL, body, "X-Plan-Header", "plan-header-value"} {
			if formatted := fmt.Sprintf("%+v", got); strings.Contains(formatted, forbidden) {
				t.Errorf("formatted observer metadata leaked %q: %s", forbidden, formatted)
			}
		}
	}

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { success(w) }))
	defer ts.Close()
	call.Target.BaseURL = ts.URL
	assertAttempt(t, tlsClient(t, ts), nil)

	call.Target.BaseURL = "https://provider.example/v1"
	assertAttempt(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport failure")
	})}, sdk.ErrTransport)
}

func TestComplete_ObserverPanicIsIsolated(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		success(w)
	}))
	defer ts.Close()
	panicObserver := AttemptObserverFunc(func(context.Context, sdk.AttemptMetadata) { panic("observer") })
	if _, err := newTestClient(t, ts, panicObserver).Complete(context.Background(), testCall(ts.URL, "key")); err != nil {
		t.Fatalf("observer panic escaped: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestComplete_SuccessRequestIDSharedPolicy(t *testing.T) {
	// The success-path Completion.RequestID must use the same shared policy as
	// sdk.ClassifiedError.RequestID (sdk.SafeRequestID): printable punctuation
	// and printable Unicode are retained; the old strict [A-Za-z0-9_-] safeToken
	// would have reduced every value below to "".
	const id = "req.abc:123/xyz;café-中文_42"
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-request-id", id)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"finish_reason":"stop","index":0,"message":{"role":"assistant","content":"hi"}}],"created":1,"model":"upstream-model","object":"chat.completion"}`))
	}))
	defer ts.Close()
	c := newTestClient(t, ts, nil)
	completion, err := c.Complete(context.Background(), testCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	if completion.RequestID != id {
		t.Fatalf("RequestID = %q, want %q (shared SafeRequestID policy)", completion.RequestID, id)
	}
}

func TestComplete_InvalidPreflightMakesNoHTTP(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		success(w)
	}))
	defer ts.Close()
	client := newTestClient(t, ts, nil)

	for _, tc := range []struct {
		name string
		edit func(*sdk.Call)
		want error
	}{
		{"unsupported protocol", func(c *sdk.Call) { c.Target.Protocol = adapter.ProtocolAnthropic }, ErrUnsupportedProtocol},
		{"missing upstream model", func(c *sdk.Call) { c.Target.UpstreamModel = " \t" }, ErrMissingUpstreamModel},
		{"missing credential", func(c *sdk.Call) { c.Secret = sdk.NewCredentialSecret(nil) }, ErrMissingAPIKey},
		{"protected header", func(c *sdk.Call) { c.Request.InjectionPlan.Headers["Authorization"] = "evil" }, ErrInvalidInjection},
		{"credential-like query", func(c *sdk.Call) { c.Request.InjectionPlan.Query["api_key"] = "evil" }, ErrInvalidInjection},
		{"header control character", func(c *sdk.Call) { c.Request.InjectionPlan.Headers["X-Trace"] = "bad\r\nvalue" }, ErrInvalidInjection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := testCall(ts.URL, "key")
			tc.edit(&call)
			if _, err := client.Complete(context.Background(), call); !errors.Is(err, tc.want) {
				t.Fatalf("Complete error = %v, want %v", err, tc.want)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("invalid preflight made %d HTTP requests", got)
	}
}

type AttemptObserverFunc func(context.Context, sdk.AttemptMetadata)

func (f AttemptObserverFunc) OnAttempt(ctx context.Context, m sdk.AttemptMetadata) { f(ctx, m) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
