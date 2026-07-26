package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Writer is the write contract for the config service draft/publish lifecycle.
type Writer interface {
	// CreateDraft inserts a new config_revisions row with status='draft'.
	CreateDraft(ctx context.Context, revision, createdBy, changeLog string, parentRevisionID *int64) (int64, error)
	// UpdateDraftJSON stores the raw snapshot JSON for a draft revision.
	UpdateDraftJSON(ctx context.Context, revisionID int64, snapshotJSON json.RawMessage) error
	// GetDraft returns a draft revision and its snapshot JSON (if any).
	GetDraft(ctx context.Context, revisionID int64) (DraftRevision, error)
	// PublishRevision marks a draft revision as published, computes the
	// sha256, and inserts a config_revision_snapshots row. The previous
	// published revision (if any) is archived.
	PublishRevision(ctx context.Context, revisionID int64) error
	// ListRevisions returns all revisions ordered newest-first, paginated.
	ListRevisions(ctx context.Context, limit, offset int) ([]RevisionSummary, int, error)
	// RollbackRevision re-publishes a historical published revision by
	// creating a new published revision pointing to the same snapshot.
	RollbackRevision(ctx context.Context, targetRevisionID int64) (int64, error)
}

// DraftRevision is a draft revision with its snapshot JSON.
type DraftRevision struct {
	RevisionID   int64           `json:"revision_id"`
	Revision     string          `json:"revision"`
	Status       string          `json:"status"`
	ChangeLog    string          `json:"change_log"`
	SnapshotJSON json.RawMessage `json:"snapshot_json,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// RevisionSummary is a lightweight revision listing item.
type RevisionSummary struct {
	ID          int64      `json:"id"`
	Revision    string     `json:"revision"`
	Status      string     `json:"status"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	ChangeLog   string     `json:"change_log,omitempty"`
}

// ErrConflict indicates a state conflict (e.g. publishing a non-draft).
var ErrConflict = errors.New("repository: conflicting state")

// ErrInsertFailed indicates an insert failure.
var ErrInsertFailed = errors.New("repository: insert failed")

// ---- Writer implementation ----

func (r *GormRepository) CreateDraft(ctx context.Context, revision, createdBy, changeLog string, parentRevisionID *int64) (int64, error) {
	if revision == "" {
		return 0, ErrConflict
	}
	type insertRow struct {
		ID               int64  `gorm:"column:id"`
		Revision         string `gorm:"column:revision"`
		Status           string `gorm:"column:status"`
		CreatedBy        string `gorm:"column:created_by"`
		ChangeLog        string `gorm:"column:change_log"`
		ParentRevisionID *int64 `gorm:"column:parent_revision_id"`
	}
	row := insertRow{
		Revision:         revision,
		Status:           "draft",
		CreatedBy:        createdBy,
		ChangeLog:        changeLog,
		ParentRevisionID: parentRevisionID,
	}
	// Use a raw INSERT so parentRevisionID=nil produces an explicit NULL
	// (GORM's struct Create may omit a nil *int64 column, falling back to
	// the DB DEFAULT sequence value, which violates the self-referential FK
	// on the first revision). NULL is exempt from the FK.
	if parentRevisionID == nil {
		if err := r.db.WithContext(ctx).Raw(`INSERT INTO config_revisions (revision, status, created_by, change_log, parent_revision_id) VALUES (?, 'draft', ?, ?, NULL) RETURNING id`, revision, createdBy, changeLog).Scan(&row.ID).Error; err != nil {
			return 0, ErrInsertFailed
		}
		return row.ID, nil
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, ErrInsertFailed
	}
	return row.ID, nil
}

func (r *GormRepository) UpdateDraftJSON(ctx context.Context, revisionID int64, snapshotJSON json.RawMessage) error {
	// Verify the revision is a draft.
	var status string
	if err := r.db.WithContext(ctx).Raw("SELECT status FROM config_revisions WHERE id = ?", revisionID).Scan(&status).Error; err != nil {
		return ErrQueryFailed
	}
	if status == "" {
		return ErrNotFound
	}
	if status != "draft" {
		return ErrConflict
	}
	// Upsert into config_revision_snapshots (unique index on revision_id).
	sha := sha256Hex(snapshotJSON)
	res := r.db.WithContext(ctx).Exec(
		`INSERT INTO config_revision_snapshots (revision_id, snapshot_json, sha256, created_at)
		 VALUES (?, ?, ?, now())
		 ON CONFLICT (revision_id) DO UPDATE SET snapshot_json = EXCLUDED.snapshot_json, sha256 = EXCLUDED.sha256, created_at = now()`,
		revisionID, []byte(snapshotJSON), sha,
	)
	if res.Error != nil {
		return ErrInsertFailed
	}
	return nil
}

