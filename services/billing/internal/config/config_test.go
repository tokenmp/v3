package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validURL = "postgres://user:pass@localhost:5432/tokenmp_billing?sslmode=disable"

func unsetExcept(t *testing.T, keep ...string) {
	t.Helper()
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	for _, k := range []string{
		"BILLING_DATABASE_URL", "BILLING_HTTP_ADDR", "BILLING_LOG_LEVEL", "BILLING_LOG_FORMAT",
		"BILLING_SHUTDOWN_TIMEOUT", "BILLING_DB_MAX_OPEN_CONNS", "BILLING_DB_MAX_IDLE_CONNS",
		"BILLING_DB_CONN_MAX_LIFETIME", "BILLING_SWEEPER_ENABLED", "BILLING_SWEEPER_INTERVAL",
		"BILLING_SWEEPER_PENDING_GRACE", "BILLING_SWEEPER_EXPIRY_BATCH", "BILLING_SWEEPER_PENDING_BATCH",
		"BILLING_SWEEPER_RETENTION_DEADLINE", "BILLING_SWEEPER_UNKNOWN_POLICY",
		"BILLING_LOGGING_URL", "BILLING_LOGGING_SERVICE_TOKEN", "BILLING_LOGGING_SERVICE_TOKEN_FILE", "BILLING_LOGGING_TIMEOUT",
	} {
		if !keepSet[k] {
			t.Setenv(k, "")
		}
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", "")
	unsetExcept(t)
	if _, err := Load(); !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("expected ErrMissingDatabaseURL, got %v", err)
	}
}

func TestLoad_InvalidScheme(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", "mysql://u:p@localhost/tokenmp_billing")
	unsetExcept(t, "BILLING_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("expected ErrInvalidDatabaseURL for mysql scheme, got %v", err)
	}
}

func TestLoad_WrongDatabaseName(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", "postgres://u:p@localhost:5432/tokenmp_config")
	unsetExcept(t, "BILLING_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("expected ErrInvalidDatabaseURL for wrong db name, got %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":8085" {
		t.Errorf("HTTPAddr = %q, want :8085", cfg.HTTPAddr)
	}
	if cfg.DBMaxOpenConns != 10 {
		t.Errorf("DBMaxOpenConns = %d, want 10", cfg.DBMaxOpenConns)
	}
}

func TestLoad_SocketForm(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", "postgres:///tokenmp_billing?host=/tmp&port=55433")
	unsetExcept(t, "BILLING_DATABASE_URL")
	if _, err := Load(); err != nil {
		t.Fatalf("socket form must be accepted, got %v", err)
	}
}

func TestLoad_SocketFormMissingHost(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", "postgres:///tokenmp_billing?port=55433")
	unsetExcept(t, "BILLING_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("socket form without host= must be rejected, got %v", err)
	}
}

func TestLoad_SweeperFailFast(t *testing.T) {
	cases := []struct {
		key string
		val string
	}{
		{"BILLING_SWEEPER_ENABLED", "maybe"},
		{"BILLING_SWEEPER_INTERVAL", "not-a-duration"},
		{"BILLING_SWEEPER_PENDING_GRACE", "xyz"},
		{"BILLING_SWEEPER_EXPIRY_BATCH", "0"},
		{"BILLING_SWEEPER_PENDING_BATCH", "-1"},
		{"BILLING_SWEEPER_RETENTION_DEADLINE", "0s"},
		{"BILLING_LOGGING_TIMEOUT", "0s"},
		{"BILLING_SWEEPER_UNKNOWN_POLICY", "release_everything"},
	}
	for _, tc := range cases {
		t.Run(tc.key+"="+tc.val, func(t *testing.T) {
			t.Setenv("BILLING_DATABASE_URL", validURL)
			unsetExcept(t, "BILLING_DATABASE_URL")
			t.Setenv(tc.key, tc.val)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s=%s, got nil", tc.key, tc.val)
			}
		})
	}
}

func TestLoad_RetentionMustExceedGrace(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	t.Setenv("BILLING_SWEEPER_PENDING_GRACE", "2m")
	t.Setenv("BILLING_SWEEPER_RETENTION_DEADLINE", "2m")
	if _, err := Load(); err == nil {
		t.Fatalf("expected error when retention == grace")
	}
}

func TestLoad_NewDefaults(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RetentionDeadline != 30*time.Minute {
		t.Errorf("RetentionDeadline = %s, want 30m", cfg.RetentionDeadline)
	}
	if cfg.UnknownPolicy != "keep_pending" {
		t.Errorf("UnknownPolicy = %q, want keep_pending", cfg.UnknownPolicy)
	}
	if cfg.LoggingTimeout != 10*time.Second {
		t.Errorf("LoggingTimeout = %s, want 10s", cfg.LoggingTimeout)
	}
	if cfg.LoggingURL != "" {
		t.Errorf("LoggingURL = %q, want empty default", cfg.LoggingURL)
	}
}

func TestLoad_LoggingTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("  sekret-token-123  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	t.Setenv("BILLING_LOGGING_URL", "http://logging:8083")
	t.Setenv("BILLING_LOGGING_SERVICE_TOKEN_FILE", tokenFile)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LoggingServiceToken != "sekret-token-123" {
		t.Errorf("LoggingServiceToken = %q, want trimmed sekret-token-123", cfg.LoggingServiceToken)
	}
}

