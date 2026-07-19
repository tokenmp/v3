//go:build integration

// Package integration contains integration tests for the auth service that
// require a real PostgreSQL instance. They are skipped in normal `go test`
// runs and executed by the GitHub Actions Go job using a PostgreSQL 17
// service container.
//
// Build with: go test -tags=integration ./test/integration/...
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/tokenmp/v3/services/auth/internal/database"
	"github.com/tokenmp/v3/services/auth/internal/server"
)

// dbDSN returns the test database URL. It must point at a database dedicated
// to tests; CI provisions a fresh postgres container.
func dbDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUTH_DATABASE_URL not set; skipping integration test")
	}
	return dsn
}

func openDB(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// migrateDownThenUp runs golang-migrate down then up. Each schema test starts
// with this so it runs against a clean, freshly-migrated database — there is
// no manual table dropping between tests. The migration cycle itself is the
// authoritative reset.
func migrateDownThenUp(t *testing.T, migrationsURL string, dsn string) {
	t.Helper()
	if err := runMigrate("down", migrationsURL, dsn, -1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := runMigrate("up", migrationsURL, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}

func migrationsPath(t *testing.T) string {
	t.Helper()
	url := os.Getenv("AUTH_MIGRATIONS_URL")
	if url != "" {
		return url
	}
	// Resolve absolute path relative to this test file so the migrate CLI can
	// find the migration files regardless of the test working directory.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	abs := filepath.Join(dir, "..", "..", "migrations")
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		t.Fatalf("migrations dir not found at %s: %v", abs, err)
	}
	return abs
}

func TestMigrations_UpAndDown(t *testing.T) {
	dsn := dbDSN(t)
	migrationsURL := migrationsPath(t)

	// Up first.
	if err := runMigrate("up", migrationsURL, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	// Down to zero.
	if err := runMigrate("down", migrationsURL, dsn, -1); err != nil {
		t.Fatalf("migrate down to zero: %v", err)
	}
	// Up again to prove idempotency.
	if err := runMigrate("up", migrationsURL, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}

// TestSchema_UsersDefaultsAndConstraints verifies the users table defaults,
// CHECK constraints and the email normalization invariant. The stored email
// must equal LOWER(BTRIM(email)) and be non-empty; password_hash is TEXT with
// a non-empty CHECK (compatible with the legacy production TEXT column).
func TestSchema_UsersDefaultsAndConstraints(t *testing.T) {
	dsn := dbDSN(t)
	migrationsURL := migrationsPath(t)
	migrateDownThenUp(t, migrationsURL, dsn)
	db := openDB(t, dsn)

	// Default role must be 'user', status 'active', token_version 1. Email
	// must be inserted already-normalized (the CHECK rejects anything else).
	var role, status string
	var tokenVersion int
	row := db.QueryRow(`
		INSERT INTO users (email, password_hash) VALUES ('foo@example.com', '$2a$10$abc')
		RETURNING role, status, token_version
	`)
	if err := row.Scan(&role, &status, &tokenVersion); err != nil {
		t.Fatalf("insert default user: %v", err)
	}
	if role != "user" {
		t.Errorf("default role = %q, want user", role)
	}
	if status != "active" {
		t.Errorf("default status = %q, want active", status)
	}
	if tokenVersion != 1 {
		t.Errorf("default token_version = %d, want 1", tokenVersion)
	}

	// Invalid role must be rejected.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash, role) VALUES ('a@b.com','$2a$10$abc','superuser')`); err == nil {
		t.Error("expected CHECK violation for role='superuser'")
	}
	// Invalid status must be rejected.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash, status) VALUES ('c@d.com','$2a$10$abc','banned')`); err == nil {
		t.Error("expected CHECK violation for status='banned'")
	}
	// token_version < 1 must be rejected.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash, token_version) VALUES ('e@f.com','$2a$10$abc',0)`); err == nil {
		t.Error("expected CHECK violation for token_version=0")
	}

	// Email normalization invariant: stored value must equal LOWER(BTRIM(...)).
	// Mixed-case insert must be rejected by the CHECK (not by uniqueness).
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('Mixed@Example.COM','$2a$10$abc')`); err == nil {
		t.Error("expected CHECK violation for non-normalized email (mixed case)")
	}
	// Whitespace-padded insert must be rejected by the CHECK.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('  pad@example.com ','$2a$10$abc')`); err == nil {
		t.Error("expected CHECK violation for non-normalized email (whitespace)")
	}
	// Empty email must be rejected by the non-empty CHECK.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('','$2a$10$abc')`); err == nil {
		t.Error("expected CHECK violation for empty email")
	}

	// password_hash is TEXT: a value longer than 255 must be accepted (the
	// legacy column was TEXT, not VARCHAR(255)).
	longHash := strings.Repeat("x", 600)
	var stored string
	if err := db.QueryRow(`INSERT INTO users (email, password_hash) VALUES ('longhash@example.com',$1) RETURNING password_hash`, longHash).Scan(&stored); err != nil {
		t.Fatalf("insert long password_hash (TEXT) failed: %v", err)
	}
	if stored != longHash {
		t.Errorf("long password_hash round-trip mismatch (len got=%d want=%d)", len(stored), len(longHash))
	}
	// password_hash non-empty CHECK rejects the empty string.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('empty@example.com','')`); err == nil {
		t.Error("expected CHECK violation for empty password_hash")
	}
}

