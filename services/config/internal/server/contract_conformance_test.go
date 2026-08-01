package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/services/config/internal/adminauth"
	"github.com/tokenmp/v3/services/config/internal/repository"
	"gopkg.in/yaml.v3"
)

// contractSpec is the minimal OpenAPI shape parsed from
// packages/contracts/openapi/config/v1.yaml. Only the paths and their HTTP
// method keys are needed for route conformance — the contract is the single
// source of truth for the public v1 Config API surface.
type contractSpec struct {
	Paths map[string]map[string]any `yaml:"paths"`
}

// contractYAMLPath resolves the OpenAPI contract relative to this test file
// (services/config/internal/server -> repo root -> packages/contracts/...).
const contractYAMLPath = "../../../../packages/contracts/openapi/config/v1.yaml"

// httpMethods is the OpenAPI method set (lowercase). Path-level keys such as
// "parameters"/"summary"/"description" are excluded so only operations are
// treated as routes.
var httpMethods = map[string]string{
	"get":     http.MethodGet,
	"post":    http.MethodPost,
	"patch":   http.MethodPatch,
	"put":     http.MethodPut,
	"delete":  http.MethodDelete,
	"head":    http.MethodHead,
	"options": http.MethodOptions,
}

// loadContractOperations parses the OpenAPI YAML and returns the declared
// (method, path) operations. The contract uses {revisionId} while the chi
// router registers {id}; the normalization keeps them comparable. The test
// fails if the contract file cannot be parsed so the contract stays the source
// of truth (a stale hardcoded list would no longer silently drift).
func loadContractOperations(t *testing.T) []struct{ method, path string } {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(contractYAMLPath))
	if err != nil {
		t.Fatalf("read contract YAML %s: %v", contractYAMLPath, err)
	}
	var spec contractSpec
	if err := yaml.Unmarshal(b, &spec); err != nil {
		t.Fatalf("parse contract YAML: %v", err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("contract YAML declared no paths")
	}
	var ops []struct{ method, path string }
	for p, methods := range spec.Paths {
		// Normalize the contract path param name to the chi param name.
		chiPath := p
		chiPath = replaceAll(chiPath, "{revisionId}", "{id}")
		for m, upper := range httpMethods {
			if _, ok := methods[m]; ok {
				ops = append(ops, struct{ method, path string }{upper, chiPath})
			}
		}
	}
	return ops
}

// replaceAll is a tiny strings.ReplaceAll alias to avoid importing strings only
// for one call site.
func replaceAll(s, old, new string) string {
	out := ""
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			out += new
			i += len(old)
		} else {
			out += string(s[i])
			i++
		}
	}
	return out
}

// TestContractRouterConformance parses the OpenAPI contract and asserts every
// declared operation has a matching registered Config handler (forward
// direction: contract → router). Driven by the YAML so the test cannot drift
// from the contract.
func TestContractRouterConformance(t *testing.T) {
	s := newWriteServerWithAdmin(t, true)
	r := s.Router()
	routes := walkRoutes(t, r)
	for _, op := range loadContractOperations(t) {
		key := op.method + " " + op.path
		if _, ok := routes[key]; !ok {
			t.Errorf("contract path %q has no registered handler", key)
		}
	}
}

