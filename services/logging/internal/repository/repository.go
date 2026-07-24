// Package repository writes and reads request log records from the Log DB.
//
// The logging service owns the durable write path: executor/edge push
// lifecycle records through the service, which persists them here. Reads
// expose a single request's log together with its attempts and event
// timeline.
//
// This package stores NO plaintext request/response bodies — the V3 schema
// intentionally dropped the body columns that were the V2 privacy pain point.
// Only summaries, token counts and classified error codes are persisted.
//
// Errors are stable sentinels. Driver errors (which may carry DSN fragments)
// are never surfaced via Error(); the repository maps failures to classified
// sentinels (ErrNotFound / ErrQueryFailed / ErrInsertFailed).
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
)

// RequestLog corresponds to the request_logs table (daily RANGE partitioned;
// PK is (id, created_at)). It holds the request-level summary with no
// plaintext body.
type RequestLog struct {
	ID                     int64      `json:"-" gorm:"column:id"`
	RequestID              string     `json:"request_id" gorm:"column:request_id"`
	TraceID                string     `json:"trace_id,omitempty" gorm:"column:trace_id"`
	UserID                 string     `json:"user_id,omitempty" gorm:"column:user_id"`
	ClientKeyID            string     `json:"client_key_id,omitempty" gorm:"column:client_key_id"`
	ModelName              string     `json:"model_name,omitempty" gorm:"column:model_name"`
	ResolvedModel          string     `json:"resolved_model,omitempty" gorm:"column:resolved_model"`
	RouteID                string     `json:"route_id,omitempty" gorm:"column:route_id"`
	ProviderID             string     `json:"provider_id,omitempty" gorm:"column:provider_id"`
	CredentialID           string     `json:"credential_id,omitempty" gorm:"column:credential_id"`
	Protocol               string     `json:"protocol,omitempty" gorm:"column:protocol"`
	Stream                 bool       `json:"stream" gorm:"column:stream"`
	FinalStatus            string     `json:"final_status" gorm:"column:final_status"`
	HTTPStatus             int        `json:"http_status,omitempty" gorm:"column:http_status"`
	InputTokens            int        `json:"input_tokens,omitempty" gorm:"column:input_tokens"`
	OutputTokens           int        `json:"output_tokens,omitempty" gorm:"column:output_tokens"`
	TotalTokens            int        `json:"total_tokens,omitempty" gorm:"column:total_tokens"`
	CacheTokens            int        `json:"cache_tokens,omitempty" gorm:"column:cache_tokens"`
	LatencyMS              int        `json:"latency_ms,omitempty" gorm:"column:latency_ms"`
	TTFTMS                 int        `json:"ttft_ms,omitempty" gorm:"column:ttft_ms"`
	ErrorCode              string     `json:"error_code,omitempty" gorm:"column:error_code"`
	ErrorType              string     `json:"error_type,omitempty" gorm:"column:error_type"`
	UpstreamHTTPStatus     int        `json:"upstream_http_status,omitempty" gorm:"column:upstream_http_status"`
	UsageStatus            string     `json:"usage_status,omitempty" gorm:"column:usage_status"`
	ThinkingMode           string     `json:"thinking_mode,omitempty" gorm:"column:thinking_mode"`
	ThinkingEffort         string     `json:"thinking_effort,omitempty" gorm:"column:thinking_effort"`
	ThinkingEffortDegraded string     `json:"thinking_effort_degraded,omitempty" gorm:"column:thinking_effort_degraded"`
	ReservationID          string     `json:"reservation_id,omitempty" gorm:"column:reservation_id"`
	BillingPlan            string     `json:"billing_plan,omitempty" gorm:"column:billing_plan"`
	CreatedAt              time.Time  `json:"created_at" gorm:"column:created_at"`
	CompletedAt            *time.Time `json:"completed_at,omitempty" gorm:"column:completed_at"`
}

