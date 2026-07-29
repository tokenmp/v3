package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Unavailable(t *testing.T) {
	c := NewClient("")
	if c.Available() {
		t.Fatal("empty client should be unavailable")
	}
	if _, err := c.ListPlans(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ListPlans err = %v", err)
	}
	if _, err := c.ListUserPlans(context.Background(), "u"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ListUserPlans err = %v", err)
	}
	if _, err := c.GetBalance(context.Background(), "u"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetBalance err = %v", err)
	}
}

func TestListPlans_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/plans" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Plans []Plan `json:"plans"`
		}{Plans: []Plan{{ID: 1, Name: "Pro", PlanType: "coding", Price: 9.9, Status: "active"}}})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.ListPlans(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out) != 1 || out[0].ID != 1 || out[0].Price != 9.9 {
		t.Errorf("out = %+v", out)
	}
}

func TestListUserPlans_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/users/u1/plans" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Plans []UserPlan `json:"plans"`
		}{Plans: []UserPlan{{ID: 5, UserID: "u1", PlanID: 1, PlanName: "Pro", PlanType: "coding", Status: "active"}}})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.ListUserPlans(context.Background(), "u1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out) != 1 || out[0].ID != 5 || out[0].PlanName != "Pro" {
		t.Errorf("out = %+v", out)
	}
}

func TestListUserPlans_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.ListUserPlans(context.Background(), "u"); !errors.Is(err, NotFound) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestGetBalance_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/users/u1/balance" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Balance{CodingRemaining: "42", TokenRemaining: "1000"})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.GetBalance(context.Background(), "u1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.CodingRemaining != "42" || out.TokenRemaining != "1000" {
		t.Errorf("out = %+v", out)
	}
}

func TestGetUsageWindows_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/users/u1/usage-windows" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Windows []UsageWindow `json:"windows"`
		}{Windows: []UsageWindow{
			{Scope: "hour5", Limit: intPtr(5), Consumed: 2, Remaining: 3, WindowStart: timeParse(t, "2026-07-29T12:00:00Z")},
			{Scope: "weekly", Limit: intPtr(50), Consumed: 2, Remaining: 48, WindowStart: timeParse(t, "2026-07-28T00:00:00Z"), WindowEnd: timePtr(timeParse(t, "2026-08-04T00:00:00Z"))},
			{Scope: "period", Consumed: 2, Remaining: 498, WindowStart: timeParse(t, "2026-07-01T00:00:00Z")},
		}})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.GetUsageWindows(context.Background(), "u1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if out[0].Scope != "hour5" || out[0].Remaining != 3 || out[0].Limit == nil || *out[0].Limit != 5 {
		t.Errorf("out[0] = %+v", out[0])
	}
	if out[2].Scope != "period" || out[2].Limit != nil || out[2].WindowEnd != nil {
		t.Errorf("out[2] = %+v", out[2])
	}
}

func TestGetUsageWindows_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(struct {
			Windows []UsageWindow `json:"windows"`
		}{})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.GetUsageWindows(context.Background(), "u1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out == nil || len(out) != 0 {
		t.Fatalf("out = %+v, want non-nil empty", out)
	}
}

func TestGetUsageWindows_Unavailable(t *testing.T) {
	c := NewClient("")
	if _, err := c.GetUsageWindows(context.Background(), "u1"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestGetUsageWindows_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.GetUsageWindows(context.Background(), "u1"); !errors.Is(err, NotFound) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestListAllUserPlans_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/admin/user-plans" {
			t.Errorf("path = %q", r.URL.Path)
		}
		// Billing returns envelope: {code:0, data: {userPlans:[...], total, page, pageSize}}
		_ = json.NewEncoder(w).Encode(struct {
			Code int `json:"code"`
			Data struct {
				UserPlans []UserPlan `json:"userPlans"`
				Total     int        `json:"total"`
				Page      int        `json:"page"`
				PageSize  int        `json:"pageSize"`
			} `json:"data"`
		}{
			Code: 0,
			Data: struct {
				UserPlans []UserPlan `json:"userPlans"`
				Total     int        `json:"total"`
				Page      int        `json:"page"`
				PageSize  int        `json:"pageSize"`
			}{
				UserPlans: []UserPlan{
					{ID: 10, UserID: "u1", PlanID: 1, PlanType: "coding", Status: "active"},
					{ID: 11, UserID: "u2", PlanID: 2, PlanType: "image", Status: "active"},
				},
				Total:    2,
				Page:     1,
				PageSize: 20,
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.ListAllUserPlans(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].ID != 10 || out[0].UserID != "u1" || out[0].PlanType != "coding" {
		t.Errorf("out[0] = %+v", out[0])
	}
	if out[1].ID != 11 || out[1].UserID != "u2" || out[1].PlanType != "image" {
		t.Errorf("out[1] = %+v", out[1])
	}
}

func TestListAllUserPlans_EmptyEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Billing returns envelope with empty userPlans array
		_ = json.NewEncoder(w).Encode(struct {
			Code int `json:"code"`
			Data struct {
				UserPlans []UserPlan `json:"userPlans"`
				Total     int        `json:"total"`
				Page      int        `json:"page"`
				PageSize  int        `json:"pageSize"`
			} `json:"data"`
		}{Code: 0})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.ListAllUserPlans(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out == nil {
		t.Fatal("out is nil, want empty slice")
	}
	if len(out) != 0 {
		t.Errorf("len = %d, want 0", len(out))
	}
}

func TestListAllUserPlans_Unavailable(t *testing.T) {
	c := NewClient("")
	if _, err := c.ListAllUserPlans(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestListAllUserPlans_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.ListAllUserPlans(context.Background()); !errors.Is(err, NotFound) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func intPtr(n int) *int { return &n }

func timePtr(t time.Time) *time.Time { return &t }

func timeParse(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("timeParse %q: %v", s, err)
	}
	return v
}
