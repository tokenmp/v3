package repository

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestReserve_ActiveHoldsCountAgainstWindow verifies that concurrent active
// reserved holds count against the coding window so they cannot punch through
// a hard limit before being finalized.
func TestReserve_ActiveHoldsCountAgainstWindow(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "hold")
	lim := 2
	planID := insertCodingPlan(t, db, "holdplan", &lim, nil, nil)
	insertUserPlanActivated(t, db, "hold", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	// Two active holds (reserved, not yet finalized) must saturate the
	// hourly window of 2.
	if err := r.Reserve(ctx, "h1", "hold", "rq1", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve h1: %v", err)
	}
	if err := r.Reserve(ctx, "h2", "hold", "rq2", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve h2: %v", err)
	}
	// A third reserve must be rejected (held=2 == limit).
	err := r.Reserve(ctx, "h3", "hold", "rq3", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if s := quotaExceededScope(err); s != "hour5" {
		t.Fatalf("scope = %q, want hour5", s)
	}
	// Releasing one hold frees the window.
	if err := r.Release(ctx, "h1"); err != nil {
		t.Fatalf("release h1: %v", err)
	}
	if err := r.Reserve(ctx, "h3", "hold", "rq3", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve h3 after release: %v", err)
	}
}

// TestFinalize_UnknownUsageRejected verifies that Finalize with usageKnown=false
// is rejected (no guessing). The caller must MarkPending instead.
func TestFinalize_UnknownUsageRejected(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "unk")
	setupCodingEntitlement(t, db, "unk")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "ru", "unk", "rqu", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.Finalize(ctx, "ru", 0, 0, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("Finalize unknown usage: expected ErrConflict, got %v", err)
	}
	if s := reservationStatus(t, db, "ru"); s != "reserved" {
		t.Fatalf("status after rejected finalize = %q, want reserved", s)
	}
}

// TestMarkPending_ReconcileKnown verifies the pending → reconcile(settled) path.
func TestMarkPending_ReconcileKnown(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "pen")
	setupCodingEntitlement(t, db, "pen")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "rp", "pen", "rqp", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.MarkPending(ctx, "rp"); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if s := reservationStatus(t, db, "rp"); s != "pending_reconciliation" {
		t.Fatalf("status = %q, want pending_reconciliation", s)
	}
	// Idempotent re-mark.
	if err := r.MarkPending(ctx, "rp"); err != nil {
		t.Fatalf("mark pending (idempotent): %v", err)
	}
	// Reconcile with known usage → finalized/settled.
	if err := r.Reconcile(ctx, "rp", 1, 50, true); err != nil {
		t.Fatalf("reconcile known: %v", err)
	}
	st, err := r.GetReservation(ctx, "rp")
	if err != nil {
		t.Fatalf("get reservation: %v", err)
	}
	if st.Status != "finalized" || st.SettlementStatus != "settled" || !st.UsageKnown {
		t.Fatalf("status = %+v", st)
	}
	// Reconcile idempotent.
	if err := r.Reconcile(ctx, "rp", 1, 50, true); err != nil {
		t.Fatalf("reconcile (idempotent): %v", err)
	}
}

// TestReconcile_UnknownReleases verifies unknown usage is released, never guessed.
func TestReconcile_UnknownReleases(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "unk2")
	setupCodingEntitlement(t, db, "unk2")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "ru2", "unk2", "rqu2", "coding", 1, 10, nil); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.MarkPending(ctx, "ru2"); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	if err := r.Reconcile(ctx, "ru2", 0, 0, false); err != nil {
		t.Fatalf("reconcile unknown: %v", err)
	}
	st, _ := r.GetReservation(ctx, "ru2")
	if st.Status != "released" || st.UsageKnown {
		t.Fatalf("status = %+v, want released/usage_known=false", st)
	}
}

