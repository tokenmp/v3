package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tokenmp/v3/services/logging/internal/repository"
)

// fakePinger implements database.Pinger.
type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

// fakeRepo implements repository.Writer, repository.Reader and
// repository.BatchIngestor so it can serve as both the writer and reader
// argument to New. The server type-asserts the writer to BatchIngestor, so
// the fake implements that too.
type fakeRepo struct {
	log        repository.RequestLog
	logErr     error
	attempts   []repository.Attempt
	attemptErr error
	events     []repository.Event
	eventErr   error
	ingestErr  error
	ingested   repository.Batch
	ingestCall int
}

func (f *fakeRepo) InsertRequestLog(ctx context.Context, log repository.RequestLog) (int64, error) {
	return 1, nil
}
func (f *fakeRepo) InsertAttempt(ctx context.Context, attempt repository.Attempt) error { return nil }
func (f *fakeRepo) InsertEvent(ctx context.Context, event repository.Event) error       { return nil }

func (f *fakeRepo) GetRequestLog(ctx context.Context, requestID string) (repository.RequestLog, error) {
	return f.log, f.logErr
}
func (f *fakeRepo) ListAttempts(ctx context.Context, requestID string) ([]repository.Attempt, error) {
	return f.attempts, f.attemptErr
}
func (f *fakeRepo) ListEvents(ctx context.Context, requestID string) ([]repository.Event, error) {
	return f.events, f.eventErr
}

func (f *fakeRepo) IngestBatch(ctx context.Context, batch repository.Batch) error {
	f.ingestCall++
	f.ingested = batch
	return f.ingestErr
}

func newServer(repo *fakeRepo, p fakePinger) *Server {
	return New(repo, repo, p, nil)
}

func do(t *testing.T, s *Server, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, r)
	return rec
}

func assertCacheControl(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rec.Body.String())
	}
}

