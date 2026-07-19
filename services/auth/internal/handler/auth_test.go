package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
)

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
	var e errorBody
	decodeBody(t, rec, &e)
	if e.Error.Code != CodeEmailTaken {
		t.Errorf("code=%s want %s", e.Error.Code, CodeEmailTaken)
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
			var e errorBody
			decodeBody(t, rec, &e)
			if e.Error.Code != CodeInvalidCredentials {
				t.Errorf("code=%s want %s", e.Error.Code, CodeInvalidCredentials)
			}
			// Body must not echo the submitted credentials.
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
	var e errorBody
	decodeBody(t, rec2, &e)
	// Reuse returns the same shape as invalid refresh to avoid signalling.
	if e.Error.Code != CodeInvalidRefresh {
		t.Errorf("code=%s want %s", e.Error.Code, CodeInvalidRefresh)
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
	// Logout once.
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": rt}); rec.Code != http.StatusNoContent {
		t.Errorf("logout status=%d want 204", rec.Code)
	}
	// Logout again (revoked) — still 204.
	if rec := doJSON(t, r, http.MethodPost, "/api/v1/auth/logout", "", map[string]string{"refresh_token": rt}); rec.Code != http.StatusNoContent {
		t.Errorf("idempotent logout status=%d want 204", rec.Code)
	}
	// Logout unknown token — still 204 (no probing).
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
	// Bump token_version (simulating logout-all / password change) and
	// confirm the now-stale access token is rejected.
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
	// Must NOT return account_disabled code — that would leak account status.
	var e errorBody
	decodeBody(t, rec, &e)
	if e.Error.Code == "account_disabled" {
		t.Error("disabled account leaked account_disabled code in response")
	}
	if e.Error.Code != CodeInvalidToken {
		t.Errorf("code=%s want %s", e.Error.Code, CodeInvalidToken)
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
	// The current access token (with old token_version) must now be invalid.
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
	// Access token now invalid (token_version bumped).
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
	var e errorBody
	decodeBody(t, rec, &e)
	if e.Error.Code == "" || e.Error.Message == "" {
		t.Errorf("error body missing code/message: %+v", e)
	}
	if strings.Contains(rec.Body.String(), "whateverpassword") {
		t.Error("password leaked in error body")
	}
}

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

func TestRegister_OversizedBody413(t *testing.T) {
	env := newTestEnv(t)
	r := env.routerWithStore(t)
	// Send a body larger than the 1 KiB limit.
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
	// Two JSON objects in one body must be rejected.
	body := `{"email":"x@example.com","password":"verystrongpassword123"}{"extra":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for trailing JSON", rec.Code)
	}
}

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

// silence unused linter warnings for imports retained for clarity.
var (
	_ = json.NewDecoder
	_ = time.Now
	_ = jwt.NewIssuer
)
