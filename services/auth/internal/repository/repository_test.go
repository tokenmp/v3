package repository

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/tokenmp/v3/services/auth/internal/database"
	"github.com/tokenmp/v3/services/auth/internal/database/models"
	"github.com/tokenmp/v3/services/auth/internal/security/apikey"
)

func TestClassify_Nil(t *testing.T) {
	if err := classify(nil); err != nil {
		t.Errorf("classify(nil) = %v, want nil", err)
	}
}

func TestClassify_GormErrRecordNotFound(t *testing.T) {
	if err := classify(gorm.ErrRecordNotFound); !errors.Is(err, ErrNotFound) {
		t.Errorf("classify(ErrRecordNotFound) = %v, want ErrNotFound", err)
	}
}

func TestClassify_PgError_UniqueViolation_EmailConstraint(t *testing.T) {
	cases := []struct {
		name           string
		constraintName string
		want           error
	}{
		{"users_email_unique_idx", "users_email_unique_idx", ErrDuplicateEmail},
		{"users_email_key", "users_email_key", ErrDuplicateEmail},
		{"unknown unique", "some_other_idx", ErrConstraint},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pgErr := &pgconn.PgError{
				Code:           "23505",
				ConstraintName: c.constraintName,
			}
			got := classify(pgErr)
			if !errors.Is(got, c.want) {
				t.Errorf("classify(pgErr 23505 %q) = %v, want %v", c.constraintName, got, c.want)
			}
		})
	}
}

func TestClassify_PgError_UniqueViolation_SecretHash(t *testing.T) {
	for _, constraintName := range []string{
		"auth_sessions_refresh_token_hash_unique_idx",
		"auth_sessions_refresh_token_hash_key",
		"api_keys_key_hash_unique_idx",
		"api_keys_key_hash_key",
	} {
		t.Run(constraintName, func(t *testing.T) {
			pgErr := &pgconn.PgError{
				Code:           "23505",
				ConstraintName: constraintName,
			}
			got := classify(pgErr)
			if !errors.Is(got, ErrConstraint) {
				t.Errorf("classify(23505 %s) = %v, want ErrConstraint", constraintName, got)
			}
		})
	}
}

func TestClassify_PgError_ForeignKeyViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23503"}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23503) = %v, want ErrConstraint", got)
	}
}

func TestClassify_PgError_CheckViolation(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23514"}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23514) = %v, want ErrConstraint", got)
	}
}

func TestClassify_PgError_OtherCode(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "08006"} // connection_failure
	got := classify(pgErr)
	if !errors.Is(got, ErrInternal) {
		t.Errorf("classify(08006) = %v, want ErrInternal", got)
	}
}

func TestClassify_PgError_UniqueViolation_DefaultConstraint(t *testing.T) {
	// A 23505 with an unrecognized constraint name must map to ErrConstraint,
	// not ErrDuplicateEmail.
	pgErr := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "users_some_other_col_key",
	}
	got := classify(pgErr)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(23505 unknown constraint) = %v, want ErrConstraint", got)
	}
	if errors.Is(got, ErrDuplicateEmail) {
		t.Error("unexpectedly matched ErrDuplicateEmail for non-email constraint")
	}
}

func TestClassify_GormErrDuplicatedKey(t *testing.T) {
	// GORM's ErrDuplicatedKey cannot distinguish which constraint was violated,
	// so it maps to the generic ErrConstraint.
	got := classify(gorm.ErrDuplicatedKey)
	if !errors.Is(got, ErrConstraint) {
		t.Errorf("classify(ErrDuplicatedKey) = %v, want ErrConstraint", got)
	}
}

func TestClassify_UnknownError(t *testing.T) {
	got := classify(errors.New("something unexpected"))
	if !errors.Is(got, ErrInternal) {
		t.Errorf("classify(unknown) = %v, want ErrInternal", got)
	}
}

func TestClassify_PgError_Wrapped(t *testing.T) {
	// errors.As must unwrap to find the pgconn.PgError inside a wrapped error.
	inner := &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "users_email_unique_idx",
	}
	wrapped := wrappedErr{err: inner}
	got := classify(wrapped)
	if !errors.Is(got, ErrDuplicateEmail) {
		t.Errorf("classify(wrapped pgErr) = %v, want ErrDuplicateEmail", got)
	}
}

// wrappedErr simulates a one-level error wrapper so errors.As must unwrap.
type wrappedErr struct{ err error }

