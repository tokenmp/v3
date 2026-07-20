package authv1api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tokenmp/v3/services/auth/internal/auth"
	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/security/password"
)

// ---------------------------------------------------------------------------
// Test environment
// ---------------------------------------------------------------------------

type testEnv struct {
	svc      *auth.Service
	issuer   *jwt.Issuer
	verifier *jwt.Verifier
	users    *fakeStore
	sessions *fakeSessionStore
	clock    *fixedClock
	adapter  *StrictAdapter
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
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
	adapter := NewStrictAdapter(svc, fakePinger{}, 15*time.Minute)
	return &testEnv{
		svc:      svc,
		issuer:   issuer,
		verifier: verifier,
		users:    users,
		sessions: sessions,
		clock:    clock,
		adapter:  adapter,
	}
}

func (e *testEnv) routerWithStore(t *testing.T) http.Handler {
	t.Helper()
	store := &envUserStore{e: e}
	middlewares := []authv1.StrictMiddlewareFunc{}
	if e.verifier != nil && store != nil {
		middlewares = append(middlewares, bearerMiddleware(e.verifier, store))
	}
	strictHandler := authv1.NewStrictHandlerWithOptions(e.adapter, middlewares, authv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestErrorHandler,
		ResponseErrorHandlerFunc: strictResponseErrorHandler,
	})
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cacheControlNoStoreMiddleware())
	r.Use(bodyPreDecodeMiddleware())
	r.Use(clientMetaMiddleware())
	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{
		BaseRouter: r,
	})
	return r
}

// envUserStore adapts the in-memory fakeStore into the UserStore interface.
type envUserStore struct{ e *testEnv }

func (s *envUserStore) FindByID(ctx context.Context, id string) (string, int, string, error) {
	u, ok := s.e.users.byID[id]
	if !ok {
		return "", 0, "", errNotFound
	}
	return string(u.Status), u.TokenVersion, string(u.Role), nil
}

var errNotFound = notFoundErr{}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "not found" }

// fakePinger always returns ready.
type fakePinger struct{}

func (fakePinger) Ping(_ context.Context) error { return nil }

// ---------------------------------------------------------------------------
// In-memory fakes
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func doJSON(t *testing.T, h http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r interface {
		Read(p []byte) (n int, err error)
	}
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

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestRegister_201Contract(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "User@Example.com   ", "password": "verystrongpassword123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	decodeBody(t, rec, &out)
	for _, k := range []string{"id", "email", "role", "status", "created_at"} {
		if _, ok := out[k]; !ok {
			t.Errorf("missing field %q in register response: %v", k, out)
		}
	}
	if out["email"] != "user@example.com" {
		t.Errorf("email=%v want normalized", out["email"])
	}
	if _, ok := out["password_hash"]; ok {
		t.Error("password_hash leaked in response")
	}
	if _, ok := out["token_version"]; ok {
		t.Error("token_version leaked in response")
	}
	if _, ok := out["access_token"]; ok {
		t.Error("register must not auto-login")
	}
	if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("content-type=%q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("cache-control=%q", rec.Header().Get("Cache-Control"))
	}
}

func TestRegister_Duplicate409(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	body := map[string]string{"email": "dup@example.com", "password": "verystrongpassword123"}
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", body)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409", rec.Code)
	}
	var e map[string]any
	decodeBody(t, rec, &e)
	errObj := e["error"].(map[string]any)
	if errObj["code"] != "email_taken" {
		t.Errorf("code=%v want email_taken", errObj["code"])
	}
	if strings.Contains(rec.Body.String(), "dup@example.com") {
		t.Error("email echoed in error body")
	}
}

