package authv1api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/apikey"
)

// ---------------------------------------------------------------------------
// 内存 APIKeyStore —— 仅供测试
// ---------------------------------------------------------------------------

// fakeAPIKeyStore 是 APIKeyStore 的内存实现，用于覆盖密钥管理端点。
type fakeAPIKeyStore struct {
	mu     sync.Mutex
	keys   map[string]*models.APIKey
	byHash map[string]*models.APIKey
}

func newFakeAPIKeyStore() *fakeAPIKeyStore {
	return &fakeAPIKeyStore{keys: map[string]*models.APIKey{}, byHash: map[string]*models.APIKey{}}
}

func (s *fakeAPIKeyStore) seed(k *models.APIKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k.ID == "" {
		k.ID = uuid.NewString()
	}
	copyK := *k
	s.keys[k.ID] = &copyK
	s.byHash[string(k.KeyHash)] = &copyK
}

func (s *fakeAPIKeyStore) Create(_ context.Context, key *models.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key.ID == "" {
		key.ID = uuid.NewString()
	}
	if _, exists := s.byHash[string(key.KeyHash)]; exists {
		return repository.ErrConstraint
	}
	copyK := *key
	s.keys[key.ID] = &copyK
	s.byHash[string(key.KeyHash)] = &copyK
	return nil
}

func (s *fakeAPIKeyStore) ListByUser(_ context.Context, userID string) ([]models.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []models.APIKey
	for _, k := range s.keys {
		if k.UserID == userID && k.Status != "revoked" {
			out = append(out, *k)
		}
	}
	// 模拟 repository 的最新优先排序。
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (s *fakeAPIKeyStore) FindByIDForUser(_ context.Context, id, userID string) (*models.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok || k.UserID != userID || k.Status == "revoked" {
		return nil, repository.ErrNotFound
	}
	copyK := *k
	return &copyK, nil
}

func (s *fakeAPIKeyStore) UpdateFields(_ context.Context, id, userID string, fields map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok || k.UserID != userID || k.Status == "revoked" {
		return repository.ErrNotFound
	}
	if v, ok := fields["name"]; ok {
		k.Name = v.(string)
	}
	if v, ok := fields["status"]; ok {
		k.Status = v.(string)
	}
	return nil
}

func (s *fakeAPIKeyStore) Rotate(_ context.Context, id, userID string, hash []byte, prefix, suffix string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok || k.UserID != userID || k.Status == "revoked" {
		return repository.ErrNotFound
	}
	delete(s.byHash, string(k.KeyHash))
	k.KeyHash = hash
	k.KeyPrefix = prefix
	k.KeySuffix = suffix
	k.Status = "active"
	s.byHash[string(hash)] = k
	return nil
}

// ---------------------------------------------------------------------------
// 测试路由装配
// ---------------------------------------------------------------------------

// keysTestEnv 在标准 testEnv 基础上注入一个内存 APIKeyStore，并装配带密钥
// 端点的 chi 路由。
type keysTestEnv struct {
	*testEnv
	keys *fakeAPIKeyStore
}

func newKeysTestEnv(t *testing.T) *keysTestEnv {
	t.Helper()
	env := newTestEnv(t)
	keys := newFakeAPIKeyStore()
	env.adapter = env.adapter.WithAPIKeyStore(keys)
	return &keysTestEnv{testEnv: env, keys: keys}
}

func (e *keysTestEnv) router(t *testing.T) http.Handler {
	t.Helper()
	store := &envUserStore{e: e.testEnv}
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
	authv1.HandlerWithOptions(strictHandler, authv1.ChiServerOptions{BaseRouter: r})
	return r
}

// bearerForUser 注册一个用户并签发其 access token。
func (e *keysTestEnv) bearerForUser(t *testing.T) string {
	t.Helper()
	_ = doJSON(t, e.testEnv.routerWithStore(t), http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "keys@example.com", "password": "verystrongpassword123",
	})
	u := e.users.get("keys@example.com")
	access, _, err := e.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, e.clock.t)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return access
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestKeys_ListEmpty(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/keys", bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	decodeBody(t, rec, &out)
	if _, ok := out["keys"]; !ok {
		t.Errorf("missing keys field: %v", out)
	}
	if _, ok := out["keys"].([]any); !ok {
		t.Errorf("keys not array: %T", out["keys"])
	}
}

