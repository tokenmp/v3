package keys_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"

	"github.com/tokenmp/v3/services/api/internal/identity"
	"github.com/tokenmp/v3/services/api/internal/keys"
)

// ---------------------------------------------------------------------------
// 辅助：JWT、Auth mock
// ---------------------------------------------------------------------------

func genKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func writePubPEM(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := t.TempDir() + "/pub.pem"
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func makeJWT(t *testing.T, priv ed25519.PrivateKey, sub string) string {
	t.Helper()
	claims := &jwt.RegisteredClaims{
		Subject:   sub,
		Issuer:    "tokenmp-auth",
		Audience:  jwt.ClaimStrings{"tokenmp-web"},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// authCall 记录 Auth 收到的请求。
type authCall struct {
	method string
	path   string
	auth   string
	body   string
}

// newAuthBackend 构造一个 fake Auth，按 path 分发固定响应，并记录调用。
func newAuthBackend(t *testing.T, keyID string) (*httptest.Server, *authCall) {
	t.Helper()
	var call authCall
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		call.method = r.Method
		call.path = r.URL.Path
		call.auth = r.Header.Get("Authorization")
		call.body = readAll(r.Body)
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"keys":[{"id":"` + keyID + `","name":"k","key_prefix":"test_prefix","key_suffix":"suff","status":"active","created_at":"2026-01-02T03:04:05Z"}]}`))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"key":{"id":"` + keyID + `","name":"k","key_prefix":"test_prefix","key_suffix":"suff","secret":"test_secret_value","status":"active","created_at":"2026-01-02T03:04:05Z"}}`))
		}
	})
	mux.HandleFunc("/api/v1/auth/keys/", func(w http.ResponseWriter, r *http.Request) {
		call.method = r.Method
		call.path = r.URL.Path
		call.auth = r.Header.Get("Authorization")
		call.body = readAll(r.Body)
		isRotate := strings.HasSuffix(r.URL.Path, "/rotate")
		switch {
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key":{"id":"` + keyID + `","name":"k","key_prefix":"test_prefix","key_suffix":"suff","status":"active","created_at":"2026-01-02T03:04:05Z"}}`))
		case r.Method == http.MethodPatch:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key":{"id":"` + keyID + `","name":"renamed","key_prefix":"test_prefix","key_suffix":"suff","status":"disabled","created_at":"2026-01-02T03:04:05Z"}}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && isRotate:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key":{"id":"` + keyID + `","name":"k","key_prefix":"test_rot","key_suffix":"rot","secret":"test_rotated_val","status":"active","created_at":"2026-01-02T03:04:05Z"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return httptest.NewServer(mux), &call
}

func readAll(r io.ReadCloser) string {
	if r == nil {
		return ""
	}
	b, _ := io.ReadAll(r)
	return string(b)
}

// newEdgeRouter 构造一个带身份中间件的 Edge 路由，注册 keys handler。
// 返回路由、handler 与用于鉴权的 access token。
func newEdgeRouter(t *testing.T, authURL string) (http.Handler, *keys.Handler, string) {
	t.Helper()
	pub, priv := genKeyPair(t)
	bearer := makeJWT(t, priv, "user-uuid-1234")
	keyFile := writePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	h := keys.NewHandler(keys.New(authURL), nil)
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(verifier, nil))
		h.Routes(r)
	})
	return r, h, bearer
}

