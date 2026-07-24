package panel_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tokenmp/v3/services/api/internal/billing"
	apiv1 "github.com/tokenmp/v3/services/api/internal/contract/apiv1"
	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/logging"
	"github.com/tokenmp/v3/services/api/internal/panel"
	"github.com/tokenmp/v3/services/api/internal/settings"
)

// stubBackend 是一个可编程的 httptest 后端，按 path 返回固定响应体。
type stubBackend struct {
	srv *httptest.Server
	// path -> (status, body)
	routes map[string]struct {
		status int
		body   string
	}
	hits map[string]int
}

func newStubBackend(routes map[string]struct {
	status int
	body   string
}) *stubBackend {
	b := &stubBackend{routes: routes, hits: map[string]int{}}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.hits[r.URL.Path]++
		// /v1/billing/plans 等固定 path；动态 user id 路径需匹配前缀。
		rt, ok := b.routes[r.URL.Path]
		if !ok {
			// 尝试按前缀匹配动态段。
			for path, v := range b.routes {
				if strings.HasSuffix(path, "/*") && strings.HasPrefix(r.URL.Path, strings.TrimSuffix(path, "/*")) {
					rt = v
					ok = true
					break
				}
			}
		}
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rt.status)
		_, _ = w.Write([]byte(rt.body))
	}))
	return b
}

func (b *stubBackend) close() { b.srv.Close() }

// newTestRouter 用 identity 的 noop verifier 装配 panel 路由，subject 取自 Bearer
// token（noop verifier 直接把 token 当 subject，role="user"）。
func newTestRouter(t *testing.T, loggingURL, billingURL string, st *settings.Store) (http.Handler, *panel.Handlers) {
	t.Helper()
	verifier, err := identity.NewVerifier("", "iss", "aud", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	h := panel.New(logging.NewClient(loggingURL), billing.NewClient(billingURL), st, nil)
	r := chi.NewRouter()
	r.Get("/api/v1/plans", h.ListPlans)
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(verifier, nil))
		r.Get("/api/v1/user/balance", h.GetUserBalance)
		r.Get("/api/v1/user/plans", h.ListUserPlans)
		r.Get("/api/v1/user/settings", h.GetUserSettings)
		r.Patch("/api/v1/user/settings", h.UpdateUserSettings)
		r.Get("/api/v1/request-logs", h.ListRequestLogs)
		r.Get("/api/v1/request-logs/stats", h.GetRequestLogStats)
		r.Get("/api/v1/request-logs/{requestId}", h.GetRequestLog)
	})
	return r, h
}

