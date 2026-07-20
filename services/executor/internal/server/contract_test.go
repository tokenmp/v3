// Package server contains a contract conformance test that verifies the
// Executor service's generated Chi routes match the OpenAPI contract at
// packages/contracts/openapi/executor/v1.yaml.
//
// This test is the machine-verifiable link between the Executor transport
// adapter (built on the oapi-codegen generated strict server) and the
// @tokenmp/contracts package. It loads the single authoritative contract at
// test time (never at runtime) and fails the build if the generated routing
// drifts from the contract or vice versa.
//
// No database connection is required: the strict handler is constructed from
// the transport adapter, whose model operations return protocol-native 501
// responses, so every route resolves without a real backend. The contract YAML
// is located via runtime.Caller (not cwd) so the test works regardless of the
// working directory.
//
// Note: This test only proves the generated Handler/StrictHandler wiring is
// consistent with the OpenAPI contract. It does NOT imply the business routes
// are registered in the runtime main — the runtime Executor server still only
// serves the Foundation /healthz endpoint directly via the healthz handler.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	legacyrouter "github.com/getkin/kin-openapi/routers/legacy"
	"github.com/go-chi/chi/v5"

	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
	"github.com/tokenmp/v3/services/executor/internal/transport/executorv1api"
)

// ---------------------------------------------------------------------------
// Contract loading
// ---------------------------------------------------------------------------

// repoRoot returns the repository root directory by walking up from the
// source file location of this test. It uses runtime.Caller so the result
// does not depend on the process working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: .../v3/services/executor/internal/server/contract_test.go
	// We need to go up 5 directories to reach the repo root "v3/".
	dir := thisFile
	for i := 0; i < 5; i++ {
		idx := strings.LastIndexByte(dir, '/')
		if idx < 0 {
			t.Fatalf("cannot ascend from %q", dir)
		}
		dir = dir[:idx]
	}
	return dir
}

// loadContract reads and validates the Executor OpenAPI contract from the repo.
func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	root := repoRoot(t)
	contractPath := root + "/packages/contracts/openapi/executor/v1.yaml"

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(contractPath)
	if err != nil {
		t.Fatalf("failed to load OpenAPI contract from %s: %v", contractPath, err)
	}
	// Validate the document structure. The Executor contract uses the
	// OpenAPI 3.0 idiom of `description`/`nullable` siblings alongside `$ref`
	// (spec-permitted; siblings are ignored). The project's own contract
	// validator (packages/contracts validate.mjs) accepts this, so we allow
	// those sibling fields here rather than acting as a stricter, contradictory
	// validator. Full structural validation is owned by the contracts package.
	ctx := context.Background()
	if err := doc.Validate(ctx, openapi3.AllowExtraSiblingFields("description", "nullable")); err != nil {
		t.Fatalf("OpenAPI contract validation failed: %v", err)
	}
	return doc
}

// contractRoutes extracts all (method, path) pairs declared in the OpenAPI
// contract. Methods are upper-cased HTTP verbs; paths are the OpenAPI path
// strings (e.g. "/v1/models").
func contractRoutes(t *testing.T, doc *openapi3.T) map[string]struct{} {
	t.Helper()
	routes := make(map[string]struct{})
	if doc.Paths == nil {
		t.Fatal("contract has no paths")
	}
	for _, pathStr := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Find(pathStr)
		if item == nil {
			continue
		}
		for method := range item.Operations() {
			key := strings.ToUpper(method) + " " + pathStr
			routes[key] = struct{}{}
		}
	}
	if len(routes) == 0 {
		t.Fatal("contract declares zero routes")
	}
	return routes
}

// ---------------------------------------------------------------------------
// Generated handler construction
// ---------------------------------------------------------------------------

