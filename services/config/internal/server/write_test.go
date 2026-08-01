package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/config/internal/adminauth"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

// fakeWriterMinimal is a minimal Writer for write-path handler tests.
type fakeWriterMinimal struct {
	fakeWriter
}

func newWriteServer(t *testing.T, devAuth bool) *Server {
	t.Helper()
	w := &fakeWriterMinimal{fakeWriter: *newFakeWriter()}
	var mw *adminauth.Middleware
	var err error
	if devAuth {
		mw, err = adminauth.New("", true)
	} else {
		f := tmpTokenFileForServer(t, "writestoken")
		mw, err = adminauth.New(f, false)
	}
	if err != nil {
		t.Fatalf("adminauth.New: %v", err)
	}
	s := NewWithAdminAuth(nil, w, fakePinger{}, nil, mw)
	return s
}

func tmpTokenFileForServer(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := dir + "/admin.token"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return p
}

func TestWriteRoutes_RequireAdminAuth(t *testing.T) {
	f := tmpTokenFileForServer(t, "writestoken")
	mw, err := adminauth.New(f, false)
	if err != nil {
		t.Fatalf("adminauth.New: %v", err)
	}
	s := NewWithAdminAuth(nil, &fakeWriterMinimal{fakeWriter: *newFakeWriter()}, fakePinger{}, nil, mw)
	r := s.Router()
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/config/drafts"},
		{http.MethodGet, "/v1/config/revisions"},
		{http.MethodGet, "/v1/config/audit"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: status = %d, want 401 (auth required)", tc.method, tc.path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "writestoken") {
			t.Fatalf("%s %s: body leaked token: %s", tc.method, tc.path, body)
		}
	}
}

func TestWriteRoutes_FailClosedWhenAuthNotConfigured(t *testing.T) {
	// Server built without adminAuth middleware -> write routes fail 503.
	w := &fakeWriterMinimal{}
	s := New(nil, w, fakePinger{}, nil)
	r := s.Router()
	req := httptest.NewRequest(http.MethodGet, "/v1/config/revisions", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (fail-closed)", rec.Code)
	}
}

func TestReadRoutes_RemainAnonymous(t *testing.T) {
	// Build a production-mode server (real token file) so read routes stay
	// anonymous while write routes require auth.
	f := tmpTokenFileForServer(t, "readstoken")
	mw, err := adminauth.New(f, false)
	if err != nil {
		t.Fatalf("adminauth.New: %v", err)
	}
	s := NewWithAdminAuth(nil, &fakeWriterMinimal{fakeWriter: *newFakeWriter()}, fakePinger{}, nil, mw)
	r := s.Router()
	// snapshots/latest is anonymous read; with no published snapshot it 404s
	// (not 401).
	req := httptest.NewRequest(http.MethodGet, "/v1/config/snapshots/latest", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("read route must stay anonymous, got 401")
	}
	// healthz anonymous.
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
}

func TestCreateDraft_ValidationAndAudit(t *testing.T) {
	s := newWriteServer(t, true)
	r := s.Router()
	// Missing revision -> 400.
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// Valid create -> 201.
	body := `{"revision":"h1","created_by":"alice","snapshot_json":{"x":1}}`
	req = httptest.NewRequest(http.MethodPost, "/v1/config/drafts", strings.NewReader(body))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateDraft_CASPreconditionFailed(t *testing.T) {
	s := newWriteServer(t, true)
	r := s.Router()
	// Create draft.
	body := `{"revision":"cas1","snapshot_json":{"a":1}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var res struct {
		Data struct {
			ID      int64 `json:"id"`
			Version int   `json:"version"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	id := res.Data.ID
	// Patch with wrong If-Match -> 412.
	patch := httptest.NewRequest(http.MethodPatch, "/v1/config/drafts/"+strconv.FormatInt(id, 10), strings.NewReader(`{"y":2}`))
	patch.Header.Set("If-Match", "999")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, patch)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412", rec.Code)
	}
	// Correct If-Match -> 200 + ETag.
	patch = httptest.NewRequest(http.MethodPatch, "/v1/config/drafts/"+strconv.FormatInt(id, 10), strings.NewReader(`{"y":2}`))
	patch.Header.Set("If-Match", strconv.Itoa(res.Data.Version))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, patch)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Fatalf("expected ETag header")
	}
}

