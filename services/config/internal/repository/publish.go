package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// DraftInput is the input for creating a draft revision.
type DraftInput struct {
	Revision         string
	CreatedBy        string
	ChangeLog        string
	ParentRevisionID *int64
	// SourceRevisionID is set for rollback drafts (the immutable revision whose
	// snapshot is copied). Non-nil + non-zero marks this as a rollback source.
	SourceRevisionID *int64
	RollbackNote     string
	// SnapshotJSON is the optional initial raw snapshot stored atomically with
	// the draft creation. May be nil to create an empty draft.
	SnapshotJSON json.RawMessage
}

// DraftResult is the safe output of draft creation.
type DraftResult struct {
	ID       int64  `json:"id"`
	Revision string `json:"revision"`
	Version  int    `json:"version"`
	Status   string `json:"status"`
}

// RevisionDetail is a revision with its snapshot JSON and metadata.
type RevisionDetail struct {
	ID               int64           `json:"id"`
	Revision         string          `json:"revision"`
	Status           string          `json:"status"`
	Version          int             `json:"version"`
	ChangeLog        string          `json:"change_log"`
	CreatedBy        string          `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
	PublishedAt      *time.Time      `json:"published_at,omitempty"`
	ArchivedAt       *time.Time      `json:"archived_at,omitempty"`
	ParentRevisionID *int64          `json:"parent_revision_id,omitempty"`
	SourceRevisionID *int64          `json:"source_revision_id,omitempty"`
	RollbackNote     string          `json:"rollback_note,omitempty"`
	SHA256           string          `json:"sha256"`
	SnapshotJSON     json.RawMessage `json:"snapshot_json,omitempty"`
}

// RevisionSummary is a lightweight revision listing item.
type RevisionSummary struct {
	ID               int64      `json:"id"`
	Revision         string     `json:"revision"`
	Status           string     `json:"status"`
	Version          int        `json:"version"`
	CreatedBy        string     `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	PublishedAt      *time.Time `json:"published_at,omitempty"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	ChangeLog        string     `json:"change_log,omitempty"`
	SourceRevisionID *int64     `json:"source_revision_id,omitempty"`
	RollbackNote     string     `json:"rollback_note,omitempty"`
}

// AuditEntry is a safe (secret-free) audit row.
type AuditEntry struct {
	ID         int64     `json:"id"`
	RevisionID *int64    `json:"revision_id,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	ActorKind  string    `json:"actor_kind,omitempty"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	At         time.Time `json:"at"`
}

// Stable classified write errors. They never wrap the driver error.
var (
	ErrConflict      = errors.New("repository: conflicting state")
	ErrInsertFailed  = errors.New("repository: insert failed")
	ErrCASMismatch   = errors.New("repository: version mismatch")
	ErrEmptySnapshot = errors.New("repository: empty snapshot")
	// ErrInvalidInput is returned when a write violates a database constraint
	// that indicates a client-side error (NOT NULL, foreign key, or check
	// constraint). Maps to HTTP 400.
	ErrInvalidInput = errors.New("repository: invalid input")
	// ErrSecretRejected is returned when a write attempts to store a plaintext
	// secret or a non-vault:// credential ref. It is safe to log.
	ErrSecretRejected = errors.New("repository: plaintext secret rejected")
)

// Writer is the write contract for the config service draft/publish lifecycle.
// All methods are atomic state-machine transitions; audit rows are written in
// the SAME transaction as the state change and carry only safe metadata.
type Writer interface {
	// CreateDraftWithSnapshot inserts a new draft revision (version=1) and
	// optionally stores its initial raw snapshot, atomically with an audit
	// row. Returns ErrConflict if the revision string already exists.
	CreateDraftWithSnapshot(ctx context.Context, in DraftInput, audit AuditMeta) (DraftResult, error)

	// GetRevision returns a revision with its snapshot and metadata.
	GetRevision(ctx context.Context, revisionID int64) (RevisionDetail, error)

	// UpdateDraftSnapshot performs a CAS update of a draft's snapshot. The
	// provided expectedVersion must match the current version; on success the
	// version is bumped and an audit row is written. Returns ErrCASMismatch
	// (412) when the version does not match, ErrConflict when the revision is
	// not a draft, ErrNotFound when the revision does not exist.
	UpdateDraftSnapshot(ctx context.Context, revisionID int64, expectedVersion int, snapshotJSON json.RawMessage, audit AuditMeta) (int, error)

	// PublishRevision atomically: locks the draft, verifies a complete
	// snapshot, computes the canonical sha256, archives the previous published
	// revision, marks the draft published (immutable), and writes a secret-free
	// audit row. The global singleton invariant is enforced by the partial
	// unique index; archiving happens before publishing within the same tx so
	// at no committed point are there two published revisions.
	PublishRevision(ctx context.Context, revisionID int64, audit AuditMeta) error

	// ArchiveRevision marks a published revision archived. It does NOT revive
	// or copy any revision; it only transitions status. After archiving the
	// sole published revision there is no active published revision.
	ArchiveRevision(ctx context.Context, revisionID int64, audit AuditMeta) error

	// RollbackAsNew creates AND publishes a NEW revision copying the snapshot
	// of an arbitrary historical immutable revision (published or archived).
	// It never modifies or revives the source row. Returns the new revision id.
	RollbackAsNew(ctx context.Context, sourceRevisionID int64, note, createdBy string, audit AuditMeta) (int64, error)

	// ListRevisions returns revisions ordered newest-first, paginated.
	ListRevisions(ctx context.Context, limit, offset int) ([]RevisionSummary, int, error)

	// ListAudit returns audit entries ordered newest-first, paginated, filtered
	// optionally by revision_id. Entries never contain secrets.
	ListAudit(ctx context.Context, revisionID *int64, limit, offset int) ([]AuditEntry, int, error)
}

