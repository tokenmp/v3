// Package repository implements the GORM-backed user and session persistence
// for the auth service.
//
// The schema is owned by versioned SQL migrations under services/auth/migrations
// (AutoMigrate is forbidden). These types are application-layer access only.
//
// Errors returned here are stable, classified sentinels. The underlying GORM /
// driver error is never exposed through Error()/Unwrap() so DSN fragments,
// raw SQL, or column names cannot reach logs or HTTP responses. Callers branch
// on errors.Is(err, repository.ErrDuplicateEmail) etc.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tokenmp/v3/services/auth/internal/database/models"
)

// Classified sentinel errors. None carry the raw driver error text.
var (
	ErrNotFound       = errors.New("repository: not found")
	ErrDuplicateEmail = errors.New("repository: duplicate email")
	ErrConstraint     = errors.New("repository: constraint violation")
	ErrInternal       = errors.New("repository: internal error")
)

// txKey is the context key carrying the *gorm.DB transaction handle. When
// present, repository methods bind to the transaction; otherwise they use the
// connection-level db.
type txKey struct{}

// withTx returns the *gorm.DB to use for this call: the transaction stored in
// ctx if present, else the fallback db.
func withTx(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if v, ok := ctx.Value(txKey{}).(*gorm.DB); ok && v != nil {
		return v.WithContext(ctx)
	}
	return fallback.WithContext(ctx)
}

// classify translates a GORM error into a stable sentinel. The raw error is
// discarded so its message can never leak. It uses pgconn.PgError SQLSTATE
// codes for reliable classification instead of string matching on err.Error().
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	// Use pgconn.PgError for SQLSTATE-based classification.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			// Distinguish the email unique index from a generic unique violation.
			// The migration creates: CREATE UNIQUE INDEX users_email_unique_idx ON users (LOWER(BTRIM(email)))
			// PostgreSQL may also report the auto-generated constraint name users_email_key.
			switch pgErr.ConstraintName {
			case "users_email_unique_idx", "users_email_key":
				return ErrDuplicateEmail
			case "auth_sessions_refresh_token_hash_key":
				return ErrConstraint // refresh token hash collision
			default:
				return ErrConstraint
			}
		case "23503": // foreign_key_violation
			return ErrConstraint
		case "23514": // check_violation
			return ErrConstraint
		}
	}
	// GORM's ErrDuplicatedKey is only returned when TranslateError is enabled
	// and the dialector implements ErrorTranslator. The postgres driver does
	// not implement ErrorTranslator, so this branch is defensive only.
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return ErrConstraint
	}
	return ErrInternal
}

// nowPtr returns a pointer to t.
func nowPtr(t time.Time) *time.Time { return &t }

// strPtr returns a pointer to s (or nil for "").
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// UserRepository is the GORM implementation of the user persistence port.
type UserRepository struct {
	db *gorm.DB
}

// NewUserRepository builds a UserRepository bound to the given db.
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create inserts a new user. The caller must normalize email and hash the
// password before calling. On a duplicate email ErrDuplicateEmail is returned
// without the raw driver text.
func (r *UserRepository) Create(ctx context.Context, u *models.User) error {
	if err := withTx(ctx, r.db).Create(u).Error; err != nil {
		return classify(err)
	}
	return nil
}

// FindByEmail loads a user by the canonical (LOWER(BTRIM)) email. The caller
// must normalize the email before calling.
func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	if err := withTx(ctx, r.db).Where("email = ?", email).First(&u).Error; err != nil {
		return nil, classify(err)
	}
	return &u, nil
}

// FindByID loads a user by primary key.
func (r *UserRepository) FindByID(ctx context.Context, id string) (*models.User, error) {
	var u models.User
	if err := withTx(ctx, r.db).Where("id = ?", id).First(&u).Error; err != nil {
		return nil, classify(err)
	}
	return &u, nil
}