func (r *GormRepository) GetDraft(ctx context.Context, revisionID int64) (DraftRevision, error) {
	var dr DraftRevision
	err := r.db.WithContext(ctx).Raw(
		`SELECT r.id AS revision_id, r.revision, r.status, r.change_log, r.created_at,
		        s.snapshot_json
		 FROM config_revisions r
		 LEFT JOIN config_revision_snapshots s ON s.revision_id = r.id
		 WHERE r.id = ?`, revisionID,
	).Scan(&dr).Error
	if err != nil {
		return DraftRevision{}, ErrQueryFailed
	}
	if dr.RevisionID == 0 {
		return DraftRevision{}, ErrNotFound
	}
	return dr, nil
}

func (r *GormRepository) PublishRevision(ctx context.Context, revisionID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Load the draft.
		var status string
		var snapshotJSON []byte
		err := tx.Raw(
			`SELECT r.status, s.snapshot_json
			 FROM config_revisions r
			 LEFT JOIN config_revision_snapshots s ON s.revision_id = r.id
			 WHERE r.id = ?`, revisionID,
		).Row().Scan(&status, &snapshotJSON)
		if err != nil {
			return ErrQueryFailed
		}
		if status == "" {
			return ErrNotFound
		}
		if status != "draft" {
			return ErrConflict
		}
		if len(snapshotJSON) == 0 {
			return ErrConflict
		}

		// Archive previously published revisions.
		if err := tx.Exec(
			`UPDATE config_revisions SET status = 'archived', archived_at = now()
			 WHERE status = 'published'`,
		).Error; err != nil {
			return ErrQueryFailed
		}

		// Publish the draft.
		if err := tx.Exec(
			`UPDATE config_revisions SET status = 'published', published_at = now() WHERE id = ?`,
			revisionID,
		).Error; err != nil {
			return ErrQueryFailed
		}

		// Ensure snapshot has correct sha256.
		sha := sha256Hex(json.RawMessage(snapshotJSON))
		if err := tx.Exec(
			`UPDATE config_revision_snapshots SET sha256 = ? WHERE revision_id = ?`,
			sha, revisionID,
		).Error; err != nil {
			return ErrQueryFailed
		}
		return nil
	})
}

func (r *GormRepository) ListRevisions(ctx context.Context, limit, offset int) ([]RevisionSummary, int, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM config_revisions").Scan(&total).Error; err != nil {
		return nil, 0, ErrQueryFailed
	}
	var rows []RevisionSummary
	err := r.db.WithContext(ctx).Raw(
		`SELECT id, revision, status, created_by, created_at, published_at, archived_at, change_log
		 FROM config_revisions
		 ORDER BY id DESC
		 LIMIT ? OFFSET ?`,
		limit, offset,
	).Scan(&rows).Error
	if err != nil {
		return nil, 0, ErrQueryFailed
	}
	return rows, int(total), nil
}

func (r *GormRepository) RollbackRevision(ctx context.Context, targetRevisionID int64) (int64, error) {
	// Load the target published revision.
	var rev string
	var snapshotJSON []byte
	err := r.db.WithContext(ctx).Raw(
		`SELECT r.revision, s.snapshot_json
		 FROM config_revisions r
		 JOIN config_revision_snapshots s ON s.revision_id = r.id
		 WHERE r.id = ? AND r.status = 'published'`, targetRevisionID,
	).Row().Scan(&rev, &snapshotJSON)
	if err != nil {
		return 0, ErrQueryFailed
	}
	if rev == "" {
		return 0, ErrNotFound
	}
	// Create a new draft from the target snapshot, then publish it.
	newRev := rev + "-rollback-" + time.Now().UTC().Format("20060102150405")
	draftID, err := r.CreateDraft(ctx, newRev, "system", "rollback to "+rev, &targetRevisionID)
	if err != nil {
		return 0, err
	}
	if err := r.UpdateDraftJSON(ctx, draftID, json.RawMessage(snapshotJSON)); err != nil {
		return 0, err
	}
	if err := r.PublishRevision(ctx, draftID); err != nil {
		return 0, err
	}
	return draftID, nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