func TestRegister_WeakPassword400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "x@example.com", "password": "short",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestRegister_InvalidBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestLogin_200Contract(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "USER@example.com  ", "password": "verystrongpassword123",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	decodeBody(t, rec, &out)
	for _, k := range []string{"access_token", "refresh_token", "token_type", "expires_in"} {
		if _, ok := out[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
	if out["token_type"] != "Bearer" {
		t.Errorf("token_type=%v", out["token_type"])
	}
	if out["expires_in"] != float64(900) {
		t.Errorf("expires_in=%v want 900", out["expires_in"])
	}
}

func TestLogin_InvalidCredentialsUniform401(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	cases := []struct {
		name, email, pw string
	}{
		{"wrong pw", "user@example.com", "wrongpw"},
		{"unknown user", "nope@example.com", "verystrongpassword123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
				"email": c.email, "password": c.pw,
			})
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d want 401", rec.Code)
			}
			var e map[string]any
			decodeBody(t, rec, &e)
			errObj := e["error"].(map[string]any)
			if errObj["code"] != "invalid_credentials" {
				t.Errorf("code=%v want invalid_credentials", errObj["code"])
			}
			body := rec.Body.String()
			for _, n := range []string{c.pw, c.email} {
				if strings.Contains(body, n) {
					t.Errorf("body leaked %q: %s", n, body)
				}
			}
		})
	}
}

func TestRefresh_200Rotation(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, rec, &login)
	rec2 := doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{
		"refresh_token": login["refresh_token"].(string),
	})
	if rec2.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec2.Code, rec2.Body)
	}
	var out map[string]any
	decodeBody(t, rec2, &out)
	if out["refresh_token"] == login["refresh_token"] {
		t.Error("refresh token not rotated")
	}
}

func TestRefresh_Reuse401(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, rec, &login)
	old := login["refresh_token"].(string)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": old})
	rec2 := doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": old})
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec2.Code)
	}
	var e map[string]any
	decodeBody(t, rec2, &e)
	errObj := e["error"].(map[string]any)
	if errObj["code"] != "invalid_refresh_token" {
		t.Errorf("code=%v want invalid_refresh_token", errObj["code"])
	}
}

func TestLogout_Idempotent204(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, rec, &login)
	rt := login["refresh_token"].(string)
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": rt}); rec.Code != http.StatusNoContent {
		t.Errorf("logout status=%d want 204", rec.Code)
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": rt}); rec.Code != http.StatusNoContent {
		t.Errorf("idempotent logout status=%d want 204", rec.Code)
	}
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": "totally-bogus"}); rec.Code != http.StatusNoContent {
		t.Errorf("unknown logout status=%d want 204", rec.Code)
	}
}

func TestMe_RequiresBearer401(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestMe_WithBearer200(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, err := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", access, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	decodeBody(t, rec, &out)
	if out["email"] != "user@example.com" {
		t.Errorf("email=%v", out["email"])
	}
}

func TestMe_BumpVersionInvalidatesAccess(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	env.users.IncrementTokenVersion(nil, u.ID)
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", access, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale access status=%d want 401", rec.Code)
	}
}

func TestMe_DisabledAccount401(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	u.Status = "disabled"
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", access, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled status=%d want 401", rec.Code)
	}
	var e map[string]any
	decodeBody(t, rec, &e)
	errObj := e["error"].(map[string]any)
	if errObj["code"] == "account_disabled" {
		t.Error("disabled account leaked account_disabled code in response")
	}
	if errObj["code"] != "invalid_token" {
		t.Errorf("code=%v want invalid_token", errObj["code"])
	}
}

