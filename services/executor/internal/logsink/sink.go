// Package logsink forwards executor execution events to the Logging Service.
// It wraps an in-memory ExecutionPort (preserving local query capability) and
// synchronously posts each event as a single-event batch to the Logging
// Service /v1/logs/ingest endpoint using a background context.
//
// RecordExecution never returns an error from the remote post: logging
// degradation must never block or fail the executor's request path. The post
// uses context.Background() so caller cancellation does not abort the log
// delivery. HTTP failures are swallowed and logged via slog.
package logsink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/requestlog"
)

// MaxIngestBodyBytes is the hard cap on a serialized ingest batch. It mirrors
// the Logging Service server's 2 MiB body limit.
const MaxIngestBodyBytes = 2 << 20

// Sentinel errors. None embed the endpoint URL, host, port, or any
// request/response body material.
var (
	ErrSinkBlankURL    = errors.New("logsink: blank endpoint URL")
	ErrSinkInvalidURL  = errors.New("logsink: invalid endpoint URL")
	ErrSinkUnavailable = errors.New("logsink: logging service unavailable")
	ErrSinkOversized   = errors.New("logsink: ingest batch exceeds size limit")
)

// Options configures a RemoteSink.
type Options struct {
	// Endpoint is the base URL of the Logging Service (e.g.
	// "http://logging.example:18084"). It must be an http(s) URL with no
	// path segments, query, fragment, or userinfo. The sink appends
	// /v1/logs/ingest automatically.
	Endpoint string
	// Local is the in-memory ExecutionPort wrapped for local queries. Must
	// be non-nil.
	Local requestlog.ExecutionPort
	// HTTPClient is an optional injected client. If nil, a default client
	// with no redirect following and PostTimeout is used.
	HTTPClient *http.Client
	// PostTimeout is the timeout for each post. Defaults to 10s if zero.
	// Negative values are rejected.
	PostTimeout time.Duration
}

// RemoteSink wraps an ExecutionPort (in-memory) and posts each event to the
// Logging Service. It implements ExecutionPort.
type RemoteSink struct {
	inner    requestlog.ExecutionPort
	client   *http.Client
	endpoint string // base URL; /v1/logs/ingest is appended at post time
	logger   *slog.Logger
}

// NewRemoteSink creates a RemoteSink from the given options. The endpoint is
// validated: it must be a non-blank http(s) URL with no path, query,
// fragment, or userinfo. Local must be non-nil. PostTimeout must be
// non-negative.
func NewRemoteSink(opts Options) (*RemoteSink, error) {
	raw := strings.TrimSpace(opts.Endpoint)
	if raw == "" {
		return nil, ErrSinkBlankURL
	}
	if opts.Local == nil {
		return nil, ErrSinkInvalidURL
	}
	if opts.PostTimeout < 0 {
		return nil, ErrSinkInvalidURL
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrSinkInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrSinkInvalidURL
	}
	if u.Host == "" {
		return nil, ErrSinkInvalidURL
	}
	// Reject any path beyond an optional trailing slash.
	path := strings.TrimSuffix(u.Path, "/")
	if path != "" {
		return nil, ErrSinkInvalidURL
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, ErrSinkInvalidURL
	}

	// Normalize: strip trailing slash for clean concatenation.
	endpoint := strings.TrimSuffix(raw, "/")

	timeout := opts.PostTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}

	return &RemoteSink{
		inner:    opts.Local,
		client:   client,
		endpoint: endpoint,
		logger:   slog.Default(),
	}, nil
}

// RecordExecution records the event in the inner store and posts a
// single-event batch to the Logging Service. The post uses a background
// context and its error is swallowed (logged but never returned), so logging
// degradation never fails the executor's request path.
func (s *RemoteSink) RecordExecution(ctx context.Context, event requestlog.ExecutionEvent) error {
	if err := s.inner.RecordExecution(ctx, event); err != nil {
		return err
	}
	if event.RequestID == "" {
		return nil
	}
	b := buildBatch(event)
	if err := s.post(b); err != nil {
		s.logger.Warn("logsink post failed", "request_id", event.RequestID, "error", err)
	}
	return nil
}

