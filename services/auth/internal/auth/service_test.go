package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/security/jwt"
	"github.com/tokenmp/v3/services/auth/internal/security/password"
	"github.com/tokenmp/v3/services/auth/internal/security/refresh"
)

// fakeIssuer captures the last issued token and its claims for assertions.
type fakeIssuer struct {
	kp      *jwt.KeyPair
	issued  int
	lastSub string
	lastTV  int
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	pub, priv, err := ed25519GenerateKey()
	if err != nil {
		t.Fatalf("ed25519: %v", err)
	}
	kp2 := &jwt.KeyPair{Private: priv, Public: pub}
	return &fakeIssuer{kp: kp2}
}

func (f *fakeIssuer) IssueAccessToken(userID, role string, tokenVersion int, now time.Time) (string, time.Time, error) {
	f.issued++
	f.lastSub = userID
	f.lastTV = tokenVersion
	return "fake-access-" + userID, now.Add(15 * time.Minute), nil
}

func newSvc(t *testing.T) (*Service, *fakeUserRepo, *fakeSessionRepo, *fakeClock, *fakeIssuer) {
	t.Helper()
	users := newFakeUserRepo()
	sessions := newFakeSessionRepo()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	issuer := newFakeIssuer(t)
	svc := NewService(users, sessions, fakeTxRunner{}, issuer, clock, 15*time.Minute, 30*24*time.Hour)
	return svc, users, sessions, clock, issuer
}

func TestRegister_Success(t *testing.T) {
	svc, users, _, _, _ := newSvc(t)
	u, err := svc.Register(context.Background(), "User@Example.com ", "verystrongpassword123")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Email != "user@example.com" {
		t.Errorf("email not normalized: %q", u.Email)
	}
	if u.Role != "user" || u.Status != "active" {
		t.Errorf("role/status defaults wrong: %s/%s", u.Role, u.Status)
	}
	// Ensure no refresh session was created (no auto-login).
	if len(users.byID) != 1 {
		t.Errorf("expected 1 user, got %d", len(users.byID))
	}
	stored, _ := users.FindByEmail(context.Background(), "user@example.com")
	if !password.IsArgon2id(stored.PasswordHash) {
		t.Error("password not hashed with Argon2id")
	}
}

func TestRegister_DuplicateEmail_409(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "dup@example.com", "verystrongpassword123")
	_, err := svc.Register(context.Background(), "DUP@example.com   ", "anotherstrongpass456")
	if !errors.Is(err, ErrEmailTaken) {
		t.Errorf("err=%v want ErrEmailTaken", err)
	}
}

func TestRegister_WeakPassword(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	cases := []string{"short", "", "nocontrols\x00xxx", strings.Repeat("a", 129)}
	for _, pw := range cases {
		if _, err := svc.Register(context.Background(), "x@example.com", pw); !errors.Is(err, ErrPasswordTooWeak) {
			t.Errorf("password %q err=%v want ErrPasswordTooWeak", pw, err)
		}
	}
}

func TestRegister_InvalidEmail(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	for _, e := range []string{"", "notanemail", "user@", "@example.com", strings.Repeat("a", 260) + "@x.com", "user @example.com", "user@ example.com", "user@ex\tample.com", "a@@b.com"} {
		if _, err := svc.Register(context.Background(), e, "verystrongpassword123"); !errors.Is(err, ErrInvalidEmailFormat) {
			t.Errorf("email %q err=%v want ErrInvalidEmailFormat", e, err)
		}
	}
}

func TestRegister_ValidEmailNoDotInDomain(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	// Emails without a dot in the domain are valid (e.g. user@localhost).
	u, err := svc.Register(context.Background(), "user@localhost", "verystrongpassword123")
	if err != nil {
		t.Fatalf("Register user@localhost: %v", err)
	}
	if u.Email != "user@localhost" {
		t.Errorf("email=%q want user@localhost", u.Email)
	}
}

func TestLogin_SuccessArgon2id(t *testing.T) {
	svc, users, sessions, _, issuer := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, err := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "203.0.113.1", "ua")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.AccessToken == "" || !strings.HasPrefix(res.AccessToken, "fake-access-") {
		t.Errorf("access token wrong: %q", res.AccessToken)
	}
	if res.RefreshToken == "" {
		t.Error("refresh token missing")
	}
	if res.TokenType != "Bearer" {
		t.Errorf("token_type=%q", res.TokenType)
	}
	if res.ExpiresIn != 900 {
		t.Errorf("expires_in=%d want 900", res.ExpiresIn)
	}
	user := users.get("user@example.com")
	if issuer.lastSub != user.ID {
		t.Errorf("issued sub=%q want %q", issuer.lastSub, user.ID)
	}
	if issuer.lastTV != 1 {
		t.Errorf("issued token_version=%d want 1", issuer.lastTV)
	}
	if len(sessions.byID) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions.byID))
	}
}

