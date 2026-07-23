// Package metrics provides Prometheus HTTP observability for the Executor
// service. It exposes a counter vec and histogram vec for HTTP request
// throughput and latency, plus state gauges for in-memory runtime state.
// The metrics middleware wraps AuthMiddleware so that 401 responses are also
// counted. The /metrics endpoint is anonymous (same policy as /healthz) and
// is not part of the OpenAPI contract.
package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace = "executor"

	labelRoute       = "route"
	labelMethod      = "method"
	labelStatusClass = "status_class"
	labelState       = "state"
)

// Stable route labels — low cardinality, no user/model/credential/subject.
const (
	RouteHealthz           = "healthz"
	RouteMetrics           = "metrics"
	RouteModels            = "models"
	RouteChatCompletions   = "chat_completions"
	RouteMessages          = "messages"
	RouteResponses         = "responses"
	RouteImagesGenerations = "images_generations"
	RouteOther             = "other"
)

// Collector holds all Executor Prometheus metrics and their state callbacks.
type Collector struct {
	mu             sync.RWMutex
	generationFn   func() uint64
	countByStateFn func() map[string]int
	eventCountFn   func() int

	httpRequestsTotal          *prometheus.CounterVec
	httpRequestDurationSeconds *prometheus.HistogramVec
	configGeneration           prometheus.GaugeFunc
	requestlogEventsTotal      prometheus.GaugeFunc
	quotaStateCollector        *quotaStateGauge
}

// NewCollector creates a Collector with all Executor metrics. The generation,
// countByState, and eventCount functions supply live state for gauge metrics;
// they may be nil (gauges report zero in that case).
func NewCollector(generation func() uint64, countByState func() map[string]int, eventCount func() int) *Collector {
	c := &Collector{
		generationFn:   generation,
		countByStateFn: countByState,
		eventCountFn:   eventCount,
	}

	c.httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests by route, method and status class.",
		},
		[]string{labelRoute, labelMethod, labelStatusClass},
	)

	c.httpRequestDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds by route, method and status class.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{labelRoute, labelMethod, labelStatusClass},
	)

	c.configGeneration = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "config_generation",
			Help:      "Current compiled configuration generation number.",
		},
		func() float64 {
			c.mu.RLock()
			fn := c.generationFn
			c.mu.RUnlock()
			if fn != nil {
				return float64(fn())
			}
			return 0
		},
	)

	c.requestlogEventsTotal = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "requestlog_events_total",
			Help:      "Current number of events in the in-memory execution log.",
		},
		func() float64 {
			c.mu.RLock()
			fn := c.eventCountFn
			c.mu.RUnlock()
			if fn != nil {
				return float64(fn())
			}
			return 0
		},
	)

	c.quotaStateCollector = &quotaStateGauge{
		c: c,
		desc: prometheus.NewDesc(
			namespace+"_quota_reservations_total",
			"Current number of quota reservations by state.",
			[]string{labelState},
			nil,
		),
	}

	return c
}

// Register registers all metrics with the given registry.
func (c *Collector) Register(registry prometheus.Registerer) {
	registry.MustRegister(c.httpRequestsTotal)
	registry.MustRegister(c.httpRequestDurationSeconds)
	registry.MustRegister(c.configGeneration)
	registry.MustRegister(c.quotaStateCollector)
	registry.MustRegister(c.requestlogEventsTotal)
}

// Middleware returns an http.Handler that records request counter and latency
// histogram metrics. It must be the outermost middleware (outside
// AuthMiddleware) so that 401 responses are also counted.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		route := routeLabel(r.URL.Path)
		method := strings.ToLower(r.Method)
		cls := statusClass(rw.statusCode)

		c.httpRequestsTotal.WithLabelValues(route, method, cls).Inc()
		c.httpRequestDurationSeconds.WithLabelValues(route, method, cls).Observe(time.Since(start).Seconds())
	})
}

// Registry returns a new prometheus.Registry with all metrics registered.
// This is a convenience for the composition root.
func (c *Collector) Registry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	c.Register(reg)
	return reg
}

// quotaStateGauge implements prometheus.Collector for the
// executor_quota_reservations_total{state} gauge.
type quotaStateGauge struct {
	c    *Collector
	desc *prometheus.Desc
}

func (g *quotaStateGauge) Describe(ch chan<- *prometheus.Desc) {
	ch <- g.desc
}

func (g *quotaStateGauge) Collect(ch chan<- prometheus.Metric) {
	g.c.mu.RLock()
	fn := g.c.countByStateFn
	g.c.mu.RUnlock()
	if fn == nil {
		return
	}
	for state, count := range fn() {
		ch <- prometheus.MustNewConstMetric(g.desc, prometheus.GaugeValue, float64(count), state)
	}
}

// routeLabel maps a URL path to a stable low-cardinality route label.
func routeLabel(path string) string {
	p := strings.TrimRight(path, "/")
	switch {
	case p == "/healthz":
		return RouteHealthz
	case p == "/metrics":
		return RouteMetrics
	case p == "/v1/models":
		return RouteModels
	case p == "/v1/chat/completions":
		return RouteChatCompletions
	case p == "/v1/messages":
		return RouteMessages
	case p == "/v1/responses":
		return RouteResponses
	case p == "/v1/images/generations":
		return RouteImagesGenerations
	default:
		return RouteOther
	}
}

// statusClass maps an HTTP status code to a low-cardinality class label.
func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "0xx"
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code.
// It forwards http.Flusher if the underlying writer implements it, so SSE
// streaming continues to work through the metrics middleware.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code and delegates.
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Flush delegates to the underlying writer if it implements http.Flusher.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// String returns a redacted representation.
func (rw *responseWriter) String() string { return "metrics.responseWriter{redacted}" }

// ParseStatusClass is exported for testing only.
func ParseStatusClass(code int) string { return statusClass(code) }

// ParseRouteLabel is exported for testing only.
func ParseRouteLabel(path string) string { return routeLabel(path) }

// FormatStatusCode is a test helper that formats an int status code.
func FormatStatusCode(code int) string { return strconv.Itoa(code) }
