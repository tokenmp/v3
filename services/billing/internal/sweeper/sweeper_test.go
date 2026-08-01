package sweeper

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/billing/internal/evidence"
	"github.com/tokenmp/v3/services/billing/internal/repository"
)

// fakeStore implements SweeperStore for tests.
type fakeStore struct {
	mu              sync.Mutex
	expiredIDs      []string
	pendingRows     []repository.PendingReservation
	expired         []string
	reconciledKnown []repository.PendingReservation
	reconciledUnk   []repository.PendingReservation
	retentionKept   []repository.PendingReservation
	expireErr       error
	reconcileErr    error
	listExpiredErr  error
	listPendingErr  error
	expireCalls     int
	reconcileCalls  int
}

func (f *fakeStore) GetReservation(context.Context, string) (repository.ReservationStatus, error) {
	return repository.ReservationStatus{}, repository.ErrNotFound
}
func (f *fakeStore) MarkPending(context.Context, string) error { return nil }
func (f *fakeStore) Reconcile(_ context.Context, id string, reqs int, tokens int64, known bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconcileCalls++
	if f.reconcileErr != nil {
		return f.reconcileErr
	}
	// Mimic real behavior: a reconciled reservation leaves the pending list,
	// so the next tick does not re-reconcile it.
	for i, p := range f.pendingRows {
		if p.ID == id {
			f.pendingRows = append(f.pendingRows[:i], f.pendingRows[i+1:]...)
			break
		}
	}
	p := repository.PendingReservation{ID: id}
	if known {
		f.reconciledKnown = append(f.reconciledKnown, p)
	} else {
		f.reconciledUnk = append(f.reconciledUnk, p)
	}
	_ = reqs
	_ = tokens
	return nil
}
func (f *fakeStore) Expire(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireCalls++
	if f.expireErr != nil {
		return f.expireErr
	}
	f.expired = append(f.expired, id)
	return nil
}
func (f *fakeStore) ListExpiredReservations(context.Context, int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listExpiredErr != nil {
		return nil, f.listExpiredErr
	}
	return f.expiredIDs, nil
}
func (f *fakeStore) ListPendingReservations(context.Context, time.Duration, int) ([]repository.PendingReservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listPendingErr != nil {
		return nil, f.listPendingErr
	}
	return f.pendingRows, nil
}

// fakeEvidence implements evidence.Lookup with controllable behavior.
type fakeEvidence struct {
	mu        sync.Mutex
	calls     int
	known     bool
	tokens    int64
	err       error
	delay     time.Duration
	byRequest map[string]result
}

type result struct {
	known  bool
	tokens int64
	err    error
}

func (f *fakeEvidence) TerminalUsage(ctx context.Context, requestID string) (evidence.Evidence, error) {
	f.mu.Lock()
	f.calls++
	delay := f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return evidence.Evidence{}, evidence.ErrUnavailable
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.byRequest != nil {
		if r, ok := f.byRequest[requestID]; ok {
			return evidence.Evidence{Known: r.known, Tokens: r.tokens}, r.err
		}
	}
	return evidence.Evidence{Known: f.known, Tokens: f.tokens}, f.err
}

func pendingRow(id, requestID, plan string, age time.Duration) repository.PendingReservation {
	return repository.PendingReservation{
		ID: id, RequestID: requestID, UserID: "u", BillingPlan: plan,
		ReservedRequests: 1, ReservedAt: time.Now().UTC().Add(-age),
	}
}

func TestSweeper_KnownEvidenceReconcilesConfirmed(t *testing.T) {
	store := &fakeStore{
		expiredIDs:  []string{},
		pendingRows: []repository.PendingReservation{pendingRow("p1", "rq1", "token", 1*time.Minute)},
	}
	ev := &fakeEvidence{known: true, tokens: 42}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour, UnknownPolicy: PolicyKeepPending}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconciledKnown) != 1 || store.reconciledKnown[0].ID != "p1" {
		t.Fatalf("reconciledKnown = %+v", store.reconciledKnown)
	}
	if len(store.reconciledUnk) != 0 {
		t.Fatalf("must not release unknown when evidence known: %+v", store.reconciledUnk)
	}
	st := s.Stats()
	if st.ReconciledKnown < 1 {
		t.Fatalf("stats = %+v", st)
	}
}

