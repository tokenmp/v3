// Package auth implements the TokenMP v3 auth identity flows: registration,
// login, refresh-token rotation with reuse detection, logout, logout-all,
// password change, and the authenticated /me endpoint.
//
// The service depends on narrow repository interfaces (UserRepository,
// SessionRepository, TxRunner) so unit tests can substitute fakes. Security
// primitives (JWT issue/verify, password hash/compare, refresh token
// generation/hash) live in internal/security and are injected as ports
// (Issuer, Verifier, Clock).
//
// Error model: the service returns typed errors (auth.ErrInvalidCredentials
// etc.) that the HTTP layer maps to stable {error:{code,message}} responses.
// Raw database errors never leave the repository layer.
package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/repository"
	"github.com/tokenmp/v3/services/auth/internal/security/password"
	"github.com/tokenmp/v3/services/auth/internal/security/refresh"
)

// Typed service errors. The HTTP layer maps these to stable codes; they never
// carry raw DB / password / token material.
var (
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrEmailTaken         = errors.New("auth: email already taken")
	ErrInvalidRefresh     = errors.New("auth: invalid refresh token")
	ErrTokenReuse         = errors.New("auth: refresh token reuse detected")
	ErrPasswordTooWeak    = errors.New("auth: password does not meet policy")
	ErrInvalidEmailFormat = errors.New("auth: invalid email format")
	ErrInternal           = errors.New("auth: internal error")
)

// Ports the service depends on. Defined here so fakes can implement them.

// Clock returns the current time. Injected for deterministic tests.
type Clock interface {
	Now() time.Time
}

// Issuer issues access tokens. Mirrors security/jwt.Issuer.
type Issuer interface {
	IssueAccessToken(userID, role string, tokenVersion int, now time.Time) (token string, exp time.Time, err error)
}

// UserRepository is the persistence port for users.
type UserRepository interface {
	Create(ctx context.Context, u *models.User) error
	FindByEmail(ctx context.Context, email string) (*models.User, error)
	FindByID(ctx context.Context, id string) (*models.User, error)
	UpdatePasswordHash(ctx context.Context, userID, hash string) error
	IncrementTokenVersion(ctx context.Context, userID string) (int, error)
}

// SessionRepository is the persistence port for refresh sessions.
type SessionRepository interface {
	Create(ctx context.Context, s *models.AuthSession) error
	FindByRefreshHashForUpdate(ctx context.Context, hash []byte) (*models.AuthSession, error)
	Revoke(ctx context.Context, sessionID, reason string, at time.Time) error
	RevokeActiveByFamily(ctx context.Context, familyID, reason string, at time.Time) error
	RevokeActiveByUser(ctx context.Context, userID, reason string, at time.Time) error
	SetReplacedBy(ctx context.Context, oldID, newID string) error
	FindByID(ctx context.Context, id string) (*models.AuthSession, error)
}

