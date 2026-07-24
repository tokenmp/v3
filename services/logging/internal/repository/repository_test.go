package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// dsn is built from env (set by the test harness that starts a temp pg). If
// LOGGING_REPO_TEST_DSN is unset, integration tests are skipped.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("LOGGING_REPO_TEST_DSN")
	if d == "" {
		t.Skip("LOGGING_REPO_TEST_DSN not set; skipping repository integration test")
	}
	return d
}

func openDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// applyMigrations runs the down migration first (idempotent via IF EXISTS)
// so the test starts from a clean state regardless of prior migration
// state, then applies the up migration. The down is re-run on cleanup so
// repeated test runs against the same temp DB stay clean.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	migrationsDir := filepath.Join("..", "..", "migrations")
	downPath := filepath.Join(migrationsDir, "000001_init.down.sql")
	downBytes, err := os.ReadFile(downPath)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downBytes)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	upPath := filepath.Join(migrationsDir, "000001_init.up.sql")
	upBytes, err := os.ReadFile(upPath)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(upBytes)); err != nil {
		t.Fatalf("apply up migration: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, string(downBytes))
	})
}

func mustInsertRequestLog(t *testing.T, w Writer, requestID, finalStatus string) int64 {
	t.Helper()
	log := RequestLog{
		RequestID:    requestID,
		UserID:       "u-1",
		ModelName:    "gpt-test",
		Protocol:     "openai_chat",
		Stream:       false,
		FinalStatus:  finalStatus,
		HTTPStatus:   200,
		InputTokens:  10,
		OutputTokens: 20,
		TotalTokens:  30,
		LatencyMS:    150,
		CreatedAt:    time.Now().UTC(),
	}
	id, err := w.InsertRequestLog(context.Background(), log)
	if err != nil {
		t.Fatalf("insert request log: %v", err)
	}
	if id == 0 {
		t.Fatalf("insert returned zero id")
	}
	return id
}

