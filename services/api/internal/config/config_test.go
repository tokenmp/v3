package config

import (
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://127.0.0.1:8081")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	// Clear optional URLs.
	t.Setenv("API_BILLING_URL", "")
	t.Setenv("API_LOGGING_URL", "")
	t.Setenv("API_JWT_PUBLIC_KEY_FILE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:3002" {
		t.Errorf("HTTPAddr = %q, want 127.0.0.1:3002", cfg.HTTPAddr)
	}
	if cfg.ExecutorURL != "http://127.0.0.1:8081" {
		t.Errorf("ExecutorURL = %q", cfg.ExecutorURL)
	}
	if cfg.JWTIssuer != "tokenmp-auth" {
		t.Errorf("JWTIssuer = %q", cfg.JWTIssuer)
	}
	if cfg.JWTAudience != "tokenmp-web" {
		t.Errorf("JWTAudience = %q", cfg.JWTAudience)
	}
	if cfg.BillingURL != "" || cfg.LoggingURL != "" {
		t.Errorf("optional URLs should be empty: billing=%q logging=%q", cfg.BillingURL, cfg.LoggingURL)
	}
}

func TestLoadMissingExecutorURL(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for missing API_EXECUTOR_URL")
	}
}

func TestLoadMissingExecutorToken(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_EXECUTOR_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for missing API_EXECUTOR_TOKEN")
	}
}

func TestLoadInvalidExecutorURL(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "ftp://x")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid scheme")
	}
}

func TestLoadExecutorURLWithQuery(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x?token=s")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for query in URL")
	}
}

func TestLoadOptionalURLs(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "https://exec.example")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	t.Setenv("API_BILLING_URL", "https://bill.example")
	t.Setenv("API_LOGGING_URL", "https://log.example")
	t.Setenv("API_JWT_PUBLIC_KEY_FILE", "/tmp/key.pem")
	t.Setenv("API_JWT_ISSUER", "custom-iss")
	t.Setenv("API_JWT_AUDIENCE", "custom-aud")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.BillingURL != "https://bill.example" {
		t.Errorf("BillingURL = %q", cfg.BillingURL)
	}
	if cfg.LoggingURL != "https://log.example" {
		t.Errorf("LoggingURL = %q", cfg.LoggingURL)
	}
	if cfg.JWTPublicKeyFile != "/tmp/key.pem" {
		t.Errorf("JWTPublicKeyFile = %q", cfg.JWTPublicKeyFile)
	}
	if cfg.JWTIssuer != "custom-iss" {
		t.Errorf("JWTIssuer = %q", cfg.JWTIssuer)
	}
	if cfg.JWTAudience != "custom-aud" {
		t.Errorf("JWTAudience = %q", cfg.JWTAudience)
	}
}
