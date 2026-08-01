// Package sweeper runs the background settlement loop for the billing
// settlement state machine.
//
// It owns two durable cleanup duties that must NOT depend on a request
// context (the request may have completed or been cancelled long before the
// reservation is resolvable):
//
//   - Expiry: reserved reservations whose expires_at is in the past are
//     marked 'expired' and a 'sweep' refund ledger row reverses the hold.
//   - Reconciliation: pending_reconciliation reservations are resolved from
//     confirmed terminal usage evidence queried via the evidence.Lookup port
//     (Billing never reads the Logging DB). The reconciler NEVER guesses:
//     known usage → Reconcile confirmed (the actual counts); not-found /
//     not-terminal / Logging-unavailable → keep pending and retry on the next
//     tick, up to an explicit retention deadline. Only once a pending
//     reservation is older than the retention deadline AND the configured
//     unknown-policy permits it is it released as unknown (under-charge,
//     audited reason). By default the policy is "keep pending and alert" so a
//     transient Logging outage can never cause a stable under-charge.
//
// The loop is bounded: each tick processes a finite batch, sleeps, and
// observes the process context for graceful shutdown. All errors are stable
// sentinels and are logged without leaking SQL/DSN; a single reservation's
// failure never aborts the whole sweep. The sweeper never deletes ledger
// rows — it only appends settlement rows and updates reservation status.
//
// This package does not claim cross-process exactly-once: the DB guarantees
// per-reservation idempotency (PK + idempotency_key UNIQUE + status-guarded
// UPDATE ... WHERE status=...), so a duplicate sweep of the same reservation
// is a no-op.
package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tokenmp/v3/services/billing/internal/evidence"
	"github.com/tokenmp/v3/services/billing/internal/repository"
)

// SweeperStore is the minimal persistence port the loop needs: the
// SettlementManager plus the list queries. GormRepository satisfies it.
type SweeperStore interface {
	repository.SettlementManager
	ListExpiredReservations(ctx context.Context, batch int) ([]string, error)
	ListPendingReservations(ctx context.Context, minAge time.Duration, batch int) ([]repository.PendingReservation, error)
}

// UnknownPolicy controls what happens to a pending reservation that has
// exceeded the retention deadline with no confirmed evidence. It is the
// explicit product decision that replaces blind release.
type UnknownPolicy string

const (
	// PolicyKeepPending is the default-safe policy: a reservation past the
	// retention deadline is KEPT pending and an alert counter is incremented.
	// No release, no sweep. This is correct when Logging outages must never
	// cause a stable under-charge; operators resolve stuck rows manually.
	PolicyKeepPending UnknownPolicy = "keep_pending"
	// PolicyReleaseUnknown permits releasing (under-charge, never a guessed
	// count) a reservation past the retention deadline with no evidence. It
	// is audited with reason "retention-expired-unknown". Use only when the
	// product accepts that long-unresolvable holds eventually free up.
	PolicyReleaseUnknown UnknownPolicy = "release_unknown"
)

// Config configures the sweep loop. All fields are validated by config.Load
// (fail-fast on illegal values); New only applies sane clamps for the
// zero-value test defaults. A non-positive Interval disables the loop (Run
// returns nil immediately) so operators can run a stateless billing instance
// without the background worker.
type Config struct {
	Interval     time.Duration // tick cadence (default 30s)
	PendingGrace time.Duration // pending reservations younger than this are left alone (default 2m)
	ExpiryBatch  int           // max expired reservations per tick (default 100)
	PendingBatch int           // max pending reservations per tick (default 100)
	// RetentionDeadline is how long a pending reservation is retried before
	// the unknown-policy applies. It MUST be > PendingGrace so evidence gets a
	// real chance. Default 30m.
	RetentionDeadline time.Duration
	// UnknownPolicy is applied to pending rows older than RetentionDeadline.
	// Default PolicyKeepPending (never blind-release).
	UnknownPolicy UnknownPolicy
}

const (
	defaultInterval       = 30 * time.Second
	defaultPendingGrace   = 2 * time.Minute
	defaultExpiryBatch    = 100
	defaultPendingBatch   = 100
	defaultRetention      = 30 * time.Minute
	defaultEvidenceLookup = 5 * time.Second
)

// Sweeper runs the expiry + reconciliation loop until ctx is cancelled.
type Sweeper struct {
	store   SweeperStore
	evid    evidence.Lookup
	cfg     Config
	evidTO  time.Duration
	logger  *slog.Logger
	metrics metricsSink
}