// containsLeak reports whether a response body exposes SQL/DSN/driver or
// credential fragments. Error bodies must never carry such detail.
func containsLeak(b string) bool {
	for _, frag := range []string{"password", "postgres://", "dsn", "pq:", "sql:", "tokenmp_logging"} {
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

func TestHealthz(t *testing.T) {
	s := newServer(&fakeRepo{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	assertCacheControl(t, rec)
}

func TestReadyz_OK(t *testing.T) {
	s := newServer(&fakeRepo{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", rec.Code)
	}
	assertCacheControl(t, rec)
}

func TestReadyz_NotReady(t *testing.T) {
	s := newServer(&fakeRepo{}, fakePinger{err: errors.New("down")})
	rec := do(t, s, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503", rec.Code)
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("readyz body leaked detail: %s", rec.Body.String())
	}
	assertCacheControl(t, rec)
}

func TestIngest_OK(t *testing.T) {
	repo := &fakeRepo{}
	s := newServer(repo, fakePinger{})
	body := []byte(`{
		"log":{"request_id":"req-1","final_status":"success","stream":false},
		"attempts":[
			{"request_id":"req-1","status":"success"},
			{"request_id":"req-1","status":"upstream_error"}
		],
		"events":[{"request_id":"req-1","source":"edge","stage":"received","status":"info"}]
	}`)
	rec := do(t, s, http.MethodPost, "/v1/logs/ingest", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp ingestResponse
	decodeBody(t, rec, &resp)
	if resp.RequestID != "req-1" {
		t.Errorf("request_id = %q, want req-1", resp.RequestID)
	}
	if resp.Accepted != 4 { // 1 log + 2 attempts + 1 event
		t.Errorf("accepted = %d, want 4", resp.Accepted)
	}
	if repo.ingestCall != 1 {
		t.Errorf("ingest call count = %d, want 1", repo.ingestCall)
	}
	if repo.ingested.Log.RequestID != "req-1" {
		t.Errorf("ingested log request_id = %q", repo.ingested.Log.RequestID)
	}
	if len(repo.ingested.Attempts) != 2 || len(repo.ingested.Events) != 1 {
		t.Errorf("ingested counts = attempts %d events %d, want 2 and 1", len(repo.ingested.Attempts), len(repo.ingested.Events))
	}
	assertCacheControl(t, rec)
}

func TestIngest_MissingRequestID(t *testing.T) {
	repo := &fakeRepo{}
	s := newServer(repo, fakePinger{})
	body := []byte(`{"log":{"final_status":"success","stream":false}}`)
	rec := do(t, s, http.MethodPost, "/v1/logs/ingest", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !contains(rec.Body.String(), "missing_request_id") {
		t.Errorf("expected missing_request_id, got %s", rec.Body.String())
	}
	if repo.ingestCall != 0 {
		t.Errorf("ingest should not be called, got %d", repo.ingestCall)
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked: %s", rec.Body.String())
	}
	assertCacheControl(t, rec)
}

func TestIngest_InvalidJSON(t *testing.T) {
	repo := &fakeRepo{}
	s := newServer(repo, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/logs/ingest", []byte(`{not json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !contains(rec.Body.String(), "invalid_json") {
		t.Errorf("expected invalid_json, got %s", rec.Body.String())
	}
	if repo.ingestCall != 0 {
		t.Errorf("ingest should not be called, got %d", repo.ingestCall)
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked: %s", rec.Body.String())
	}
	assertCacheControl(t, rec)
}

func TestIngest_WriteError(t *testing.T) {
	repo := &fakeRepo{ingestErr: repository.ErrInsertFailed}
	s := newServer(repo, fakePinger{})
	body := []byte(`{"log":{"request_id":"req-1","final_status":"success","stream":false}}`)
	rec := do(t, s, http.MethodPost, "/v1/logs/ingest", body)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !contains(rec.Body.String(), "ingest_failed") {
		t.Errorf("expected ingest_failed, got %s", rec.Body.String())
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked driver detail: %s", rec.Body.String())
	}
	assertCacheControl(t, rec)
}

// TestIngest_BodyLimit verifies the 2 MiB cap: a body exceeding it is
// rejected with invalid_json (413-style read bounded to 400 per contract).
func TestIngest_BodyLimit(t *testing.T) {
	s := newServer(&fakeRepo{}, fakePinger{})
	big := make([]byte, (2<<20)+64)
	for i := range big {
		big[i] = ' '
	}
	payload := append([]byte(`{"log":{"request_id":"r","final_status":"success","stream":false,"trace_id":"`), big...)
	payload = append(payload, []byte(`"}}`)...)
	rec := do(t, s, http.MethodPost, "/v1/logs/ingest", payload)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for oversized body", rec.Code)
	}
	if !contains(rec.Body.String(), "invalid_json") {
		t.Errorf("expected invalid_json, got %s", rec.Body.String())
	}
}

func TestGetLog_OK(t *testing.T) {
	repo := &fakeRepo{
		log:      repository.RequestLog{RequestID: "req-1", FinalStatus: "success"},
		attempts: []repository.Attempt{{RequestID: "req-1", Status: "success"}},
		events:   []repository.Event{{RequestID: "req-1", Source: "edge", Stage: "received", Status: "info"}},
	}
	s := newServer(repo, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/logs/req-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out logResponse
	decodeBody(t, rec, &out)
	if out.Log.RequestID != "req-1" {
		t.Errorf("log.request_id = %q", out.Log.RequestID)
	}
	if len(out.Attempts) != 1 || out.Attempts[0].RequestID != "req-1" {
		t.Errorf("attempts = %+v", out.Attempts)
	}
	if len(out.Events) != 1 || out.Events[0].Stage != "received" {
		t.Errorf("events = %+v", out.Events)
	}
	assertCacheControl(t, rec)
}

func TestGetLog_NotFound(t *testing.T) {
	repo := &fakeRepo{logErr: repository.ErrNotFound}
	s := newServer(repo, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/logs/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !contains(rec.Body.String(), "not_found") {
		t.Errorf("expected not_found, got %s", rec.Body.String())
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked: %s", rec.Body.String())
	}
	assertCacheControl(t, rec)
}

// TestGetLog_QueryError verifies a non-not-found read error is a safe 500
// with no SQL/DSN leakage.
func TestGetLog_QueryError(t *testing.T) {
	repo := &fakeRepo{logErr: repository.ErrQueryFailed}
	s := newServer(repo, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/logs/req-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked driver detail: %s", rec.Body.String())
	}
	assertCacheControl(t, rec)
}
