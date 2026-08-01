package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// dsn is built from env BILLING_REPO_TEST_DSN (set by the test harness that
// starts a temp pg). When unset, integration tests are skipped. The DSN must
// target the billing-only test database (tokenmp_billing) so the destructive
// schema reset used for test isolation cannot touch any other database.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("BILLING_REPO_TEST_DSN")
	if d == "" {
		t.Skip("BILLING_REPO_TEST_DSN not set; skipping repository integration test")
	}
	if !strings.Contains(d, "tokenmp_billing") {
		t.Fatalf("BILLING_REPO_TEST_DSN must target the tokenmp_billing test database; refusing to run against an arbitrary DSN")
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

// upMigrationFiles is the ordered list of up migrations applied by the test
// harness. Declared once so every test starts from the same complete schema;
// adding a new migration here is a deliberate, visible act.
var upMigrationFiles = []string{
	"000001_init.up.sql",
	"000002_limit_overrides.up.sql",
	"000003_plan_daily_weekly_categories.up.sql",
	"000004_settlement_state_machine.up.sql",
}

// migrationsDir returns the repo-relative path to the billing migrations.
func migrationsDir() string { return filepath.Join("..", "..", "migrations") }

// resetSchema drops and recreates the public schema, yielding a fully clean
// slate regardless of what data a previous test left behind. It deliberately
// does NOT run the down migrations: migration 000004's down is intentionally
// fail-closed (it RAISEs whenever settlement data exists), so it MUST NOT be
// used for test cleanup. The old helper ran down4..down1 in a loop and ignored
// errors; when down4 RAISEd (after any test that produced a settled/pending
// reservation or a reconcile/sweep ledger row) the loop kept going, ran
// down3/down2/down1, and dropped usage_ledger while down4 had already aborted
// mid-way. The next test's pre-clean down4 then hit `ALTER TABLE usage_ledger
// ...` on a now-missing table and fatalf'd, cascading into every later test.
// A schema reset is idempotent, migration-content-independent, and immune to
// the fail-closed guard. The DSN is constrained by dsn() to tokenmp_billing.
func resetSchema(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS public CASCADE`,
		`CREATE SCHEMA public`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("reset schema (%s): %v", stmt, err)
		}
	}
}

// applyUpMigrations applies every up migration in order on the caller's
// connection. It does not reset first; pair with resetSchema for a clean
// slate. A failure is a hard fatal so a partial/empty schema never cascades
// into obscure downstream assertion failures. It also verifies the resulting
// schema has the expected core tables so a silently-incomplete apply is
// caught here instead of at a confusing later assertion.
func applyUpMigrations(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	for _, name := range upMigrationFiles {
		up := readMigration(t, migrationsDir(), name)
		if _, err := conn.Exec(ctx, up); err != nil {
			t.Fatalf("apply up migration %s: %v", name, err)
		}
	}
	var coreTables int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
WHERE table_schema = current_schema()
  AND table_name IN ('users','plans','user_plans','quota_reservations','usage_ledger','user_plan_limit_overrides')`).Scan(&coreTables); err != nil {
		t.Fatalf("verify schema: query core tables: %v", err)
	}
	if coreTables != 6 {
		t.Fatalf("verify schema: expected 6 core tables, got %d (partial/empty schema)", coreTables)
	}
}

// applyMigrations resets the schema (drop+recreate public) and applies every
// up migration in order so the test starts from a known-clean, complete
// schema. Cleanup resets the schema again so the next test is fully isolated
// and never inherits another test's settled/pending rows or ledger entries.
// The fail-closed 000004 down migration is intentionally NOT run for cleanup.
func applyMigrations(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	resetSchema(t, ctx, conn)
	applyUpMigrations(t, ctx, conn)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		resetSchema(t, cctx, conn)
	})
}

