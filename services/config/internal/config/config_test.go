package config

import (
	"errors"
	"testing"
)

const validURL = "postgres://user:pass@localhost:5432/tokenmp_config?sslmode=disable"

func unsetExcept(t *testing.T, keep ...string) {
	t.Helper()
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	for _, k := range []string{
		"CONFIG_DATABASE_URL", "CONFIG_HTTP_ADDR", "CONFIG_LOG_LEVEL", "CONFIG_LOG_FORMAT",
		"CONFIG_SHUTDOWN_TIMEOUT", "CONFIG_DB_MAX_OPEN_CONNS", "CONFIG_DB_MAX_IDLE_CONNS",
		"CONFIG_DB_CONN_MAX_LIFETIME",
	} {
		if !keepSet[k] {
			t.Setenv(k, "")
		}
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("CONFIG_DATABASE_URL", "")
	unsetExcept(t)
	if _, err := Load(); !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("expected ErrMissingDatabaseURL, got %v", err)
	}
}

func TestLoad_InvalidScheme(t *testing.T) {
	t.Setenv("CONFIG_DATABASE_URL", "mysql://u:p@localhost/tokenmp_config")
	unsetExcept(t, "CONFIG_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("expected ErrInvalidDatabaseURL for mysql scheme, got %v", err)
	}
}

func TestLoad_WrongDatabaseName(t *testing.T) {
	t.Setenv("CONFIG_DATABASE_URL", "postgres://u:p@localhost:5432/tokenmp_auth")
	unsetExcept(t, "CONFIG_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("expected ErrInvalidDatabaseURL for wrong db name, got %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("CONFIG_DATABASE_URL", validURL)
	unsetExcept(t, "CONFIG_DATABASE_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":8082" {
		t.Errorf("HTTPAddr = %q, want :8082", cfg.HTTPAddr)
	}
	if cfg.DBMaxOpenConns != 10 {
		t.Errorf("DBMaxOpenConns = %d, want 10", cfg.DBMaxOpenConns)
	}
}

func TestLoad_SocketForm(t *testing.T) {
	t.Setenv("CONFIG_DATABASE_URL", "postgres:///tokenmp_config?host=/tmp&port=55433")
	unsetExcept(t, "CONFIG_DATABASE_URL")
	if _, err := Load(); err != nil {
		t.Fatalf("socket form must be accepted, got %v", err)
	}
}

func TestLoad_SocketFormMissingHost(t *testing.T) {
	t.Setenv("CONFIG_DATABASE_URL", "postgres:///tokenmp_config?port=55433")
	unsetExcept(t, "CONFIG_DATABASE_URL")
	if _, err := Load(); !errors.Is(err, ErrInvalidDatabaseURL) {
		t.Fatalf("socket form without host= must be rejected, got %v", err)
	}
}
