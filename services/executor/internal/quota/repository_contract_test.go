package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func typedReservation(id ReservationID) Reservation {
	return Reservation{ID: id, Metadata: Metadata{RequestID: "req_123", Subject: "subject-1", KeyID: "key-1", Protocol: "openai", Model: "gpt-4o", InitialCandidate: "candidate-1", Revision: "revision-1", Generation: 1}, Estimate: Estimate{Basis: BasisNone}, State: ReservationReserved}
}
func typedID(n string) ReservationID { return ReservationID("res_" + n + "aaaaaaaaaaaaaaaa") }

// RepositoryContractTests verifies the public typed Repository contract.
func RepositoryContractTests(t *testing.T, newRepository func() Repository) {
	t.Helper()
	t.Run("reserve lookup and owned result", func(t *testing.T) {
		repo := newRepository()
		in := typedReservation(typedID("one"))
		got, err := repo.ReserveReservation(context.Background(), in)
		if err != nil || got.State != ReservationReserved || got.CreatedAt.IsZero() {
			t.Fatalf("ReserveReservation state=%q created=%t err=%v", got.State, !got.CreatedAt.IsZero(), err)
		}
		got.Metadata.Subject = "changed"
		stored, err := repo.Lookup(context.Background(), in.ID)
		if err != nil || stored.Metadata.Subject != in.Metadata.Subject {
			t.Fatalf("Lookup owned copy state=%q err=%v", stored.State, err)
		}
	})
	t.Run("reserve exact replay and metadata or estimate conflict", func(t *testing.T) {
		repo := newRepository()
		in := typedReservation(typedID("replay"))
		first, err := repo.ReserveReservation(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.ReserveReservation(context.Background(), in)
		if err != nil || first != second {
			t.Fatalf("replay state=%q err=%v", second.State, err)
		}
		changed := in
		changed.Metadata.Model = "other"
		if _, err := repo.ReserveReservation(context.Background(), changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("metadata conflict = %v", err)
		}
		changed = in
		changed.Estimate.Basis = "future"
		if _, err := repo.ReserveReservation(context.Background(), changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("estimate conflict = %v", err)
		}
	})
	t.Run("terminal exact replay conflict and lookup", func(t *testing.T) {
		repo := newRepository()
		in := typedReservation(typedID("terminal"))
		_, _ = repo.ReserveReservation(context.Background(), in)
		outcome := FinalizeOutcome{Disposition: AccountingConfirmedUsage, Usage: ConfirmedUsage{InputTokens: 7, OutputTokens: 3}}
		first, err := repo.FinalizeReservation(context.Background(), in.ID, outcome)
		if err != nil {
			t.Fatal(err)
		}
		second, err := repo.FinalizeReservation(context.Background(), in.ID, outcome)
		if err != nil || !sameSettlement(first.Settlement, second.Settlement) {
			t.Fatalf("finalize replay state=%q err=%v", second.State, err)
		}
		if _, err := repo.FinalizeReservation(context.Background(), in.ID, FinalizeOutcome{Disposition: AccountingUnpricedSuccess}); !errors.Is(err, ErrConflict) {
			t.Fatalf("different finalize = %v", err)
		}
		if _, err := repo.ReleaseReservation(context.Background(), in.ID, ReleaseFailed); !errors.Is(err, ErrConflict) {
			t.Fatalf("opposite terminal = %v", err)
		}
		stored, err := repo.Lookup(context.Background(), in.ID)
		if err != nil || stored.State != ReservationFinalized || stored.Settlement.Outcome == nil {
			t.Fatalf("Lookup terminal state=%q outcome=%t err=%v", stored.State, stored.Settlement.Outcome != nil, err)
		}
	})
	t.Run("release exact replay conflict", func(t *testing.T) {
		repo := newRepository()
		in := typedReservation(typedID("release"))
		_, _ = repo.ReserveReservation(context.Background(), in)
		if _, err := repo.ReleaseReservation(context.Background(), in.ID, ReleaseTimeout); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ReleaseReservation(context.Background(), in.ID, ReleaseTimeout); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ReleaseReservation(context.Background(), in.ID, ReleaseFailed); !errors.Is(err, ErrConflict) {
			t.Fatalf("different release = %v", err)
		}
	})
	t.Run("invalid inputs and precancel do not write", func(t *testing.T) {
		repo := newRepository()
		good := typedReservation(typedID("good"))
		for name, in := range map[string]Reservation{"id": func() Reservation { x := good; x.ID = "bad"; return x }(), "metadata": func() Reservation { x := good; x.Metadata.Subject = " space"; return x }(), "estimate": func() Reservation { x := good; x.Estimate.Basis = "future"; return x }()} {
			t.Run(name, func(t *testing.T) {
				if _, err := repo.ReserveReservation(context.Background(), in); err == nil {
					t.Fatal("ReserveReservation error = nil")
				}
			})
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := repo.ReserveReservation(ctx, good); !errors.Is(err, context.Canceled) {
			t.Fatalf("precancel reserve = %v", err)
		}
		if _, err := repo.Lookup(context.Background(), good.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("precancel wrote state: %v", err)
		}
		if _, err := repo.FinalizeReservation(context.Background(), good.ID, FinalizeOutcome{Disposition: AccountingUnpricedSuccess}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown finalize = %v", err)
		}
		if _, err := repo.Lookup(context.Background(), ReservationID("bad")); !errors.Is(err, ErrInvalidReservation) {
			t.Fatalf("invalid lookup = %v", err)
		}
	})
	t.Run("concurrent reserve one record", func(t *testing.T) {
		repo := newRepository()
		in := typedReservation(typedID("concurrent"))
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := repo.ReserveReservation(context.Background(), in); err != nil {
					t.Errorf("Reserve: %v", err)
				}
			}()
		}
		wg.Wait()
		if _, err := repo.Lookup(context.Background(), in.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestDomainInMemoryRepositoryContract(t *testing.T) {
	RepositoryContractTests(t, func() Repository { return NewDomainInMemory() })
}
func TestTypedMockRepositoryContract(t *testing.T) {
	RepositoryContractTests(t, func() Repository { return NewTypedMock() })
}

func TestTypedRepositoryFaultAfterCommit(t *testing.T) {
	for _, tc := range []struct {
		name string
		new  func() interface {
			Repository
			SetTypedFaultHook(TypedFaultHook)
		}
	}{
		{"in-memory", func() interface {
			Repository
			SetTypedFaultHook(TypedFaultHook)
		} {
			return NewDomainInMemory()
		}},
		{"mock", func() interface {
			Repository
			SetTypedFaultHook(TypedFaultHook)
		} {
			return NewTypedMock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := tc.new()
			in := typedReservation(typedID(tc.name))
			_, _ = repo.ReserveReservation(context.Background(), in)
			fault := errors.New("after commit")
			repo.SetTypedFaultHook(func(Reservation) error { return fault })
			outcome := FinalizeOutcome{Disposition: AccountingUnpricedSuccess}
			got, err := repo.FinalizeReservation(context.Background(), in.ID, outcome)
			if !errors.Is(err, fault) || got.State != ReservationFinalized {
				t.Fatalf("Finalize state=%q err=%v", got.State, err)
			}
			got, err = repo.FinalizeReservation(context.Background(), in.ID, outcome)
			if err != nil || got.State != ReservationFinalized {
				t.Fatalf("replay state=%q err=%v", got.State, err)
			}
		})
	}
}

func TestTypedMockCallsAndOverrides(t *testing.T) {
	mock := NewTypedMock()
	in := typedReservation(typedID("calls"))
	mock.SetReserveReservationFn(func(context.Context, Reservation) (Reservation, error) { return Reservation{}, fmt.Errorf("backend") })
	if _, err := mock.ReserveReservation(context.Background(), in); err == nil {
		t.Fatal("override error = nil")
	}
	calls := mock.TypedCalls()
	if len(calls) != 1 || calls[0].Method != "ReserveReservation" || calls[0].ID != in.ID {
		t.Fatalf("unexpected call count=%d method=%q", len(calls), func() string {
			if len(calls) == 0 {
				return ""
			}
			return calls[0].Method
		}())
	}
	calls[0].Method = "changed"
	if mock.TypedCalls()[0].Method == "changed" {
		t.Fatal("calls aliases internal slice")
	}
}