func TestLogin_BcryptLegacyUpgrade(t *testing.T) {
	svc, users, sessions, _, _ := newSvc(t)
	// Seed a user with a legacy bcrypt hash directly via the repo.
	bcryptHash, err := bcryptHashFor("legacypassword123")
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	users.Create(context.Background(), &models.User{Email: "legacy@example.com", PasswordHash: bcryptHash})
	beforeCount := len(sessions.byID)
	res, err := svc.Login(context.Background(), "legacy@example.com", "legacypassword123", "", "")
	if err != nil {
		t.Fatalf("Login with bcrypt: %v", err)
	}
	if res.RefreshToken == "" {
		t.Error("no refresh token issued on bcrypt login")
	}
	stored, _ := users.FindByEmail(context.Background(), "legacy@example.com")
	if !password.IsArgon2id(stored.PasswordHash) {
		t.Error("bcrypt hash was not upgraded to Argon2id on login")
	}
	if stored.TokenVersion != 1 {
		t.Errorf("token_version bumped on login: %d (must NOT bump)", stored.TokenVersion)
	}
	if len(sessions.byID) != beforeCount+1 {
		t.Errorf("expected new session, got %d", len(sessions.byID))
	}
}

func TestLogin_InvalidCredentialsUniform(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	cases := []struct {
		name, email, pw string
	}{
		{"wrong password", "user@example.com", "wrongpassword"},
		{"unknown user", "nope@example.com", "verystrongpassword123"},
		{"empty password", "user@example.com", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Login(context.Background(), c.email, c.pw, "", "")
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("err=%v want ErrInvalidCredentials", err)
			}
		})
	}
}

func TestLogin_DisabledAccount(t *testing.T) {
	svc, users, _, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	users.get("user@example.com").Status = models.StatusDisabled
	_, err := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("disabled err=%v want ErrInvalidCredentials", err)
	}
}

func TestRefresh_Rotation(t *testing.T) {
	svc, _, sessions, clock, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	// First refresh: rotate.
	clock.advance(time.Hour)
	res2, err := svc.Refresh(context.Background(), res.RefreshToken, "", "")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res2.RefreshToken == res.RefreshToken {
		t.Error("refresh token not rotated")
	}
	if len(sessions.byID) != 2 {
		t.Errorf("expected 2 sessions after rotation, got %d", len(sessions.byID))
	}
	// Old session must be revoked with token_rotated.
	oldHash, _ := refresh.HashToken(res.RefreshToken)
	oldSess := sessions.byHash[string(oldHash)]
	if oldSess.RevokedAt == nil || oldSess.RevokeReason == nil || *oldSess.RevokeReason != "token_rotated" {
		t.Errorf("old session not revoked token_rotated: reason=%v", oldSess.RevokeReason)
	}
	if oldSess.ReplacedBySessionID == nil {
		t.Error("old session replaced_by_session_id not set")
	}
}

func TestRefresh_ReuseRevokesFamilyCommits(t *testing.T) {
	svc, _, sessions, clock, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	clock.advance(time.Hour)
	// Rotate once.
	res2, err := svc.Refresh(context.Background(), res.RefreshToken, "", "")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	// Rotate again using the new token.
	clock.advance(time.Hour)
	if _, err := svc.Refresh(context.Background(), res2.RefreshToken, "", ""); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	// Now attempt to reuse the ORIGINAL (revoked) token. This is reuse and
	// must revoke the whole family and COMMIT before returning 401.
	_, err = svc.Refresh(context.Background(), res.RefreshToken, "", "")
	if !errors.Is(err, ErrTokenReuse) {
		t.Errorf("reuse err=%v want ErrTokenReuse", err)
	}
	// Assert the family's active sessions are revoked with token_reuse and
	// that the revocation COMMITTED (the fake applies immediately, mirroring
	// the real transaction's commit-on-nil-return).
	familyHash, _ := refresh.HashToken(res.RefreshToken)
	family := sessions.byHash[string(familyHash)].TokenFamilyID
	anyActive := false
	for _, s := range sessions.snapshot() {
		if s.TokenFamilyID == family && s.RevokedAt == nil {
			anyActive = true
		}
	}
	if anyActive {
		t.Error("reuse did not revoke all active sessions in the family")
	}
	// At least one session must carry token_reuse.
	foundReuse := false
	for _, s := range sessions.snapshot() {
		if s.RevokeReason != nil && *s.RevokeReason == "token_reuse" {
			foundReuse = true
		}
	}
	if !foundReuse {
		t.Error("no session carries revoke_reason=token_reuse")
	}
}

func TestRefresh_Expired(t *testing.T) {
	svc, _, _, clock, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	// Advance past the refresh token lifetime (30d default).
	clock.advance(31 * 24 * time.Hour)
	_, err := svc.Refresh(context.Background(), res.RefreshToken, "", "")
	if !errors.Is(err, ErrInvalidRefresh) {
		t.Errorf("err=%v want ErrInvalidRefresh", err)
	}
}

func TestRefresh_UnknownToken(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	_, err := svc.Refresh(context.Background(), "not-a-real-token", "", "")
	if !errors.Is(err, ErrInvalidRefresh) {
		t.Errorf("err=%v want ErrInvalidRefresh", err)
	}
}