func (e wrappedErr) Error() string { return "wrapped: " + e.err.Error() }
func (e wrappedErr) Unwrap() error { return e.err }

func TestAPIKeyRepository_Flow(t *testing.T) {
	// Repository integration runs only after the CI migration cycle. Keeping
	// its DSN separate from AUTH_DATABASE_URL prevents the ordinary unit-test
	// phase from connecting to a database before schema setup.
	dsn := os.Getenv("AUTH_REPOSITORY_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTH_REPOSITORY_TEST_DSN not set; skipping repository integration test")
	}

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		DatabaseURL:     dsn,
		MaxOpenConns:    5,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(db) })

	users := NewUserRepository(db)
	keys := NewAPIKeyRepository(db)
	user := &models.User{
		Email:        "apikey-repository-" + time.Now().UTC().Format("20060102150405.000000000") + "@example.com",
		PasswordHash: "test-password-hash",
		Role:         models.RoleUser,
		Status:       models.StatusActive,
		TokenVersion: 1,
	}
	if err := users.Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	fullKey, hash, err := apikey.Generate()
	if err != nil {
		t.Fatalf("apikey.Generate: %v", err)
	}
	key := &models.APIKey{
		UserID:    user.ID,
		Name:      "repository flow",
		KeyHash:   hash,
		KeyPrefix: apikey.Prefix(fullKey),
		KeySuffix: apikey.Suffix(fullKey),
		Role:      models.RoleUser,
		Status:    "active",
	}
	if err := keys.Create(ctx, key); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if key.ID == "" {
		t.Fatal("Create did not return database-generated API key ID")
	}

	found, err := keys.FindByHash(ctx, hash)
	if err != nil {
		t.Fatalf("FindByHash: %v", err)
	}
	if found.ID != key.ID || found.UserID != user.ID || found.Status != "active" {
		t.Errorf("FindByHash returned %+v, want active key %s for user %s", found, key.ID, user.ID)
	}

	listed, err := keys.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != key.ID {
		t.Fatalf("ListByUser = %+v, want only key %s", listed, key.ID)
	}

	before := time.Now().UTC()
	if err := keys.UpdateLastUsed(ctx, key.ID); err != nil {
		t.Fatalf("UpdateLastUsed: %v", err)
	}
	used, err := keys.FindByHash(ctx, hash)
	if err != nil {
		t.Fatalf("FindByHash after UpdateLastUsed: %v", err)
	}
	if used.LastUsedAt == nil || used.LastUsedAt.Before(before) {
		t.Errorf("LastUsedAt = %v, want a timestamp after %v", used.LastUsedAt, before)
	}
	if err := keys.UpdateLastUsed(ctx, "00000000-0000-0000-0000-000000000000"); err != nil {
		t.Errorf("UpdateLastUsed missing key = %v, want no-op", err)
	}

	if err := keys.Revoke(ctx, key.ID, user.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := keys.Revoke(ctx, key.ID, user.ID); err != nil {
		t.Errorf("second Revoke = %v, want idempotent no-op", err)
	}
	if _, err := keys.FindByHash(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByHash revoked key = %v, want ErrNotFound", err)
	}
	listed, err = keys.ListByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListByUser after revoke: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("ListByUser after revoke = %+v, want no keys", listed)
	}
	if _, err := keys.FindByHash(ctx, []byte("not-a-real-hash")); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByHash missing key = %v, want ErrNotFound", err)
	}

	duplicate := &models.APIKey{
		UserID:    user.ID,
		Name:      "duplicate hash",
		KeyHash:   hash,
		KeyPrefix: apikey.Prefix(fullKey),
		KeySuffix: apikey.Suffix(fullKey),
		Role:      models.RoleUser,
		Status:    "active",
	}
	if err := keys.Create(ctx, duplicate); !errors.Is(err, ErrConstraint) {
		t.Errorf("Create duplicate key hash = %v, want ErrConstraint", err)
	}
}

