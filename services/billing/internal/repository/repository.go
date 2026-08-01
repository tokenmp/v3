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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// UserPlanDetail is a user_plan joined with its immutable-ish plan definition.
// It is used by read APIs that render quota cards and should not be used for
// billing lifecycle writes.
type UserPlanDetail struct {
	UserPlan
	PlanName     string  `json:"plan_name" gorm:"column:plan_name"`
	Category     string  `json:"category" gorm:"column:category"`
	Price        float64 `json:"price" gorm:"column:price"`
	HourlyLimit  *int    `json:"hourly_limit,omitempty" gorm:"column:hourly_limit"`
	WeeklyLimit  *int    `json:"weekly_limit,omitempty" gorm:"column:weekly_limit"`
	MonthlyLimit *int    `json:"monthly_limit,omitempty" gorm:"column:monthly_limit"`
	TokenLimit   *int64  `json:"token_limit,omitempty" gorm:"column:token_limit"`
	PlanStatus   string  `json:"plan_status" gorm:"column:plan_status"`
}

// QuotaReservation corresponds to the quota_reservations table (配额预留).
// Its text PK is the reservation ID carried on the V3 request. Nullable
// final_* / finalized_at / expires_at use pointers. The settlement columns
// (usage_known, settlement_status, reconciled_at, idempotency_payload_hash)
// were added by migration 000004 to make Reserve/Finalize/Release a durable,
// auditable state machine rather than mechanical ledger writes.
type QuotaReservation struct {
	ID                     string     `json:"id" gorm:"column:id"`
	UserID                 string     `json:"user_id" gorm:"column:user_id"`
	RequestID              string     `json:"request_id" gorm:"column:request_id"`
	BillingPlan            string     `json:"billing_plan" gorm:"column:billing_plan"`
	Status                 string     `json:"status" gorm:"column:status"`
	ReservedRequests       *int       `json:"reserved_requests,omitempty" gorm:"column:reserved_requests"`
	ReservedTokens         *int64     `json:"reserved_tokens,omitempty" gorm:"column:reserved_tokens"`
	FinalRequests          *int       `json:"final_requests,omitempty" gorm:"column:final_requests"`
	FinalTokens            *int64     `json:"final_tokens,omitempty" gorm:"column:final_tokens"`
	UsageKnown             bool       `json:"usage_known" gorm:"column:usage_known"`
	SettlementStatus       string     `json:"settlement_status,omitempty" gorm:"column:settlement_status"`
	IdempotencyPayloadHash string     `json:"idempotency_payload_hash,omitempty" gorm:"column:idempotency_payload_hash"`
	ReservedAt             time.Time  `json:"reserved_at" gorm:"column:reserved_at"`
	FinalizedAt            *time.Time `json:"finalized_at,omitempty" gorm:"column:finalized_at"`
	ReconciledAt           *time.Time `json:"reconciled_at,omitempty" gorm:"column:reconciled_at"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty" gorm:"column:expires_at"`
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
	// ListActiveUserPlans returns all active user_plans joined with plan metadata,
	// newest-first. Missing users/plans return an empty slice, not ErrNotFound.
	ListActiveUserPlans(ctx context.Context, userID string) ([]UserPlanDetail, error)
}

// QuotaManager implements the "reserve then finalize" quota lifecycle:
// Reserve at request start, Finalize at request end (success) or Release on
// failure/cancel. All three are idempotent. Finalize distinguishes a
// duplicate payload (idempotent nil) from a conflicting payload/opposite
// terminal (ErrConflict) so callers can surface a stable 409.
type QuotaManager interface {
	// Reserve creates a 'reserved' quota_reservations row and a 'reserve'
	// usage_ledger entry (token/request deltas = -reserved). Re-calling with
	// the same reservationID is a no-op (ON CONFLICT DO NOTHING on both the
	// reservation PK and the ledger idempotency_key). Active reserved holds
	// count against coding windows so concurrent requests cannot punch
	// through a limit; a per-user advisory lock serializes the check.
	Reserve(ctx context.Context, reservationID, userID, requestID, billingPlan string, reservedReqs int, reservedTokens int64, expiresAt *time.Time) error
	// Finalize settles a reservation: marks it 'finalized' with the final
	// request/token counts and appends a 'charge' ledger entry
	// (deltas = -final). usageKnown reports whether final usage is confirmed;
	// when false the caller should MarkPending instead. Idempotent: re-
	// finalizing with the SAME payload returns nil without re-charging. A
	// different payload, or an already-released/expired/pending reservation,
	// returns ErrConflict. A missing reservation returns ErrNotFound.
	Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64, usageKnown bool) error
	// Release cancels a reservation: marks it 'released' and appends a
	// 'refund' ledger entry that reverses the held amount (+reserved).
	// Idempotent: re-releasing a released reservation returns nil. A
	// finalized reservation returns ErrConflict (cannot release a settled
	// reservation); a missing reservation returns ErrNotFound.
	Release(ctx context.Context, reservationID string) error
}