func TestKeys_RequireBearer401(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/keys", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestKeys_CreateAndGet(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	// 创建
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "my key"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body)
	}
	var created struct {
		Key struct {
			Id        string `json:"id"`
			Name      string `json:"name"`
			Secret    string `json:"secret"`
			KeyPrefix string `json:"key_prefix"`
			Status    string `json:"status"`
		} `json:"key"`
	}
	decodeBody(t, rec, &created)
	if created.Key.Secret == "" || !strings.HasPrefix(created.Key.Secret, apikey.PrefixMarker) {
		t.Errorf("secret invalid: %q", created.Key.Secret)
	}
	if created.Key.Name != "my key" {
		t.Errorf("name=%q want my key", created.Key.Name)
	}
	if created.Key.Status != "active" {
		t.Errorf("status=%q want active", created.Key.Status)
	}
	keyID := created.Key.Id

	// 列表包含
	rec = doJSON(t, r, http.MethodGet, "/api/v1/auth/keys", bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d", rec.Code)
	}
	var list struct {
		Keys []struct {
			Id     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"keys"`
	}
	decodeBody(t, rec, &list)
	if len(list.Keys) != 1 || list.Keys[0].Id != keyID {
		t.Fatalf("list = %+v, want one key %s", list.Keys, keyID)
	}
	if list.Keys[0].Secret != "" {
		t.Errorf("list must not return secret, got %q", list.Keys[0].Secret)
	}

	// 详情
	rec = doJSON(t, r, http.MethodGet, "/api/v1/auth/keys/"+keyID, bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", rec.Code, rec.Body)
	}
	var got struct {
		Key struct {
			Id     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"key"`
	}
	decodeBody(t, rec, &got)
	if got.Key.Id != keyID {
		t.Errorf("get id=%s want %s", got.Key.Id, keyID)
	}
	if got.Key.Secret != "" {
		t.Errorf("get must not return secret")
	}
}

func TestKeys_CreateValidates(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	// 空 name → 400
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": ""})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name status=%d want 400", rec.Code)
	}
	// 过去 expires_at → 400
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "ok", "expires_at": past})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("past expires_at status=%d want 400", rec.Code)
	}
}

func TestKeys_CreateBodyValidation(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	// 未知字段 → 400
	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "ok", "extra": 1})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field status=%d want 400", rec.Code)
	}
	// 超 1 KiB → 400
	big := strings.Repeat("a", 2048)
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": big})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversize status=%d want 400", rec.Code)
	}
	// trailing JSON → 400
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/keys", strings.NewReader(`{"name":"a"}{"name":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("trailing status=%d want 400", rr.Code)
	}
}

