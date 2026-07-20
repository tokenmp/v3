package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// keySalt is mixed into the raw API key before hashing so the digest stored as
// the map index is not a plain SHA-256 of the raw secret in memory. It is a
// process-local, non-secret value: its purpose is to avoid exposing trivially
// recognizable raw-key hashes and to keep the raw key out of the store.
const keySalt = "tokenmp-executor-identity-v1"

// digestKey returns the stable, non-reversible index for a raw API key. The
// raw key itself is never retained. This is the only other place, besides
// Port.LookupByKey, where a raw API key is accepted.
func digestKey(rawAPIKey string) string {
	sum := sha256.Sum256([]byte(keySalt + rawAPIKey))
	return hex.EncodeToString(sum[:])
}

// InMemory implements Port using an in-memory map keyed by a salted digest of
// the raw API key. The raw API key is never stored. Production code may only
// construct an empty store via NewInMemory and resolve identities via
// LookupByKey; seeding/mutation is available solely through test helpers.
type InMemory struct {
	mu      sync.RWMutex
	entries map[string]Identity
}

var _ Port = (*InMemory)(nil)

// NewInMemory creates an empty in-memory identity port. It accepts no raw API
// keys; seeding is test-only. The store indexes identities by a salted digest
// of the raw key, so the raw key is never retained.
func NewInMemory() *InMemory {
	return &InMemory{entries: make(map[string]Identity)}
}

// LookupByKey returns the identity for the given raw API key. This is the only
// production-facing method that accepts a raw API key; it is reduced to a
// salted digest and discarded.
func (m *InMemory) LookupByKey(_ context.Context, apiKey string) (Identity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.entries[digestKey(apiKey)]
	if !ok {
		return Identity{}, ErrUnknownKey
	}
	if id.Status != StatusActive {
		return Identity{}, ErrKeyDisabled
	}
	return id, nil
}