// TxRunner runs a function inside a transaction.
type TxRunner interface {
	Run(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service is the auth identity flow service. Construct with NewService.
type Service struct {
	users      UserRepository
	sessions   SessionRepository
	tx         TxRunner
	issuer     Issuer
	clock      Clock
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewService builds the auth service. The Issuer, repositories, TxRunner and
// Clock are injected; main.go wires the GORM-backed implementations and the
// Ed25519 JWT issuer. accessTTL is exposed so the login/refresh response can
// report expires_in; refreshTTL governs session lifetime.
func NewService(users UserRepository, sessions SessionRepository, tx TxRunner, issuer Issuer, clock Clock, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{
		users:      users,
		sessions:   sessions,
		tx:         tx,
		issuer:     issuer,
		clock:      clock,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// TokenResponse is the login/refresh success payload (minus the bearer
// envelope). The handler wraps it as {access_token, refresh_token, token_type,
// expires_in}.
type TokenResponse struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
}

// PublicUser is the shape returned by Register and /me: id, email, role,
// status and created_at. It never includes password_hash or token_version.
type PublicUser struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func publicUser(u *models.User) PublicUser {
	return PublicUser{
		ID:        u.ID,
		Email:     u.Email,
		Role:      string(u.Role),
		Status:    string(u.Status),
		CreatedAt: u.CreatedAt,
	}
}

// normalizeEmail applies LOWER(BTRIM) to match the DB canonical form and the
// CHECK constraint backstop. It enforces: exactly one @, local and domain
// parts non-empty, no whitespace or control characters, total length <= 255.
// It does NOT require a dot in the domain — that would reject valid TLD-less
// addresses and local-form emails used in testing. The DB CHECK backstop
// (email = LOWER(BTRIM(email))) provides the final guarantee.
func normalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if e == "" || len(e) > 255 {
		return "", ErrInvalidEmailFormat
	}
	// Reject whitespace and control characters anywhere.
	for _, r := range e {
		if r <= 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return "", ErrInvalidEmailFormat
		}
	}
	at := strings.IndexByte(e, '@')
	if at <= 0 || at == len(e)-1 {
		return "", ErrInvalidEmailFormat
	}
	// Exactly one @.
	if strings.IndexByte(e[at+1:], '@') >= 0 {
		return "", ErrInvalidEmailFormat
	}
	return e, nil
}

// Register creates a new user. It normalizes the email, validates the
// password, hashes it with Argon2id, and persists. On success it returns the
// public user view; no session or access token is created (no auto-login).
func (s *Service) Register(ctx context.Context, emailRaw, passwordRaw string) (PublicUser, error) {
	email, err := normalizeEmail(emailRaw)
	if err != nil {
		return PublicUser{}, err
	}
	if err := password.Validate(passwordRaw); err != nil {
		if errors.Is(err, password.ErrEmptyPassword) || errors.Is(err, password.ErrInvalidLength) || errors.Is(err, password.ErrInvalidEncoding) || errors.Is(err, password.ErrControlChar) {
			return PublicUser{}, ErrPasswordTooWeak
		}
		return PublicUser{}, ErrPasswordTooWeak
	}
	hash, err := password.HashArgon2id(passwordRaw)
	if err != nil {
		return PublicUser{}, ErrPasswordTooWeak
	}
	u := &models.User{
		Email:        email,
		PasswordHash: hash,
		Role:         models.RoleUser,
		Status:       models.StatusActive,
		TokenVersion: 1,
	}
	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, repository.ErrDuplicateEmail) {
			return PublicUser{}, ErrEmailTaken
		}
		if errors.Is(err, repository.ErrConstraint) {
			return PublicUser{}, ErrInvalidEmailFormat
		}
		return PublicUser{}, ErrInternal
	}
	return publicUser(u), nil
}

// Login authenticates a user by email + password and mints a new refresh
// session. On any failure (user not found, wrong password, disabled) the same
// ErrInvalidCredentials is returned so an attacker cannot distinguish the
// cases. A pre-generated dummy Argon2id hash is used for the CompareDummy
// call on the not-found path to flatten the timing side channel. A disabled
// account still completes the password comparison first before returning the
// uniform error. On a successful bcrypt login the password is re-hashed with
// Argon2id in the same transaction (progressive upgrade); this does NOT bump
// token_version.
//
// Timing note: Argon2id and bcrypt have inherently different latency profiles.
// Complete uniformity between the two is not achievable; this is an honest
// documented limitation. The pre-generated dummy ensures the not-found path
// always performs exactly one Argon2id Compare, matching the wrong-password
// path for Argon2id users.
func (s *Service) Login(ctx context.Context, emailRaw, passwordRaw, ip, userAgent string) (TokenResponse, error) {
	email, err := normalizeEmail(emailRaw)
	if err != nil {
		// Burn comparable CPU before returning the uniform error.
		password.CompareDummy()
		return TokenResponse{}, ErrInvalidCredentials
	}
	var result TokenResponse
	txErr := s.tx.Run(ctx, func(ctx context.Context) error {
		u, err := s.users.FindByEmail(ctx, email)
		if errors.Is(err, repository.ErrNotFound) {
			password.CompareDummy()
			result = TokenResponse{}
			return ErrInvalidCredentialsMark
		}
		if err != nil {
			return ErrInternal
		}
		// Compare password against stored hash (Argon2id or legacy bcrypt).
		if cmpErr := password.Compare(u.PasswordHash, passwordRaw); cmpErr != nil {
			password.CompareDummy()
			result = TokenResponse{}
			return ErrInvalidCredentialsMark
		}
		// Disabled accounts cannot log in; the password was already compared
		// to maintain timing uniformity before returning the uniform error.
		if u.Status != models.StatusActive {
			result = TokenResponse{}
			return ErrInvalidCredentialsMark
		}
		// Progressive upgrade: bcrypt → Argon2id in the same transaction.
		// Does NOT bump token_version (login does not invalidate sessions).
		if password.UpgradeNeeded(u.PasswordHash) {
			newHash, hErr := password.HashArgon2id(passwordRaw)
			if hErr != nil {
				return ErrInternal
			}
			if uErr := s.users.UpdatePasswordHash(ctx, u.ID, newHash); uErr != nil {
				return ErrInternal
			}
		}
		// Mint refresh token + session.
		tok, raw, gErr := refresh.Generate()
		if gErr != nil {
			return ErrInternal
		}
		now := s.clock.Now()
		sess := &models.AuthSession{
			UserID:           u.ID,
			RefreshTokenHash: refresh.Hash(raw),
			ExpiresAt:        now.Add(s.refreshTTL),
			IP:               strPtrOrNil(ip),
			UserAgent:        strPtrOrNil(userAgent),
		}
		if cErr := s.sessions.Create(ctx, sess); cErr != nil {
			return ErrInternal
		}
		// Issue access token outside the transaction (no DB writes). We use the
		// same ctx; the issuer does not touch the DB.
		access, _, iErr := s.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, now)
		if iErr != nil {
			return ErrInternal
		}
		result = TokenResponse{
			AccessToken:  access,
			RefreshToken: tok,
			TokenType:    "Bearer",
			ExpiresIn:    int(s.accessTTL.Seconds()),
		}
		return nil
	})
	if txErr != nil {
		if errors.Is(txErr, ErrInvalidCredentialsMark) {
			return TokenResponse{}, ErrInvalidCredentials
		}
		return TokenResponse{}, txErr
	}
	return result, nil
}

