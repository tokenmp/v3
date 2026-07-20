package identity

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestMockContract(t *testing.T) {
	ContractTests(t, func(seed []KeySeed) Port {
		return NewMockWith(WithLookupByKeyFn(func(_ context.Context, apiKey string) (Identity, error) {
			for _, s := range seed {
				if s.RawAPIKey == apiKey {
					if s.Identity.Status != StatusActive {
						return Identity{}, ErrKeyDisabled
					}
					return s.Identity, nil
				}
			}
			return Identity{}, ErrUnknownKey
		}))
	})
}

func TestMockLookupByKeyFn(t *testing.T) {
	t.Parallel()

	want := Identity{Subject: "s", KeyID: "k", Role: RoleService, Status: StatusActive}
	var called bool
	mock := NewMockWith(WithLookupByKeyFn(func(_ context.Context, _ string) (Identity, error) {
		called = true
		return want, nil
	}))
	got, err := mock.LookupByKey(context.Background(), "k")
	if err != nil {
		t.Fatalf("LookupByKey() error = %v", err)
	}
	if !called {
		t.Error("LookupByKeyFn not called")
	}
	if got != want {
		t.Errorf("LookupByKey() = %+v, want %+v", got, want)
	}
}

func TestMockLookupByKeyError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("lookup failed")
	mock := NewMockWith(WithLookupByKeyErr(sentinel))
	_, err := mock.LookupByKey(context.Background(), "k")
	if !errors.Is(err, sentinel) {
		t.Errorf("LookupByKey() error = %v, want %v", err, sentinel)
	}
}

func TestMockSettersOverride(t *testing.T) {
	t.Parallel()

	mock := NewMock()

	mock.SetLookupByKeyResult(Identity{Subject: "first", KeyID: "k1"})
	got, err := mock.LookupByKey(context.Background(), "any")
	if err != nil || got.Subject != "first" {
		t.Fatalf("first LookupByKey() = (%+v, %v), want first", got, err)
	}

	mock.SetLookupByKeyResult(Identity{Subject: "second", KeyID: "k2"})
	got, err = mock.LookupByKey(context.Background(), "any")
	if err != nil || got.Subject != "second" {
		t.Fatalf("second LookupByKey() = (%+v, %v), want second", got, err)
	}

	sentinel := errors.New("boom")
	mock.SetLookupByKeyErr(sentinel)
	if _, err := mock.LookupByKey(context.Background(), "any"); !errors.Is(err, sentinel) {
		t.Fatalf("err LookupByKey() error = %v, want %v", err, sentinel)
	}

	mock.SetLookupByKeyErr(nil)
	if got, err := mock.LookupByKey(context.Background(), "any"); err != nil || got.Subject != "second" {
		t.Fatalf("cleared LookupByKey() = (%+v, %v), want second", got, err)
	}
}

// TestMockConcurrentLookup verifies LookupByKey is safe for concurrent use when
// the mock is configured once up front and not reconfigured during the calls.
func TestMockConcurrentLookup(t *testing.T) {
	t.Parallel()

	want := Identity{Subject: "concurrent-subject", KeyID: "k", Role: RoleService, Status: StatusActive}
	mock := NewMockWith(WithLookupByKeyResult(want))

	const goroutines = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got, err := mock.LookupByKey(context.Background(), "concurrent-raw-key")
			if err != nil {
				errs <- err
				return
			}
			if got != want {
				errs <- fmt.Errorf("LookupByKey() = %+v, want %+v", got, want)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
