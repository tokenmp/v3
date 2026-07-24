package repository

import (
	"context"
	"encoding/json"
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
// CONFIG_REPO_TEST_DSN is unset, integration tests are skipped.
func dsn(t *testing.T) string {
	t.Helper()
	d := os.Getenv("CONFIG_REPO_TEST_DSN")
	if d == "" {
		t.Skip("CONFIG_REPO_TEST_DSN not set; skipping repository integration test")
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

func applyMigrations(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	migrationsDir := filepath.Join("..", "..", "migrations")
	// Run down first (idempotent via IF EXISTS) so the test starts from a
	// clean state regardless of prior migration state, then apply up.
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

func insertSnapshot(t *testing.T, db *gorm.DB, revision, status string, published bool, snapshotJSON string) {
	t.Helper()
	now := time.Now()
	revRow := struct {
		ID int64 `gorm:"column:id"`
	}{}
	if err := db.Raw(`INSERT INTO config_revisions (revision, status, published_at) VALUES (?, ?, ?) RETURNING id`,
		revision, status, nil).Scan(&revRow).Error; err != nil {
		t.Fatalf("insert revision: %v", err)
	}
	if published {
		db.Exec(`UPDATE config_revisions SET published_at = ? WHERE id = ?`, now, revRow.ID)
	}
	if snapshotJSON != "" {
		if err := db.Exec(`INSERT INTO config_revision_snapshots (revision_id, snapshot_json, sha256, created_at) VALUES (?, ?::jsonb, ?, ?)`,
			revRow.ID, snapshotJSON, "sha-"+revision, now).Error; err != nil {
			t.Fatalf("insert snapshot: %v", err)
		}
	}
}

func TestLatestPublished_NotFoundOnEmpty(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	r := New(db)
	_, err := r.LatestPublished(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLatestPublished_ReturnsLatest(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)

	insertSnapshot(t, db, "2026-07-24-01", "published", true, `{"revision":"v1"}`)
	// newer published revision
	insertSnapshot(t, db, "2026-07-24-02", "published", true, `{"revision":"v2"}`)
	// draft (must NOT be served)
	insertSnapshot(t, db, "2026-07-24-03", "draft", false, `{"revision":"v3draft"}`)
	// archived published (older, must NOT be served over the newer one)
	insertSnapshot(t, db, "2026-07-23-99", "published", true, `{"revision":"old"}`)
	// bump the older one to be clearly older
	db.Exec(`UPDATE config_revisions SET published_at = ? WHERE revision = '2026-07-23-99'`,
		time.Now().Add(-24*time.Hour))

	r := New(db)
	snap, err := r.LatestPublished(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Revision != "2026-07-24-02" {
		t.Errorf("revision = %q, want 2026-07-24-02", snap.Revision)
	}
	var payload map[string]any
	if err := json.Unmarshal(snap.SnapshotJSON, &payload); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if payload["revision"] != "v2" {
		t.Errorf("snapshot.revision = %v, want v2", payload["revision"])
	}
	if snap.SHA256 != "sha-2026-07-24-02" {
		t.Errorf("sha256 = %q, want sha-2026-07-24-02", snap.SHA256)
	}
}

func TestLatestPublished_IgnoresDrafts(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertSnapshot(t, db, "draft-only", "draft", false, `{"revision":"d"}`)
	r := New(db)
	_, err := r.LatestPublished(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft-only must yield ErrNotFound, got %v", err)
	}
}
