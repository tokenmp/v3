package executorv1api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	legacyrouter "github.com/getkin/kin-openapi/routers/legacy"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// This integration test wires the transport Adapter into the oapi-codegen
// generated NewStrictHandler, wraps it with the package's external
// CaptureRawBody middleware, and drives every non-stream path through the
// generated Chi router. It reuses the server package's OpenAPI response
// validation pattern (load contract, build legacy router, validate response)
// without modifying the server package.

const (
	integrationChatRaw = `{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"gpt","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	integrationMsgRaw  = `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`
)

// recorderExecutor is a NonStreamExecutor double that records the exact
// normalized request it receives and returns a canned terminal result.
type recorderExecutor struct {
	calls     atomic.Int32
	seenBody  []byte
	seenID    string
	seenThink adapter.ThinkingRequest
	result    execution.Result
	execErr   error
}

func (e *recorderExecutor) Execute(_ context.Context, request NonStreamRequest) (NonStreamResult, error) {
	e.calls.Add(1)
	e.seenBody = append([]byte(nil), request.Body...)
	e.seenID = request.RequestID
	e.seenThink = request.Thinking
	return e.result, e.execErr
}

type nilPointerExecutor struct{}

func (*nilPointerExecutor) Execute(context.Context, NonStreamRequest) (NonStreamResult, error) {
	panic("nil pointer executor must never be called")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := thisFile
	// thisFile: .../v3/services/executor/internal/transport/executorv1api/*_test.go
	for i := 0; i < 6; i++ {
		idx := strings.LastIndexByte(dir, '/')
		if idx < 0 {
			t.Fatalf("cannot ascend from %q", dir)
		}
		dir = dir[:idx]
	}
	return dir
}

func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(repoRoot(t) + "/packages/contracts/openapi/executor/v1.yaml")
	if err != nil {
		t.Fatalf("load contract: %v", err)
	}
	ctx := context.Background()
	if err := doc.Validate(ctx, openapi3.AllowExtraSiblingFields("description", "nullable")); err != nil {
		t.Fatalf("validate contract: %v", err)
	}
	return doc
}

func newOpenAPIRouter(t *testing.T) routers.Router {
	t.Helper()
	router, err := legacyrouter.NewRouter(loadContract(t), openapi3.AllowExtraSiblingFields("description", "nullable"))
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	return router
}

func validateOpenAPIResponse(t *testing.T, requestInput *openapi3filter.RequestValidationInput, recorder *httptest.ResponseRecorder) {
	t.Helper()
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: requestInput,
		Status:                 recorder.Code,
		Header:                 recorder.Header(),
	}
	input.SetBodyBytes(recorder.Body.Bytes())
	if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
		t.Fatalf("OpenAPI response validation failed for status %d body %q: %v", recorder.Code, recorder.Body.String(), err)
	}
}

func findAndValidateRequest(t *testing.T, router routers.Router, request *http.Request) *openapi3filter.RequestValidationInput {
	t.Helper()
	input := routeInput(t, router, request)
	if err := openapi3filter.ValidateRequest(request.Context(), input); err != nil {
		t.Fatalf("OpenAPI request validation failed: %v", err)
	}
	return input
}

// routeInput builds the request validation input (route + path params) without
// validating the request body. It is used for cases where the request body is
// intentionally invalid (malformed JSON) so the response can still be
// validated against the route's response schema.
func routeInput(t *testing.T, router routers.Router, request *http.Request) *openapi3filter.RequestValidationInput {
	t.Helper()
	route, pathParams, err := router.FindRoute(request)
	if err != nil {
		t.Fatalf("find route: %v", err)
	}
	return &openapi3filter.RequestValidationInput{
		Request:    request,
		PathParams: pathParams,
		Route:      route,
		Options:    &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc},
	}
}

// buildServer wires the Adapter through the generated strict server, the safe
// strict options (so decoder failures render protocol-native 400s) and the
// external CaptureRawBody middleware.
func buildServer(t *testing.T, adapter *Adapter) http.Handler {
	t.Helper()
	strict := executorv1.NewStrictHandlerWithOptions(adapter, nil, SafeStrictOptions())
	return CaptureRawBody(executorv1.Handler(strict))
}

func newRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, "http://127.0.0.1:8081"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	return req
}

func TestNonStreamIntegrationChatSuccessRawIdentityAndOpenAPI(t *testing.T) {
	t.Parallel()
	executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
	server := buildServer(t, NewNonStream(Options{Executor: executor}))
	router := newOpenAPIRouter(t)

	body := integrationChatRequestBody()
	req := newRequest(http.MethodPost, "/v1/chat/completions", body)
	requestInput := findAndValidateRequest(t, router, req)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)

	validateOpenAPIResponse(t, requestInput, recorder)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	// Raw identity: the bytes the executor received are exactly the client bytes.
	if !bytes.Equal(executor.seenBody, []byte(body)) {
		t.Errorf("executor body = %q, want exact %q", executor.seenBody, body)
	}
	// Raw identity: the response body is byte-for-byte the validated provider body.
	if recorder.Body.String() != integrationChatRaw {
		t.Errorf("response body = %q, want exact provider body", recorder.Body.String())
	}
	if executor.calls.Load() != 1 {
		t.Errorf("executor calls = %d, want 1", executor.calls.Load())
	}
	if !strings.HasPrefix(executor.seenID, requestIDPrefix) {
		t.Errorf("trusted request ID = %q, want %s prefix", executor.seenID, requestIDPrefix)
	}
}

func TestNonStreamIntegrationMessageSuccessRawIdentityAndOpenAPI(t *testing.T) {
	t.Parallel()
	executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationMsgRaw)}}}
	server := buildServer(t, NewNonStream(Options{Executor: executor}))
	router := newOpenAPIRouter(t)

	body := integrationMessageRequestBody()
	req := newRequest(http.MethodPost, "/v1/messages", body)
	requestInput := findAndValidateRequest(t, router, req)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)

	validateOpenAPIResponse(t, requestInput, recorder)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Equal(executor.seenBody, []byte(body)) {
		t.Errorf("executor body = %q, want exact %q", executor.seenBody, body)
	}
	if recorder.Body.String() != integrationMsgRaw {
		t.Errorf("response body = %q, want exact provider body", recorder.Body.String())
	}
	if executor.calls.Load() != 1 {
		t.Errorf("executor calls = %d, want 1", executor.calls.Load())
	}
}

func TestNonStreamIntegrationSuccessExtensionsValidateAgainstContract(t *testing.T) {
	t.Parallel()
	router := newOpenAPIRouter(t)
	cases := []struct {
		name, path, body, raw string
	}{
		{
			"openai", "/v1/chat/completions", integrationChatRequestBody(),
			`{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"gpt","system_fingerprint":"fp_1","service_tier":"default","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi","refusal":null,"annotations":[]},"logprobs":{"content":[]}}]}`,
		},
		{
			"anthropic", "/v1/messages", integrationMessageRequestBody(),
			`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi","citations":[]}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"server_tool_use":{"web_search_requests":0}},"container":{"id":"container_1"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(tc.raw)}}}
			req := newRequest(http.MethodPost, tc.path, tc.body)
			input := findAndValidateRequest(t, router, req)
			recorder := httptest.NewRecorder()
			buildServer(t, NewNonStream(Options{Executor: executor})).ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, input, recorder)
			if recorder.Code != http.StatusOK || recorder.Body.String() != tc.raw {
				t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestNonStreamIntegrationMappedFailureRendersSafeProtocolNative(t *testing.T) {
	t.Parallel()
	router := newOpenAPIRouter(t)

	cases := []struct {
		name     string
		path     string
		body     string
		failure  adapter.MappedResponse
		wantCode int
		leak     string
	}{
		{
			name:     "chat declared",
			path:     "/v1/chat/completions",
			body:     integrationChatRequestBody(),
			failure:  adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Retry later."},
			wantCode: 429,
		},
		{
			name:     "message declared",
			path:     "/v1/messages",
			body:     integrationMessageRequestBody(),
			failure:  adapter.MappedResponse{HTTPStatus: 429, ErrorCode: "RATE_LIMITED", ErrorType: "rate_limit_error", Message: "Retry later."},
			wantCode: 429,
		},
		{
			name:     "chat unsafe sanitized",
			path:     "/v1/chat/completions",
			body:     integrationChatRequestBody(),
			failure:  adapter.MappedResponse{HTTPStatus: 500, ErrorCode: "private\ncode", ErrorType: "private", Message: "https://private.example/secret"},
			wantCode: 500,
			leak:     "private.example",
		},
		{
			name:     "message unsafe sanitized",
			path:     "/v1/messages",
			body:     integrationMessageRequestBody(),
			failure:  adapter.MappedResponse{HTTPStatus: 500, ErrorCode: "private\ncode", ErrorType: "private", Message: "https://private.example/secret"},
			wantCode: 500,
			leak:     "private.example",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recorderExecutor{result: execution.Result{Failure: &tc.failure}}
			server := buildServer(t, NewNonStream(Options{Executor: executor}))
			req := newRequest(http.MethodPost, tc.path, tc.body)
			requestInput := findAndValidateRequest(t, router, req)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, requestInput, recorder)
			if recorder.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantCode, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if tc.leak != "" && strings.Contains(recorder.Body.String(), tc.leak) {
				t.Fatalf("unsafe upstream detail leaked: %s", recorder.Body.String())
			}
			if executor.calls.Load() != 1 {
				t.Errorf("executor calls = %d, want 1", executor.calls.Load())
			}
		})
	}
}

