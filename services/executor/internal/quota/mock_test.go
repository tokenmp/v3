package quota

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/model"
)

func TestMockContract(t *testing.T) {
	ContractTests(t, func() Port { return NewMock() })
}

func TestMockCallRecording(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	_, _ = mock.Reserve(context.Background(), "r1")
	_, _ = mock.Finalize(context.Background(), "r1")
	_, _ = mock.Release(context.Background(), "r2") // conflict

	want := []CallRecord{
		{Method: "Reserve", ID: "r1"},
		{Method: "Finalize", ID: "r1"},
		{Method: "Release", ID: "r2"},
	}
	calls := mock.Calls()
	if len(calls) != len(want) {
		t.Fatalf("len(Calls()) = %d, want %d", len(calls), len(want))
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("Calls()[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

// TestMockCallsReturnsCopy verifies that Calls() returns a defensive copy so
// callers cannot alias or mutate the mock's internal record slice.
func TestMockInvalidIDCallsAreRecordedWithoutStateOrCallbackTransition(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	callbackCalls := 0
	mock.SetReserveFn(func(context.Context, string) (model.Reservation, error) {
		callbackCalls++
		return model.Reservation{}, nil
	})
	mock.SetFinalizeFn(func(context.Context, string) (model.Reservation, error) {
		callbackCalls++
		return model.Reservation{}, nil
	})
	mock.SetReleaseFn(func(context.Context, string) (model.Reservation, error) {
		callbackCalls++
		return model.Reservation{}, nil
	})

	for _, operation := range []struct {
		method string
		call   func(context.Context, string) (model.Reservation, error)
	}{
		{method: "Reserve", call: mock.Reserve},
		{method: "Finalize", call: mock.Finalize},
		{method: "Release", call: mock.Release},
	} {
		if _, err := operation.call(context.Background(), " \t "); !errors.Is(err, ErrInvalidID) {
			t.Errorf("%s invalid ID error = %v, want %v", operation.method, err, ErrInvalidID)
		}
	}
	if callbackCalls != 0 {
		t.Errorf("override callbacks = %d, want 0", callbackCalls)
	}
	if got := mock.Count(); got != 0 {
		t.Errorf("Count() = %d, want 0", got)
	}
	want := []CallRecord{
		{Method: "Reserve", ID: " \t "},
		{Method: "Finalize", ID: " \t "},
		{Method: "Release", ID: " \t "},
	}
	if got := mock.Calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("Calls() = %+v, want %+v", got, want)
	}
}

func TestMockCallsReturnsCopy(t *testing.T) {
	t.Parallel()

	mock := NewMock()
	_, _ = mock.Reserve(context.Background(), "r1")
	_, _ = mock.Finalize(context.Background(), "r1")

	first := mock.Calls()
	second := mock.Calls()
	if len(first) != len(second) {
		t.Fatalf("len mismatch: %d vs %d", len(first), len(second))
	}
	// Mutate the first returned copy; the second copy and subsequent reads must
	// be unaffected.
	first[0].Method = "MUTATED"
	if got := mock.Calls(); got[0].Method == "MUTATED" {
		t.Error("Calls() returned a reference to the internal slice")
	}
}