func TestLoad_LoggingTokenInline(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	t.Setenv("BILLING_LOGGING_URL", "http://logging:8083")
	t.Setenv("BILLING_LOGGING_SERVICE_TOKEN", "inline-token")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LoggingServiceToken != "inline-token" {
		t.Errorf("LoggingServiceToken = %q, want inline-token", cfg.LoggingServiceToken)
	}
}

func TestLoad_LoggingTokenConflict(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	t.Setenv("BILLING_LOGGING_URL", "http://logging:8083")
	t.Setenv("BILLING_LOGGING_SERVICE_TOKEN_FILE", tokenFile)
	t.Setenv("BILLING_LOGGING_SERVICE_TOKEN", "inline-token")
	if _, err := Load(); !errors.Is(err, ErrLoggingTokenConflict) {
		t.Fatalf("expected ErrLoggingTokenConflict, got %v", err)
	}
}

func TestLoad_LoggingTokenWithoutURL(t *testing.T) {
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	// no BILLING_LOGGING_URL
	t.Setenv("BILLING_LOGGING_SERVICE_TOKEN", "inline-token")
	if _, err := Load(); !errors.Is(err, ErrLoggingTokenWithoutURL) {
		t.Fatalf("expected ErrLoggingTokenWithoutURL, got %v", err)
	}
}

func TestLoad_LoggingTokenFileWithoutURL(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	t.Setenv("BILLING_LOGGING_SERVICE_TOKEN_FILE", tokenFile)
	if _, err := Load(); !errors.Is(err, ErrLoggingTokenWithoutURL) {
		t.Fatalf("expected ErrLoggingTokenWithoutURL, got %v", err)
	}
}

func TestLoad_LoggingTokenNoURLNoToken(t *testing.T) {
	// No URL and no token is the default no-evidence deployment: valid.
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LoggingServiceToken != "" {
		t.Errorf("LoggingServiceToken = %q, want empty", cfg.LoggingServiceToken)
	}
}

func TestLoad_LoggingTokenURLNoToken(t *testing.T) {
	// URL configured but no token is valid (unauthenticated Logging).
	t.Setenv("BILLING_DATABASE_URL", validURL)
	unsetExcept(t, "BILLING_DATABASE_URL")
	t.Setenv("BILLING_LOGGING_URL", "http://logging:8083")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LoggingServiceToken != "" {
		t.Errorf("LoggingServiceToken = %q, want empty", cfg.LoggingServiceToken)
	}
}

func TestLoad_LoggingTokenFileErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		want  error
	}{
		{
			name: "missing file",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nope")
			},
			want: ErrLoggingTokenFileNotFound,
		},
		{
			name: "symlink rejected",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte("tok"), 0o600); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(dir, "link")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			want: ErrLoggingTokenFileNotRegular,
		},
		{
			name: "directory rejected",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			want: ErrLoggingTokenFileNotRegular,
		},
		{
			name: "empty file",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "empty")
				if err := os.WriteFile(p, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: ErrLoggingTokenFileEmpty,
		},
		{
			name: "too large",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "big")
				if err := os.WriteFile(p, bytes.Repeat([]byte("a"), int(maxLoggingTokenBytes)+1), 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: ErrLoggingTokenFileTooLarge,
		},
		{
			name: "invalid utf-8",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "bad")
				if err := os.WriteFile(p, []byte{0xff, 0xfe, 0xfd}, 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: ErrLoggingTokenFileInvalid,
		},
		{
			name: "nul byte",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "nul")
				if err := os.WriteFile(p, []byte("tok\x00en"), 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: ErrLoggingTokenFileInvalid,
		},
		{
			name: "carriage return embedded",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "cr")
				if err := os.WriteFile(p, []byte("tok\ren"), 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: ErrLoggingTokenFileInvalid,
		},
		{
			name: "blank after trim",
			setup: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "blank")
				if err := os.WriteFile(p, []byte("   \t  "), 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			want: ErrLoggingTokenFileInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			t.Setenv("BILLING_DATABASE_URL", validURL)
			unsetExcept(t, "BILLING_DATABASE_URL")
			t.Setenv("BILLING_LOGGING_URL", "http://logging:8083")
			t.Setenv("BILLING_LOGGING_SERVICE_TOKEN_FILE", path)
			_, err := Load()
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			// No path/content leakage in the error message.
			if err != nil && (strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "tok")) {
				t.Fatalf("error leaks path/content: %v", err)
			}
		})
	}
}

func TestLoad_LoggingTokenInlineInvalid(t *testing.T) {
	cases := map[string]string{
		"newline": "tok\nen",
		"cr":      "tok\ren",
	}
	for name, val := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BILLING_DATABASE_URL", validURL)
			unsetExcept(t, "BILLING_DATABASE_URL")
			t.Setenv("BILLING_LOGGING_URL", "http://logging:8083")
			t.Setenv("BILLING_LOGGING_SERVICE_TOKEN", val)
			_, err := Load()
			if !errors.Is(err, ErrLoggingTokenInvalid) {
				t.Fatalf("expected ErrLoggingTokenInvalid, got %v", err)
			}
		})
	}
}
