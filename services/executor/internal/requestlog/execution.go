package requestlog

import (
	"context"
	"time"
)

// ExecutionEvent is the safe, attempt-level execution record supplied to an
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
	Timestamp     time.Time
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

// ExecutionPort records safe, attempt-level execution events.
type ExecutionPort interface {
	RecordExecution(ctx context.Context, event ExecutionEvent) error
}
