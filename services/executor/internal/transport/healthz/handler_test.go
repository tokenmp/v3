package healthz

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler(t *testing.T) {
	t.Parallel()

	handler := NewHandler()
	tests := []struct {
		name        string
		method      string
		path        string
		status      int
		body        string
		allow       string
		contentType string
		cache       string
	}{
		{"get", http.MethodGet, "/healthz", http.StatusOK, "{\"status\":\"ok\"}\n", "", "application/json", "no-store"},
		{"head", http.MethodHead, "/healthz", http.StatusOK, "", "", "application/json", "no-store"},
		{"method not allowed", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "", "GET, HEAD", "", ""},
		{"unknown path", http.MethodGet, "/unknown", http.StatusNotFound, "404 page not found\n", "", "text/plain; charset=utf-8", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, nil))
			response := recorder.Result()
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Errorf("status = %d, want %d", response.StatusCode, test.status)
			}
			if recorder.Body.String() != test.body {
				t.Errorf("body = %q, want %q", recorder.Body.String(), test.body)
			}
			if got := response.Header.Get("Allow"); got != test.allow {
				t.Errorf("Allow = %q, want %q", got, test.allow)
			}
			if got := response.Header.Get("Content-Type"); got != test.contentType {
				t.Errorf("Content-Type = %q, want %q", got, test.contentType)
			}
			if got := response.Header.Get("Cache-Control"); got != test.cache {
				t.Errorf("Cache-Control = %q, want %q", got, test.cache)
			}
		})
	}
}