// TestReconcile_ReservedRejected verifies a reserved (non-pending) reservation
// cannot be reconciled directly — it must MarkPending first.
func TestReconcile_ReservedRejected(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "rr")
	setupCodingEntitlement(t, db, "rr")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "rr1", "rr", "rqrr", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.Reconcile(ctx, "rr1", 1, 1, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

// TestExpire_SweepsExpiredHold verifies the sweeper path: a reserved
// reservation with a past expires_at is expired and a sweep refund row appended.
func TestExpire_SweepsExpiredHold(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "exp")
	lim := 100
	planID := insertCodingPlan(t, db, "expplan", &lim, &lim, &lim)
	insertUserPlanActivated(t, db, "exp", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	past := time.Now().UTC().Add(-time.Minute)
	if err := r.Reserve(ctx, "re", "exp", "rqe", "coding", 1, 5, &past); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.Expire(ctx, "re"); err != nil {
		t.Fatalf("expire: %v", err)
	}
	st, _ := r.GetReservation(ctx, "re")
	if st.Status != "expired" || st.SettlementStatus != "expired" {
		t.Fatalf("status = %+v", st)
	}
	// Idempotent.
	if err := r.Expire(ctx, "re"); err != nil {
		t.Fatalf("expire (idempotent): %v", err)
	}
	// A non-due reservation cannot be expired.
	future := time.Now().UTC().Add(time.Hour)
	if err := r.Reserve(ctx, "rf", "exp", "rqf", "coding", 1, 5, &future); err != nil {
		t.Fatalf("reserve future: %v", err)
	}
	if err := r.Expire(ctx, "rf"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expire non-due: expected ErrConflict, got %v", err)
	}
}

// TestListExpiredReservations and TestListPendingReservations back the sweeper loop.
func TestListExpiredReservations(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "lx")
	lim := 100
	planID := insertCodingPlan(t, db, "lxplan", &lim, &lim, &lim)
	insertUserPlanActivated(t, db, "lx", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	past := time.Now().UTC().Add(-time.Minute)
	if err := r.Reserve(ctx, "lx1", "lx", "rq1", "coding", 1, 1, &past); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	ids, err := r.ListExpiredReservations(ctx, 100)
	if err != nil {
		t.Fatalf("list expired: %v", err)
	}
	if len(ids) != 1 || ids[0] != "lx1" {
		t.Fatalf("expired ids = %v", ids)
	}
}

func TestListPendingReservations(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "lp")
	setupCodingEntitlement(t, db, "lp")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "lp1", "lp", "rq1", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := r.MarkPending(ctx, "lp1"); err != nil {
		t.Fatalf("mark pending: %v", err)
	}
	// With a generous grace, nothing is due.
	rows, err := r.ListPendingReservations(ctx, time.Hour, 100)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("grace too short: %v", rows)
	}
	// With zero grace, it is due.
	rows, err = r.ListPendingReservations(ctx, 0, 100)
	if err != nil {
		t.Fatalf("list pending (zero grace): %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "lp1" {
		t.Fatalf("pending rows = %+v", rows)
	}
	if rows[0].RequestID != "rq1" || rows[0].UserID != "lp" || rows[0].BillingPlan != "coding" || rows[0].ReservedRequests != 1 {
		t.Fatalf("pending projection = %+v", rows[0])
	}
}