func doAuth(t *testing.T, h http.Handler, method, target, token string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, bytes.NewBufferString(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// ----- 套餐 -----

func TestListPlans_OK(t *testing.T) {
	body := `{"plans":[
		{"id":1,"name":"Pro","plan_type":"coding","price":9.9,"category":"monthly","monthly_limit":1000,"allowed_models":["gpt-4"],"status":"active"},
		{"id":2,"name":"Img","plan_type":"image","price":0,"category":"monthly","status":"active"}
	]}`
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{"/v1/billing/plans": {200, body}})
	defer b.close()
	h, _ := newTestRouter(t, "", b.srv.URL, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/plans", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Plans []apiv1.Plan `json:"plans"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if len(out.Plans) != 1 {
		t.Fatalf("expected 1 plan (image filtered), got %d", len(out.Plans))
	}
	p := out.Plans[0]
	if p.Name != "Pro" || p.PlanType != "coding" || p.TotalQuota != "1000" || p.DurationDays != 30 {
		t.Errorf("plan = %+v", p)
	}
}

func TestListPlans_BillingUnavailable_503(t *testing.T) {
	h, _ := newTestRouter(t, "", "", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/plans", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// ----- 余额 -----

func TestGetUserBalance_OK(t *testing.T) {
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{"/v1/billing/users/user-1/balance": {200, `{"coding_remaining":"42","token_remaining":"1000"}`}})
	defer b.close()
	h, _ := newTestRouter(t, "", b.srv.URL, nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/user/balance", "user-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var bal apiv1.UserBalance
	_ = json.NewDecoder(rec.Body).Decode(&bal)
	if bal.CodingRemaining != "42" || bal.TokenRemaining != "1000" {
		t.Errorf("balance = %+v", bal)
	}
}

func TestGetUserBalance_DegradedReturnsZeros(t *testing.T) {
	h, _ := newTestRouter(t, "", "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/user/balance", "user-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var bal apiv1.UserBalance
	_ = json.NewDecoder(rec.Body).Decode(&bal)
	if bal.CodingRemaining != "0" || bal.TokenRemaining != "0" {
		t.Errorf("degraded balance = %+v", bal)
	}
}

// ----- 用户套餐 -----

func TestListUserPlans_OK(t *testing.T) {
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{
		"/v1/billing/users/user-1/plan":    {200, `{"id":5,"user_id":"user-1","plan_id":1,"plan_type":"coding","status":"active","activated_at":"2026-01-01T00:00:00Z"}`},
		"/v1/billing/users/user-1/balance": {200, `{"coding_remaining":"7","token_remaining":"0"}`},
	})
	defer b.close()
	h, _ := newTestRouter(t, "", b.srv.URL, nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/user/plans", "user-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Plans []apiv1.UserPlan `json:"plans"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if len(out.Plans) != 1 || out.Plans[0].RemainingQuota != "7" {
		t.Errorf("plans = %+v", out)
	}
}

// ----- 请求日志 -----

func TestListRequestLogs_OK(t *testing.T) {
	body := `{"logs":[{"request_id":"r1","user_id":"user-1","model_name":"gpt-4","final_status":"success","created_at":"2026-01-01T00:00:00Z"}],"total":1,"page":1,"page_size":20}`
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{"/v1/logs": {200, body}})
	defer b.close()
	h, _ := newTestRouter(t, b.srv.URL, "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/request-logs?status=success&page=1&pageSize=20", "user-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Logs     []apiv1.RequestLog `json:"logs"`
		Total    int                `json:"total"`
		Page     int                `json:"page"`
		PageSize int                `json:"pageSize"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.Total != 1 || len(out.Logs) != 1 || out.Logs[0].Status != "success" {
		t.Errorf("out = %+v", out)
	}
}

func TestGetRequestLog_NotFound(t *testing.T) {
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{"/v1/logs/missing": {404, `{"error":"not_found"}`}})
	defer b.close()
	h, _ := newTestRouter(t, b.srv.URL, "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/request-logs/missing", "user-1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetRequestLog_OwnershipEnforced(t *testing.T) {
	// 日志属于 other-user；当前 user-1 不应看到。
	body := `{"log":{"request_id":"r1","user_id":"other-user","final_status":"success","created_at":"2026-01-01T00:00:00Z"},"attempts":[]}`
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{"/v1/logs/r1": {200, body}})
	defer b.close()
	h, _ := newTestRouter(t, b.srv.URL, "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/request-logs/r1", "user-1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (ownership)", rec.Code)
	}
}

func TestGetRequestLogStats_OK(t *testing.T) {
	body := `{"days":7,"total_requests":5,"total_input_tokens":100,"total_output_tokens":200,"by_model":[{"model":"gpt-4","requests":5,"input_tokens":100,"output_tokens":200}]}`
	b := newStubBackend(map[string]struct {
		status int
		body   string
	}{"/v1/logs/stats": {200, body}})
	defer b.close()
	h, _ := newTestRouter(t, b.srv.URL, "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/request-logs/stats?days=7", "user-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Days          int `json:"days"`
		TotalRequests int `json:"totalRequests"`
		ByModel       []struct {
			Model    string `json:"model"`
			Requests int    `json:"requests"`
		} `json:"byModel"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.Days != 7 || out.TotalRequests != 5 || len(out.ByModel) != 1 {
		t.Errorf("out = %+v", out)
	}
}

// ----- 用户设置 -----

func TestUserSettings_GetDefaults(t *testing.T) {
	h, _ := newTestRouter(t, "", "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/user/settings", "user-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var s apiv1.UserSettings
	_ = json.NewDecoder(rec.Body).Decode(&s)
	if string(s.PreferredBilling) != "coding" || s.FallbackEnabled != false {
		t.Errorf("defaults = %+v", s)
	}
}

func TestUserSettings_PatchPersists(t *testing.T) {
	st := settings.NewStore()
	h, _ := newTestRouter(t, "", "", st)
	// 局部更新：只设 fallbackEnabled=false，preferredBilling 不变。
	rec := doAuth(t, h, http.MethodPatch, "/api/v1/user/settings", "user-1", `{"fallbackEnabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := st.Get("user-1")
	if got.FallbackEnabled != false || got.PreferredBilling != "coding" {
		t.Errorf("persisted = %+v", got)
	}
	// 改 preferredBilling 为 token。
	rec = doAuth(t, h, http.MethodPatch, "/api/v1/user/settings", "user-1", `{"preferredBilling":"token"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	got = st.Get("user-1")
	if got.PreferredBilling != "token" {
		t.Errorf("persisted preferredBilling = %q", got.PreferredBilling)
	}
}

func TestUserSettings_PatchInvalidBilling_400(t *testing.T) {
	h, _ := newTestRouter(t, "", "", nil)
	rec := doAuth(t, h, http.MethodPatch, "/api/v1/user/settings", "user-1", `{"preferredBilling":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestUserSettings_Unauthenticated_401(t *testing.T) {
	h, _ := newTestRouter(t, "", "", nil)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/user/settings", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// keep imports used for future test extensions.
var _ = time.Now
