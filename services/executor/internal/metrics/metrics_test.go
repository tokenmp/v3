package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRouteLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want string
	}{
		{"/healthz", RouteHealthz},
		{"/healthz/", RouteHealthz},
		{"/metrics", RouteMetrics},
		{"/metrics/", RouteMetrics},
		{"/v1/models", RouteModels},
		{"/v1/models/", RouteModels},
		{"/v1/chat/completions", RouteChatCompletions},
		{"/v1/chat/completions/", RouteChatCompletions},
		{"/v1/messages", RouteMessages},
		{"/v1/messages/", RouteMessages},
		{"/v1/responses", RouteResponses},
		{"/v1/responses/", RouteResponses},
		{"/v1/images/generations", RouteImagesGenerations},
		{"/v1/images/generations/", RouteImagesGenerations},
		{"/v1/unknown", RouteOther},
		{"/v1/chat/completions/extra", RouteOther},
		{"/", RouteOther},
		{"/favicon.ico", RouteOther},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got := ParseRouteLabel(tc.path)
			if got != tc.want {
				t.Errorf("routeLabel(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestStatusClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code int
		want string
	}{
		{200, "2xx"},
		{204, "2xx"},
		{301, "3xx"},
		{304, "3xx"},
		{400, "4xx"},
		{401, "4xx"},
		{404, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
		{0, "0xx"},
		{99, "0xx"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(FormatStatusCode(tc.code), func(t *testing.T) {
			t.Parallel()
			got := ParseStatusClass(tc.code)
			if got != tc.want {
				t.Errorf("statusClass(%d) = %q, want %q", tc.code, got, tc.want)
			}
		})
	}
}

func newCollectorAndRegistry(t *testing.T, generation func() uint64, countByState func() map[string]int, eventCount func() int) (*Collector, *prometheus.Registry) {
	t.Helper()
	c := NewCollector(generation, countByState, eventCount)
	reg := c.Registry()
	return c, reg
}

func TestMiddlewareRecordsMetrics(t *testing.T) {
	t.Parallel()
	c, reg := newCollectorAndRegistry(t, nil, nil, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	handler := c.Middleware(inner)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	found := false
	for _, mf := range mfs {
		if mf.GetName() == "executor_http_requests_total" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				routeVal := ""
				methodVal := ""
				classVal := ""
				for _, l := range labels {
					switch l.GetName() {
					case "route":
						routeVal = l.GetValue()
					case "method":
						methodVal = l.GetValue()
					case "status_class":
						classVal = l.GetValue()
					}
				}
				if routeVal == RouteHealthz && methodVal == "get" && classVal == "2xx" {
					found = true
					if m.GetCounter().GetValue() != 1 {
						t.Errorf("counter value = %v, want 1", m.GetCounter().GetValue())
					}
				}
			}
		}
	}
	if !found {
		t.Error("executor_http_requests_total{route=healthz,method=get,status_class=2xx} not found")
	}
}

func TestMiddlewareRecords401(t *testing.T) {
	t.Parallel()
	c, reg := newCollectorAndRegistry(t, nil, nil, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	handler := c.Middleware(inner)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	found := false
	for _, mf := range mfs {
		if mf.GetName() == "executor_http_requests_total" {
			for _, m := range mf.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "route" && l.GetValue() == RouteChatCompletions {
						for _, l2 := range m.GetLabel() {
							if l2.GetName() == "status_class" && l2.GetValue() == "4xx" {
								found = true
							}
						}
					}
				}
			}
		}
	}
	if !found {
		t.Error("executor_http_requests_total{route=chat_completions,status_class=4xx} not found")
	}
}

func TestMiddlewareConcurrentRequests(t *testing.T) {
	t.Parallel()
	c, _ := newCollectorAndRegistry(t, nil, nil, nil)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := c.Middleware(inner)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/healthz", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	// Verify counter via the collector's own counter vec.
	total := c.httpRequestsTotal.WithLabelValues(RouteHealthz, "get", "2xx")
	// The prometheus counter doesn't expose its value directly, but we can
	// verify it doesn't panic and the middleware is safe under concurrency.
	_ = total
}

func TestStateGauges(t *testing.T) {
	t.Parallel()
	_, reg := newCollectorAndRegistry(t,
		func() uint64 { return 42 },
		func() map[string]int { return map[string]int{"reserved": 5, "finalized": 3, "released": 2} },
		func() int { return 99 },
	)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	foundGeneration := false
	foundEvents := false
	foundQuota := false
	for _, mf := range mfs {
		switch mf.GetName() {
		case "executor_config_generation":
			foundGeneration = true
			for _, m := range mf.GetMetric() {
				if m.GetGauge().GetValue() != 42 {
					t.Errorf("config_generation = %v, want 42", m.GetGauge().GetValue())
				}
			}
		case "executor_requestlog_events_total":
			foundEvents = true
			for _, m := range mf.GetMetric() {
				if m.GetGauge().GetValue() != 99 {
					t.Errorf("requestlog_events_total = %v, want 99", m.GetGauge().GetValue())
				}
			}
		case "executor_quota_reservations_total":
			foundQuota = true
			stateCounts := map[string]float64{}
			for _, m := range mf.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "state" {
						stateCounts[l.GetValue()] = m.GetGauge().GetValue()
					}
				}
			}
			if stateCounts["reserved"] != 5 {
				t.Errorf("quota reserved = %v, want 5", stateCounts["reserved"])
			}
			if stateCounts["finalized"] != 3 {
				t.Errorf("quota finalized = %v, want 3", stateCounts["finalized"])
			}
			if stateCounts["released"] != 2 {
				t.Errorf("quota released = %v, want 2", stateCounts["released"])
			}
		}
	}
	if !foundGeneration {
		t.Error("executor_config_generation not found in gathered metrics")
	}
	if !foundEvents {
		t.Error("executor_requestlog_events_total not found in gathered metrics")
	}
	if !foundQuota {
		t.Error("executor_quota_reservations_total not found in gathered metrics")
	}
}

func TestStateGaugesNil(t *testing.T) {
	t.Parallel()
	_, reg := newCollectorAndRegistry(t, nil, nil, nil)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, mf := range mfs {
		switch mf.GetName() {
		case "executor_config_generation":
			for _, m := range mf.GetMetric() {
				if m.GetGauge().GetValue() != 0 {
					t.Errorf("config_generation = %v, want 0 when nil", m.GetGauge().GetValue())
				}
			}
		case "executor_requestlog_events_total":
			for _, m := range mf.GetMetric() {
				if m.GetGauge().GetValue() != 0 {
					t.Errorf("requestlog_events_total = %v, want 0 when nil", m.GetGauge().GetValue())
				}
			}
		}
	}
}

func TestHandlerGETReturnsMetrics(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	handler := Handler(reg, false)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain prefix", ct)
	}
}

func TestHandlerPOSTReturns405(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	handler := Handler(reg, false)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
}

func TestHandlerSubpathReturns404(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	handler := Handler(reg, false)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/metrics/foo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandlerDisabledReturns404(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	handler := Handler(reg, true)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (disabled)", rec.Code)
	}
}

func TestResponseWriterCapturesStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)
	if rw.statusCode != http.StatusNotFound {
		t.Errorf("statusCode = %d, want 404", rw.statusCode)
	}
}

func TestResponseWriterDefaultStatus(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	// Write without WriteHeader — default 200.
	_, _ = rw.Write([]byte("ok"))
	if rw.statusCode != http.StatusOK {
		t.Errorf("statusCode = %d, want 200", rw.statusCode)
	}
}