// TestMigration_Down_FailClosed_OnPending verifies the fail-closed downgrade:
// 000004 down must RAISE EXCEPTION when pending_reconciliation rows exist,
// and must succeed once all pending rows are resolved to a state the
// pre-000004 schema can express (terminal status + all 000004-only columns
// cleared). This is the pending-status variant, run alongside the
// reconcile/sweep/settled-column variants below.
//
// Sequence: up(0001..0004) → insert pending → down(000004) expected to fail →
// resolve pending (clear 000004-only data) → down(000004) succeeds.
func TestMigration_Down_FailClosed_OnPending(t *testing.T) {
	d := dsn(t)
	ctx0, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx0, d)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	migrationsDir := filepath.Join("..", "..", "migrations")
	ups := []string{
		readMigration(t, migrationsDir, "000001_init.up.sql"),
		readMigration(t, migrationsDir, "000002_limit_overrides.up.sql"),
		readMigration(t, migrationsDir, "000003_plan_daily_weekly_categories.up.sql"),
		readMigration(t, migrationsDir, "000004_settlement_state_machine.up.sql"),
	}
	down4 := readMigration(t, migrationsDir, "000004_settlement_state_machine.down.sql")
	down3 := readMigration(t, migrationsDir, "000003_plan_daily_weekly_categories.down.sql")
	down2 := readMigration(t, migrationsDir, "000002_limit_overrides.down.sql")
	down1 := readMigration(t, migrationsDir, "000001_init.down.sql")
	// down-all first (idempotent) for a clean slate.
	for _, d := range []string{down4, down3, down2, down1} {
		if _, err := conn.Exec(ctx0, d); err != nil {
			t.Fatalf("pre-clean down: %v", err)
		}
	}
	for _, u := range ups {
		if _, err := conn.Exec(ctx0, u); err != nil {
			t.Fatalf("apply up: %v", err)
		}
	}
	t.Cleanup(func() {
		cctx, cc := context.WithTimeout(context.Background(), 60*time.Second)
		defer cc()
		for _, d := range []string{down4, down3, down2, down1} {
			_, _ = conn.Exec(cctx, d)
		}
	})

	// Seed a user + reservation in pending_reconciliation directly (no code path
	// needed; we test the migration guard, not the repository).
	if _, err := conn.Exec(ctx0, `INSERT INTO users (id, status) VALUES ('mig', 'active')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := conn.Exec(ctx0, `INSERT INTO quota_reservations
(id, user_id, request_id, billing_plan, status, settlement_status, reserved_requests, reserved_tokens, reserved_at)
VALUES ('mig1', 'mig', 'migrq', 'coding', 'pending_reconciliation', 'pending', 1, 0, now())`); err != nil {
		t.Fatalf("insert pending reservation: %v", err)
	}

	// Downgrade 000004 must FAIL while pending exists.
	if _, err := conn.Exec(ctx0, down4); err == nil {
		t.Fatalf("down 000004 must fail with pending rows, got nil")
	}
	// Schema must be intact after the failed downgrade: the partial unique
	// index, the pending CHECK value and the 000004-only columns remain, and
	// the reservation is unchanged (down must not mutate on failure).
	assertSchemaIntact000004(t, ctx0, conn)
	var st string
	if err := conn.QueryRow(ctx0, `SELECT status FROM quota_reservations WHERE id = 'mig1'`).Scan(&st); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if st != "pending_reconciliation" {
		t.Fatalf("status = %q, want pending_reconciliation (down must not mutate on failure)", st)
	}

	// Resolve the pending row to a terminal state the pre-000004 schema can
	// express: a finalized reservation with all 000004-only columns cleared.
	// (The ledger is never touched here — this test inserted no ledger rows.)
	if _, err := conn.Exec(ctx0, `UPDATE quota_reservations
SET status = 'finalized', settlement_status = NULL, usage_known = false,
    reconciled_at = NULL, idempotency_payload_hash = NULL
WHERE id = 'mig1'`); err != nil {
		t.Fatalf("resolve pending: %v", err)
	}
	if _, err := conn.Exec(ctx0, down4); err != nil {
		t.Fatalf("down 000004 must succeed after resolving pending, got %v", err)
	}
	// Re-apply 000004 up so subsequent test cleanup down works.
	if _, err := conn.Exec(ctx0, ups[3]); err != nil {
		t.Fatalf("re-apply up 000004: %v", err)
	}
}

// TestMigration_Down_FailClosed_OnReconcileSweepLedger verifies the fail-closed
// downgrade refuses to revert when usage_ledger rows of ledger_type
// reconcile/sweep exist: the pre-000004 CHECK rejects those types and the
// ledger is never deleted. The guard runs before any DDL, so on failure the
// schema (index/columns/check) stays intact. After removing the test-only
// ledger rows the downgrade succeeds.
func TestMigration_Down_FailClosed_OnReconcileSweepLedger(t *testing.T) {
	d := dsn(t)
	ctx0, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx0, d)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	migrationsDir := filepath.Join("..", "..", "migrations")
	ups := []string{
		readMigration(t, migrationsDir, "000001_init.up.sql"),
		readMigration(t, migrationsDir, "000002_limit_overrides.up.sql"),
		readMigration(t, migrationsDir, "000003_plan_daily_weekly_categories.up.sql"),
		readMigration(t, migrationsDir, "000004_settlement_state_machine.up.sql"),
	}
	down4 := readMigration(t, migrationsDir, "000004_settlement_state_machine.down.sql")
	down3 := readMigration(t, migrationsDir, "000003_plan_daily_weekly_categories.down.sql")
	down2 := readMigration(t, migrationsDir, "000002_limit_overrides.down.sql")
	down1 := readMigration(t, migrationsDir, "000001_init.down.sql")
	for _, d := range []string{down4, down3, down2, down1} {
		if _, err := conn.Exec(ctx0, d); err != nil {
			t.Fatalf("pre-clean down: %v", err)
		}
	}
	for _, u := range ups {
		if _, err := conn.Exec(ctx0, u); err != nil {
			t.Fatalf("apply up: %v", err)
		}
	}
	t.Cleanup(func() {
		cctx, cc := context.WithTimeout(context.Background(), 60*time.Second)
		defer cc()
		for _, d := range []string{down4, down3, down2, down1} {
			_, _ = conn.Exec(cctx, d)
		}
	})

	if _, err := conn.Exec(ctx0, `INSERT INTO users (id, status) VALUES ('migl', 'active')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// Insert a reconcile ledger row directly (no code path: we test the guard).
	if _, err := conn.Exec(ctx0, `INSERT INTO usage_ledger
(user_id, request_id, billing_plan, ledger_type, request_delta, token_delta, reason, idempotency_key)
VALUES ('migl', 'migrq-l', 'coding', 'reconcile', -1, -10, 'reconcile', 'ik-reconcile')`); err != nil {
		t.Fatalf("insert reconcile ledger: %v", err)
	}

	// Down must FAIL while a reconcile ledger row exists.
	if _, err := conn.Exec(ctx0, down4); err == nil {
		t.Fatalf("down 000004 must fail with reconcile ledger rows, got nil")
	}
	assertSchemaIntact000004(t, ctx0, conn)
	var lt string
	if err := conn.QueryRow(ctx0, `SELECT ledger_type FROM usage_ledger WHERE idempotency_key = 'ik-reconcile'`).Scan(&lt); err != nil {
		t.Fatalf("query ledger_type: %v", err)
	}
	if lt != "reconcile" {
		t.Fatalf("ledger_type = %q, want reconcile (down must not mutate ledger on failure)", lt)
	}

	// Add a sweep ledger row too; still fails.
	if _, err := conn.Exec(ctx0, `INSERT INTO usage_ledger
(user_id, request_id, billing_plan, ledger_type, request_delta, token_delta, reason, idempotency_key)
VALUES ('migl', 'migrq-s', 'coding', 'sweep', 1, 5, 'sweep', 'ik-sweep')`); err != nil {
		t.Fatalf("insert sweep ledger: %v", err)
	}
	if _, err := conn.Exec(ctx0, down4); err == nil {
		t.Fatalf("down 000004 must fail with sweep ledger rows, got nil")
	}
	assertSchemaIntact000004(t, ctx0, conn)

	// Remove the test-only ledger rows (this is test data, not production
	// ledger — in production the operator cannot delete the ledger and must
	// instead reconcile/retention-resolve until no reconcile/sweep rows remain
	// at the point of downgrade, which is exactly why the guard exists). After
	// removal the downgrade succeeds.
	if _, err := conn.Exec(ctx0, `DELETE FROM usage_ledger WHERE idempotency_key IN ('ik-reconcile', 'ik-sweep')`); err != nil {
		t.Fatalf("delete test ledger rows: %v", err)
	}
	if _, err := conn.Exec(ctx0, down4); err != nil {
		t.Fatalf("down 000004 must succeed after removing reconcile/sweep ledger, got %v", err)
	}
	if _, err := conn.Exec(ctx0, ups[3]); err != nil {
		t.Fatalf("re-apply up 000004: %v", err)
	}
}