func TestInsertRequestLog_Success(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	repo := New(db)

	now := time.Now().UTC().Truncate(time.Microsecond)
	complete := now.Add(120 * time.Millisecond)
	log := RequestLog{
		RequestID:      "req-success-1",
		TraceID:        "trace-1",
		UserID:         "u-1",
		ClientKeyID:    "key-1",
		ModelName:      "gpt-test",
		ResolvedModel:  "gpt-test-resolved",
		RouteID:        "route-1",
		ProviderID:     "prov-1",
		CredentialID:   "cred-1",
		Protocol:       "openai_chat",
		Stream:         false,
		FinalStatus:    "success",
		HTTPStatus:     200,
		InputTokens:    10,
		OutputTokens:   20,
		TotalTokens:    30,
		CacheTokens:    5,
		LatencyMS:      150,
		TTFTMS:         40,
		UsageStatus:    "final",
		ThinkingMode:   "enabled",
		ThinkingEffort: "medium",
		ReservationID:  "res-1",
		BillingPlan:    "plan-1",
		CreatedAt:      now,
		CompletedAt:    &complete,
	}
	id, err := repo.InsertRequestLog(context.Background(), log)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == 0 {
		t.Fatalf("returned id is zero")
	}

	got, err := repo.GetRequestLog(context.Background(), "req-success-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != id {
		t.Errorf("id = %d, want %d", got.ID, id)
	}
	if got.RequestID != "req-success-1" {
		t.Errorf("request_id = %q", got.RequestID)
	}
	if got.TraceID != "trace-1" {
		t.Errorf("trace_id = %q", got.TraceID)
	}
	if got.UserID != "u-1" {
		t.Errorf("user_id = %q", got.UserID)
	}
	if got.ClientKeyID != "key-1" {
		t.Errorf("client_key_id = %q", got.ClientKeyID)
	}
	if got.ModelName != "gpt-test" {
		t.Errorf("model_name = %q", got.ModelName)
	}
	if got.ResolvedModel != "gpt-test-resolved" {
		t.Errorf("resolved_model = %q", got.ResolvedModel)
	}
	if got.RouteID != "route-1" {
		t.Errorf("route_id = %q", got.RouteID)
	}
	if got.ProviderID != "prov-1" {
		t.Errorf("provider_id = %q", got.ProviderID)
	}
	if got.CredentialID != "cred-1" {
		t.Errorf("credential_id = %q", got.CredentialID)
	}
	if got.Protocol != "openai_chat" {
		t.Errorf("protocol = %q", got.Protocol)
	}
	if got.Stream {
		t.Errorf("stream = true, want false")
	}
	if got.FinalStatus != "success" {
		t.Errorf("final_status = %q", got.FinalStatus)
	}
	if got.HTTPStatus != 200 {
		t.Errorf("http_status = %d", got.HTTPStatus)
	}
	if got.InputTokens != 10 || got.OutputTokens != 20 || got.TotalTokens != 30 || got.CacheTokens != 5 {
		t.Errorf("tokens = in%d/out%d/tot%d/cache%d", got.InputTokens, got.OutputTokens, got.TotalTokens, got.CacheTokens)
	}
	if got.LatencyMS != 150 || got.TTFTMS != 40 {
		t.Errorf("latency=%d ttft=%d", got.LatencyMS, got.TTFTMS)
	}
	if got.UsageStatus != "final" {
		t.Errorf("usage_status = %q", got.UsageStatus)
	}
	if got.ThinkingMode != "enabled" || got.ThinkingEffort != "medium" {
		t.Errorf("thinking = %q/%q", got.ThinkingMode, got.ThinkingEffort)
	}
	if got.ReservationID != "res-1" {
		t.Errorf("reservation_id = %q", got.ReservationID)
	}
	if got.BillingPlan != "plan-1" {
		t.Errorf("billing_plan = %q", got.BillingPlan)
	}
	if got.CompletedAt == nil {
		t.Fatalf("completed_at is nil")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("created_at is zero")
	}
}

func TestInsertRequestLog_DefaultsCreatedAt(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	repo := New(db)

	// CreatedAt left zero — repository must pin it so the row routes into a
	// real daily partition instead of the default partition.
	log := RequestLog{
		RequestID:   "req-default-ts",
		Stream:      false,
		FinalStatus: "success",
	}
	id, err := repo.InsertRequestLog(context.Background(), log)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := repo.GetRequestLog(context.Background(), "req-default-ts")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != id {
		t.Errorf("id = %d, want %d", got.ID, id)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("created_at was not pinned by repository")
	}
}

func TestInsertAttempt_AndList(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	repo := New(db)

	logID := mustInsertRequestLog(t, repo, "req-attempt-1", "success")

	base := time.Now().UTC()
	a1 := Attempt{
		RequestLogID:    logID,
		RequestID:       "req-attempt-1",
		AttemptIndex:    0,
		RouteID:         "route-1",
		ProviderID:      "prov-1",
		CredentialID:    "cred-1",
		UpstreamModel:   "gpt-test",
		UpstreamURL:     "https://api.openai.test",
		Status:          "success",
		HTTPStatus:      200,
		LatencyMS:       120,
		RetryClassified: "terminal",
		Metadata:        []byte(`{"k":"v1"}`),
		CreatedAt:       base,
	}
	a2 := Attempt{
		RequestLogID:    logID,
		RequestID:       "req-attempt-1",
		AttemptIndex:    1,
		RouteID:         "route-2",
		ProviderID:      "prov-2",
		CredentialID:    "cred-2",
		UpstreamModel:   "claude-test",
		UpstreamURL:     "https://api.anthropic.test",
		Status:          "success",
		HTTPStatus:      200,
		LatencyMS:       90,
		RetryClassified: "retryable",
		Metadata:        []byte(`{"k":"v2"}`),
		CreatedAt:       base.Add(time.Millisecond),
	}
	if err := repo.InsertAttempt(context.Background(), a1); err != nil {
		t.Fatalf("insert attempt 1: %v", err)
	}
	if err := repo.InsertAttempt(context.Background(), a2); err != nil {
		t.Fatalf("insert attempt 2: %v", err)
	}

	got, err := repo.ListAttempts(context.Background(), "req-attempt-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d attempts, want 2", len(got))
	}
	if got[0].AttemptIndex != 0 || got[1].AttemptIndex != 1 {
		t.Errorf("order wrong: idx %d then %d", got[0].AttemptIndex, got[1].AttemptIndex)
	}
	if got[0].RequestLogID != logID {
		t.Errorf("request_log_id = %d, want %d", got[0].RequestLogID, logID)
	}
	if got[1].UpstreamURL != "https://api.anthropic.test" {
		t.Errorf("upstream_url = %q", got[1].UpstreamURL)
	}
	if got[1].RetryClassified != "retryable" {
		t.Errorf("retry_classified = %q", got[1].RetryClassified)
	}
	// empty request_id → empty list (not an error)
	none, err := repo.ListAttempts(context.Background(), "req-attempt-none")
	if err != nil {
		t.Fatalf("list none: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 attempts for missing request, got %d", len(none))
	}
}

