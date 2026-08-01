package migrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
)

// dsn returns the test DSN from env, skipping the test when unset. CI runs
// this against an ephemeral PG; locally it is skipped so `go test ./...` works
// without a database.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("CONFIG_REPO_TEST_DSN")
	if d == "" {
		t.Skip("CONFIG_REPO_TEST_DSN not set; skipping migration integration test")
	}
	return d
}

// migrationsDir is the directory holding the golang-migrate .sql files (same
// directory as this test file).
const migrationsDir = "."

// execFile executes a .sql migration file against conn within a single
// transaction, faithfully modelling golang-migrate's default behavior (each
// migration file runs in one transaction; any error rolls the whole file
// back). A returned error means the migration failed (e.g. a preflight guard
// raised), which is exactly what the fail-closed test asserts.
func execFile(t *testing.T, ctx context.Context, conn *pgx.Conn, name string) error {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(migrationsDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx for %s: %v", name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // noop after commit / on abort
	if _, err := tx.Exec(ctx, string(b)); err != nil {
		return err // tx rolled back by defer; no partial DDL persists
	}
	return tx.Commit(ctx)
}

// applyAllUp runs 000001..000004 up in order on a clean (or dirty) DB. It first
// runs all down migrations in reverse so the DB starts in a known-empty state.
func applyAllUp(t *testing.T, ctx context.Context, conn *pgx.Conn) {
	t.Helper()
	downFiles := []string{
		"000004_publish_hardening.down.sql",
		"000003_limits_and_routing_policy.down.sql",
		"000002_credential_plaintext.down.sql",
		"000001_init.down.sql",
	}
	for _, f := range downFiles {
		if err := execFile(t, ctx, conn, f); err != nil {
			t.Fatalf("cleanup down %s: %v", f, err)
		}
	}
	upFiles := []string{
		"000001_init.up.sql",
		"000002_credential_plaintext.up.sql",
		"000003_limits_and_routing_policy.up.sql",
		"000004_publish_hardening.up.sql",
	}
	for _, f := range upFiles {
		if err := execFile(t, ctx, conn, f); err != nil {
			t.Fatalf("apply up %s: %v", f, err)
		}
	}
	// Leave the DB clean for sibling tests in the same CI run (other packages
	// share the ephemeral PG). Errors are ignored: 000004 down may raise a
	// preflight guard on data this test created, but 000001 down (DROP TABLE
	// IF EXISTS) still drops everything at the end.
	t.Cleanup(func() {
		for _, f := range downFiles {
			b, _ := os.ReadFile(filepath.Join(migrationsDir, f))
			_, _ = conn.Exec(ctx, string(b))
		}
	})
}

// columnExists reports whether a column exists on a table.
func columnExists(ctx context.Context, conn *pgx.Conn, table, column string) bool {
	var ok bool
	_ = conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		  WHERE table_schema='public' AND table_name=$1 AND column_name=$2)`,
		table, column).Scan(&ok)
	return ok
}

// TestMigration_000004Down_FailClosedOnParentlessDraft verifies the down
// migration aborts BEFORE any destructive DDL when data exists that the
// pre-000004 schema cannot express (here: a parentless draft). After the
// aborted down, all 000004 schema (version column, source_revision_id, the
// singleton index, the audit columns) must still be present. Once the
// offending data is removed, the down must succeed.
func TestMigration_000004Down_FailClosedOnParentlessDraft(t *testing.T) {
	d := dsn(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	applyAllUp(t, ctx, conn)

	// Insert a parentless draft — valid under 000004 (parent_revision_id is
	// nullable) but inexpressible in the pre-000004 schema (bigserial NOT NULL).
	var draftID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO config_revisions (revision, status, version, created_by)
		 VALUES ('parentless-1', 'draft', 1, 'tester') RETURNING id`,
	).Scan(&draftID); err != nil {
		t.Fatalf("insert parentless draft: %v", err)
	}

	// Attempt the down migration — it MUST fail closed (preflight guard 1).
	downErr := execFile(t, ctx, conn, "000004_publish_hardening.down.sql")
	if downErr == nil {
		t.Fatal("down 000004 unexpectedly succeeded with a parentless draft present; preflight guard missing")
	}

	// Fail-closed contract: the aborted down must leave 000004 schema intact.
	// (golang-migrate wraps the file in a transaction; a raised exception rolls
	// back the whole migration.)
	if !columnExists(ctx, conn, "config_revisions", "version") {
		t.Fatal("version column dropped despite preflight abort; down was not atomic")
	}
	if !columnExists(ctx, conn, "config_revisions", "source_revision_id") {
		t.Fatal("source_revision_id column dropped despite preflight abort; down was not atomic")
	}
	if !columnExists(ctx, conn, "config_audit_log", "actor_kind") {
		t.Fatal("actor_kind column dropped despite preflight abort; down was not atomic")
	}
	var singletonIdx bool
	_ = conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname='public'
		  AND indexname='config_revisions_single_published_uidx')`).Scan(&singletonIdx)
	if !singletonIdx {
		t.Fatal("singleton published index dropped despite preflight abort; down was not atomic")
	}
	// The parentless draft must still exist (not silently removed).
	var n int64
	_ = conn.QueryRow(ctx,
		`SELECT count(*) FROM config_revisions WHERE id=$1 AND parent_revision_id IS NULL`, draftID).Scan(&n)
	if n != 1 {
		t.Fatal("parentless draft was silently removed by the aborted down; history must not be lost")
	}

	// Convert the data to a form the old schema can express: give the draft a
	// parent (create a parent revision and link it).
	var parentID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO config_revisions (revision, status, version, created_by)
		 VALUES ('parent-1', 'archived', 1, 'tester') RETURNING id`,
	).Scan(&parentID); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`UPDATE config_revisions SET parent_revision_id=$1 WHERE id=$2`, parentID, draftID); err != nil {
		t.Fatalf("link parent: %v", err)
	}

	// Now the down must succeed (no remaining parentless rows).
	if err := execFile(t, ctx, conn, "000004_publish_hardening.down.sql"); err != nil {
		t.Fatalf("down 000004 should succeed after converting parentless data: %v", err)
	}
	// Verify the revert took effect: version column gone, parent_revision_id
	// back to NOT NULL.
	if columnExists(ctx, conn, "config_revisions", "version") {
		t.Fatal("version column still present after successful down")
	}
	var isNullable string
	_ = conn.QueryRow(ctx,
		`SELECT is_nullable FROM information_schema.columns
		  WHERE table_schema='public' AND table_name='config_revisions' AND column_name='parent_revision_id'`,
	).Scan(&isNullable)
	if isNullable != "NO" {
		t.Fatalf("parent_revision_id should be NOT NULL after down, got is_nullable=%q", isNullable)
	}
}

