package repository

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
)

// UserPlanLimitOverride corresponds to the user_plan_limit_overrides table.
//
// A limit override adjusts how a single scope (hour5/weekly/period) of one
// user_plan is enforced:
//
//   - kind="reset": moves the scope's effective window start forward to
//     max(baseStart, effective_from), forgiving consumption before that point.
//   - kind="bonus": adds bonus_requests to the scope's base limit while the
//     override is active (now >= effective_from AND (effective_until IS NULL
//     OR now < effective_until)).
//
// Revocation is soft: effective_until is set to now(); no status column is
// needed. Request timestamps (usage_ledger.created_at etc.) are never
// rewritten — overrides only change how consumption is read.
type UserPlanLimitOverride struct {
	ID             int64      `json:"id" gorm:"column:id"`
	UserPlanID     int64      `json:"user_plan_id" gorm:"column:user_plan_id"`
	Kind           string     `json:"kind" gorm:"column:kind"`
	Scope          string     `json:"scope" gorm:"column:scope"`
	EffectiveFrom  time.Time  `json:"effective_from" gorm:"column:effective_from"`
	EffectiveUntil *time.Time `json:"effective_until,omitempty" gorm:"column:effective_until"`
	BonusRequests  *int       `json:"bonus_requests,omitempty" gorm:"column:bonus_requests"`
	Reason         string     `json:"reason,omitempty" gorm:"column:reason"`
	CreatedBy      string     `json:"created_by,omitempty" gorm:"column:created_by"`
	CreatedAt      time.Time  `json:"created_at" gorm:"column:created_at"`
}

const overrideColumns = `id, user_plan_id, kind, scope, effective_from, effective_until, bonus_requests, reason, created_by, created_at`

// validOverrideKind reports whether kind is an allowed override kind.
func validOverrideKind(kind string) bool {
	return kind == "reset" || kind == "bonus"
}

// validOverrideScope reports whether scope is an allowed override scope. It
// accepts the QuotaScope string form so callers can pass string(scope) or a
// raw string interchangeably.
func validOverrideScope(scope string) bool {
	return scope == string(ScopeHour5) || scope == string(ScopeWeekly) || scope == string(ScopePeriod)
}

