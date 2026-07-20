package configrepo

import (
	"context"
	"sync"
	"testing"
)

func TestInMemoryContract(t *testing.T) {
	ContractTests(t, func(snapshot Snapshot) Port {
		return NewInMemory(snapshot)
	})
}

func TestInMemorySetSnapshot(t *testing.T) {
	t.Parallel()

	repo := NewInMemory(Snapshot{HTTPAddr: "old"})
	repo.SetSnapshot(Snapshot{HTTPAddr: "new"})
	got, err := repo.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got.HTTPAddr != "new" {
		t.Errorf("HTTPAddr = %q, want %q", got.HTTPAddr, "new")
	}
}

func TestInMemoryConcurrentAccess(t *testing.T) {
	t.Parallel()

	repo := NewInMemory(Snapshot{HTTPAddr: "initial"})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			repo.SetSnapshot(Snapshot{HTTPAddr: "updated"})
		}()
		go func() {
			defer wg.Done()
			_, _ = repo.Snapshot(context.Background())
		}()
	}
	wg.Wait()
}
