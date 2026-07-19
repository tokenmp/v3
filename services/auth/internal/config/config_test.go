package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	validURL = "postgres://user:pass@localhost:5432/tokenmp_auth?sslmode=disable"
)

func setEnvs(t *testing.T, envs map[string]string) {
	t.Helper()
	for k, v := range envs {
		t.Setenv(k, v)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", "")
	unsetAuthEnvsExcept(t)
	if _, err := Load(); !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("expected ErrMissingDatabaseURL, got %v", err)
	}
}

func TestLoad_BlankDatabaseURL(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", "   ")
	unsetAuthEnvsExcept(t)
	if _, err := Load(); !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("expected ErrMissingDatabaseURL for blank, got %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", validURL)
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.LogFormat)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.DBMaxOpenConns != 25 {
		t.Errorf("DBMaxOpenConns = %d, want 25", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 5 {
		t.Errorf("DBMaxIdleConns = %d, want 5", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != 30*time.Minute {
		t.Errorf("DBConnMaxLifetime = %s, want 30m", cfg.DBConnMaxLifetime)
	}
	if cfg.JWTIssuer != "tokenmp-auth" {
		t.Errorf("JWTIssuer = %q, want tokenmp-auth", cfg.JWTIssuer)
	}
	if cfg.JWTAudience != "tokenmp-web" {
		t.Errorf("JWTAudience = %q, want tokenmp-web", cfg.JWTAudience)
	}
	if cfg.AccessTokenTTL != 15*time.Minute {
		t.Errorf("AccessTokenTTL = %s, want 15m", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 30*24*time.Hour {
		t.Errorf("RefreshTokenTTL = %s, want 720h", cfg.RefreshTokenTTL)
	}
	if cfg.JWTPrivateKeyFile != "" {
		t.Errorf("JWTPrivateKeyFile = %q, want empty", cfg.JWTPrivateKeyFile)
	}
	if cfg.JWTPublicKeyFile != "" {
		t.Errorf("JWTPublicKeyFile = %q, want empty", cfg.JWTPublicKeyFile)
	}
}

func TestLoad_Overrides(t *testing.T) {
	setEnvs(t, map[string]string{
		"AUTH_DATABASE_URL":         "postgres://u:p@db:5432/tokenmp_auth?sslmode=disable",
		"AUTH_HTTP_ADDR":            ":9090",
		"AUTH_LOG_LEVEL":            "DEBUG",
		"AUTH_LOG_FORMAT":           "Text",
		"AUTH_SHUTDOWN_TIMEOUT":     "15s",
		"AUTH_DB_MAX_OPEN_CONNS":    "10",
		"AUTH_DB_MAX_IDLE_CONNS":    "2",
		"AUTH_DB_CONN_MAX_LIFETIME": "10m",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("LogFormat = %q, want text", cfg.LogFormat)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 15s", cfg.ShutdownTimeout)
	}
	if cfg.DBMaxOpenConns != 10 {
		t.Errorf("DBMaxOpenConns = %d, want 10", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 2 {
		t.Errorf("DBMaxIdleConns = %d, want 2", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime != 10*time.Minute {
		t.Errorf("DBConnMaxLifetime = %s, want 10m", cfg.DBConnMaxLifetime)
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", validURL)
	t.Setenv("AUTH_LOG_LEVEL", "trace")
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL", "AUTH_LOG_LEVEL")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid log level")
	}
}

func TestLoad_InvalidLogFormat(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", validURL)
	t.Setenv("AUTH_LOG_FORMAT", "yaml")
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL", "AUTH_LOG_FORMAT")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid log format")
	}
}

// TestLoad_InvalidDurationsAndInts asserts that invalid numeric/duration
// values fail fast. They must NOT silently fall back to the default — a
// misconfigured pool size or timeout must be caught at startup.
func TestLoad_InvalidDurationsAndInts(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"AUTH_SHUTDOWN_TIMEOUT", "not-a-duration"},
		{"AUTH_DB_MAX_OPEN_CONNS", "not-an-int"},
		{"AUTH_DB_MAX_IDLE_CONNS", "xyz"},
		{"AUTH_DB_CONN_MAX_LIFETIME", "abc"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			t.Setenv("AUTH_DATABASE_URL", validURL)
			unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL", c.key)
			t.Setenv(c.key, c.value)
			if _, err := Load(); err == nil {
				t.Fatalf("invalid %s value must fail fast, not fall back", c.key)
			}
		})
	}
}

// TestLoad_JWTTTLValidation covers JWT TTL constraints: access TTL must be > 0,
// refresh TTL must be > 0, and refresh TTL must be greater than access TTL.
func TestLoad_JWTTTLValidation(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", validURL)
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL")

	t.Run("zero access TTL", func(t *testing.T) {
		t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "0s")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for zero access TTL")
		}
	})

	t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "") // reset

	t.Run("negative access TTL", func(t *testing.T) {
		t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "-5m")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for negative access TTL")
		}
	})

	t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "") // reset

	t.Run("zero refresh TTL", func(t *testing.T) {
		t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "0s")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for zero refresh TTL")
		}
	})

	t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "") // reset

	t.Run("refresh less than access", func(t *testing.T) {
		t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "1h")
		t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "30m")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for refresh TTL <= access TTL")
		}
	})

	t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "")
	t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "")

	t.Run("refresh equal to access", func(t *testing.T) {
		t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "15m")
		t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "15m")
		if _, err := Load(); err == nil {
			t.Fatal("expected error for refresh TTL == access TTL")
		}
	})

	t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "")
	t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "")

	t.Run("refresh greater than access ok", func(t *testing.T) {
		t.Setenv("AUTH_JWT_ACCESS_TOKEN_TTL", "15m")
		t.Setenv("AUTH_JWT_REFRESH_TOKEN_TTL", "1h")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AccessTokenTTL != 15*time.Minute {
			t.Errorf("AccessTokenTTL = %s, want 15m", cfg.AccessTokenTTL)
		}
		if cfg.RefreshTokenTTL != time.Hour {
			t.Errorf("RefreshTokenTTL = %s, want 1h", cfg.RefreshTokenTTL)
		}
	})
}