// TestMigration_Down_FailClosed_OnSettledColumns verifies the fail-closed
// downgrade refuses to revert when any reservation carries a non-default
// value in a 000004-only column (settlement_status, usage_known=true,
// reconciled_at, idempotency_payload_hash) — evidence a settlement/reconcile
// happened that the old schema has no place to record. After clearing those
// columns the downgrade succeeds.
func TestMigration_Down_FailClosed_OnSettledColumns(t *testing.T) {
	d := dsn(t)
	ctx0, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx0, d)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	migrationsDir := filepath.Join("..", "..", "migrations")
	ups := []string{
		readMigration(t, migrationsDir, "000001_init.up.sql"),
		readMigration(t, migrationsDir, "000002_limit_overrides.up.sql"),
		readMigration(t, migrationsDir, "000003_plan_daily_weekly_categories.up.sql"),
		readMigration(t, migrationsDir, "000004_settlement_state_machine.up.sql"),
	}
	down4 := readMigration(t, migrationsDir, "000004_settlement_state_machine.down.sql")
	down3 := readMigration(t, migrationsDir, "000003_plan_daily_weekly_categories.down.sql")
	down2 := readMigration(t, migrationsDir, "000002_limit_overrides.down.sql")
	down1 := readMigration(t, migrationsDir, "000001_init.down.sql")
	for _, d := range []string{down4, down3, down2, down1} {
		if _, err := conn.Exec(ctx0, d); err != nil {
			t.Fatalf("pre-clean down: %v", err)
		}
	}
	for _, u := range ups {
		if _, err := conn.Exec(ctx0, u); err != nil {
			t.Fatalf("apply up: %v", err)
		}
	}
	t.Cleanup(func() {
		cctx, cc := context.WithTimeout(context.Background(), 60*time.Second)
		defer cc()
		for _, d := range []string{down4, down3, down2, down1} {
			_, _ = conn.Exec(cctx, d)
		}
	})

	if _, err := conn.Exec(ctx0, `INSERT INTO users (id, status) VALUES ('migs', 'active')`); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// A finalized reservation with settlement_status='settled' (and other
	// 000004-only columns set) — the old schema cannot express this.
	if _, err := conn.Exec(ctx0, `INSERT INTO quota_reservations
(id, user_id, request_id, billing_plan, status, settlement_status, usage_known, reconciled_at, idempotency_payload_hash, reserved_requests, reserved_tokens, reserved_at)
VALUES ('migs1', 'migs', 'migrq-s', 'coding', 'finalized', 'settled', true, now(), 'hash', 1, 0, now())`); err != nil {
		t.Fatalf("insert settled reservation: %v", err)
	}

	if _, err := conn.Exec(ctx0, down4); err == nil {
		t.Fatalf("down 000004 must fail with settled columns, got nil")
	}
	assertSchemaIntact000004(t, ctx0, conn)
	var ss string
	if err := conn.QueryRow(ctx0, `SELECT settlement_status FROM quota_reservations WHERE id = 'migs1'`).Scan(&ss); err != nil {
		t.Fatalf("query settlement_status: %v", err)
	}
	if ss != "settled" {
		t.Fatalf("settlement_status = %q, want settled (down must not mutate on failure)", ss)
	}

	// Clear all 000004-only columns to express the row in the old schema.
	if _, err := conn.Exec(ctx0, `UPDATE quota_reservations
SET settlement_status = NULL, usage_known = false, reconciled_at = NULL, idempotency_payload_hash = NULL
WHERE id = 'migs1'`); err != nil {
		t.Fatalf("clear settled columns: %v", err)
	}
	if _, err := conn.Exec(ctx0, down4); err != nil {
		t.Fatalf("down 000004 must succeed after clearing settled columns, got %v", err)
	}
	if _, err := conn.Exec(ctx0, ups[3]); err != nil {
		t.Fatalf("re-apply up 000004: %v", err)
	}
}