func TestNonStreamIntegrationStreamRequestReturnsNative501NoExecution(t *testing.T) {
	t.Parallel()
	executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
	server := buildServer(t, NewNonStream(Options{Executor: executor}))
	router := newOpenAPIRouter(t)

	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"chat stream", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true}`},
		{"message stream", "/v1/messages", `{"model":"claude","messages":[{"role":"user","content":"hi"}],"max_tokens":1,"stream":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest(http.MethodPost, tc.path, tc.body)
			requestInput := findAndValidateRequest(t, router, req)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, requestInput, recorder)
			if recorder.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body=%s", recorder.Code, recorder.Body.String())
			}
			if executor.calls.Load() != 0 {
				t.Fatalf("executor was called %d times for a stream request", executor.calls.Load())
			}
		})
	}
}

func TestNonStreamIntegrationDecoderBodyLimitReturnsNative400(t *testing.T) {
	t.Parallel()
	executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
	server := buildServer(t, NewNonStream(Options{Executor: executor}))
	router := newOpenAPIRouter(t)

	overLimit := bytes.Repeat([]byte("x"), int(MaxCapturedBodyBytes)+1)
	req := newRequest(http.MethodPost, "/v1/messages", string(overLimit))
	requestInput := routeInput(t, router, req)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	validateOpenAPIResponse(t, requestInput, recorder)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "invalid_request_error") {
		t.Fatalf("body = %q, want Anthropic invalid_request_error", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "xxxx") {
		t.Fatalf("raw oversized body leaked into error: %s", recorder.Body.String())
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor called %d times for oversized body", executor.calls.Load())
	}
}

