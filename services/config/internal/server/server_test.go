package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	return New(r, nil, p, nil)
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
	// The body MUST be the raw ConfigSnapshot JSON exactly as stored — not a
	// {revision,snapshot,sha256,...} wrapper. Revision/sha256 travel in the
	// X-Config-* response headers (the authoritative wire contract).
	rawSnapshot := []byte(`{"revision":"2026-07-24-01","createdAt":"2026-07-24T01:00:00Z","models":{},"providers":{},"routes":[],"adapters":{}}`)
	snap := repository.Snapshot{
		Revision:     "2026-07-24-01",
		SnapshotJSON: rawSnapshot,
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
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Config-Revision"); got != snap.Revision {
		t.Errorf("X-Config-Revision = %q, want %q", got, snap.Revision)
	}
	if got := rec.Header().Get("X-Config-SHA256"); got != snap.SHA256 {
		t.Errorf("X-Config-SHA256 = %q, want %q", got, snap.SHA256)
	}
	// Body must equal the raw snapshot bytes verbatim — no wrapper envelope,
	// no revision/sha256/created_at metadata fields at the top level.
	if got := rec.Body.Bytes(); !bytes.Equal(got, rawSnapshot) {
		t.Errorf("body = %s, want raw snapshot verbatim %s", got, rawSnapshot)
	}
	// Defense: the served body must not be a wrapper object carrying the
	// Config Service metadata fields.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	for _, banned := range []string{"snapshot", "sha256", "created_at", "compiled_meta"} {
		if _, ok := probe[banned]; ok {
			t.Errorf("body must not carry wrapper field %q at top level", banned)
		}
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

// TestServer_LatestSnapshot_RejectsUnsafeBody verifies the endpoint never
// serves a stored body that is not a single strict JSON value: an empty body,
// a JSON null, trailing data, or a syntactically invalid document must all map
// to a 500 instead of emitting bytes that would fail the executor's strict
// decoder. These guard the raw-body wire contract at the producer.
func TestServer_LatestSnapshot_RejectsUnsafeBody(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"empty", []byte("")},
		{"null", []byte("null")},
		{"whitespace only", []byte("   \n\t  ")},
		{"trailing data", []byte(`{"revision":"x"}{}`)},
		{"malformed", []byte(`{"revision":`)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			snap := repository.Snapshot{
				Revision:     "2026-07-24-01",
				SnapshotJSON: tc.body,
				SHA256:       "abc123",
				CreatedAt:    time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
			}
			s := newServer(&fakeReader{snap: snap}, fakePinger{})
			req := httptest.NewRequest(http.MethodGet, "/v1/config/snapshots/latest", nil)
			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 for body %q", rec.Code, tc.body)
			}
			// Must never emit the raw bytes of an unsafe (non-trivial) body. Empty
			// and "null" inputs are skipped because they are substrings of the
			// stable JSON error envelope, not leaked content.
			if rec.Body.Len() > 0 && len(tc.body) > 0 && tc.name != "null" && bytes.Contains(rec.Body.Bytes(), tc.body) {
				t.Errorf("response emitted unsafe raw body %q", tc.body)
			}
			if containsLeak(rec.Body.String()) {
				t.Errorf("response body leaked detail: %s", rec.Body.String())
			}
		})
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

// executorFixtureBytes loads a real, secret-free raw ConfigSnapshot fixture from
// the Executor service (services/executor/fixtures/configs/<name>.json). It is
// the shared wire-contract anchor: the Config Service serves this exact bytes
// as the raw body, and the Executor's configsource.LoadFromConfigService must
// consume the same shape. Reading the shared fixture (rather than a Config-side
// hand-written body) prevents either side from drifting behind a local copy.
//
// The path is resolved relative to this test file (like contractYAMLPath),
// which is the established convention in this package; `go test` runs in the
// package directory so the relative path is stable.
func executorFixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Clean(filepath.Join("../../../../services/executor/fixtures/configs", name+".json"))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read shared executor fixture %s: %v", p, err)
	}
	return b
}

// TestLatestSnapshot_CrossServiceWireContract drives the REAL Config
// Server.Router() (not a hand-written response fixture) with a fake repository
// that returns a real Executor ConfigSnapshot fixture as the stored raw body,
// then asserts the emitted HTTP response exactly matches the wire contract the
// Executor's configsource.LoadFromConfigService depends on:
//   - the body is the raw ConfigSnapshot JSON, byte-for-byte equal to the
//     stored snapshot (no {revision,snapshot,sha256,...} wrapper envelope);
//   - revision and content digest travel in X-Config-Revision / X-Config-SHA256;
//   - the body is a single strict top-level JSON object carrying NO Config
//     Service metadata wrapper fields, so it is directly consumable by the
//     Executor's strict raw-body decoder.
//
// This couples the producer (Config handler) to the consumer (Executor loader)
// through the shared fixture file, so a regression in either side's format
// breaks this test without any cross-module import (Go internal boundary
// respected).
func TestLatestSnapshot_CrossServiceWireContract(t *testing.T) {
	for _, name := range []string{"default", "xfyun", "anthropic"} {
		name := name
		t.Run(name, func(t *testing.T) {
			raw := executorFixtureBytes(t, name)
			snap := repository.Snapshot{
				Revision:     name + "-rev",
				SnapshotJSON: raw,
				SHA256:       name + "-sha",
				CreatedAt:    time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
			}
			s := newServer(&fakeReader{snap: snap}, fakePinger{})

			req := httptest.NewRequest(http.MethodGet, "/v1/config/snapshots/latest", nil)
			req.Header.Set("Accept", "application/json")
			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			// Headers carry the authoritative revision and digest.
			if got := rec.Header().Get("X-Config-Revision"); got != snap.Revision {
				t.Errorf("X-Config-Revision = %q, want %q", got, snap.Revision)
			}
			if got := rec.Header().Get("X-Config-SHA256"); got != snap.SHA256 {
				t.Errorf("X-Config-SHA256 = %q, want %q", got, snap.SHA256)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			// The body MUST equal the raw snapshot bytes verbatim — proving the
			// handler emits the raw ConfigSnapshot, not a wrapper.
			if got := rec.Body.Bytes(); !bytes.Equal(got, raw) {
				t.Errorf("body does not equal raw snapshot (got %d bytes, want %d); a wrapper envelope would change this", len(got), len(raw))
			}
			// The served body must be a single strict top-level JSON object and
			// must NOT carry Config Service metadata wrapper fields. This is the
			// exact shape the Executor's strict raw-body decoder consumes.
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
				t.Fatalf("body is not a JSON object (Executor loader would reject): %v", err)
			}
			for _, banned := range []string{"snapshot", "sha256", "created_at", "compiled_meta"} {
				if _, ok := probe[banned]; ok {
					t.Errorf("body carries wrapper field %q — Executor raw-body decoder would reject unknown field", banned)
				}
			}
		})
	}
}