// AuditMeta carries the safe actor/request metadata attached to an audit row.
// It must never contain a secret, snapshot body or DSN.
type AuditMeta struct {
	Actor     string
	ActorKind string
	RequestID string
}

// ErrEmptyAuditMeta is returned when audit metadata is missing the actor. The
// write path requires a non-empty actor for traceability; this is a safe
// sentinel (no secret).
var ErrEmptyAuditMeta = errors.New("repository: audit actor required")

func (m AuditMeta) validate() error {
	if strings.TrimSpace(m.Actor) == "" {
		return ErrEmptyAuditMeta
	}
	return nil
}

// auditRow maps the config_audit_log table. before/after are intentionally
// small safe JSON (status/version), never the full snapshot.
type auditRow struct {
	ID         int64     `gorm:"column:id"`
	RevisionID *int64    `gorm:"column:revision_id"`
	Actor      string    `gorm:"column:actor"`
	ActorKind  string    `gorm:"column:actor_kind"`
	Action     string    `gorm:"column:action"`
	EntityType string    `gorm:"column:entity_type"`
	EntityID   string    `gorm:"column:entity_id"`
	Before     []byte    `gorm:"column:before;type:jsonb"`
	After      []byte    `gorm:"column:after;type:jsonb"`
	RequestID  string    `gorm:"column:request_id"`
	At         time.Time `gorm:"column:at"`
}

func (auditRow) TableName() string { return "config_audit_log" }

func writeAudit(tx *gorm.DB, revisionID *int64, action, entityType, entityID string, before, after map[string]any, meta AuditMeta) error {
	if err := meta.validate(); err != nil {
		return err
	}
	var beforeJSON, afterJSON []byte
	if before != nil {
		b, err := json.Marshal(before)
		if err != nil {
			return ErrInsertFailed
		}
		beforeJSON = b
	}
	if after != nil {
		a, err := json.Marshal(after)
		if err != nil {
			return ErrInsertFailed
		}
		afterJSON = a
	}
	row := auditRow{
		RevisionID: revisionID,
		Actor:      meta.Actor,
		ActorKind:  meta.ActorKind,
		Action:     action,
		EntityType: entityType,
		EntityID:   entityID,
		Before:     beforeJSON,
		After:      afterJSON,
		RequestID:  meta.RequestID,
	}
	if err := tx.Create(&row).Error; err != nil {
		return ErrInsertFailed
	}
	return nil
}

// safeStatusJSON builds the small safe before/after audit payload. It never
// includes the snapshot body or any secret.
func safeStatusJSON(status string, version int) map[string]any {
	return map[string]any{"status": status, "version": version}
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation by string-matching the SQLSTATE 23505. We do not import the pq
// driver package (config service uses pgx via gorm) to avoid a new dependency;
// the match is on the stable SQLSTATE that pgx surfaces in the error string.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "23505") || strings.Contains(strings.ToLower(s), "unique constraint")
}

// ---- CreateDraftWithSnapshot ----