// buildContractHandler wires the Executor transport adapter into the
// oapi-codegen generated strict server and returns the generated Chi handler
// whose routing mirrors the OpenAPI contract. The adapter's model operations
// return protocol-native 501 errors, so every route resolves without a real
// backend.
func buildContractHandler(t *testing.T) http.Handler {
	t.Helper()
	strict := executorv1.NewStrictHandler(executorv1api.New(), nil)
	return executorv1.Handler(strict)
}

// ---------------------------------------------------------------------------
// Chi route walking
// ---------------------------------------------------------------------------

// actualRoutes walks the Chi router and returns all registered (method, path)
// pairs.
func actualRoutes(t *testing.T, h http.Handler) map[string]struct{} {
	t.Helper()
	routes := make(map[string]struct{})

	r, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("handler does not implement chi.Routes")
	}

	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if len(route) > 1 {
			route = strings.TrimRight(route, "/")
		}
		key := strings.ToUpper(method) + " " + route
		routes[key] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk failed: %v", err)
	}

	if len(routes) == 0 {
		t.Fatal("Chi router has zero routes")
	}
	return routes
}

// ---------------------------------------------------------------------------
// Conformance test
// ---------------------------------------------------------------------------

func TestContractConformance(t *testing.T) {
	t.Parallel()

	// 1. Load and validate the OpenAPI contract.
	doc := loadContract(t)
	contractSet := contractRoutes(t, doc)

	// 2. Build the generated handler from the transport adapter and extract
	//    actual routes.
	handler := buildContractHandler(t)
	actualSet := actualRoutes(t, handler)

	// 3. Bidirectional comparison.
	var missingFromImpl []string
	for k := range contractSet {
		if _, ok := actualSet[k]; !ok {
			missingFromImpl = append(missingFromImpl, k)
		}
	}

	var missingFromContract []string
	for k := range actualSet {
		if _, ok := contractSet[k]; !ok {
			missingFromContract = append(missingFromContract, k)
		}
	}

	sort.Strings(missingFromImpl)
	sort.Strings(missingFromContract)

	if len(missingFromImpl) > 0 {
		t.Errorf("routes in contract but NOT in generated handler:\n  %s", strings.Join(missingFromImpl, "\n  "))
	}
	if len(missingFromContract) > 0 {
		t.Errorf("routes in generated handler but NOT in contract:\n  %s", strings.Join(missingFromContract, "\n  "))
	}

	if len(missingFromImpl) > 0 || len(missingFromContract) > 0 {
		t.Fatalf("contract conformance check failed: %d missing from generated handler, %d missing from contract",
			len(missingFromImpl), len(missingFromContract))
	}

	t.Logf("contract conformance OK: %d routes match", len(contractSet))
}

