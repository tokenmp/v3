package composition

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// This file exercises the fully wrapped runtime handler produced by Build —
// i.e. AuthMiddleware(identity)(CaptureRawBody(generated)) — against every
// operation declared in the Executor OpenAPI contract. It deliberately does
// NOT rely on chi.Walk: the outer wrappers returned by Build are plain
// http.Handler values and are not guaranteed to implement chi.Routes, so route
// discovery must come from the contract, not from introspecting the handler.
//
// For each contract operation the test dispatches both an anonymous and an
// authenticated request and asserts the runtime status that operation must
// produce under an empty (no business routes) compiled config:
//
//   - GET/HEAD /healthz are anonymous 200 (auth is never required).
//   - Every /v1 operation is auth-protected: anonymous → 401.
//   - /v1/models and /v1/responses return 501 when authenticated.
//   - /v1/chat/completions, /v1/messages and /v1/images/generations execute
//     through the facade; with
//     an empty config the requested model resolves no route → 404.
//
// If a route is added to the contract, the expectation table must be updated
// or this test fails closed, keeping the runtime behavioral contract in sync
// with the OpenAPI surface.

// routeExpectation describes the required runtime behavior for one contract
// operation under an empty compiled config.
type routeExpectation struct {
	// anonStatus is the status for an unauthenticated request. /healthz is
	// anonymous; every /v1 path is auth-protected and must fail 401.
	anonStatus int
	// authStatus is the status for an authenticated request.
	authStatus int
	// body is the request body for POST operations. It is re-read for each
	// request via a fresh reader so the same expectation can be dispatched
	// twice without consuming a shared io.Reader.
	body string
	// note is a short human-readable description asserted in failure output.
	note string
}

