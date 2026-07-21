package anthropicadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		Target:    sdk.Target{BaseURL: baseURL, UpstreamModel: "upstream-model", Protocol: adapter.ProtocolAnthropic},
		Request:   adapter.AppliedRequest{Body: json.RawMessage(`{"model":"caller","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`), InjectionPlan: adapter.InjectionPlan{Headers: map[string]string{}, Query: map[string]string{}}},
		Secret:    sdk.NewCredentialSecret([]byte(secret)),
	}
}

func success(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("request-id", "req_123")
	_, _ = w.Write([]byte(`{"id":"msg_01","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":3}}`))
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
	// anthropic-sdk-go reads these environment variables during NewClient.
	// Complete must still use only its call-local target and credential, and
	// the final transport boundary must discard environment-derived custom
	// headers.
	const (
		envAPIKey = "environment-api-key"
		envToken  = "environment-auth-token"
		envHeader = "environment-custom-header"
		callKey   = "call-local-api-key"
	)
	t.Setenv("ANTHROPIC_API_KEY", envAPIKey)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", envToken)
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.example/ignored")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Environment-Custom: "+envHeader)

	var gotPath string
	var gotHeader http.Header
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Clone()
		success(w)
	}))
	defer ts.Close()

	completion, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, callKey))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q", gotPath)
	}
	if v := gotHeader.Get("anthropic-version"); v != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want fixed 2023-06-01", v)
	}
	if values := gotHeader.Values("x-api-key"); len(values) != 1 || values[0] != callKey {
		t.Fatalf("x-api-key values = %q, want exactly call-local key", values)
	}
	for name := range map[string]string{
		"X-Environment-Custom": envHeader,
	} {
		if got := gotHeader.Get(name); got != "" {
			t.Errorf("environment header %s = %q, want absent", name, got)
		}
	}
	// The SDK would authenticate via Authorization if the env auth token won;
	// the boundary must keep it absent so only the call-local x-api-key is used.
	if got := gotHeader.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want absent", got)
	}
	if completion.Status != http.StatusOK || completion.RequestID != "req_123" {
		t.Fatalf("metadata = %#v", completion)
	}
	if len(completion.RawJSON) == 0 {
		t.Fatal("RawJSON empty")
	}
}

func TestComplete_InvalidMessageParamsMakeNoHTTP(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { n.Add(1) }))
	defer ts.Close()
	client := newTestClient(t, ts, nil)
	for name, body := range map[string]string{
		"missing messages":   `{"model":"m","max_tokens":1}`,
		"empty messages":     `{"model":"m","max_tokens":1,"messages":[]}`,
		"bad content union":  `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":null}]}`,
		"unknown root field": `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"x"}],"extra":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			call := testCall(ts.URL, "key")
			call.Request.Body = json.RawMessage(body)
			if _, err := client.Complete(context.Background(), call); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Complete error = %v, want ErrInvalidRequest", err)
			}
		})
	}
	if n.Load() != 0 {
		t.Fatalf("invalid params made %d HTTP requests", n.Load())
	}
}

func TestComplete_AcceptsSafeBaseURLPrefix(t *testing.T) {
	var gotPath string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		success(w)
	}))
	defer ts.Close()

	completion, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL+"/gateway/anthropic", "key"))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/gateway/anthropic/v1/messages" {
		t.Fatalf("path = %q, want /gateway/anthropic/v1/messages", gotPath)
	}
	if completion.Status != http.StatusOK {
		t.Fatalf("status = %d, want %d", completion.Status, http.StatusOK)
	}
}

func TestComplete_TargetValidationNoHTTP(t *testing.T) {
	var n atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { n.Add(1) }))
	defer ts.Close()
	c := newTestClient(t, ts, nil)
	for _, base := range []string{
		"",
		"http://example.test",
		"https://user:pass@example.test",
		"https://example.test?a=b",
		"https://example.test#f",
		"https://example.test//gateway",
		"https://example.test/gateway%2fanthropic",
		"https://example.test/gateway/./anthropic",
		"https://example.test/gateway/../anthropic",
		"https://example.test/gateway\\anthropic",
	} {
		_, err := c.Complete(context.Background(), testCall(base, "key"))
		if !errors.Is(err, ErrInvalidBaseURL) {
			t.Fatalf("base %q: %v", base, err)
		}
	}
	if n.Load() != 0 {
		t.Fatal("invalid target made request")
	}
}

func TestComplete_DifferentSecretsAndExactlyOneAPIKey(t *testing.T) {
	var keys []string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("x-api-key"); len(values) != 1 {
			t.Fatalf("x-api-key values = %v", values)
		}
		keys = append(keys, r.Header.Get("x-api-key"))
		success(w)
	}))
	defer ts.Close()
	c := newTestClient(t, ts, nil)
	for _, secret := range []string{"first", "second"} {
		if _, err := c.Complete(context.Background(), testCall(ts.URL, secret)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(keys, ",") != "first,second" {
		t.Fatalf("keys = %v", keys)
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
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
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
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "x-api-key: evil\nX-Evil: yes")
	var key, evil string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, evil = r.Header.Get("x-api-key"), r.Header.Get("X-Evil")
		success(w)
	}))
	defer ts.Close()
	if _, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "good")); err != nil {
		t.Fatal(err)
	}
	if key != "good" || evil != "" {
		t.Fatalf("key=%q evil=%q", key, evil)
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
		// Every 5xx status maps to unavailable; 529 is Anthropic's overloaded.
		{"overloaded", 529, sdk.ErrUnavailable},
		{"server error", http.StatusBadGateway, sdk.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("request-id", "req.safe:123")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"remote_type","message":"` + secret + `"}}`))
			}))
			defer ts.Close()
			_, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "key"))
			if !errors.Is(err, tc.kind) || strings.Contains(err.Error(), secret) {
				t.Fatalf("Complete error = %v, want %v without remote message", err, tc.kind)
			}
			ce, ok := err.(*sdk.ClassifiedError)
			if !ok || ce.Status() != tc.status || ce.RequestID() != "req.safe:123" || ce.Code() != "" || ce.Type() != "remote_type" {
				t.Fatalf("classified error = %#v", ce)
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts = %d, want 1", attempts.Load())
			}
		})
	}
}

