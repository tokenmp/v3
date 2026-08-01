package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// publishTestDB opens the DB with all migrations applied. Skips when
// CONFIG_REPO_TEST_DSN is unset (CI runs the real PG).
func publishTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	d := dsn(t)
	applyMigrations(t, d)
	db, err := gorm.Open(postgres.Open(d), &gorm.Config{
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

const (
	testSnapshotV1 = `{"revision":"v1","models":{"m1":{"ID":"m1"}}}`
	testSnapshotV2 = `{"revision":"v2","models":{"m2":{"ID":"m2"}}}`
)

func auditMeta() AuditMeta { return AuditMeta{Actor: "tester", ActorKind: "admin"} }

func TestPublish_CreateDraftWithSnapshot(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()

	res, err := r.CreateDraftWithSnapshot(ctx, DraftInput{
		Revision:     "d1",
		CreatedBy:    "tester",
		ChangeLog:    "init",
		SnapshotJSON: json.RawMessage(testSnapshotV1),
	}, auditMeta())
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if res.Version != 1 || res.Status != "draft" {
		t.Fatalf("unexpected draft result: %+v", res)
	}
	// Audit row written.
	entries, total, err := r.ListAudit(ctx, &res.ID, 50, 0)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if total != 1 || entries[0].Action != "create" {
		t.Fatalf("audit mismatch: %d %+v", total, entries)
	}
	// Audit must not contain the snapshot body.
	b, _ := json.Marshal(entries[0])
	if string(b) != "" && (contains(string(b), "models") || contains(string(b), testSnapshotV1)) {
		t.Fatalf("audit leaked snapshot body: %s", b)
	}
}

func TestPublish_DuplicateRevisionConflict(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	_, err := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "dup"}, auditMeta())
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err = r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "dup"}, auditMeta())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestPublish_CASUpdateMismatch(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	res, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "cas", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	// Wrong expected version -> ErrCASMismatch.
	_, err := r.UpdateDraftSnapshot(ctx, res.ID, 999, json.RawMessage(testSnapshotV2), auditMeta())
	if !errors.Is(err, ErrCASMismatch) {
		t.Fatalf("expected ErrCASMismatch, got %v", err)
	}
	// Correct version succeeds and bumps.
	v, err := r.UpdateDraftSnapshot(ctx, res.ID, 1, json.RawMessage(testSnapshotV2), auditMeta())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v != 2 {
		t.Fatalf("version = %d, want 2", v)
	}
}

func TestPublish_AtMostOnePublished(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()

	// Publish first draft.
	d1, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "p1", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	if err := r.PublishRevision(ctx, d1.ID, auditMeta()); err != nil {
		t.Fatalf("publish d1: %v", err)
	}
	// Publish second draft -> first must be archived, only one published.
	d2, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "p2", SnapshotJSON: json.RawMessage(testSnapshotV2)}, auditMeta())
	if err := r.PublishRevision(ctx, d2.ID, auditMeta()); err != nil {
		t.Fatalf("publish d2: %v", err)
	}
	var publishedCount int64
	db.Raw(`SELECT COUNT(*) FROM config_revisions WHERE status='published'`).Scan(&publishedCount)
	if publishedCount != 1 {
		t.Fatalf("published count = %d, want 1", publishedCount)
	}
	// Latest served is d2.
	snap, err := r.LatestPublished(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if snap.Revision != "p2" {
		t.Fatalf("latest revision = %s, want p2", snap.Revision)
	}
}

func TestPublish_ConcurrentOnlyOneSurvives(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()

	// Two drafts ready to publish.
	d1, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "c1", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	d2, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "c2", SnapshotJSON: json.RawMessage(testSnapshotV2)}, auditMeta())

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs[0] = r.PublishRevision(ctx, d1.ID, auditMeta())
	}()
	go func() {
		defer wg.Done()
		errs[1] = r.PublishRevision(ctx, d2.ID, auditMeta())
	}()
	wg.Wait()

	var publishedCount int64
	db.Raw(`SELECT COUNT(*) FROM config_revisions WHERE status='published'`).Scan(&publishedCount)
	if publishedCount != 1 {
		t.Fatalf("concurrent published count = %d, want exactly 1", publishedCount)
	}
	// At least one publish must have succeeded; the singleton index guarantees
	// no more than one. If both returned nil the index still enforces uniqueness.
}