// ReservationStatus is the safe projection of a quota_reservations row
// returned by GetReservation. It never carries credentials and only exposes
// the settlement-relevant fields an Edge reconciler needs.
type ReservationStatus struct {
	ID               string     `json:"reservation_id"`
	UserID           string     `json:"user_id"`
	RequestID        string     `json:"request_id"`
	BillingPlan      string     `json:"billing_plan"`
	Status           string     `json:"status"`
	SettlementStatus string     `json:"settlement_status,omitempty"`
	ReservedRequests *int       `json:"reserved_requests,omitempty"`
	ReservedTokens   *int64     `json:"reserved_tokens,omitempty"`
	FinalRequests    *int       `json:"final_requests,omitempty"`
	FinalTokens      *int64     `json:"final_tokens,omitempty"`
	UsageKnown       bool       `json:"usage_known"`
	ReservedAt       time.Time  `json:"reserved_at"`
	FinalizedAt      *time.Time `json:"finalized_at,omitempty"`
	ReconciledAt     *time.Time `json:"reconciled_at,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

// PendingReservation is a pending_reconciliation reservation projected for
// the reconciler. It carries the request_id/user_id/billing_plan/reserved
// count so the reconciler can query usage evidence and compute the confirmed
// counts WITHOUT a second GetReservation round-trip per row.
type PendingReservation struct {
	ID               string
	RequestID        string
	UserID           string
	BillingPlan      string
	ReservedRequests int
	ReservedAt       time.Time
}

// SettlementManager exposes the durable settlement state machine beyond the
// synchronous Reserve/Finalize/Release path: status reads, pending marking,
// reconciliation and expiry. These methods back the reconciler/sweeper and the
// Edge terminal-status endpoint. They never leak SQL/DSN.
type SettlementManager interface {
	// GetReservation returns the safe settlement projection for a
	// reservation. Missing → ErrNotFound.
	GetReservation(ctx context.Context, reservationID string) (ReservationStatus, error)
	// MarkPending transitions a reserved reservation to
	// 'pending_reconciliation' when the caller cannot produce confirmed
	// usage (e.g. stream committed but usage unknown, or Billing was
	// temporarily unavailable at finalize time). The held amount stays on
	// the ledger; a reconciler later resolves it via Reconcile. Idempotent:
	// re-marking a pending reservation is nil; a finalized/released/
	// expired reservation returns ErrConflict; missing → ErrNotFound.
	MarkPending(ctx context.Context, reservationID string) error
	// Reconcile resolves a pending_reconciliation reservation using
	// confirmed usage evidence. When usageKnown is true it Finalizes the
	// actual counts (settled); when false it Releases the held amount
	// (released, unknown usage must never be guessed). Idempotent per
	// reservation: re-reconciling a settled/released reservation is nil.
	// A reserved (non-pending) reservation returns ErrConflict; missing →
	// ErrNotFound.
	Reconcile(ctx context.Context, reservationID string, finalReqs int, finalTokens int64, usageKnown bool) error
	// Expire sweeps a reserved reservation whose expires_at is in the past,
	// marking it 'expired' and appending a 'sweep' refund ledger row. It is
	// the durable cleanup for orphan holds; it never deletes ledger rows.
	// Idempotent: expiring an already-expired reservation is nil; a non-
	// expired reserved reservation returns ErrConflict (not yet due);
	// finalized/released/pending → ErrConflict; missing → ErrNotFound.
	Expire(ctx context.Context, reservationID string) error
}

// LedgerReader reads the usage ledger for a user.
type LedgerReader interface {
	// ListLedger returns the user's ledger entries newest-first. limit is
	// clamped to (0,1000] with a default of 100.
	ListLedger(ctx context.Context, userID string, limit int) ([]UsageLedgerEntry, error)
}

// Balance is the user's remaining quota snapshot derived from active
// plan limits and the usage ledger. CodingRemaining is requests remaining
// in the current calendar month; TokenRemaining is the all-time token
// balance (token plans are total-quota, not periodic). Both are clamped
// to >=0 so a user who overspent never reports a negative balance.
type Balance struct {
	CodingRemaining int64
	TokenRemaining  int64
}

// BalanceReader computes a user's current balance.
type BalanceReader interface {
	// GetBalance returns the user's remaining coding-request and token
	// quotas. A user with no active plans and no ledger returns zeros; it
	// is never an error (ErrNotFound is reserved for the user-plan lookup).
	GetBalance(ctx context.Context, userID string) (Balance, error)
}

// UsageWindow is one rate-limit window for an active coding plan. Limit is
// the plan's hourly/weekly/monthly limit (non-nil only when the plan sets
// it); Consumed is the count of confirmed coding requests inside
// [WindowStart, WindowEnd) — synchronous 'charge' rows plus confirmed
// 'reconcile' rows (deferred settlement), both carrying a negative
// request_delta; refund/sweep reversals are excluded. Remaining =
// max(0, Limit-Consumed). WindowEnd is nil for the rolling hour5 and
// open-ended period windows.
type UsageWindow struct {
	Scope       string     `json:"scope"`
	Limit       *int       `json:"limit,omitempty"`
	Consumed    int        `json:"consumed"`
	Remaining   int        `json:"remaining"`
	WindowStart time.Time  `json:"window_start"`
	WindowEnd   *time.Time `json:"window_end,omitempty"`
}

// UsageWindowsReader exposes the current coding usage windows for a user's
// active coding plan(s), so callers (panel/BFF) can show remaining quota.
type UsageWindowsReader interface {
	// GetUsageWindows returns the hourly/weekly/monthly usage windows for
	// the user's most recently activated active coding plan. A user with no
	// active coding plan returns an empty slice (not an error).
	GetUsageWindows(ctx context.Context, userID string) ([]UsageWindow, error)
}

// Stable classified errors. They do not wrap the driver error so DSN/SQL
// fragments never reach logs through Error().
var (
	ErrNotFound      = errors.New("repository: not found")
	ErrQueryFailed   = errors.New("repository: query failed")
	ErrInsertFailed  = errors.New("repository: insert failed")
	ErrConflict      = errors.New("repository: conflicting state")
	ErrQuotaExceeded = errors.New("repository: quota exceeded")
)

// QuotaScope identifies which coding window was exceeded.
type QuotaScope string

const (
	ScopeHour5  QuotaScope = "hour5"  // 5-hour rolling requests window (hourly_limit)
	ScopeWeekly QuotaScope = "weekly" // Monday 00:00 UTC → next Monday 00:00 UTC (weekly_limit)
	ScopePeriod QuotaScope = "period" // active user_plan activated_at → expires_at (monthly_limit)
)

// QuotaExceededError carries the scope (and accounting) of the exceeded
// coding window. Error() returns a stable, secret-free message so it is safe
// to log; use errors.Is(err, ErrQuotaExceeded) or errors.As to recover the
// scope. Wanted is the number of requests the Reserve tried to hold.
type QuotaExceededError struct {
	Scope    QuotaScope
	Limit    int
	Consumed int
	Wanted   int
}

func (e *QuotaExceededError) Error() string { return "repository: quota exceeded" }
func (e *QuotaExceededError) Unwrap() error { return ErrQuotaExceeded }

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
	_ PlanReader         = (*GormRepository)(nil)
	_ UserPlanReader     = (*GormRepository)(nil)
	_ QuotaManager       = (*GormRepository)(nil)
	_ SettlementManager  = (*GormRepository)(nil)
	_ LedgerReader       = (*GormRepository)(nil)
	_ BalanceReader      = (*GormRepository)(nil)
	_ UsageWindowsReader = (*GormRepository)(nil)
)

// Compile-time assertion that GormRepository implements the admin override
// methods (the server.AdminStore interface is asserted in the server pkg).
var _ interface {
	CreateLimitOverride(context.Context, *UserPlanLimitOverride) error
	ListLimitOverrides(context.Context, int64) ([]UserPlanLimitOverride, error)
	RevokeLimitOverride(context.Context, int64) error
} = (*GormRepository)(nil)

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

// ListActiveUserPlans returns all active user plans with the plan metadata the
// user panel needs to display quotas and expiry. Expired rows that have not yet
// been marked expired are excluded by expires_at so the UI never shows stale
// entitlements as current.
func (r *GormRepository) ListActiveUserPlans(ctx context.Context, userID string) ([]UserPlanDetail, error) {
	const q = `SELECT
	up.id, up.user_id, up.plan_id, up.plan_type, up.status, up.activated_at, up.expires_at, up.created_at, up.updated_at,
	p.name AS plan_name, p.category, p.price, p.hourly_limit, p.weekly_limit, p.monthly_limit, p.token_limit, p.status AS plan_status
FROM user_plans up
JOIN plans p ON p.id = up.plan_id
WHERE up.user_id = ?
  AND up.status = 'active'
  AND p.status = 'active'
  AND (up.expires_at IS NULL OR up.expires_at > now())
ORDER BY up.plan_type ASC, up.activated_at DESC, up.id DESC`
	var rows []UserPlanDetail
	if err := r.db.WithContext(ctx).Raw(q, userID).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	if rows == nil {
		rows = []UserPlanDetail{}
	}
	return rows, nil
}

// ----------------------------------------------------------------------------
// QuotaManager
// ----------------------------------------------------------------------------

const insertReservationSQL = `INSERT INTO quota_reservations (
  id, user_id, request_id, billing_plan, status, settlement_status,
  reserved_requests, reserved_tokens, reserved_at, expires_at
) VALUES (
  ?, ?, ?, ?, 'reserved', 'held', ?, ?, ?, ?
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
// transaction. Idempotent per reservationID: a repeat call returns nil
// without re-enforcing windows or inserting duplicate rows.
//
// For billing_plan == "coding", Reserve enforces the active coding plan's
// plan limits before holding quota:
//   - hourly_limit  → 5-hour rolling requests window
//   - weekly_limit  → Monday 00:00 UTC → next Monday 00:00 UTC
//   - monthly_limit → active user_plan activated_at → expires_at (plan period)
//
// Consumption counts synchronous 'charge' rows PLUS confirmed 'reconcile'
// rows (deferred settlement with usageKnown=true), both carrying a negative
// request_delta; refund/unknown-reconcile/sweep reversals are excluded. A
// per-user pg_advisory_xact_lock(hashtext(user_id)) is taken inside the
// transaction so the read-check-insert is atomic per user. Active
// reserved/pending holds also count against the window. Exceeding any
// applicable window returns a *QuotaExceededError (scope
// hour5/weekly/period); a user with no active coding plan is rejected
// fail-closed (period, limit 0).
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

	// Serialize reserve per user so the window read-check-insert is atomic.
	// Transaction-scoped: released on commit/rollback.
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext(?))`, userID).Error; err != nil {
		return ErrQueryFailed
	}

	// Idempotency: a repeat reserve for the same reservation ID is a no-op
	// and must NOT be rejected by a window that was fine on the first call.
	var exists int
	if err := tx.Raw(`SELECT 1 FROM quota_reservations WHERE id = ? LIMIT 1`, reservationID).Scan(&exists).Error; err != nil {
		return ErrQueryFailed
	}
	if exists == 1 {
		return nil
	}

	// Ensure the user exists before inserting the reservation (FK).
	if err := tx.Exec(`INSERT INTO users (id, status) VALUES (?, 'active') ON CONFLICT (id) DO NOTHING`, userID).Error; err != nil {
		return ErrInsertFailed
	}

	if billingPlan == "coding" {
		if err := enforceCodingWindows(tx, userID, reservedReqs, now); err != nil {
			return err
		}
	}

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

// activeCodingPlanRow is the projection of the user's most recently
// activated active coding plan with its plan-level limits.
type activeCodingPlanRow struct {
	UserPlanID   int64      `gorm:"column:user_plan_id"`
	ActivatedAt  time.Time  `gorm:"column:activated_at"`
	ExpiresAt    *time.Time `gorm:"column:expires_at"`
	HourlyLimit  *int       `gorm:"column:hourly_limit"`
	WeeklyLimit  *int       `gorm:"column:weekly_limit"`
	MonthlyLimit *int       `gorm:"column:monthly_limit"`
}

const activeCodingPlanSQL = `SELECT up.id AS user_plan_id, up.activated_at, up.expires_at,
	p.hourly_limit, p.weekly_limit, p.monthly_limit
FROM user_plans up JOIN plans p ON p.id = up.plan_id
WHERE up.user_id = ? AND up.status = 'active'
  AND p.plan_type = 'coding' AND p.status = 'active'
  AND (up.expires_at IS NULL OR up.expires_at > ?)
ORDER BY up.activated_at DESC, up.id DESC
LIMIT 1`

// getActiveCodingPlan loads the user's most recent active coding plan with
// limits. A zero ActivatedAt means no active coding entitlement.
func positiveIntLimit(v *int) bool {
	return v != nil && *v > 0
}

func getActiveCodingPlan(tx *gorm.DB, userID string, now time.Time) (activeCodingPlanRow, error) {
	var row activeCodingPlanRow
	if err := tx.Raw(activeCodingPlanSQL, userID, now).Scan(&row).Error; err != nil {
		return activeCodingPlanRow{}, ErrQueryFailed
	}
	return row, nil
}

// consumedCodingSince returns the number of coding requests consumed since
// `since`: finalized synchronous 'charge' rows PLUS confirmed 'reconcile'
// rows (deferred settlement of a pending reservation with usageKnown=true).
// Both carry a NEGATIVE request_delta (consumption). Refund / unknown-reconcile
// / sweep rows carry a POSITIVE request_delta (reversal) and are excluded by
// the request_delta < 0 filter, so an under-charge release or a sweep refund
// is never counted as consumption. This keeps the hard quota windows honest
// after a deferred confirmed reconcile: a request that was pending then
// confirmed MUST still count against hour5/weekly/period.
func consumedCodingSince(tx *gorm.DB, userID string, since time.Time) (int, error) {
	const q = `SELECT COALESCE(-SUM(request_delta), 0) FROM usage_ledger
WHERE user_id = ? AND billing_plan = 'coding'
  AND ledger_type IN ('charge', 'reconcile')
  AND request_delta < 0
  AND created_at >= ?`
	var consumed int
	if err := tx.Raw(q, userID, since).Scan(&consumed).Error; err != nil {
		return 0, ErrQueryFailed
	}
	return consumed, nil
}

// activeHeldCodingSince returns the number of coding requests currently held
// in active 'reserved' reservations created since `since` for the user. These
// are in-flight holds not yet settled, so they must count against the window
// to prevent concurrent requests from punching through a hard limit. Only
// reserved_requests is counted (the request hold); reserved_tokens are not a
// coding-window unit. Pending_reconciliation rows also remain held until the
// reconciler resolves them, so they are included.
func activeHeldCodingSince(tx *gorm.DB, userID string, since time.Time) (int, error) {
	const q = `SELECT COALESCE(SUM(COALESCE(reserved_requests, 0)), 0)
FROM quota_reservations
WHERE user_id = ? AND billing_plan = 'coding'
  AND status IN ('reserved', 'pending_reconciliation')
  AND reserved_at >= ?`
	var held int
	if err := tx.Raw(q, userID, since).Scan(&held).Error; err != nil {
		return 0, ErrQueryFailed
	}
	return held, nil
}

// startOfWeekUTC returns the Monday 00:00 UTC that contains t.
func startOfWeekUTC(t time.Time) time.Time {
	t = t.UTC()
	daysSinceMonday := (int(t.Weekday()) - int(time.Monday) + 7) % 7
	return time.Date(t.Year(), t.Month(), t.Day()-daysSinceMonday, 0, 0, 0, 0, time.UTC)
}

// enforceCodingWindows checks the active coding plan's hourly/weekly/monthly
// limits against finalized 'charge' consumption plus the new reservedReqs
// (wanted). Returns *QuotaExceededError on breach, nil if within limits.
// No active coding plan → fail-closed period breach (limit 0).
//
// User-plan limit overrides (reset/bonus) are applied per scope: a 'reset'
// override moves the window's effective start forward to
// max(baseStart, latest active reset effective_from), forgiving earlier
// consumption; a 'bonus' override adds active bonus_requests to the base
// limit. Request timestamps are never modified — only the consumption read
// window and effective limit change.
func enforceCodingWindows(tx *gorm.DB, userID string, wanted int, now time.Time) error {
	row, err := getActiveCodingPlan(tx, userID, now)
	if err != nil {
		return err
	}
	if row.ActivatedAt.IsZero() {
		return &QuotaExceededError{Scope: ScopePeriod, Limit: 0, Consumed: 0, Wanted: wanted}
	}
	if positiveIntLimit(row.HourlyLimit) {
		res, err := computeWindow(tx, userID, row.UserPlanID, string(ScopeHour5), now.Add(-5*time.Hour), *row.HourlyLimit, now)
		if err != nil {
			return err
		}
		if res.Consumed+wanted > res.AdjustedLimit {
			return &QuotaExceededError{Scope: ScopeHour5, Limit: res.AdjustedLimit, Consumed: res.Consumed, Wanted: wanted}
		}
	}
	if positiveIntLimit(row.WeeklyLimit) {
		res, err := computeWindow(tx, userID, row.UserPlanID, string(ScopeWeekly), startOfWeekUTC(now), *row.WeeklyLimit, now)
		if err != nil {
			return err
		}
		if res.Consumed+wanted > res.AdjustedLimit {
			return &QuotaExceededError{Scope: ScopeWeekly, Limit: res.AdjustedLimit, Consumed: res.Consumed, Wanted: wanted}
		}
	}
	if positiveIntLimit(row.MonthlyLimit) {
		res, err := computeWindow(tx, userID, row.UserPlanID, string(ScopePeriod), row.ActivatedAt, *row.MonthlyLimit, now)
		if err != nil {
			return err
		}
		if res.Consumed+wanted > res.AdjustedLimit {
			return &QuotaExceededError{Scope: ScopePeriod, Limit: res.AdjustedLimit, Consumed: res.Consumed, Wanted: wanted}
		}
	}
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

// finalizePayloadHash returns a stable SHA-256 digest of the finalize
// payload so a repeat Finalize with the same counts/usageKnown is idempotent
// while a different payload is a stable conflict (ErrConflict → 409). It is
// not a secret — it only summarizes public settlement counts.
func finalizePayloadHash(finalReqs int, finalTokens int64, usageKnown bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%d|%t", finalReqs, finalTokens, usageKnown)
	return hex.EncodeToString(h.Sum(nil))
}

// Finalize settles a reserved reservation. Idempotent: a finalized reservation
// returns nil without re-charging when the payload matches; a different
// payload returns ErrConflict (stable 409). A released/expired/pending
// reservation returns ErrConflict; a missing one returns ErrNotFound.
// usageKnown=false should normally route through MarkPending; Finalize with
// usageKnown=false is rejected as ErrConflict to prevent guessing.
func (r *GormRepository) Finalize(ctx context.Context, reservationID string, finalReqs int, finalTokens int64, usageKnown bool) error {
	if finalReqs < 0 || finalTokens < 0 {
		return ErrConflict
	}
	if !usageKnown {
		// Unknown usage must never be guessed at a token count. The caller
		// must MarkPending so the reconciler can resolve it from evidence.
		return ErrConflict
	}
	now := time.Now().UTC()
	payloadHash := finalizePayloadHash(finalReqs, finalTokens, usageKnown)
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

	const sel = `SELECT status, user_id, request_id, billing_plan, idempotency_payload_hash
FROM quota_reservations WHERE id = ? LIMIT 1`
	var row struct {
		Status                 string `gorm:"column:status"`
		UserID                 string `gorm:"column:user_id"`
		RequestID              string `gorm:"column:request_id"`
		BillingPlan            string `gorm:"column:billing_plan"`
		IdempotencyPayloadHash string `gorm:"column:idempotency_payload_hash"`
	}
	if err := tx.Raw(sel, reservationID).Scan(&row).Error; err != nil {
		return ErrQueryFailed
	}
	if row.Status == "" {
		return ErrNotFound
	}
	switch row.Status {
	case "finalized":
		// Idempotent only when the payload matches; a different payload is a
		// stable conflict so a caller cannot retroactively change settled
		// usage.
		if row.IdempotencyPayloadHash != "" && row.IdempotencyPayloadHash != payloadHash {
			return ErrConflict
		}
		return nil
	case "reserved":
		// proceed
	default:
		// released / expired / pending_reconciliation — cannot finalize.
		return ErrConflict
	}

	res := tx.Exec(`UPDATE quota_reservations
SET status = 'finalized', settlement_status = 'settled',
    final_requests = ?, final_tokens = ?, usage_known = true,
    finalized_at = ?, idempotency_payload_hash = ?
WHERE id = ? AND status = 'reserved'`,
		finalReqs, finalTokens, now, payloadHash, reservationID)
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
SET status = 'released', settlement_status = 'released'
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
// SettlementManager (GetReservation / MarkPending / Reconcile / Expire)
// ----------------------------------------------------------------------------

// reservationStatusCols is the safe projection selected by GetReservation.
const reservationStatusCols = `id, user_id, request_id, billing_plan, status, settlement_status,
reserved_requests, reserved_tokens, final_requests, final_tokens, usage_known,
reserved_at, finalized_at, reconciled_at, expires_at`

// GetReservation returns the safe settlement projection for a reservation.
func (r *GormRepository) GetReservation(ctx context.Context, reservationID string) (ReservationStatus, error) {
	const q = `SELECT ` + reservationStatusCols + ` FROM quota_reservations WHERE id = ? LIMIT 1`
	var row ReservationStatus
	if err := r.db.WithContext(ctx).Raw(q, reservationID).Scan(&row).Error; err != nil {
		return ReservationStatus{}, ErrQueryFailed
	}
	if row.ID == "" {
		return ReservationStatus{}, ErrNotFound
	}
	return row, nil
}

// MarkPending transitions a reserved reservation to pending_reconciliation when
// the caller cannot produce confirmed usage. The held amount stays on the
// ledger; a reconciler later resolves it. Idempotent for pending; conflict for
// other terminals; ErrNotFound when missing.
func (r *GormRepository) MarkPending(ctx context.Context, reservationID string) error {
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

	var status string
	if err := tx.Raw(`SELECT status FROM quota_reservations WHERE id = ? LIMIT 1`, reservationID).Scan(&status).Error; err != nil {
		return ErrQueryFailed
	}
	if status == "" {
		return ErrNotFound
	}
	switch status {
	case "pending_reconciliation":
		return nil
	case "reserved":
		// proceed
	default:
		return ErrConflict
	}

	res := tx.Exec(`UPDATE quota_reservations
SET status = 'pending_reconciliation', settlement_status = 'pending', reconciled_at = ?
WHERE id = ? AND status = 'reserved'`, now, reservationID)
	if res.Error != nil {
		return ErrInsertFailed
	}
	if res.RowsAffected == 0 {
		return nil
	}
	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// Reconcile resolves a pending_reconciliation reservation using confirmed
// usage evidence. usageKnown=true → Finalize the actual counts (settled);
// usageKnown=false → Release the held amount (released; unknown usage never
// guessed). Idempotent per reservation for the resolved terminal; reserved →
// ErrConflict (must MarkPending first); missing → ErrNotFound.
func (r *GormRepository) Reconcile(ctx context.Context, reservationID string, finalReqs int, finalTokens int64, usageKnown bool) error {
	if finalReqs < 0 || finalTokens < 0 {
		return ErrConflict
	}
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
	case "finalized", "released", "expired":
		// Already resolved — idempotent success.
		return nil
	case "pending_reconciliation":
		// proceed
	default:
		// reserved — must MarkPending first so the lifecycle is explicit.
		return ErrConflict
	}

	if usageKnown {
		// Settle with confirmed counts. A 'reconcile' charge ledger row is
		// appended (distinct from the synchronous 'charge' path so audits can
		// tell deferred settlement apart). It carries a NEGATIVE request_delta
		// (consumption) so it counts against the coding hard quota windows — a
		// pending-then-confirmed request MUST still count, otherwise a
		// deferred settlement would punch through the limit. Only the actual
		// final counts are charged — never a guess.
		res := tx.Exec(`UPDATE quota_reservations
SET status = 'finalized', settlement_status = 'settled',
    final_requests = ?, final_tokens = ?, usage_known = true, finalized_at = ?, reconciled_at = ?
WHERE id = ? AND status = 'pending_reconciliation'`,
			finalReqs, finalTokens, now, now, reservationID)
		if res.Error != nil {
			return ErrInsertFailed
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := tx.Exec(insertLedgerSQL,
			row.UserID, row.RequestID, "reconcile", row.BillingPlan,
			-finalTokens, -finalReqs, "reconcile",
			reservationID+":reconcile", now,
		).Error; err != nil {
			return ErrInsertFailed
		}
	} else {
		// Unknown usage must never be guessed: release the held amount. The
		// reservation reaches 'released' with usage_known=false so the
		// under-charge is auditable, and a 'reconcile' refund row reverses
		// the hold.
		reservedReqs := 0
		if row.ReservedRequests != nil {
			reservedReqs = *row.ReservedRequests
		}
		reservedTokens := int64(0)
		if row.ReservedTokens != nil {
			reservedTokens = *row.ReservedTokens
		}
		res := tx.Exec(`UPDATE quota_reservations
SET status = 'released', settlement_status = 'released', usage_known = false, reconciled_at = ?
WHERE id = ? AND status = 'pending_reconciliation'`, now, reservationID)
		if res.Error != nil {
			return ErrInsertFailed
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := tx.Exec(insertLedgerSQL,
			row.UserID, row.RequestID, "reconcile", row.BillingPlan,
			reservedTokens, reservedReqs, "reconcile-unknown",
			reservationID+":reconcile-release", now,
		).Error; err != nil {
			return ErrInsertFailed
		}
	}
	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// Expire sweeps a reserved reservation whose expires_at is in the past. It
// marks the reservation 'expired' and appends a 'sweep' refund ledger row that
// reverses the held amount. Never deletes ledger rows. Idempotent for
// expired; non-due reserved → ErrConflict; other terminals → ErrConflict;
// missing → ErrNotFound.
func (r *GormRepository) Expire(ctx context.Context, reservationID string) error {
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

	const sel = `SELECT status, user_id, request_id, billing_plan, reserved_requests, reserved_tokens, expires_at
FROM quota_reservations WHERE id = ? LIMIT 1`
	var row struct {
		Status           string     `gorm:"column:status"`
		UserID           string     `gorm:"column:user_id"`
		RequestID        string     `gorm:"column:request_id"`
		BillingPlan      string     `gorm:"column:billing_plan"`
		ReservedRequests *int       `gorm:"column:reserved_requests"`
		ReservedTokens   *int64     `gorm:"column:reserved_tokens"`
		ExpiresAt        *time.Time `gorm:"column:expires_at"`
	}
	if err := tx.Raw(sel, reservationID).Scan(&row).Error; err != nil {
		return ErrQueryFailed
	}
	if row.Status == "" {
		return ErrNotFound
	}
	switch row.Status {
	case "expired":
		return nil
	case "reserved":
		// proceed — only reserved rows are expirable.
	default:
		return ErrConflict
	}
	if row.ExpiresAt == nil || !row.ExpiresAt.Before(now) {
		// Not due for expiry; the sweeper only acts on past expires_at.
		return ErrConflict
	}

	res := tx.Exec(`UPDATE quota_reservations
SET status = 'expired', settlement_status = 'expired'
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
		row.UserID, row.RequestID, "sweep", row.BillingPlan,
		reservedTokens, reservedReqs, "sweep",
		reservationID+":sweep", now,
	).Error; err != nil {
		return ErrInsertFailed
	}
	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// ListExpiredReservations returns the IDs of reserved reservations whose