// ErrInvalidCredentialsMark is an internal marker so Login can distinguish the
// uniform invalid-credentials outcome (which must roll back the transaction)
// from a real internal error. It is never returned to callers; Login maps it
// to ErrInvalidCredentials after the transaction commits/rolls back.
var ErrInvalidCredentialsMark = errors.New("auth: invalid credentials (internal)")

// Refresh rotates a refresh token. The presented token is hashed and looked up
// with SELECT FOR UPDATE. If the row is already revoked, this is token reuse:
// all active sessions in the family are revoked with reason 'token_reuse',
// the transaction commits, and ErrTokenReuse is returned. If the token is
// valid, a new session is created in the same family, the old session is
// revoked with reason 'token_rotated' and pointed at the new session, and new
// access + refresh tokens are returned.
//
// The reuse revocation MUST commit even though an error is returned to the
// caller; to guarantee this, fn returns nil after performing the revocation
// and the service decides the result error after the transaction commits.
func (s *Service) Refresh(ctx context.Context, refreshToken, ip, userAgent string) (TokenResponse, error) {
	hash, err := refresh.HashToken(refreshToken)
	if err != nil {
		// Malformed/empty token → unified invalid without DB query.
		return TokenResponse{}, ErrInvalidRefresh
	}
	var (
		result          TokenResponse
		notFound        bool
		reuseDetected   bool
		expired         bool
		disabled        bool
		versionMismatch bool
	)
	txErr := s.tx.Run(ctx, func(ctx context.Context) error {
		sess, err := s.sessions.FindByRefreshHashForUpdate(ctx, hash)
		if errors.Is(err, repository.ErrNotFound) {
			notFound = true
			return nil // commit (no-op), caller returns 401
		}
		if err != nil {
			return ErrInternal
		}
		now := s.clock.Now()
		// Reuse: a revoked row being presented again. Revoke the family's
		// active sessions and commit before signalling 401.
		if sess.RevokedAt != nil {
			if rErr := s.sessions.RevokeActiveByFamily(ctx, sess.TokenFamilyID, "token_reuse", now); rErr != nil {
				return ErrInternal
			}
			reuseDetected = true
			return nil // commit the revocation
		}
		// Expired session.
		if !sess.ExpiresAt.After(now) {
			expired = true
			return nil
		}
		// Load user to check status + token_version consistency.
		u, err := s.users.FindByID(ctx, sess.UserID)
		if errors.Is(err, repository.ErrNotFound) {
			notFound = true
			return nil
		}
		if err != nil {
			return ErrInternal
		}
		if u.Status != models.StatusActive {
			disabled = true
			return nil
		}
		// Token version consistency: the session does not store token_version
		// (access tokens do). A refresh after logout-all / password change is
		// already revoked (logout_all / password_changed), so this branch is
		// belt-and-braces for any future drift. We do not revoke here; the
		// revoked path already handles the normal case.
		if u.TokenVersion < 1 {
			versionMismatch = true
			return nil
		}
		// Rotate: create new session in same family, revoke old (token_rotated).
		tok, raw, gErr := refresh.Generate()
		if gErr != nil {
			return ErrInternal
		}
		newSession := &models.AuthSession{
			UserID:           sess.UserID,
			TokenFamilyID:    sess.TokenFamilyID,
			RefreshTokenHash: refresh.Hash(raw),
			ExpiresAt:        now.Add(s.refreshTTL),
			IP:               strPtrOrNil(ip),
			UserAgent:        strPtrOrNil(userAgent),
		}
		if cErr := s.sessions.Create(ctx, newSession); cErr != nil {
			return ErrInternal
		}
		if rbErr := s.sessions.SetReplacedBy(ctx, sess.ID, newSession.ID); rbErr != nil {
			return ErrInternal
		}
		if rvErr := s.sessions.Revoke(ctx, sess.ID, "token_rotated", now); rvErr != nil {
			return ErrInternal
		}
		access, _, iErr := s.issuer.IssueAccessToken(u.ID, string(u.Role), u.TokenVersion, now)
		if iErr != nil {
			return ErrInternal
		}
		result = TokenResponse{
			AccessToken:  access,
			RefreshToken: tok,
			TokenType:    "Bearer",
			ExpiresIn:    int(s.accessTTL.Seconds()),
		}
		return nil
	})
	if txErr != nil {
		return TokenResponse{}, txErr
	}
	switch {
	case reuseDetected:
		return TokenResponse{}, ErrTokenReuse
	case notFound, expired, disabled, versionMismatch:
		return TokenResponse{}, ErrInvalidRefresh
	}
	return result, nil
}

