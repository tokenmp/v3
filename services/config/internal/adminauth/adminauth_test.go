package adminauth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func tmpTokenFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "admin.token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return p
}

func TestNew_RequiresFileInProduction(t *testing.T) {
	_, err := New("", false)
	if err != ErrTokenFileRequired {
		t.Fatalf("expected ErrTokenFileRequired, got %v", err)
	}
}

func TestNew_LoadsSecretFromFile(t *testing.T) {
	p := tmpTokenFile(t, "supersecret\n")
	mw, err := New(p, false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !mw.Configured() {
		t.Fatalf("expected configured")
	}
}

func TestNew_EmptyFileFails(t *testing.T) {
	p := tmpTokenFile(t, "   \n")
	_, err := New(p, false)
	if err != ErrTokenLoadFailed {
		t.Fatalf("expected ErrTokenLoadFailed, got %v", err)
	}
}

func TestNew_DevNoAuth(t *testing.T) {
	mw, err := New("", true)
	if err != nil {
		t.Fatalf("New dev: %v", err)
	}
	if mw.Configured() {
		t.Fatalf("dev mode must not enforce")
	}
}

func TestMiddleware_RejectsMissingToken(t *testing.T) {
	p := tmpTokenFile(t, "goodtoken")
	mw, _ := New(p, false)
	called := false
	h := mw.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatalf("handler must not be called without token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if body := rec.Body.String(); contains(body, "goodtoken") {
		t.Fatalf("response leaked token: %s", body)
	}
}

func TestMiddleware_RejectsWrongToken(t *testing.T) {
	p := tmpTokenFile(t, "goodtoken")
	mw, _ := New(p, false)
	called := false
	h := mw.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", nil)
	req.Header.Set("X-Admin-Token", "badtoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatalf("handler must not be called with wrong token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMiddleware_AcceptsCorrectToken(t *testing.T) {
	p := tmpTokenFile(t, "goodtoken")
	mw, _ := New(p, false)
	called := false
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	for _, setter := range []func(req *http.Request){
		func(req *http.Request) { req.Header.Set("X-Admin-Token", "goodtoken") },
		func(req *http.Request) { req.Header.Set("Authorization", "Bearer goodtoken") },
	} {
		called = false
		req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", nil)
		setter(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !called {
			t.Fatalf("handler must be called with correct token")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	}
}

func TestMiddleware_DevNoAuthPassthrough(t *testing.T) {
	mw, _ := New("", true)
	called := false
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/config/drafts", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called {
		t.Fatalf("dev mode must pass through")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
