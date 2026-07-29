package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// AdminStore extends GormRepository with admin write/query methods.
// These methods are used by the billing service admin endpoints.

// CreatePlan inserts a new plan row. Status defaults to "active" if empty.
func (r *GormRepository) CreatePlan(ctx context.Context, p *Plan) error {
	if p.Status == "" {
		p.Status = "active"
	}
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return ErrInsertFailed
	}
	return nil
}

// UpdatePlan modifies the mutable columns of a plan identified by id.
// Only non-nil fields in the fields map are applied.
func (r *GormRepository) UpdatePlan(ctx context.Context, id int64, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db.WithContext(ctx).Model(&Plan{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return ErrQueryFailed
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeletePlan soft-deletes a plan by setting status="deleted".
func (r *GormRepository) DeletePlan(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Model(&Plan{}).Where("id = ? AND status <> ?", id, "deleted").Update("status", "deleted")
	if res.Error != nil {
		return ErrQueryFailed
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAllUserPlans returns all user_plans across all users, paginated,
// ordered by activated_at descending.
func (r *GormRepository) ListAllUserPlans(ctx context.Context, limit, offset int) ([]UserPlan, int, error) {
	tx := r.db.WithContext(ctx).Model(&UserPlan{})
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, ErrQueryFailed
	}
	var ups []UserPlan
	if err := tx.Order("activated_at DESC").Limit(limit).Offset(offset).Find(&ups).Error; err != nil {
		return nil, 0, ErrQueryFailed
	}
	return ups, int(total), nil
}

// EnsureUser creates a user row if it does not already exist. This is
// needed when assigning a plan to a user who was created in Auth but not
// yet synced to Billing.
func (r *GormRepository) EnsureUser(ctx context.Context, userID string) error {
	const q = `INSERT INTO users (id, status) VALUES (?, 'active') ON CONFLICT (id) DO NOTHING`
	if err := r.db.WithContext(ctx).Exec(q, userID).Error; err != nil {
		return ErrInsertFailed
	}
	return nil
}

// AssignUserPlan creates a new active user_plan binding.
func (r *GormRepository) AssignUserPlan(ctx context.Context, up *UserPlan) error {
	if up.Status == "" {
		up.Status = "active"
	}
	if up.PlanType == "" {
		// Look up the plan type from the plans table.
		var p Plan
		if err := r.db.WithContext(ctx).Where("id = ?", up.PlanID).First(&p).Error; err != nil {
			return ErrNotFound
		}
		up.PlanType = p.PlanType
	}
	// Ensure the user exists in the billing users table before inserting
	// the user_plan row (foreign key constraint).
	if err := r.EnsureUser(ctx, up.UserID); err != nil {
		return err
	}
	if err := r.db.WithContext(ctx).Create(up).Error; err != nil {
		return ErrInsertFailed
	}
	return nil
}

// RenewUserPlan extends or explicitly sets the expiry of an existing user_plan.
// When expiresAt is nil, extendDays must be >0; extension starts from the
// current expires_at if it is in the future, otherwise from now. A never-expiring
// plan requires an explicit expiresAt to avoid surprising conversion.
func (r *GormRepository) RenewUserPlan(ctx context.Context, id int64, extendDays int, expiresAt *time.Time) (UserPlan, error) {
	var out UserPlan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var up UserPlan
		if err := tx.Where("id = ?", id).First(&up).Error; err != nil {
			return ErrNotFound
		}
		newExpiry := expiresAt
		if newExpiry == nil {
			if extendDays <= 0 {
				return ErrConflict
			}
			base := time.Now().UTC()
			if up.ExpiresAt != nil && up.ExpiresAt.After(base) {
				base = up.ExpiresAt.UTC()
			}
			v := base.AddDate(0, 0, extendDays)
			newExpiry = &v
		}
		res := tx.Model(&UserPlan{}).Where("id = ?", id).Updates(map[string]any{
			"expires_at": *newExpiry,
			"updated_at": time.Now().UTC(),
		})
		if res.Error != nil {
			return ErrQueryFailed
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Where("id = ?", id).First(&out).Error; err != nil {
			return ErrQueryFailed
		}
		return nil
	})
	if err != nil {
		return UserPlan{}, err
	}
	return out, nil
}

// UpgradeUserPlan is kept as a compatibility alias for SwitchUserPlan.
func (r *GormRepository) UpgradeUserPlan(ctx context.Context, id int64, newPlanID int64, expiresAt *time.Time) (UserPlan, error) {
	return r.SwitchUserPlan(ctx, id, newPlanID, expiresAt)
}

// SwitchUserPlan cancels the existing user_plan and creates a replacement with
// the new plan. It is intentionally explicit rather than mutating plan_id in
// place so historical reservations remain attributable to the original plan
// period. The target plan must be comparable and not lower than the current
// plan: same plan_type and relevant quota limits not lower. Price is not used
// as grade because operational/free gift plans can have higher quota at price 0.
func (r *GormRepository) SwitchUserPlan(ctx context.Context, id int64, newPlanID int64, expiresAt *time.Time) (UserPlan, error) {
	var created UserPlan
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var old UserPlan
		if err := tx.Where("id = ?", id).First(&old).Error; err != nil {
			return ErrNotFound
		}
		var currentPlan Plan
		if err := tx.Where("id = ?", old.PlanID).First(&currentPlan).Error; err != nil {
			return ErrNotFound
		}
		var p Plan
		if err := tx.Where("id = ? AND status = ?", newPlanID, "active").First(&p).Error; err != nil {
			return ErrNotFound
		}
		if !canSwitchPlan(currentPlan, p) {
			return ErrConflict
		}
		now := time.Now().UTC()
		if err := tx.Model(&UserPlan{}).Where("id = ? AND status = ?", id, "active").Updates(map[string]any{"status": "cancelled", "updated_at": now}).Error; err != nil {
			return ErrQueryFailed
		}
		created = UserPlan{UserID: old.UserID, PlanID: newPlanID, PlanType: p.PlanType, Status: "active", ActivatedAt: now, ExpiresAt: expiresAt}
		if err := tx.Create(&created).Error; err != nil {
			return ErrInsertFailed
		}
		return nil
	})
	if err != nil {
		return UserPlan{}, err
	}
	return created, nil
}

func canSwitchPlan(current Plan, target Plan) bool {
	if current.PlanType != target.PlanType {
		return false
	}
	switch current.PlanType {
	case "coding":
		return intLimitNotLower(current.HourlyLimit, target.HourlyLimit) &&
			intLimitNotLower(current.WeeklyLimit, target.WeeklyLimit) &&
			intLimitNotLower(current.MonthlyLimit, target.MonthlyLimit)
	case "token":
		return int64LimitNotLower(current.TokenLimit, target.TokenLimit)
	default:
		return true
	}
}

func intLimitNotLower(current *int, target *int) bool {
	currentUnlimited := current == nil || *current <= 0
	targetUnlimited := target == nil || *target <= 0
	if currentUnlimited {
		return targetUnlimited
	}
	if targetUnlimited {
		return true
	}
	return *target >= *current
}

func int64LimitNotLower(current *int64, target *int64) bool {
	if current == nil {
		return target == nil
	}
	if target == nil {
		return true
	}
	return *target >= *current
}

// CancelUserPlan marks a user_plan as cancelled. Idempotent: cancelling an
// already-cancelled plan is a no-op.
func (r *GormRepository) CancelUserPlan(ctx context.Context, id int64) error {
	res := r.db.WithContext(ctx).Model(&UserPlan{}).Where("id = ? AND status = ?", id, "active").Update("status", "cancelled")
	if res.Error != nil {
		return ErrQueryFailed
	}
	if res.RowsAffected == 0 {
		// Check if it exists at all
		var count int64
		r.db.WithContext(ctx).Model(&UserPlan{}).Where("id = ?", id).Count(&count)
		if count == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// UsageStatRow is one row of aggregated usage.
type UsageStatRow struct {
	Period       string `json:"period"`
	UserID       string `json:"user_id,omitempty"`
	BillingPlan  string `json:"billing_plan,omitempty"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
	ChargeCount  int64  `json:"charge_count"`
}

// GetUsageStats aggregates the usage_ledger over the last N days, grouped
// by day. When groupBy="user", results are grouped by user_id instead of day.
func (r *GormRepository) GetUsageStats(ctx context.Context, days int, groupBy string) ([]UsageStatRow, error) {
	if days <= 0 {
		days = 7
	}
	if days > 365 {
		days = 365
	}
	since := time.Now().AddDate(0, 0, -days)

	switch groupBy {
	case "user":
		const q = `SELECT
			'' AS period,
			user_id,
			'' AS billing_plan,
			COALESCE(SUM(CASE WHEN ledger_type='charge' THEN -request_delta ELSE 0 END), 0) AS requests,
			0 AS input_tokens,
			0 AS output_tokens,
			COALESCE(SUM(CASE WHEN ledger_type='charge' THEN -token_delta ELSE 0 END), 0) AS total_tokens,
			COUNT(CASE WHEN ledger_type='charge' THEN 1 END) AS charge_count
			FROM usage_ledger
			WHERE created_at >= ?
			GROUP BY user_id
			ORDER BY total_tokens DESC`
		var rows []UsageStatRow
		if err := r.db.WithContext(ctx).Raw(q, since).Scan(&rows).Error; err != nil {
			return nil, ErrQueryFailed
		}
		return rows, nil

	default: // "day" or empty
		const q = `SELECT
			TO_CHAR(DATE(created_at), 'YYYY-MM-DD') AS period,
			'' AS user_id,
			'' AS billing_plan,
			COALESCE(SUM(CASE WHEN ledger_type='charge' THEN -request_delta ELSE 0 END), 0) AS requests,
			0 AS input_tokens,
			0 AS output_tokens,
			COALESCE(SUM(CASE WHEN ledger_type='charge' THEN -token_delta ELSE 0 END), 0) AS total_tokens,
			COUNT(CASE WHEN ledger_type='charge' THEN 1 END) AS charge_count
			FROM usage_ledger
			WHERE created_at >= ?
			GROUP BY DATE(created_at)
			ORDER BY period DESC`
		var rows []UsageStatRow
		if err := r.db.WithContext(ctx).Raw(q, since).Scan(&rows).Error; err != nil {
			return nil, ErrQueryFailed
		}
		return rows, nil
	}
}