func readMigration(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(b)
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

// setupCodingEntitlement inserts a coding plan with generous limits and an
// active user_plan binding so Reserve('coding', ...) enforcement does not
// fail-closed in tests that are not about quota enforcement.
func setupCodingEntitlement(t *testing.T, db *gorm.DB, userID string) {
	t.Helper()
	h, w, m := 1000000, 1000000, 1000000
	planID := insertCodingPlan(t, db, "ent-"+userID, &h, &w, &m)
	insertUserPlanActivated(t, db, userID, planID, "active", time.Now().UTC())
}

func reservationStatus(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var s string
	if err := db.Raw(`SELECT status FROM quota_reservations WHERE id = ?`, id).Scan(&s).Error; err != nil {
		t.Fatalf("query reservation status: %v", err)
	}
	return s
}

// insertCodingPlan inserts a coding plan with explicit nullable limits.
func insertCodingPlan(t *testing.T, db *gorm.DB, name string, hourly, weekly, monthly *int) int64 {
	t.Helper()
	var id int64
	if err := db.Raw(`INSERT INTO plans (name, plan_type, price, category, hourly_limit, weekly_limit, monthly_limit, allowed_models, status)
VALUES (?, 'coding', 0, 'monthly', ?, ?, ?, '[]'::jsonb, 'active') RETURNING id`,
		name, hourly, weekly, monthly).Scan(&id).Error; err != nil {
		t.Fatalf("insert coding plan: %v", err)
	}
	return id
}

func insertUserPlanActivated(t *testing.T, db *gorm.DB, userID string, planID int64, status string, activatedAt time.Time) {
	t.Helper()
	if err := db.Exec(`INSERT INTO user_plans (user_id, plan_id, plan_type, status, activated_at)
VALUES (?, ?, 'coding', ?, ?)`, userID, planID, status, activatedAt).Error; err != nil {
		t.Fatalf("insert user_plan: %v", err)
	}
}

func quotaExceededScope(err error) string {
	var qe *QuotaExceededError
	if errors.As(err, &qe) {
		return string(qe.Scope)
	}
	return ""
}

// intPtr returns a pointer to v (test helper for nullable plan limits).
func intPtr(v int) *int { return &v }

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
	setupCodingEntitlement(t, db, "u1")
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

	if err := r.Finalize(ctx, "res1", 8, 800, true); err != nil {
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
	err := r.Finalize(context.Background(), "does-not-exist", 1, 1, true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFinalize_Idempotent(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "u3")
	setupCodingEntitlement(t, db, "u3")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "res3", "u3", "req3", "coding", 10, 1000, nil); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := r.Finalize(ctx, "res3", 8, 800, true); err != nil {
		t.Fatalf("Finalize first: %v", err)
	}
	if err := r.Finalize(ctx, "res3", 8, 800, true); err != nil {
		t.Fatalf("Finalize second (idempotent same payload): %v", err)
	}
	// A different payload must now be a stable conflict (no retroactive change).
	if err := r.Finalize(ctx, "res3", 9, 900, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("Finalize with different payload: expected ErrConflict, got %v", err)
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
	setupCodingEntitlement(t, db, "u4")
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
	setupCodingEntitlement(t, db, "u5")
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

// --- coding window enforcement tests -------------------------------------

// consume charges one coding request: reserve then finalize (charge ledger).
func consume(t *testing.T, r *GormRepository, ctx context.Context, userID, tag string) {
	t.Helper()
	resID := "res-" + tag
	if err := r.Reserve(ctx, resID, userID, "req-"+tag, "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve %s: %v", tag, err)
	}
	if err := r.Finalize(ctx, resID, 1, 1, true); err != nil {
		t.Fatalf("Finalize %s: %v", tag, err)
	}
}

func TestReserve_CodingNoPlan_FailClosed(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "np")
	r := New(db)
	ctx := context.Background()

	err := r.Reserve(ctx, "res-np", "np", "req-np", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded (no active coding plan), got %v", err)
	}
	if s := quotaExceededScope(err); s != "period" {
		t.Fatalf("scope = %q, want period", s)
	}
	// No reservation or ledger row should have been written on rejection.
	var n int
	db.Raw(`SELECT count(*) FROM quota_reservations WHERE id = 'res-np'`).Scan(&n)
	if n != 0 {
		t.Fatalf("reservation row leaked on rejection: %d", n)
	}
}

func TestReserve_CodingEnforcesHour5(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "h5")
	lim := 2
	planID := insertCodingPlan(t, db, "h5plan", &lim, nil, nil)
	insertUserPlanActivated(t, db, "h5", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	// Consume 2 finalized requests (hour5 limit=2).
	consume(t, r, ctx, "h5", "a")
	consume(t, r, ctx, "h5", "b")
	// A third reserve must be rejected with scope hour5.
	err := r.Reserve(ctx, "res-h5c", "h5", "req-h5c", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if s := quotaExceededScope(err); s != "hour5" {
		t.Fatalf("scope = %q, want hour5", s)
	}
}

func TestReserve_CodingEnforcesWeekly(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "wk")
	lim := 1
	planID := insertCodingPlan(t, db, "wkplan", nil, &lim, nil)
	insertUserPlanActivated(t, db, "wk", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	consume(t, r, ctx, "wk", "a")
	err := r.Reserve(ctx, "res-wkb", "wk", "req-wkb", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if s := quotaExceededScope(err); s != "weekly" {
		t.Fatalf("scope = %q, want weekly", s)
	}
}

func TestReserve_CodingEnforcesPeriod(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "pd")
	lim := 2
	planID := insertCodingPlan(t, db, "pdplan", nil, nil, &lim)
	insertUserPlanActivated(t, db, "pd", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	consume(t, r, ctx, "pd", "a")
	consume(t, r, ctx, "pd", "b")
	err := r.Reserve(ctx, "res-pdc", "pd", "req-pdc", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if s := quotaExceededScope(err); s != "period" {
		t.Fatalf("scope = %q, want period", s)
	}
}

func TestReserve_CodingIdempotentSkipsEnforcement(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "idem")
	lim := 2
	planID := insertCodingPlan(t, db, "idemplan", &lim, nil, nil)
	insertUserPlanActivated(t, db, "idem", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	// First reservation takes one active hold. Under active-hold semantics the
	// reserved hold counts against the window, so window consumed becomes 1.
	if err := r.Reserve(ctx, "res-idem", "idem", "req-idem", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve first: %v", err)
	}
	// A second, independent reservation saturates the hard limit of 2
	// (window consumed becomes 2); any further fresh reserve is rejected.
	if err := r.Reserve(ctx, "res-idem2", "idem", "req-idem2", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve second (fills window): %v", err)
	}
	// Repeat reserve of the SAME id must stay idempotent (nil): the
	// existence check short-circuits before window enforcement, so a retry
	// of the first reservation succeeds even though the window is now full.
	if err := r.Reserve(ctx, "res-idem", "idem", "req-idem", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve repeat (idempotent): %v", err)
	}
	// Idempotency is keyed on reservation id alone: Reserve has no payload
	// conflict path (unlike Finalize/Release, which hash and reject
	// mismatched payloads), so repeating res-idem with a DIFFERENT request_id
	// also returns nil and leaves the stored row untouched.
	if err := r.Reserve(ctx, "res-idem", "idem", "req-other", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve repeat with different request_id (id-keyed, no conflict): %v", err)
	}
	// A third, distinct reservation id is genuinely new and must be rejected
	// by the now-full window — proving the limit was not merely relaxed to
	// mask the idempotency behavior: enforcement still bites on a fresh id.
	err := r.Reserve(ctx, "res-idem3", "idem", "req-idem3", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded for a fresh reservation when the window is full, got %v", err)
	}
	if s := quotaExceededScope(err); s != "hour5" {
		t.Fatalf("scope = %q, want hour5", s)
	}
}

func TestReserve_CodingWithinLimitSucceeds(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "ok")
	lim := 5
	planID := insertCodingPlan(t, db, "okplan", &lim, &lim, &lim)
	insertUserPlanActivated(t, db, "ok", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	consume(t, r, ctx, "ok", "a")
	consume(t, r, ctx, "ok", "b")
	// Third reserve is within all limits (3 <= 5).
	if err := r.Reserve(ctx, "res-ok3", "ok", "req-ok3", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve within limit: %v", err)
	}
}

func TestGetUsageWindows(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "uw")
	h, w, m := 5, 50, 500
	planID := insertCodingPlan(t, db, "uwplan", &h, &w, &m)
	insertUserPlanActivated(t, db, "uw", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	consume(t, r, ctx, "uw", "a")
	consume(t, r, ctx, "uw", "b")

	windows, err := r.GetUsageWindows(ctx, "uw")
	if err != nil {
		t.Fatalf("GetUsageWindows: %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("windows count = %d, want 3", len(windows))
	}
	byScope := map[string]UsageWindow{}
	for _, win := range windows {
		byScope[win.Scope] = win
	}
	for _, scope := range []string{"hour5", "weekly", "period"} {
		win, ok := byScope[scope]
		if !ok {
			t.Errorf("missing scope %q", scope)
			continue
		}
		if win.Consumed != 2 {
			t.Errorf("scope %q consumed = %d, want 2", scope, win.Consumed)
		}
		if win.Limit == nil {
			t.Errorf("scope %q limit nil", scope)
		}
	}
	// Sanity: remaining == limit - consumed for each.
	if byScope["hour5"].Remaining != 3 || byScope["weekly"].Remaining != 48 || byScope["period"].Remaining != 498 {
		t.Errorf("remaining = hour5:%d weekly:%d period:%d", byScope["hour5"].Remaining, byScope["weekly"].Remaining, byScope["period"].Remaining)
	}
}

func TestGetUsageWindows_NoCodingPlan(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "uw-none")
	r := New(db)
	windows, err := r.GetUsageWindows(context.Background(), "uw-none")
	if err != nil {
		t.Fatalf("GetUsageWindows: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("windows count = %d, want 0 (no coding plan)", len(windows))
	}
}