func TestNonStreamIntegrationMalformedDuplicateUnknownReturnNative400(t *testing.T) {
	t.Parallel()
	executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
	server := buildServer(t, NewNonStream(Options{Executor: executor}))
	router := newOpenAPIRouter(t)

	cases := []struct {
		name string
		path string
		body string
	}{
		{"chat malformed", "/v1/chat/completions", `{"model":"gpt","messages":[ BADJSON`},
		{"chat duplicate", "/v1/chat/completions", `{"model":"gpt","model":"other","messages":[]}`},
		{"chat unknown", "/v1/chat/completions", `{"model":"gpt","messages":[],"unknown_field":1}`},
		{"message malformed", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[}`},
		{"message duplicate", "/v1/messages", `{"model":"c","model":"d","max_tokens":1,"messages":[]}`},
		{"message unknown", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[],"bogus":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequest(http.MethodPost, tc.path, tc.body)
			requestInput := routeInput(t, router, req)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, requestInput, recorder)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if !strings.Contains(recorder.Body.String(), "invalid_request_error") {
				t.Fatalf("body = %q, want invalid_request_error", recorder.Body.String())
			}
		})
	}
	if executor.calls.Load() != 0 {
		t.Fatalf("executor called %d times for an invalid request", executor.calls.Load())
	}
}

func TestNonStreamIntegrationAnthropicErrorCarriesTrustedRequestID(t *testing.T) {
	t.Parallel()
	executor := &recorderExecutor{result: execution.Result{Failure: &adapter.MappedResponse{HTTPStatus: 500, ErrorCode: "BOOM", ErrorType: "api_error", Message: "fail"}}}
	fixedID := RequestIDSourceFunc(func(context.Context) string { return "trusted.req/abc-1" })
	server := buildServer(t, NewNonStream(Options{Executor: executor, RequestIDs: fixedID}))
	router := newOpenAPIRouter(t)

	req := newRequest(http.MethodPost, "/v1/messages", integrationMessageRequestBody())
	requestInput := findAndValidateRequest(t, router, req)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, req)
	validateOpenAPIResponse(t, requestInput, recorder)
	if recorder.Code != 500 {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["request_id"] != "trusted.req/abc-1" {
		t.Errorf("request_id = %#v, want trusted.req/abc-1", body["request_id"])
	}
	if executor.seenID != "trusted.req/abc-1" {
		t.Errorf("executor request ID = %q, want trusted.req/abc-1", executor.seenID)
	}
	if executor.calls.Load() != 1 {
		t.Errorf("executor calls = %d, want 1", executor.calls.Load())
	}
}

func TestNonStreamIntegrationHealthCacheAndModelHeaders(t *testing.T) {
	t.Parallel()
	server := buildServer(t, New())
	router := newOpenAPIRouter(t)

	t.Run("health cache headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/healthz", nil)
		requestInput := findAndValidateRequest(t, router, req)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		validateOpenAPIResponse(t, requestInput, recorder)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})

	t.Run("foundation model status and content type", func(t *testing.T) {
		req := newRequest(http.MethodPost, "/v1/responses", `{"model":"gpt","input":"hi"}`)
		requestInput := findAndValidateRequest(t, router, req)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		validateOpenAPIResponse(t, requestInput, recorder)
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("status = %d, want 501", recorder.Code)
		}
		if got := recorder.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
	})
}

func TestNonStreamIntegrationNilAndTypedNilExecutorFailClosed(t *testing.T) {
	t.Parallel()
	router := newOpenAPIRouter(t)

	servers := []struct {
		name   string
		server http.Handler
	}{
		{"untyped nil", buildServer(t, NewNonStream(Options{Executor: nil}))},
		{"typed nil", buildServer(t, NewNonStream(Options{Executor: (*nilPointerExecutor)(nil)}))},
	}
	for _, sc := range servers {
		t.Run(sc.name, func(t *testing.T) {
			req := newRequest(http.MethodPost, "/v1/chat/completions", integrationChatRequestBody())
			requestInput := findAndValidateRequest(t, router, req)
			recorder := httptest.NewRecorder()
			sc.server.ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, requestInput, recorder)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 fail-closed; body=%s", recorder.Code, recorder.Body.String())
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
				Status int `json:"status"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Status != http.StatusInternalServerError || body.Error.Code != internalErrorCode || body.Error.Type != "api_error" || strings.Contains(body.Error.Message, "executor") {
				t.Fatalf("fail-closed body = %#v", body)
			}
		})
	}
}

