// Package evidence is the Billing Service's narrow, secret-free port for
// resolving pending reservations from confirmed terminal usage evidence.
//
// Billing must NOT read the Logging Service database directly. Instead a
// reconciler queries Logging over HTTP through the [Lookup] port: given a
// reservation's request_id it asks "is the terminal usage for this request
// known yet?". Logging is the sole owner of request_log rows; Billing only
// consumes the safe projection it returns.
//
// The contract is deliberately minimal and tri-state so the reconciler can
// implement a safe retry-until-retention policy:
//
//   - Known:   the request reached a terminal status with confirmed usage
//     (usage_status="final"). The reconciler settles the actual
//     counts and never guesses.
//   - NotFound: no request_log row exists yet (the executor/edge has not
//     written it, or it was purged). Retriable until retention.
//   - NotTerminal: a row exists but usage_status is not "final" yet
//     (processing/pending/estimated/missing). Retriable until
//     retention.
//   - Unavailable: Logging could not be reached or returned an error. The
//     reconciler MUST NOT release on this — it keeps the
//     reservation pending and retries on the next tick.
//
// No method or error ever leaks the Logging URL, request body or response
// body. A nil Lookup is valid and reports Unavailable for every call so a
// billing deployment without a configured Logging endpoint degrades to the
// default-safe "keep pending and alert" policy rather than blind-releasing.
package evidence

import (
	"context"
	"errors"
)

// Evidence is the confirmed terminal usage for one request, as reported by
// the Logging Service. Known is true only when usage_status="final"; in that
// case Requests and Tokens carry the actual consumed counts (>=0) and must be
// settled as-is — never guessed.
type Evidence struct {
	Known    bool
	Requests int
	Tokens   int64
}

// Sentinel errors. They never wrap an upstream error so URL/host/body
// fragments never reach logs through Error()/Unwrap().
var (
	// ErrNotFound means no request_log row exists for the request yet. It is
	// retriable: the reconciler keeps the reservation pending and tries again
	// on the next tick, up to the retention deadline.
	ErrNotFound = errors.New("evidence: not found")
	// ErrNotTerminal means a request_log row exists but its usage_status is
	// not "final" (the request is still processing or usage is pending). Also
	// retriable up to the retention deadline.
	ErrNotTerminal = errors.New("evidence: not terminal")
	// ErrUnavailable means Logging could not be reached or returned an error.
	// The reconciler MUST NOT release on this — it keeps the reservation
	// pending and retries. This is the default for an unconfigured Lookup.
	ErrUnavailable = errors.New("evidence: logging unavailable")
)

// Lookup resolves confirmed terminal usage evidence for a request from the
// Logging Service. It is the ONLY way Billing learns upstream usage for a
// pending reservation (Billing never reads Logging's DB).
//
// Implementations must be strict: bounded timeout, no redirects, no URL/body
// in errors, optional service token. A nil receiver (NilLookup) reports
// ErrUnavailable for every call.
type Lookup interface {
	// TerminalUsage returns the confirmed terminal usage for requestID.
	// Known==true only when usage_status="final". Errors are the sentinels
	// above; the context error is preserved by returning ErrUnavailable.
	TerminalUsage(ctx context.Context, requestID string) (Evidence, error)
}

// NilLookup is the zero-value Lookup: every call reports ErrUnavailable. It
// lets the reconciler run without a configured Logging endpoint while keeping
// the default-safe "keep pending" policy (never blind-release).
type NilLookup struct{}

// TerminalUsage implements Lookup.
func (NilLookup) TerminalUsage(context.Context, string) (Evidence, error) {
	return Evidence{}, ErrUnavailable
}