// assertSchemaIntact000004 verifies that a failed downgrade left the 000004
// schema fully intact: the partial unique index, the pending CHECK value, the
// 000004-only columns and the reconcile/sweep ledger_type CHECK all remain.
// This guards against a non-transactional executor leaving the schema
// half-reverted.
func assertSchemaIntact000004(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	// Partial unique index still exists.
	var idxExists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_indexes
		WHERE schemaname = current_schema() AND indexname = 'quota_reservations_request_active_uidx')`).Scan(&idxExists); err != nil || !idxExists {
		t.Fatalf("active-hold unique index missing after failed down: exists=%v err=%v", idxExists, err)
	}
	// 000004-only columns still exist.
	for _, col := range []string{"usage_known", "settlement_status", "reconciled_at", "idempotency_payload_hash"} {
		var colExists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'quota_reservations' AND column_name = $1)`, col).Scan(&colExists); err != nil || !colExists {
			t.Fatalf("column %s missing after failed down: exists=%v err=%v", col, colExists, err)
		}
	}
	// pending_reconciliation still accepted by the status CHECK.
	var allowsPending bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conname = 'quota_reservations_status_chk'
		AND pg_get_constraintdef(oid) LIKE '%pending_reconciliation%')`).Scan(&allowsPending); err != nil || !allowsPending {
		t.Fatalf("status CHECK does not allow pending_reconciliation after failed down: allows=%v err=%v", allowsPending, err)
	}
	// reconcile/sweep still accepted by the ledger_type CHECK.
	var allowsReconcile bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conname = 'usage_ledger_ledger_type_chk'
		AND pg_get_constraintdef(oid) LIKE '%reconcile%')`).Scan(&allowsReconcile); err != nil || !allowsReconcile {
		t.Fatalf("ledger_type CHECK does not allow reconcile after failed down: allows=%v err=%v", allowsReconcile, err)
	}
}