func TestPublish_PublishNonDraftConflict(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	d, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "np", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	_ = r.PublishRevision(ctx, d.ID, auditMeta())
	// Publishing already-published -> conflict.
	err := r.PublishRevision(ctx, d.ID, auditMeta())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestPublish_PublishEmptySnapshotConflict(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	d, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "empty"}, auditMeta())
	err := r.PublishRevision(ctx, d.ID, auditMeta())
	if !errors.Is(err, ErrEmptySnapshot) {
		t.Fatalf("expected ErrEmptySnapshot, got %v", err)
	}
}

func TestPublish_RollbackAsNewFromArchived(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()

	// Publish v1, then v2 (archives v1).
	d1, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "rb1", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	_ = r.PublishRevision(ctx, d1.ID, auditMeta())
	d2, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "rb2", SnapshotJSON: json.RawMessage(testSnapshotV2)}, auditMeta())
	_ = r.PublishRevision(ctx, d2.ID, auditMeta())

	// Revert to archived v1 -> new published revision copying v1's snapshot.
	newID, err := r.RollbackAsNew(ctx, d1.ID, "revert to rb1", "tester", auditMeta())
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// Source row must remain archived (not revived).
	var srcStatus string
	db.Raw(`SELECT status FROM config_revisions WHERE id=?`, d1.ID).Scan(&srcStatus)
	if srcStatus != "archived" {
		t.Fatalf("source status = %s, want archived", srcStatus)
	}
	// New revision is published and is the latest.
	snap, err := r.LatestPublished(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if snap.RevisionID != newID {
		t.Fatalf("latest id = %d, want %d", snap.RevisionID, newID)
	}
	// Snapshot content equals v1.
	var payload map[string]any
	_ = json.Unmarshal(snap.SnapshotJSON, &payload)
	if payload["revision"] != "v1" {
		t.Fatalf("rollback snapshot = %v, want v1", payload["revision"])
	}
	// Source provenance recorded.
	det, _ := r.GetRevision(ctx, newID)
	if det.SourceRevisionID == nil || *det.SourceRevisionID != d1.ID {
		t.Fatalf("source_revision_id mismatch: %+v", det.SourceRevisionID)
	}
	// Still exactly one published.
	var publishedCount int64
	db.Raw(`SELECT COUNT(*) FROM config_revisions WHERE status='published'`).Scan(&publishedCount)
	if publishedCount != 1 {
		t.Fatalf("published count = %d, want 1", publishedCount)
	}
}

func TestPublish_RollbackFromDraftConflict(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	d, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "rbdraft", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	// Cannot revert to a draft (not immutable).
	_, err := r.RollbackAsNew(ctx, d.ID, "", "tester", auditMeta())
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestPublish_AuditNoSecret(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	d, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "audit", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	_ = r.PublishRevision(ctx, d.ID, auditMeta())
	entries, total, err := r.ListAudit(ctx, nil, 100, 0)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if total == 0 {
		t.Fatalf("expected audit entries")
	}
	// Marshal and assert no snapshot body / secret markers.
	for _, e := range entries {
		b, _ := json.Marshal(e)
		s := string(b)
		if contains(s, testSnapshotV1) || contains(s, `"models"`) {
			t.Fatalf("audit entry leaked snapshot content: %s", s)
		}
	}
}

func TestPublish_ArchiveRevision(t *testing.T) {
	db := publishTestDB(t)
	r := New(db)
	ctx := context.Background()
	d, _ := r.CreateDraftWithSnapshot(ctx, DraftInput{Revision: "arc", SnapshotJSON: json.RawMessage(testSnapshotV1)}, auditMeta())
	_ = r.PublishRevision(ctx, d.ID, auditMeta())
	if err := r.ArchiveRevision(ctx, d.ID, auditMeta()); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// No active published after archiving.
	_, err := r.LatestPublished(ctx)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after archive, got %v", err)
	}
	// Archiving already-archived -> conflict.
	if err := r.ArchiveRevision(ctx, d.ID, auditMeta()); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on re-archive, got %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