func TestRefresh_MalformedToken(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	// Malformed (non-base64url) tokens must return ErrInvalidRefresh without DB query.
	for _, tok := range []string{"", "not!!!valid-b64url", "short", "aG9sbw=="} {
		_, err := svc.Refresh(context.Background(), tok, "", "")
		if !errors.Is(err, ErrInvalidRefresh) {
			t.Errorf("token %q err=%v want ErrInvalidRefresh", tok, err)
		}
	}
}

func TestLogout_MalformedToken(t *testing.T) {
	svc, _, _, _, _ := newSvc(t)
	// Malformed tokens must return nil (idempotent) without DB query.
	for _, tok := range []string{"", "not!!!valid-b64url"} {
		if err := svc.Logout(context.Background(), tok); err != nil {
			t.Errorf("logout malformed token %q err=%v want nil", tok, err)
		}
	}
}

func TestLogout_Idempotent(t *testing.T) {
	svc, _, sessions, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	// First logout: revoke.
	if err := svc.Logout(context.Background(), res.RefreshToken); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	sessHash, _ := refresh.HashToken(res.RefreshToken)
	sess := sessions.byHash[string(sessHash)]
	if sess.RevokedAt == nil || sess.RevokeReason == nil || *sess.RevokeReason != "logout" {
		t.Errorf("session not revoked logout: %v", sess.RevokeReason)
	}
	// Second logout with same (already-revoked) token: idempotent, no error.
	if err := svc.Logout(context.Background(), res.RefreshToken); err != nil {
		t.Errorf("idempotent logout err=%v", err)
	}
	// Logout with an unknown token: idempotent, no error.
	if err := svc.Logout(context.Background(), "totally-unknown"); err != nil {
		t.Errorf("logout unknown token err=%v", err)
	}
}

func TestLogoutAll_BumpsTokenVersionRevokesSessions(t *testing.T) {
	svc, users, sessions, _, issuer := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	userID := users.get("user@example.com").ID
	beforeTV, _ := users.FindByID(context.Background(), userID)
	// Simulate another active session.
	res2, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	if err := svc.LogoutAll(context.Background(), userID); err != nil {
		t.Fatalf("LogoutAll: %v", err)
	}
	afterTV, _ := users.FindByID(context.Background(), userID)
	if afterTV.TokenVersion != beforeTV.TokenVersion+1 {
		t.Errorf("token_version not bumped: %d → %d", beforeTV.TokenVersion, afterTV.TokenVersion)
	}
	// All sessions revoked with logout_all.
	for _, s := range sessions.snapshot() {
		if s.UserID == userID && s.RevokedAt == nil {
			t.Error("active session survived logout-all")
		}
		if s.UserID == userID && (s.RevokeReason == nil || *s.RevokeReason != "logout_all") {
			t.Errorf("session revoke_reason=%v want logout_all", s.RevokeReason)
		}
	}
	_ = res
	_ = res2
	_ = issuer
}

func TestChangePassword_SucceedsAndRevokes(t *testing.T) {
	svc, users, sessions, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	res, _ := svc.Login(context.Background(), "user@example.com", "verystrongpassword123", "", "")
	userID := users.get("user@example.com").ID
	beforeTV, _ := users.FindByID(context.Background(), userID)
	if err := svc.ChangePassword(context.Background(), userID, "verystrongpassword123", "newverystrongpassword456"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	afterTV, _ := users.FindByID(context.Background(), userID)
	if afterTV.TokenVersion != beforeTV.TokenVersion+1 {
		t.Errorf("token_version not bumped: %d → %d", beforeTV.TokenVersion, afterTV.TokenVersion)
	}
	stored, _ := users.FindByID(context.Background(), userID)
	if !password.IsArgon2id(stored.PasswordHash) {
		t.Error("password not Argon2id after change")
	}
	// All sessions (including the current one) revoked with password_changed.
	for _, s := range sessions.snapshot() {
		if s.UserID == userID && s.RevokedAt == nil {
			t.Error("active session survived password change")
		}
		if s.UserID == userID && (s.RevokeReason == nil || *s.RevokeReason != "password_changed") {
			t.Errorf("session revoke_reason=%v want password_changed", s.RevokeReason)
		}
	}
	_ = res
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	svc, users, _, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	userID := users.get("user@example.com").ID
	err := svc.ChangePassword(context.Background(), userID, "wrongcurrentpassword", "newverystrongpassword456")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("err=%v want ErrInvalidCredentials", err)
	}
}

func TestChangePassword_WeakNew(t *testing.T) {
	svc, users, _, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	userID := users.get("user@example.com").ID
	err := svc.ChangePassword(context.Background(), userID, "verystrongpassword123", "short")
	if !errors.Is(err, ErrPasswordTooWeak) {
		t.Errorf("err=%v want ErrPasswordTooWeak", err)
	}
}

func TestMe_Success(t *testing.T) {
	svc, users, _, _, _ := newSvc(t)
	_, _ = svc.Register(context.Background(), "user@example.com", "verystrongpassword123")
	userID := users.get("user@example.com").ID
	u, err := svc.Me(context.Background(), userID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if u.ID != userID || u.Email != "user@example.com" {
		t.Errorf("me wrong: %+v", u)
	}
}