// Attempt corresponds to the request_attempts table (daily RANGE partitioned;
// PK is (id, created_at)). One request may have multiple attempts
// (retry/fallback). request_log_id is a logical (non-FK) link to the parent
// request_logs row.
type Attempt struct {
	ID                 int64           `json:"-" gorm:"column:id"`
	RequestLogID       int64           `json:"request_log_id" gorm:"column:request_log_id"`
	RequestID          string          `json:"request_id" gorm:"column:request_id"`
	AttemptIndex       int             `json:"attempt_index" gorm:"column:attempt_index"`
	RouteID            string          `json:"route_id,omitempty" gorm:"column:route_id"`
	ProviderID         string          `json:"provider_id,omitempty" gorm:"column:provider_id"`
	CredentialID       string          `json:"credential_id,omitempty" gorm:"column:credential_id"`
	UpstreamModel      string          `json:"upstream_model,omitempty" gorm:"column:upstream_model"`
	UpstreamURL        string          `json:"upstream_url,omitempty" gorm:"column:upstream_url"`
	Status             string          `json:"status" gorm:"column:status"`
	HTTPStatus         int             `json:"http_status,omitempty" gorm:"column:http_status"`
	LatencyMS          int             `json:"latency_ms,omitempty" gorm:"column:latency_ms"`
	ErrorCode          string          `json:"error_code,omitempty" gorm:"column:error_code"`
	ErrorType          string          `json:"error_type,omitempty" gorm:"column:error_type"`
	UpstreamHTTPStatus int             `json:"upstream_http_status,omitempty" gorm:"column:upstream_http_status"`
	RetryClassified    string          `json:"retry_classified,omitempty" gorm:"column:retry_classified"`
	Metadata           json.RawMessage `json:"metadata,omitempty" gorm:"column:metadata"`
	CreatedAt          time.Time       `json:"created_at" gorm:"column:created_at"`
}

// Event corresponds to the request_log_events table (daily RANGE partitioned;
// PK is (id, created_at)). Events form the per-request timeline and originate
// from edge and executor pushes.
type Event struct {
	ID           int64           `json:"-" gorm:"column:id"`
	RequestLogID int64           `json:"request_log_id" gorm:"column:request_log_id"`
	RequestID    string          `json:"request_id" gorm:"column:request_id"`
	TraceID      string          `json:"trace_id,omitempty" gorm:"column:trace_id"`
	Source       string          `json:"source" gorm:"column:source"`
	Stage        string          `json:"stage" gorm:"column:stage"`
	Status       string          `json:"status" gorm:"column:status"`
	AttemptIndex *int            `json:"attempt_index,omitempty" gorm:"column:attempt_index"`
	DurationMS   int             `json:"duration_ms,omitempty" gorm:"column:duration_ms"`
	Message      string          `json:"message,omitempty" gorm:"column:message"`
	Metadata     json.RawMessage `json:"metadata,omitempty" gorm:"column:metadata"`
	CreatedAt    time.Time       `json:"created_at" gorm:"column:created_at"`
}

// Writer is the write contract used by the logging service to persist
// request lifecycle records pushed by executor/edge.
type Writer interface {
	// InsertRequestLog inserts a request-level summary row and returns the
	// assigned id. created_at is pinned to a non-zero value (caller-supplied
	// or now()) so the row routes into the correct daily partition.
	InsertRequestLog(ctx context.Context, log RequestLog) (id int64, err error)
	// InsertAttempt inserts an attempt-level row. request_log_id must be set.
	InsertAttempt(ctx context.Context, attempt Attempt) error
	// InsertEvent inserts a timeline event. request_log_id must be set.
	InsertEvent(ctx context.Context, event Event) error
}

// Reader is the read contract for query-side consumers.
type Reader interface {
	// GetRequestLog returns the request-level summary for requestID.
	// Returns ErrNotFound when no row matches.
	GetRequestLog(ctx context.Context, requestID string) (RequestLog, error)
	// ListAttempts returns the attempts for requestID ordered by time.
	ListAttempts(ctx context.Context, requestID string) ([]Attempt, error)
	// ListEvents returns the timeline events for requestID ordered by time.
	ListEvents(ctx context.Context, requestID string) ([]Event, error)
}