func TestMe_TamperedToken401(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", "not.a.real.token", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

func TestPasswordChange_204RevokesAndBumps(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	rec := doJSON(t, r, http.MethodPut, "/api/v1/auth/password", access, map[string]string{
		"current_password": "verystrongpassword123", "new_password": "newverystrongpassword456",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	rec2 := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", access, nil)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("stale access after pw change status=%d want 401", rec2.Code)
	}
}

func TestPasswordChange_WrongCurrent401(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	rec := doJSON(t, r, http.MethodPut, "/api/v1/auth/password", access, map[string]string{
		"current_password": "wrongcurrent", "new_password": "newverystrongpassword456",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

func TestLogoutAll_RequiresBearer(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout-all", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestLogoutAll_204(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout-all", access, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
	rec2 := doJSON(t, r, http.MethodGet, "/api/v1/auth/me", access, nil)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("stale access after logout-all status=%d want 401", rec2.Code)
	}
}

func TestErrorResponseShape(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "nobody@example.com", "password": "whateverpassword",
	})
	var e map[string]any
	decodeBody(t, rec, &e)
	errObj := e["error"].(map[string]any)
	if errObj["code"] == "" || errObj["message"] == "" {
		t.Errorf("error body missing code/message: %+v", e)
	}
	if strings.Contains(rec.Body.String(), "whateverpassword") {
		t.Error("password leaked in error body")
	}
}

func TestHealthz_Get(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type=%q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q", cc)
	}
	var resp map[string]any
	decodeBody(t, rec, &resp)
	if resp["status"] != "ok" {
		t.Errorf("status=%v", resp["status"])
	}
	if resp["service"] != "auth" {
		t.Errorf("service=%v", resp["service"])
	}
}

func TestHealthz_HeadNoBody(t *testing.T) {
	env := newTestEnv(t)
	assertHeadHealthResponse(t, env.routerWithStore(t), "/healthz", http.StatusOK)
}

func TestReadyz_GetReady(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

func TestReadyz_HeadReadyNoBody(t *testing.T) {
	env := newTestEnv(t)
	assertHeadHealthResponse(t, env.routerWithStore(t), "/readyz", http.StatusOK)
}

func TestReadyz_GetUnready503NoLeak(t *testing.T) {
	// Build a server with a failing pinger.
	unreadyPinger := &unreadyPinger{}
	adapter := NewStrictAdapter(nil, unreadyPinger, 15*time.Minute)
	strictHandler := authv1.NewStrictHandlerWithOptions(adapter, nil, authv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestErrorHandler,
		ResponseErrorHandlerFunc: strictResponseErrorHandler,
	})
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cacheControlNoStoreMiddleware(), bodyPreDecodeMiddleware(), clientMetaMiddleware())
	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{BaseRouter: r})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	var resp map[string]any
	decodeBody(t, rec, &resp)
	if resp["status"] != "unready" {
		t.Errorf("status=%v want unready", resp["status"])
	}
	body := rec.Body.String()
	for _, needle := range []string{"secret", "password", "host", "db.internal", "connection refused"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(needle)) {
			t.Errorf("response leaked underlying error text %q", needle)
		}
	}
}

type unreadyPinger struct{}

func (unreadyPinger) Ping(_ context.Context) error {
	return errors.New("pq: connection refused (password=secret) host=db.internal")
}

func TestReadyz_HeadUnready503NoBodyNoLeak(t *testing.T) {
	adapter := NewStrictAdapter(nil, unreadyPinger{}, 15*time.Minute)
	strictHandler := authv1.NewStrictHandlerWithOptions(adapter, nil, authv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestErrorHandler,
		ResponseErrorHandlerFunc: strictResponseErrorHandler,
	})
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cacheControlNoStoreMiddleware(), bodyPreDecodeMiddleware(), clientMetaMiddleware())
	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{BaseRouter: r})

	assertHeadHealthResponse(t, r, "/readyz", http.StatusServiceUnavailable)
}

func assertHeadHealthResponse(t *testing.T, r http.Handler, path string, wantStatus int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodHead, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("status=%d want %d", rec.Code, wantStatus)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD must not write a body, got %d bytes", rec.Body.Len())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type=%q want application/json; charset=utf-8", ct)
	}
	for _, needle := range []string{"secret", "password", "host", "db.internal", "connection refused"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(needle)) {
			t.Errorf("response leaked underlying error text %q", needle)
		}
	}
}

func TestHealthz_MethodNotAllowed(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/healthz", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: status=%d want 405", method, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("method %s: Cache-Control=%q want no-store", method, cc)
		}
	}
}

func TestStrictInterfaceConformance(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Body validation tests (migrated from old handler + new)
// ---------------------------------------------------------------------------

func TestRegister_UnknownFieldRejected400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]any{
		"email": "x@example.com", "password": "verystrongpassword123", "extra": "nope",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (unknown field rejected)", rec.Code)
	}
}

func TestRegister_OversizedBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	bigBody := map[string]any{
		"email": "x@example.com", "password": strings.Repeat("a", 2000),
	}
	b, _ := json.Marshal(bigBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for oversized body", rec.Code)
	}
}

func TestRegister_TrailingJSONRejected400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	body := `{"email":"x@example.com","password":"verystrongpassword123"}{"extra":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for trailing JSON", rec.Code)
	}
}

func TestRegister_EmptyBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for empty body", rec.Code)
	}
}

func TestLogin_UnknownField400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]any{
		"email": "x@example.com", "password": "verystrongpassword123", "foo": "bar",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (unknown field)", rec.Code)
	}
}

func TestLogin_OversizedBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	bigBody := map[string]any{
		"email": "x@example.com", "password": strings.Repeat("a", 2000),
	}
	b, _ := json.Marshal(bigBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for oversized body", rec.Code)
	}
}

func TestLogin_TrailingJSON400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	body := `{"email":"x@example.com","password":"verystrongpassword123"}{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for trailing JSON", rec.Code)
	}
}