func TestSecretBoundary_RejectsPlaintextApiKey(t *testing.T) {
	// Even in dev-auth mode, credential create must reject plaintext api_key.
	// This test exercises the handler-level boundary independent of the DB.
	devAuth, _ := adminauth.New("", true)
	faw := &fakeAdminWriterCreds{}
	s := NewWithAdminAuth(nil, &fakeWriterMinimal{fakeWriter: *newFakeWriter()}, fakePinger{}, nil, devAuth)
	s.adminReader = &fakeAdminReader{}
	s.adminWriter = faw
	r := s.Router()
	body := `{"id":"c1","api_key":"sk-secret"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers/p1/credentials", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (plaintext rejected); body=%s", rec.Code, rec.Body.String())
	}
	if faw.created {
		t.Fatalf("credential must not be created when api_key present")
	}
}

func TestSecretBoundary_AcceptsVaultRef(t *testing.T) {
	devAuth, _ := adminauth.New("", true)
	faw := &fakeAdminWriterCreds{}
	s := NewWithAdminAuth(nil, &fakeWriterMinimal{fakeWriter: *newFakeWriter()}, fakePinger{}, nil, devAuth)
	s.adminReader = &fakeAdminReader{}
	s.adminWriter = faw
	r := s.Router()
	body := `{"id":"c2","credential_ref":"vault://p1/credential/c2"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers/p1/credentials", strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	// Response must not include api_key.
	if strings.Contains(rec.Body.String(), "api_key") {
		t.Fatalf("response leaked api_key: %s", rec.Body.String())
	}
}

// fakeAdminWriterCreds only implements the credential write path.
type fakeAdminWriterCreds struct {
	created bool
	last    repository.UpstreamCredential
}

func (f *fakeAdminWriterCreds) CreateProvider(_ context.Context, _ *repository.Provider) error {
	return nil
}
func (f *fakeAdminWriterCreds) UpdateProvider(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (f *fakeAdminWriterCreds) DeleteProvider(_ context.Context, _ string) error         { return nil }
func (f *fakeAdminWriterCreds) CreateModel(_ context.Context, _ *repository.Model) error { return nil }
func (f *fakeAdminWriterCreds) UpdateModel(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (f *fakeAdminWriterCreds) DeleteModel(_ context.Context, _ string) error { return nil }
func (f *fakeAdminWriterCreds) CreateAdapter(_ context.Context, _ *repository.Adapter) error {
	return nil
}
func (f *fakeAdminWriterCreds) UpdateAdapter(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (f *fakeAdminWriterCreds) DeleteAdapter(_ context.Context, _ string) error { return nil }
func (f *fakeAdminWriterCreds) CreateEndpoint(_ context.Context, _ *repository.UpstreamEndpoint) error {
	return nil
}
func (f *fakeAdminWriterCreds) UpdateEndpoint(_ context.Context, _ int64, _ map[string]any) error {
	return nil
}
func (f *fakeAdminWriterCreds) DeleteEndpoint(_ context.Context, _ int64) error { return nil }
func (f *fakeAdminWriterCreds) CreateCredential(_ context.Context, c *repository.UpstreamCredential) error {
	if c.APIKey != nil && *c.APIKey != "" {
		return errors.New("plaintext rejected")
	}
	f.created = true
	f.last = *c
	return nil
}
func (f *fakeAdminWriterCreds) UpdateCredential(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (f *fakeAdminWriterCreds) DeleteCredential(_ context.Context, _ string) error { return nil }
func (f *fakeAdminWriterCreds) CreateRoute(_ context.Context, _ *repository.RouteMapping) error {
	return nil
}
func (f *fakeAdminWriterCreds) UpdateRoute(_ context.Context, _ string, _ map[string]any) error {
	return nil
}
func (f *fakeAdminWriterCreds) DeleteRoute(_ context.Context, _ string) error { return nil }
func (f *fakeAdminWriterCreds) SetRouteCredentials(_ context.Context, _ string, _ []repository.RouteCredential) error {
	return nil
}
func (f *fakeAdminWriterCreds) SetGlobalConfigEntry(_ context.Context, _ string, _ []byte, _ string) error {
	return nil
}

func itoa(i int64) string {
	return strconv.FormatInt(i, 10)
}
