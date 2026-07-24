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

// dsn is built from env BILLING_REPO_TEST_DSN (set by the test harness that
// starts a temp pg). When unset, integration tests are skipped.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("BILLING_REPO_TEST_DSN")
	if d == "" {
		t.Skip("BILLING_REPO_TEST_DSN not set; skipping repository integration test")
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

// applyMigrations runs down first (idempotent via IF EXISTS) so the test
// starts from a clean state, then applies up. Down is re-run on cleanup.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
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
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_, _ = conn.Exec(cctx, string(downBytes))
	})
}

// --- test fixtures -------------------------------------------------------

func insertUser(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO users (id, status) VALUES (?, 'active')`, id).Error; err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertPlan(t *testing.T, db *gorm.DB, name, planType, category string, price float64, hourlyLimit int) int64 {
	t.Helper()
	var id int64
	if err := db.Raw(`INSERT INTO plans (name, plan_type, price, category, hourly_limit, allowed_models, status)
VALUES (?, ?, ?, ?, ?, '[]'::jsonb, 'active') RETURNING id`,
		name, planType, price, category, hourlyLimit).Scan(&id).Error; err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	return id
}

func insertUserPlan(t *testing.T, db *gorm.DB, userID string, planID int64, planType, status string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO user_plans (user_id, plan_id, plan_type, status, activated_at)
VALUES (?, ?, ?, ?, ?)`, userID, planID, planType, status, time.Now().UTC()).Error; err != nil {
		t.Fatalf("insert user_plan: %v", err)
	}
}

func reservationStatus(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var s string
	if err := db.Raw(`SELECT status FROM quota_reservations WHERE id = ?`, id).Scan(&s).Error; err != nil {
		t.Fatalf("query reservation status: %v", err)
	}
	return s
}

func ledgerCount(t *testing.T, db *gorm.DB, userID string) int {
	t.Helper()
	var n int
	if err := db.Raw(`SELECT count(*) FROM usage_ledger WHERE user_id = ?`, userID).Scan(&n).Error; err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n
}

func ledgerTypes(t *testing.T, db *gorm.DB, userID string) []string {
	t.Helper()
	var types []string
	if err := db.Raw(`SELECT ledger_type FROM usage_ledger WHERE user_id = ? ORDER BY id ASC`, userID).Scan(&types).Error; err != nil {
		t.Fatalf("query ledger types: %v", err)
	}
	return types
}

// --- tests ---------------------------------------------------------------

func TestReserve_Finalize_Release(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u1")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "res1", "u1", "req1", "coding", 10, 1000, nil); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if s := reservationStatus(t, db, "res1"); s != "reserved" {
		t.Fatalf("status after reserve = %q, want reserved", s)
	}
	if n := ledgerCount(t, db, "u1"); n != 1 {
		t.Fatalf("ledger count after reserve = %d, want 1", n)
	}

	if err := r.Finalize(ctx, "res1", 8, 800); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if s := reservationStatus(t, db, "res1"); s != "finalized" {
		t.Fatalf("status after finalize = %q, want finalized", s)
	}
	var finalRow struct {
		FinalRequests *int
		FinalTokens   *int64
	}
	if err := db.Raw(`SELECT final_requests, final_tokens FROM quota_reservations WHERE id = 'res1'`).Scan(&finalRow).Error; err != nil {
		t.Fatalf("query final values: %v", err)
	}
	finalReqs := 0
	finalTokens := int64(0)
	if finalRow.FinalRequests != nil {
		finalReqs = *finalRow.FinalRequests
	}
	if finalRow.FinalTokens != nil {
		finalTokens = *finalRow.FinalTokens
	}
	if finalReqs != 8 || finalTokens != 800 {
		t.Fatalf("final values = (%d,%d), want (8,800)", finalReqs, finalTokens)
	}
	if n := ledgerCount(t, db, "u1"); n != 2 {
		t.Fatalf("ledger count after finalize = %d, want 2 (reserve+charge)", n)
	}
	types := ledgerTypes(t, db, "u1")
	if len(types) != 2 || types[0] != "reserve" || types[1] != "charge" {
		t.Fatalf("ledger types = %v, want [reserve charge]", types)
	}

	// Release on a finalized reservation: idempotent (nil) or ErrConflict.
	err := r.Release(ctx, "res1")
	if err != nil && !errors.Is(err, ErrConflict) {
		t.Fatalf("Release on finalized: expected nil or ErrConflict, got %v", err)
	}
	// No new ledger row should have been added (refund not created on finalized).
	if n := ledgerCount(t, db, "u1"); n != 2 {
		t.Fatalf("ledger count after release-on-finalized = %d, want 2", n)
	}
}

func TestReserve_Idempotent(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u2")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "res2", "u2", "req2", "token", 5, 500, nil); err != nil {
		t.Fatalf("Reserve first: %v", err)
	}
	if err := r.Reserve(ctx, "res2", "u2", "req2", "token", 5, 500, nil); err != nil {
		t.Fatalf("Reserve second: %v", err)
	}
	// Exactly one reservation and one ledger row.
	var resN int
	if err := db.Raw(`SELECT count(*) FROM quota_reservations WHERE id = 'res2'`).Scan(&resN).Error; err != nil {
		t.Fatalf("count reservations: %v", err)
	}
	if resN != 1 {
		t.Fatalf("reservation count = %d, want 1", resN)
	}
	if n := ledgerCount(t, db, "u2"); n != 1 {
		t.Fatalf("ledger count = %d, want 1", n)
	}
}