// QueryEvents delegates to the inner store for local queries.
func (s *RemoteSink) QueryEvents(ctx context.Context, filter requestlog.ExecutionFilter) ([]requestlog.ExecutionEvent, error) {
	return s.inner.QueryEvents(ctx, filter)
}

// post sends a batch to the Logging Service /v1/logs/ingest endpoint. It
// returns a stable sentinel error; the endpoint URL, host, port, and any
// request/response body material are never embedded in the error.
func (s *RemoteSink) post(b batch) error {
	payload, err := json.Marshal(b)
	if err != nil {
		return ErrSinkUnavailable
	}
	if len(payload) > MaxIngestBodyBytes {
		return ErrSinkOversized
	}

	target := s.endpoint + "/v1/logs/ingest"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return ErrSinkUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return ErrSinkUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrSinkUnavailable
	}
	return nil
}

// ── Wire types ────────────────────────────────────────────────────────────
// These mirror the json tags of the Logging Service repository types
// (RequestLog, Attempt, Event) and the server's ingestRequest struct exactly.
// They are design/build-time aligned; there is no Go runtime import.

// requestLog mirrors repository.RequestLog.
type requestLog struct {
	ID                     int64      `json:"-"`
	RequestID              string     `json:"request_id"`
	TraceID                string     `json:"trace_id,omitempty"`
	UserID                 string     `json:"user_id,omitempty"`
	ClientKeyID            string     `json:"client_key_id,omitempty"`
	ModelName              string     `json:"model_name,omitempty"`
	ResolvedModel          string     `json:"resolved_model,omitempty"`
	RouteID                string     `json:"route_id,omitempty"`
	ProviderID             string     `json:"provider_id,omitempty"`
	CredentialID           string     `json:"credential_id,omitempty"`
	Protocol               string     `json:"protocol,omitempty"`
	Stream                 bool       `json:"stream"`
	FinalStatus            string     `json:"final_status"`
	HTTPStatus             int        `json:"http_status,omitempty"`
	InputTokens            int        `json:"input_tokens,omitempty"`
	OutputTokens           int        `json:"output_tokens,omitempty"`
	TotalTokens            int        `json:"total_tokens,omitempty"`
	CacheTokens            int        `json:"cache_tokens,omitempty"`
	LatencyMS              int        `json:"latency_ms,omitempty"`
	TTFTMS                 int        `json:"ttft_ms,omitempty"`
	ErrorCode              string     `json:"error_code,omitempty"`
	ErrorType              string     `json:"error_type,omitempty"`
	UpstreamHTTPStatus     int        `json:"upstream_http_status,omitempty"`
	UsageStatus            string     `json:"usage_status,omitempty"`
	ThinkingMode           string     `json:"thinking_mode,omitempty"`
	ThinkingEffort         string     `json:"thinking_effort,omitempty"`
	ThinkingEffortDegraded string     `json:"thinking_effort_degraded,omitempty"`
	ReservationID          string     `json:"reservation_id,omitempty"`
	BillingPlan            string     `json:"billing_plan,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
}

// attempt mirrors repository.Attempt.
type attempt struct {
	ID                 int64           `json:"-"`
	RequestLogID       int64           `json:"request_log_id"`
	RequestID          string          `json:"request_id"`
	AttemptIndex       int             `json:"attempt_index"`
	RouteID            string          `json:"route_id,omitempty"`
	ProviderID         string          `json:"provider_id,omitempty"`
	CredentialID       string          `json:"credential_id,omitempty"`
	UpstreamModel      string          `json:"upstream_model,omitempty"`
	UpstreamURL        string          `json:"upstream_url,omitempty"`
	Status             string          `json:"status"`
	HTTPStatus         int             `json:"http_status,omitempty"`
	LatencyMS          int             `json:"latency_ms,omitempty"`
	ErrorCode          string          `json:"error_code,omitempty"`
	ErrorType          string          `json:"error_type,omitempty"`
	UpstreamHTTPStatus int             `json:"upstream_http_status,omitempty"`
	RetryClassified    string          `json:"retry_classified,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

// timelineEvent mirrors repository.Event.
type timelineEvent struct {
	ID           int64           `json:"-"`
	RequestLogID int64           `json:"request_log_id"`
	RequestID    string          `json:"request_id"`
	TraceID      string          `json:"trace_id,omitempty"`
	Source       string          `json:"source"`
	Stage        string          `json:"stage"`
	Status       string          `json:"status"`
	AttemptIndex *int            `json:"attempt_index,omitempty"`
	DurationMS   int             `json:"duration_ms,omitempty"`
	Message      string          `json:"message,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

// batch is the Logging Service /v1/logs/ingest request body.
type batch struct {
	Log      requestLog      `json:"log"`
	Attempts []attempt       `json:"attempts,omitempty"`
	Events   []timelineEvent `json:"events,omitempty"`
}

// buildBatch assembles a single-event ingest batch from an ExecutionEvent.
func buildBatch(e requestlog.ExecutionEvent) batch {
	log := requestLog{
		RequestID:     e.RequestID,
		UserID:        e.Subject,
		ClientKeyID:   e.KeyID,
		ResolvedModel: e.Candidate.ModelID,
		RouteID:       e.Candidate.RouteID,
		ProviderID:    e.Candidate.ProviderID,
		CredentialID:  e.Candidate.CredentialID,
		Protocol:      e.Protocol,
		ReservationID: e.ReservationID,
		CreatedAt:     e.Timestamp,
	}

	// Final status from event status or kind.
	log.FinalStatus = e.Status
	if log.FinalStatus == "" {
		switch e.Kind {
		case requestlog.KindFinalized:
			log.FinalStatus = "success"
		case requestlog.KindReleased:
			log.FinalStatus = "failed"
		}
	}

	// Usage.
	if e.UsageKnown {
		log.InputTokens = int(e.Usage.InputTokens)
		log.OutputTokens = int(e.Usage.OutputTokens)
		log.TotalTokens = int(e.Usage.TotalTokens)
		log.UsageStatus = "final"
	}

	// Latency and HTTP status from attempt events.
	if e.Kind == requestlog.KindAttempt {
		log.LatencyMS = int(e.Latency / time.Millisecond)
	}

	// CompletedAt for terminal events.
	if e.Kind == requestlog.KindFinalized || e.Kind == requestlog.KindReleased {
		completed := e.Timestamp
		log.CompletedAt = &completed
	}

	// Error fields.
	log.ErrorCode = e.Code
	log.ErrorType = e.Type

	b := batch{Log: log}

	// Attempt row for KindAttempt.
	if e.Kind == requestlog.KindAttempt {
		b.Attempts = []attempt{{
			RequestID:     e.RequestID,
			AttemptIndex:  e.Attempt,
			RouteID:       e.Candidate.RouteID,
			ProviderID:    e.Candidate.ProviderID,
			CredentialID:  e.Candidate.CredentialID,
			UpstreamModel: e.Candidate.ModelID,
			Status:        e.Status,
			HTTPStatus:    parseHTTPStatus(e.Code),
			LatencyMS:     int(e.Latency / time.Millisecond),
			ErrorCode:     e.Code,
			ErrorType:     e.Type,
			CreatedAt:     e.Timestamp,
		}}
	}

	// Timeline event for every event.
	idx := e.Attempt
	b.Events = []timelineEvent{{
		RequestID:    e.RequestID,
		Source:       "executor",
		Stage:        e.Kind,
		Status:       e.Status,
		AttemptIndex: ptrIfPositive(idx),
		DurationMS:   int(e.Latency / time.Millisecond),
		CreatedAt:    e.Timestamp,
	}}

	return b
}

// parseHTTPStatus tries to parse a 3-digit HTTP status from a code string.
func parseHTTPStatus(code string) int {
	if len(code) == 3 {
		n := 0
		for _, c := range code {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
		if n >= 100 && n < 600 {
			return n
		}
	}
	return 0
}

// ptrIfPositive returns a pointer to v if v > 0, else nil.
func ptrIfPositive(v int) *int {
	if v > 0 {
		return &v
	}
	return nil
}
