package auth

import (
	"context"
	"sync"
	"time"

	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
)

// fakeClock returns a fixed time, advancing by step on each Now() call.
type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start, step: time.Second}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// fakeUserRepo is an in-memory UserRepository. It supports Create, FindByEmail,
// FindByID, UpdatePasswordHash and IncrementTokenVersion. Mutations are
// applied immediately (no transaction isolation) so a fake TxRunner that runs
// fn directly yields committed-by-default semantics matching the real GORM
// transaction when fn returns nil.
type fakeUserRepo struct {
	mu    sync.Mutex
	byID  map[string]*models.User
	email map[string]string // email → id
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: map[string]*models.User{}, email: map[string]string{}}
}

func (r *fakeUserRepo) Create(ctx context.Context, u *models.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u.ID == "" {
		u.ID = newFakeID()
	}
	if u.Email == "" {
		return repository.ErrConstraint
	}
	if _, exists := r.email[u.Email]; exists {
		return repository.ErrDuplicateEmail
	}
	if u.TokenVersion == 0 {
		u.TokenVersion = 1
	}
	if u.Role == "" {
		u.Role = models.RoleUser
	}
	if u.Status == "" {
		u.Status = models.StatusActive
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	clone := *u
	r.byID[clone.ID] = &clone
	r.email[clone.Email] = clone.ID
	*u = clone
	return nil
}

func (r *fakeUserRepo) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.email[email]
	if !ok {
		return nil, repository.ErrNotFound
	}
	u := *r.byID[id]
	return &u, nil
}

func (r *fakeUserRepo) FindByID(ctx context.Context, id string) (*models.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	clone := *u
	return &clone, nil
}

func (r *fakeUserRepo) UpdatePasswordHash(ctx context.Context, userID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byID[userID]
	if !ok {
		return repository.ErrNotFound
	}
	u.PasswordHash = hash
	u.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *fakeUserRepo) IncrementTokenVersion(ctx context.Context, userID string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byID[userID]
	if !ok {
		return 0, repository.ErrNotFound
	}
	u.TokenVersion++
	u.UpdatedAt = time.Now().UTC()
	return u.TokenVersion, nil
}

// fakeSessionRepo is an in-memory SessionRepository.
type fakeSessionRepo struct {
	mu     sync.Mutex
	byID   map[string]*models.AuthSession
	byHash map[string]*models.AuthSession // hex(RefreshTokenHash) → session
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{byID: map[string]*models.AuthSession{}, byHash: map[string]*models.AuthSession{}}
}

func (r *fakeSessionRepo) Create(ctx context.Context, s *models.AuthSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.ID == "" {
		s.ID = newFakeID()
	}
	if s.TokenFamilyID == "" {
		s.TokenFamilyID = newFakeID()
	}
	if len(s.RefreshTokenHash) == 0 {
		return repository.ErrConstraint
	}
	key := string(s.RefreshTokenHash)
	if _, exists := r.byHash[key]; exists {
		return repository.ErrConstraint
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = s.CreatedAt
	}
	clone := *s
	r.byID[clone.ID] = &clone
	r.byHash[key] = &clone
	*s = clone
	return nil
}

func (r *fakeSessionRepo) FindByRefreshHashForUpdate(ctx context.Context, hash []byte) (*models.AuthSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byHash[string(hash)]
	if !ok {
		return nil, repository.ErrNotFound
	}
	clone := *s
	return &clone, nil
}

func (r *fakeSessionRepo) Revoke(ctx context.Context, id, reason string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byID[id]
	if !ok {
		return repository.ErrNotFound
	}
	if s.RevokedAt != nil {
		return nil
	}
	s.RevokedAt = &at
	reasonCopy := reason
	s.RevokeReason = &reasonCopy
	s.UpdatedAt = at
	return nil
}

func (r *fakeSessionRepo) RevokeActiveByFamily(ctx context.Context, familyID, reason string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.byID {
		if s.TokenFamilyID == familyID && s.RevokedAt == nil {
			s.RevokedAt = &at
			rc := reason
			s.RevokeReason = &rc
			s.UpdatedAt = at
		}
	}
	return nil
}

func (r *fakeSessionRepo) RevokeActiveByUser(ctx context.Context, userID, reason string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.byID {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &at
			rc := reason
			s.RevokeReason = &rc
			s.UpdatedAt = at
		}
	}
	return nil
}

func (r *fakeSessionRepo) SetReplacedBy(ctx context.Context, oldID, newID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byID[oldID]
	if !ok {
		return repository.ErrNotFound
	}
	idCopy := newID
	s.ReplacedBySessionID = &idCopy
	s.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *fakeSessionRepo) FindByID(ctx context.Context, id string) (*models.AuthSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	clone := *s
	return &clone, nil
}

// get is a test helper returning the live pointer for a user by email.
func (r *fakeUserRepo) get(email string) *models.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.email[email]
	if !ok {
		return nil
	}
	return r.byID[id]
}

// snapshot returns the current set of active sessions for a family (for assertions).
func (r *fakeSessionRepo) snapshot() []*models.AuthSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*models.AuthSession, 0, len(r.byID))
	for _, s := range r.byID {
		c := *s
		out = append(out, &c)
	}
	return out
}

// fakeTxRunner runs fn directly with no isolation. Because the fakes apply
// mutations immediately, a fn that returns nil yields committed semantics —
// matching the real GORM transaction behavior the service relies on for the
// "reuse revocation must commit" contract.
type fakeTxRunner struct{}

func (fakeTxRunner) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

// id counter for fake ids.
var (
	idMu  sync.Mutex
	idCnt int
)

func newFakeID() string {
	idMu.Lock()
	defer idMu.Unlock()
	idCnt++
	// Produce a UUID-like deterministic string; uniqueness within a test is all
	// that is required (this is never written to a real DB).
	return formatFakeID(idCnt)
}

func formatFakeID(n int) string {
	// 32 hex chars with the n embedded.
	const hex = "0123456789abcdef"
	out := make([]byte, 32)
	for i := range out {
		out[i] = '0'
	}
	s := []byte(formatIntHex(n))
	copy(out[len(out)-len(s):], s)
	// Set version nibble (4) at position 12 to mimic UUID v4 shape.
	out[12] = '4'
	// Set variant nibble (8/9/a/b) at position 16.
	out[16] = '8'
	return string(out)
}

func formatIntHex(n int) string {
	if n == 0 {
		return "0"
	}
	const hex = "0123456789abcdef"
	out := []byte{}
	for n > 0 {
		out = append([]byte{hex[n%16]}, out...)
		n /= 16
	}
	return string(out)
}