func doReq(t *testing.T, h http.Handler, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
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
	req.Header.Set("Authorization", "Bearer "+bearer)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestKeys_ListForwardsBearer(t *testing.T) {
	auth, call := newAuthBackend(t, "11111111-1111-1111-1111-111111111111")
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	rec := doReq(t, r, http.MethodGet, "/api/v1/keys", bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if call.method != http.MethodGet || call.path != "/api/v1/auth/keys" {
		t.Errorf("auth call = %s %s, want GET /api/v1/auth/keys", call.method, call.path)
	}
	if !strings.HasPrefix(call.auth, "Bearer ") {
		t.Errorf("auth bearer not forwarded: %q", call.auth)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	keysArr, ok := out["keys"].([]any)
	if !ok || len(keysArr) != 1 {
		t.Fatalf("keys = %v", out["keys"])
	}
	first := keysArr[0].(map[string]any)
	// Edge camelCase 转换验证。
	if first["keyPrefix"] != "test_prefix" {
		t.Errorf("keyPrefix=%v", first["keyPrefix"])
	}
	if first["createdAt"] == nil {
		t.Errorf("createdAt missing")
	}
	if _, has := first["secret"]; has {
		t.Errorf("list must not include secret")
	}
}

func TestKeys_CreateReturnsSecret(t *testing.T) {
	auth, _ := newAuthBackend(t, "22222222-2222-2222-2222-222222222222")
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	rec := doReq(t, r, http.MethodPost, "/api/v1/keys", bearer, map[string]any{"name": "my key"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := out["key"].(map[string]any)
	if key["secret"] != "test_secret_value" {
		t.Errorf("secret=%v", key["secret"])
	}
	if key["status"] != "active" {
		t.Errorf("status=%v", key["status"])
	}
}

func TestKeys_GetDetail(t *testing.T) {
	id := "33333333-3333-3333-3333-333333333333"
	auth, _ := newAuthBackend(t, id)
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	rec := doReq(t, r, http.MethodGet, "/api/v1/keys/"+id, bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := out["key"].(map[string]any)
	if key["id"] != id {
		t.Errorf("id=%v", key["id"])
	}
	if _, has := key["secret"]; has {
		t.Errorf("get must not include secret")
	}
}

func TestKeys_Update(t *testing.T) {
	id := "44444444-4444-4444-4444-444444444444"
	auth, _ := newAuthBackend(t, id)
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	rec := doReq(t, r, http.MethodPatch, "/api/v1/keys/"+id, bearer, map[string]any{"name": "renamed", "status": "disabled"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := out["key"].(map[string]any)
	if key["status"] != "disabled" {
		t.Errorf("status=%v", key["status"])
	}
}

func TestKeys_Delete204(t *testing.T) {
	id := "55555555-5555-5555-5555-555555555555"
	auth, _ := newAuthBackend(t, id)
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	rec := doReq(t, r, http.MethodDelete, "/api/v1/keys/"+id, bearer, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rec.Code)
	}
}

func TestKeys_RotateReturnsNewSecret(t *testing.T) {
	id := "66666666-6666-6666-6666-666666666666"
	auth, _ := newAuthBackend(t, id)
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	rec := doReq(t, r, http.MethodPost, "/api/v1/keys/"+id+"/rotate", bearer, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := out["key"].(map[string]any)
	if key["secret"] != "test_rotated_val" {
		t.Errorf("secret=%v", key["secret"])
	}
}

func TestKeys_AuthNotFoundMaps404(t *testing.T) {
	// Auth 对未知 keyId 返回 404。
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"api key not found"}}`))
	}))
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	id := "77777777-7777-7777-7777-777777777777"
	rec := doReq(t, r, http.MethodGet, "/api/v1/keys/"+id, bearer, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rec.Code)
	}
}

func TestKeys_AuthUnreachableMaps503(t *testing.T) {
	// Auth 不可达（指向已关闭端口）。
	r, _, bearer := newEdgeRouter(t, "http://127.0.0.1:0")
	id := "88888888-8888-8888-8888-888888888888"
	rec := doReq(t, r, http.MethodGet, "/api/v1/keys/"+id, bearer, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

func TestKeys_RequireAuth401(t *testing.T) {
	auth, _ := newAuthBackend(t, "99999999-9999-9999-9999-999999999999")
	defer auth.Close()
	r, _, _ := newEdgeRouter(t, auth.URL)
	// 无 Authorization 头。
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
}

func TestKeys_InvalidKeyID400(t *testing.T) {
	auth, _ := newAuthBackend(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)
	rec := doReq(t, r, http.MethodGet, "/api/v1/keys/not-a-uuid", bearer, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rec.Code)
	}
}

func TestKeys_NoAuthURL503(t *testing.T) {
	// 未注入 client 的 handler。
	pub, priv := genKeyPair(t)
	keyFile := writePubPEM(t, pub)
	verifier, err := identity.NewVerifier(keyFile, "tokenmp-auth", "tokenmp-web", nil)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	h := keys.NewHandler(nil, nil)
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(identity.Middleware(verifier, nil))
		h.Routes(r)
	})
	bearer := makeJWT(t, priv, "user-uuid")
	rec := doReq(t, r, http.MethodGet, "/api/v1/keys", bearer, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rec.Code)
	}
}

func TestKeys_CreateBodyValidation(t *testing.T) {
	auth, _ := newAuthBackend(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	defer auth.Close()
	r, _, bearer := newEdgeRouter(t, auth.URL)

	// 未知字段 → 400
	rec := doReq(t, r, http.MethodPost, "/api/v1/keys", bearer, map[string]any{"name": "x", "extra": 1})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field status=%d want 400", rec.Code)
	}
}

// 确保 context 被使用（避免删除 import 误报）。
var _ = context.TODO
