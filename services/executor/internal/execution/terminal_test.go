package execution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/quota"
)

func terminalID() quota.ReservationID { return "res_1234567890123456" }

func terminalReserve(t *testing.T, r quota.Repository) {
	t.Helper()
	_, err := r.ReserveReservation(context.Background(), quota.ReserveRequest{
		ID:       terminalID(),
		Metadata: quota.Metadata{RequestID: "req_1", Subject: "subject", KeyID: "key", Protocol: "openai", Model: "model", ProviderID: "provider", RouteID: "route", AdapterID: "adapter", Revision: "rev", Generation: 1},
		Estimate: quota.Estimate{Basis: quota.BasisNone},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTerminalizerReplaysOnlyExactFinalizeRequest(t *testing.T) {
	repo := quota.NewTypedMock()
	terminalReserve(t, repo)
	started := make(chan struct{})
	unblock := make(chan struct{})
	var got quota.FinalizeRequest
	repo.SetFinalizeReservationFn(func(_ context.Context, request quota.FinalizeRequest) (quota.Reservation, error) {
		got = request
		close(started)
		<-unblock
		return quota.Reservation{}, nil
	})
	term := NewTerminalizer(repo, terminalID())
	first := quota.FinalizeOutcome{Disposition: quota.AccountingConfirmedUsage, Outcome: quota.OutcomeCompleted, Usage: quota.ConfirmedUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}}
	firstDone := make(chan error, 1)
	go func() { firstDone <- term.Finalize(context.Background(), first) }()
	<-started

	sameDone := make(chan error, 1)
	go func() { sameDone <- term.Finalize(context.Background(), first) }()
	select {
	case err := <-sameDone:
		t.Fatalf("exact replay returned before first call completed: %v", err)
	default:
	}
	if err := term.Finalize(context.Background(), quota.FinalizeOutcome{Disposition: quota.AccountingConfirmedUsage, Outcome: quota.OutcomeCompleted, Usage: quota.ConfirmedUsage{InputTokens: 2, OutputTokens: 4, TotalTokens: 6}}); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("divergent finalize = %v, want %v", err, ErrTerminalConflict)
	}
	if err := term.Release(context.Background(), quota.ReleaseFailed); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("opposite release = %v, want %v", err, ErrTerminalConflict)
	}
	if calls := repo.TypedCalls(); len(calls) != 2 { // reserve + the first finalize only
		t.Fatalf("repository calls before unblock = %d, want 2", len(calls))
	}
	close(unblock)
	if err := <-firstDone; err != nil {
		t.Fatalf("first finalize = %v", err)
	}
	if err := <-sameDone; err != nil {
		t.Fatalf("exact finalize replay = %v", err)
	}
	if got.ID != terminalID() || got.Outcome != first {
		t.Fatalf("repository received %+v, want exact first request", got)
	}
	if calls := repo.TypedCalls(); len(calls) != 2 {
		t.Fatalf("repository calls = %d, want reserve + finalize", len(calls))
	}
}

func TestTerminalizerReplaysOnlyExactReleaseRequest(t *testing.T) {
	repo := quota.NewTypedMock()
	terminalReserve(t, repo)
	term := NewTerminalizer(repo, terminalID())
	if err := term.Release(context.Background(), quota.ReleaseTimeout); err != nil {
		t.Fatalf("first release = %v", err)
	}
	if err := term.Release(context.Background(), quota.ReleaseTimeout); err != nil {
		t.Fatalf("exact release replay = %v", err)
	}
	if err := term.Release(context.Background(), quota.ReleaseFailed); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("divergent release = %v, want %v", err, ErrTerminalConflict)
	}
	if err := term.Finalize(context.Background(), quota.FinalizeOutcome{Disposition: quota.AccountingUnpricedSuccess}); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("opposite finalize = %v, want %v", err, ErrTerminalConflict)
	}
	if calls := repo.TypedCalls(); len(calls) != 2 {
		t.Fatalf("repository calls = %d, want reserve + release", len(calls))
	}
}

func TestTerminalizerFaultAfterTransitionRetainsExactIntent(t *testing.T) {
	repo := quota.NewTypedMock()
	terminalReserve(t, repo)
	fault := errors.New("terminal response lost after transition")
	repo.SetTypedFaultHook(func(quota.Reservation) error { return fault })
	term := NewTerminalizer(repo, terminalID())
	outcome := quota.FinalizeOutcome{Disposition: quota.AccountingUnpricedSuccess, Outcome: quota.OutcomeCompleted}

	if err := term.Finalize(context.Background(), outcome); !errors.Is(err, fault) {
		t.Fatalf("Finalize = %v, want sticky fault", err)
	}
	if err := term.Finalize(context.Background(), outcome); !errors.Is(err, fault) {
		t.Fatalf("exact replay = %v, want sticky fault", err)
	}
	if err := term.Release(context.Background(), quota.ReleaseFailed); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("opposite terminal = %v", err)
	}
	stored, err := repo.Lookup(context.Background(), terminalID())
	if err != nil || stored.State != quota.ReservationFinalized {
		t.Fatalf("stored terminal state=%q err=%v", stored.State, err)
	}
	if calls := repo.TypedCalls(); len(calls) != 3 { // reserve, finalize, lookup
		t.Fatalf("repository calls=%d, want 3", len(calls))
	}
}

func TestTerminalizerReleaseUsesCancellationIndependentCleanupContext(t *testing.T) {
	type contextKey struct{}
	requestCtx, requestCancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-value"))
	requestCancel()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(requestCtx), time.Second)
	defer cleanupCancel()

	repo := quota.NewTypedMock()
	terminalReserve(t, repo)
	repo.SetReleaseReservationFn(func(ctx context.Context, in quota.ReleaseRequest) (quota.Reservation, error) {
		if err := ctx.Err(); err != nil {
			t.Errorf("cleanup context error = %v, want nil", err)
		}
		if got := ctx.Value(contextKey{}); got != "request-value" {
			t.Errorf("cleanup context value = %v, want request-value", got)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("cleanup context has no deadline")
		}
		if in.ID != terminalID() || in.Reason != quota.ReleaseCancelled {
			t.Errorf("release request = %+v", in)
		}
		return quota.Reservation{}, nil
	})
	if err := NewTerminalizer(repo, terminalID()).Release(cleanupCtx, quota.ReleaseCancelled); err != nil {
		t.Fatalf("Release(cleanupCtx) = %v", err)
	}
}

func TestTerminalizerConcurrentExactReplayRetainsFirstError(t *testing.T) {
	repo := quota.NewTypedMock()
	terminalReserve(t, repo)
	fault := errors.New("post-commit response lost")
	repo.SetTypedFaultHook(func(quota.Reservation) error { return fault })
	term := NewTerminalizer(repo, terminalID())
	request := quota.FinalizeOutcome{Disposition: quota.AccountingUnpricedSuccess}

	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- term.Finalize(context.Background(), request)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, fault) {
			t.Errorf("Finalize = %v, want sticky %v", err, fault)
		}
	}
	if calls := repo.TypedCalls(); len(calls) != 2 {
		t.Fatalf("repository calls = %d, want reserve + finalize", len(calls))
	}
}
