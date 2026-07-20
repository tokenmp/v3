package configrepo

import (
	"context"
	"sync"
)

// InMemory implements Port using an in-memory snapshot.
type InMemory struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

var _ Port = (*InMemory)(nil)

// NewInMemory creates an InMemory config repo with the given snapshot.
func NewInMemory(snapshot Snapshot) *InMemory {
	return &InMemory{snapshot: snapshot}
}

// Snapshot returns the stored configuration snapshot.
func (m *InMemory) Snapshot(_ context.Context) (Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot, nil
}

// SetSnapshot replaces the stored snapshot. Safe for concurrent use.
func (m *InMemory) SetSnapshot(snapshot Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = snapshot
}
