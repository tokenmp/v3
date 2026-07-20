package identity

import (
	"context"
	"errors"
	"testing"
)

// ContractTests runs the repository contract suite against any Port implementation.
func ContractTests(t *testing.T, newPort func(seed []KeySeed) Port) {
	t.Helper()

	t.Run("lookup existing key", func(t *testing.T) {
		t.Parallel()
		want := Identity{Subject: "user-1", KeyID: "k-1", Role: RoleService, Status: StatusActive}
		port := newPort([]KeySeed{{RawAPIKey: "key-1", Identity: want}})
		got, err := port.LookupByKey(context.Background(), "key-1")
		if err != nil {
			t.Fatalf("LookupByKey() error = %v", err)
		}
		if got != want {
			t.Errorf("LookupByKey() = %+v, want %+v", got, want)
		}
	})

	t.Run("lookup unknown key returns ErrUnknownKey", func(t *testing.T) {
		t.Parallel()
		port := newPort(nil)
		_, err := port.LookupByKey(context.Background(), "unknown")
		if !errors.Is(err, ErrUnknownKey) {
			t.Errorf("LookupByKey() error = %v, want %v", err, ErrUnknownKey)
		}
	})

	t.Run("lookup with empty key", func(t *testing.T) {
		t.Parallel()
		port := newPort(nil)
		_, err := port.LookupByKey(context.Background(), "")
		if !errors.Is(err, ErrUnknownKey) {
			t.Errorf("LookupByKey() error = %v, want %v", err, ErrUnknownKey)
		}
	})

	t.Run("lookup disabled returns ErrKeyDisabled", func(t *testing.T) {
		t.Parallel()
		disabled := Identity{Subject: "user-disabled", KeyID: "k-d", Role: RoleService, Status: StatusDisabled}
		port := newPort([]KeySeed{{RawAPIKey: "key-disabled", Identity: disabled}})
		_, err := port.LookupByKey(context.Background(), "key-disabled")
		if !errors.Is(err, ErrKeyDisabled) {
			t.Errorf("LookupByKey() error = %v, want %v", err, ErrKeyDisabled)
		}
	})

	t.Run("lookup with multiple identities", func(t *testing.T) {
		t.Parallel()
		seed := []KeySeed{
			{RawAPIKey: "a", Identity: Identity{Subject: "alpha", KeyID: "ka", Role: RoleService, Status: StatusActive}},
			{RawAPIKey: "b", Identity: Identity{Subject: "beta", KeyID: "kb", Role: RoleAdmin, Status: StatusActive}},
		}
		port := newPort(seed)
		for _, s := range seed {
			got, err := port.LookupByKey(context.Background(), s.RawAPIKey)
			if err != nil {
				t.Fatalf("LookupByKey(%q) error = %v", s.RawAPIKey, err)
			}
			if got != s.Identity {
				t.Errorf("LookupByKey(%q) = %+v, want %+v", s.RawAPIKey, got, s.Identity)
			}
		}
	})
}