// TestSchema_EmailUniqueNormalized verifies the unique index on
// LOWER(BTRIM(email)). Because the CHECK forces the stored value to already
// be normalized, uniqueness is effectively on the canonical form.
func TestSchema_EmailUniqueNormalized(t *testing.T) {
	dsn := dbDSN(t)
	migrationsURL := migrationsPath(t)
	migrateDownThenUp(t, migrationsURL, dsn)
	db := openDB(t, dsn)

	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('user@example.com', '$2a$10$abc')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// Same normalized email must violate the unique index.
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('user@example.com', '$2a$10$def')`); err == nil {
		t.Fatal("expected unique violation for duplicate normalized email")
	}
}

// TestSchema_AuthSessionsConstraintsAndIndexes verifies the auth_sessions
// table: defaults, CHECK constraints (revoke_reason allow-list, revoked
// consistency, refresh_token_hash non-empty, expires_at > created_at), the
// INET ip column, the TEXT user_agent column, the token_family default, the
// self-referential replaced_by_session_id FK, and unique indexes.
func TestSchema_AuthSessionsConstraintsAndIndexes(t *testing.T) {
	dsn := dbDSN(t)
	migrationsURL := migrationsPath(t)
	migrateDownThenUp(t, migrationsURL, dsn)
	db := openDB(t, dsn)

	var userID string
	err := db.QueryRow(`INSERT INTO users (email, password_hash) VALUES ('sess@example.com','$2a$10$abc') RETURNING id`).
		Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	// Insert a valid session relying on token_family_id default
	// gen_random_uuid() and the default id.
	var sessionID, familyID string
	err = db.QueryRow(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, now() + interval '1 hour')
		RETURNING id, token_family_id
	`, userID, []byte("hash-a")).Scan(&sessionID, &familyID)
	if err != nil {
		t.Fatalf("insert session with default token_family_id: %v", err)
	}
	if familyID == "" {
		t.Error("token_family_id default gen_random_uuid() did not fire")
	}

	// Duplicate refresh_token_hash must violate uniqueness.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, now() + interval '1 hour')
	`, userID, []byte("hash-a"))
	if err == nil {
		t.Fatal("expected unique violation on duplicate refresh_token_hash")
	}

	// refresh_token_hash non-empty CHECK: empty bytea must be rejected.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, now() + interval '1 hour')
	`, userID, []byte{})
	if err == nil {
		t.Fatal("expected CHECK violation for empty refresh_token_hash")
	}

	// FK: inserting session with bogus user_id must fail.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at)
		VALUES ('00000000-0000-0000-0000-000000000000', $1, now() + interval '1 hour')
	`, []byte("hash-b"))
	if err == nil {
		t.Fatal("expected FK violation for non-existent user_id")
	}

	// CHECK (expires_at > created_at): expires_at <= created_at rejected.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, created_at)
		VALUES ($1, $2, now(), now() + interval '1 hour')
	`, userID, []byte("hash-c"))
	if err == nil {
		t.Fatal("expected CHECK violation for expires_at <= created_at")
	}

	// revoke_reason allow-list: an invalid reason must be rejected.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, revoked_at, revoke_reason)
		VALUES ($1, $2, now() + interval '1 hour', now(), 'bogus_reason')
	`, userID, []byte("hash-d"))
	if err == nil {
		t.Fatal("expected CHECK violation for invalid revoke_reason")
	}
	// A valid allow-list reason must be accepted.
	validReasons := []string{
		"logout", "logout_all", "password_changed", "admin_revoked",
		"token_rotated", "token_reuse", "user_disabled",
	}
	for i, r := range validReasons {
		_, err = db.Exec(`
			INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, revoked_at, revoke_reason)
			VALUES ($1, $2, now() + interval '1 hour', now(), $3)
		`, userID, []byte(fmt.Sprintf("hash-reason-%d", i)), r)
		if err != nil {
			t.Errorf("expected valid revoke_reason %q to be accepted, got: %v", r, err)
		}
	}

	// Consistency CHECK: revoked_at set but revoke_reason NULL must be rejected.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, revoked_at)
		VALUES ($1, $2, now() + interval '1 hour', now())
	`, userID, []byte("hash-e"))
	if err == nil {
		t.Fatal("expected CHECK violation for revoked_at set without revoke_reason")
	}
	// Consistency CHECK: revoke_reason set but revoked_at NULL must be rejected.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, revoke_reason)
		VALUES ($1, $2, now() + interval '1 hour', 'logout')
	`, userID, []byte("hash-f"))
	if err == nil {
		t.Fatal("expected CHECK violation for revoke_reason set without revoked_at")
	}

	// ip is INET: an invalid IP literal must be rejected.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, ip)
		VALUES ($1, $2, now() + interval '1 hour', 'not-an-ip')
	`, userID, []byte("hash-g"))
	if err == nil {
		t.Fatal("expected error for invalid INET ip value")
	}
	// A valid IPv4/IPv6 must be accepted.
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, ip)
		VALUES ($1, $2, now() + interval '1 hour', '203.0.113.7')
	`, userID, []byte("hash-h"))
	if err != nil {
		t.Fatalf("expected valid IPv4 ip to be accepted, got: %v", err)
	}

	// user_agent is TEXT: a value longer than the old VARCHAR(512) cap must be
	// accepted.
	longUA := strings.Repeat("M", 600)
	_, err = db.Exec(`
		INSERT INTO auth_sessions (user_id, refresh_token_hash, expires_at, user_agent)
		VALUES ($1, $2, now() + interval '1 hour', $3)
	`, userID, []byte("hash-i"), longUA)
	if err != nil {
		t.Fatalf("expected long user_agent (TEXT) to be accepted, got: %v", err)
	}

	// replaced_by_session_id self-FK preserved for future rotation semantics.
	// The column reads "this row was replaced BY session <id>": on rotation
	// the OLD row is updated to point at the NEW row and revoked with
	// revoke_reason='token_rotated'. The new row never carries
	// replaced_by_session_id.
	//
	// Create a new (replacement) session in the same token_family, then
	// UPDATE the old row (sessionID) to point at it and revoke it.
	var newID string
	err = db.QueryRow(`
		INSERT INTO auth_sessions (user_id, token_family_id, refresh_token_hash, expires_at)
		VALUES ($1, $2, $3, now() + interval '1 hour')
		RETURNING id
	`, userID, familyID, []byte("hash-rotated")).Scan(&newID)
	if err != nil {
		t.Fatalf("insert replacement session: %v", err)
	}

	// Rotate: the OLD row (sessionID) is replaced BY the NEW row (newID).
	res, err := db.Exec(`
		UPDATE auth_sessions
		SET replaced_by_session_id = $1, revoked_at = now(), revoke_reason = 'token_rotated'
		WHERE id = $2
	`, newID, sessionID)
	if err != nil {
		t.Fatalf("update old row replaced_by_session_id -> new row: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row updated, got %d", n)
	}

	// Verify the old row now points at the new row and is revoked consistently.
	var replacedBy *string
	var revokedAt *time.Time
	var revokeReason *string
	err = db.QueryRow(`
		SELECT replaced_by_session_id, revoked_at, revoke_reason
		FROM auth_sessions WHERE id = $1
	`, sessionID).Scan(&replacedBy, &revokedAt, &revokeReason)
	if err != nil {
		t.Fatalf("re-read old row: %v", err)
	}
	if replacedBy == nil || *replacedBy != newID {
		t.Errorf("old row replaced_by_session_id = %v, want %s", replacedBy, newID)
	}
	if revokedAt == nil {
		t.Error("old row revoked_at is NULL, want set")
	}
	if revokeReason == nil || *revokeReason != "token_rotated" {
		t.Errorf("old row revoke_reason = %v, want token_rotated", revokeReason)
	}

	// replaced_by_session_id pointing at a non-existent session must still
	// fail the FK constraint.
	_, err = db.Exec(`
		UPDATE auth_sessions
		SET replaced_by_session_id = '00000000-0000-0000-0000-000000000000'
		WHERE id = $1
	`, sessionID)
	if err == nil {
		t.Fatal("expected FK violation for non-existent replaced_by_session_id")
	}
}

// TestReadyz_HTTPReadyAndUnready drives the real HTTP /readyz path through
// httptest.NewServer(srv.Router()) against a live database, then closes the
// underlying pool and confirms a subsequent request returns 503 with no
// underlying error text in the body. It does not just call Ping directly.
func TestReadyz_HTTPReadyAndUnready(t *testing.T) {
	dsn := dbDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	gormDB, err := database.Open(ctx, database.Config{
		DatabaseURL:     dsn,
		MaxOpenConns:    5,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(gormDB) })

	pinger := database.PingerFromDB(gormDB)
	srv := server.New("127.0.0.1:0", pinger)
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)

	// Live DB → 200.
	resp, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz (live): %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live GET /readyz status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"status":"ok"`) {
		t.Errorf("live body = %s, want status ok", body)
	}

	// Close the underlying pool, then request again. The handler must return
	// 503 and must not include any underlying error text in the body.
	if err := database.Close(gormDB); err != nil {
		t.Fatalf("close db: %v", err)
	}

	resp2, err := http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz (closed): %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("closed GET /readyz status = %d, want 503", resp2.StatusCode)
	}
	if !strings.Contains(string(body2), `"status":"unready"`) {
		t.Errorf("closed body = %s, want status unready", body2)
	}
	for _, needle := range []string{"password", "secret", "sql:", "database is closed", "connection"} {
		if strings.Contains(strings.ToLower(string(body2)), strings.ToLower(needle)) {
			t.Errorf("503 body leaked underlying error text %q: %s", needle, body2)
		}
	}
}

// runMigrate invokes the golang-migrate CLI with the given command.
// The migrate binary must be on PATH (CI installs
// github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.3).
//
// Commands:
//
//	runMigrate("up", migrationsURL, dsn)
//	runMigrate("down", migrationsURL, dsn, -1)   // -1 == apply all down migrations
func runMigrate(command, migrationsURL, dsn string, extra ...int) error {
	bin := os.Getenv("AUTH_MIGRATE_BIN")
	if bin == "" {
		bin = "migrate"
	}
	args := []string{
		"-path", migrationsURL,
		"-database", dsn,
		command,
	}
	if command == "down" {
		// golang-migrate `down` requires a count argument or -all in older
		// versions; v4.18 supports `-all`. Use -all when no count given.
		if len(extra) == 0 {
			args = append(args, "-all")
		} else if extra[0] == -1 {
			args = append(args, "-all")
		} else {
			args = append(args, strconv.Itoa(extra[0]))
		}
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("migrate %s: %w; output: %s", strings.Join(args, " "), err, out)
	}
	return nil
}