func TestRefresh_UnknownField400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{
		"refresh_token": "sometoken", "extra": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (unknown field)", rec.Code)
	}
}

func TestRefresh_OversizedBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	bigBody := map[string]any{
		"refresh_token": strings.Repeat("a", 2000),
	}
	b, _ := json.Marshal(bigBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for oversized body", rec.Code)
	}
}

func TestChangePassword_UnknownField400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	rec := doJSON(t, r, http.MethodPut, "/api/v1/auth/password", access, map[string]any{
		"current_password": "verystrongpassword123", "new_password": "newverystrongpassword456", "extra": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 (unknown field)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Logout idempotency tests
// ---------------------------------------------------------------------------

func TestLogout_EmptyBody204(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 for empty body logout", rec.Code)
	}
}

func TestLogout_InvalidJSON204(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 for invalid JSON logout", rec.Code)
	}
}

func TestLogout_OversizedBody204(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	bigBody := map[string]any{
		"refresh_token": strings.Repeat("a", 2000),
	}
	b, _ := json.Marshal(bigBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204 for oversized body logout", rec.Code)
	}
}

func TestLogout_ValidTokenActualRevocation(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, rec, &login)
	rt := login["refresh_token"].(string)

	// Logout with valid token → 204.
	rec1 := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": rt})
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d want 204", rec1.Code)
	}

	// Refresh with the now-revoked token → 401.
	rec2 := doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": rt})
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout status=%d want 401", rec2.Code)
	}

	// Logout again with same token → still 204 (idempotent).
	rec3 := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": rt})
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("idempotent logout status=%d want 204", rec3.Code)
	}
}

func TestClientMeta_StripPort(t *testing.T) {
	cases := []struct {
		name, remoteAddr, host, wantIP string
	}{
		{"IPv4 with port", "203.0.113.1:12345", "", "203.0.113.1"},
		{"IPv6 with port", "[::1]:12345", "", "::1"},
		{"bare IPv4", "203.0.113.1", "", "203.0.113.1"},
		{"empty RemoteAddr fallback to Host", "", "example.com:8080", "example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remoteAddr
			if c.host != "" {
				r.Host = c.host
			}
			ip, _ := clientMeta(r)
			if ip != c.wantIP {
				t.Errorf("ip=%q want %q", ip, c.wantIP)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Logout unknown-field compatibility test (task 3)
// ---------------------------------------------------------------------------

func TestLogout_UnknownFieldStillRevokes(t *testing.T) {
	// A body with a valid refresh_token plus an unknown field must still
	// revoke the token — the old handler only decoded the first JSON value
	// and ignored unknown fields. normalizeLogoutBody must NOT use
	// DisallowUnknownFields.
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, rec, &login)
	rt := login["refresh_token"].(string)

	// Logout with unknown field — must still revoke the valid token.
	body := `{"refresh_token":"` + rt + `","extra":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("logout with unknown field: status=%d want 204", rec1.Code)
	}

	// Verify the token was actually revoked — refresh should fail.
	rec2 := doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": rt})
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout-with-extra: status=%d want 401", rec2.Code)
	}
}

func TestLogout_TrailingJSONStillRevokes(t *testing.T) {
	// A body with a valid refresh_token followed by trailing JSON must still
	// revoke the token — the old handler only called Decode once.
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, rec, &login)
	rt := login["refresh_token"].(string)

	// Logout with trailing JSON — must still revoke the valid token.
	body := `{"refresh_token":"` + rt + `"}{"extra":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("logout with trailing JSON: status=%d want 204", rec1.Code)
	}

	// Verify the token was actually revoked.
	rec2 := doJSON(t, r, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": rt})
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout-with-trailing: status=%d want 401", rec2.Code)
	}
}

// ---------------------------------------------------------------------------
// Body validation gap tests (task 4)
// ---------------------------------------------------------------------------

func TestPassword_TrailingJSON400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	body := `{"current_password":"verystrongpassword123","new_password":"newverystrongpassword456"}{}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/password", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for trailing JSON", rec.Code)
	}
}

func TestPassword_EmptyBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "user@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("user@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/password", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for empty body", rec.Code)
	}
}

func TestRefresh_TrailingJSON400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	body := `{"refresh_token":"sometoken"}{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for trailing JSON", rec.Code)
	}
}

func TestRefresh_EmptyBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for empty body", rec.Code)
	}
}

