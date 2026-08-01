package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEdgeSecretFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "rl_hmac")
	b := make([]byte, 48)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return p
}

func baseEdgeRL(t *testing.T) {
	t.Helper()
	t.Setenv("API_EXECUTOR_URL", "http://127.0.0.1:8081")
	t.Setenv("API_EXECUTOR_TOKEN", "tok")
	t.Setenv("API_JWT_PUBLIC_KEY_FILE", "")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	t.Setenv("API_RATE_LIMIT_ENABLED", "true")
	t.Setenv("API_RATE_LIMIT_REDIS_ADDR", "redis://127.0.0.1:6379/0")
	t.Setenv("API_RATE_LIMIT_REDIS_DB", "0")
	t.Setenv("API_RATE_LIMIT_HMAC_SECRET_FILE", writeEdgeSecretFile(t))
	t.Setenv("API_RATE_LIMIT_TRUSTED_PROXIES", "10.0.0.0/8")
}

func TestEdgeLoad_RateLimitDisabledByDefault(t *testing.T) {
	t.Setenv("API_EXECUTOR_URL", "http://127.0.0.1:8081")
	t.Setenv("API_ALLOW_NOOP_AUTH", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RateLimitEnabled {
		t.Fatal("rate limit must be disabled by default")
	}
}

func TestEdgeLoad_RateLimitEnabledOK(t *testing.T) {
	baseEdgeRL(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.RateLimitEnabled {
		t.Fatal("must be enabled")
	}
	if cfg.RateLimitRedisAddr != "redis://127.0.0.1:6379/0" {
		t.Errorf("redis addr = %q", cfg.RateLimitRedisAddr)
	}
	if cfg.RateLimitHMACSecretFile == "" {
		t.Fatal("secret path not loaded")
	}
	if len(cfg.RateLimitHMACSecret) == 0 {
		t.Fatal("secret bytes not loaded")
	}
	if len(cfg.RateLimitTrustedProxies) != 1 {
		t.Errorf("proxies = %v", cfg.RateLimitTrustedProxies)
	}
}

func TestEdgeLoad_RateLimitMissingRedisAddr(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_REDIS_ADDR", "")
	if _, err := Load(); err != ErrRLRedisAddrRequired {
		t.Fatalf("got %v, want ErrRLRedisAddrRequired", err)
	}
}

func TestEdgeLoad_RateLimitMissingSecret(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_HMAC_SECRET_FILE", "")
	if _, err := Load(); err != ErrRLSecretFileRequired {
		t.Fatalf("got %v, want ErrRLSecretFileRequired", err)
	}
}

func TestEdgeLoad_RateLimitShortSecret(t *testing.T) {
	baseEdgeRL(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "s")
	if err := os.WriteFile(p, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_RATE_LIMIT_HMAC_SECRET_FILE", p)
	if _, err := Load(); err != ErrRLSecretTooShort {
		t.Fatalf("got %v, want ErrRLSecretTooShort", err)
	}
}

func TestEdgeLoad_RateLimitMissingProxies(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_TRUSTED_PROXIES", "")
	if _, err := Load(); err != ErrRLTrustedProxiesReqd {
		t.Fatalf("got %v, want ErrRLTrustedProxiesReqd", err)
	}
}

func TestEdgeLoad_RateLimitInvalidCIDR(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_TRUSTED_PROXIES", "garbage")
	if _, err := Load(); err != ErrRLInvalidTrustedProxy {
		t.Fatalf("got %v, want ErrRLInvalidTrustedProxy", err)
	}
}

func TestEdgeLoad_RateLimitInvalidRedisURL(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_REDIS_ADDR", "ftp://bad")
	if _, err := Load(); err != ErrRLInvalidRedisURL {
		t.Fatalf("got %v, want ErrRLInvalidRedisURL", err)
	}
}

func TestEdgeLoad_RateLimitBareHostPortOK(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_REDIS_ADDR", "127.0.0.1:6379")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("bare host:port must be accepted: %v", err)
	}
	if cfg.RateLimitRedisAddr != "127.0.0.1:6379" {
		t.Errorf("got %q", cfg.RateLimitRedisAddr)
	}
}

func TestEdgeLoad_RateLimitInvalidEnabled(t *testing.T) {
	baseEdgeRL(t)
	t.Setenv("API_RATE_LIMIT_ENABLED", "yes")
	if _, err := Load(); err == nil {
		t.Fatal("non-bool enabled must fail")
	}
}