// TestReconcile_KnownCountsAgainstAllWindows verifies that a confirmed
// reconcile (usageKnown=true) of a pending reservation writes a 'reconcile'
// ledger row with a negative request_delta that counts against ALL three
// coding windows (hour5/weekly/period) on subsequent Reserve calls. This is
// the settlement-semantics fix: consumedCodingSince must count charge PLUS
// confirmed reconcile, so a deferred confirmed settlement cannot punch
// through a hard limit.
//
// Per-window variant: each subtest sets up a plan whose only tight window is
// the one under test (the others are wide open), reserves the limit, marks
// pending, confirmed-reconciles it, then asserts a fresh Reserve for the
// SAME window is rejected with the matching scope.
func TestReconcile_KnownCountsAgainstAllWindows(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hourly  *int
		weekly  *int
		monthly *int
		scope   string
	}{
		{"hour5", intPtr(1), intPtr(1000000), intPtr(1000000), "hour5"},
		{"weekly", intPtr(1000000), intPtr(1), intPtr(1000000), "weekly"},
		{"period", intPtr(1000000), intPtr(1000000), intPtr(1), "period"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dsn(t)
			applyMigrations(t, d)
			db := openDB(t, d)
			insertUser(t, db, "rc-"+tc.name)
			planID := insertCodingPlan(t, db, "rcplan-"+tc.name, tc.hourly, tc.weekly, tc.monthly)
			insertUserPlanActivated(t, db, "rc-"+tc.name, planID, "active", time.Now().UTC())
			r := New(db)
			ctx := context.Background()

			// Reserve the full window (limit 1) — succeeds.
			if err := r.Reserve(ctx, "rc-r1-"+tc.name, "rc-"+tc.name, "rc-req1-"+tc.name, "coding", 1, 1, nil); err != nil {
				t.Fatalf("reserve r1: %v", err)
			}
			if err := r.MarkPending(ctx, "rc-r1-"+tc.name); err != nil {
				t.Fatalf("mark pending r1: %v", err)
			}
			// Confirmed reconcile (usageKnown=true) writes a 'reconcile' row with
			// negative request_delta that must count as consumption.
			if err := r.Reconcile(ctx, "rc-r1-"+tc.name, 1, 10, true); err != nil {
				t.Fatalf("reconcile known r1: %v", err)
			}
			// A fresh Reserve for a DIFFERENT request_id in the same window must
			// now be rejected — the confirmed reconcile counted.
			err := r.Reserve(ctx, "rc-r2-"+tc.name, "rc-"+tc.name, "rc-req2-"+tc.name, "coding", 1, 1, nil)
			if !errors.Is(err, ErrQuotaExceeded) {
				t.Fatalf("expected ErrQuotaExceeded after confirmed reconcile, got %v", err)
			}
			if s := quotaExceededScope(err); s != tc.scope {
				t.Fatalf("scope = %q, want %s", s, tc.scope)
			}

			// Verify the reconcile ledger row exists with negative request_delta
			// (consumption), so the audit trail confirms the accounting source.
			var recDelta int
			if err := db.Raw(`SELECT request_delta FROM usage_ledger
WHERE user_id = ? AND ledger_type = 'reconcile' AND reason = 'reconcile'
ORDER BY id DESC LIMIT 1`, "rc-"+tc.name).Scan(&recDelta).Error; err != nil {
				t.Fatalf("query reconcile delta: %v", err)
			}
			if recDelta >= 0 {
				t.Fatalf("reconcile request_delta = %d, want negative (consumption)", recDelta)
			}
		})
	}
}

