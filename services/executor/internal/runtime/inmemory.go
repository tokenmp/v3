package runtime

import (
	"context"
	"sync"
	"time"
)

// InMemory implements Port using in-memory runtime state.
type InMemory struct {
	mu          sync.RWMutex
	startTime   time.Time
	version     string
	quarantines map[RuntimeTarget]Quarantine
}

var _ Port = (*InMemory)(nil)

// NewInMemory creates an InMemory runtime port with the given version.
func NewInMemory(version string) *InMemory {
	return &InMemory{
		startTime:   time.Now(),
		version:     version,
		quarantines: make(map[RuntimeTarget]Quarantine),
	}
}

// Snapshot returns the current runtime state.
func (m *InMemory) Snapshot(_ context.Context) (Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	return Snapshot{
		StartTime: m.startTime,
		Version:   m.version,
		Uptime:    now.Sub(m.startTime),
	}, nil
}

// GetQuarantine returns the quarantine state for target.
func (m *InMemory) GetQuarantine(_ context.Context, target RuntimeTarget) (Quarantine, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	quarantine, ok := m.quarantines[target]
	if !ok {
		return Quarantine{}, ErrNotFound
	}
	return quarantine, nil
}

// SetQuarantine stores the quarantine state for input.Target. Safe for
// concurrent use.
func (m *InMemory) SetQuarantine(_ context.Context, input QuarantineInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quarantines[input.Target] = Quarantine{
		Target: input.Target,
		Until:  input.Until,
		Reason: input.Reason,
	}
	return nil
}

// SetVersion updates the version string. Safe for concurrent use.
func (m *InMemory) SetVersion(version string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.version = version
}
