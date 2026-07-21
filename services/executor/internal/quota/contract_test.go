package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/model"
)

// ContractTests runs the exhaustive quota reservation contract suite against
// any Port implementation.
func ContractTests(t *testing.T, newPort func() Port) {
	t.Helper()

	// --- Invalid ID rejection ---

	for _, id := range []string{"", " ", "\t\n"} {
		id := id
		t.Run("invalid ID rejects all operations without state transition "+fmt.Sprintf("%q", id), func(t *testing.T) {
			t.Parallel()
			port := newPort()
			counter, ok := port.(interface{ Count() int })
			if !ok {
				t.Fatal("Port must expose Count for invalid-ID state assertion")
			}
			for _, operation := range []struct {
				name string
				call func(context.Context, string) (model.Reservation, error)
			}{
				{name: "Reserve", call: port.Reserve},
				{name: "Finalize", call: port.Finalize},
				{name: "Release", call: port.Release},
			} {
				got, err := operation.call(context.Background(), id)
				if !errors.Is(err, ErrInvalidID) {
					t.Errorf("%s(%q) error = %v, want %v", operation.name, id, err, ErrInvalidID)
				}
				if got != (model.Reservation{}) {
					t.Errorf("%s(%q) reservation = %+v, want zero value", operation.name, id, got)
				}
			}
			if got := counter.Count(); got != 0 {
				t.Errorf("Count() after invalid operations = %d, want 0", got)
			}
		})
	}

	t.Run("concurrent invalid IDs do not transition existing state", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		counter, ok := port.(interface{ Count() int })
		if !ok {
			t.Fatal("Port must expose Count for invalid-ID state assertion")
		}
		if _, err := port.Reserve(context.Background(), "valid"); err != nil {
			t.Fatalf("Reserve(valid) error = %v", err)
		}

		var wg sync.WaitGroup
		errs := make(chan error, 300)
		for i := 0; i < 100; i++ {
			for _, call := range []func(context.Context, string) (model.Reservation, error){port.Reserve, port.Finalize, port.Release} {
				wg.Add(1)
				go func(call func(context.Context, string) (model.Reservation, error)) {
					defer wg.Done()
					_, err := call(context.Background(), " \t\n")
					errs <- err
				}(call)
			}
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if !errors.Is(err, ErrInvalidID) {
				t.Errorf("invalid ID error = %v, want %v", err, ErrInvalidID)
			}
		}
		if got := counter.Count(); got != 1 {
			t.Errorf("Count() after concurrent invalid operations = %d, want 1", got)
		}
		stored, err := port.Reserve(context.Background(), "valid")
		if err != nil || stored.Status != model.StatusReserved {
			t.Errorf("stored valid reservation = %+v, %v; want reserved", stored, err)
		}
	})

	// --- Reserve idempotency ---

	t.Run("reserve creates reservation", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		r, err := port.Reserve(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		if r.ID != "r1" {
			t.Errorf("ID = %q, want %q", r.ID, "r1")
		}
		if r.Status != model.StatusReserved {
			t.Errorf("Status = %q, want %q", r.Status, model.StatusReserved)
		}
	})

	t.Run("reserve is idempotent", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		r1, _ := port.Reserve(context.Background(), "r1")
		r2, err := port.Reserve(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		if r1 != r2 {
			t.Errorf("second Reserve() = %+v, want %+v", r2, r1)
		}
	})

	t.Run("reserve multiple different IDs", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		for _, id := range []string{"a", "b", "c"} {
			r, err := port.Reserve(context.Background(), id)
			if err != nil {
				t.Fatalf("Reserve(%q) error = %v", id, err)
			}
			if r.ID != id {
				t.Errorf("ID = %q, want %q", r.ID, id)
			}
		}
	})

	// --- Finalize idempotency ---

	t.Run("finalize transitions reserved to finalized", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		r, err := port.Finalize(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Finalize() error = %v", err)
		}
		if r.Status != model.StatusFinalized {
			t.Errorf("Status = %q, want %q", r.Status, model.StatusFinalized)
		}
	})

	t.Run("finalize same terminal is idempotent", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		r1, _ := port.Finalize(context.Background(), "r1")
		r2, err := port.Finalize(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Finalize() error = %v", err)
		}
		if r1 != r2 {
			t.Errorf("second Finalize() = %+v, want %+v", r2, r1)
		}
	})

	// --- Release idempotency ---

	t.Run("release transitions reserved to released", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		r, err := port.Release(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if r.Status != model.StatusReleased {
			t.Errorf("Status = %q, want %q", r.Status, model.StatusReleased)
		}
	})

	t.Run("release same terminal is idempotent", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		r1, _ := port.Release(context.Background(), "r1")
		r2, err := port.Release(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if r1 != r2 {
			t.Errorf("second Release() = %+v, want %+v", r2, r1)
		}
	})

	// --- Reserve after a terminal transition ---

	for _, terminal := range []struct {
		name   string
		status model.ReservationStatus
	}{
		{name: "finalized", status: model.StatusFinalized},
		{name: "released", status: model.StatusReleased},
	} {
		terminal := terminal
		t.Run("reserve returns existing "+terminal.name+" reservation", func(t *testing.T) {
			t.Parallel()
			port := newPort()
			if _, err := port.Reserve(context.Background(), "r1"); err != nil {
				t.Fatalf("Reserve() error = %v", err)
			}
			var err error
			switch terminal.status {
			case model.StatusFinalized:
				_, err = port.Finalize(context.Background(), "r1")
			case model.StatusReleased:
				_, err = port.Release(context.Background(), "r1")
			}
			if err != nil {
				t.Fatalf("terminal transition error = %v", err)
			}
			r, err := port.Reserve(context.Background(), "r1")
			if err != nil {
				t.Fatalf("Reserve() after %s error = %v", terminal.name, err)
			}
			if r.Status != terminal.status {
				t.Errorf("Reserve() status = %q, want %q", r.Status, terminal.status)
			}
		})
	}

	// --- Opposite terminal conflict ---

	t.Run("release after finalize is conflict", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		_, _ = port.Finalize(context.Background(), "r1")
		_, err := port.Release(context.Background(), "r1")
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Release() error = %v, want %v", err, ErrConflict)
		}
	})

	t.Run("finalize after release is conflict", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		_, _ = port.Release(context.Background(), "r1")
		_, err := port.Finalize(context.Background(), "r1")
		if !errors.Is(err, ErrConflict) {
			t.Errorf("Finalize() error = %v, want %v", err, ErrConflict)
		}
	})

	// --- Unknown ID not found ---

	t.Run("finalize unknown ID returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, err := port.Finalize(context.Background(), "unknown")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Finalize() error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("release unknown ID returns ErrNotFound", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, err := port.Release(context.Background(), "unknown")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Release() error = %v, want %v", err, ErrNotFound)
		}
	})

	// --- Concurrent safety ---

	t.Run("concurrent reserve same ID is safe", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = port.Reserve(context.Background(), "shared")
			}()
		}
		wg.Wait()
		r, err := port.Reserve(context.Background(), "shared")
		if err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		if r.Status != model.StatusReserved {
			t.Errorf("Status = %q, want %q", r.Status, model.StatusReserved)
		}
	})

	t.Run("concurrent same terminal finalize is safe", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = port.Finalize(context.Background(), "r1")
			}()
		}
		wg.Wait()
		r, err := port.Finalize(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Finalize() error = %v", err)
		}
		if r.Status != model.StatusFinalized {
			t.Errorf("Status = %q, want %q", r.Status, model.StatusFinalized)
		}
	})

	t.Run("concurrent same terminal release is safe", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = port.Release(context.Background(), "r1")
			}()
		}
		wg.Wait()
		r, err := port.Release(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Release() error = %v", err)
		}
		if r.Status != model.StatusReleased {
			t.Errorf("Status = %q, want %q", r.Status, model.StatusReleased)
		}
	})

	t.Run("concurrent opposite terminals have one winner and one conflict", func(t *testing.T) {
		t.Parallel()
		port := newPort()
		_, _ = port.Reserve(context.Background(), "r1")

		start := make(chan struct{})
		type result struct {
			status model.ReservationStatus
			err    error
		}
		results := make(chan result, 2)
		go func() {
			<-start
			r, err := port.Finalize(context.Background(), "r1")
			results <- result{status: r.Status, err: err}
		}()
		go func() {
			<-start
			r, err := port.Release(context.Background(), "r1")
			results <- result{status: r.Status, err: err}
		}()
		close(start)

		first, second := <-results, <-results
		winners := 0
		conflicts := 0
		var winner model.ReservationStatus
		for _, got := range []result{first, second} {
			switch {
			case got.err == nil && (got.status == model.StatusFinalized || got.status == model.StatusReleased):
				winners++
				winner = got.status
			case errors.Is(got.err, ErrConflict):
				conflicts++
			default:
				t.Errorf("terminal result = %+v, want winner or ErrConflict", got)
			}
		}
		if winners != 1 || conflicts != 1 {
			t.Fatalf("winners = %d, conflicts = %d; want exactly one of each", winners, conflicts)
		}

		// Reserve must return the reservation's stored terminal state rather
		// than recreating it as reserved after either terminal transition wins.
		stored, err := port.Reserve(context.Background(), "r1")
		if err != nil {
			t.Fatalf("Reserve() after terminal transition error = %v", err)
		}
		if stored.Status != winner {
			t.Errorf("Reserve() status = %q, want winning stored status %q", stored.Status, winner)
		}
	})
}
