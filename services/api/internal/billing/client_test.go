package billing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
