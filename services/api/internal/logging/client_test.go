package logging

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
	if _, err := c.ListLogs(context.Background(), ListFilter{UserID: "u"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("ListLogs err = %v, want ErrUnavailable", err)
	}
	if _, err := c.GetLog(context.Background(), "r"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetLog err = %v, want ErrUnavailable", err)
	}
	if _, err := c.GetStats(context.Background(), "u", 7); !errors.Is(err, ErrUnavailable) {
		t.Errorf("GetStats err = %v", err)
	}
}

func TestListLogs_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("user_id") != "u1" || q.Get("page") != "2" || q.Get("page_size") != "5" {
			t.Errorf("query = %v", q)
		}
		_ = json.NewEncoder(w).Encode(ListResult{
			Logs:  []RequestLog{{RequestID: "r1", UserID: "u1"}},
			Total: 7, Page: 2, PageSize: 5,
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.ListLogs(context.Background(), ListFilter{UserID: "u1", Page: 2, PageSize: 5})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.Total != 7 || len(out.Logs) != 1 || out.Logs[0].RequestID != "r1" {
		t.Errorf("out = %+v", out)
	}
}

func TestGetLog_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.GetLog(context.Background(), "missing"); !errors.Is(err, NotFound) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestGetLog_5xxUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.GetLog(context.Background(), "r"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestGetStats_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/logs/stats" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Stats{
			Days: 7, TotalRequests: 3,
			ByModel: []ModelStat{{Model: "gpt-4", Requests: 3, InputTokens: 10, OutputTokens: 20}},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.GetStats(context.Background(), "u1", 7)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.Days != 7 || out.TotalRequests != 3 || len(out.ByModel) != 1 {
		t.Errorf("out = %+v", out)
	}
}

func TestGetStats_StartEndDateSerialized(t *testing.T) {
	var gotStart, gotEnd string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStart = r.URL.Query().Get("start_date")
		gotEnd = r.URL.Query().Get("end_date")
		_ = json.NewEncoder(w).Encode(Stats{})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := c.ListLogs(context.Background(), ListFilter{StartTime: start, EndTime: end}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if gotStart != start.Format(time.RFC3339) || gotEnd != end.Format(time.RFC3339) {
		t.Errorf("dates = %q / %q", gotStart, gotEnd)
	}
}
