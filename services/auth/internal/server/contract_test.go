// Package server contains a contract conformance test that verifies the
// Auth service's actual Chi routes match the OpenAPI contract at
// packages/contracts/openapi/auth/v1.yaml.
//
// This test is the machine-verifiable link between the Auth implementation
// and the @tokenmp/contracts package. It loads the single authoritative
// contract at test time (never at runtime) and fails the build if the
// implementation drifts from the contract or vice versa.
//
// No database connection is required: the server is constructed with a fake
// Pinger, JWT verifier and UserStore so all routes resolve without a real
// backend. The contract YAML is located via runtime.Caller (not cwd) so the
// test works regardless of the working directory.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/transport/authv1api"
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
	// thisFile: .../v3/services/auth/internal/server/contract_test.go
	// We need to go up 5 directories to reach the repo root "v3/".
	dir := thisFile
	for i := 0; i < 5; i++ {
		// Find last separator.
		idx := strings.LastIndexByte(dir, '/')
		if idx < 0 {
			t.Fatalf("cannot ascend from %q", dir)
		}
		dir = dir[:idx]
	}
	return dir
}

// loadContract reads and validates the OpenAPI contract from the repo.
func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	root := repoRoot(t)
	contractPath := root + "/packages/contracts/openapi/auth/v1.yaml"

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(contractPath)
	if err != nil {
		t.Fatalf("failed to load OpenAPI contract from %s: %v", contractPath, err)
	}
	// Validate the document structure.
	ctx := context.Background()
	if err := doc.Validate(ctx); err != nil {
		t.Fatalf("OpenAPI contract validation failed: %v", err)
	}
	return doc
}

// contractRoutes extracts all (method, path) pairs declared in the OpenAPI
// contract. Methods are upper-cased HTTP verbs; paths are the OpenAPI path
// strings (e.g. "/api/v1/auth/register").
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
// Fake dependencies (no database)
// ---------------------------------------------------------------------------

type contractPinger struct{}

func (contractPinger) Ping(_ context.Context) error { return nil }

// contractUserStore implements authv1api.UserStore for the bearerMiddleware.
type contractUserStore struct{}

func (contractUserStore) FindByID(_ context.Context, id string) (string, int, string, error) {
	return "active", 1, "user", nil
}

// contractUserRepo implements auth.UserRepository for the auth.Service.
type contractUserRepo struct{}

func (contractUserRepo) Create(_ context.Context, _ *models.User) error {
	return repository.ErrDuplicateEmail
}
func (contractUserRepo) FindByEmail(_ context.Context, _ string) (*models.User, error) {
	return nil, repository.ErrNotFound
}
func (contractUserRepo) FindByID(_ context.Context, _ string) (*models.User, error) {
	return &models.User{ID: "fake", Status: models.StatusActive, Role: models.RoleUser, TokenVersion: 1}, nil
}
func (contractUserRepo) UpdatePasswordHash(_ context.Context, _, _ string) error { return nil }
func (contractUserRepo) IncrementTokenVersion(_ context.Context, _ string) (int, error) {
	return 2, nil
}

// contractSessionRepo implements auth.SessionRepository.
type contractSessionRepo struct{}

func (contractSessionRepo) Create(_ context.Context, _ *models.AuthSession) error { return nil }
func (contractSessionRepo) FindByRefreshHashForUpdate(_ context.Context, _ []byte) (*models.AuthSession, error) {
	return nil, repository.ErrNotFound
}
func (contractSessionRepo) Revoke(_ context.Context, _, _ string, _ time.Time) error { return nil }
func (contractSessionRepo) RevokeActiveByFamily(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}
func (contractSessionRepo) RevokeActiveByUser(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}
func (contractSessionRepo) SetReplacedBy(_ context.Context, _, _ string) error { return nil }
func (contractSessionRepo) FindByID(_ context.Context, _ string) (*models.AuthSession, error) {
	return nil, repository.ErrNotFound
}

// contractTxRunner runs fn directly.
type contractTxRunner struct{}

func (contractTxRunner) Run(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}

// contractClock returns a fixed time.
type contractClock struct{}

func (contractClock) Now() time.Time { return time.Now().UTC() }

// buildServerWithFakes constructs a Server with all fake dependencies so
// every route is registered without a database.
func buildServerWithFakes(t *testing.T) *Server {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519 keygen: %v", err)
	}
	kp := &jwt.KeyPair{Private: priv, Public: pub}

	issuer, err := jwt.NewIssuer(kp, "tokenmp-auth", "tokenmp-web", 900_000_000_000)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := jwt.NewVerifier(kp, "tokenmp-auth", "tokenmp-web")
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	svc := auth.NewService(
		contractUserRepo{},
		contractSessionRepo{},
		contractTxRunner{},
		issuer,
		contractClock{},
		15*1e9,
		30*24*3600*1e9,
	)

	userStore := authv1api.NewUserRepoAdapter(contractUserRepo{})
	return New("127.0.0.1:0", contractPinger{}, verifier, svc, userStore)
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
	// 1. Load and validate the OpenAPI contract.
	doc := loadContract(t)
	contractSet := contractRoutes(t, doc)

	// 2. Build the server with fake deps and extract actual routes.
	srv := buildServerWithFakes(t)
	actualSet := actualRoutes(t, srv.Router())

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
		t.Errorf("routes in contract but NOT in implementation:\n  %s", strings.Join(missingFromImpl, "\n  "))
	}
	if len(missingFromContract) > 0 {
		t.Errorf("routes in implementation but NOT in contract:\n  %s", strings.Join(missingFromContract, "\n  "))
	}

	if len(missingFromImpl) > 0 || len(missingFromContract) > 0 {
		t.Fatalf("contract conformance check failed: %d missing from impl, %d missing from contract",
			len(missingFromImpl), len(missingFromContract))
	}

	t.Logf("contract conformance OK: %d routes match", len(contractSet))
}