// UpdatePasswordHash sets password_hash for the given user id. The caller is
// responsible for hashing with Argon2id.
func (r *UserRepository) UpdatePasswordHash(ctx context.Context, userID, hash string) error {
	res := withTx(ctx, r.db).Model(&models.User{}).
		Where("id = ?", userID).
		Update("password_hash", hash)
	if res.Error != nil {
		return classify(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// IncrementTokenVersion atomically bumps users.token_version by 1 and returns
// the new value. Used by password change and logout-all to invalidate all
// outstanding access tokens. Implemented as an UPDATE SET token_version =
// token_version + 1 followed by a re-read (First) within the same
// transaction so callers see the committed value.
func (r *UserRepository) IncrementTokenVersion(ctx context.Context, userID string) (int, error) {
	var u models.User
	tx := withTx(ctx, r.db).Model(&models.User{}).Where("id = ?", userID)
	if err := tx.UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error; err != nil {
		return 0, classify(err)
	}
	// Re-read to return the new value within the same transaction so callers
	// can issue access tokens with the up-to-date version.
	if err := withTx(ctx, r.db).Where("id = ?", userID).First(&u).Error; err != nil {
		return 0, classify(err)
	}
	return u.TokenVersion, nil
}

// SessionRepository is the GORM implementation of the refresh-session port.
type SessionRepository struct {
	db *gorm.DB
}

// NewSessionRepository builds a SessionRepository bound to the given db.
func NewSessionRepository(db *gorm.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create inserts a new auth_sessions row. The DB provides id and
// token_family_id defaults when the caller leaves them empty.
func (r *SessionRepository) Create(ctx context.Context, s *models.AuthSession) error {
	if err := withTx(ctx, r.db).Create(s).Error; err != nil {
		return classify(err)
	}
	return nil
}

// FindByRefreshHashForUpdate loads a session by refresh_token_hash with
// SELECT ... FOR UPDATE. It must be called inside a transaction; the row lock
// serializes concurrent refresh attempts on the same token.
func (r *SessionRepository) FindByRefreshHashForUpdate(ctx context.Context, hash []byte) (*models.AuthSession, error) {
	var s models.AuthSession
	err := withTx(ctx, r.db).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("refresh_token_hash = ?", hash).
		First(&s).Error
	if err != nil {
		return nil, classify(err)
	}
	return &s, nil
}

// Revoke marks a single session revoked with the given reason and timestamp.
// It is a no-op (no error) if the session is already revoked, which keeps
// logout idempotent.
func (r *SessionRepository) Revoke(ctx context.Context, sessionID, reason string, at time.Time) error {
	res := withTx(ctx, r.db).
		Model(&models.AuthSession{}).
		Where("id = ? AND revoked_at IS NULL", sessionID).
		Updates(map[string]any{
			"revoked_at":    at,
			"revoke_reason": reason,
		})
	if res.Error != nil {
		return classify(res.Error)
	}
	return nil
}

// RevokeActiveByFamily revokes all non-revoked sessions in a token family.
// Used on refresh-token reuse detection. Zero rows affected is not an error
// (the family may have no active sessions left).
func (r *SessionRepository) RevokeActiveByFamily(ctx context.Context, familyID, reason string, at time.Time) error {
	res := withTx(ctx, r.db).
		Model(&models.AuthSession{}).
		Where("token_family_id = ? AND revoked_at IS NULL", familyID).
		Updates(map[string]any{
			"revoked_at":    at,
			"revoke_reason": reason,
		})
	if res.Error != nil {
		return classify(res.Error)
	}
	// RowsAffected == 0 is acceptable (no active sessions in family).
	return nil
}

// RevokeActiveByUser revokes all non-revoked sessions for a user. Used by
// logout-all and password change. Zero rows affected is not an error.
func (r *SessionRepository) RevokeActiveByUser(ctx context.Context, userID, reason string, at time.Time) error {
	res := withTx(ctx, r.db).
		Model(&models.AuthSession{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{
			"revoked_at":    at,
			"revoke_reason": reason,
		})
	if res.Error != nil {
		return classify(res.Error)
	}
	// RowsAffected == 0 is acceptable (no active sessions for user).
	return nil
}

// SetReplacedBy sets replaced_by_session_id on the old session row, recording
// that it was replaced by the new session. Used during rotation.
func (r *SessionRepository) SetReplacedBy(ctx context.Context, oldID, newID string) error {
	res := withTx(ctx, r.db).
		Model(&models.AuthSession{}).
		Where("id = ?", oldID).
		Update("replaced_by_session_id", newID)
	if res.Error != nil {
		return classify(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// FindByID loads a session by id (no lock). Used by tests / admin paths.
func (r *SessionRepository) FindByID(ctx context.Context, id string) (*models.AuthSession, error) {
	var s models.AuthSession
	if err := withTx(ctx, r.db).Where("id = ?", id).First(&s).Error; err != nil {
		return nil, classify(err)
	}
	return &s, nil
}

// TxRunner runs a function within a GORM transaction. The transaction handle
// is stored in ctx so repository methods called inside fn bind to it.
type TxRunner struct {
	db *gorm.DB
}

// NewTxRunner builds a TxRunner over the given db.
func NewTxRunner(db *gorm.DB) *TxRunner {
	return &TxRunner{db: db}
}

// Run executes fn inside a serialized GORM transaction. If fn returns nil the
// transaction commits; if fn returns an error the transaction rolls back. The
// auth service relies on this for refresh-token rotation and reuse handling.
func (r *TxRunner) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		txCtx := context.WithValue(ctx, txKey{}, tx)
		return fn(txCtx)
	})
}