func TestKeys_Update(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "orig"})
	var created struct {
		Key struct {
			Id string `json:"id"`
		} `json:"key"`
	}
	decodeBody(t, rec, &created)
	keyID := created.Key.Id

	// 更新 name + status
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/auth/keys/"+keyID, bearer, map[string]any{"name": "new", "status": "disabled"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body)
	}
	var upd struct {
		Key struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"key"`
	}
	decodeBody(t, rec, &upd)
	if upd.Key.Name != "new" || upd.Key.Status != "disabled" {
		t.Errorf("update = name %q status %q, want new/disabled", upd.Key.Name, upd.Key.Status)
	}

	// 空 body → 400
	rec = doJSON(t, r, http.MethodPatch, "/api/v1/auth/keys/"+keyID, bearer, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty update status=%d want 400", rec.Code)
	}
}

func TestKeys_DeleteSoft(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "to delete"})
	var created struct {
		Key struct {
			Id string `json:"id"`
		} `json:"key"`
	}
	decodeBody(t, rec, &created)
	keyID := created.Key.Id

	// 删除 → 204
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/auth/keys/"+keyID, bearer, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d want 204", rec.Code)
	}
	// 详情仍可见，但状态为 disabled（软删除未吊销）
	rec = doJSON(t, r, http.MethodGet, "/api/v1/auth/keys/"+keyID, bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get after delete status=%d want 200", rec.Code)
	}
	var got struct {
		Key struct {
			Status string `json:"status"`
		} `json:"key"`
	}
	decodeBody(t, rec, &got)
	if got.Key.Status != "disabled" {
		t.Errorf("status after delete=%q want disabled", got.Key.Status)
	}
	// 幂等：再次删除仍 204
	rec = doJSON(t, r, http.MethodDelete, "/api/v1/auth/keys/"+keyID, bearer, nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("second delete status=%d want 204", rec.Code)
	}
}

func TestKeys_Rotate(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "rotate me"})
	var created struct {
		Key struct {
			Id     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"key"`
	}
	decodeBody(t, rec, &created)
	oldSecret := created.Key.Secret
	keyID := created.Key.Id

	// 先禁用，验证 rotate 会重新激活
	_ = doJSON(t, r, http.MethodPatch, "/api/v1/auth/keys/"+keyID, bearer, map[string]any{"status": "disabled"})

	// 轮换
	rec = doJSON(t, r, http.MethodPost, "/api/v1/auth/keys/"+keyID+"/rotate", bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", rec.Code, rec.Body)
	}
	var rot struct {
		Key struct {
			Id     string `json:"id"`
			Secret string `json:"secret"`
			Status string `json:"status"`
		} `json:"key"`
	}
	decodeBody(t, rec, &rot)
	if rot.Key.Id != keyID {
		t.Errorf("rotate id=%s want %s", rot.Key.Id, keyID)
	}
	if rot.Key.Secret == "" || rot.Key.Secret == oldSecret {
		t.Errorf("rotate secret must be new and non-empty")
	}
	if rot.Key.Status != "active" {
		t.Errorf("rotate status=%q want active", rot.Key.Status)
	}

	// 详情状态应为 active
	rec = doJSON(t, r, http.MethodGet, "/api/v1/auth/keys/"+keyID, bearer, nil)
	var got struct {
		Key struct {
			Status string `json:"status"`
		} `json:"key"`
	}
	decodeBody(t, rec, &got)
	if got.Key.Status != "active" {
		t.Errorf("get after rotate status=%q want active", got.Key.Status)
	}
}

func TestKeys_NotFound(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)
	missing := uuid.NewString()

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/auth/keys/" + missing, http.StatusNotFound},
		{http.MethodPatch, "/api/v1/auth/keys/" + missing, http.StatusNotFound},
		{http.MethodDelete, "/api/v1/auth/keys/" + missing, http.StatusNotFound},
		{http.MethodPost, "/api/v1/auth/keys/" + missing + "/rotate", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := doJSON(t, r, c.method, c.path, bearer, map[string]any{"name": "x"})
		if rec.Code != c.want {
			t.Errorf("%s %s status=%d want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

func TestKeys_NoStoreHeader(t *testing.T) {
	env := newKeysTestEnv(t)
	r := env.router(t)
	bearer := env.bearerForUser(t)

	rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/keys", bearer, map[string]any{"name": "hdr"})
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("create Cache-Control=%q want no-store", cc)
	}
	rec = doJSON(t, r, http.MethodGet, "/api/v1/auth/keys", bearer, nil)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("list Cache-Control=%q want no-store", cc)
	}
}

func TestKeys_NilStoreReturns500(t *testing.T) {
	// 未注入 store 的 adapter，密钥端点应返回 500。
	env := newTestEnv(t) // 无 keys store
	r := env.routerWithStore(t)
	bearer := registerAndGetBearer(t, env)
	rec := doJSON(t, r, http.MethodGet, "/api/v1/auth/keys", bearer, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rec.Code)
	}
}

// registerAndGetBearer 注册用户并返回 access token。
func registerAndGetBearer(t *testing.T, env *testEnv) string {
	t.Helper()
	_ = doJSON(t, env.routerWithStore(t), http.MethodPost, "/api/v1/auth/register", "", map[string]string{
		"email": "nokeys@example.com", "password": "verystrongpassword123",
	})
	u := env.users.get("nokeys@example.com")
	access, _, err := env.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, env.clock.t)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return access
}

// 确保 fakeAPIKeyStore 满足 APIKeyStore 接口（编译期断言）。
var _ APIKeyStore = (*fakeAPIKeyStore)(nil)