// contractExpectations maps "METHOD /path" (method upper-cased) to the
// required runtime behavior. Every operation in the OpenAPI contract MUST
// appear here; an operation present in the contract but absent from this map
// fails the test, and vice versa.
var contractExpectations = map[string]routeExpectation{
	"GET /healthz": {
		anonStatus: http.StatusOK,
		authStatus: http.StatusOK,
		note:       "anonymous health; auth never required",
	},
	"HEAD /healthz": {
		anonStatus: http.StatusOK,
		authStatus: http.StatusOK,
		note:       "anonymous head health; no response body",
	},
	"GET /v1/models": {
		anonStatus: http.StatusUnauthorized,
		authStatus: http.StatusOK,
		note:       "auth-protected; returns empty model list for empty config",
	},
	"POST /v1/chat/completions": {
		anonStatus: http.StatusUnauthorized,
		authStatus: http.StatusNotFound,
		body:       `{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":true}`,
		note:       "auth-protected; stream request pre-commit resolves no model → JSON 404",
	},
	"POST /v1/messages": {
		anonStatus: http.StatusUnauthorized,
		authStatus: http.StatusNotFound,
		body:       `{"model":"missing","max_tokens":1,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
		note:       "auth-protected; stream request pre-commit resolves no model → JSON 404",
	},
	"POST /v1/responses": {
		anonStatus: http.StatusUnauthorized,
		authStatus: http.StatusNotImplemented,
		body:       `{"model":"x","input":"hi"}`,
		note:       "auth-protected; not executed by runtime",
	},
	"POST /v1/images/generations": {
		anonStatus: http.StatusUnauthorized,
		authStatus: http.StatusNotFound,
		body:       `{"model":"missing-image","prompt":"hi"}`,
		note:       "auth-protected; image request resolves no model → JSON 404",
	},
}

// repoRootFromCaller walks up from this test file's location until it finds a
// directory containing the Executor contract, so route discovery does not
// depend on the process working directory.
func repoRootFromCaller(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	marker := filepath.Join("packages", "contracts", "openapi", "executor", "v1.yaml")
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root (missing %s) from %s", marker, thisFile)
	return ""
}

func loadExecutorContract(t *testing.T) *openapi3.T {
	t.Helper()
	root := repoRootFromCaller(t)
	contractPath := filepath.Join(root, "packages", "contracts", "openapi", "executor", "v1.yaml")
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(contractPath)
	if err != nil {
		t.Fatalf("load OpenAPI contract %s: %v", contractPath, err)
	}
	ctx := context.Background()
	if err := doc.Validate(ctx, openapi3.AllowExtraSiblingFields("description", "nullable")); err != nil {
		t.Fatalf("OpenAPI contract validation failed: %v", err)
	}
	return doc
}

// contractOperations returns the sorted set of "METHOD /path" keys declared by
// the OpenAPI contract.
func contractOperations(t *testing.T, doc *openapi3.T) []string {
	t.Helper()
	if doc.Paths == nil {
		t.Fatal("contract has no paths")
	}
	var ops []string
	for _, pathStr := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Find(pathStr)
		if item == nil {
			continue
		}
		for method := range item.Operations() {
			ops = append(ops, strings.ToUpper(method)+" "+pathStr)
		}
	}
	if len(ops) == 0 {
		t.Fatal("contract declares zero operations")
	}
	sort.Strings(ops)
	return ops
}

// buildWrappedHandler builds the fully wrapped runtime handler over an empty
// config with a single test identity, exactly as runtime main does.
func buildWrappedHandler(t *testing.T) http.Handler {
	t.Helper()
	path := writeConfig(t, minimalEmptyConfig)
	cfg := testConfig(path, "{}")
	handler, err := Build(context.Background(), cfg, envLookup(healthyEnv(t, path)))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if handler == nil {
		t.Fatal("Build() returned nil handler")
	}
	return handler
}

// dispatch sends one request through the wrapped handler. body is re-read via a
// fresh reader each call so expectations can be dispatched repeatedly without
// draining a shared reader.
func dispatch(t *testing.T, handler http.Handler, method, path, body, auth string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:8081"+path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestRuntimeRouteConformanceAgainstContract enumerates every OpenAPI contract
// operation and asserts the fully wrapped runtime handler produces the correct
// anonymous and authenticated status for each, under an empty compiled config.
// It does not use chi.Walk.
func TestRuntimeRouteConformanceAgainstContract(t *testing.T) {
	t.Parallel()

	doc := loadExecutorContract(t)
	ops := contractOperations(t, doc)
	handler := buildWrappedHandler(t)

	// 1. Every contract operation must have an expectation. A contract change
	//    without a table update fails closed.
	var missingExpectation []string
	for _, op := range ops {
		if _, ok := contractExpectations[op]; !ok {
			missingExpectation = append(missingExpectation, op)
		}
	}
	if len(missingExpectation) > 0 {
		t.Fatalf("contract operations without a runtime expectation (update contractExpectations):\n  %s",
			strings.Join(missingExpectation, "\n  "))
	}

	// 2. No expectation should outlive its contract operation.
	var extraExpectation []string
	for op := range contractExpectations {
		found := false
		for _, c := range ops {
			if c == op {
				found = true
				break
			}
		}
		if !found {
			extraExpectation = append(extraExpectation, op)
		}
	}
	sort.Strings(extraExpectation)
	if len(extraExpectation) > 0 {
		t.Fatalf("expectations without a contract operation (stale table):\n  %s",
			strings.Join(extraExpectation, "\n  "))
	}

	// 3. Dispatch anonymous + authenticated requests for every operation.
	for _, op := range ops {
		op := op
		exp := contractExpectations[op]
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			parts := strings.SplitN(op, " ", 2)
			method, path := parts[0], parts[1]

			anonRec := dispatch(t, handler, method, path, exp.body, "")
			if anonRec.Code != exp.anonStatus {
				t.Errorf("anonymous %s status = %d, want %d; body=%q (%s)",
					op, anonRec.Code, exp.anonStatus, anonRec.Body.String(), exp.note)
			}
			// Every /v1 path must be auth-protected: a 401 must carry a
			// protocol-native error body, never a routing or credential leak.
			if strings.HasPrefix(path, "/v1") && exp.anonStatus == http.StatusUnauthorized {
				if !strings.Contains(anonRec.Body.String(), "authentication_error") &&
					!strings.Contains(anonRec.Body.String(), `"type":"error"`) {
					t.Errorf("anonymous %s body = %q, want protocol-native auth error", op, anonRec.Body.String())
				}
			}
			// /healthz must stay anonymous: a bearer must not change its status.
			if path == "/healthz" {
				if anonRec.Header().Get("Cache-Control") != "no-store" {
					t.Errorf("anonymous %s Cache-Control = %q, want no-store", op, anonRec.Header().Get("Cache-Control"))
				}
			}

			authRec := dispatch(t, handler, method, path, exp.body, "Bearer "+testAPIKey)
			if authRec.Code != exp.authStatus {
				t.Errorf("authenticated %s status = %d, want %d; body=%q (%s)",
					op, authRec.Code, exp.authStatus, authRec.Body.String(), exp.note)
			}
			// HEAD must never write a body, anonymous or authenticated.
			if method == http.MethodHead && authRec.Body.Len() != 0 {
				t.Errorf("authenticated HEAD %s body = %q, want empty", path, authRec.Body.String())
			}
		})
	}
}

// TestRuntimeUnknownV1PathIsAuthProtected asserts the middleware protects
// unknown /v1 paths (which become 404 downstream) and never serves them
// anonymously. This covers paths outside the contract surface.
func TestRuntimeUnknownV1PathIsAuthProtected(t *testing.T) {
	t.Parallel()
	handler := buildWrappedHandler(t)

	t.Run("anonymous unknown v1 path is 401", func(t *testing.T) {
		t.Parallel()
		rec := dispatch(t, handler, http.MethodPost, "/v1/does-not-exist", `{"x":1}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%q", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "authentication_error") {
			t.Fatalf("body = %q, want OpenAI-native authentication_error", rec.Body.String())
		}
	})

	t.Run("authenticated unknown v1 path is 404", func(t *testing.T) {
		t.Parallel()
		rec := dispatch(t, handler, http.MethodPost, "/v1/does-not-exist", `{"x":1}`, "Bearer "+testAPIKey)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown non-v1 path is not auth-gated", func(t *testing.T) {
		t.Parallel()
		// Paths outside /v1 and /healthz are passed through by AuthMiddleware;
		// the generated Chi router returns 404 for them without auth.
		rec := dispatch(t, handler, http.MethodGet, "/unknown", "", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%q", rec.Code, rec.Body.String())
		}
	})
}