// Stable classified errors. They do not wrap the driver error so DSN/SQL
// fragments never reach logs through Error().
var (
	ErrNotFound     = errors.New("repository: not found")
	ErrQueryFailed  = errors.New("repository: query failed")
	ErrInsertFailed = errors.New("repository: insert failed")
)

// GormRepository persists and reads log records via GORM. It is the single
// implementation of Writer and Reader in production.
type GormRepository struct {
	db *gorm.DB
}

// New returns a GORM-backed repository.
func New(db *gorm.DB) *GormRepository {
	return &GormRepository{db: db}
}

// insertRequestLogSQL inserts a request-level summary. usage_status is the
// only nullable text column carrying a CHECK constraint (NULL or one of
// final/pending/estimated/missing); an unset Go string is the empty string,
// which would violate that CHECK, so it is mapped to NULL via NULLIF. All
// other nullable columns accept the empty string / zero value, and the
// NOT-NULL columns (request_id, final_status, stream, created_at) are always
// supplied. The assigned bigserial id is returned via RETURNING.
const insertRequestLogSQL = `INSERT INTO request_logs (
  request_id, trace_id, user_id, client_key_id, model_name, resolved_model,
  route_id, provider_id, credential_id, protocol, stream, final_status,
  http_status, input_tokens, output_tokens, total_tokens, cache_tokens,
  latency_ms, ttft_ms, error_code, error_type, upstream_http_status,
  usage_status, thinking_mode, thinking_effort, thinking_effort_degraded,
  reservation_id, billing_plan, created_at, completed_at
) VALUES (
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?,
  NULLIF(?, '')::text, ?, ?, ?,
  ?, ?, ?, ?
)
RETURNING id`

// InsertRequestLog inserts a request-level summary. created_at is defaulted
// to now() (UTC) when the caller leaves it zero so the partitioned table can
// route the row. The assigned bigserial id is returned via RETURNING.
func (r *GormRepository) InsertRequestLog(ctx context.Context, log RequestLog) (int64, error) {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	var id int64
	if err := r.db.WithContext(ctx).Raw(insertRequestLogSQL,
		log.RequestID, log.TraceID, log.UserID, log.ClientKeyID, log.ModelName, log.ResolvedModel,
		log.RouteID, log.ProviderID, log.CredentialID, log.Protocol, log.Stream, log.FinalStatus,
		log.HTTPStatus, log.InputTokens, log.OutputTokens, log.TotalTokens, log.CacheTokens,
		log.LatencyMS, log.TTFTMS, log.ErrorCode, log.ErrorType, log.UpstreamHTTPStatus,
		log.UsageStatus, log.ThinkingMode, log.ThinkingEffort, log.ThinkingEffortDegraded,
		log.ReservationID, log.BillingPlan, log.CreatedAt, log.CompletedAt,
	).Scan(&id).Error; err != nil {
		return 0, ErrInsertFailed
	}
	return id, nil
}

// insertAttemptSQL inserts an attempt-level row. retry_classified is the
// only nullable text column with a CHECK constraint (NULL or one of
// retryable/non_retryable/terminal); an unset Go empty string would violate
// it, so it is mapped to NULL via NULLIF. metadata is nullable jsonb and
// becomes NULL when the caller passes a nil/empty RawMessage.
const insertAttemptSQL = `INSERT INTO request_attempts (
  request_log_id, request_id, attempt_index, route_id, provider_id,
  credential_id, upstream_model, upstream_url, status, http_status,
  latency_ms, error_code, error_type, upstream_http_status,
  retry_classified, metadata, created_at
) VALUES (
  ?, ?, ?, ?, ?,
  ?, ?, ?, ?, ?,
  ?, ?, ?, ?,
  NULLIF(?, '')::text, NULLIF(?::text, '')::jsonb, ?
)
RETURNING id`