func (r *GormRepository) CreateDraftWithSnapshot(ctx context.Context, in DraftInput, audit AuditMeta) (DraftResult, error) {
	if strings.TrimSpace(in.Revision) == "" {
		return DraftResult{}, ErrConflict
	}
	if err := audit.validate(); err != nil {
		return DraftResult{}, err
	}
	var result DraftResult
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		type insertRow struct {
			ID               int64  `gorm:"column:id"`
			Revision         string `gorm:"column:revision"`
			Status           string `gorm:"column:status"`
			Version          int    `gorm:"column:version"`
			CreatedBy        string `gorm:"column:created_by"`
			ChangeLog        string `gorm:"column:change_log"`
			ParentRevisionID *int64 `gorm:"column:parent_revision_id"`
			SourceRevisionID *int64 `gorm:"column:source_revision_id"`
			RollbackNote     string `gorm:"column:rollback_note"`
		}
		row := insertRow{
			Revision:         in.Revision,
			Status:           "draft",
			Version:          1,
			CreatedBy:        in.CreatedBy,
			ChangeLog:        in.ChangeLog,
			ParentRevisionID: in.ParentRevisionID,
			SourceRevisionID: in.SourceRevisionID,
			RollbackNote:     in.RollbackNote,
		}
		// Raw INSERT with explicit NULL handling so a nil *int64 parent does
		// not fall back to a sequence default (self-referential FK violation).
		// Use Raw().Scan() (not Exec) so RETURNING id populates row.ID.
		if err := tx.Raw(
			`INSERT INTO config_revisions
			   (revision, status, version, created_by, change_log, parent_revision_id, source_revision_id, rollback_note)
			 VALUES (?, 'draft', 1, ?, ?, ?, ?, ?) RETURNING id`,
			in.Revision, in.CreatedBy, in.ChangeLog, in.ParentRevisionID, in.SourceRevisionID, in.RollbackNote,
		).Scan(&row.ID).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return ErrInsertFailed
		}
		if len(in.SnapshotJSON) > 0 {
			sha := sha256Hex(in.SnapshotJSON)
			if err := tx.Exec(
				`INSERT INTO config_revision_snapshots (revision_id, snapshot_json, sha256, created_at)
				 VALUES (?, ?, ?, now())
				 ON CONFLICT (revision_id) DO UPDATE SET snapshot_json = EXCLUDED.snapshot_json, sha256 = EXCLUDED.sha256, created_at = now()`,
				row.ID, []byte(in.SnapshotJSON), sha,
			).Error; err != nil {
				return ErrInsertFailed
			}
		}
		if err := writeAudit(tx, &row.ID, "create", "config_revision", in.Revision, nil, safeStatusJSON("draft", 1), audit); err != nil {
			return err
		}
		result = DraftResult{ID: row.ID, Revision: in.Revision, Version: 1, Status: "draft"}
		return nil
	})
	if err != nil {
		return DraftResult{}, err
	}
	return result, nil
}

// ---- GetRevision ----

func (r *GormRepository) GetRevision(ctx context.Context, revisionID int64) (RevisionDetail, error) {
	var rd RevisionDetail
	err := r.db.WithContext(ctx).Raw(
		`SELECT r.id, r.revision, r.status, r.version, r.change_log, r.created_by, r.created_at,
		        r.published_at, r.archived_at, r.parent_revision_id, r.source_revision_id, r.rollback_note,
		        s.sha256, s.snapshot_json
		 FROM config_revisions r
		 LEFT JOIN config_revision_snapshots s ON s.revision_id = r.id
		 WHERE r.id = ?`, revisionID,
	).Scan(&rd).Error
	if err != nil {
		return RevisionDetail{}, ErrQueryFailed
	}
	if rd.ID == 0 {
		return RevisionDetail{}, ErrNotFound
	}
	return rd, nil
}

// ---- UpdateDraftSnapshot (CAS) ----

func (r *GormRepository) UpdateDraftSnapshot(ctx context.Context, revisionID int64, expectedVersion int, snapshotJSON json.RawMessage, audit AuditMeta) (int, error) {
	if len(snapshotJSON) == 0 {
		return 0, ErrEmptySnapshot
	}
	if err := audit.validate(); err != nil {
		return 0, err
	}
	newVersion := expectedVersion + 1
	sha := sha256Hex(snapshotJSON)
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// CAS: lock the row and verify draft + version in one UPDATE.
		res := tx.Exec(
			`UPDATE config_revisions SET version = version + 1
			 WHERE id = ? AND status = 'draft' AND version = ?`,
			revisionID, expectedVersion,
		)
		if res.Error != nil {
			return ErrQueryFailed
		}
		switch res.RowsAffected {
		case 0:
			// Distinguish not-found from version/status mismatch.
			var status string
			var version int
			if err := tx.Raw(`SELECT status, version FROM config_revisions WHERE id = ?`, revisionID).Row().Scan(&status, &version); err != nil {
				return ErrQueryFailed
			}
			if status == "" {
				return ErrNotFound
			}
			if status != "draft" {
				return ErrConflict
			}
			return ErrCASMismatch
		case 1:
			// proceed
		default:
			return ErrConflict
		}
		// Upsert snapshot with canonical hash.
		if err := tx.Exec(
			`INSERT INTO config_revision_snapshots (revision_id, snapshot_json, sha256, created_at)
			 VALUES (?, ?, ?, now())
			 ON CONFLICT (revision_id) DO UPDATE SET snapshot_json = EXCLUDED.snapshot_json, sha256 = EXCLUDED.sha256, created_at = now()`,
			revisionID, []byte(snapshotJSON), sha,
		).Error; err != nil {
			return ErrInsertFailed
		}
		if err := writeAudit(tx, &revisionID, "update", "config_revision", "", safeStatusJSON("draft", expectedVersion), safeStatusJSON("draft", newVersion), audit); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newVersion, nil
}