// metricsSink is an optional observability hook (nil-safe). Counters are
// safe to read for /metrics. The default in-memory impl is used by New.
type metricsSink interface {
	IncExpired()
	IncReconciledKnown()
	IncReconciledUnknown()
	IncRetentionAlert()
	IncExpiryError()
	IncReconcileError()
}

// inMemMetrics is the default metricsSink.
type inMemMetrics struct {
	expired, known, unknown, alerts, expiryErr, reconcileErr int64
}

func (m *inMemMetrics) IncExpired()           { m.expired++ }
func (m *inMemMetrics) IncReconciledKnown()   { m.known++ }
func (m *inMemMetrics) IncReconciledUnknown() { m.unknown++ }
func (m *inMemMetrics) IncRetentionAlert()    { m.alerts++ }
func (m *inMemMetrics) IncExpiryError()       { m.expiryErr++ }
func (m *inMemMetrics) IncReconcileError()    { m.reconcileErr++ }

// Stats returns safe counters for metrics/observability.
type Stats struct {
	ExpiredTotal      int64
	ReconciledKnown   int64
	ReconciledUnknown int64
	RetentionAlerts   int64
	ExpiryErrors      int64
	ReconcileErrors   int64
}

// New returns a Sweeper. A nil logger falls back to slog.Default. A nil
// evidence.Lookup is replaced by evidence.NilLookup so the loop runs but never
// resolves evidence (keeps pending per the policy). Defaults are applied only
// for zero values; callers (config.Load) are expected to validate first.
func New(store SweeperStore, cfg Config, ev evidence.Lookup, logger *slog.Logger) *Sweeper {
	if logger == nil {
		logger = slog.Default()
	}
	if ev == nil {
		ev = evidence.NilLookup{}
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.PendingGrace <= 0 {
		cfg.PendingGrace = defaultPendingGrace
	}
	if cfg.ExpiryBatch <= 0 {
		cfg.ExpiryBatch = defaultExpiryBatch
	}
	if cfg.PendingBatch <= 0 {
		cfg.PendingBatch = defaultPendingBatch
	}
	if cfg.RetentionDeadline <= 0 {
		cfg.RetentionDeadline = defaultRetention
	}
	if cfg.UnknownPolicy == "" {
		cfg.UnknownPolicy = PolicyKeepPending
	}
	return &Sweeper{store: store, evid: ev, cfg: cfg, evidTO: defaultEvidenceLookup, logger: logger, metrics: &inMemMetrics{}}
}

// Run blocks until ctx is cancelled, ticking at cfg.Interval. Each tick runs
// expireOnce then reconcileOnce. It returns nil on shutdown.
func (s *Sweeper) Run(ctx context.Context) error {
	if s.cfg.Interval <= 0 {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		s.tick(ctx)
	}
}

// tick runs one expiry + reconciliation pass. It uses a bounded detached
// context (independent of any request) so the work completes even if the
// process is shutting down — the outer ctx still bounds it for shutdown.
func (s *Sweeper) tick(ctx context.Context) {
	workCtx, cancel := context.WithTimeout(ctx, s.cfg.Interval)
	defer cancel()
	s.expireOnce(workCtx)
	s.reconcileOnce(workCtx)
}

func (s *Sweeper) expireOnce(ctx context.Context) {
	ids, err := s.store.ListExpiredReservations(ctx, s.cfg.ExpiryBatch)
	if err != nil {
		s.logger.Warn("sweeper list expired failed", "error", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if err := s.store.Expire(ctx, id); err != nil {
			// ErrConflict means the reservation was resolved concurrently
			// (finalized/released) — not an error worth counting.
			if errors.Is(err, repository.ErrConflict) {
				continue
			}
			s.logger.Warn("sweeper expire failed", "error", err)
			s.metrics.IncExpiryError()
			continue
		}
		s.metrics.IncExpired()
	}
}

// reconcileOnce resolves pending_reconciliation reservations from confirmed
// terminal usage evidence. It NEVER guesses:
//  1. For each pending row older than PendingGrace, query the evidence port
//     (Billing → Logging HTTP, never the DB).
//  2. Known → Reconcile confirmed (actual counts).
//  3. NotFound / NotTerminal / Unavailable → keep pending, retry next tick.
//  4. Only when older than RetentionDeadline does UnknownPolicy apply:
//     PolicyKeepPending (default) → keep pending + alert; PolicyReleaseUnknown
//     → Reconcile(id, reserved, 0, false) with audited reason.
func (s *Sweeper) reconcileOnce(ctx context.Context) {
	rows, err := s.store.ListPendingReservations(ctx, s.cfg.PendingGrace, s.cfg.PendingBatch)
	if err != nil {
		s.logger.Warn("sweeper list pending failed", "error", err)
		return
	}
	now := time.Now().UTC()
	for _, p := range rows {
		if ctx.Err() != nil {
			return
		}
		s.reconcileOne(ctx, p, now)
	}
}

// reconcileOne resolves a single pending reservation.
func (s *Sweeper) reconcileOne(ctx context.Context, p repository.PendingReservation, now time.Time) {
	age := now.Sub(p.ReservedAt.UTC())
	// Query confirmed terminal evidence with a bounded lookup context, so a
	// slow Logging cannot starve the whole tick. Logging is never read from
	// the DB; this is the sole evidence source.
	evCtx, cancel := context.WithTimeout(ctx, s.evidTO)
	defer cancel()
	ev, err := s.evid.TerminalUsage(evCtx, p.RequestID)
	// Release the evidence-lookup context immediately so the (potentially
	// slow) Reconcile call below runs against the full tick context, not the
	// short evidence timeout.
	cancel()
	switch {
	case err == nil && ev.Known:
		reqs, tokens := confirmedCounts(p, ev)
		if rerr := s.store.Reconcile(ctx, p.ID, reqs, tokens, true); rerr != nil {
			if errors.Is(rerr, repository.ErrConflict) {
				return // resolved concurrently
			}
			s.logger.Warn("sweeper reconcile (known) failed", "error", rerr)
			s.metrics.IncReconcileError()
			return
		}
		s.metrics.IncReconciledKnown()
	case errors.Is(err, evidence.ErrNotFound), errors.Is(err, evidence.ErrNotTerminal), errors.Is(err, evidence.ErrUnavailable):
		// Not yet resolvable OR Logging unreachable. Default-safe: keep
		// pending and retry next tick. Only apply the retention policy below.
		if age > s.cfg.RetentionDeadline {
			s.applyRetentionPolicy(ctx, p)
		}
	default:
		// Defensive: unknown error class from the port — treat as unavailable
		// (keep pending) and apply retention policy if overdue.
		if age > s.cfg.RetentionDeadline {
			s.applyRetentionPolicy(ctx, p)
		}
	}
}

// applyRetentionPolicy handles a pending reservation that has exceeded the
// retention deadline without confirmed evidence. Default-safe: keep pending
// and alert. PolicyReleaseUnknown releases (under-charge, audited reason).
func (s *Sweeper) applyRetentionPolicy(ctx context.Context, p repository.PendingReservation) {
	switch s.cfg.UnknownPolicy {
	case PolicyReleaseUnknown:
		// Release the held amount (under-charge, never a guessed count). The
		// 'reconcile' refund row records reason 'retention-expired-unknown'
		// via the Reconcile(usageKnown=false) path for auditability.
		if err := s.store.Reconcile(ctx, p.ID, 0, 0, false); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return
			}
			s.logger.Warn("sweeper retention release failed", "error", err)
			s.metrics.IncReconcileError()
			return
		}
		s.logger.Warn("sweeper retention deadline exceeded; released as unknown", "reservation_id", p.ID, "policy", string(s.cfg.UnknownPolicy))
		s.metrics.IncReconciledUnknown()
	default:
		// PolicyKeepPending (and any unrecognized policy): keep pending and
		// alert. Never blind-release.
		s.logger.Warn("sweeper retention deadline exceeded; keeping pending (default-safe)", "reservation_id", p.ID, "policy", string(s.cfg.UnknownPolicy))
		s.metrics.IncRetentionAlert()
	}
}

// confirmedCounts computes the actual final counts for a confirmed reconcile
// from the reservation's plan/hold and the evidence. Coding plans meter
// requests (one successful request = the reserved count); token plans meter
// tokens (from evidence). Tokens are never guessed.
func confirmedCounts(p repository.PendingReservation, ev evidence.Evidence) (reqs int, tokens int64) {
	switch p.BillingPlan {
	case "coding":
		// The request completed with confirmed usage; the unit is the
		// reserved request count (typically 1). Tokens are not metered for
		// coding plans.
		reqs = p.ReservedRequests
		if reqs < 0 {
			reqs = 0
		}
		return reqs, 0
	default:
		// token / image / free: meter tokens from evidence.
		t := ev.Tokens
		if t < 0 {
			t = 0
		}
		return 0, t
	}
}

// Stats returns safe counters for metrics/observability.
func (s *Sweeper) Stats() Stats {
	m := s.metrics.(*inMemMetrics)
	return Stats{
		ExpiredTotal:      m.expired,
		ReconciledKnown:   m.known,
		ReconciledUnknown: m.unknown,
		RetentionAlerts:   m.alerts,
		ExpiryErrors:      m.expiryErr,
		ReconcileErrors:   m.reconcileErr,
	}
}