func TestLogin_EmptyBody400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for empty body", rec.Code)
	}
}

// silence unused linter warnings.
var (
	_ = json.NewDecoder
	_ = time.Now
	_ = jwt.NewIssuer
	_ = password.Validate
)

// ---------------------------------------------------------------------------
// Cache-Control no-store comprehensive tests (task 2)
// ---------------------------------------------------------------------------

func TestCacheControl_AllContractRoutes(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)

	// Register a user and get tokens for authenticated endpoints.
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "cc@example.com", "password": "verystrongpassword123",
	})
	loginRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "cc@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, loginRec, &login)
	accessToken := login["access_token"].(string)
	refreshToken := login["refresh_token"].(string)

	assertCC := func(t *testing.T, name, method, path, bearer string, body any, want int) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, r, method, path, bearer, body)
			if rec.Code != want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body)
			}
			cc := rec.Header().Get("Cache-Control")
			if cc != "no-store" {
				t.Errorf("Cache-Control=%q want no-store", cc)
			}
		})
	}

	// Health
	assertCC(t, "GET /healthz 200", http.MethodGet, "/healthz", "", nil, http.StatusOK)
	assertCC(t, "HEAD /healthz 200", http.MethodHead, "/healthz", "", nil, http.StatusOK)
	assertCC(t, "GET /readyz 200", http.MethodGet, "/readyz", "", nil, http.StatusOK)
	assertCC(t, "HEAD /readyz 200", http.MethodHead, "/readyz", "", nil, http.StatusOK)

	// Register
	assertCC(t, "register 201", http.MethodPost, "/api/v1/auth/register", "", map[string]string{"email": "new@example.com", "password": "verystrongpassword123"}, http.StatusCreated)
	assertCC(t, "register 400 weak pw", http.MethodPost, "/api/v1/auth/register", "", map[string]string{"email": "x@example.com", "password": "short"}, http.StatusBadRequest)
	assertCC(t, "register 409 dup", http.MethodPost, "/api/v1/auth/register", "", map[string]string{"email": "cc@example.com", "password": "verystrongpassword123"}, http.StatusConflict)

	// Login
	assertCC(t, "login 200", http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "cc@example.com", "password": "verystrongpassword123"}, http.StatusOK)
	assertCC(t, "login 401", http.MethodPost, "/api/v1/auth/login", "", map[string]string{"email": "cc@example.com", "password": "wrongpw"}, http.StatusUnauthorized)

	// Refresh
	assertCC(t, "refresh 200", http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": refreshToken}, http.StatusOK)
	assertCC(t, "refresh 401", http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": "bogus"}, http.StatusUnauthorized)

	// Me (before any token invalidation)
	assertCC(t, "me 200", http.MethodGet, "/api/v1/auth/me", accessToken, nil, http.StatusOK)
	assertCC(t, "me 401 no token", http.MethodGet, "/api/v1/auth/me", "", nil, http.StatusUnauthorized)

	// Logout
	assertCC(t, "logout 204", http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": refreshToken}, http.StatusNoContent)

	// Logout-all (invalidates accessToken)
	assertCC(t, "logout-all 204", http.MethodPost, "/api/v1/auth/logout-all", accessToken, nil, http.StatusNoContent)

	// Re-login for password test
	loginRec2 := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "cc@example.com", "password": "verystrongpassword123",
	})
	var login2 map[string]any
	decodeBody(t, loginRec2, &login2)
	accessToken2 := login2["access_token"].(string)

	// Password
	assertCC(t, "password 204", http.MethodPut, "/api/v1/auth/password", accessToken2, map[string]string{"current_password": "verystrongpassword123", "new_password": "newverystrongpassword456"}, http.StatusNoContent)
	assertCC(t, "password 401 no token", http.MethodPut, "/api/v1/auth/password", "", map[string]string{"current_password": "x", "new_password": "y"}, http.StatusUnauthorized)
}

