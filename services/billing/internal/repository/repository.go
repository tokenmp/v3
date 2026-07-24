// Package repository persists and reads billing records (plans, user plans,
// quota reservations and the usage ledger) from the Billing DB.
//
// The billing service owns the durable write path for the "reserve then
// finalize" quota model borrowed from V2: a request start reserves quota
// (quota_reservations + usage_ledger reserve entry), request end finalizes
// (reservation → finalized + usage_ledger charge entry) or releases on
// failure (reservation → released + usage_ledger refund entry).
//
// All mutating operations are single-transaction and idempotent: the
// usage_ledger.idempotency_key UNIQUE index guarantees ledger de-duplication
// (a duplicate INSERT is collapsed via ON CONFLICT DO NOTHING), and
// quota_reservations.id (text PK) makes Reserve idempotent per reservation
// ID. Finalize/Release detect already-terminal rows and return without
// re-charging.
//
// Errors are stable sentinels. They never wrap the driver error, whose
// message may carry the DSN or connection string fragments, so the public
// Error() surface is safe to log. Use errors.Is to branch on the failure
// class.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// Plan corresponds to the plans table (套餐定义). price is numeric(12,2);
// the nullable limit columns use pointers so a NULL stays NULL rather than
// being confused with a zero limit.
type Plan struct {
	ID            int64           `json:"id" gorm:"column:id"`
	Name          string          `json:"name" gorm:"column:name"`
	PlanType      string          `json:"plan_type" gorm:"column:plan_type"`
	Price         float64         `json:"price" gorm:"column:price"`
	Category      string          `json:"category" gorm:"column:category"`
	HourlyLimit   *int            `json:"hourly_limit,omitempty" gorm:"column:hourly_limit"`
	WeeklyLimit   *int            `json:"weekly_limit,omitempty" gorm:"column:weekly_limit"`
	MonthlyLimit  *int            `json:"monthly_limit,omitempty" gorm:"column:monthly_limit"`
	TokenLimit    *int64          `json:"token_limit,omitempty" gorm:"column:token_limit"`
	AllowedModels json.RawMessage `json:"allowed_models" gorm:"column:allowed_models"`
	Status        string          `json:"status" gorm:"column:status"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

// User corresponds to the users table (计费最小用户引用; 主数据在 Auth/Identity 库).
type User struct {
	ID        string    `json:"id" gorm:"column:id"`
	Status    string    `json:"status" gorm:"column:status"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at"`
}