func TestFinalize_NotFound(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	r := New(db)
	err := r.Finalize(context.Background(), "does-not-exist", 1, 1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFinalize_Idempotent(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u3")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "res3", "u3", "req3", "coding", 10, 1000, nil); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := r.Finalize(ctx, "res3", 8, 800); err != nil {
		t.Fatalf("Finalize first: %v", err)
	}
	if err := r.Finalize(ctx, "res3", 9, 900); err != nil {
		t.Fatalf("Finalize second (idempotent): %v", err)
	}
	// Only one charge row: reserve + charge = 2 total.
	if n := ledgerCount(t, db, "u3"); n != 2 {
		t.Fatalf("ledger count after double finalize = %d, want 2", n)
	}
	types := ledgerTypes(t, db, "u3")
	chargeN := 0
	for _, lt := range types {
		if lt == "charge" {
			chargeN++
		}
	}
	if chargeN != 1 {
		t.Fatalf("charge ledger count = %d, want 1", chargeN)
	}
}

func TestRelease_Idempotent(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u4")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "res4", "u4", "req4", "coding", 7, 700, nil); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := r.Release(ctx, "res4"); err != nil {
		t.Fatalf("Release first: %v", err)
	}
	if s := reservationStatus(t, db, "res4"); s != "released" {
		t.Fatalf("status = %q, want released", s)
	}
	if err := r.Release(ctx, "res4"); err != nil {
		t.Fatalf("Release second (idempotent): %v", err)
	}
	// reserve + refund = 2 total; no duplicate refund.
	if n := ledgerCount(t, db, "u4"); n != 2 {
		t.Fatalf("ledger count after double release = %d, want 2", n)
	}
}

func TestRelease_NotFound(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	r := New(db)
	err := r.Release(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListLedger(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u5")
	r := New(db)
	ctx := context.Background()

	// Two reservations → two reserve ledger entries.
	if err := r.Reserve(ctx, "res5a", "u5", "req5a", "coding", 3, 300, nil); err != nil {
		t.Fatalf("Reserve a: %v", err)
	}
	if err := r.Reserve(ctx, "res5b", "u5", "req5b", "coding", 4, 400, nil); err != nil {
		t.Fatalf("Reserve b: %v", err)
	}

	entries, err := r.ListLedger(ctx, "u5", 10)
	if err != nil {
		t.Fatalf("ListLedger: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListLedger returned %d entries, want 2", len(entries))
	}
	// newest-first: the later reservation's entry comes first.
	if entries[0].RequestID != "req5b" {
		t.Errorf("entries[0].request_id = %q, want req5b", entries[0].RequestID)
	}
	if entries[0].LedgerType != "reserve" {
		t.Errorf("entries[0].ledger_type = %q, want reserve", entries[0].LedgerType)
	}
	if entries[0].TokenDelta != -400 {
		t.Errorf("entries[0].token_delta = %d, want -400", entries[0].TokenDelta)
	}
}

func TestGetActiveUserPlan(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u6")
	planID := insertPlan(t, db, "pro", "coding", "monthly", 19.99, 100)
	// An expired binding (older) — must NOT be returned.
	insertUserPlan(t, db, "u6", planID, "coding", "expired")
	// An active binding.
	insertUserPlan(t, db, "u6", planID, "coding", "active")

	r := New(db)
	up, err := r.GetActiveUserPlan(context.Background(), "u6")
	if err != nil {
		t.Fatalf("GetActiveUserPlan: %v", err)
	}
	if up.UserID != "u6" || up.PlanID != planID || up.Status != "active" {
		t.Errorf("user_plan = %+v, want active binding for u6/plan %d", up, planID)
	}

	if _, err := r.GetActiveUserPlan(context.Background(), "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown user, got %v", err)
	}
}

func TestGetPlan(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	planID := insertPlan(t, db, "starter", "token", "yearly", 0, 50)
	r := New(db)

	plan, err := r.GetPlan(context.Background(), planID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if plan.ID != planID || plan.Name != "starter" || plan.PlanType != "token" {
		t.Errorf("plan = %+v", plan)
	}
	if plan.Price != 0 {
		t.Errorf("price = %v, want 0", plan.Price)
	}
	if plan.AllowedModels == nil || string(plan.AllowedModels) != "[]" {
		t.Errorf("allowed_models = %q, want []", string(plan.AllowedModels))
	}
	if plan.HourlyLimit == nil || *plan.HourlyLimit != 50 {
		t.Errorf("hourly_limit = %v, want 50", plan.HourlyLimit)
	}

	if _, err := r.GetPlan(context.Background(), 99999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListPlans(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertPlan(t, db, "a-active", "coding", "monthly", 10, 100)
	// Bump a disabled plan with a distinct name.
	if err := db.Exec(`INSERT INTO plans (name, plan_type, price, category, allowed_models, status)
VALUES ('b-disabled', 'token', 0, 'monthly', '[]'::jsonb, 'disabled')`).Error; err != nil {
		t.Fatalf("insert disabled plan: %v", err)
	}
	r := New(db)

	active, err := r.ListPlans(context.Background(), "active")
	if err != nil {
		t.Fatalf("ListPlans active: %v", err)
	}
	if len(active) != 1 || active[0].Name != "a-active" {
		t.Fatalf("active plans = %+v", active)
	}

	disabled, err := r.ListPlans(context.Background(), "disabled")
	if err != nil {
		t.Fatalf("ListPlans disabled: %v", err)
	}
	if len(disabled) != 1 || disabled[0].Name != "b-disabled" {
		t.Fatalf("disabled plans = %+v", disabled)
	}

	all, err := r.ListPlans(context.Background(), "")
	if err != nil {
		t.Fatalf("ListPlans all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("all plans count = %d, want 2", len(all))
	}
}