func TestComplete_ClassifiesMalformedSuccessfulResponseAsProtocol(t *testing.T) {
	for _, body := range []string{"not json", `{"content":`} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("request-id", "req.protocol")
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

func TestComplete_StrictSuccessfulResponseValidationDoesNotLeakBody(t *testing.T) {
	for _, body := range []string{
		`{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"upstream-model","stop_reason":"refusal","usage":{"input_tokens":1,"output_tokens":1}}`,
		`{"id":"msg_01","type":"message","role":"assistant","content":[],"model":"upstream-model","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1},"unexpected":"remote-secret"}`,
		`{"id":"msg_01","id":"remote-secret","type":"message","role":"assistant","content":[],"model":"upstream-model","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}`,
	} {
		t.Run(body, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("request-id", "req.protocol")
				_, _ = w.Write([]byte(body))
			}))
			defer ts.Close()

			_, err := newTestClient(t, ts, nil).Complete(context.Background(), testCall(ts.URL, "key"))
			if !errors.Is(err, sdk.ErrProtocol) || strings.Contains(err.Error(), body) || strings.Contains(err.Error(), "remote-secret") {
				t.Fatalf("Complete error = %v, want protocol error without response body", err)
			}
			ce, ok := err.(*sdk.ClassifiedError)
			if !ok || ce.Status() != http.StatusOK || ce.RequestID() != "req.protocol" {
				t.Fatalf("classified error = %#v", ce)
			}
		})
	}
}

func TestComplete_DeadlineClassifiesTimeoutAndPreservesDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A cancelled context remains the native control-flow error.
	_, err := newTestClient(t, httptest.NewTLSServer(http.NotFoundHandler()), nil).Complete(ctx, testCall("https://provider.example", "key"))
	if !errors.Is(err, context.Canceled) || errors.Is(err, sdk.ErrTimeout) {
		t.Fatalf("cancelled Complete error = %v", err)
	}

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	_, err = newTestClient(t, httptest.NewTLSServer(http.NotFoundHandler()), nil).Complete(deadlineCtx, testCall("https://provider.example", "key"))
	if !errors.Is(err, sdk.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("preflight deadline Complete error = %v", err)
	}

	hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
	c, err := NewClient(WithHTTPClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Complete(context.Background(), testCall("https://provider.example", "key"))
	if !errors.Is(err, sdk.ErrTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline Complete error = %v", err)
	}
}