// UserPlan corresponds to the user_plans table (用户套餐绑定).
type UserPlan struct {
	ID          int64      `json:"id" gorm:"column:id"`
	UserID      string     `json:"user_id" gorm:"column:user_id"`
	PlanID      int64      `json:"plan_id" gorm:"column:plan_id"`
	PlanType    string     `json:"plan_type" gorm:"column:plan_type"`
	Status      string     `json:"status" gorm:"column:status"`
	ActivatedAt time.Time  `json:"activated_at" gorm:"column:activated_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" gorm:"column:expires_at"`
	CreatedAt   time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

// QuotaReservation corresponds to the quota_reservations table (配额预留).
// Its text PK is the reservation ID carried on the V3 request. Nullable
// final_* / finalized_at / expires_at use pointers.
type QuotaReservation struct {
	ID               string     `json:"id" gorm:"column:id"`
	UserID           string     `json:"user_id" gorm:"column:user_id"`
	RequestID        string     `json:"request_id" gorm:"column:request_id"`
	BillingPlan      string     `json:"billing_plan" gorm:"column:billing_plan"`
	Status           string     `json:"status" gorm:"column:status"`
	ReservedRequests *int       `json:"reserved_requests,omitempty" gorm:"column:reserved_requests"`
	ReservedTokens   *int64     `json:"reserved_tokens,omitempty" gorm:"column:reserved_tokens"`
	FinalRequests    *int       `json:"final_requests,omitempty" gorm:"column:final_requests"`
	FinalTokens      *int64     `json:"final_tokens,omitempty" gorm:"column:final_tokens"`
	ReservedAt       time.Time  `json:"reserved_at" gorm:"column:reserved_at"`
	FinalizedAt      *time.Time `json:"finalized_at,omitempty" gorm:"column:finalized_at"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty" gorm:"column:expires_at"`
}

// UsageLedgerEntry corresponds to the usage_ledger table (用量账本流水).
// token_delta/request_delta are signed movements (正=增 负=减); reserve and
// charge carry negative deltas (held/consumed quota), refund carries the
// positive reversal of the held amount.
type UsageLedgerEntry struct {
	ID             int64     `json:"id" gorm:"column:id"`
	UserID         string    `json:"user_id" gorm:"column:user_id"`
	RequestID      string    `json:"request_id,omitempty" gorm:"column:request_id"`
	LedgerType     string    `json:"ledger_type" gorm:"column:ledger_type"`
	BillingPlan    string    `json:"billing_plan" gorm:"column:billing_plan"`
	TokenDelta     int64     `json:"token_delta" gorm:"column:token_delta"`
	RequestDelta   int       `json:"request_delta" gorm:"column:request_delta"`
	Reason         string    `json:"reason,omitempty" gorm:"column:reason"`
	IdempotencyKey string    `json:"idempotency_key" gorm:"column:idempotency_key"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
}

// PlanReader reads 套餐 definitions.
type PlanReader interface {
	// GetPlan returns the plan with the given id. Returns ErrNotFound when
	// no row matches.
	GetPlan(ctx context.Context, id int64) (Plan, error)
	// ListPlans returns plans filtered by status. An empty status returns
	// all plans. Rows are ordered by id ascending for a stable listing.
	ListPlans(ctx context.Context, status string) ([]Plan, error)
}

// UserPlanReader reads a user's current effective plan binding.
type UserPlanReader interface {
	// GetActiveUserPlan returns the user's most recently activated
	// active user_plan. Returns ErrNotFound when the user has none.
	GetActiveUserPlan(ctx context.Context, userID string) (UserPlan, error)
}

// QuotaManager implements the "reserve then finalize" quota lifecycle:
// Reserve at request start, Finalize at request end (success) or Release on
// failure/cancel. All three are idempotent.
type QuotaManager interface {
	// Reserve creates a 'reserved' quota_reservations row and a 'reserve'
	// usage_ledger entry (token/request deltas = -reserved). Re-calling with
	// the same reservationID is a no-op (ON CONFLICT DO NOTHING on both the
	// reservation PK and the ledger idempotency_key).
	Reserve(ctx context.Context, reservationID, userID, requestID, billingPlan string, reservedReqs int, reservedTokens int64, expiresAt *time.Time) error
	// Finalize settles a reservation: marks it 'finalized' with the final
	// request/token counts and appends a 'charge' ledger entry
	// (deltas = -final). Idempotent: re-finalizing a finalized reservation
	// returns nil without re-charging. A missing reservation returns
	// ErrNotFound; a released/expired reservation returns ErrConflict.
	Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64) error
	// Release cancels a reservation: marks it 'released' and appends a
	// 'refund' ledger entry that reverses the held amount (+reserved).
	// Idempotent: re-releasing a released reservation returns nil. A
	// finalized reservation returns ErrConflict (cannot release a settled
	// reservation); a missing reservation returns ErrNotFound.
	Release(ctx context.Context, reservationID string) error
}

// LedgerReader reads the usage ledger for a user.
type LedgerReader interface {
	// ListLedger returns the user's ledger entries newest-first. limit is
	// clamped to (0,1000] with a default of 100.
	ListLedger(ctx context.Context, userID string, limit int) ([]UsageLedgerEntry, error)
}

// Stable classified errors. They do not wrap the driver error so DSN/SQL
// fragments never reach logs through Error().
var (
	ErrNotFound     = errors.New("repository: not found")
	ErrQueryFailed  = errors.New("repository: query failed")
	ErrInsertFailed = errors.New("repository: insert failed")
	ErrConflict     = errors.New("repository: conflicting state")
)

// GormRepository persists and reads billing records via GORM. It is the
// single production implementation of PlanReader, UserPlanReader,
// QuotaManager and LedgerReader.
type GormRepository struct {
	db *gorm.DB
}

// New returns a GORM-backed repository.
func New(db *gorm.DB) *GormRepository {
	return &GormRepository{db: db}
}

// Compile-time assertions that GormRepository satisfies every port.
var (
	_ PlanReader     = (*GormRepository)(nil)
	_ UserPlanReader = (*GormRepository)(nil)
	_ QuotaManager   = (*GormRepository)(nil)
	_ LedgerReader   = (*GormRepository)(nil)
)

// ----------------------------------------------------------------------------
// PlanReader
// ----------------------------------------------------------------------------

const planColumns = `id, name, plan_type, price, category, hourly_limit, weekly_limit, monthly_limit, token_limit, allowed_models, status, created_at, updated_at`

// GetPlan looks up a plan by id. A query error is ErrQueryFailed; a missing
// row is ErrNotFound (detected via the zero id since Raw().Scan() does not
// return gorm.ErrRecordNotFound for a struct scan).
func (r *GormRepository) GetPlan(ctx context.Context, id int64) (Plan, error) {
	const q = `SELECT ` + planColumns + ` FROM plans WHERE id = ? LIMIT 1`
	var row Plan
	if err := r.db.WithContext(ctx).Raw(q, id).Scan(&row).Error; err != nil {
		return Plan{}, ErrQueryFailed
	}
	if row.ID == 0 {
		return Plan{}, ErrNotFound
	}
	return row, nil
}

// ListPlans returns plans filtered by status (empty = all) ordered by id.
func (r *GormRepository) ListPlans(ctx context.Context, status string) ([]Plan, error) {
	q := `SELECT ` + planColumns + ` FROM plans`
	var args []any
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id ASC`
	var rows []Plan
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}

// ----------------------------------------------------------------------------
// UserPlanReader
// ----------------------------------------------------------------------------

const userPlanColumns = `id, user_id, plan_id, plan_type, status, activated_at, expires_at, created_at, updated_at`

// GetActiveUserPlan returns the user's most recently activated active
// user_plan. Missing → ErrNotFound.
func (r *GormRepository) GetActiveUserPlan(ctx context.Context, userID string) (UserPlan, error) {
	const q = `SELECT ` + userPlanColumns + ` FROM user_plans
WHERE user_id = ? AND status = 'active'
ORDER BY activated_at DESC, id DESC
LIMIT 1`
	var row UserPlan
	if err := r.db.WithContext(ctx).Raw(q, userID).Scan(&row).Error; err != nil {
		return UserPlan{}, ErrQueryFailed
	}
	if row.ID == 0 {
		return UserPlan{}, ErrNotFound
	}
	return row, nil
}

// ----------------------------------------------------------------------------
// QuotaManager
// ----------------------------------------------------------------------------

const insertReservationSQL = `INSERT INTO quota_reservations (
  id, user_id, request_id, billing_plan, status, reserved_requests,
  reserved_tokens, reserved_at, expires_at
) VALUES (
  ?, ?, ?, ?, 'reserved', ?, ?, ?, ?
)
ON CONFLICT (id) DO NOTHING`

const insertLedgerSQL = `INSERT INTO usage_ledger (
  user_id, request_id, ledger_type, billing_plan, token_delta, request_delta,
  reason, idempotency_key, created_at
) VALUES (
  ?, NULLIF(?, '')::text, ?, ?, ?, ?, NULLIF(?, '')::text, ?, ?
)
ON CONFLICT (idempotency_key) DO NOTHING`

// Reserve creates the reservation and its 'reserve' ledger entry in a single
// transaction. Idempotent per reservationID: a repeat call collides on the
// reservation PK and the ledger idempotency_key, both handled by ON CONFLICT
// DO NOTHING, so no duplicate rows are created.
func (r *GormRepository) Reserve(ctx context.Context, reservationID, userID, requestID, billingPlan string, reservedReqs int, reservedTokens int64, expiresAt *time.Time) error {
	now := time.Now().UTC()
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return ErrInsertFailed
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback().Error
		}
	}()

	if err := tx.Exec(insertReservationSQL,
		reservationID, userID, requestID, billingPlan,
		reservedReqs, reservedTokens, now, expiresAt,
	).Error; err != nil {
		return ErrInsertFailed
	}

	if err := tx.Exec(insertLedgerSQL,
		userID, requestID, "reserve", billingPlan,
		-reservedTokens, -reservedReqs, "reserve",
		reservationID+":reserve", now,
	).Error; err != nil {
		return ErrInsertFailed
	}

	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// reservationStatusRow is the minimal projection Finalize/Release need to
