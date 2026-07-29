package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

// activeUserPlanIDForUser returns the id of the user's most recently
// activated active coding user_plan (used by override tests to obtain the
// user_plan_id FK).
func activeUserPlanIDForUser(t *testing.T, db *gorm.DB, userID string) int64 {
	t.Helper()
	var id int64
	if err := db.Raw(`SELECT up.id FROM user_plans up JOIN plans p ON p.id = up.plan_id
WHERE up.user_id = ? AND up.status = 'active' AND p.plan_type = 'coding'
ORDER BY up.activated_at DESC, up.id DESC LIMIT 1`, userID).Scan(&id).Error; err != nil {
		t.Fatalf("lookup active user_plan id: %v", err)
	}
	if id == 0 {
		t.Fatalf("no active coding user_plan for %s", userID)
	}
	return id
}

func TestLimitOverride_BonusRaisesEnforcementAndWindow(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "ob")
	lim := 1
	big := 1000000
	planID := insertCodingPlan(t, db, "obplan", &lim, &big, &big)
	insertUserPlanActivated(t, db, "ob", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()
	upID := activeUserPlanIDForUser(t, db, "ob")

	// Consume the 1 hour5 request.
	consume(t, r, ctx, "ob", "a")
	// Without override, a second reserve must be rejected (hour5).
	err := r.Reserve(ctx, "res-ob-bad", "ob", "req-ob-bad", "coding", 1, 1, nil)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}
	if s := quotaExceededScope(err); s != string(ScopeHour5) {
		t.Fatalf("scope = %q, want hour5", s)
	}

	// Grant a +1 bonus on hour5.
	bonus := 1
	if err := r.CreateLimitOverride(ctx, &UserPlanLimitOverride{
		UserPlanID: upID, Kind: "bonus", Scope: string(ScopeHour5), BonusRequests: &bonus, Reason: "support grant",
	}); err != nil {
		t.Fatalf("CreateLimitOverride bonus: %v", err)
	}
	// Now a second reserve within the bonus-adjusted limit succeeds.
	if err := r.Reserve(ctx, "res-ob-ok", "ob", "req-ob-ok", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve after bonus: %v", err)
	}

	windows, err := r.GetUsageWindows(ctx, "ob")
	if err != nil {
		t.Fatalf("GetUsageWindows: %v", err)
	}
	h5 := windowByScope(windows, string(ScopeHour5))
	if h5 == nil {
		t.Fatalf("no hour5 window")
	}
	if h5.Limit == nil || *h5.Limit != 2 {
		t.Fatalf("hour5 adjusted limit = %v, want 2", h5.Limit)
	}
	if h5.Consumed != 1 {
		t.Fatalf("hour5 consumed = %d, want 1", h5.Consumed)
	}
	if h5.Remaining != 1 {
		t.Fatalf("hour5 remaining = %d, want 1", h5.Remaining)
	}
}

func TestLimitOverride_ResetMovesWindowStart(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "ors")
	lim := 1
	planID := insertCodingPlan(t, db, "orsplan", &lim, nil, nil)
	insertUserPlanActivated(t, db, "ors", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()
	upID := activeUserPlanIDForUser(t, db, "ors")

	// Consume the only hour5 slot.
	consume(t, r, ctx, "ors", "a")
	// Further reserve rejected.
	if err := r.Reserve(ctx, "res-ors-bad", "ors", "req-ors-bad", "coding", 1, 1, nil); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded, got %v", err)
	}

	// A reset override with effective_from = now moves the window start so
	// the prior consumption (just before now) falls outside the window.
	if err := r.CreateLimitOverride(ctx, &UserPlanLimitOverride{
		UserPlanID: upID, Kind: "reset", Scope: string(ScopeHour5),
	}); err != nil {
		t.Fatalf("CreateLimitOverride reset: %v", err)
	}
	// After reset, the consumed request (created ~now, before effective_from
	// which defaults to now) is excluded; a new reserve should succeed.
	if err := r.Reserve(ctx, "res-ors-ok", "ors", "req-ors-ok", "coding", 1, 1, nil); err != nil {
		t.Fatalf("Reserve after reset: %v", err)
	}

	windows, err := r.GetUsageWindows(ctx, "ors")
	if err != nil {
		t.Fatalf("GetUsageWindows: %v", err)
	}
	h5 := windowByScope(windows, string(ScopeHour5))
	if h5 == nil {
		t.Fatalf("no hour5 window")
	}
	if h5.Consumed != 0 {
		t.Fatalf("hour5 consumed after reset = %d, want 0 (prior consumption excluded)", h5.Consumed)
	}
}

