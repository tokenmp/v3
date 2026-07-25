package config

import (
	"testing"
	"time"
)

func setEnv(t *testing.T, kvs map[string]string) {
	t.Helper()
	for k, v := range kvs {
		t.Setenv(k, v)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("NOTICE_JWT_PUBLIC_KEY_FILE", "/tmp/pub.pem")
	t.Setenv("NOTICE_DATABASE_URL", "")
	_, err := Load()
	if err != ErrMissingDatabaseURL {
		t.Fatalf("got %v, want ErrMissingDatabaseURL", err)
	}
}

func TestLoad_MissingJWTPublicKey(t *testing.T) {
	t.Setenv("NOTICE_DATABASE_URL", "postgres://u:h@db/tokenmp_biz")
	_, err := Load()
	if err != ErrMissingJWTPublicKey {
		t.Fatalf("got %v, want ErrMissingJWTPublicKey", err)
	}
}

func TestLoad_InvalidDatabaseURL(t *testing.T) {
	cases := []string{
		"mysql://u:h@db/tokenmp_biz",      // wrong scheme
		"postgres://u:h@db/tokenmp_auth",  // wrong db name
		"postgres://u:h@db/",              // empty path
		"postgres://u:h@db/tokenmp_biz/x", // extra path segment
	}
	for _, u := range cases {
		t.Setenv("NOTICE_DATABASE_URL", u)
		t.Setenv("NOTICE_JWT_PUBLIC_KEY_FILE", "/tmp/pub.pem")
		_, err := Load()
		if err != ErrInvalidDatabaseURL {
			t.Fatalf("for %q got %v, want ErrInvalidDatabaseURL", u, err)
		}
	}
}

func TestLoad_OK(t *testing.T) {
	setEnv(t, map[string]string{
		"NOTICE_DATABASE_URL":         "postgres://user:pass@host:5432/tokenmp_biz",
		"NOTICE_JWT_PUBLIC_KEY_FILE":  "/tmp/pub.pem",
		"NOTICE_HTTP_ADDR":            ":9090",
		"NOTICE_DB_MAX_OPEN_CONNS":    "10",
		"NOTICE_DB_CONN_MAX_LIFETIME": "10m",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.DBMaxOpenConns != 10 {
		t.Errorf("DBMaxOpenConns = %d", cfg.DBMaxOpenConns)
	}
	if cfg.ConnMaxLifetime != 10*time.Minute {
		t.Errorf("ConnMaxLifetime = %v", cfg.ConnMaxLifetime)
	}
	if cfg.JWTIssuer != "tokenmp-auth" || cfg.JWTAudience != "tokenmp-web" {
		t.Errorf("JWT issuer/audience defaults wrong: %q/%q", cfg.JWTIssuer, cfg.JWTAudience)
	}
}

func TestLoad_LogLevelInvalid(t *testing.T) {
	setEnv(t, map[string]string{
		"NOTICE_DATABASE_URL":        "postgres://u:h@db/tokenmp_biz",
		"NOTICE_JWT_PUBLIC_KEY_FILE": "/tmp/pub.pem",
		"NOTICE_LOG_LEVEL":           "trace",
	})
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid log level")
	}
}