func TestInsertEvent_AndList(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	repo := New(db)

	logID := mustInsertRequestLog(t, repo, "req-event-1", "success")

	idx0 := 0
	idx1 := 1
	base := time.Now().UTC()
	e1 := Event{
		RequestLogID: logID,
		RequestID:    "req-event-1",
		TraceID:      "trace-1",
		Source:       "edge",
		Stage:        "received",
		Status:       "info",
		AttemptIndex: &idx0,
		DurationMS:   5,
		Message:      "request received",
		Metadata:     []byte(`{"src":"edge"}`),
		CreatedAt:    base,
	}
	e2 := Event{
		RequestLogID: logID,
		RequestID:    "req-event-1",
		Source:       "executor",
		Stage:        "upstream_started",
		Status:       "info",
		AttemptIndex: &idx1,
		DurationMS:   12,
		Message:      "upstream call started",
		Metadata:     []byte(`{"attempt":1}`),
		CreatedAt:    base.Add(time.Millisecond),
	}
	if err := repo.InsertEvent(context.Background(), e1); err != nil {
		t.Fatalf("insert event 1: %v", err)
	}
	if err := repo.InsertEvent(context.Background(), e2); err != nil {
		t.Fatalf("insert event 2: %v", err)
	}

	got, err := repo.ListEvents(context.Background(), "req-event-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Stage != "received" || got[1].Stage != "upstream_started" {
		t.Errorf("order wrong: %q then %q", got[0].Stage, got[1].Stage)
	}
	if got[0].Source != "edge" || got[1].Source != "executor" {
		t.Errorf("source wrong: %q then %q", got[0].Source, got[1].Source)
	}
	if got[1].AttemptIndex == nil || *got[1].AttemptIndex != 1 {
		t.Errorf("attempt_index wrong: %v", got[1].AttemptIndex)
	}
}

func TestGetRequestLog_NotFound(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	repo := New(db)

	_, err := repo.GetRequestLog(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestInsertRequestLog_NoPlaintext verifies the repository structs carry no
// plaintext request/response body fields — the V3 privacy design point. It
// reads this package's own source and greps for the forbidden columns so the
// guard runs in-band (no external tooling needed).
func TestInsertRequestLog_NoPlaintext(t *testing.T) {
	srcPath := "repository.go"
	b, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read %s: %v", srcPath, err)
	}
	src := string(b)
	for _, bad := range []string{"request_body", "response_body", "RequestBody", "ResponseBody"} {
		if containsSubstring(src, bad) {
			t.Errorf("forbidden plaintext body reference %q present in %s", bad, srcPath)
		}
	}
}

func containsSubstring(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