// TestReconcile_UnknownDoesNotCount verifies that an unknown reconcile
// (usageKnown=false, a release) writes a 'reconcile' row with a POSITIVE
// request_delta (reversal) and does NOT count as consumption — the user can
// still reserve after it. This guards against regressing consumedCodingSince
// to sum ALL reconcile rows.
func TestReconcile_UnknownDoesNotCount(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "ru3")
	lim := 1
	planID := insertCodingPlan(t, db, "ruplan", &lim, &lim, &lim)
	insertUserPlanActivated(t, db, "ru3", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "ru3-r1", "ru3", "ru3-req1", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve r1: %v", err)
	}
	if err := r.MarkPending(ctx, "ru3-r1"); err != nil {
		t.Fatalf("mark pending r1: %v", err)
	}
	// Unknown reconcile = release (positive delta, reversal).
	if err := r.Reconcile(ctx, "ru3-r1", 0, 0, false); err != nil {
		t.Fatalf("reconcile unknown r1: %v", err)
	}
	// The unknown reconcile reversed the hold, so a fresh Reserve must succeed
	// (the window is free again).
	if err := r.Reserve(ctx, "ru3-r2", "ru3", "ru3-req2", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve r2 after unknown reconcile should succeed, got %v", err)
	}
	// The reconcile-unknown row must carry a positive request_delta.
	var recDelta int
	if err := db.Raw(`SELECT request_delta FROM usage_ledger
WHERE user_id = ? AND ledger_type = 'reconcile' AND reason = 'reconcile-unknown'
ORDER BY id DESC LIMIT 1`, "ru3").Scan(&recDelta).Error; err != nil {
		t.Fatalf("query reconcile-unknown delta: %v", err)
	}
	if recDelta <= 0 {
		t.Fatalf("reconcile-unknown request_delta = %d, want positive (reversal)", recDelta)
	}
}

// TestRequestLevelUniqueConstraint verifies that a second active hold for the
// same request_id is rejected by the partial unique index (concurrent穿透 guard).
func TestRequestLevelUniqueConstraint(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "uq")
	setupCodingEntitlement(t, db, "uq")
	r := New(db)
	ctx := context.Background()

	if err := r.Reserve(ctx, "rq-a", "uq", "shared-req", "coding", 1, 1, nil); err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	// Same request_id, different reservation id — must fail at the DB unique
	// index (mapped to ErrInsertFailed).
	if err := r.Reserve(ctx, "rq-b", "uq", "shared-req", "coding", 1, 1, nil); err == nil {
		t.Fatalf("expected error for duplicate active request_id, got nil")
	}
}