func TestCacheControl_404NotSet(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc == "no-store" {
		t.Errorf("Cache-Control should not be set on 404, got %q", cc)
	}
}

func TestCacheControl_Readyz503(t *testing.T) {
	unreadyPinger := &unreadyPinger{}
	adapter := NewStrictAdapter(nil, unreadyPinger, 15*time.Minute)
	strictHandler := authv1.NewStrictHandlerWithOptions(adapter, nil, authv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestErrorHandler,
		ResponseErrorHandlerFunc: strictResponseErrorHandler,
	})
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cacheControlNoStoreMiddleware(), bodyPreDecodeMiddleware(), clientMetaMiddleware())
	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{BaseRouter: r})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
}

func TestCacheControl_BodyValidation400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	// Invalid JSON body on register → 400 from bodyPreDecodeMiddleware
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
}

func TestCacheControl_LogoutEmptyBody204(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
}

func TestCacheControl_PasswordWeak400(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "pw@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("pw@example.com")
	access, _, _ := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	rec := doJSON(t, r, http.MethodPut, "/api/v1/auth/password", access, map[string]string{
		"current_password": "verystrongpassword123", "new_password": "short",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
}

// ---------------------------------------------------------------------------
// Unknown service error test (task 3)
// ---------------------------------------------------------------------------

// failingUserRepo returns an internal error on Create to test the 500 path.
type failingUserRepo struct {
	fakeStore
}

func (f *failingUserRepo) Create(_ context.Context, _ *models.User) error {
	return errors.New("simulated internal error")
}

func TestUnknownServiceError_500(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519: %v", err)
	}
	kp := &jwt.KeyPair{Private: priv, Public: pub}
	issuer, err := jwt.NewIssuer(kp, "tokenmp-auth", "tokenmp-web", 15*time.Minute)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	failUsers := &failingUserRepo{fakeStore: *newFakeStore()}
	svc := auth.NewService(failUsers, newFakeSessionStore(), fakeTxRunner{}, issuer, &fixedClock{t: time.Now().UTC()}, 15*time.Minute, 30*24*time.Hour)
	adapter := NewStrictAdapter(svc, fakePinger{}, 15*time.Minute)
	strictHandler := authv1.NewStrictHandlerWithOptions(adapter, nil, authv1.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  strictRequestErrorHandler,
		ResponseErrorHandlerFunc: strictResponseErrorHandler,
	})
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cacheControlNoStoreMiddleware(), bodyPreDecodeMiddleware(), clientMetaMiddleware())
	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{BaseRouter: r})

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "fail@example.com", "password": "verystrongpassword123",
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rec.Code)
	}
	var e map[string]any
	decodeBody(t, rec, &e)
	errObj := e["error"].(map[string]any)
	if errObj["code"] != "internal_error" {
		t.Errorf("code=%v want internal_error", errObj["code"])
	}
	// Must not leak the simulated error message.
	if strings.Contains(rec.Body.String(), "simulated") {
		t.Error("500 response leaked internal error details")
	}
	cc := rec.Header().Get("Cache-Control")
	if cc != "no-store" {
		t.Errorf("Cache-Control=%q want no-store", cc)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type=%q", ct)
	}
}

// ---------------------------------------------------------------------------
// Content-Type comprehensive tests
// ---------------------------------------------------------------------------

func TestContentType_JSONResponses(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)

	_ = doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "ct@example.com", "password": "verystrongpassword123",
	})
	loginRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "ct@example.com", "password": "verystrongpassword123",
	})
	var login map[string]any
	decodeBody(t, loginRec, &login)

	// 200 login
	if ct := loginRec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("login 200 Content-Type=%q", ct)
	}

	// 201 register
	regRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "ct2@example.com", "password": "verystrongpassword123",
	})
	if ct := regRec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("register 201 Content-Type=%q", ct)
	}

	// 401 error
	errRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "ct@example.com", "password": "wrong",
	})
	if ct := errRec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("login 401 Content-Type=%q", ct)
	}

	// 409 error
	dupRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "ct@example.com", "password": "verystrongpassword123",
	})
	if ct := dupRec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("register 409 Content-Type=%q", ct)
	}

	// 204 has no Content-Type
	logoutRec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": "bogus"})
	if ct := logoutRec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("logout 204 Content-Type=%q want empty", ct)
	}
}
