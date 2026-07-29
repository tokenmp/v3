package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/billing/internal/repository"
)

type fakeAdminStore struct {
	createPlanErr     error
	createdPlan       *repository.Plan
	updateErr         error
	deleteErr         error
	listUserPlansErr  error
	userPlans         []repository.UserPlan
	userPlansTotal    int
	assignErr         error
	assignedUserPlan  *repository.UserPlan
	renewErr          error
	renewedUserPlan   repository.UserPlan
	renewID           int64
	renewExtendDays   int
	renewExpiresAt    *time.Time
	upgradeErr        error
	upgradedUserPlan  repository.UserPlan
	upgradeID         int64
	upgradePlanID     int64
	upgradeExpiresAt  *time.Time
	cancelErr         error
	usageStatsErr     error
	usageStatsRows    []repository.UsageStatRow
	createOverrideErr error
	createdOverride   *repository.UserPlanLimitOverride
	listOverrideErr   error
	listOverrides     []repository.UserPlanLimitOverride
	listOverrideID    int64
	revokeErr         error
	revokeID          int64
}

func (f *fakeAdminStore) CreatePlan(_ context.Context, p *repository.Plan) error {
	f.createdPlan = p
	return f.createPlanErr
}
func (f *fakeAdminStore) UpdatePlan(context.Context, int64, map[string]any) error { return f.updateErr }
func (f *fakeAdminStore) DeletePlan(context.Context, int64) error                 { return f.deleteErr }
func (f *fakeAdminStore) ListAllUserPlans(context.Context, int, int) ([]repository.UserPlan, int, error) {
	return f.userPlans, f.userPlansTotal, f.listUserPlansErr
}
func (f *fakeAdminStore) AssignUserPlan(_ context.Context, up *repository.UserPlan) error {
	f.assignedUserPlan = up
	return f.assignErr
}
func (f *fakeAdminStore) RenewUserPlan(_ context.Context, id int64, extendDays int, expiresAt *time.Time) (repository.UserPlan, error) {
	f.renewID = id
	f.renewExtendDays = extendDays
	f.renewExpiresAt = expiresAt
	return f.renewedUserPlan, f.renewErr
}
func (f *fakeAdminStore) UpgradeUserPlan(_ context.Context, id int64, newPlanID int64, expiresAt *time.Time) (repository.UserPlan, error) {
	f.upgradeID = id
	f.upgradePlanID = newPlanID
	f.upgradeExpiresAt = expiresAt
	return f.upgradedUserPlan, f.upgradeErr
}
func (f *fakeAdminStore) CancelUserPlan(context.Context, int64) error { return f.cancelErr }
func (f *fakeAdminStore) GetUsageStats(context.Context, int, string) ([]repository.UsageStatRow, error) {
	return f.usageStatsRows, f.usageStatsErr
}
func (f *fakeAdminStore) CreateLimitOverride(_ context.Context, o *repository.UserPlanLimitOverride) error {
	f.createdOverride = o
	return f.createOverrideErr
}
func (f *fakeAdminStore) ListLimitOverrides(_ context.Context, userPlanID int64) ([]repository.UserPlanLimitOverride, error) {
	f.listOverrideID = userPlanID
	return f.listOverrides, f.listOverrideErr
}
func (f *fakeAdminStore) RevokeLimitOverride(_ context.Context, id int64) error {
	f.revokeID = id
	return f.revokeErr
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type fakePlanReader struct {
	plan       repository.Plan
	getErr     error
	plans      []repository.Plan
	listErr    error
	listStatus string
}

func (f *fakePlanReader) GetPlan(context.Context, int64) (repository.Plan, error) {
	return f.plan, f.getErr
}
func (f *fakePlanReader) ListPlans(_ context.Context, status string) ([]repository.Plan, error) {
	f.listStatus = status
	return f.plans, f.listErr
}

type fakeUserPlanReader struct {
	plan    repository.UserPlan
	details []repository.UserPlanDetail
	err     error
}

func (f *fakeUserPlanReader) GetActiveUserPlan(context.Context, string) (repository.UserPlan, error) {
	return f.plan, f.err
}

func (f *fakeUserPlanReader) ListActiveUserPlans(context.Context, string) ([]repository.UserPlanDetail, error) {
	return f.details, f.err
}

type fakeQuotaManager struct {
	reserveErr, finalizeErr, releaseErr error
	reserveCalls                        int
	finalizeCalls                       int
	releaseCalls                        int
	reservationID                       string
}

func (f *fakeQuotaManager) Reserve(_ context.Context, reservationID, _, _, _ string, _ int, _ int64, _ *time.Time) error {
	f.reserveCalls++
	f.reservationID = reservationID
	return f.reserveErr
}
func (f *fakeQuotaManager) Finalize(_ context.Context, reservationID string, _ int, _ int64) error {
	f.finalizeCalls++
	f.reservationID = reservationID
	return f.finalizeErr
}
func (f *fakeQuotaManager) Release(_ context.Context, reservationID string) error {
	f.releaseCalls++
	f.reservationID = reservationID
	return f.releaseErr
}

type fakeLedgerReader struct {
	entries []repository.UsageLedgerEntry
	err     error
	limit   int
}

func (f *fakeLedgerReader) ListLedger(_ context.Context, _ string, limit int) ([]repository.UsageLedgerEntry, error) {
	f.limit = limit
	return f.entries, f.err
}

type fakeBalanceReader struct {
	balance repository.Balance
	err     error
	user    string
}

func (f *fakeBalanceReader) GetBalance(_ context.Context, userID string) (repository.Balance, error) {
	f.user = userID
	return f.balance, f.err
}

type fakeUsageWindowsReader struct {
	windows []repository.UsageWindow
	err     error
	user    string
}

func (f *fakeUsageWindowsReader) GetUsageWindows(_ context.Context, userID string) ([]repository.UsageWindow, error) {
	f.user = userID
	return f.windows, f.err
}

func newServer(plans *fakePlanReader, userPlans *fakeUserPlanReader, quota *fakeQuotaManager, ledger *fakeLedgerReader, pinger fakePinger) *Server {
	return New(plans, userPlans, quota, ledger, &fakeBalanceReader{}, nil, nil, pinger, nil)
}

func newServerWithBalance(plans *fakePlanReader, userPlans *fakeUserPlanReader, quota *fakeQuotaManager, ledger *fakeLedgerReader, balance *fakeBalanceReader, pinger fakePinger) *Server {
	return New(plans, userPlans, quota, ledger, balance, nil, nil, pinger, nil)
}

func newServerWithUsageWindows(plans *fakePlanReader, userPlans *fakeUserPlanReader, quota *fakeQuotaManager, ledger *fakeLedgerReader, balance *fakeBalanceReader, uw *fakeUsageWindowsReader, pinger fakePinger) *Server {
	return New(plans, userPlans, quota, ledger, balance, uw, nil, pinger, nil)
}

func newServerWithAdmin(pinger fakePinger, admin *fakeAdminStore) *Server {
	return New(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, &fakeBalanceReader{}, nil, admin, pinger, nil)
}

func do(t *testing.T, s *Server, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	return rec
}

type testEnvelope struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var env testEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, rec.Body.String())
	}
	if env.Code != 0 {
		t.Fatalf("envelope code = %d, want 0 (body=%s)", env.Code, rec.Body.String())
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return
	}
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode data: %v (data=%s)", err, string(env.Data))
	}
}