func TestLimitOverride_RevokeBonus(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "orb")
	lim := 1
	planID := insertCodingPlan(t, db, "orbplan", &lim, &lim, &lim)
	insertUserPlanActivated(t, db, "orb", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()
	upID := activeUserPlanIDForUser(t, db, "orb")

	bonus := 5
	o := &UserPlanLimitOverride{UserPlanID: upID, Kind: "bonus", Scope: string(ScopeHour5), BonusRequests: &bonus}
	if err := r.CreateLimitOverride(ctx, o); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if o.ID == 0 {
		t.Fatalf("override id not backfilled")
	}
	// List returns the one override.
	rows, err := r.ListLimitOverrides(ctx, upID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != o.ID {
		t.Fatalf("list rows = %+v", rows)
	}
	// Revoke it.
	if err := r.RevokeLimitOverride(ctx, o.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Revoke again is idempotent.
	if err := r.RevokeLimitOverride(ctx, o.ID); err != nil {
		t.Fatalf("Revoke idempotent: %v", err)
	}
	// The bonus no longer applies: window limit back to base.
	windows, err := r.GetUsageWindows(ctx, "orb")
	if err != nil {
		t.Fatalf("GetUsageWindows: %v", err)
	}
	h5 := windowByScope(windows, string(ScopeHour5))
	if h5 == nil || h5.Limit == nil || *h5.Limit != 1 {
		t.Fatalf("hour5 limit after revoke = %v, want 1", h5.Limit)
	}
	// Revoking a non-existent override returns ErrNotFound.
	if err := r.RevokeLimitOverride(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoke unknown: expected ErrNotFound, got %v", err)
	}
}

func TestLimitOverride_BonusExpiredDoesNotApply(t *testing.T) {
	d := dsn(t)
	applyMigrations(t, d)
	db := openDB(t, d)
	insertUser(t, db, "obe")
	lim := 1
	planID := insertCodingPlan(t, db, "obeplan", &lim, &lim, &lim)
	insertUserPlanActivated(t, db, "obe", planID, "active", time.Now().UTC())
	r := New(db)
	ctx := context.Background()
	upID := activeUserPlanIDForUser(t, db, "obe")

	// A bonus whose effective_until is already in the past must not apply.
	past := time.Now().Add(-time.Second).UTC()
	bonus := 5
	if err := r.CreateLimitOverride(ctx, &UserPlanLimitOverride{
		UserPlanID: upID, Kind: "bonus", Scope: string(ScopeHour5),
		BonusRequests: &bonus, EffectiveUntil: &past,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	windows, err := r.GetUsageWindows(ctx, "obe")
	if err != nil {
		t.Fatalf("GetUsageWindows: %v", err)
	}
	h5 := windowByScope(windows, string(ScopeHour5))
	if h5 == nil || h5.Limit == nil || *h5.Limit != 1 {
		t.Fatalf("hour5 limit with expired bonus = %v, want 1", h5.Limit)
	}
}

func windowByScope(ws []UsageWindow, scope string) *UsageWindow {
	for i := range ws {
		if ws[i].Scope == scope {
			return &ws[i]
		}
	}
	return nil
}
