package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/config/internal/repository"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

type fakeReader struct {
	snap repository.Snapshot
	err  error
}

func (f *fakeReader) LatestPublished(ctx context.Context) (repository.Snapshot, error) {
	return f.snap, f.err
}

func newServer(r repository.Reader, p fakePinger) *Server {
	return New(r, p, nil)
}

func TestServer_Healthz(t *testing.T) {
	s := newServer(&fakeReader{}, fakePinger{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
}

func TestServer_Readyz_OK(t *testing.T) {
	s := newServer(&fakeReader{}, fakePinger{})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", rec.Code)
	}
}

func TestServer_Readyz_NotReady(t *testing.T) {
	s := newServer(&fakeReader{}, fakePinger{err: errors.New("down")})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503", rec.Code)
	}
}

func TestServer_LatestSnapshot_OK(t *testing.T) {
	snap := repository.Snapshot{
		Revision:     "2026-07-24-01",
		SnapshotJSON: []byte(`{"revision":"2026-07-24-01"}`),
		SHA256:       "abc123",
		CreatedAt:    time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
	}
	s := newServer(&fakeReader{snap: snap}, fakePinger{})
	req := httptest.NewRequest(http.MethodGet, "/v1/config/snapshots/latest", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Config-Revision"); got != snap.Revision {
		t.Errorf("X-Config-Revision = %q, want %q", got, snap.Revision)
	}
	if got := rec.Header().Get("X-Config-SHA256"); got != snap.SHA256 {
		t.Errorf("X-Config-SHA256 = %q, want %q", got, snap.SHA256)
	}
	var out repository.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Revision != snap.Revision {
		t.Errorf("body revision = %q, want %q", out.Revision, snap.Revision)
	}
}

func TestServer_LatestSnapshot_NotFound(t *testing.T) {
	s := newServer(&fakeReader{err: repository.ErrNotFound}, fakePinger{})
	req := httptest.NewRequest(http.MethodGet, "/v1/config/snapshots/latest", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServer_LatestSnapshot_QueryError(t *testing.T) {
	s := newServer(&fakeReader{err: repository.ErrQueryFailed}, fakePinger{})
	req := httptest.NewRequest(http.MethodGet, "/v1/config/snapshots/latest", nil)
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The error body must not leak any DSN/driver detail, only the stable code.
	if b := rec.Body.String(); containsLeak(b) {
		t.Errorf("response body leaked detail: %s", b)
	}
}

func containsLeak(b string) bool {
	for _, frag := range []string{"password", "postgres://", "dsn", "pq:"} {
		if contains(b, frag) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