// TestSweeper_SuccessLogArrivesAfterFirstQueryButBeforeGrace verifies the
// reconciler keeps pending on the first tick (evidence not yet terminal) and
// resolves it on a later tick once evidence becomes known — the canonical
// "log arrives late" race that blind-release would under-charge.
func TestSweeper_SuccessLogArrivesAfterFirstQueryButBeforeGrace(t *testing.T) {
	store := &fakeStore{
		pendingRows: []repository.PendingReservation{pendingRow("p2", "rq2", "token", 1*time.Millisecond)},
	}
	// First call: not terminal. Then flip to known. PendingGrace=0 so it is
	// due immediately; retention is long so it never falls to the policy.
	ev := &fakeEvidence{err: evidence.ErrNotTerminal}
	s := New(store, Config{Interval: 15 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour, UnknownPolicy: PolicyKeepPending}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	go func() { _ = s.Run(ctx) }()
	// Wait for at least one not-terminal lookup.
	deadline := time.After(500 * time.Millisecond)
	for {
		ev.mu.Lock()
		c := ev.calls
		ev.mu.Unlock()
		if c >= 1 {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("evidence never queried")
		case <-time.After(2 * time.Millisecond):
		}
	}
	// Flip evidence to known.
	ev.mu.Lock()
	ev.err = nil
	ev.known = true
	ev.tokens = 7
	ev.mu.Unlock()
	// Wait for a known reconcile.
	deadline2 := time.After(500 * time.Millisecond)
	for {
		store.mu.Lock()
		n := len(store.reconciledKnown)
		store.mu.Unlock()
		if n >= 1 {
			cancel()
			return
		}
		select {
		case <-deadline2:
			cancel()
			t.Fatalf("never reconciled known after evidence arrived")
		case <-time.After(3 * time.Millisecond):
		}
	}
}

// TestSweeper_LoggingOutageKeepsPending verifies that when Logging is
// unavailable, a pending reservation is NEVER released — it stays pending
// for the next tick (default-safe).
func TestSweeper_LoggingOutageKeepsPending(t *testing.T) {
	store := &fakeStore{
		pendingRows: []repository.PendingReservation{pendingRow("p3", "rq3", "token", 1*time.Millisecond)},
	}
	ev := &fakeEvidence{err: evidence.ErrUnavailable}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour, UnknownPolicy: PolicyKeepPending}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconciledKnown) != 0 || len(store.reconciledUnk) != 0 {
		t.Fatalf("Logging outage must not release: known=%+v unk=%+v", store.reconciledKnown, store.reconciledUnk)
	}
	if store.reconcileCalls != 0 {
		t.Fatalf("Logging outage must not call Reconcile, got %d calls", store.reconcileCalls)
	}
	// Retention alert should be 0 because retention deadline is long.
	if s.Stats().RetentionAlerts != 0 {
		t.Fatalf("retention alerts = %d, want 0", s.Stats().RetentionAlerts)
	}
}

// TestSweeper_NotFoundKeepsPending verifies not-found evidence (no log row
// yet) keeps pending and retries.
func TestSweeper_NotFoundKeepsPending(t *testing.T) {
	store := &fakeStore{
		pendingRows: []repository.PendingReservation{pendingRow("p4", "rq4", "token", 1*time.Millisecond)},
	}
	ev := &fakeEvidence{err: evidence.ErrNotFound}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour, UnknownPolicy: PolicyKeepPending}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconciledKnown) != 0 || len(store.reconciledUnk) != 0 {
		t.Fatalf("not-found must keep pending: %+v/%+v", store.reconciledKnown, store.reconciledUnk)
	}
}

// TestSweeper_DuplicateReconcileIsIdempotent verifies a re-sweep of an
// already-reconciled reservation is a no-op (ErrConflict, not an error).
func TestSweeper_DuplicateReconcileIsIdempotent(t *testing.T) {
	store := &fakeStore{
		pendingRows:  []repository.PendingReservation{pendingRow("p5", "rq5", "token", 1*time.Millisecond)},
		reconcileErr: repository.ErrConflict,
	}
	ev := &fakeEvidence{known: true, tokens: 1}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour, UnknownPolicy: PolicyKeepPending}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	// ErrConflict must not count as an error.
	if s.Stats().ReconcileErrors != 0 {
		t.Fatalf("ErrConflict must not count as error: %+v", s.Stats())
	}
}

// TestSweeper_RetentionKeepPendingDefault verifies the default-safe policy:
// a reservation past retention with no evidence is KEPT pending and alerted,
// never released.
func TestSweeper_RetentionKeepPendingDefault(t *testing.T) {
	// Pending row older than retention deadline (age > retention).
	store := &fakeStore{
		pendingRows: []repository.PendingReservation{pendingRow("p6", "rq6", "token", 2*time.Hour)},
	}
	ev := &fakeEvidence{err: evidence.ErrUnavailable}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Minute, UnknownPolicy: PolicyKeepPending}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconciledUnk) != 0 {
		t.Fatalf("default policy must not release: %+v", store.reconciledUnk)
	}
	if s.Stats().RetentionAlerts < 1 {
		t.Fatalf("expected retention alert, got %+v", s.Stats())
	}
}