// TestAPIKeyRepository_Management 覆盖密钥管理方法：FindByIDForUser、
// UpdateFields、Rotate，以及跨用户隔离与已吊销可见性。仅在 CI 集成环境运行。
func TestAPIKeyRepository_Management(t *testing.T) {
	dsn := os.Getenv("AUTH_REPOSITORY_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTH_REPOSITORY_TEST_DSN not set; skipping repository integration test")
	}

	ctx := context.Background()
	db, err := database.Open(ctx, database.Config{
		DatabaseURL:     dsn,
		MaxOpenConns:    5,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(db) })

	users := NewUserRepository(db)
	keys := NewAPIKeyRepository(db)
	stamp := time.Now().UTC().Format("20060102150405.000000000")

	mkUser := func(email string) *models.User {
		u := &models.User{
			Email:        email + stamp + "@example.com",
			PasswordHash: "test-password-hash",
			Role:         models.RoleUser,
			Status:       models.StatusActive,
			TokenVersion: 1,
		}
		if err := users.Create(ctx, u); err != nil {
			t.Fatalf("create user: %v", err)
		}
		return u
	}
	owner := mkUser("apikey-mgmt-owner-")
	other := mkUser("apikey-mgmt-other-")

	fullKey, hash, err := apikey.Generate()
	if err != nil {
		t.Fatalf("apikey.Generate: %v", err)
	}
	key := &models.APIKey{
		UserID:    owner.ID,
		Name:      "manage me",
		KeyHash:   hash,
		KeyPrefix: apikey.Prefix(fullKey),
		KeySuffix: apikey.Suffix(fullKey),
		Role:      models.RoleUser,
		Status:    "active",
	}
	if err := keys.Create(ctx, key); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// FindByIDForUser：属主可见，他人不可见。
	got, err := keys.FindByIDForUser(ctx, key.ID, owner.ID)
	if err != nil {
		t.Fatalf("FindByIDForUser owner: %v", err)
	}
	if got.ID != key.ID {
		t.Errorf("FindByIDForUser = %s, want %s", got.ID, key.ID)
	}
	if _, err := keys.FindByIDForUser(ctx, key.ID, other.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByIDForUser other user = %v, want ErrNotFound", err)
	}

	// UpdateFields：改名 + 禁用，再回读校验。
	if err := keys.UpdateFields(ctx, key.ID, owner.ID, map[string]any{
		"name":   "renamed",
		"status": "disabled",
	}); err != nil {
		t.Fatalf("UpdateFields: %v", err)
	}
	got, err = keys.FindByIDForUser(ctx, key.ID, owner.ID)
	if err != nil {
		t.Fatalf("FindByIDForUser after update: %v", err)
	}
	if got.Name != "renamed" || got.Status != "disabled" {
		t.Errorf("after update = name %q status %q, want renamed/disabled", got.Name, got.Status)
	}

	// UpdateFields：跨用户失败。
	if err := keys.UpdateFields(ctx, key.ID, other.ID, map[string]any{"name": "stolen"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateFields other user = %v, want ErrNotFound", err)
	}

	// Rotate：新哈希/前缀/后缀，状态重激活。
	newFull, newHash, err := apikey.Generate()
	if err != nil {
		t.Fatalf("apikey.Generate rotate: %v", err)
	}
	if err := keys.Rotate(ctx, key.ID, owner.ID, newHash, apikey.Prefix(newFull), apikey.Suffix(newFull)); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	rotated, err := keys.FindByHash(ctx, newHash)
	if err != nil {
		t.Fatalf("FindByHash after rotate: %v", err)
	}
	if rotated.ID != key.ID || rotated.Status != "active" {
		t.Errorf("rotated = id %s status %q, want %s/active", rotated.ID, rotated.Status, key.ID)
	}
	if rotated.KeyPrefix != apikey.Prefix(newFull) || rotated.KeySuffix != apikey.Suffix(newFull) {
		t.Errorf("rotated prefix/suffix = %q/%q, want new material", rotated.KeyPrefix, rotated.KeySuffix)
	}
	// 旧哈希失效。
	if _, err := keys.FindByHash(ctx, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByHash old hash after rotate = %v, want ErrNotFound", err)
	}

	// Rotate 跨用户失败。
	if err := keys.Rotate(ctx, key.ID, other.ID, hash, "p", "s"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Rotate other user = %v, want ErrNotFound", err)
	}

	// 已吊销不可见。
	if err := keys.Revoke(ctx, key.ID, owner.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := keys.FindByIDForUser(ctx, key.ID, owner.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByIDForUser revoked = %v, want ErrNotFound", err)
	}
	if err := keys.UpdateFields(ctx, key.ID, owner.ID, map[string]any{"name": "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateFields revoked = %v, want ErrNotFound", err)
	}
}
