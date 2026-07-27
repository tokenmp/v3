package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/settings"
)

func TestProxyForwardsRequestWithToken(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	p, err := New(backend.URL, "edge-token", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", nil)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()

	if gotAuth != "Bearer edge-token" {
		t.Errorf("Authorization = %q, want 'Bearer edge-token'", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("Path = %q", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("Method = %q", gotMethod)
	}
}

func TestProxyErrorOnUnreachable(t *testing.T) {
	// Point at an unreachable port.
	p, err := New("http://127.0.0.1:1", "tok", nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", nil)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !contains(string(body), "upstream_unavailable") {
		t.Errorf("body = %q, want upstream_unavailable", string(body))
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

// TestProxyInjectsAutoModelIDsHeader verifies the per-user auto model pool is
// injected as X-Auto-Model-IDs when the request carries a verified identity.
func TestProxyInjectsAutoModelIDsHeader(t *testing.T) {
	var got string
	var hasHdr bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Auto-Model-IDs")
		hasHdr = got != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	st := settings.NewStore()
	st.Snapshot("user-1", nil, nil, []string{"glm-5.1", "glm-5.2"})

	prx, err := NewWithSettings(backend.URL, "edge-svc-token", st, nil)
	if err != nil {
		t.Fatalf("NewWithSettings: %v", err)
	}
	ts := httptest.NewServer(prx)
	defer ts.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req = req.WithContext(identity.WithClaims(context.Background(), identity.Claims{Subject: "user-1"}))
	prx.ServeHTTP(httptest.NewRecorder(), req)

	if !hasHdr {
		t.Fatal("X-Auto-Model-IDs header not forwarded")
	}
	if got != "glm-5.1,glm-5.2" {
		t.Fatalf("header = %q, want %q", got, "glm-5.1,glm-5.2")
	}
}

// TestProxyStripsClientSuppliedAutoModelIDs verifies a client-supplied
// X-Auto-Model-IDs header is stripped (no spoofing in passthrough mode).
func TestProxyStripsClientSuppliedAutoModelIDs(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Auto-Model-IDs")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	// No settings store: nothing injected, client value must be stripped.
	prx, _ := NewWithSettings(backend.URL, "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("X-Auto-Model-IDs", "spoofed")
	req = req.WithContext(identity.WithClaims(context.Background(), identity.Claims{Subject: "user-1"}))
	prx.ServeHTTP(httptest.NewRecorder(), req)
	if got != "" {
		t.Fatalf("client X-Auto-Model-IDs leaked: %q", got)
	}
}
