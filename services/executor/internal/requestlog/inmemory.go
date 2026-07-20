package requestlog

import (
	"context"
	"sync"
)

// InMemory implements Port using an in-memory slice.
type InMemory struct {
	mu      sync.Mutex
	entries []CallEntry
}

var _ Port = (*InMemory)(nil)

// NewInMemory creates an empty InMemory request log.
func NewInMemory() *InMemory {
	return &InMemory{}
}

// Record appends an entry to the log.
func (m *InMemory) Record(_ context.Context, entry CallEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

// Calls returns all recorded entries in order.
func (m *InMemory) Calls(_ context.Context) []CallEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]CallEntry, len(m.entries))
	copy(result, m.entries)
	return result
}