// writeTrackingRecorder distinguishes no write from httptest.ResponseRecorder's
// implicit default 200, proving lifecycle errors do not produce a response.
type writeTrackingRecorder struct {
	header http.Header
	body   bytes.Buffer
	writes int
}

func newWriteTrackingRecorder() *writeTrackingRecorder {
	return &writeTrackingRecorder{header: make(http.Header)}
}
func (w *writeTrackingRecorder) Header() http.Header { return w.header }
func (w *writeTrackingRecorder) WriteHeader(int)     { w.writes++ }
func (w *writeTrackingRecorder) Write(p []byte) (int, error) {
	w.writes++
	return w.body.Write(p)
}

func TestNonStreamIntegrationContextLifecycleDoesNotWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		path    string
		body    string
		execErr error
	}{
		{"chat canceled", "/v1/chat/completions", integrationChatRequestBody(), context.Canceled},
		{"chat deadline", "/v1/chat/completions", integrationChatRequestBody(), context.DeadlineExceeded},
		{"message canceled", "/v1/messages", integrationMessageRequestBody(), context.Canceled},
		{"message deadline", "/v1/messages", integrationMessageRequestBody(), context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recorderExecutor{execErr: tc.execErr}
			server := buildServer(t, NewNonStream(Options{Executor: executor}))
			writer := newWriteTrackingRecorder()
			server.ServeHTTP(writer, newRequest(http.MethodPost, tc.path, tc.body))
			if writer.writes != 0 || writer.body.Len() != 0 || len(writer.header) != 0 {
				t.Fatalf("lifecycle response wrote %d times: headers=%#v body=%q", writer.writes, writer.header, writer.body.String())
			}
			if executor.calls.Load() != 1 {
				t.Errorf("executor calls = %d, want 1", executor.calls.Load())
			}
		})
	}
}

