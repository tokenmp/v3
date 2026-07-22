package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDomainInMemoryCancellationWhileWaitingForLockDoesNotReadOrWrite(t *testing.T) {
	repo := NewDomainInMemory()
	in := typedReservation(typedID("lock-terminal"))
	if _, err := repo.ReserveReservation(context.Background(), in); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		call  func(context.Context) (Reservation, error)
		check func(t *testing.T)
	}{
		{
			name: "finalize",
			call: func(ctx context.Context) (Reservation, error) {
				return repo.FinalizeReservation(ctx, in.ID, FinalizeOutcome{Disposition: AccountingUnpricedSuccess})
			},
			check: func(t *testing.T) {
				t.Helper()
				got, err := repo.Lookup(context.Background(), in.ID)
				if err != nil || got.State != ReservationReserved {
					t.Fatalf("terminal cancellation changed state=%q err=%v", got.State, err)
				}
			},
		},
		{
			name: "reserve",
			call: func(ctx context.Context) (Reservation, error) {
				return repo.ReserveReservation(ctx, typedReservation(typedID("lock-reserve")))
			},
			check: func(t *testing.T) {
				t.Helper()
				if repo.Count() != 1 {
					t.Fatalf("cancelled reserve changed count=%d", repo.Count())
				}
			},
		},
		{
			name: "lookup",
			call: func(ctx context.Context) (Reservation, error) { return repo.Lookup(ctx, in.ID) },
			check: func(t *testing.T) {
				t.Helper()
				if repo.Count() != 1 {
					t.Fatalf("cancelled lookup changed count=%d", repo.Count())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo.mu.Lock()
			base, cancel := context.WithCancel(context.Background())
			ctx := &firstCheckContext{Context: base, checked: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				_, err := tc.call(ctx)
				result <- err
			}()
			// The initial cancellation check has passed, while mu remains held.
			// Cancellation therefore must be observed by the post-lock recheck.
			<-ctx.checked
			cancel()
			repo.mu.Unlock()

			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("call error=%v, want context cancellation", err)
				}
			case <-time.After(time.Second):
				t.Fatal("blocked call did not return")
			}
			tc.check(t)
		})
	}
}

// firstCheckContext makes the lock-contention tests prove the second context
// check rather than merely the pre-call check.
type firstCheckContext struct {
	context.Context
	checked chan struct{}
	calls   atomic.Uint32
}

func (c *firstCheckContext) Err() error {
	if c.calls.Add(1) == 1 {
		close(c.checked)
		return nil
	}
	return c.Context.Err()
}

func TestTypedMockPrecancelDoesNotCallOverride(t *testing.T) {
	mock := NewTypedMock()
	in := typedReservation(typedID("mock-cancel"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	mock.SetReserveReservationFn(func(context.Context, Reservation) (Reservation, error) {
		called = true
		return Reservation{}, nil
	})
	if _, err := mock.ReserveReservation(ctx, in); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("reserve err=%v override-called=%t", err, called)
	}
	mock.SetFinalizeReservationFn(func(context.Context, ReservationID, FinalizeOutcome) (Reservation, error) {
		called = true
		return Reservation{}, nil
	})
	if _, err := mock.FinalizeReservation(ctx, in.ID, FinalizeOutcome{Disposition: AccountingUnpricedSuccess}); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("finalize err=%v override-called=%t", err, called)
	}
	mock.SetReleaseReservationFn(func(context.Context, ReservationID, ReleaseReason) (Reservation, error) {
		called = true
		return Reservation{}, nil
	})
	if _, err := mock.ReleaseReservation(ctx, in.ID, ReleaseFailed); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("release err=%v override-called=%t", err, called)
	}
	mock.SetLookupFn(func(context.Context, ReservationID) (Reservation, error) {
		called = true
		return Reservation{}, nil
	})
	if _, err := mock.Lookup(ctx, in.ID); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("lookup err=%v override-called=%t", err, called)
	}
}

func TestTypedDomainFormattingIsRedactedForAllVerbs(t *testing.T) {
	const marker = "sensitive-id-subject-model-units"
	id := ReservationID("res_" + marker + "aaaaaaaaaaaaaaaa")
	record := typedReservation(id)
	record.Metadata.Subject = marker
	record.Metadata.Model = marker
	record.Settlement = TerminalSettlement{Outcome: &FinalizeOutcome{Disposition: AccountingConfirmedUsage, Usage: ConfirmedUsage{InputTokens: 123, OutputTokens: 456}}}
	call := TypedCallRecord{Method: "ReserveReservation", ID: id}
	mock := NewTypedMock()

	for _, value := range []any{record, id, record.Metadata, record.Estimate, *record.Settlement.Outcome, record.Settlement, call, mock, NewDomainInMemory()} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
			got := fmt.Sprintf(verb, value)
			if strings.Contains(got, marker) || strings.Contains(got, "123") || strings.Contains(got, "456") {
				t.Fatalf("format %q leaked sensitive value", verb)
			}
		}
	}
}

func TestTypedFaultHookReceivesRecordButFormattingItIsRedacted(t *testing.T) {
	const marker = "hook-sensitive-subject"
	repo := NewDomainInMemory()
	in := typedReservation(typedID("hook"))
	in.Metadata.Subject = marker
	if _, err := repo.ReserveReservation(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	seen := false
	repo.SetTypedFaultHook(func(r Reservation) error {
		seen = r.Metadata.Subject == marker
		if strings.Contains(fmt.Sprintf("%+v", r), marker) {
			t.Fatal("fault hook formatting leaked metadata")
		}
		return nil
	})
	if _, err := repo.FinalizeReservation(context.Background(), in.ID, FinalizeOutcome{Disposition: AccountingUnpricedSuccess}); err != nil || !seen {
		t.Fatalf("finalize err=%v hook-seen=%t", err, seen)
	}
}
