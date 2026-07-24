// Package repository reads configuration data from the Config DB.
//
// The config service owns no business write logic in this skeleton; its read
// path exposes the latest published config revision snapshot for executor
// pull. Writes (draft/publish) are future work.
//
// Errors are stable sentinels. Driver errors (which may carry DSN fragments)
// are never surfaced via Error(); the repository maps query failures to
// classified sentinels (ErrNotFound / ErrQueryFailed).
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Snapshot is the published configuration snapshot served to executors. It is
// the raw ConfigSnapshot JSON plus safe metadata; it is NOT the compiled
// snapshot — compilation happens executor-side via snapshot.Compile so the
// config service does not depend on the executor internal package.
type Snapshot struct {
	RevisionID   int64           `json:"-"`
	Revision     string          `json:"revision"`
	SnapshotJSON json.RawMessage `json:"snapshot"`
	SHA256       string          `json:"sha256"`
	CompiledMeta json.RawMessage `json:"compiled_meta,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// Reader is the read contract the server depends on. It is implemented by the
// GORM-backed repository and by test doubles.
type Reader interface {
	// LatestPublished returns the most recently published config revision
	// snapshot. Returns ErrNotFound when no published revision exists.
	LatestPublished(ctx context.Context) (Snapshot, error)
}

// Stable classified errors. They do not wrap the driver error so DSN/SQL
// fragments never reach logs through Error().
var (
	ErrNotFound    = errors.New("repository: no published snapshot found")
	ErrQueryFailed = errors.New("repository: query failed")
)

type snapshotRow struct {
	RevisionID   int64     `gorm:"column:revision_id"`
	Revision     string    `gorm:"column:revision"`
	SnapshotJSON []byte    `gorm:"column:snapshot_json"`
	SHA256       string    `gorm:"column:sha256"`
	CompiledMeta []byte    `gorm:"column:compiled_meta"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

// GormRepository reads snapshots from the Config DB via GORM.
type GormRepository struct {
	db *gorm.DB
}

// New returns a GORM-backed Reader.
func New(db *gorm.DB) *GormRepository {
	return &GormRepository{db: db}
}

// LatestPublished joins config_revisions (status='published', newest
// published_at) with config_revision_snapshots and returns the snapshot. It
// fails closed: a query error is ErrQueryFailed, no row is ErrNotFound.
func (r *GormRepository) LatestPublished(ctx context.Context) (Snapshot, error) {
	const q = `
SELECT s.revision_id, r.revision, s.snapshot_json, s.sha256, s.compiled_meta, s.created_at
FROM config_revision_snapshots s
JOIN config_revisions r ON r.id = s.revision_id
WHERE r.status = 'published'
ORDER BY r.published_at DESC, s.revision_id DESC
LIMIT 1`
	var row snapshotRow
	if err := r.db.WithContext(ctx).Raw(q).Scan(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, ErrQueryFailed
	}
	// Raw().Scan() does not return ErrRecordNotFound when no row matches; the
	// row stays zero-valued. Detect a missing published revision by empty
	// revision_id and surface ErrNotFound.
	if row.RevisionID == 0 {
		return Snapshot{}, ErrNotFound
	}
	return Snapshot{
		RevisionID:   row.RevisionID,
		Revision:     row.Revision,
		SnapshotJSON: json.RawMessage(row.SnapshotJSON),
		SHA256:       row.SHA256,
		CompiledMeta: json.RawMessage(row.CompiledMeta),
		CreatedAt:    row.CreatedAt,
	}, nil
}
