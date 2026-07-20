// Package snapshot provides atomic publication of immutable compiled configuration.
package snapshot

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

var (
	// ErrNoSnapshot is returned until a successfully compiled snapshot is
	// published. Its identity is stable so callers can reliably classify an
	// Executor that has not become ready yet.
	ErrNoSnapshot = errors.New("compiled snapshot unavailable")

	// ErrInvalidSnapshot is returned when a caller tries to publish a nil
	// snapshot, a snapshot with blank revision, or zero generation. Invalid
	// publications never replace the last known good value.
	ErrInvalidSnapshot = errors.New("invalid compiled snapshot")

	// ErrStaleSnapshot is returned when Publish is called with a generation
	// that does not exceed the current generation. This prevents an older
	// concurrent publisher from overwriting a newer snapshot.
	ErrStaleSnapshot = errors.New("stale snapshot generation")
)

// CompiledSnapshot holds a frozen compiled configuration with a monotonic
// generation for publisher ordering. Construct only via NewCompiledSnapshot.
//
// The value is stored as a concrete CompiledConfig (not a pointer) so
// typed-nil issues cannot arise. Every read via Value returns an independent
// deep copy.
type CompiledSnapshot struct {
	revision   string
	value      CompiledConfig // concrete value prevents typed-nil
	generation uint64
}

// NewCompiledSnapshot freezes a compiled configuration for publication.
// revision must be non-blank; cfg must be non-nil with non-blank Revision;
// external revision must match cfg.Revision; generation must be > 0.
// The config is deep-copied immediately so later mutations to the caller's
// input cannot affect the snapshot.
func NewCompiledSnapshot(revision string, cfg *CompiledConfig, generation uint64) (*CompiledSnapshot, error) {
	rev := strings.TrimSpace(revision)
	if rev == "" || cfg == nil || strings.TrimSpace(cfg.Revision) == "" || generation == 0 {
		return nil, ErrInvalidSnapshot
	}
	if rev != strings.TrimSpace(cfg.Revision) {
		return nil, ErrInvalidSnapshot
	}
	return &CompiledSnapshot{
		revision:   rev,
		value:      adapter.CloneCompiledConfig(*cfg),
		generation: generation,
	}, nil
}

// Revision returns the immutable source revision for this compiled snapshot.
func (s *CompiledSnapshot) Revision() string {
	if s == nil {
		return ""
	}
	return s.revision
}

// Value returns an independent deep copy of the compiled config as a pointer
// so callers can safely mutate the returned value without affecting this
// snapshot or any concurrent reader. Returns nil for a nil receiver.
func (s *CompiledSnapshot) Value() *CompiledConfig {
	if s == nil {
		return nil
	}
	cloned := adapter.CloneCompiledConfig(s.value)
	return &cloned
}

// Generation returns the monotonic generation number assigned at construction.
func (s *CompiledSnapshot) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.generation
}

// storeEntry is the Store's internal retained snapshot with its generation.
type storeEntry struct {
	config     CompiledConfig
	revision   string
	generation uint64
}

// Store atomically publishes complete compiled snapshots. It retains the last
// known good snapshot: a failed, invalid, or stale publication never replaces
// the current value. Current returns an independent deep-cloned view.
//
// Publisher ordering: a Publish with generation <= the current generation is
// rejected with ErrStaleSnapshot, so an older concurrent publisher cannot
// overwrite a newer snapshot.
type Store struct {
	mu      sync.Mutex
	current atomic.Pointer[storeEntry]
}

// Publish atomically makes the snapshot current if its generation exceeds the
// current generation. Returns ErrStaleSnapshot if generation <= current.
// Returns ErrInvalidSnapshot for nil or invalid snapshots. The published
// snapshot is deep-copied; the caller's pointer is not retained.
func (s *Store) Publish(snapshot *CompiledSnapshot) error {
	if snapshot == nil {
		return ErrInvalidSnapshot
	}
	if strings.TrimSpace(snapshot.revision) == "" || snapshot.generation == 0 {
		return ErrInvalidSnapshot
	}
	if strings.TrimSpace(snapshot.value.Revision) == "" || strings.TrimSpace(snapshot.value.Revision) != strings.TrimSpace(snapshot.revision) {
		return ErrInvalidSnapshot
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	old := s.current.Load()
	if old != nil && snapshot.generation <= old.generation {
		return ErrStaleSnapshot
	}

	entry := &storeEntry{
		config:     adapter.CloneCompiledConfig(snapshot.value),
		revision:   snapshot.revision,
		generation: snapshot.generation,
	}
	s.current.Store(entry)
	return nil
}

// Current returns an independent deep-cloned view of the latest published
// snapshot. Before the first successful publication it returns ErrNoSnapshot.
// The returned snapshot is independent of Store ownership, so it remains a
// stable old-revision view after a later Publish.
func (s *Store) Current() (*CompiledSnapshot, error) {
	entry := s.current.Load()
	if entry == nil {
		return nil, ErrNoSnapshot
	}
	return &CompiledSnapshot{
		revision:   entry.revision,
		value:      adapter.CloneCompiledConfig(entry.config),
		generation: entry.generation,
	}, nil
}

// Generation returns the current generation counter, or 0 if no snapshot has
// been published. This is useful for callers that want to assign the next
// generation for a new snapshot.
func (s *Store) Generation() uint64 {
	entry := s.current.Load()
	if entry == nil {
		return 0
	}
	return entry.generation
}
