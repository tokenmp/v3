package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/security/password"
)

// testEnv assembles a real auth.Service + handler with in-memory fakes and an
// in-memory Ed25519 key pair (no disk). It exposes helpers to drive HTTP
// requests against the auth routes and inspect the issued tokens.
type testEnv struct {
	svc      *auth.Service
	issuer   *jwt.Issuer
	verifier *jwt.Verifier
	users    *fakeStore
	sessions *fakeSessionStore
	clock    *fixedClock
	authH    *AuthHandler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pub, priv, err := ed25519GenerateKey()
	if err != nil {
		t.Fatalf("ed25519: %v", err)
	}
	kp := &jwt.KeyPair{Private: priv, Public: pub}
	issuer, err := jwt.NewIssuer(kp, "tokenmp-auth", "tokenmp-web", 15*time.Minute)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := jwt.NewVerifier(kp, "tokenmp-auth", "tokenmp-web")
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	users := newFakeStore()
	sessions := newFakeSessionStore()
	clock := &fixedClock{t: time.Now().UTC().Add(-1 * time.Minute)}
	svc := auth.NewService(users, sessions, fakeTxRunner{}, issuer, clock, 15*time.Minute, 30*24*time.Hour)
	return &testEnv{
		svc:      svc,
		issuer:   issuer,
		verifier: verifier,
		users:    users,
		sessions: sessions,
		clock:    clock,
		authH:    NewAuthHandler(svc),
	}
}

// fakeStore / fakeSessionStore / fakeTxRunner / fixedClock mirror the auth
// package fakes but live in the handler package for HTTP-level tests. They
// implement the same ports.
type fakeStore struct {
	byID  map[string]*models.User
	email map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: map[string]*models.User{}, email: map[string]string{}}
}

func (r *fakeStore) Create(ctx context.Context, u *models.User) error {
	if u.ID == "" {
		u.ID = newID()
	}
	if _, ok := r.email[u.Email]; ok {
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
	c := *u
	r.byID[c.ID] = &c
	r.email[c.Email] = c.ID
	*u = c
	return nil
}

func (r *fakeStore) FindByEmail(ctx context.Context, email string) (*models.User, error) {
	id, ok := r.email[email]
	if !ok {
		return nil, repository.ErrNotFound
	}
	c := *r.byID[id]
	return &c, nil
}

func (r *fakeStore) FindByID(ctx context.Context, id string) (*models.User, error) {
	u, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	c := *u
	return &c, nil
}

func (r *fakeStore) UpdatePasswordHash(ctx context.Context, userID, hash string) error {
	u, ok := r.byID[userID]
	if !ok {
		return repository.ErrNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (r *fakeStore) IncrementTokenVersion(ctx context.Context, userID string) (int, error) {
	u, ok := r.byID[userID]
	if !ok {
		return 0, repository.ErrNotFound
	}
	u.TokenVersion++
	return u.TokenVersion, nil
}

func (r *fakeStore) get(email string) *models.User {
	id, ok := r.email[email]
	if !ok {
		return nil
	}
	return r.byID[id]
}

type fakeSessionStore struct {
	byID   map[string]*models.AuthSession
	byHash map[string]*models.AuthSession
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{byID: map[string]*models.AuthSession{}, byHash: map[string]*models.AuthSession{}}
}

func (r *fakeSessionStore) Create(ctx context.Context, s *models.AuthSession) error {
	if s.ID == "" {
		s.ID = newID()
	}
	if s.TokenFamilyID == "" {
		s.TokenFamilyID = newID()
	}
	if len(s.RefreshTokenHash) == 0 {
		return repository.ErrConstraint
	}
	if _, ok := r.byHash[string(s.RefreshTokenHash)]; ok {
		return repository.ErrConstraint
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now().UTC()
	}
	c := *s
	r.byID[c.ID] = &c
	r.byHash[string(c.RefreshTokenHash)] = &c
	*s = c
	return nil
}

func (r *fakeSessionStore) FindByRefreshHashForUpdate(ctx context.Context, hash []byte) (*models.AuthSession, error) {
	s, ok := r.byHash[string(hash)]
	if !ok {
		return nil, repository.ErrNotFound
	}
	c := *s
	return &c, nil
}

func (r *fakeSessionStore) Revoke(ctx context.Context, id, reason string, at time.Time) error {
	s, ok := r.byID[id]
	if !ok {
		return repository.ErrNotFound
	}
	if s.RevokedAt != nil {
		return nil
	}
	s.RevokedAt = &at
	rc := reason
	s.RevokeReason = &rc
	return nil
}

func (r *fakeSessionStore) RevokeActiveByFamily(ctx context.Context, familyID, reason string, at time.Time) error {
	for _, s := range r.byID {
		if s.TokenFamilyID == familyID && s.RevokedAt == nil {
			s.RevokedAt = &at
			rc := reason
			s.RevokeReason = &rc
		}
	}
	return nil
}

func (r *fakeSessionStore) RevokeActiveByUser(ctx context.Context, userID, reason string, at time.Time) error {
	for _, s := range r.byID {
		if s.UserID == userID && s.RevokedAt == nil {
			s.RevokedAt = &at
			rc := reason
			s.RevokeReason = &rc
		}
	}
	return nil
}

func (r *fakeSessionStore) SetReplacedBy(ctx context.Context, oldID, newID string) error {
	s, ok := r.byID[oldID]
	if !ok {
		return repository.ErrNotFound
	}
	id := newID
	s.ReplacedBySessionID = &id
	return nil
}

func (r *fakeSessionStore) FindByID(ctx context.Context, id string) (*models.AuthSession, error) {
	s, ok := r.byID[id]
	if !ok {
		return nil, repository.ErrNotFound
	}
	c := *s
	return &c, nil
}

type fakeTxRunner struct{}

func (fakeTxRunner) Run(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

type fixedClock struct{ t time.Time }

func (f *fixedClock) Now() time.Time { return f.t }

// id helpers (deterministic, unique within a test process).
var idCounter int

func newID() string {
	idCounter++
	return formatID(idCounter)
}

func formatID(n int) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 32)
	for i := range out {
		out[i] = '0'
	}
	s := []byte{}
	for n > 0 {
		s = append([]byte{hex[n%16]}, s...)
		n /= 16
	}
	copy(out[len(out)-len(s):], s)
	out[12] = '4'
	out[16] = '8'
	return string(out)
}

func ed25519GenerateKey() (pub []byte, priv []byte, err error) {
	return generateEd25519Key()
}

// doJSON issues a request with a JSON body and returns the recorder and decoded
// body (generic) for status assertions.
func doJSON(t *testing.T, h http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r ioReader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type ioReader = interface {
	Read(p []byte) (n int, err error)
}

// decodeBody decodes the recorder body into out.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// dummy to avoid unused import in some configs.
var _ = strings.TrimSpace
var _ = password.Validate