func containsLeak(body string) bool {
	for _, fragment := range []string{"password", "postgres://", "dsn", "pq:", "sql:", "tokenmp_billing"} {
		if contains(body, fragment) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHealthz(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	if rec := do(t, s, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadyz_OK(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	if rec := do(t, s, http.MethodGet, "/readyz", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadyz_NotReady(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{err: errors.New("postgres://user:password@db/tokenmp_billing")})
	rec := do(t, s, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("leaked detail: %s", rec.Body.String())
	}
}

func TestListPlans(t *testing.T) {
	plans := &fakePlanReader{plans: []repository.Plan{{ID: 1, Name: "Free", Status: "active"}}}
	s := newServer(plans, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/plans", "")
	if rec.Code != http.StatusOK || plans.listStatus != "active" {
		t.Fatalf("status = %d, filter = %q", rec.Code, plans.listStatus)
	}
	var out struct {
		Plans []repository.Plan `json:"plans"`
	}
	decode(t, rec, &out)
	if len(out.Plans) != 1 || out.Plans[0].ID != 1 {
		t.Errorf("plans = %+v", out.Plans)
	}
}

func TestGetPlan_OK(t *testing.T) {
	s := newServer(&fakePlanReader{plan: repository.Plan{ID: 7, Name: "Pro"}}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/plans/7", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out repository.Plan
	decode(t, rec, &out)
	if out.ID != 7 {
		t.Errorf("id = %d", out.ID)
	}
}

func TestGetPlan_NotFound(t *testing.T) {
	s := newServer(&fakePlanReader{getErr: repository.ErrNotFound}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/plans/7", "")
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not found") {
		t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
	}
}

func TestGetUserPlan_OK(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{plan: repository.UserPlan{ID: 1, UserID: "u1", PlanType: "pro"}}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/plan", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out repository.UserPlan
	decode(t, rec, &out)
	if out.UserID != "u1" {
		t.Errorf("user_id = %q", out.UserID)
	}
}

func TestGetUserPlan_NotFound(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{err: repository.ErrNotFound}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	if rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/plan", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestReserve_OK(t *testing.T) {
	quota := &fakeQuotaManager{}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/reserve", `{"reservation_id":"res-1","user_id":"u1","request_id":"req-1","billing_plan":"pro","reserved_requests":1,"reserved_tokens":42}`)
	if rec.Code != http.StatusOK || quota.reserveCalls != 1 {
		t.Fatalf("status/calls = %d/%d", rec.Code, quota.reserveCalls)
	}
	var out map[string]string
	decode(t, rec, &out)
	if out["reservation_id"] != "res-1" || out["status"] != "reserved" {
		t.Errorf("response = %#v", out)
	}
}

func TestReserve_ConflictIdempotent(t *testing.T) {
	quota := &fakeQuotaManager{reserveErr: repository.ErrConflict}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/reserve", `{"reservation_id":"res-1","user_id":"u1","request_id":"req-1","billing_plan":"pro","reserved_requests":1,"reserved_tokens":42}`)
	if rec.Code != http.StatusOK || quota.reserveCalls != 1 {
		t.Fatalf("status/calls = %d/%d", rec.Code, quota.reserveCalls)
	}
}

func TestReserve_MissingField(t *testing.T) {
	quota := &fakeQuotaManager{}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/reserve", `{"reservation_id":"res-1"}`)
	if rec.Code != http.StatusBadRequest || quota.reserveCalls != 0 {
		t.Fatalf("status/calls = %d/%d", rec.Code, quota.reserveCalls)
	}
}

func TestFinalize_OK(t *testing.T) {
	quota := &fakeQuotaManager{}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/finalize", `{"reservation_id":"res-1","final_requests":1,"final_tokens":42}`)
	if rec.Code != http.StatusOK || quota.finalizeCalls != 1 {
		t.Fatalf("status/calls = %d/%d", rec.Code, quota.finalizeCalls)
	}
}

func TestFinalize_NotFound(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{finalizeErr: repository.ErrNotFound}, &fakeLedgerReader{}, fakePinger{})
	if rec := do(t, s, http.MethodPost, "/v1/billing/quota/finalize", `{"reservation_id":"res-1","final_requests":1,"final_tokens":42}`); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestFinalize_Idempotent(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{finalizeErr: repository.ErrConflict}, &fakeLedgerReader{}, fakePinger{})
	if rec := do(t, s, http.MethodPost, "/v1/billing/quota/finalize", `{"reservation_id":"res-1","final_requests":1,"final_tokens":42}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRelease_OK(t *testing.T) {
	quota := &fakeQuotaManager{}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/release", `{"reservation_id":"res-1"}`)
	if rec.Code != http.StatusOK || quota.releaseCalls != 1 {
		t.Fatalf("status/calls = %d/%d", rec.Code, quota.releaseCalls)
	}
}

func TestRelease_NotFound(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{releaseErr: repository.ErrNotFound}, &fakeLedgerReader{}, fakePinger{})
	if rec := do(t, s, http.MethodPost, "/v1/billing/quota/release", `{"reservation_id":"res-1"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestListLedger(t *testing.T) {
	ledger := &fakeLedgerReader{entries: []repository.UsageLedgerEntry{{ID: 1, UserID: "u1", LedgerType: "charge"}}}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, ledger, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/ledger", "")
	if rec.Code != http.StatusOK || ledger.limit != 50 {
		t.Fatalf("status/limit = %d/%d", rec.Code, ledger.limit)
	}
	var out struct {
		Entries []repository.UsageLedgerEntry `json:"entries"`
	}
	decode(t, rec, &out)
	if len(out.Entries) != 1 || out.Entries[0].LedgerType != "charge" {
		t.Errorf("entries = %+v", out.Entries)
	}
}

func TestGetBalance_OK(t *testing.T) {
	bal := &fakeBalanceReader{balance: repository.Balance{CodingRemaining: 42, TokenRemaining: 1000}}
	s := newServerWithBalance(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, bal, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/balance", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var out balanceResponse
	decode(t, rec, &out)
	if out.CodingRemaining != "42" || out.TokenRemaining != "1000" {
		t.Errorf("balance = %+v", out)
	}
	if bal.user != "u1" {
		t.Errorf("user = %q", bal.user)
	}
}

func TestGetBalance_QueryError(t *testing.T) {
	bal := &fakeBalanceReader{err: repository.ErrQueryFailed}
	s := newServerWithBalance(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, bal, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/balance", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked: %s", rec.Body.String())
	}
}

func TestReserve_QuotaExceeded(t *testing.T) {
	quota := &fakeQuotaManager{reserveErr: &repository.QuotaExceededError{Scope: repository.ScopeHour5, Limit: 2, Consumed: 2, Wanted: 1}}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/reserve", `{"reservation_id":"res-1","user_id":"u1","request_id":"req-1","billing_plan":"coding","reserved_requests":1,"reserved_tokens":42}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if !contains(rec.Body.String(), "quota_exceeded") || !contains(rec.Body.String(), "hour5") {
		t.Fatalf("body missing scope: %s", rec.Body.String())
	}
	if containsLeak(rec.Body.String()) {
		t.Errorf("body leaked: %s", rec.Body.String())
	}
}

func TestReserve_QuotaExceededWrapped(t *testing.T) {
	// A wrapped QuotaExceededError must still be recognized (errors.As walks
	// the chain) so callers that wrap repo errors do not regress to 500.
	quota := &fakeQuotaManager{reserveErr: fmt.Errorf("%w", &repository.QuotaExceededError{Scope: repository.ScopeWeekly})}
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, quota, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/quota/reserve", `{"reservation_id":"res-1","user_id":"u1","request_id":"req-1","billing_plan":"coding","reserved_requests":1,"reserved_tokens":42}`)
	if rec.Code != http.StatusTooManyRequests || !contains(rec.Body.String(), "weekly") {
		t.Fatalf("status/body = %d/%s", rec.Code, rec.Body.String())
	}
}

func TestGetUsageWindows_OK(t *testing.T) {
	lim := 100
	uw := &fakeUsageWindowsReader{windows: []repository.UsageWindow{{Scope: "hour5", Limit: &lim, Consumed: 3, Remaining: 97}}}
	s := newServerWithUsageWindows(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, &fakeBalanceReader{}, uw, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/usage-windows", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if uw.user != "u1" {
		t.Errorf("user = %q", uw.user)
	}
	var out struct {
		Windows []repository.UsageWindow `json:"windows"`
	}
	decode(t, rec, &out)
	if len(out.Windows) != 1 || out.Windows[0].Scope != "hour5" || out.Windows[0].Remaining != 97 {
		t.Errorf("windows = %+v", out.Windows)
	}
}

func TestGetUsageWindows_NilReturnsEmpty(t *testing.T) {
	s := newServerWithBalance(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, &fakeBalanceReader{}, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/usage-windows", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestGetUsageWindows_QueryError(t *testing.T) {
	uw := &fakeUsageWindowsReader{err: repository.ErrQueryFailed}
	s := newServerWithUsageWindows(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, &fakeBalanceReader{}, uw, fakePinger{})
	rec := do(t, s, http.MethodGet, "/v1/billing/users/u1/usage-windows", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// ---- limit override admin endpoint tests ----

func TestAdminCreateLimitOverride_OK(t *testing.T) {
	admin := &fakeAdminStore{}
	s := newServerWithAdmin(fakePinger{}, admin)
	body := `{"kind":"bonus","scope":"hour5","bonus_requests":5,"reason":"grant","created_by":"admin1"}`
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/42/limit-overrides", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	if admin.createdOverride == nil || admin.createdOverride.UserPlanID != 42 || admin.createdOverride.Kind != "bonus" || admin.createdOverride.Scope != "hour5" {
		t.Fatalf("created override = %+v", admin.createdOverride)
	}
	if admin.createdOverride.BonusRequests == nil || *admin.createdOverride.BonusRequests != 5 {
		t.Fatalf("bonus_requests = %v", admin.createdOverride.BonusRequests)
	}
	if admin.createdOverride.EffectiveFrom.IsZero() {
		t.Fatalf("effective_from default not set")
	}
	var out repository.UserPlanLimitOverride
	decode(t, rec, &out)
	if out.UserPlanID != 42 || out.Kind != "bonus" {
		t.Errorf("response = %+v", out)
	}
}

func TestAdminCreateLimitOverride_InvalidKind(t *testing.T) {
	admin := &fakeAdminStore{}
	s := newServerWithAdmin(fakePinger{}, admin)
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/42/limit-overrides", `{"kind":"nope","scope":"hour5"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if admin.createdOverride != nil {
		t.Fatalf("store should not be called")
	}
}

func TestAdminCreateLimitOverride_BonusMissingRequests(t *testing.T) {
	admin := &fakeAdminStore{}
	s := newServerWithAdmin(fakePinger{}, admin)
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/42/limit-overrides", `{"kind":"bonus","scope":"hour5"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminCreateLimitOverride_ResetOK(t *testing.T) {
	admin := &fakeAdminStore{}
	s := newServerWithAdmin(fakePinger{}, admin)
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/1/limit-overrides", `{"kind":"reset","scope":"period"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if admin.createdOverride.Kind != "reset" || admin.createdOverride.Scope != "period" || admin.createdOverride.BonusRequests != nil {
		t.Fatalf("override = %+v", admin.createdOverride)
	}
}

func TestAdminCreateLimitOverride_NotFound(t *testing.T) {
	admin := &fakeAdminStore{createOverrideErr: repository.ErrNotFound}
	s := newServerWithAdmin(fakePinger{}, admin)
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/99/limit-overrides", `{"kind":"reset","scope":"weekly"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAdminCreateLimitOverride_InvalidID(t *testing.T) {
	s := newServerWithAdmin(fakePinger{}, &fakeAdminStore{})
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/abc/limit-overrides", `{"kind":"reset","scope":"weekly"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestAdminListLimitOverrides_OK(t *testing.T) {
	admin := &fakeAdminStore{listOverrides: []repository.UserPlanLimitOverride{{ID: 1, Kind: "bonus", Scope: "hour5"}}}
	s := newServerWithAdmin(fakePinger{}, admin)
	rec := do(t, s, http.MethodGet, "/v1/billing/admin/user-plans/7/limit-overrides", "")
	if rec.Code != http.StatusOK || admin.listOverrideID != 7 {
		t.Fatalf("status/id = %d/%d", rec.Code, admin.listOverrideID)
	}
	var out struct {
		Overrides []repository.UserPlanLimitOverride `json:"overrides"`
	}
	decode(t, rec, &out)
	if len(out.Overrides) != 1 || out.Overrides[0].ID != 1 {
		t.Errorf("overrides = %+v", out.Overrides)
	}
}

func TestAdminRevokeLimitOverride_OK(t *testing.T) {
	admin := &fakeAdminStore{}
	s := newServerWithAdmin(fakePinger{}, admin)
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/limit-overrides/5/revoke", "")
	if rec.Code != http.StatusOK || admin.revokeID != 5 {
		t.Fatalf("status/id = %d/%d", rec.Code, admin.revokeID)
	}
	var out map[string]any
	decode(t, rec, &out)
	if out["status"] != "revoked" {
		t.Errorf("status = %v", out["status"])
	}
}

func TestAdminRevokeLimitOverride_NotFound(t *testing.T) {
	s := newServerWithAdmin(fakePinger{}, &fakeAdminStore{revokeErr: repository.ErrNotFound})
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/limit-overrides/5/revoke", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAdminLimitOverride_NoAdminConfigured(t *testing.T) {
	s := newServer(&fakePlanReader{}, &fakeUserPlanReader{}, &fakeQuotaManager{}, &fakeLedgerReader{}, fakePinger{})
	rec := do(t, s, http.MethodPost, "/v1/billing/admin/user-plans/1/limit-overrides", `{"kind":"reset","scope":"weekly"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
