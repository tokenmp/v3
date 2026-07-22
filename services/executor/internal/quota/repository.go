package quota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Repository is the typed quota domain port consumed by all execution paths.
type Repository interface {
	ReserveReservation(context.Context, ReserveRequest) (Reservation, error)
	FinalizeReservation(context.Context, FinalizeRequest) (Reservation, error)
	ReleaseReservation(context.Context, ReleaseRequest) (Reservation, error)
	Lookup(context.Context, ReservationID) (Reservation, error)
}

var (
	ErrInvalidReservation = errors.New("invalid quota reservation")
	ErrInvalidMetadata    = errors.New("invalid quota metadata")
	ErrInvalidEstimate    = errors.New("invalid quota estimate")
	ErrInvalidOutcome     = errors.New("invalid quota finalize outcome")
	ErrInvalidRelease     = errors.New("invalid quota release reason")
	ErrConflict           = errors.New("quota reservation conflict")
	ErrNotFound           = errors.New("quota reservation not found")
)

// ReservationID is an owned quota-domain ID. quota intentionally does not
// import requestid or execution: its grammar is independently enforced.
type ReservationID string

const (
	reservationIDPrefix       = "res_"
	minReservationIDSuffixLen = 16
	maxReservationIDSuffixLen = 128
)

func (id ReservationID) Valid() bool {
	s := string(id)
	if !strings.HasPrefix(s, reservationIDPrefix) {
		return false
	}
	suffix := s[len(reservationIDPrefix):]
	if len(suffix) < minReservationIDSuffixLen || len(suffix) > maxReservationIDSuffixLen {
		return false
	}
	for _, c := range suffix {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// Metadata is the safe, bounded attribution captured when a reservation is
// made. It contains no request body, credential reference, endpoint, or key.
type Metadata struct {
	RequestID string
	Subject   string
	KeyID     string
	Protocol  string
	Model     string
	// InitialCandidate is retained for Phase 12.1 record compatibility. New
	// consumers must attribute each safe candidate dimension below.
	InitialCandidate string
	ProviderID       string
	RouteID          string
	CredentialID     string
	AdapterID        string
	Revision         string
	Generation       uint64
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

const maxGeneration uint64 = 1_000_000_000_000

// ValidateRequestIdentity validates the request-owned metadata that execution
// must reject before candidate preflight can inspect configuration or resolve a
// credential. Candidate attribution is validated by Metadata.Validate once the
// first prepared call is available.
func ValidateRequestID(requestID string) error {
	if !safeToken.MatchString(requestID) {
		return ErrInvalidMetadata
	}
	return nil
}

// ValidateQuotaIdentity validates request-authenticated quota attribution
// independently from a generated request ID.
func ValidateQuotaIdentity(subject, keyID, protocol string) error {
	if !safeToken.MatchString(subject) || !safeToken.MatchString(keyID) || !safeToken.MatchString(protocol) {
		return ErrInvalidMetadata
	}
	return nil
}

func ValidateRequestIdentity(requestID, subject, keyID, protocol string) error {
	if err := ValidateRequestID(requestID); err != nil {
		return err
	}
	return ValidateQuotaIdentity(subject, keyID, protocol)
}

// Validate verifies all safe, bounded reservation attribution. It is exported
// so consumers can fail before Reserve rather than depending on a repository
// implementation to reject malformed metadata.
func (m Metadata) Validate() error {
	if err := ValidateRequestIdentity(m.RequestID, m.Subject, m.KeyID, m.Protocol); err != nil {
		return err
	}
	if m.Generation == 0 || m.Generation > maxGeneration || !safeToken.MatchString(m.Model) || !safeToken.MatchString(m.Revision) {
		return ErrInvalidMetadata
	}
	if (safeToken.MatchString(m.ProviderID) && safeToken.MatchString(m.RouteID) && safeToken.MatchString(m.AdapterID) &&
		(m.CredentialID == "" || safeToken.MatchString(m.CredentialID))) || safeToken.MatchString(m.InitialCandidate) {
		return nil
	}
	return ErrInvalidMetadata
}

func (m Metadata) valid() bool { return m.Validate() == nil }

// EstimateBasis identifies the estimate schema. Only BasisNone is accepted in
// Phase 12.1; later phases may add explicitly versioned token-estimate bases.
type EstimateBasis string

const BasisNone EstimateBasis = "none"

type Estimate struct{ Basis EstimateBasis }

func (e Estimate) valid() bool { return e.Basis == BasisNone }

type AccountingDisposition string

const (
	AccountingUnpricedSuccess AccountingDisposition = "unpriced_success"
	AccountingConfirmedUsage  AccountingDisposition = "confirmed_usage"
)

// ConfirmedUsage is intentionally bounded so a faulty provider cannot turn an
// accounting record into an unbounded value. It is meaningful only for
// AccountingConfirmedUsage.
type ConfirmedUsage struct {
	InputTokens  uint64
	OutputTokens uint64
	TotalTokens  uint64
}

const maxConfirmedTokens uint64 = 1_000_000_000

func (u ConfirmedUsage) valid() bool {
	return u.Valid()
}

// Valid reports whether usage is within the quota-domain accounting bounds.
func (u ConfirmedUsage) Valid() bool {
	return u.InputTokens <= maxConfirmedTokens && u.OutputTokens <= maxConfirmedTokens &&
		u.TotalTokens <= maxConfirmedTokens && u.InputTokens+u.OutputTokens <= maxConfirmedTokens &&
		u.TotalTokens == u.InputTokens+u.OutputTokens
}

type CompletionOutcome string

const (
	OutcomeCompleted        CompletionOutcome = "completed"
	OutcomeAfterCommitError CompletionOutcome = "after_commit_error"
	OutcomeClientCancelled  CompletionOutcome = "client_cancelled"
)

type FinalizeOutcome struct {
	Disposition AccountingDisposition
	Outcome     CompletionOutcome
	Usage       ConfirmedUsage
}

func (o FinalizeOutcome) normalized() FinalizeOutcome {
	if o.Outcome == "" {
		o.Outcome = OutcomeCompleted
	}
	return o
}
func (o FinalizeOutcome) valid() bool {
	o = o.normalized()
	if o.Outcome != OutcomeCompleted && o.Outcome != OutcomeAfterCommitError && o.Outcome != OutcomeClientCancelled {
		return false
	}
	switch o.Disposition {
	case AccountingUnpricedSuccess:
		return o.Usage == (ConfirmedUsage{}) && o.Outcome == OutcomeCompleted
	case AccountingConfirmedUsage:
		return o.Usage.valid()
	default:
		return false
	}
}

type ReleaseReason string

const (
	ReleaseCancelled    ReleaseReason = "cancelled"
	ReleaseFailed       ReleaseReason = "failed"
	ReleasePrecondition ReleaseReason = "precondition_failed"
	ReleaseTimeout      ReleaseReason = "timeout"
	ReleaseAfterCommit  ReleaseReason = "after_commit_error"
	ReleaseUnresolved   ReleaseReason = "unresolved_usage"
)

func (r ReleaseReason) valid() bool {
	switch r {
	case ReleaseCancelled, ReleaseFailed, ReleasePrecondition, ReleaseTimeout, ReleaseAfterCommit, ReleaseUnresolved:
		return true
	default:
		return false
	}
}

type ReservationState string

const (
	ReservationReserved  ReservationState = "reserved"
	ReservationFinalized ReservationState = "finalized"
	ReservationReleased  ReservationState = "released"
)

// TerminalSettlement is populated exactly once on a terminal reservation. A
// finalized settlement has Outcome only; a released settlement has Reason only.
type TerminalSettlement struct {
	Outcome *FinalizeOutcome
	Reason  *ReleaseReason
}

func (s TerminalSettlement) validFor(state ReservationState) bool {
	switch state {
	case ReservationFinalized:
		return s.Outcome != nil && s.Outcome.valid() && s.Reason == nil
	case ReservationReleased:
		return s.Reason != nil && s.Reason.valid() && s.Outcome == nil
	default:
		return s == (TerminalSettlement{})
	}
}

// ReserveRequest is the exact claim required to create or replay a reservation.
// It is deliberately separate from the stored record so callers cannot set state
// or creation time.
type ReserveRequest struct {
	ID       ReservationID
	Metadata Metadata
	Estimate Estimate
}

// FinalizeRequest and ReleaseRequest make terminal intent and settlement
// explicit. Exact replay of the first request is idempotent; a divergent intent
// or settlement conflicts.
type FinalizeRequest struct {
	ID      ReservationID
	Outcome FinalizeOutcome
}
type ReleaseRequest struct {
	ID     ReservationID
	Reason ReleaseReason
}

// Reservation is an owned, deep-copyable typed quota record. Format is
// intentionally redacted and never renders principal IDs, model names, or IDs.
type Reservation struct {
	ID         ReservationID
	Metadata   Metadata
	Estimate   Estimate
	State      ReservationState
	Settlement TerminalSettlement
	CreatedAt  time.Time
}

func (r ReserveRequest) valid() bool {
	return r.ID.Valid() && r.Metadata.valid() && r.Estimate.valid()
}

func (r ReserveRequest) reservation() Reservation {
	return Reservation{ID: r.ID, Metadata: r.Metadata, Estimate: r.Estimate, State: ReservationReserved}
}

func (r Reservation) clone() Reservation {
	out := r
	if r.Settlement.Outcome != nil {
		outcome := *r.Settlement.Outcome
		out.Settlement.Outcome = &outcome
	}
	if r.Settlement.Reason != nil {
		reason := *r.Settlement.Reason
		out.Settlement.Reason = &reason
	}
	return out
}

// String, GoString, and Format deliberately redact every field. These records
// carry attribution and accounting data and must remain safe when passed to
// ordinary logging and test formatting.
func (r Reservation) String() string   { return "quota.Reservation{redacted}" }
func (r Reservation) GoString() string { return r.String() }
func (r Reservation) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, r.String())
}

// String, GoString, and Format keep IDs and all request attribution out of
// ordinary formatting paths.
func (id ReservationID) String() string   { return "quota.ReservationID{redacted}" }
func (id ReservationID) GoString() string { return id.String() }
func (id ReservationID) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, id.String())
}

func (m Metadata) String() string   { return "quota.Metadata{redacted}" }
func (m Metadata) GoString() string { return m.String() }
func (m Metadata) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, m.String())
}

func (e Estimate) String() string   { return "quota.Estimate{redacted}" }
func (e Estimate) GoString() string { return e.String() }
func (e Estimate) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, e.String())
}

func (u ConfirmedUsage) String() string   { return "quota.ConfirmedUsage{redacted}" }
func (u ConfirmedUsage) GoString() string { return u.String() }
func (u ConfirmedUsage) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, u.String())
}

func (o FinalizeOutcome) String() string   { return "quota.FinalizeOutcome{redacted}" }
func (o FinalizeOutcome) GoString() string { return o.String() }
func (o FinalizeOutcome) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, o.String())
}

func (s TerminalSettlement) String() string   { return "quota.TerminalSettlement{redacted}" }
func (s TerminalSettlement) GoString() string { return s.String() }
func (s TerminalSettlement) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, s.String())
}