// integrationChatRequestBody is a minimal valid chat request for the contract.
func integrationChatRequestBody() string {
	return `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false}`
}

// integrationMessageRequestBody is a minimal valid Anthropic messages request.
func integrationMessageRequestBody() string {
	return `{"model":"claude","max_tokens":128,"messages":[{"role":"user","content":"hi"}],"stream":false}`
}

// TestNonStreamIntegrationSchemaInvalidNestedReturnsNative400NoExecution asserts that
// schema-invalid requests at every nested boundary (messages, content parts,
// tools, tool_choice, thinking, system blocks, metadata, numeric bounds) render
// a protocol-native 400, never call the executor, and never echo the invalid
// field name or value into the error body. The generated decoder accepts many
// of these because Go struct unmarshal ignores unknown fields, so the
// transport normalizer is the strict schema gate.
func TestNonStreamIntegrationSchemaInvalidNestedReturnsNative400NoExecution(t *testing.T) {
	t.Parallel()
	router := newOpenAPIRouter(t)

	cases := []struct {
		name string
		path string
		body string
		leak string
	}{
		// OpenAI Chat nested schema
		{"chat unknown root", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"extra":1}`, "extra"},
		{"chat empty messages", "/v1/chat/completions", `{"model":"gpt","messages":[]}`, ""},
		{"chat bad role", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"dev","content":"hi"}]}`, "dev"},
		{"chat tool missing parameters", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f"}}]}`, "parameters"},
		{"chat tool_choice bad enum", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"tool_choice":"never"}`, "never"},
		{"chat temperature too high", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"temperature":5}`, "temperature"},
		{"chat image_url non-uri", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"not a url"}}]}]}`, "not a url"},
		{"chat reasoning_effort unknown", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"ultra"}`, "ultra"},
		{"chat stream true unknown root", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":true,"extra":1}`, "extra"},
		{"chat stream true bad role", "/v1/chat/completions", `{"model":"gpt","messages":[{"role":"dev","content":"hi"}],"stream":true}`, "dev"},
		// Anthropic Messages nested schema
		{"message unknown root", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"extra":1}`, "extra"},
		{"message empty messages", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[]}`, ""},
		{"message content block unknown type", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"audio"}]}]}`, "audio"},
		{"message image data non-string", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":7}}]}]}`, ""},
		{"message tool_use input not object", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"tool_use","id":"x","name":"f","input":[]}]}]}`, "input"},
		{"message thinking budget equals max", "/v1/messages", `{"model":"c","max_tokens":1024,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024}}`, "budget_tokens"},
		{"message tool missing input_schema", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"f"}]}`, "input_schema"},
		{"message metadata unknown field", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"metadata":{"team":"x"}}`, "team"},
		{"message max_tokens zero", "/v1/messages", `{"model":"c","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`, ""},
		{"message stream true unknown root", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":true,"extra":1}`, "extra"},
		{"message stream true unknown block", "/v1/messages", `{"model":"c","max_tokens":1,"messages":[{"role":"user","content":[{"type":"audio"}]}],"stream":true}`, "audio"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
			server := buildServer(t, NewNonStream(Options{Executor: executor}))
			req := newRequest(http.MethodPost, tc.path, tc.body)
			requestInput := routeInput(t, router, req)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, requestInput, recorder)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if !strings.Contains(recorder.Body.String(), "invalid_request_error") {
				t.Fatalf("body = %q, want invalid_request_error", recorder.Body.String())
			}
			if tc.leak != "" && strings.Contains(recorder.Body.String(), tc.leak) {
				t.Fatalf("schema-invalid field/value echoed into error: %s", recorder.Body.String())
			}
			if executor.calls.Load() != 0 {
				t.Fatalf("executor called %d times for schema-invalid request", executor.calls.Load())
			}
		})
	}
}

