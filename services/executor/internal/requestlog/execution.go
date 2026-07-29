package requestlog

import (
	"context"
	"time"
)

// Kind constants for ExecutionEvent.Kind.
const (
	KindAttempt   = "attempt"
	KindReserved  = "reserved"
	KindStarted   = "started"
	KindFinalized = "finalized"
	KindReleased  = "released"
	KindCommitted = "committed"
)

// ExecutionUsage carries bounded token counters extracted from a provider
// response. All fields are safe public metadata; no secret, URL, or body
// material is present.
type ExecutionUsage struct {
	InputTokens  uint64
	OutputTokens uint64
	TotalTokens  uint64
}

// ExecutionSettlement carries the safe, public outcome of a quota terminal
// action. Disposition and Outcome mirror the quota domain's safe string
// constants; Reason is the release reason string when applicable.
type ExecutionSettlement struct {
	Disposition string
	Outcome     string
	Reason      string
}

// ExecutionEvent is the safe, lifecycle execution record supplied to an
// ExecutionPort. It intentionally has no request body, URL, header,
// credential reference, or secret field.
//
// This event is an in-process observation value. It provides neither durable
// storage nor idempotency guarantees.
type ExecutionEvent struct {
	RequestID     string
	ReservationID string
	Revision      string
	Generation    uint64
	Attempt       int
	Candidate     ExecutionCandidate
	Protocol      string
	Kind          string
	RuleID        string
	Action        string
	Status        string
	Code          string
	Type          string
	// FailureCategory carries a coarse upstream failure classification
	// (timeout / transport_error / upstream_error) for Kind=attempt and
	// Kind=released events. It is the safe, provider-agnostic label used by
	// remote sinks to populate the Logging DB final_status enum.
	FailureCategory string
	Timestamp       time.Time

	// Subject is the authenticated identity subject (non-secret).
	Subject string
	// KeyID is the authenticated identity key identifier (non-secret).
	KeyID string
	// Latency is the wall-clock duration of the attempt. It is non-zero only
	// for Kind=attempt events.
	Latency time.Duration
	// HTTPStatus is the mapped client-facing HTTP status (from adapter
	// MapResponse) for Kind=attempt failure events.
	HTTPStatus int
	// UpstreamStatus is the real upstream HTTP status for Kind=attempt
	// failure events (e.g. 401). 0 when no upstream response was received.
	UpstreamStatus int
	// UpstreamRequestID is the sanitized upstream x-request-id for
	// Kind=attempt failure events.
	UpstreamRequestID string
	// UpstreamMessage is the sanitized upstream error message retained for
	// admin diagnostics. Never returned to clients.
	UpstreamMessage string
	// UpstreamResponseBody is the sanitized raw upstream error response body
	// retained for admin diagnostics/reproduction. Bounded by 4 KiB, control
	// characters replaced with spaces. Never returned to clients.
	UpstreamResponseBody string
	// RetryStop is the retry State stop reason for Kind=attempt failure
	// events (e.g. "no_match", "retry_none", "max_total_attempts").
	RetryStop string
	// TTFT is the time-to-first-token for streaming attempts. It is
	// non-zero only for streaming Kind=attempt events where the bridge
	// measured the elapsed time from stream start to first committed
	// semantic event.
	TTFT time.Duration
	// Stream reports whether the request was a streaming request.
	// Set by the StreamDriver for streaming attempts.
	Stream bool
	// Usage carries bounded token counters when available.
	Usage ExecutionUsage
	// UsageKnown reports whether Usage was explicitly confirmed by the
	// provider response.
	UsageKnown bool
	// Committed reports whether the stream bridge committed before the event
	// was recorded. Relevant only for streaming events.
	Committed bool
	// Settlement carries the safe terminal outcome for Kind=finalized and
	// Kind=released events.
	Settlement ExecutionSettlement
}

// ExecutionCandidate contains only safe, stable candidate identifiers. It
// intentionally cannot carry a credential reference or secret material.
type ExecutionCandidate struct {
	ModelID      string
	ProviderID   string
	RouteID      string
	CredentialID string
	AdapterID    string
}

// ExecutionFilter is the optional query filter for QueryEvents.
type ExecutionFilter struct {
	RequestID     string
	ReservationID string
	Kind          string
}

// ExecutionPort records safe, lifecycle execution events and supports
// querying recorded events by filter.
type ExecutionPort interface {
	RecordExecution(ctx context.Context, event ExecutionEvent) error
	QueryEvents(ctx context.Context, filter ExecutionFilter) ([]ExecutionEvent, error)
}