// ---- PublishRevision ----

func (r *GormRepository) PublishRevision(ctx context.Context, revisionID int64, audit AuditMeta) (retErr error) {
	if err := audit.validate(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the target revision row first. We cannot use FOR UPDATE on the
		// nullable side of a LEFT JOIN (PostgreSQL ERROR 0A000), so fetch the
		// snapshot separately after locking the revision.
		var status string
		var version int
		if err := tx.Raw(
			`SELECT status, version FROM config_revisions WHERE id = ? FOR UPDATE`, revisionID,
		).Row().Scan(&status, &version); err != nil {
			return ErrQueryFailed
		}
		if status == "" {
			return ErrNotFound
		}
		if status != "draft" {
			return ErrConflict
		}
		var snapshotJSON []byte
		switch err := tx.Raw(
			`SELECT snapshot_json FROM config_revision_snapshots WHERE revision_id = ?`, revisionID,
		).Row().Scan(&snapshotJSON); {
		case err == nil:
			// snapshot row present
		case errors.Is(err, sql.ErrNoRows):
			// No snapshot row -> empty snapshot.
			snapshotJSON = nil
		default:
			return ErrQueryFailed
		}
		if len(snapshotJSON) == 0 {
			return ErrEmptySnapshot
		}

		before := safeStatusJSON(status, version)

		// Archive the previous published revision (if any) BEFORE publishing so
		// the singleton partial unique index is never violated at commit.
		var prevID int64
		_ = tx.Raw(`SELECT id FROM config_revisions WHERE status = 'published' LIMIT 1`).Scan(&prevID).Error
		if prevID > 0 {
			if err := tx.Exec(
				`UPDATE config_revisions SET status = 'archived', archived_at = now() WHERE id = ? AND status = 'published'`,
				prevID,
			).Error; err != nil {
				return ErrQueryFailed
			}
			if err := writeAudit(tx, &prevID, "archive", "config_revision", "", safeStatusJSON("published", 0), safeStatusJSON("archived", 0), audit); err != nil {
				return err
			}
		}

		// Publish the draft (immutable from here). Recompute canonical sha256.
		sha := sha256Hex(json.RawMessage(snapshotJSON))
		if err := tx.Exec(
			`UPDATE config_revisions SET status = 'published', published_at = now() WHERE id = ? AND status = 'draft'`,
			revisionID,
		).Error; err != nil {
			return ErrQueryFailed
		}
		if err := tx.Exec(
			`UPDATE config_revision_snapshots SET sha256 = ? WHERE revision_id = ?`,
			sha, revisionID,
		).Error; err != nil {
			return ErrQueryFailed
		}
		after := safeStatusJSON("published", version)
		if err := writeAudit(tx, &revisionID, "publish", "config_revision", "", before, after, audit); err != nil {
			return err
		}
		return nil
	})
}

// ---- ArchiveRevision ----

func (r *GormRepository) ArchiveRevision(ctx context.Context, revisionID int64, audit AuditMeta) error {
	if err := audit.validate(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var status string
		var version int
		if err := tx.Raw(`SELECT status, version FROM config_revisions WHERE id = ? FOR UPDATE`, revisionID).Row().Scan(&status, &version); err != nil {
			return ErrQueryFailed
		}
		if status == "" {
			return ErrNotFound
		}
		if status == "archived" {
			return ErrConflict
		}
		if status != "published" && status != "draft" {
			return ErrConflict
		}
		before := safeStatusJSON(status, version)
		if err := tx.Exec(
			`UPDATE config_revisions SET status = 'archived', archived_at = now() WHERE id = ?`,
			revisionID,
		).Error; err != nil {
			return ErrQueryFailed
		}
		if err := writeAudit(tx, &revisionID, "archive", "config_revision", "", before, safeStatusJSON("archived", 0), audit); err != nil {
			return err
		}
		return nil
	})
}