// CreateLimitOverride inserts a new user_plan_limit_overrides row. kind and
// scope must be valid; a "bonus" override requires a non-nil, non-negative
// bonus_requests. effective_from defaults to now() when zero. The caller is
// responsible for ensuring user_plan_id references an existing user_plan
// (the FK enforces this at the DB level; a violation is ErrInsertFailed).
func (r *GormRepository) CreateLimitOverride(ctx context.Context, o *UserPlanLimitOverride) error {
	if o == nil {
		return ErrInsertFailed
	}
	if !validOverrideKind(o.Kind) {
		return ErrInsertFailed
	}
	if !validOverrideScope(o.Scope) {
		return ErrInsertFailed
	}
	if o.Kind == "bonus" {
		if o.BonusRequests == nil || *o.BonusRequests < 0 {
			return ErrInsertFailed
		}
	}
	if o.EffectiveFrom.IsZero() {
		o.EffectiveFrom = time.Now().UTC()
	}
	if err := r.db.WithContext(ctx).Exec(`INSERT INTO user_plan_limit_overrides
(user_plan_id, kind, scope, effective_from, effective_until, bonus_requests, reason, created_by)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		o.UserPlanID, o.Kind, o.Scope, o.EffectiveFrom, o.EffectiveUntil, o.BonusRequests, o.Reason, o.CreatedBy,
	).Error; err != nil {
		return ErrInsertFailed
	}
	// Backfill the generated id/created_at so the caller can return the row.
	if err := r.db.WithContext(ctx).Raw(`SELECT `+overrideColumns+` FROM user_plan_limit_overrides
WHERE user_plan_id = ? AND kind = ? AND scope = ? AND effective_from = ?
ORDER BY id DESC LIMIT 1`, o.UserPlanID, o.Kind, o.Scope, o.EffectiveFrom).Scan(o).Error; err != nil {
		return ErrQueryFailed
	}
	return nil
}

// ListLimitOverrides returns all overrides for a user_plan, newest-first.
func (r *GormRepository) ListLimitOverrides(ctx context.Context, userPlanID int64) ([]UserPlanLimitOverride, error) {
	const q = `SELECT ` + overrideColumns + ` FROM user_plan_limit_overrides
WHERE user_plan_id = ?
ORDER BY created_at DESC, id DESC`
	var rows []UserPlanLimitOverride
	if err := r.db.WithContext(ctx).Raw(q, userPlanID).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	if rows == nil {
		rows = []UserPlanLimitOverride{}
	}
	return rows, nil
}

// RevokeLimitOverride soft-revokes an override by setting effective_until to
// now() when it is still NULL. Idempotent: revoking an already-revoked
// override is a no-op. A missing override returns ErrNotFound.
func (r *GormRepository) RevokeLimitOverride(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Exec(`UPDATE user_plan_limit_overrides
SET effective_until = ?
WHERE id = ? AND effective_until IS NULL`, now, id)
	if res.Error != nil {
		return ErrQueryFailed
	}
	if res.RowsAffected == 0 {
		// Either it does not exist, or it was already revoked. Distinguish.
		var exists int
		if err := r.db.WithContext(ctx).Raw(`SELECT 1 FROM user_plan_limit_overrides WHERE id = ? LIMIT 1`, id).Scan(&exists).Error; err != nil {
			return ErrQueryFailed
		}
		if exists == 0 {
			return ErrNotFound
		}
		// Already revoked — idempotent success.
		return nil
	}
	return nil
}

// ----------------------------------------------------------------------------
// Override effects on enforcement / usage windows
// ----------------------------------------------------------------------------

// windowResult is the computed effective window for one scope after applying
// active reset (moves start forward) and bonus (adds to limit) overrides.
type windowResult struct {
	EffectiveStart time.Time
	AdjustedLimit  int
	Consumed       int
}

// overrideEffects computes the effective window start (after applying the
// latest active reset override) and the total active bonus for a scope.
//
// A reset is active when effective_from <= now AND (effective_until IS NULL
// OR effective_until > now); its effective_from competes with baseStart via
// max(). A bonus is active under the same time predicate and its
// bonus_requests are summed.
//
// tx may be a *gorm.DB or a transaction; the reads are side-effect free.
func overrideEffects(tx *gorm.DB, userPlanID int64, scope string, baseStart, now time.Time) (time.Time, int, error) {
	var resetFrom sql.NullTime
	const resetQ = `SELECT MAX(effective_from) FROM user_plan_limit_overrides
WHERE user_plan_id = ? AND kind = 'reset' AND scope = ?
  AND effective_from <= ?
  AND (effective_until IS NULL OR effective_until > ?)`
	if err := tx.Raw(resetQ, userPlanID, scope, now, now).Scan(&resetFrom).Error; err != nil {
		return time.Time{}, 0, ErrQueryFailed
	}
	effStart := baseStart
	if resetFrom.Valid && resetFrom.Time.After(baseStart) {
		effStart = resetFrom.Time
	}

	var bonus int
	const bonusQ = `SELECT COALESCE(SUM(bonus_requests), 0) FROM user_plan_limit_overrides
WHERE user_plan_id = ? AND kind = 'bonus' AND scope = ?
  AND effective_from <= ?
  AND (effective_until IS NULL OR effective_until > ?)`
	if err := tx.Raw(bonusQ, userPlanID, scope, now, now).Scan(&bonus).Error; err != nil {
		return time.Time{}, 0, ErrQueryFailed
	}
	return effStart, bonus, nil
}

// computeWindow computes the effective window for one scope: the adjusted
// limit (base + active bonus), the effective start (max(baseStart, latest
// active reset effective_from)), and consumed finalized 'charge' coding
// requests since that effective start.
func computeWindow(tx *gorm.DB, userID string, userPlanID int64, scope string, baseStart time.Time, baseLimit int, now time.Time) (windowResult, error) {
	effStart, bonus, err := overrideEffects(tx, userPlanID, scope, baseStart, now)
	if err != nil {
		return windowResult{}, err
	}
	consumed, err := consumedCodingSince(tx, userID, effStart)
	if err != nil {
		return windowResult{}, err
	}
	return windowResult{EffectiveStart: effStart, AdjustedLimit: baseLimit + bonus, Consumed: consumed}, nil
}
