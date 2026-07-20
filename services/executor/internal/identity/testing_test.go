package identity

// KeySeed pairs a raw API key (input only) with the identity it resolves to.
// It is a test-only seed value used to populate an InMemory store; the raw
// key is never persisted by InMemory, which stores only its salted digest.
type KeySeed struct {
	RawAPIKey string
	Identity  Identity
}

// newInMemorySeeded creates an InMemory identity port seeded with the given
// (raw API key, identity) pairs. Each raw key is reduced to a salted digest
// before being stored; the raw key is discarded. Test-only helper.
func newInMemorySeeded(seed []KeySeed) *InMemory {
	m := NewInMemory()
	for _, s := range seed {
		m.put(s.RawAPIKey, s.Identity)
	}
	return m
}

// put adds or updates an identity for the given raw API key. Safe for
// concurrent use. The raw key is reduced to a digest and not retained.
// Test-only mutator.
func (m *InMemory) put(rawAPIKey string, id Identity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[digestKey(rawAPIKey)] = id
}

// deleteKey removes the identity for the given raw API key. Safe for
// concurrent use. It is a no-op if the key is unknown. Test-only mutator.
func (m *InMemory) deleteKey(rawAPIKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, digestKey(rawAPIKey))
}

// has reports whether a raw API key is known to the store. Safe for concurrent
// use. Test-only helper.
func (m *InMemory) has(rawAPIKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.entries[digestKey(rawAPIKey)]
	return ok
}

// FixedTestKeys is a set of predefined test API key seeds for use in tests
// only. It is intentionally defined in a _test.go file so it is never compiled
// into production wiring. Each seed pairs a raw test API key (input only) with
// the identity it resolves to; the raw key is never persisted by InMemory.
var FixedTestKeys = []KeySeed{
	{
		RawAPIKey: "test-key-alice",
		Identity: Identity{
			Subject: "alice",
			KeyID:   "test-alice",
			Role:    RoleAdmin,
			Status:  StatusActive,
		},
	},
	{
		RawAPIKey: "test-key-bob",
		Identity: Identity{
			Subject: "bob",
			KeyID:   "test-bob",
			Role:    RoleService,
			Status:  StatusActive,
		},
	},
}

// NewInMemoryWithTestKeys creates an InMemory identity port seeded with
// FixedTestKeys. Test-only helper.
func NewInMemoryWithTestKeys() *InMemory {
	return newInMemorySeeded(FixedTestKeys)
}