// InsertAttempt inserts an attempt-level row. created_at is defaulted to now()
// (UTC) when zero so partition routing works.
func (r *GormRepository) InsertAttempt(ctx context.Context, attempt Attempt) error {
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now().UTC()
	}
	var id int64
	if err := r.db.WithContext(ctx).Raw(insertAttemptSQL,
		attempt.RequestLogID, attempt.RequestID, attempt.AttemptIndex, attempt.RouteID, attempt.ProviderID,
		attempt.CredentialID, attempt.UpstreamModel, attempt.UpstreamURL, attempt.Status, attempt.HTTPStatus,
		attempt.LatencyMS, attempt.ErrorCode, attempt.ErrorType, attempt.UpstreamHTTPStatus,
		attempt.RetryClassified, rawJSONText(attempt.Metadata), attempt.CreatedAt,
	).Scan(&id).Error; err != nil {
		return ErrInsertFailed
	}
	return nil
}

// insertEventSQL inserts a timeline event. source, stage and status are
// NOT NULL with CHECK constraints and are always supplied by the caller.
// metadata is nullable jsonb and becomes NULL when nil/empty.
const insertEventSQL = `INSERT INTO request_log_events (
  request_log_id, request_id, trace_id, source, stage, status,
  attempt_index, duration_ms, message, metadata, created_at
) VALUES (
  ?, ?, ?, ?, ?, ?,
  ?, ?, ?, NULLIF(?::text, '')::jsonb, ?
)
RETURNING id`

// InsertEvent inserts a timeline event. created_at is defaulted to now()
// (UTC) when zero so partition routing works.
func (r *GormRepository) InsertEvent(ctx context.Context, event Event) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	var id int64
	if err := r.db.WithContext(ctx).Raw(insertEventSQL,
		event.RequestLogID, event.RequestID, event.TraceID, event.Source, event.Stage, event.Status,
		event.AttemptIndex, event.DurationMS, event.Message, rawJSONText(event.Metadata), event.CreatedAt,
	).Scan(&id).Error; err != nil {
		return ErrInsertFailed
	}
	return nil
}

// Batch is the atomic ingestion payload for a single request lifecycle: the
// request-level summary, its attempts and its timeline events. It is
// persisted as a single all-or-nothing unit. No plaintext request/response
// body is carried (V3 privacy design).
type Batch struct {
	Log      RequestLog
	Attempts []Attempt
	Events   []Event
}

// BatchIngestor persists a full lifecycle batch atomically: the request log,
// its attempts and its events are committed in a single transaction or none
// survive. Failure is classified to ErrInsertFailed, a sentinel that never
// carries SQL or DSN fragments.
type BatchIngestor interface {
	IngestBatch(ctx context.Context, batch Batch) error
}

// IngestBatch persists batch within a single transaction. The request log is
// inserted first (RETURNING its assigned id); that id is stamped onto every
// attempt and event so they reference the parent row inserted in the same
// atomic unit. Any insert failure rolls back the whole batch and returns
// ErrInsertFailed, which never leaks SQL/DSN.
func (r *GormRepository) IngestBatch(ctx context.Context, batch Batch) error {
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return ErrInsertFailed
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback().Error
		}
	}()

	log := batch.Log
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	var logID int64
	if err := tx.Raw(insertRequestLogSQL,
		log.RequestID, log.TraceID, log.UserID, log.ClientKeyID, log.ModelName, log.ResolvedModel,
		log.RouteID, log.ProviderID, log.CredentialID, log.Protocol, log.Stream, log.FinalStatus,
		log.HTTPStatus, log.InputTokens, log.OutputTokens, log.TotalTokens, log.CacheTokens,
		log.LatencyMS, log.TTFTMS, log.ErrorCode, log.ErrorType, log.UpstreamHTTPStatus,
		log.UsageStatus, log.ThinkingMode, log.ThinkingEffort, log.ThinkingEffortDegraded,
		log.ReservationID, log.BillingPlan, log.CreatedAt, log.CompletedAt,
	).Scan(&logID).Error; err != nil {
		return ErrInsertFailed
	}
	if logID == 0 {
		return ErrInsertFailed
	}

	for _, attempt := range batch.Attempts {
		attempt.RequestLogID = logID
		if attempt.CreatedAt.IsZero() {
			attempt.CreatedAt = time.Now().UTC()
		}
		var id int64
		if err := tx.Raw(insertAttemptSQL,
			attempt.RequestLogID, attempt.RequestID, attempt.AttemptIndex, attempt.RouteID, attempt.ProviderID,
			attempt.CredentialID, attempt.UpstreamModel, attempt.UpstreamURL, attempt.Status, attempt.HTTPStatus,
			attempt.LatencyMS, attempt.ErrorCode, attempt.ErrorType, attempt.UpstreamHTTPStatus,
			attempt.RetryClassified, rawJSONText(attempt.Metadata), attempt.CreatedAt,
		).Scan(&id).Error; err != nil {
			return ErrInsertFailed
		}
	}

	for _, event := range batch.Events {
		event.RequestLogID = logID
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now().UTC()
		}
		var id int64
		if err := tx.Raw(insertEventSQL,
			event.RequestLogID, event.RequestID, event.TraceID, event.Source, event.Stage, event.Status,
			event.AttemptIndex, event.DurationMS, event.Message, rawJSONText(event.Metadata), event.CreatedAt,
		).Scan(&id).Error; err != nil {
			return ErrInsertFailed
		}
	}

	if err := tx.Commit().Error; err != nil {
		return ErrInsertFailed
	}
	committed = true
	return nil
}

