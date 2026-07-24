//go:build integration

// Package integration contains integration tests for the auth service that
// require a real PostgreSQL instance. They are skipped in normal `go test`
// runs and executed by the GitHub Actions Go job using a PostgreSQL 17
// service container.
//
// Build with: go test -tags=integration ./test/integration/...
package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/database"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/server"
	"github.com/tokenmp/v3/services/auth/internal/transport/authv1api"
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

// TestSchema_APIKeysConstraintsAndIndexes verifies the Auth-owned API-key
// schema that unifies legacy api_keys, user_api_keys, and bot_keys. It covers
// defaults, lifecycle constraints, hash uniqueness, ownership FK, expiry, and
// the updated_at trigger; plaintext API key material never enters the table.
func TestSchema_APIKeysConstraintsAndIndexes(t *testing.T) {
	dsn := dbDSN(t)
	migrationsURL := migrationsPath(t)
	migrateDownThenUp(t, migrationsURL, dsn)
	db := openDB(t, dsn)

	var userID string
	if err := db.QueryRow(`INSERT INTO users (email, password_hash) VALUES ('api-key-schema@example.com', '$2a$10$abc') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var keyID string
	var role, status string
	var createdAt, updatedAt time.Time
	err := db.QueryRow(`
		INSERT INTO api_keys (user_id, name, key_hash, key_prefix, key_suffix, created_at)
		VALUES ($1, 'default key', $2, 'tmp_display', 'play', now() - interval '1 hour')
		RETURNING id, role, status, created_at, updated_at
	`, userID, []byte("hash-a")).Scan(&keyID, &role, &status, &createdAt, &updatedAt)
	if err != nil {
		t.Fatalf("insert API key defaults: %v", err)
	}
	if role != "user" || status != "active" {
		t.Errorf("defaults role/status = %q/%q, want user/active", role, status)
	}
	if !updatedAt.After(createdAt) {
		t.Errorf("default updated_at = %v, want after explicit created_at %v", updatedAt, createdAt)
	}
	initialUpdatedAt := updatedAt

	if _, err := db.Exec(`UPDATE api_keys SET name = 'touched key', updated_at = created_at - interval '1 hour' WHERE id = $1`, keyID); err != nil {
		t.Fatalf("update API key: %v", err)
	}
	if err := db.QueryRow(`SELECT updated_at FROM api_keys WHERE id = $1`, keyID).Scan(&updatedAt); err != nil {
		t.Fatalf("read touched updated_at: %v", err)
	}
	if updatedAt.Before(initialUpdatedAt) {
		t.Errorf("trigger updated_at = %v, want no earlier than prior value %v", updatedAt, initialUpdatedAt)
	}

	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{"duplicate hash", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix) VALUES ($1,'duplicate',$2,'tmp_display','play')`, []any{userID, []byte("hash-a")}},
		{"empty name", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix) VALUES ($1,'',$2,'tmp_display','play')`, []any{userID, []byte("hash-b")}},
		{"empty hash", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix) VALUES ($1,'empty hash',$2,'tmp_display','play')`, []any{userID, []byte{}}},
		{"invalid role", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix,role) VALUES ($1,'bad role',$2,'tmp_display','play','owner')`, []any{userID, []byte("hash-c")}},
		{"invalid status", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix,status) VALUES ($1,'bad status',$2,'tmp_display','play','expired')`, []any{userID, []byte("hash-d")}},
		{"invalid expiry", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix,created_at,expires_at) VALUES ($1,'bad expiry',$2,'tmp_display','play',now(),now())`, []any{userID, []byte("hash-e")}},
		{"missing user", `INSERT INTO api_keys (user_id,name,key_hash,key_prefix,key_suffix) VALUES ('00000000-0000-0000-0000-000000000000','missing user',$1,'tmp_display','play')`, []any{[]byte("hash-f")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(tc.query, tc.args...); err == nil {
				t.Error("expected schema constraint violation")
			}
		})
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
	srv := server.New("127.0.0.1:0", pinger, nil, nil, nil)
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

// ---- Auth identity flow integration tests ----
//
// These tests exercise the full stack: real PostgreSQL + migrations + the
// GORM repository + the auth service + the Chi router + JWT Ed25519 keys
// generated in-memory per test process (no openssl, no committed key). They
// run only under the `integration` build tag in CI against a fresh
// postgres:17-alpine service container; never on developer machines.

// newAuthStack builds a fresh auth.Service wired to a live GORM DB and an
// in-memory Ed25519 JWT key pair. It returns an httptest server whose router
// exposes the health endpoints AND the auth identity flow routes. The
// migrations are reset (down then up) at the start so each test runs against a
// clean database.
func newAuthStack(t *testing.T, dsn string) (*httptest.Server, *auth.Service, *jwt.Issuer, *jwt.Verifier) {
	t.Helper()
	migrationsURL := migrationsPath(t)
	migrateDownThenUp(t, migrationsURL, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gdb, err := database.Open(ctx, database.Config{
		DatabaseURL:     dsn,
		MaxOpenConns:    10,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(gdb) })

	// Generate an Ed25519 key pair in-memory for this test process. No key is
	// read from disk and no private key is ever written to the repository.
	pub, priv, err := ed25519GenerateKey()
	if err != nil {
		t.Fatalf("ed25519 generate: %v", err)
	}
	kp := &jwt.KeyPair{Private: priv, Public: pub}
	issuer, err := jwt.NewIssuer(kp, "tokenmp-auth", "tokenmp-web", 15*time.Minute)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := jwt.NewVerifier(kp, "tokenmp-auth", "tokenmp-web")
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	userRepo := repository.NewUserRepository(gdb)
	sessionRepo := repository.NewSessionRepository(gdb)
	txRunner := repository.NewTxRunner(gdb)
	clock := realClockUTC{}
	svc := auth.NewService(userRepo, sessionRepo, txRunner, issuer, clock, 15*time.Minute, 30*24*time.Hour)
	userStore := authv1api.NewUserRepoAdapter(userRepo)

	pinger := database.PingerFromDB(gdb)
	srv := server.New("127.0.0.1:0", pinger, verifier, svc, userStore)
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	return ts, svc, issuer, verifier
}

// realClockUTC implements auth.Clock.
type realClockUTC struct{}

func (realClockUTC) Now() time.Time { return time.Now().UTC() }

// authJSON issues a JSON request and decodes the response body (if any) into out.
func authJSON(t *testing.T, ts *httptest.Server, method, path, bearer string, body any, out any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do: %v", err)
	}
	if out != nil && resp.Body != nil {
		defer func() { _, _ = io.Copy(io.Discard, resp.Body) }()
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			t.Fatalf("decode body: %v", err)
		}
	}
	return resp
}

// mustReadBody closes the body and returns its bytes.
func mustReadBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return b
}

// TestAuthFlow_RegisterLoginRefreshReuseLogout drives the entire identity flow
// against a real database: register, login, refresh rotation, reuse detection
// (family revoked), logout idempotency, password change invalidation, and
// logout-all.
func TestAuthFlow_RegisterLoginRefreshReuseLogout(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	// Register a new user. No auto-login: response must not carry tokens.
	var regUser map[string]any
	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "user@example.com", "password": "verystrongpassword123"}, &regUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", resp.StatusCode, mustReadBody(t, resp))
	}
	if _, ok := regUser["access_token"]; ok {
		t.Fatal("register must not auto-login")
	}
	if regUser["email"] != "user@example.com" {
		t.Errorf("email=%v want normalized", regUser["email"])
	}

	// Duplicate register → 409.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "user@example.com", "password": "verystrongpassword123"}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status=%d want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Login → 200 with tokens.
	var login map[string]any
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "user@example.com", "password": "verystrongpassword123"}, &login)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", resp.StatusCode, mustReadBody(t, resp))
	}
	access1 := login["access_token"].(string)
	refresh1 := login["refresh_token"].(string)
	if access1 == "" || refresh1 == "" {
		t.Fatal("missing tokens in login response")
	}

	// /me with the access token → 200.
	var me map[string]any
	resp = authJSON(t, ts, http.MethodGet, "/api/v1/auth/me", access1, nil, &me)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status=%d body=%s", resp.StatusCode, mustReadBody(t, resp))
	}
	if me["email"] != "user@example.com" {
		t.Errorf("me email=%v", me["email"])
	}

	// Refresh rotation → new tokens; old refresh token revoked.
	var rot map[string]any
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"refresh_token": refresh1}, &rot)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status=%d body=%s", resp.StatusCode, mustReadBody(t, resp))
	}
	refresh2 := rot["refresh_token"].(string)
	if refresh2 == refresh1 {
		t.Fatal("refresh token not rotated")
	}

	// Reuse the original refresh token → 401 and family revoked.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"refresh_token": refresh1}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reuse status=%d want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The rotated token (refresh2) must now also be revoked (reuse revoked the
	// whole family), so refreshing with it also fails.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"refresh_token": refresh2}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("family-revoked refresh status=%d want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestAuthFlow_BcryptLegacyUpgrade seeds a legacy bcrypt user directly in the
// DB, logs in, and asserts the stored hash is upgraded to Argon2id without
// bumping token_version.
func TestAuthFlow_BcryptLegacyUpgrade(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	db := openDB(t, dsn)

	// Seed a legacy bcrypt hash via raw SQL (the legacy production column was
	// TEXT storing bcrypt).
	bcryptHash := mustBcryptHash(t, "legacypassword123")
	if _, err := db.Exec(`INSERT INTO users (email, password_hash) VALUES ('legacy@example.com', $1)`, bcryptHash); err != nil {
		t.Fatalf("seed bcrypt user: %v", err)
	}

	// Login with the legacy password.
	var login map[string]any
	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "legacy@example.com", "password": "legacypassword123"}, &login)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", resp.StatusCode, mustReadBody(t, resp))
	}
	_ = resp.Body.Close()

	// The stored hash must now be Argon2id.
	var stored string
	var tv int
	if err := db.QueryRow(`SELECT password_hash, token_version FROM users WHERE email='legacy@example.com'`).Scan(&stored, &tv); err != nil {
		t.Fatalf("re-read user: %v", err)
	}
	if !strings.HasPrefix(stored, "$argon2id$") {
		t.Errorf("stored hash not argon2id: %q", stored)
	}
	if tv != 1 {
		t.Errorf("token_version bumped on bcrypt upgrade: %d (must NOT bump)", tv)
	}
}

// TestAuthFlow_PasswordChangeInvalidatesAccess asserts that changing the
// password bumps token_version and revokes sessions, so the previous access
// token is immediately rejected.
func TestAuthFlow_PasswordChangeInvalidatesAccess(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	// Register + login.
	_ = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "pc@example.com", "password": "verystrongpassword123"}, nil)
	var login map[string]any
	authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "pc@example.com", "password": "verystrongpassword123"}, &login)
	access := login["access_token"].(string)

	// Change password with the valid access token.
	resp := authJSON(t, ts, http.MethodPut, "/api/v1/auth/password", access,
		map[string]string{"current_password": "verystrongpassword123", "new_password": "newverystrongpassword456"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("password change status=%d body=%s", resp.StatusCode, mustReadBody(t, resp))
	}

	// The previous access token must now be rejected (token_version bumped).
	resp = authJSON(t, ts, http.MethodGet, "/api/v1/auth/me", access, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale access after pw change status=%d want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The old refresh token is revoked (password_changed); refreshing fails.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"refresh_token": login["refresh_token"].(string)}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh after pw change status=%d want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// New login with the new password works.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "pc@example.com", "password": "newverystrongpassword456"}, &login)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("login with new password status=%d want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestAuthFlow_LogoutAllInvalidatesAccess asserts logout-all bumps
// token_version and revokes all sessions.
func TestAuthFlow_LogoutAllInvalidatesAccess(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	_ = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "la@example.com", "password": "verystrongpassword123"}, nil)
	var login map[string]any
	authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "la@example.com", "password": "verystrongpassword123"}, &login)
	access := login["access_token"].(string)

	// Second login to have two active sessions in different families.
	authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "la@example.com", "password": "verystrongpassword123"}, &login)

	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/logout-all", access, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout-all status=%d want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The access token must now be rejected (token_version bumped).
	resp = authJSON(t, ts, http.MethodGet, "/api/v1/auth/me", access, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("stale access after logout-all status=%d want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// All sessions revoked: refresh with either login's refresh token fails.
	db := openDB(t, dsn)
	var active int
	if err := db.QueryRow(`SELECT count(*) FROM auth_sessions WHERE user_id=(SELECT id FROM users WHERE email='la@example.com') AND revoked_at IS NULL`).Scan(&active); err != nil {
		t.Fatalf("count active: %v", err)
	}
	if active != 0 {
		t.Errorf("%d active sessions survived logout-all", active)
	}
}

// TestAuthFlow_ConcurrentRefreshReuse starts two concurrent refresh
// attempts against the same refresh token. Exactly one must succeed; the
// other must fail (and must not corrupt rotation state). This exercises the
// SELECT FOR UPDATE serialization at the DB boundary.
func TestAuthFlow_ConcurrentRefreshReuse(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	_ = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "conc@example.com", "password": "verystrongpassword123"}, nil)
	var login map[string]any
	authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "conc@example.com", "password": "verystrongpassword123"}, &login)
	rt := login["refresh_token"].(string)

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	failures := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
				map[string]string{"refresh_token": rt}, nil)
			mu.Lock()
			if resp.StatusCode == http.StatusOK {
				success++
			} else {
				failures++
			}
			mu.Unlock()
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()

	// Exactly one attempt must succeed (the row was locked; only one rotates).
	if success != 1 {
		t.Errorf("concurrent refresh: success=%d want 1 (failures=%d)", success, failures)
	}
	// The remaining attempts hit a revoked token → reuse path → 401.
	if success+failures != n {
		t.Errorf("total responses = %d want %d", success+failures, n)
	}
}

// TestAuthFlow_DisabledLoginRejected disables a user in the DB and confirms
// login returns the uniform invalid_credentials error.
func TestAuthFlow_DisabledLoginRejected(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	_ = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "dis@example.com", "password": "verystrongpassword123"}, nil)
	db := openDB(t, dsn)
	if _, err := db.Exec(`UPDATE users SET status='disabled' WHERE email='dis@example.com'`); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "dis@example.com", "password": "verystrongpassword123"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled login status=%d want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestAuthFlow_LogoutThenRefreshFails asserts that after a successful logout,
// the refresh token is revoked and cannot be used to obtain new tokens.
// Logout is idempotent (second logout also 204). Refreshing the revoked
// token returns a uniform 401 with no leak of revocation reason.
func TestAuthFlow_LogoutThenRefreshFails(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	// Register + login to get a refresh token.
	_ = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "lo@example.com", "password": "verystrongpassword123"}, nil)
	var login map[string]any
	authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "lo@example.com", "password": "verystrongpassword123"}, &login)
	rt := login["refresh_token"].(string)

	// POST logout → 204.
	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/logout", "",
		map[string]string{"refresh_token": rt}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first logout status=%d want 204", resp.StatusCode)
	}

	// POST logout again (idempotent) → 204.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/logout", "",
		map[string]string{"refresh_token": rt}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second logout status=%d want 204", resp.StatusCode)
	}

	// Refresh with the now-revoked token → 401.
	resp = authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"refresh_token": rt}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status=%d want 401", resp.StatusCode)
	}
	body := string(mustReadBody(t, resp))
	for _, needle := range []string{"logout", "revoked", "reuse", "pq:", "sql:", "pgconn"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(needle)) {
			t.Errorf("401 body leaked %q: %s", needle, body)
		}
	}
}

// TestAuthFlow_ExpiredRefreshFails asserts that an expired refresh token
// returns 401 with no leak of internal state. The session is expired by
// directly updating expires_at in the database to a past timestamp.
func TestAuthFlow_ExpiredRefreshFails(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)
	db := openDB(t, dsn)

	// Register + login.
	_ = authJSON(t, ts, http.MethodPost, "/api/v1/auth/register", "",
		map[string]string{"email": "exp@example.com", "password": "verystrongpassword123"}, nil)
	var login map[string]any
	authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "exp@example.com", "password": "verystrongpassword123"}, &login)
	rt := login["refresh_token"].(string)

	// Expire the session directly in the DB: set expires_at to a past
	// timestamp. The CHECK (expires_at > created_at) requires created_at
	// to be earlier, so we set both to past values that satisfy the
	// constraint.
	if _, err := db.Exec(`
		UPDATE auth_sessions
		SET expires_at = now() - interval '1 hour',
		    created_at = now() - interval '2 hours'
		WHERE user_id = (SELECT id FROM users WHERE email = 'exp@example.com')
		  AND revoked_at IS NULL
	`); err != nil {
		t.Fatalf("expire session in DB: %v", err)
	}

	// Refresh with the expired token → 401.
	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"refresh_token": rt}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired refresh status=%d want 401", resp.StatusCode)
	}
	body := string(mustReadBody(t, resp))
	for _, needle := range []string{"expires_at", "pq:", "sql:", "pgconn", "tokenmp_auth"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(needle)) {
			t.Errorf("401 body leaked %q: %s", needle, body)
		}
	}
}

// TestAuthFlow_NoRawDBErrorsLeaked asserts that auth failure responses do not
// contain raw Postgres / driver error text.
func TestAuthFlow_NoRawDBErrorsLeaked(t *testing.T) {
	dsn := dbDSN(t)
	ts, _, _, _ := newAuthStack(t, dsn)

	resp := authJSON(t, ts, http.MethodPost, "/api/v1/auth/login", "",
		map[string]string{"email": "ghost@example.com", "password": "whateverpassword"}, nil)
	body := string(mustReadBody(t, resp))
	for _, n := range []string{"pq:", "sql:", "pgconn", "23505", "tokenmp_auth", "password_hash"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(n)) {
			t.Errorf("response leaked %q: %s", n, body)
		}
	}
}