func TestLoad_NegativeValuesRejected(t *testing.T) {
	cases := []struct {
		key   string
		value string
	}{
		{"AUTH_SHUTDOWN_TIMEOUT", "-1s"},
		{"AUTH_DB_MAX_OPEN_CONNS", "-1"},
		{"AUTH_DB_MAX_IDLE_CONNS", "-2"},
		{"AUTH_DB_CONN_MAX_LIFETIME", "-5s"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			t.Setenv("AUTH_DATABASE_URL", validURL)
			unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL", c.key)
			t.Setenv(c.key, c.value)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for negative %s", c.key)
			}
		})
	}
}

// TestLoad_DatabaseURLValidation covers the strict connection contract. The
// auth service must only connect to a postgres URL with host, non-empty user
// and the exact database path /tokenmp_auth.
func TestLoad_DatabaseURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr error
	}{
		{"postgres scheme ok", "postgres://u:p@host:5432/tokenmp_auth", nil},
		{"postgresql scheme ok", "postgresql://u:p@host:5432/tokenmp_auth", nil},
		{"no scheme", "u:p@host:5432/tokenmp_auth", ErrInvalidDatabaseURL},
		{"wrong scheme mysql", "mysql://u:p@host:3306/tokenmp_auth", ErrInvalidDatabaseURL},
		{"wrong scheme http", "http://u:p@host:5432/tokenmp_auth", ErrInvalidDatabaseURL},
		{"missing host", "postgres://u:p@/tokenmp_auth", ErrInvalidDatabaseURL},
		{"missing user", "postgres://host:5432/tokenmp_auth", ErrInvalidDatabaseURL},
		{"empty user", "postgres://:p@host:5432/tokenmp_auth", ErrInvalidDatabaseURL},
		{"wrong database name", "postgres://u:p@host:5432/other_db", ErrInvalidDatabaseURL},
		{"empty path", "postgres://u:p@host:5432", ErrInvalidDatabaseURL},
		{"trailing slash", "postgres://u:p@host:5432/tokenmp_auth/", ErrInvalidDatabaseURL},
		{"subpath", "postgres://u:p@host:5432/tokenmp_auth/extra", ErrInvalidDatabaseURL},
		{"malformed", "://not a url", ErrInvalidDatabaseURL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AUTH_DATABASE_URL", c.url)
			unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL")
			_, err := Load()
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("expected %v, got %v", c.wantErr, err)
			}
		})
	}
}

// TestLoad_DatabaseURLErrorsDoNotLeakSecrets ensures that no validation error
// text contains the URL, its credentials, the host or the path. This protects
// against echoing the secret-bearing value in logs when the error is logged.
func TestLoad_DatabaseURLErrorsDoNotLeakSecrets(t *testing.T) {
	const (
		secretUser = "supersecretuser"
		secretPass = "supersecretpass"
		secretHost = "db.internal.example.invalid"
	)
	// Wrong database name exercises ErrInvalidDatabaseURL while the URL still
	// carries real-looking secrets.
	leakingURL := "postgres://" + secretUser + ":" + secretPass + "@" + secretHost + ":5432/wrongdb"
	t.Setenv("AUTH_DATABASE_URL", leakingURL)
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for wrong database name")
	}
	msg := err.Error()
	for _, needle := range []string{leakingURL, secretUser, secretPass, secretHost, "wrongdb", "5432"} {
		if strings.Contains(msg, needle) {
			t.Errorf("error leaked secret fragment %q: %s", needle, msg)
		}
	}
}

// unsetAuthEnvs unsets all optional AUTH_* env vars so the test controls the
// surface. AUTH_DATABASE_URL is intentionally not touched here because every
// test sets it explicitly.
func unsetAuthEnvs(t *testing.T) {
	unsetAuthEnvsExcept(t)
}

func unsetAuthEnvsExcept(t *testing.T, keep ...string) {
	t.Helper()
	keepSet := make(map[string]struct{}, len(keep))
	for _, k := range keep {
		keepSet[k] = struct{}{}
	}
	all := []string{
		"AUTH_HTTP_ADDR",
		"AUTH_LOG_LEVEL",
		"AUTH_LOG_FORMAT",
		"AUTH_SHUTDOWN_TIMEOUT",
		"AUTH_DB_MAX_OPEN_CONNS",
		"AUTH_DB_MAX_IDLE_CONNS",
		"AUTH_DB_CONN_MAX_LIFETIME",
		// JWT environment variables — cleared so tests don't leak from the
		// host environment and don't accidentally pick up local .env values.
		"AUTH_JWT_PRIVATE_KEY_FILE",
		"AUTH_JWT_PUBLIC_KEY_FILE",
		"AUTH_JWT_ISSUER",
		"AUTH_JWT_AUDIENCE",
		"AUTH_JWT_ACCESS_TOKEN_TTL",
		"AUTH_JWT_REFRESH_TOKEN_TTL",
	}
	for _, k := range all {
		if _, ok := keepSet[k]; ok {
			continue
		}
		t.Setenv(k, "")
	}
}