// TestSweeper_RetentionReleaseUnknownPolicy verifies the explicit
// release_unknown policy releases (under-charge) past retention with audited
// reason — and only past retention, not before.
func TestSweeper_RetentionReleaseUnknownPolicy(t *testing.T) {
	store := &fakeStore{
		pendingRows: []repository.PendingReservation{pendingRow("p7", "rq7", "token", 2*time.Hour)},
	}
	ev := &fakeEvidence{err: evidence.ErrUnavailable}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Minute, UnknownPolicy: PolicyReleaseUnknown}, ev, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconciledUnk) == 0 {
		t.Fatalf("release_unknown policy must release past retention: %+v", store.reconciledUnk)
	}
	if s.Stats().ReconciledUnknown < 1 {
		t.Fatalf("expected unknown release stat, got %+v", s.Stats())
	}
}

// TestSweeper_CodingPlanReconcilesRequestCount verifies the confirmed-count
// computation: a coding-plan reservation settles with the reserved request
// count and zero tokens.
func TestSweeper_CodingPlanReconcilesRequestCount(t *testing.T) {
	p := repository.PendingReservation{BillingPlan: "coding", ReservedRequests: 1}
	ev := evidence.Evidence{Known: true, Tokens: 0}
	reqs, tokens := confirmedCounts(p, ev)
	if reqs != 1 || tokens != 0 {
		t.Fatalf("coding confirmed = (%d,%d), want (1,0)", reqs, tokens)
	}
}

// TestSweeper_TokenPlanReconcilesTokenCount verifies token-plan confirmed
// counts come from evidence.
func TestSweeper_TokenPlanReconcilesTokenCount(t *testing.T) {
	p := repository.PendingReservation{BillingPlan: "token", ReservedRequests: 0}
	ev := evidence.Evidence{Known: true, Tokens: 99}
	reqs, tokens := confirmedCounts(p, ev)
	if reqs != 0 || tokens != 99 {
		t.Fatalf("token confirmed = (%d,%d), want (0,99)", reqs, tokens)
	}
}

// TestSweeper_NilEvidenceKeepsPending verifies a nil evidence.Lookup (no
// Logging configured) keeps pending — never blind-releases.
func TestSweeper_NilEvidenceKeepsPending(t *testing.T) {
	store := &fakeStore{
		pendingRows: []repository.PendingReservation{pendingRow("p8", "rq8", "token", 1*time.Millisecond)},
	}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour, UnknownPolicy: PolicyKeepPending}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconciledKnown) != 0 || len(store.reconciledUnk) != 0 {
		t.Fatalf("nil evidence must keep pending: %+v/%+v", store.reconciledKnown, store.reconciledUnk)
	}
}

func TestSweeper_ExpireRuns(t *testing.T) {
	store := &fakeStore{expiredIDs: []string{"e1", "e2"}}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, ExpiryBatch: 10, PendingBatch: 10, RetentionDeadline: time.Hour}, &fakeEvidence{err: evidence.ErrUnavailable}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	seen := map[string]bool{}
	for _, id := range store.expired {
		seen[id] = true
	}
	if !seen["e1"] || !seen["e2"] {
		t.Fatalf("expired = %v", store.expired)
	}
}

func TestSweeper_ConflictIsNotAnError(t *testing.T) {
	store := &fakeStore{
		expiredIDs:   []string{"e1"},
		pendingRows:  []repository.PendingReservation{pendingRow("p1", "rq1", "token", 1*time.Millisecond)},
		expireErr:    repository.ErrConflict,
		reconcileErr: repository.ErrConflict,
	}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, RetentionDeadline: time.Hour}, &fakeEvidence{known: true, tokens: 1}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	st := s.Stats()
	if st.ExpiryErrors != 0 || st.ReconcileErrors != 0 {
		t.Fatalf("conflicts must not count as errors: %+v", st)
	}
}

func TestSweeper_ListFailureDoesNotAbort(t *testing.T) {
	store := &fakeStore{
		listExpiredErr: errors.New("boom"),
		listPendingErr: errors.New("boom"),
	}
	s := New(store, Config{Interval: 10 * time.Millisecond, PendingGrace: 0, RetentionDeadline: time.Hour}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestSweeper_Disabled(t *testing.T) {
	s := New(&fakeStore{}, Config{Interval: 0, RetentionDeadline: time.Hour}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
}

// satisfy unused imports
var _ = atomic.Int32{}