// expires_at is in the past, limited to batch. It backs the sweeper loop. A nil
// expires_at means the reservation never expires and is excluded.
func (r *GormRepository) ListExpiredReservations(ctx context.Context, batch int) ([]string, error) {
	if batch <= 0 {
		batch = 100
	}
	const q = `SELECT id FROM quota_reservations
WHERE status = 'reserved' AND expires_at IS NOT NULL AND expires_at < now()
ORDER BY expires_at ASC LIMIT ?`
	var ids []string
	if err := r.db.WithContext(ctx).Raw(q, batch).Scan(&ids).Error; err != nil {
		return nil, ErrQueryFailed
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// ListPendingReservations returns the pending_reconciliation reservations
// older than minAge, limited to batch. It backs the reconciler loop so stale
// pending rows reach a terminal state without a request context. The richer
// PendingReservation projection carries request_id/user_id/billing_plan/
// reserved count so the reconciler can query usage evidence without a second
// GetReservation round-trip per row.
func (r *GormRepository) ListPendingReservations(ctx context.Context, minAge time.Duration, batch int) ([]PendingReservation, error) {
	if batch <= 0 {
		batch = 100
	}
	cutoff := time.Now().UTC().Add(-minAge)
	const q = `SELECT id, request_id, user_id, billing_plan,
COALESCE(reserved_requests, 0) AS reserved_requests, reserved_at
FROM quota_reservations
WHERE status = 'pending_reconciliation' AND reserved_at < ?
ORDER BY reserved_at ASC LIMIT ?`
	var rows []PendingReservation
	if err := r.db.WithContext(ctx).Raw(q, cutoff, batch).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	if rows == nil {
		rows = []PendingReservation{}
	}
	return rows, nil
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

// ----------------------------------------------------------------------------
// BalanceReader
// ----------------------------------------------------------------------------

// GetBalance computes the user's remaining coding-request and token quotas.
//
// Token balance: SUM(token_limit) over active token-type user_plans joined to
// plans, plus the net token_delta across the user's entire usage_ledger
// (reserve/charge carry negative deltas, refund carries the positive reversal;
// net = -total_consumed_tokens). Resulting remaining = token_limit + net_delta.
//
// Coding balance: the current active coding plan's monthly_limit interpreted
// as plan-period total, minus requests consumed since that user_plan's
// activated_at. This intentionally is NOT calendar-month based.
//
// Both results are clamped to >=0. A user with no plans and no ledger returns
// zeros; this is never ErrNotFound (the user-plan endpoint owns that case).
// Any query error is ErrQueryFailed, which never leaks SQL/DSN.
func (r *GormRepository) GetBalance(ctx context.Context, userID string) (Balance, error) {
	// Token: plan limits + net ledger delta.
	const tokenLimitQ = `SELECT COALESCE(SUM(p.token_limit), 0)
FROM user_plans up JOIN plans p ON p.id = up.plan_id
WHERE up.user_id = ? AND up.status = 'active' AND p.plan_type = 'token'`
	var tokenLimit int64
	if err := r.db.WithContext(ctx).Raw(tokenLimitQ, userID).Scan(&tokenLimit).Error; err != nil {
		return Balance{}, ErrQueryFailed
	}
	const tokenDeltaQ = `SELECT COALESCE(SUM(token_delta), 0) FROM usage_ledger WHERE user_id = ? AND billing_plan = 'token'`
	var tokenDelta int64
	if err := r.db.WithContext(ctx).Raw(tokenDeltaQ, userID).Scan(&tokenDelta).Error; err != nil {
		return Balance{}, ErrQueryFailed
	}
	tokenRemaining := tokenLimit + tokenDelta // tokenDelta <= 0 for consumption
	if tokenRemaining < 0 {
		tokenRemaining = 0
	}

	// Coding: plan-period total - consumed requests since the effective
	// (override-adjusted) activation start. Mirrors the period usage window.
	codingRemaining := int64(0)
	codingPlan, err := getActiveCodingPlan(r.db.WithContext(ctx), userID, time.Now().UTC())
	if err != nil {
		return Balance{}, ErrQueryFailed
	}
	if !codingPlan.ActivatedAt.IsZero() && positiveIntLimit(codingPlan.MonthlyLimit) {
		now := time.Now().UTC()
		res, err := computeWindow(r.db.WithContext(ctx), userID, codingPlan.UserPlanID, string(ScopePeriod), codingPlan.ActivatedAt, *codingPlan.MonthlyLimit, now)
		if err != nil {
			return Balance{}, ErrQueryFailed
		}
		codingRemaining = int64(res.AdjustedLimit - res.Consumed)
	}
	if codingRemaining < 0 {
		codingRemaining = 0
	}

	return Balance{
		CodingRemaining: codingRemaining,
		TokenRemaining:  tokenRemaining,
	}, nil
}

// ----------------------------------------------------------------------------
// UsageWindowsReader
// ----------------------------------------------------------------------------

// GetUsageWindows returns the hourly/weekly/monthly usage windows for the
// user's most recently activated active coding plan. Only windows whose plan
// limit is set are returned. A user with no active coding plan returns an
// empty slice (not an error). Consumption counts synchronous 'charge' rows
// plus confirmed 'reconcile' rows (deferred settlement), mirroring Reserve
// enforcement.
//
// User-plan limit overrides are reflected: the reported Limit is the adjusted
// limit (base + active bonus), WindowStart is the effective start (max of
// base start and latest active reset effective_from), Consumed is confirmed
// (charge + reconcile) requests since that effective start, and Remaining =
// max(0, adjusted limit - consumed).
func (r *GormRepository) GetUsageWindows(ctx context.Context, userID string) ([]UsageWindow, error) {
	now := time.Now().UTC()
	row, err := getActiveCodingPlan(r.db.WithContext(ctx), userID, now)
	if err != nil {
		return nil, ErrQueryFailed
	}
	if row.ActivatedAt.IsZero() {
		return []UsageWindow{}, nil
	}
	windows := make([]UsageWindow, 0, 3)
	if positiveIntLimit(row.HourlyLimit) {
		res, err := computeWindow(r.db.WithContext(ctx), userID, row.UserPlanID, string(ScopeHour5), now.Add(-5*time.Hour), *row.HourlyLimit, now)
		if err != nil {
			return nil, err
		}
		lim := res.AdjustedLimit
		rem := lim - res.Consumed
		if rem < 0 {
			rem = 0
		}
		windows = append(windows, UsageWindow{
			Scope: string(ScopeHour5), Limit: &lim, Consumed: res.Consumed,
			Remaining: rem, WindowStart: res.EffectiveStart,
		})
	}
	if positiveIntLimit(row.WeeklyLimit) {
		baseStart := startOfWeekUTC(now)
		res, err := computeWindow(r.db.WithContext(ctx), userID, row.UserPlanID, string(ScopeWeekly), baseStart, *row.WeeklyLimit, now)
		if err != nil {
			return nil, err
		}
		lim := res.AdjustedLimit
		rem := lim - res.Consumed
		if rem < 0 {
			rem = 0
		}
		we := baseStart.Add(7 * 24 * time.Hour)
		windows = append(windows, UsageWindow{
			Scope: string(ScopeWeekly), Limit: &lim, Consumed: res.Consumed,
			Remaining: rem, WindowStart: res.EffectiveStart, WindowEnd: &we,
		})
	}
	if positiveIntLimit(row.MonthlyLimit) {
		res, err := computeWindow(r.db.WithContext(ctx), userID, row.UserPlanID, string(ScopePeriod), row.ActivatedAt, *row.MonthlyLimit, now)
		if err != nil {
			return nil, err
		}
		lim := res.AdjustedLimit
		rem := lim - res.Consumed
		if rem < 0 {
			rem = 0
		}
		windows = append(windows, UsageWindow{
			Scope: string(ScopePeriod), Limit: &lim, Consumed: res.Consumed,
			Remaining: rem, WindowStart: res.EffectiveStart, WindowEnd: row.ExpiresAt,
		})
	}
	return windows, nil
}