func TestComplete_ObserverReportsOneSafeAccurateAttempt(t *testing.T) {
	const (
		secret = "observer-secret"
		body   = `{"model":"caller","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	)
	call := testCall("https://provider.example", secret)
	call.Candidate = sdk.CandidateIdentity{ModelID: "model-id", ProviderID: "provider-id", RouteID: "route-id", CredentialID: "credential-id", AdapterID: "adapter-id"}
	call.Request.InjectionPlan.Headers["X-Plan-Header"] = "plan-header-value"
	want := sdk.AttemptMetadata{CandidateIdentity: call.Candidate, Protocol: adapter.ProtocolAnthropic}

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

	call.Target.BaseURL = "https://provider.example"
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

func TestComplete_PlanHeaderAndQueryPreservedWithCredentialBoundary(t *testing.T) {
	// The final transport boundary must preserve exactly the validated plan
	// header and plan query, drop every SDK/env-assembled header and query, and
	// pin the credential/version. Authorization must be absent (Anthropic uses
	// x-api-key), x-api-key must occur exactly once with the call-local secret,
	// and anthropic-version must occur exactly once with the fixed protocol
	// version regardless of any environment defaults.
	const (
		callKey    = "call-local-api-key"
		envAPIKey  = "environment-api-key"
		envToken   = "environment-auth-token"
		planHeader = "X-Plan-Trace"
		headerVal  = "plan-header-value"
	)
	t.Setenv("ANTHROPIC_API_KEY", envAPIKey)
	t.Setenv("ANTHROPIC_AUTH_TOKEN", envToken)
	t.Setenv("ANTHROPIC_BASE_URL", "https://env.example/ignored")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "anthropic-version: 9999-99-99\nAuthorization: Bearer evil\nX-Env-Extra: yes")

	var gotHeader http.Header
	var gotQuery url.Values
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotQuery = r.URL.Query()
		success(w)
	}))
	defer ts.Close()

	call := testCall(ts.URL, callKey)
	call.Request.InjectionPlan.Headers[planHeader] = headerVal
	call.Request.InjectionPlan.Query["mode"] = "fast"
	call.Request.InjectionPlan.Query["version"] = "stable"

	if _, err := newTestClient(t, ts, nil).Complete(context.Background(), call); err != nil {
		t.Fatal(err)
	}

	// Plan header preserved verbatim.
	if got := gotHeader.Get(planHeader); got != headerVal {
		t.Fatalf("plan header %s = %q, want %q", planHeader, got, headerVal)
	}
	// Plan query preserved exactly; no SDK/env query survives.
	if len(gotQuery) != 2 || gotQuery.Get("mode") != "fast" || gotQuery.Get("version") != "stable" {
		t.Fatalf("query = %#v, want exactly {mode:fast, version:stable}", gotQuery)
	}
	// Authorization must be absent: Anthropic authenticates only via x-api-key.
	if got := gotHeader.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want absent", got)
	}
	// x-api-key occurs exactly once and carries only the call-local secret.
	if values := gotHeader.Values("x-api-key"); len(values) != 1 || values[0] != callKey {
		t.Fatalf("x-api-key values = %v, want exactly [%s]", values, callKey)
	}
	// anthropic-version is fixed and unique, never the env-injected value.
	if values := gotHeader.Values("anthropic-version"); len(values) != 1 || values[0] != "2023-06-01" {
		t.Fatalf("anthropic-version values = %v, want exactly [2023-06-01]", values)
	}
	// No env-derived custom header leaks through.
	if got := gotHeader.Get("X-Env-Extra"); got != "" {
		t.Fatalf("X-Env-Extra = %q, want absent", got)
	}
}

func TestSanitizingRoundTripper_DropsPreexistingQueryAndRebuildsFromPlan(t *testing.T) {
	// The sanitizer must reconstruct the request URL query solely from the
	// validated plan query, dropping every query parameter that the SDK or
	// environment assembled on the incoming request. This is the
	// defense-in-depth property that complements the TLS path: even if a
	// non-plan query reached the transport (for example a beta flag injected by
	// an SDK default), the wire query is exactly the compiler-validated plan.
	var got *http.Request
	rt := sanitizingRoundTripper{
		next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			got = r.Clone(r.Context())
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: make(http.Header)}, nil
		}),
		headers: map[string]string{"X-Plan-Trace": "plan"},
		query:   map[string]string{"mode": "fast"},
		apiKey:  "call-local-api-key",
	}
	base := &url.URL{Scheme: "https", Host: "provider.example", Path: "/v1/messages"}
	req := &http.Request{
		Method: http.MethodPost,
		URL:    base.JoinPath(),
		Header: http.Header{},
	}
	// Simulate an SDK/env-assembled query that is NOT in the plan, plus a
	// duplicate of the plan key with a smuggled value.
	req.URL.RawQuery = "beta=smuggled&mode=evil&leftover=1"
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	q := got.URL.Query()
	if len(q) != 1 || q.Get("mode") != "fast" {
		t.Fatalf("reconstructed query = %#v, want exactly {mode:fast}", q)
	}
	// Headers are rebuilt from the protocol allowlist + plan only.
	if got.Header.Get("X-Plan-Trace") != "plan" {
		t.Fatalf("plan header = %q, want plan", got.Header.Get("X-Plan-Trace"))
	}
	if values := got.Header.Values("x-api-key"); len(values) != 1 || values[0] != "call-local-api-key" {
		t.Fatalf("x-api-key = %v", values)
	}
	if got.Header.Get("Authorization") != "" {
		t.Fatalf("Authorization = %q, want absent", got.Header.Get("Authorization"))
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
		w.Header().Set("request-id", id)
		_, _ = w.Write([]byte(`{"id":"msg_01","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":5,"output_tokens":3}}`))
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
		{"unsupported protocol", func(c *sdk.Call) { c.Target.Protocol = adapter.ProtocolOpenAIChat }, ErrUnsupportedProtocol},
		{"missing upstream model", func(c *sdk.Call) { c.Target.UpstreamModel = " \t" }, ErrMissingUpstreamModel},
		{"missing credential", func(c *sdk.Call) { c.Secret = sdk.NewCredentialSecret(nil) }, ErrMissingAPIKey},
		{"protected header", func(c *sdk.Call) { c.Request.InjectionPlan.Headers["x-api-key"] = "evil" }, ErrInvalidInjection},
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
