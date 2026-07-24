package config

import (
	"errors"
	"testing"
)

const validURL = "postgres://user:pass@localhost:5432/tokenmp_logging?sslmode=disable"

func unsetExcept(t *testing.T, keep ...string) {
	t.Helper()
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	for _, k := range []string{
		"LOGGING_DATABASE_URL", "LOGGING_HTTP_ADDR", "LOGGING_LOG_LEVEL", "LOGGING_LOG_FORMAT",
		"LOGGING_SHUTDOWN_TIMEOUT", "LOGGING_DB_MAX_OPEN_CONNS", "LOGGING_DB_MAX_IDLE_CONNS",
		"LOGGING_DB_CONN_MAX_LIFETIME",
	} {
		if !keepSet[k] {
			t.Setenv(k, "")
		}
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("LOGGING_DATABASE_URL", "")
	unsetExcept(t)
	if _, err := Load(); !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("expected ErrMissingDatabaseURL, got %v", err)
	}
}

func TestLoad_InvalidScheme(t *testing.T) {
	t.Setenv("LOGGING_DATABASE_URL", "mysql://u:p@localhost/tokenmp_logging")
	unsetExcept(t, "LOGGING_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("expected ErrInvalidDatabaseURL for mysql scheme, got %v", err)
	}
}

func TestLoad_WrongDatabaseName(t *testing.T) {
	t.Setenv("LOGGING_DATABASE_URL", "postgres://u:p@localhost:5432/tokenmp_config")
	unsetExcept(t, "LOGGING_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("expected ErrInvalidDatabaseURL for wrong db name, got %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("LOGGING_DATABASE_URL", validURL)
	unsetExcept(t, "LOGGING_DATABASE_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":8083" {
		t.Errorf("HTTPAddr = %q, want :8083", cfg.HTTPAddr)
	}
	if cfg.DBMaxOpenConns != 10 {
		t.Errorf("DBMaxOpenConns = %d, want 10", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns != 2 {
		t.Errorf("DBMaxIdleConns = %d, want 2", cfg.DBMaxIdleConns)
	}
}

func TestLoad_SocketForm(t *testing.T) {
	t.Setenv("LOGGING_DATABASE_URL", "postgres:///tokenmp_logging?host=/tmp&port=55433")
	unsetExcept(t, "LOGGING_DATABASE_URL")
	if _, err := Load(); err != nil {
		t.Fatalf("socket form must be accepted, got %v", err)
	}
}

func TestLoad_SocketFormMissingHost(t *testing.T) {
	t.Setenv("LOGGING_DATABASE_URL", "postgres:///tokenmp_logging?port=55433")
	unsetExcept(t, "LOGGING_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("socket form without host= must be rejected, got %v", err)
	}
}