// TestNoUncontractedV1ConfigPaths asserts the Config router does not expose
// any public /v1/config/* path outside the contract allowlist. The admin CRUD
// routes (/v1/config/admin/*) and the models catalog are explicitly scoped
// out of the public contract (service-internal admin endpoints, admin-auth
// protected, proxied via Edge); they are listed in the allowlist so the test
// documents their scope rather than flagging a contract gap.
func TestNoUncontractedV1ConfigPaths(t *testing.T) {
	s := newWriteServerWithAdmin(t, true)
	r := s.Router()
	contractSet := map[string]bool{}
	for _, op := range loadContractOperations(t) {
		contractSet[op.method+" "+op.path] = true
	}
	// Admin CRUD + models catalog: explicitly out-of-contract, documented.
	allowedAdmin := map[string]bool{
		"GET /v1/config/admin/models":                      true,
		"POST /v1/config/admin/models":                     true,
		"GET /v1/config/admin/models/{id}":                 true,
		"PATCH /v1/config/admin/models/{id}":               true,
		"DELETE /v1/config/admin/models/{id}":              true,
		"GET /v1/config/admin/providers":                   true,
		"POST /v1/config/admin/providers":                  true,
		"GET /v1/config/admin/providers/{id}":              true,
		"PATCH /v1/config/admin/providers/{id}":            true,
		"DELETE /v1/config/admin/providers/{id}":           true,
		"GET /v1/config/admin/adapters":                    true,
		"POST /v1/config/admin/adapters":                   true,
		"GET /v1/config/admin/adapters/{id}":               true,
		"PATCH /v1/config/admin/adapters/{id}":             true,
		"DELETE /v1/config/admin/adapters/{id}":            true,
		"GET /v1/config/admin/providers/{id}/endpoints":    true,
		"POST /v1/config/admin/providers/{id}/endpoints":   true,
		"PATCH /v1/config/admin/endpoints/{eid}":           true,
		"DELETE /v1/config/admin/endpoints/{eid}":          true,
		"GET /v1/config/admin/providers/{id}/credentials":  true,
		"POST /v1/config/admin/providers/{id}/credentials": true,
		"PATCH /v1/config/admin/credentials/{cid}":         true,
		"DELETE /v1/config/admin/credentials/{cid}":        true,
		"GET /v1/config/admin/routes":                      true,
		"POST /v1/config/admin/routes":                     true,
		"GET /v1/config/admin/routes/{id}":                 true,
		"PATCH /v1/config/admin/routes/{id}":               true,
		"DELETE /v1/config/admin/routes/{id}":              true,
		"GET /v1/config/admin/routes/{id}/credentials":     true,
		"PUT /v1/config/admin/routes/{id}/credentials":     true,
		"POST /v1/config/admin/compile":                    true,
		"GET /v1/config/admin/global":                      true,
		"PUT /v1/config/admin/global/{key}":                true,
		"GET /v1/config/models/catalog":                    true,
	}
	routes := walkRoutes(t, r)
	for key := range routes {
		if contractSet[key] || allowedAdmin[key] {
			continue
		}
		t.Errorf("router exposes path not in contract or admin allowlist: %s", key)
	}
}

// TestRevertRouteRegistered ensures the contract /revert path (not /rollback)
// is wired. Regression guard for the rollback→revert rename.
func TestRevertRouteRegistered(t *testing.T) {
	s := newWriteServerWithAdmin(t, true)
	r := s.Router()
	routes := walkRoutes(t, r)
	if _, ok := routes["POST /v1/config/revisions/{id}/revert"]; !ok {
		t.Fatal("missing POST /v1/config/revisions/{id}/revert (contract uses /revert, not /rollback)")
	}
	if _, ok := routes["POST /v1/config/revisions/{id}/rollback"]; ok {
		t.Fatal("legacy /rollback route must not be registered; contract path is /revert")
	}
}

// TestContractYAMLSpecPathPresent is a sanity check that the parsed contract
// actually declares the expected revert operation, proving the YAML parse is
// not silently empty (e.g. wrong path or shape).
func TestContractYAMLSpecPathPresent(t *testing.T) {
	ops := loadContractOperations(t)
	var hasRevert bool
	for _, op := range ops {
		if op.method == http.MethodPost && op.path == "/v1/config/revisions/{id}/revert" {
			hasRevert = true
		}
	}
	if !hasRevert {
		t.Fatalf("contract YAML did not declare POST /v1/config/revisions/{id}/revert; parsed ops: %v", ops)
	}
}

// newWriteServerWithAdmin builds a dev-auth server with admin reader/writer
// wired so all routes (write + admin CRUD) register.
func newWriteServerWithAdmin(t *testing.T, devAuth bool) *Server {
	t.Helper()
	w := &fakeWriterMinimal{fakeWriter: *newFakeWriter()}
	var mw *adminauth.Middleware
	if devAuth {
		mw, _ = adminauth.New("", true)
	} else {
		f := tmpTokenFileForServer(t, "conftoken")
		mw, _ = adminauth.New(f, false)
	}
	s := NewWithAdminAuth(nil, w, fakePinger{}, nil, mw)
	s.adminReader = &fakeAdminReader{}
	s.adminWriter = &fakeAdminWriterCreds{}
	return s
}

// walkRoutes flattens a chi router (including groups) into method+pattern keys.
func walkRoutes(t *testing.T, h http.Handler) map[string]bool {
	t.Helper()
	out := make(map[string]bool)
	r, ok := h.(chi.Routes)
	if !ok {
		t.Fatalf("router does not implement chi.Routes")
	}
	walkFn := func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[method+" "+route] = true
		return nil
	}
	if err := chi.Walk(r, walkFn); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	return out
}

// keep repository import anchor (fake types live in compile_test.go).
var _ = repository.ErrNotFound
