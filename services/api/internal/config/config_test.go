package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "admin.token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://127.0.0.1:8081")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	// Clear optional URLs.
	t.Setenv("API_BILLING_URL", "")
	t.Setenv("API_LOGGING_URL", "")
	t.Setenv("API_JWT_PUBLIC_KEY_FILE", "")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")

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
	if !cfg.AllowNoopAuth {
		t.Error("AllowNoopAuth = false, want true")
	}
	if cfg.BillingURL != "" || cfg.LoggingURL != "" || cfg.AuthURL != "" {
		t.Errorf("optional URLs should be empty: billing=%q logging=%q auth=%q", cfg.BillingURL, cfg.LoggingURL, cfg.AuthURL)
	}
}

func TestLoadRejectsMissingPublicKeyWithoutNoopOptIn(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://127.0.0.1:8081")
	t.Setenv("API_JWT_PUBLIC_KEY_FILE", "")
	t.Setenv("API_ALLOW_NOOP_AUTH", "false")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for missing API_JWT_PUBLIC_KEY_FILE")
	}
}

func TestLoadRejectsInvalidAllowNoopAuth(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://127.0.0.1:8081")
	t.Setenv("API_JWT_PUBLIC_KEY_FILE", "")
	t.Setenv("API_ALLOW_NOOP_AUTH", "1")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid API_ALLOW_NOOP_AUTH")
	}
}

func TestLoadMissingExecutorURL(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for missing API_EXECUTOR_URL")
	}
}

func TestLoadExecutorTokenOptional(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_EXECUTOR_TOKEN", "")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (token is optional)", err)
	}
	if cfg.ExecutorToken != "" {
		t.Errorf("ExecutorToken = %q, want empty", cfg.ExecutorToken)
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
	t.Setenv("API_AUTH_URL", "https://auth.example")
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
	if cfg.AuthURL != "https://auth.example" {
		t.Errorf("AuthURL = %q", cfg.AuthURL)
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

func TestLoadInvalidAuthURL(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	t.Setenv("API_AUTH_URL", "ftp://auth")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid API_AUTH_URL scheme")
	}
}

func TestLoadAuthURLWithQuery(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	t.Setenv("API_AUTH_URL", "http://auth?x=1")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for API_AUTH_URL with query")
	}
}

func TestLoadConfigAdminProxyTokenRequired(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_URL", "http://config:8082")
	t.Setenv("API_CONFIG_ADMIN_PROXY_ENABLED", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error: token required when admin proxy enabled")
	}
}

func TestLoadConfigAdminProxyInvalidFlag(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_ADMIN_PROXY_ENABLED", "yes")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error for invalid boolean")
	}
}

func TestLoadConfigReadOnlyNoTokenOK(t *testing.T) {
	// Read-only Config Service use (admin proxy disabled): token is optional.
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_URL", "http://config:8082")
	t.Setenv("API_CONFIG_ADMIN_PROXY_ENABLED", "false")
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil (read-only needs no token)", err)
	}
	if cfg.ConfigAdminProxyEnabled {
		t.Fatal("ConfigAdminProxyEnabled = true, want false")
	}
}

func TestLoadConfigAdminProxyEnabledWithTokenOK(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_URL", "http://config:8082")
	t.Setenv("API_CONFIG_ADMIN_PROXY_ENABLED", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "shared-secret")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.ConfigAdminProxyEnabled {
		t.Fatal("ConfigAdminProxyEnabled = false, want true")
	}
	if cfg.ConfigServiceToken != "shared-secret" {
		t.Errorf("ConfigServiceToken = %q", cfg.ConfigServiceToken)
	}
}

func TestLoadConfigTokenFileProducesToken(t *testing.T) {
	p := writeTokenFile(t, "  file-secret\n")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_URL", "http://config:8082")
	t.Setenv("API_CONFIG_ADMIN_PROXY_ENABLED", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ConfigServiceToken != "file-secret" {
		t.Errorf("ConfigServiceToken = %q, want file-secret", cfg.ConfigServiceToken)
	}
}

func TestLoadConfigTokenFileTrimmedAndNoNewline(t *testing.T) {
	p := writeTokenFile(t, "\n\t trimmed-secret \r\n")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ConfigServiceToken != "trimmed-secret" {
		t.Errorf("ConfigServiceToken = %q, want trimmed-secret", cfg.ConfigServiceToken)
	}
}

func TestLoadConfigTokenBothSourcesFails(t *testing.T) {
	p := writeTokenFile(t, "file-secret")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "direct-secret")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error when both token sources set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want mutually exclusive", err)
	}
}

func TestLoadConfigTokenFileEmptyFails(t *testing.T) {
	p := writeTokenFile(t, "   \n")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for empty token file")
	}
}

func TestLoadConfigTokenFileMissingFails(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", filepath.Join(t.TempDir(), "nope.token"))
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for missing token file")
	}
}

func TestLoadConfigTokenFileSymlinkFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.token")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.token")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", link)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for symlink token file")
	}
}

func TestLoadConfigTokenFileTooLargeFails(t *testing.T) {
	big := make([]byte, maxConfigTokenBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	p := writeTokenFile(t, string(big))
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for oversized token file")
	}
}

func TestLoadConfigTokenFileNULFails(t *testing.T) {
	p := writeTokenFile(t, "secret\x00bad")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for NUL in token file")
	}
}

func TestLoadConfigTokenFileNewlineFails(t *testing.T) {
	// A multi-line value (after trim the leading/trailing whitespace is gone
	// but interior newline remains) must be rejected.
	p := writeTokenFile(t, "line1\nline2")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error for newline-bearing token file")
	}
}

func TestLoadConfigTokenFileErrorNoLeak(t *testing.T) {
	p := writeTokenFile(t, "supersecret-value")
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	// Point at a missing path whose name contains the secret-like suffix.
	missing := filepath.Join(t.TempDir(), p)
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", missing)
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() expected error")
	}
	if strings.Contains(err.Error(), "supersecret-value") {
		t.Fatalf("error leaked token content: %v", err)
	}
	if strings.Contains(err.Error(), missing) {
		t.Fatalf("error leaked path: %v", err)
	}
}

func TestLoadConfigAdminProxyEnabledRequiresTokenFileOrEnv(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_URL", "http://config:8082")
	t.Setenv("API_CONFIG_ADMIN_PROXY_ENABLED", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", "")
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() expected error: token required when admin proxy enabled")
	}
}

func TestLoadConfigTokenFileBlankPathIgnored(t *testing.T) {
	// A blank file path with no direct env yields no token; admin proxy off so
	// load succeeds (read-only use).
	t.Setenv("API_EXECUTOR_URL", "http://x")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_CONFIG_SERVICE_TOKEN_FILE", "   ")
	t.Setenv("API_CONFIG_SERVICE_TOKEN", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ConfigServiceToken != "" {
		t.Errorf("ConfigServiceToken = %q, want empty", cfg.ConfigServiceToken)
	}
}
