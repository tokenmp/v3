package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/config/internal/adminauth"
	"github.com/tokenmp/v3/services/config/internal/repository"
)

// errWriter is a fakeWriter whose admin write methods return a configurable
// error. It embeds fakeAdminReader so NewWithAdminAuth wires admin routes.
// It is used to exercise error-classification → HTTP-status mapping.
type errWriter struct {
	fakeWriter
	fakeAdminReader
	createProviderErr error
	updateProviderErr error
}

func (w *errWriter) CreateProvider(_ context.Context, p *repository.Provider) error {
	if w.createProviderErr != nil {
		return w.createProviderErr
	}
	return nil
}

func (w *errWriter) UpdateProvider(_ context.Context, _ string, _ map[string]any) error {
	if w.updateProviderErr != nil {
		return w.updateProviderErr
	}
	return nil
}

// newAdminTestServer builds a dev-auth server (no token required) wired to
// the given writer so we can inject errors.
func newAdminTestServer(t *testing.T, w repository.Writer) *Server {
	t.Helper()
	mw, err := adminauth.New("", true) // dev mode: no auth
	if err != nil {
		t.Fatalf("adminauth.New: %v", err)
	}
	return NewWithAdminAuth(nil, w, fakePinger{}, nil, mw)
}

// ---- Gap 1: classifyWriteErr → HTTP status mapping ----

func TestCreateProvider_ConflictMaps409(t *testing.T) {
	w := &errWriter{createProviderErr: repository.ErrConflict}
	s := newAdminTestServer(t, w)
	body := `{"id":"p1","name":"P1","base_url":"http://x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProvider_InvalidInputMaps400(t *testing.T) {
	w := &errWriter{createProviderErr: repository.ErrInvalidInput}
	s := newAdminTestServer(t, w)
	body := `{"id":"p1","name":"P1","base_url":"http://x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProvider_InternalMaps500(t *testing.T) {
	w := &errWriter{createProviderErr: repository.ErrInsertFailed}
	s := newAdminTestServer(t, w)
	body := `{"id":"p1","name":"P1","base_url":"http://x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateProvider_SuccessMaps201(t *testing.T) {
	w := &errWriter{}
	s := newAdminTestServer(t, w)
	body := `{"id":"p1","name":"P1","base_url":"http://x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}

// ---- Gap 1: missing required fields → 400 ----

func TestCreateProvider_MissingFields400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	cases := []string{
		`{}`,                                    // missing all
		`{"id":"p1"}`,                           // missing name, base_url
		`{"name":"P1"}`,                         // missing id, base_url
		`{"base_url":"http://x"}`,               // missing id, name
		`{"id":"p1","name":"P1"}`,               // missing base_url
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: status = %d, want 400", body, rec.Code)
		}
	}
}

// ---- Gap 2: PATCH field allowlist ----

func TestPatchProvider_RejectsUnknownField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"nonexistent_col":"x","name":"Updated"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/providers/p1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unknown field); body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchProvider_RejectsImmutableIdField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"id":"changed"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/providers/p1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (immutable id); body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchProvider_RejectsTimestampField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"updated_at":"2024-01-01T00:00:00Z"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/providers/p1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (immutable updated_at); body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchProvider_AcceptsValidFields200(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"name":"Updated","display_label":"New Label","status":"disabled"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/providers/p1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchModel_RejectsUnknownField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"evil_col":123}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/models/m1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchRoute_RejectsUnknownField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"bogus":"x"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/routes/r1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchCredential_RejectsApiKey400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"api_key":"sk-secret"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/credentials/c1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (api_key rejected); body=%s", rec.Code, rec.Body.String())
	}
	// Must not leak the secret in the body.
	if strings.Contains(rec.Body.String(), "sk-secret") {
		t.Fatalf("response leaked secret: %s", rec.Body.String())
	}
}

func TestPatchCredential_RejectsUnknownField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"unknown_col":"x"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/credentials/c1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchEndpoint_RejectsUnknownField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"evil":1}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/endpoints/1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPatchAdapter_RejectsUnknownField400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	body := `{"hack":"x"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/config/admin/adapters/a1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// ---- Gap 2: filterFields unit tests ----

func TestFilterFields_AllAllowed(t *testing.T) {
	in := map[string]any{"name": "x", "status": "active"}
	out, ok := filterFields(in, providerPatchFields)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(out))
	}
}

func TestFilterFields_RejectsUnknown(t *testing.T) {
	in := map[string]any{"name": "x", "evil": 1}
	out, ok := filterFields(in, providerPatchFields)
	if ok {
		t.Fatal("expected ok=false for unknown field")
	}
	if out != nil {
		t.Fatal("expected nil output on rejection")
	}
}

func TestFilterFields_EmptyMap(t *testing.T) {
	out, ok := filterFields(map[string]any{}, providerPatchFields)
	if !ok {
		t.Fatal("expected ok=true for empty map")
	}
	if len(out) != 0 {
		t.Fatalf("expected 0 fields, got %d", len(out))
	}
}

// ---- Body validation: invalid JSON ----

func TestCreateProvider_InvalidJSON400(t *testing.T) {
	s := newAdminTestServer(t, &errWriter{})
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(`{bad json`))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Verify the response never leaks internal error details.
func TestCreateProvider_ErrorDoesNotLeakDriverMessage(t *testing.T) {
	w := &errWriter{createProviderErr: repository.ErrConflict}
	s := newAdminTestServer(t, w)
	body := `{"id":"p1","name":"P1","base_url":"http://x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/config/admin/providers", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	// The body must contain the safe "conflict" message, not SQL details.
	var resp struct {
		Error string `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if strings.Contains(strings.ToLower(resp.Error), "sqlstate") || strings.Contains(strings.ToLower(resp.Error), "constraint") {
		t.Fatalf("response leaked driver details: %s", resp.Error)
	}
}
