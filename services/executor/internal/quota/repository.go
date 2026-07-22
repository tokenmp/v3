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

// Repository is the Phase 12 typed quota domain port. It is deliberately
// separate from Port while Runner and StreamDriver still use the legacy port.
// Phase 12.2 will migrate those consumers and remove Port.
type Repository interface {
	ReserveReservation(context.Context, Reservation) (Reservation, error)
	FinalizeReservation(context.Context, ReservationID, FinalizeOutcome) (Reservation, error)
	ReleaseReservation(context.Context, ReservationID, ReleaseReason) (Reservation, error)
	Lookup(context.Context, ReservationID) (Reservation, error)
}

var (
	ErrInvalidReservation = errors.New("invalid quota reservation")
	ErrInvalidMetadata    = errors.New("invalid quota metadata")
	ErrInvalidEstimate    = errors.New("invalid quota estimate")
	ErrInvalidOutcome     = errors.New("invalid quota finalize outcome")
	ErrInvalidRelease     = errors.New("invalid quota release reason")
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
	RequestID        string
	Subject          string
	KeyID            string
	Protocol         string
	Model            string
	InitialCandidate string
	Revision         string
	Generation       uint64
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

const maxGeneration uint64 = 1_000_000_000_000

func (m Metadata) valid() bool {
	return m.Generation > 0 && m.Generation <= maxGeneration && safeToken.MatchString(m.RequestID) && safeToken.MatchString(m.Subject) &&
		safeToken.MatchString(m.KeyID) && safeToken.MatchString(m.Protocol) && safeToken.MatchString(m.Model) &&
		safeToken.MatchString(m.InitialCandidate) && safeToken.MatchString(m.Revision)
}

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
}

const maxConfirmedTokens uint64 = 1_000_000_000

func (u ConfirmedUsage) valid() bool {
	return u.InputTokens <= maxConfirmedTokens && u.OutputTokens <= maxConfirmedTokens && u.InputTokens+u.OutputTokens <= maxConfirmedTokens
}

type FinalizeOutcome struct {
	Disposition AccountingDisposition
	Usage       ConfirmedUsage
}

func (o FinalizeOutcome) valid() bool {
	switch o.Disposition {
	case AccountingUnpricedSuccess:
		return o.Usage == (ConfirmedUsage{})
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
)

func (r ReleaseReason) valid() bool {
	switch r {
	case ReleaseCancelled, ReleaseFailed, ReleasePrecondition, ReleaseTimeout:
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

func (r Reservation) validForReserve() bool {
	return r.ID.Valid() && r.Metadata.valid() && r.Estimate.valid() && r.State == ReservationReserved && r.Settlement.validFor(ReservationReserved) && r.CreatedAt.IsZero()
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