// decide on the reservation's current state and emit the matching ledger row.
type reservationStatusRow struct {
	Status           string `gorm:"column:status"`
	UserID           string `gorm:"column:user_id"`
	RequestID        string `gorm:"column:request_id"`
	BillingPlan      string `gorm:"column:billing_plan"`
	ReservedRequests *int   `gorm:"column:reserved_requests"`
	ReservedTokens   *int64 `gorm:"column:reserved_tokens"`
}

// Finalize settles a reserved reservation. Idempotent: a finalized
// reservation returns nil without re-charging; a released/expired one returns
// ErrConflict; a missing one returns ErrNotFound.
func (r *GormRepository) Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64) error {
	now := time.Now().UTC()
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return ErrInsertFailed
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback().Error
		}
	}()

	const sel = `SELECT status, user_id, request_id, billing_plan
FROM quota_reservations WHERE id = ? LIMIT 1`
	var row reservationStatusRow
	if err := tx.Raw(sel, reservationID).Scan(&row).Error; err != nil {
		return ErrQueryFailed
	}
	if row.Status == "" {
		return ErrNotFound
	}
	switch row.Status {
	case "finalized":
		// Already settled — idempotent success, no re-charge. Leave the
		// transaction to the deferred Rollback so the connection is returned
		// to the pool clean (no open transaction holding locks).
		return nil
	case "reserved":
		// proceed
	default:
		// released / expired — cannot finalize.
		return ErrConflict
	}

	res := tx.Exec(`UPDATE quota_reservations
SET status = 'finalized', final_requests = ?, final_tokens = ?, finalized_at = ?
WHERE id = ? AND status = 'reserved'`,
		finalReqs, finalTokens, now, reservationID)
	if res.Error != nil {
		return ErrInsertFailed
	}
	if res.RowsAffected == 0 {
		// Concurrent state change; treat as idempotent success to avoid a
		// duplicate charge under racing finalizers.
		return nil
	}

	if err := tx.Exec(insertLedgerSQL,
		row.UserID, row.RequestID, "charge", row.BillingPlan,
		-finalTokens, -finalReqs, "charge",
		reservationID+":charge", now,
	).Error; err != nil {
		return ErrInsertFailed
	}

	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// Release cancels a reserved reservation and reverses the held amount.