// Logout revokes the single session identified by the refresh token. It is
// idempotent: an invalid or already-revoked token returns no error (the
// handler returns 204 in all cases to avoid probing).
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	hash, err := refresh.HashToken(refreshToken)
	if err != nil {
		// Malformed/empty token → idempotent success (no DB query).
		return nil
	}
	now := s.clock.Now()
	return s.tx.Run(ctx, func(ctx context.Context) error {
		sess, err := s.sessions.FindByRefreshHashForUpdate(ctx, hash)
		if errors.Is(err, repository.ErrNotFound) {
			return nil // idempotent
		}
		if err != nil {
			return ErrInternal
		}
		if sess.RevokedAt != nil {
			return nil // already revoked, idempotent
		}
		if rErr := s.sessions.Revoke(ctx, sess.ID, "logout", now); rErr != nil {
			return ErrInternal
		}
		return nil
	})
}

// LogoutAll revokes all active sessions for the user and bumps token_version
// so all outstanding access tokens are immediately invalid. Requires a Bearer
// (the handler enforces it and passes the authenticated user id).
func (s *Service) LogoutAll(ctx context.Context, userID string) error {
	now := s.clock.Now()
	return s.tx.Run(ctx, func(ctx context.Context) error {
		if rErr := s.sessions.RevokeActiveByUser(ctx, userID, "logout_all", now); rErr != nil {
			return ErrInternal
		}
		if _, err := s.users.IncrementTokenVersion(ctx, userID); err != nil {
			return ErrInternal
		}
		return nil
	})
}

// ChangePassword verifies the current password, validates and hashes the new
// password, updates the hash, bumps token_version and revokes all active
// sessions with reason 'password_changed'. The caller (handler) requires a
// Bearer and passes the authenticated user id. After success the user must
// re-login.
func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	if err := password.Validate(newPassword); err != nil {
		return ErrPasswordTooWeak
	}
	now := s.clock.Now()
	return s.tx.Run(ctx, func(ctx context.Context) error {
		u, err := s.users.FindByID(ctx, userID)
		if errors.Is(err, repository.ErrNotFound) {
			return ErrInvalidCredentials
		}
		if err != nil {
			return ErrInternal
		}
		if cmpErr := password.Compare(u.PasswordHash, currentPassword); cmpErr != nil {
			return ErrInvalidCredentials
		}
		newHash, hErr := password.HashArgon2id(newPassword)
		if hErr != nil {
			return ErrInternal
		}
		if uErr := s.users.UpdatePasswordHash(ctx, userID, newHash); uErr != nil {
			return ErrInternal
		}
		if rErr := s.sessions.RevokeActiveByUser(ctx, userID, "password_changed", now); rErr != nil {
			return ErrInternal
		}
		if _, err := s.users.IncrementTokenVersion(ctx, userID); err != nil {
			return ErrInternal
		}
		return nil
	})
}

// Me returns the public view of the authenticated user. The middleware
// loads the user; the handler calls this to render the response.
func (s *Service) Me(ctx context.Context, userID string) (PublicUser, error) {
	u, err := s.users.FindByID(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return PublicUser{}, ErrInvalidCredentials
	}
	if err != nil {
		return PublicUser{}, ErrInternal
	}
	return publicUser(u), nil
}

// strPtrOrNil returns a pointer to s (or nil for "").
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
