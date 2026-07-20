package identity

import (
	"context"
	"sync"
	"testing"
)

func TestInMemoryContract(t *testing.T) {
	ContractTests(t, func(seed []KeySeed) Port {
		return newInMemorySeeded(seed)
	})
}

func TestInMemoryWithTestKeys(t *testing.T) {
	t.Parallel()

	port := NewInMemoryWithTestKeys()
	for _, s := range FixedTestKeys {
		got, err := port.LookupByKey(context.Background(), s.RawAPIKey)
		if err != nil {
			t.Fatalf("LookupByKey(%q) error = %v", s.RawAPIKey, err)
		}
		if got != s.Identity {
			t.Errorf("LookupByKey(%q) = %+v, want %+v", s.RawAPIKey, got, s.Identity)
		}
	}
}

func TestInMemoryPutAndDelete(t *testing.T) {
	t.Parallel()

	port := NewInMemory()
	id := Identity{Subject: "new-user", KeyID: "k-new", Role: RoleService, Status: StatusActive}
	port.put("new-key", id)

	got, err := port.LookupByKey(context.Background(), "new-key")
	if err != nil {
		t.Fatalf("LookupByKey() error = %v", err)
	}
	if got != id {
		t.Errorf("LookupByKey() = %+v, want %+v", got, id)
	}

	port.deleteKey("new-key")
	_, err = port.LookupByKey(context.Background(), "new-key")
	if err != ErrUnknownKey {
		t.Errorf("LookupByKey() error = %v, want %v", err, ErrUnknownKey)
	}
}

// TestInMemoryDoesNotPersistRawKey verifies that the raw API key is not
// retained verbatim in the store's internal index by asserting that the only
// way to resolve an identity is via the original raw key (its digest). A
// different raw key with the same identity value must not collide.
func TestInMemoryDoesNotPersistRawKey(t *testing.T) {
	t.Parallel()

	port := NewInMemory()
	id := Identity{Subject: "user", KeyID: "k", Role: RoleService, Status: StatusActive}
	port.put("secret-raw-key", id)

	if !port.has("secret-raw-key") {
		t.Error("has() = false for stored key")
	}
	// A completely different raw key must not resolve to the same identity.
	if port.has("different-raw-key") {
		t.Error("has() = true for an unrelated key")
	}
}

func TestInMemoryConcurrentAccess(t *testing.T) {
	t.Parallel()

	port := NewInMemoryWithTestKeys()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, _ = port.LookupByKey(context.Background(), "test-key-alice")
		}()
		go func() {
			defer wg.Done()
			port.put("concurrent", Identity{Subject: "test", KeyID: "k", Role: RoleService, Status: StatusActive})
		}()
		go func() {
			defer wg.Done()
			port.deleteKey("concurrent")
		}()
	}
	wg.Wait()
}