// TestMigration_000004Down_FailClosedOnRollbackAudit verifies the down aborts
// when an audit row uses the 000004-added 'rollback_publish' action (which the
// pre-000004 action enum cannot express), and that the schema stays intact.
func TestMigration_000004Down_FailClosedOnRollbackAudit(t *testing.T) {
	d := dsn(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	applyAllUp(t, ctx, conn)

	// Create a published revision (rollback source) then an audit row with the
	// 000004-only action and audit metadata.
	var revID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO config_revisions (revision, status, version, created_by, source_revision_id)
		 VALUES ('rb-src', 'published', 1, 'tester', NULL) RETURNING id`,
	).Scan(&revID); err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	if _, err := conn.Exec(ctx,
		`INSERT INTO config_audit_log (revision_id, actor, actor_kind, action, entity_type, request_id)
		 VALUES ($1, 'tester', 'admin', 'rollback_publish', 'config_revision', 'req-1')`, revID); err != nil {
		t.Fatalf("insert rollback_publish audit: %v", err)
	}

	// Down must fail closed on the rollback_publish action (guard 4) — before
	// any destructive DDL.
	if err := execFile(t, ctx, conn, "000004_publish_hardening.down.sql"); err == nil {
		t.Fatal("down 000004 unexpectedly succeeded with a rollback_publish audit row; preflight guard missing")
	}
	// Schema still intact.
	if !columnExists(ctx, conn, "config_audit_log", "actor_kind") {
		t.Fatal("actor_kind column dropped despite preflight abort; down was not atomic")
	}
}

// TestMigration_000004Down_CleanDBSucceeds verifies the down is idempotent on a
// schema that has 000004 applied but no offending data (the normal happy path).
func TestMigration_000004Down_CleanDBSucceeds(t *testing.T) {
	d := dsn(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	applyAllUp(t, ctx, conn)

	// No data added; down should succeed cleanly.
	if err := execFile(t, ctx, conn, "000004_publish_hardening.down.sql"); err != nil {
		t.Fatalf("down 000004 should succeed on a clean DB: %v", err)
	}
}