// TestNonStreamIntegrationReasoningEffortNoneDisabledAndSchemaInvalid exercises
// the OpenAI reasoning_effort enum against the current Executor OpenAPI. The
// "none" enum value is the schema-valid way to disable reasoning: the
// executor must observe a disabled ThinkingRequest (Enabled=false, no effort,
// no budget). Any schema-invalid reasoning_effort must yield a protocol-native
// 400 with no echo and zero executor calls.
func TestNonStreamIntegrationReasoningEffortNoneDisabledAndSchemaInvalid(t *testing.T) {
	t.Parallel()
	router := newOpenAPIRouter(t)

	t.Run("none maps disabled at executor boundary", func(t *testing.T) {
		t.Parallel()
		executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
		server := buildServer(t, NewNonStream(Options{Executor: executor}))
		body := `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false,"reasoning_effort":"none"}`
		req := newRequest(http.MethodPost, "/v1/chat/completions", body)
		requestInput := findAndValidateRequest(t, router, req)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		validateOpenAPIResponse(t, requestInput, recorder)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if executor.calls.Load() != 1 {
			t.Fatalf("executor calls = %d, want 1", executor.calls.Load())
		}
		if executor.seenThink.Enabled || executor.seenThink.Effort != "" || executor.seenThink.BudgetTokens != nil {
			t.Fatalf("disabled reasoning_effort \"none\" reached executor as %#v, want zero/disabled", executor.seenThink)
		}
	})

	t.Run("enabled effort reaches executor", func(t *testing.T) {
		t.Parallel()
		executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
		server := buildServer(t, NewNonStream(Options{Executor: executor}))
		body := `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false,"reasoning_effort":"high"}`
		req := newRequest(http.MethodPost, "/v1/chat/completions", body)
		requestInput := findAndValidateRequest(t, router, req)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		validateOpenAPIResponse(t, requestInput, recorder)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
		if executor.calls.Load() != 1 {
			t.Fatalf("executor calls = %d, want 1", executor.calls.Load())
		}
		if !executor.seenThink.Enabled || executor.seenThink.Effort != adapter.ThinkingHigh || executor.seenThink.BudgetTokens != nil {
			t.Fatalf("reasoning_effort \"high\" reached executor as %#v, want enabled/high", executor.seenThink)
		}
	})

	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown enum", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false,"reasoning_effort":"ultra"}`},
		{"number effort", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false,"reasoning_effort":7}`},
		{"null effort", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false,"reasoning_effort":null}`},
		{"object effort", `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"stream":false,"reasoning_effort":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &recorderExecutor{result: execution.Result{Completion: sdk.Completion{Status: http.StatusOK, RawJSON: json.RawMessage(integrationChatRaw)}}}
			server := buildServer(t, NewNonStream(Options{Executor: executor}))
			req := newRequest(http.MethodPost, "/v1/chat/completions", tc.body)
			requestInput := routeInput(t, router, req)
			recorder := httptest.NewRecorder()
			server.ServeHTTP(recorder, req)
			validateOpenAPIResponse(t, requestInput, recorder)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), "invalid_request_error") {
				t.Fatalf("body = %q, want invalid_request_error", recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "reasoning_effort") || strings.Contains(recorder.Body.String(), "ultra") {
				t.Fatalf("schema-invalid field echoed into error: %s", recorder.Body.String())
			}
			if executor.calls.Load() != 0 {
				t.Fatalf("executor called %d times for schema-invalid reasoning_effort", executor.calls.Load())
			}
		})
	}
}