// Idempotent: a released reservation returns nil; a finalized/expired one
// returns ErrConflict; a missing one returns ErrNotFound.
func (r *GormRepository) Release(ctx context.Context, reservationID string) error {
	now := time.Now().UTC()
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return ErrInsertFailed
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback().Error
		}
	}()

	const sel = `SELECT status, user_id, request_id, billing_plan, reserved_requests, reserved_tokens
FROM quota_reservations WHERE id = ? LIMIT 1`
	var row reservationStatusRow
	if err := tx.Raw(sel, reservationID).Scan(&row).Error; err != nil {
		return ErrQueryFailed
	}
	if row.Status == "" {
		return ErrNotFound
	}
	switch row.Status {
	case "released":
		// Already released — idempotent success, no re-refund. Leave the
		// transaction to the deferred Rollback so the connection is returned
		// to the pool clean.
		return nil
	case "reserved":
		// proceed
	default:
		// finalized / expired — cannot release.
		return ErrConflict
	}

	res := tx.Exec(`UPDATE quota_reservations
SET status = 'released'
WHERE id = ? AND status = 'reserved'`, reservationID)
	if res.Error != nil {
		return ErrInsertFailed
	}
	if res.RowsAffected == 0 {
		return nil
	}

	reservedReqs := 0
	if row.ReservedRequests != nil {
		reservedReqs = *row.ReservedRequests
	}
	reservedTokens := int64(0)
	if row.ReservedTokens != nil {
		reservedTokens = *row.ReservedTokens
	}

	if err := tx.Exec(insertLedgerSQL,
		row.UserID, row.RequestID, "refund", row.BillingPlan,
		reservedTokens, reservedReqs, "release",
		reservationID+":refund", now,
	).Error; err != nil {
		return ErrInsertFailed
	}

	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// ----------------------------------------------------------------------------
// LedgerReader
// ----------------------------------------------------------------------------

// ListLedger returns the user's ledger entries newest-first. limit is clamped
// to (0,1000] with a default of 100.
func (r *GormRepository) ListLedger(ctx context.Context, userID string, limit int) ([]UsageLedgerEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	const q = `SELECT id, user_id, request_id, ledger_type, billing_plan, token_delta,
       request_delta, reason, idempotency_key, created_at
FROM usage_ledger
WHERE user_id = ?
ORDER BY created_at DESC, id DESC
LIMIT ?`
	var rows []UsageLedgerEntry
	if err := r.db.WithContext(ctx).Raw(q, userID, limit).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}