// ---- RollbackAsNew ----

func (r *GormRepository) RollbackAsNew(ctx context.Context, sourceRevisionID int64, note, createdBy string, audit AuditMeta) (int64, error) {
	if err := audit.validate(); err != nil {
		return 0, err
	}
	var newID int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Load + lock the source revision. Drafts are NOT immutable and cannot
		// be a rollback source. Fetch the snapshot separately to avoid applying
		// FOR UPDATE across a join.
		var srcRev string
		var srcStatus string
		if err := tx.Raw(
			`SELECT revision, status FROM config_revisions WHERE id = ? FOR UPDATE`, sourceRevisionID,
		).Row().Scan(&srcRev, &srcStatus); err != nil {
			return ErrNotFound
		}
		if srcStatus == "draft" {
			return ErrConflict
		}
		var snapshotJSON []byte
		if err := tx.Raw(
			`SELECT snapshot_json FROM config_revision_snapshots WHERE revision_id = ?`, sourceRevisionID,
		).Row().Scan(&snapshotJSON); err != nil {
			return ErrQueryFailed
		}
		if len(snapshotJSON) == 0 {
			return ErrEmptySnapshot
		}

		// Create a NEW revision copying the source snapshot. source_revision_id
		// records provenance; the source row is never mutated.
		newRev := srcRev + "-rollback-" + time.Now().UTC().Format("20060102150405")
		type idRow struct {
			ID int64 `gorm:"column:id"`
		}
		var ir idRow
		if err := tx.Raw(
			`INSERT INTO config_revisions
			   (revision, status, version, created_by, change_log, parent_revision_id, source_revision_id, rollback_note)
			 VALUES (?, 'draft', 1, ?, ?, ?, ?, ?) RETURNING id`,
			newRev, createdBy, "rollback to "+srcRev, nil, &sourceRevisionID, note,
		).Scan(&ir.ID).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrConflict
			}
			return ErrInsertFailed
		}
		newID = ir.ID
		sha := sha256Hex(json.RawMessage(snapshotJSON))
		if err := tx.Exec(
			`INSERT INTO config_revision_snapshots (revision_id, snapshot_json, sha256, created_at)
			 VALUES (?, ?, ?, now())`,
			newID, snapshotJSON, sha,
		).Error; err != nil {
			return ErrInsertFailed
		}

		// Archive previous published, then publish the new revision.
		var prevID int64
		_ = tx.Raw(`SELECT id FROM config_revisions WHERE status = 'published' LIMIT 1`).Scan(&prevID).Error
		if prevID > 0 {
			if err := tx.Exec(
				`UPDATE config_revisions SET status = 'archived', archived_at = now() WHERE id = ? AND status = 'published'`,
				prevID,
			).Error; err != nil {
				return ErrQueryFailed
			}
			if err := writeAudit(tx, &prevID, "archive", "config_revision", "", safeStatusJSON("published", 0), safeStatusJSON("archived", 0), audit); err != nil {
				return err
			}
		}
		if err := tx.Exec(
			`UPDATE config_revisions SET status = 'published', published_at = now() WHERE id = ? AND status = 'draft'`,
			newID,
		).Error; err != nil {
			return ErrQueryFailed
		}
		if err := writeAudit(tx, &newID, "rollback_publish", "config_revision", newRev, nil, safeStatusJSON("published", 1), audit); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// ---- ListRevisions ----

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
		`SELECT id, revision, status, version, created_by, created_at, published_at, archived_at,
		        change_log, source_revision_id, rollback_note
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

// ---- ListAudit ----

func (r *GormRepository) ListAudit(ctx context.Context, revisionID *int64, limit, offset int) ([]AuditEntry, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT id, revision_id, actor, actor_kind, action, entity_type, entity_id, request_id, at
	      FROM config_audit_log`
	args := []any{}
	if revisionID != nil && *revisionID > 0 {
		q += ` WHERE revision_id = ?`
		args = append(args, *revisionID)
	}
	var total int64
	countQ := `SELECT COUNT(*) FROM config_audit_log`
	if revisionID != nil && *revisionID > 0 {
		countQ += ` WHERE revision_id = ?`
	}
	if err := r.db.WithContext(ctx).Raw(countQ, args...).Scan(&total).Error; err != nil {
		return nil, 0, ErrQueryFailed
	}
	q += ` ORDER BY at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	var rows []AuditEntry
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, 0, ErrQueryFailed
	}
	return rows, int(total), nil
}

// sha256Hex computes the canonical sha256 hex digest of the snapshot bytes.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
