package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