// TestGeneratedHandlerEndToEnd exercises every OpenAPI operation through the
// generated Chi Handler and strict adapter. It intentionally does not attach
// this handler to the Executor runtime composition root.
func TestGeneratedHandlerEndToEnd(t *testing.T) {
	t.Parallel()

	doc := loadContract(t)
	router, err := legacyrouter.NewRouter(doc, openapi3.AllowExtraSiblingFields("description", "nullable"))
	if err != nil {
		t.Fatalf("create OpenAPI router: %v", err)
	}
	handler := buildContractHandler(t)
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		assertBody func(*testing.T, []byte)
	}{
		{
			name:       "get healthz",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			assertBody: assertHealthResponse,
		},
		{
			name:       "head healthz",
			method:     http.MethodHead,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			assertBody: func(t *testing.T, body []byte) {
				t.Helper()
				if len(body) != 0 {
					t.Errorf("HEAD response body = %q, want empty", body)
				}
			},
		},
		{
			name:       "list models",
			method:     http.MethodGet,
			path:       "/v1/models",
			wantStatus: http.StatusNotImplemented,
			assertBody: assertOpenAINotImplemented,
		},
		{
			name:       "create chat completion",
			method:     http.MethodPost,
			path:       "/v1/chat/completions",
			body:       `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`,
			wantStatus: http.StatusNotImplemented,
			assertBody: assertOpenAINotImplemented,
		},
		{
			name:       "create message",
			method:     http.MethodPost,
			path:       "/v1/messages",
			body:       `{"model":"claude-3-opus-20240229","max_tokens":1,"messages":[{"role":"user","content":"hello"}]}`,
			wantStatus: http.StatusNotImplemented,
			assertBody: assertAnthropicNotImplemented,
		},
		{
			name:       "create response",
			method:     http.MethodPost,
			path:       "/v1/responses",
			body:       `{"model":"gpt-4o","input":"hello"}`,
			wantStatus: http.StatusNotImplemented,
			assertBody: assertOpenAINotImplemented,
		},
		{
			name:       "create image",
			method:     http.MethodPost,
			path:       "/v1/images/generations",
			body:       `{"model":"dall-e-3","prompt":"hello"}`,
			wantStatus: http.StatusNotImplemented,
			assertBody: assertOpenAINotImplemented,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://127.0.0.1:8081"+test.path, bytes.NewBufferString(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			request.Header.Set("Authorization", "Bearer test")
			requestInput := validateOpenAPIRequest(t, router, request)

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			response := recorder.Result()
			defer response.Body.Close()
			validateOpenAPIResponse(t, requestInput, response.StatusCode, response.Header, recorder.Body.Bytes())
			if response.StatusCode != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			if test.wantStatus != http.StatusOK || test.method == http.MethodGet {
				if got := response.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", got)
				}
			}
			test.assertBody(t, recorder.Body.Bytes())
		})
	}

	t.Run("post healthz is method not allowed", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/healthz", nil))
		if got := recorder.Code; got != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", got, http.StatusMethodNotAllowed)
		}
	})
}

func validateOpenAPIRequest(t *testing.T, router routers.Router, request *http.Request) *openapi3filter.RequestValidationInput {
	t.Helper()
	route, pathParams, err := router.FindRoute(request)
	if err != nil {
		t.Fatalf("find OpenAPI route: %v", err)
	}
	input := &openapi3filter.RequestValidationInput{
		Request:    request,
		PathParams: pathParams,
		Route:      route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
	}
	if err := openapi3filter.ValidateRequest(request.Context(), input); err != nil {
		t.Fatalf("OpenAPI request validation failed: %v", err)
	}
	return input
}

func validateOpenAPIResponse(t *testing.T, requestInput *openapi3filter.RequestValidationInput, status int, header http.Header, body []byte) {
	t.Helper()
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: requestInput,
		Status:                 status,
		Header:                 header,
	}
	input.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
		t.Fatalf("OpenAPI response validation failed: %v", err)
	}
}

func assertHealthResponse(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if response.Status != "ok" {
		t.Errorf("status = %q, want ok", response.Status)
	}
}

func assertOpenAINotImplemented(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Error struct {
			Message string  `json:"message"`
			Type    string  `json:"type"`
			Code    *string `json:"code"`
		} `json:"error"`
		Status int `json:"status"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode OpenAI error response: %v", err)
	}
	if response.Status != http.StatusNotImplemented {
		t.Errorf("body status = %d, want %d", response.Status, http.StatusNotImplemented)
	}
	if response.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error", response.Error.Type)
	}
	if response.Error.Code == nil || *response.Error.Code != "NOT_IMPLEMENTED" {
		t.Errorf("error.code = %v, want NOT_IMPLEMENTED", response.Error.Code)
	}
	if response.Error.Message == "" {
		t.Error("error.message is empty")
	}
}

func assertAnthropicNotImplemented(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Type  string `json:"type"`
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode Anthropic error response: %v", err)
	}
	if response.Type != "error" {
		t.Errorf("type = %q, want error", response.Type)
	}
	if response.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error", response.Error.Type)
	}
	if response.Error.Message == "" {
		t.Error("error.message is empty")
	}
}