// rawJSONText returns the JSON text of a json.RawMessage for binding as a
// jsonb parameter. A nil or zero-length RawMessage yields the empty string,
// which the NULLIF(?, empty) cast in the insert SQL maps to a real SQL NULL
// (an empty string would otherwise fail the jsonb cast).
func rawJSONText(b json.RawMessage) string {
	if len(b) == 0 {
		return ""
	}
	return string(b)
}

// GetRequestLog looks up the request-level summary by request_id. The
// request_id index spans partitions, so this queries across the partitioned
// table. A real query error is ErrQueryFailed; no matching row is
// ErrNotFound (detected via the zero id since Raw().Scan() does not return
// gorm.ErrRecordNotFound).
func (r *GormRepository) GetRequestLog(ctx context.Context, requestID string) (RequestLog, error) {
	const q = `
SELECT id, request_id, trace_id, user_id, client_key_id, model_name, resolved_model,
       route_id, provider_id, credential_id, protocol, stream, final_status,
       http_status, input_tokens, output_tokens, total_tokens, cache_tokens,
       latency_ms, ttft_ms, error_code, error_type, upstream_http_status,
       usage_status, thinking_mode, thinking_effort, thinking_effort_degraded,
       reservation_id, billing_plan, created_at, completed_at
FROM request_logs
WHERE request_id = ?
LIMIT 1`
	var row RequestLog
	if err := r.db.WithContext(ctx).Raw(q, requestID).Scan(&row).Error; err != nil {
		return RequestLog{}, ErrQueryFailed
	}
	if row.ID == 0 {
		return RequestLog{}, ErrNotFound
	}
	return row, nil
}

// ListAttempts returns all attempts for requestID ordered by created_at then
// id for a stable timeline.
func (r *GormRepository) ListAttempts(ctx context.Context, requestID string) ([]Attempt, error) {
	const q = `
SELECT id, request_log_id, request_id, attempt_index, route_id, provider_id,
       credential_id, upstream_model, upstream_url, status, http_status,
       latency_ms, error_code, error_type, upstream_http_status,
       retry_classified, metadata, created_at
FROM request_attempts
WHERE request_id = ?
ORDER BY created_at ASC, id ASC`
	var rows []Attempt
	if err := r.db.WithContext(ctx).Raw(q, requestID).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}

// ListEvents returns the timeline events for requestID ordered by created_at
// then id.
func (r *GormRepository) ListEvents(ctx context.Context, requestID string) ([]Event, error) {
	const q = `
SELECT id, request_log_id, request_id, trace_id, source, stage, status,
       attempt_index, duration_ms, message, metadata, created_at
FROM request_log_events
WHERE request_id = ?
ORDER BY created_at ASC, id ASC`
	var rows []Event
	if err := r.db.WithContext(ctx).Raw(q, requestID).Scan(&rows).Error; err != nil {
		return nil, ErrQueryFailed
	}
	return rows, nil
}
