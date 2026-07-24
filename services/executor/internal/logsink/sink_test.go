package logsink

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/requestlog"
)

// safeBaseURL is an arbitrary valid base URL used to construct sinks against
// a throwaway host; it never receives traffic because tests inject an
// httptest client or server URL.
const safeBaseURL = "http://logsink.invalid"

// remoteSinkWithServer returns a RemoteSink whose local port is a fresh
// in-memory execution log and whose HTTP client points at the given test
// server. The sink is ready to record against.
func remoteSinkWithServer(t *testing.T, serverURL string) (*RemoteSink, *requestlog.InMemoryExecution) {
	t.Helper()
	local := requestlog.NewInMemoryExecution()
	sink, err := NewRemoteSink(Options{Endpoint: serverURL, Local: local, HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatalf("NewRemoteSink() error = %v", err)
	}
	return sink, local
}

func TestNewRemoteSinkValidatesEndpoint(t *testing.T) {
	t.Parallel()

	local := requestlog.NewInMemoryExecution()
	marker := "unique-leak-marker-xyz"

	tests := []struct {
		name     string
		opts     Options
		wantErr  error
		wantLeak bool
	}{
		{name: "blank endpoint", opts: Options{Endpoint: "  ", Local: local}, wantErr: ErrSinkBlankURL},
		{name: "query string", opts: Options{Endpoint: "http://" + marker + ".x/v1?token=secret", Local: local}, wantErr: ErrSinkInvalidURL, wantLeak: true},
		{name: "fragment", opts: Options{Endpoint: "http://" + marker + ".x/#frag", Local: local}, wantErr: ErrSinkInvalidURL, wantLeak: true},
		{name: "userinfo", opts: Options{Endpoint: "http://user:pass@" + marker + ".x/", Local: local}, wantErr: ErrSinkInvalidURL, wantLeak: true},
		{name: "non-http scheme", opts: Options{Endpoint: "file://" + marker, Local: local}, wantErr: ErrSinkInvalidURL, wantLeak: true},
		{name: "path with segments", opts: Options{Endpoint: "http://" + marker + ".x/prefix", Local: local}, wantErr: ErrSinkInvalidURL, wantLeak: true},
		{name: "missing host", opts: Options{Endpoint: "http://", Local: local}, wantErr: ErrSinkInvalidURL},
		{name: "nil local", opts: Options{Endpoint: "http://x.example/", Local: nil}, wantErr: ErrSinkInvalidURL},
		{name: "negative timeout", opts: Options{Endpoint: "http://x.example/", Local: local, PostTimeout: -1}, wantErr: ErrSinkInvalidURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink, err := NewRemoteSink(tc.opts)
			if sink != nil {
				t.Fatalf("NewRemoteSink() returned non-nil sink on failure")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NewRemoteSink() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantLeak && err != nil && strings.Contains(err.Error(), marker) {
				t.Errorf("error leaks URL marker: %q", err.Error())
			}
		})
	}

	// Happy path: http and https base URLs with optional trailing slash.
	for _, raw := range []string{"http://logsink.example", "https://logsink.example/", "http://logsink.example:18084"} {
		sink, err := NewRemoteSink(Options{Endpoint: raw, Local: local})
		if err != nil {
			t.Fatalf("NewRemoteSink(%q) error = %v", raw, err)
		}
		if sink == nil {
			t.Fatalf("NewRemoteSink(%q) returned nil", raw)
		}
		if got := sink.endpoint; strings.HasSuffix(got, "//") || strings.Contains(got, "?") || strings.Contains(got, "#") {
			t.Errorf("endpoint normalized incorrectly: %q", got)
		}
	}
}

func TestRecordExecutionPostsBatchAndPreservesLocalQuery(t *testing.T) {
	t.Parallel()

	var got atomic.Value // stores the decoded batch
	hits := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/logs/ingest" {
			t.Errorf("path = %q, want /v1/logs/ingest", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("Accept = %q, want application/json", accept)
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var b batch
		if err := json.Unmarshal(body, &b); err != nil {
			t.Errorf("unmarshal batch: %v", err)
		}
		got.Store(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"request_id":"` + b.Log.RequestID + `","accepted":3}`))
	}))
	defer srv.Close()

	sink, local := remoteSinkWithServer(t, srv.URL)
	event := requestlog.ExecutionEvent{
		RequestID:     "req-1",
		ReservationID: "res-1",
		Attempt:       2,
		Candidate:     requestlog.ExecutionCandidate{ModelID: "gpt-x", ProviderID: "openai", RouteID: "r", CredentialID: "cred", AdapterID: "a"},
		Protocol:      "openai_chat",
		Kind:          requestlog.KindAttempt,
		Status:        "success",
		Code:          "",
		Type:          "",
		Timestamp:     time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
		Subject:       "user-1",
		KeyID:         "key-1",
		Latency:       250 * time.Millisecond,
		Usage:         requestlog.ExecutionUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		UsageKnown:    true,
	}
	if err := sink.RecordExecution(context.Background(), event); err != nil {
		t.Fatalf("RecordExecution() error = %v", err)
	}

	// Local query capability preserved.
	gotEvents, err := sink.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(gotEvents) != 1 || gotEvents[0].RequestID != "req-1" {
		t.Fatalf("local QueryEvents = %+v, want single req-1", gotEvents)
	}
	// Same surface as the underlying local port (defensive copy).
	mutated, _ := local.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
	if len(mutated) > 0 {
		mutated[0].RequestID = "MUTATED"
		again, _ := sink.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if again[0].RequestID == "MUTATED" {
			t.Fatal("QueryEvents() returned a reference instead of a defensive copy")
		}
	}

	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
	b, ok := got.Load().(batch)
	if !ok {
		t.Fatal("server did not capture a batch")
	}
	// The wire shape must align with the Logging Service repository types:
	// request-level summary, one attempt row for KindAttempt, and one
	// timeline event row.
	if b.Log.RequestID != "req-1" || b.Log.UserID != "user-1" || b.Log.ClientKeyID != "key-1" {
		t.Errorf("batch.Log = %+v, want request_id/user_id/client_key_id mapped", b.Log)
	}
	if b.Log.ResolvedModel != "gpt-x" || b.Log.RouteID != "r" || b.Log.ProviderID != "openai" || b.Log.CredentialID != "cred" {
		t.Errorf("batch.Log candidate mapping = %+v", b.Log)
	}
	if b.Log.Protocol != "openai_chat" || b.Log.Stream {
		t.Errorf("batch.Log protocol/stream = %q/%v", b.Log.Protocol, b.Log.Stream)
	}
	if b.Log.FinalStatus != "success" {
		t.Errorf("batch.Log.FinalStatus = %q, want success", b.Log.FinalStatus)
	}
	if b.Log.LatencyMS != 250 {
		t.Errorf("batch.Log.LatencyMS = %d, want 250", b.Log.LatencyMS)
	}
	if b.Log.InputTokens != 10 || b.Log.OutputTokens != 20 || b.Log.TotalTokens != 30 {
		t.Errorf("batch.Log usage = in=%d out=%d tot=%d", b.Log.InputTokens, b.Log.OutputTokens, b.Log.TotalTokens)
	}
	if b.Log.UsageStatus != "final" {
		t.Errorf("batch.Log.UsageStatus = %q, want final", b.Log.UsageStatus)
	}
	if b.Log.ReservationID != "res-1" {
		t.Errorf("batch.Log.ReservationID = %q", b.Log.ReservationID)
	}
	if b.Log.CreatedAt.IsZero() {
		t.Error("batch.Log.CreatedAt is zero")
	}
	if len(b.Attempts) != 1 {
		t.Fatalf("len(batch.Attempts) = %d, want 1", len(b.Attempts))
	}
	if b.Attempts[0].RequestID != "req-1" || b.Attempts[0].AttemptIndex != 2 || b.Attempts[0].UpstreamModel != "gpt-x" {
		t.Errorf("batch.Attempts[0] = %+v", b.Attempts[0])
	}
	if b.Attempts[0].Status != "success" || b.Attempts[0].LatencyMS != 250 {
		t.Errorf("batch.Attempts[0] status/latency = %q/%d", b.Attempts[0].Status, b.Attempts[0].LatencyMS)
	}
	if b.Attempts[0].RequestLogID != 0 {
		t.Errorf("batch.Attempts[0].RequestLogID = %d, want 0 (server stamps it)", b.Attempts[0].RequestLogID)
	}
	if len(b.Events) != 1 {
		t.Fatalf("len(batch.Events) = %d, want 1", len(b.Events))
	}
	if b.Events[0].Source != "executor" || b.Events[0].Stage != requestlog.KindAttempt {
		t.Errorf("batch.Events[0] source/stage = %q/%q", b.Events[0].Source, b.Events[0].Stage)
	}
	if b.Events[0].RequestID != "req-1" || b.Events[0].DurationMS != 250 {
		t.Errorf("batch.Events[0] = %+v", b.Events[0])
	}
	if b.Events[0].AttemptIndex == nil || *b.Events[0].AttemptIndex != 2 {
		t.Errorf("batch.Events[0].AttemptIndex = %v, want 2", b.Events[0].AttemptIndex)
	}
}

func TestRecordExecutionNonAttemptKindHasNoAttemptRow(t *testing.T) {
	t.Parallel()

	var captured atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var b batch
		_ = json.Unmarshal(body, &b)
		captured.Store(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, _ := remoteSinkWithServer(t, srv.URL)
	// A finalized event: terminal, should carry completed_at and no attempt row.
	if err := sink.RecordExecution(context.Background(), requestlog.ExecutionEvent{
		RequestID:     "req-2",
		ReservationID: "res-2",
		Kind:          requestlog.KindFinalized,
		Status:        "success",
		Settlement:    requestlog.ExecutionSettlement{Disposition: "finalized", Outcome: "priced"},
		Timestamp:     time.Date(2026, 7, 25, 12, 1, 0, 0, time.UTC),
		Committed:     false,
	}); err != nil {
		t.Fatalf("RecordExecution() error = %v", err)
	}
	b := captured.Load().(batch)
	if len(b.Attempts) != 0 {
		t.Errorf("len(Attempts) = %d, want 0 for non-attempt kind", len(b.Attempts))
	}
	if len(b.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(b.Events))
	}
	if b.Events[0].Stage != requestlog.KindFinalized {
		t.Errorf("Event stage = %q, want finalized", b.Events[0].Stage)
	}
	if b.Log.CompletedAt == nil {
		t.Error("Log.CompletedAt is nil for finalized, want timestamp")
	}
	if b.Log.FinalStatus != "success" {
		t.Errorf("Log.FinalStatus = %q, want success", b.Log.FinalStatus)
	}
}

func TestRecordExecutionSwallowsRemoteFailureAndKeepsLocal(t *testing.T) {
	t.Parallel()

	// Server returns 500. RecordExecution must swallow it (return nil) so the
	// executor main path is never failed by Logging Service unavailability.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sink, _ := remoteSinkWithServer(t, srv.URL)
	if err := sink.RecordExecution(context.Background(), requestlog.ExecutionEvent{RequestID: "r", Kind: requestlog.KindAttempt, Timestamp: time.Now()}); err != nil {
		t.Fatalf("RecordExecution() error = %v, want nil (swallowed)", err)
	}
	got, _ := sink.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
	if len(got) != 1 {
		t.Fatalf("local QueryEvents len = %d, want 1 (local record must succeed even when remote fails)", len(got))
	}
}

func TestPostDoesNotFollowRedirect(t *testing.T) {
	t.Parallel()

	target := "https://redirect-target.invalid.example"
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Issue a redirect to an attacker-controlled host. A hardened client
		// must NOT follow it; the post is classified as failed and the
		// target never receives traffic.
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer srv.Close()

	sink, err := NewRemoteSink(Options{Endpoint: srv.URL, Local: requestlog.NewInMemoryExecution()})
	if err != nil {
		t.Fatalf("NewRemoteSink() error = %v", err)
	}
	if err := sink.post(batch{Log: requestLog{RequestID: "r"}, Events: []timelineEvent{{RequestID: "r", Source: "executor", Stage: "attempt", Status: "ok"}}}); !errors.Is(err, ErrSinkUnavailable) {
		t.Fatalf("post() error = %v, want ErrSinkUnavailable", err)
	}
	// No redirect following means only the single original request hit.
	if hits.Load() != 1 {
		t.Errorf("redirect was followed: hits = %d, want 1", hits.Load())
	}
}

func TestPostOversizedBatchRejectedBeforeSend(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, err := NewRemoteSink(Options{Endpoint: srv.URL, Local: requestlog.NewInMemoryExecution()})
	if err != nil {
		t.Fatalf("NewRemoteSink() error = %v", err)
	}
	// Construct an oversized batch exceeding the 2 MiB cap via a large
	// timeline message; buildBatch never produces such a payload, so this
	// exercises the post-time size gate directly.
	huge := strings.Repeat("x", int(MaxIngestBodyBytes+1))
	err = sink.post(batch{Log: requestLog{RequestID: "r"}, Events: []timelineEvent{{RequestID: "r", Source: "executor", Stage: "attempt", Status: "ok", Message: huge}}})
	if !errors.Is(err, ErrSinkOversized) {
		t.Fatalf("post() error = %v, want ErrSinkOversized", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("server received %d hits, want 0 (oversized dropped before send)", hits.Load())
	}
}

func TestPostNoLeakOnUnreachableHost(t *testing.T) {
	t.Parallel()

	// An unreachable host. The endpoint must never appear in the error.
	sink, err := NewRemoteSink(Options{Endpoint: "http://127.0.0.1:1", Local: requestlog.NewInMemoryExecution(), PostTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewRemoteSink() error = %v", err)
	}
	err = sink.post(batch{Log: requestLog{RequestID: "r"}, Events: []timelineEvent{{RequestID: "r", Source: "executor", Stage: "attempt", Status: "ok"}}})
	if !errors.Is(err, ErrSinkUnavailable) {
		t.Fatalf("post() error = %v, want ErrSinkUnavailable", err)
	}
	// No URL, host, or port leak; the sentinel message is a fixed string and
	// must not embed the configured endpoint. The body carries only the safe
	// wire shape; the sentinel never echoes request/response bytes.
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), ":1") || strings.Contains(err.Error(), "request_id") {
		t.Errorf("error leaks endpoint or request field: %q", err.Error())
	}
}

func TestRecordExecutionUsesBackgroundContext(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, _ := remoteSinkWithServer(t, srv.URL)
	// Cancel the caller's context BEFORE recording. Because the remote post
	// uses a background-derived context, the post must still succeed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.RecordExecution(ctx, requestlog.ExecutionEvent{RequestID: "r", Kind: requestlog.KindAttempt, Timestamp: time.Now()}); err != nil {
		t.Fatalf("RecordExecution() error = %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1 (post must use background context)", hits.Load())
	}
}

func TestQueryEventsForwardsFilterToLocal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink, _ := remoteSinkWithServer(t, srv.URL)
	for _, e := range []requestlog.ExecutionEvent{
		{RequestID: "r1", Kind: requestlog.KindAttempt, Attempt: 1, Timestamp: time.Now()},
		{RequestID: "r1", Kind: requestlog.KindReserved, Timestamp: time.Now()},
		{RequestID: "r2", Kind: requestlog.KindAttempt, Attempt: 1, Timestamp: time.Now()},
	} {
		if err := sink.RecordExecution(context.Background(), e); err != nil {
			t.Fatalf("RecordExecution() error = %v", err)
		}
	}
	got, err := sink.QueryEvents(context.Background(), requestlog.ExecutionFilter{RequestID: "r1", Kind: requestlog.KindAttempt})
	if err != nil {
		t.Fatalf("QueryEvents() error = %v", err)
	}
	if len(got) != 1 || got[0].RequestID != "r1" || got[0].Kind != requestlog.KindAttempt {
		t.Fatalf("QueryEvents(filter) = %+v, want single r1/attempt", got)
	}
}

func TestRemoteSinkContract(t *testing.T) {
	t.Parallel()

	// The Logging Service contract is a fresh 200 server per port so each
	// contract subtest's port posts without contention.
	newPort := func() requestlog.ExecutionPort {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		sink, err := NewRemoteSink(Options{Endpoint: srv.URL, Local: requestlog.NewInMemoryExecution()})
		if err != nil {
			t.Fatalf("NewRemoteSink() error = %v", err)
		}
		return sink
	}

	t.Run("events returns empty initially", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		got, err := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("len(QueryEvents()) = %d, want 0", len(got))
		}
	})

	t.Run("record and query single event", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		event := requestlog.ExecutionEvent{RequestID: "r1", Kind: requestlog.KindAttempt, Timestamp: time.Now()}
		if err := port.RecordExecution(context.Background(), event); err != nil {
			t.Fatalf("RecordExecution() error = %v", err)
		}
		got, err := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != 1 || got[0].RequestID != "r1" {
			t.Fatalf("QueryEvents() = %+v, want r1", got)
		}
	})

	t.Run("record preserves order", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		events := []requestlog.ExecutionEvent{
			{RequestID: "a", Kind: requestlog.KindAttempt, Timestamp: time.Now()},
			{RequestID: "b", Kind: requestlog.KindReserved, Timestamp: time.Now()},
			{RequestID: "c", Kind: requestlog.KindFinalized, Timestamp: time.Now()},
		}
		for _, e := range events {
			if err := port.RecordExecution(context.Background(), e); err != nil {
				t.Fatalf("RecordExecution() error = %v", err)
			}
		}
		got, err := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if err != nil {
			t.Fatalf("QueryEvents() error = %v", err)
		}
		if len(got) != len(events) {
			t.Fatalf("len(QueryEvents()) = %d, want %d", len(got), len(events))
		}
		for i, e := range events {
			if got[i].RequestID != e.RequestID || got[i].Kind != e.Kind {
				t.Errorf("QueryEvents()[%d] = %+v, want %+v", i, got[i], e)
			}
		}
	})

	t.Run("query returns defensive copy", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_ = port.RecordExecution(context.Background(), requestlog.ExecutionEvent{RequestID: "x", Kind: requestlog.KindAttempt, Timestamp: time.Now()})
		got1, _ := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if len(got1) != 1 {
			t.Fatalf("len(got1) = %d, want 1", len(got1))
		}
		got1[0].RequestID = "MUTATED"
		got3, _ := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if got3[0].RequestID == "MUTATED" {
			t.Error("QueryEvents() returned a reference instead of a copy")
		}
	})

	t.Run("query by kind filters correctly", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_ = port.RecordExecution(context.Background(), requestlog.ExecutionEvent{RequestID: "a", Kind: requestlog.KindAttempt, Timestamp: time.Now()})
		_ = port.RecordExecution(context.Background(), requestlog.ExecutionEvent{RequestID: "b", Kind: requestlog.KindReserved, Timestamp: time.Now()})
		_ = port.RecordExecution(context.Background(), requestlog.ExecutionEvent{RequestID: "c", Kind: requestlog.KindAttempt, Timestamp: time.Now()})
		got, _ := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{Kind: requestlog.KindAttempt})
		if len(got) != 2 {
			t.Fatalf("len(QueryEvents(kind=attempt)) = %d, want 2", len(got))
		}
	})

	t.Run("concurrent record and query", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		const n = 200
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				_ = port.RecordExecution(context.Background(), requestlog.ExecutionEvent{RequestID: "c", Kind: requestlog.KindAttempt, Attempt: i, Timestamp: time.Now()})
			}(i)
		}
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for range 50 {
					_, _ = port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
				}
			}()
		}
		close(start)
		wg.Wait()
		got, _ := port.QueryEvents(context.Background(), requestlog.ExecutionFilter{})
		if len(got) != n {
			t.Errorf("len(QueryEvents()) = %d, want %d", len(got), n)
		}
	})
}

// TestWireShapeAlignsWithRepository asserts that the JSON produced by
// marshaling a fully-populated batch exposes exactly the field names the
// Logging Service repository types declare. The expected key set is copied
// from the json tags of repository.RequestLog/Attempt/Event and the server's
// ingestRequest struct. This is the design/build-time alignment check that
// prevents drift between the two services without a Go runtime import.
func TestWireShapeAlignsWithRepository(t *testing.T) {
	t.Parallel()

	// Fully-populated batch so every omitempty field is emitted.
	idx := 3
	completed := time.Date(2026, 7, 25, 12, 2, 0, 0, time.UTC)
	meta := json.RawMessage(`{"k":"v"}`)
	b := batch{
		Log: requestLog{
			ID:                     42,
			RequestID:              "rid",
			TraceID:                "tid",
			UserID:                 "uid",
			ClientKeyID:            "ckid",
			ModelName:              "mn",
			ResolvedModel:          "rm",
			RouteID:                "rid2",
			ProviderID:             "pid",
			CredentialID:           "cid",
			Protocol:               "p",
			Stream:                 true,
			FinalStatus:            "ok",
			HTTPStatus:             200,
			InputTokens:            1,
			OutputTokens:           2,
			TotalTokens:            3,
			CacheTokens:            4,
			LatencyMS:              5,
			TTFTMS:                 6,
			ErrorCode:              "EC",
			ErrorType:              "ET",
			UpstreamHTTPStatus:     502,
			UsageStatus:            "final",
			ThinkingMode:           "on",
			ThinkingEffort:         "high",
			ThinkingEffortDegraded: "true",
			ReservationID:          "res",
			BillingPlan:            "plan",
			CreatedAt:              time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
			CompletedAt:            &completed,
		},
		Attempts: []attempt{{
			ID:                 7,
			RequestLogID:       8,
			RequestID:          "rid",
			AttemptIndex:       3,
			RouteID:            "rid2",
			ProviderID:         "pid",
			CredentialID:       "cid",
			UpstreamModel:      "rm",
			UpstreamURL:        "u",
			Status:             "ok",
			HTTPStatus:         200,
			LatencyMS:          5,
			ErrorCode:          "EC",
			ErrorType:          "ET",
			UpstreamHTTPStatus: 502,
			RetryClassified:    "retryable",
			Metadata:           meta,
			CreatedAt:          time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
		}},
		Events: []timelineEvent{{
			ID:           9,
			RequestLogID: 8,
			RequestID:    "rid",
			TraceID:      "tid",
			Source:       "executor",
			Stage:        "attempt",
			Status:       "ok",
			AttemptIndex: &idx,
			DurationMS:   5,
			Message:      "msg",
			Metadata:     meta,
			CreatedAt:    time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
		}},
	}

	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal batch to map: %v", err)
	}
	// Top-level keys mirror the Logging Service ingestRequest struct exactly.
	wantTop := map[string]bool{"log": true, "attempts": true, "events": true}
	if len(decoded) != len(wantTop) {
		t.Fatalf("top-level keys = %d, want %d", len(decoded), len(wantTop))
	}
	for k := range wantTop {
		if _, ok := decoded[k]; !ok {
			t.Errorf("missing top-level key %q", k)
		}
	}

	// Per-row field sets mirror the repository json tags exactly. The id
	// field is json:"-" so it must never appear.
	wantLog := []string{"request_id", "trace_id", "user_id", "client_key_id", "model_name", "resolved_model", "route_id", "provider_id", "credential_id", "protocol", "stream", "final_status", "http_status", "input_tokens", "output_tokens", "total_tokens", "cache_tokens", "latency_ms", "ttft_ms", "error_code", "error_type", "upstream_http_status", "usage_status", "thinking_mode", "thinking_effort", "thinking_effort_degraded", "reservation_id", "billing_plan", "created_at", "completed_at"}
	assertObjectKeySet(t, decoded["log"], wantLog, "log")
	wantAttempt := []string{"request_log_id", "request_id", "attempt_index", "route_id", "provider_id", "credential_id", "upstream_model", "upstream_url", "status", "http_status", "latency_ms", "error_code", "error_type", "upstream_http_status", "retry_classified", "metadata", "created_at"}
	assertKeySet(t, decoded["attempts"], wantAttempt, "attempts")
	wantEvent := []string{"request_log_id", "request_id", "trace_id", "source", "stage", "status", "attempt_index", "duration_ms", "message", "metadata", "created_at"}
	assertKeySet(t, decoded["events"], wantEvent, "events")
}

func assertObjectKeySet(t *testing.T, raw json.RawMessage, want []string, label string) {
	t.Helper()
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("unmarshal %s object: %v", label, err)
	}
	if len(row) != len(want) {
		t.Errorf("%s key count = %d, want %d; got=%v want=%v", label, len(row), len(want), keysOf(row), want)
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, k := range want {
		wantSet[k] = struct{}{}
	}
	for k := range row {
		if _, ok := wantSet[k]; !ok {
			t.Errorf("unexpected %s key %q", label, k)
		}
	}
	for _, k := range want {
		if _, ok := row[k]; !ok {
			t.Errorf("missing %s key %q", label, k)
		}
	}
}

func assertKeySet(t *testing.T, raw json.RawMessage, want []string, label string) {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("unmarshal %s rows: %v", label, err)
	}
	if len(rows) != 1 {
		t.Fatalf("%s rows = %d, want 1", label, len(rows))
	}
	row := rows[0]
	if len(row) != len(want) {
		t.Errorf("%s key count = %d, want %d; got=%v want=%v", label, len(row), len(want), keysOf(row), want)
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, k := range want {
		wantSet[k] = struct{}{}
	}
	for k := range row {
		if _, ok := wantSet[k]; !ok {
			t.Errorf("unexpected %s key %q", label, k)
		}
	}
	for _, k := range want {
		if _, ok := row[k]; !ok {
			t.Errorf("missing %s key %q", label, k)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
