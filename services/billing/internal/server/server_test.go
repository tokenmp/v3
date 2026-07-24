package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/billing/internal/repository"
)

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
	plan repository.UserPlan
	err  error
}

func (f *fakeUserPlanReader) GetActiveUserPlan(context.Context, string) (repository.UserPlan, error) {
	return f.plan, f.err
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

func newServer(plans *fakePlanReader, userPlans *fakeUserPlanReader, quota *fakeQuotaManager, ledger *fakeLedgerReader, pinger fakePinger) *Server {
	return New(plans, userPlans, quota, ledger, pinger, nil)
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

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(dst); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
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
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), "not_found") {
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
