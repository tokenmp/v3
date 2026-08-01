package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSecretFile writes a >=32-byte secret to a temp file and returns its path.
func writeSecretFile(t *testing.T) string {
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

func baseRateLimitEnvs(t *testing.T, secretPath string) {
	t.Helper()
	t.Setenv("AUTH_DATABASE_URL", validURL)
	t.Setenv("AUTH_JWT_PRIVATE_KEY_FILE", "")
	t.Setenv("AUTH_JWT_PUBLIC_KEY_FILE", "")
	t.Setenv("AUTH_RATE_LIMIT_ENABLED", "true")
	t.Setenv("AUTH_RATE_LIMIT_REDIS_ADDR", "redis://127.0.0.1:6379/0")
	t.Setenv("AUTH_RATE_LIMIT_REDIS_DB", "0")
	t.Setenv("AUTH_RATE_LIMIT_HMAC_SECRET_FILE", secretPath)
	t.Setenv("AUTH_RATE_LIMIT_TRUSTED_PROXIES", "10.0.0.0/8,2001:db8::/32")
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL", "AUTH_JWT_PRIVATE_KEY_FILE",
		"AUTH_JWT_PUBLIC_KEY_FILE", "AUTH_RATE_LIMIT_ENABLED", "AUTH_RATE_LIMIT_REDIS_ADDR",
		"AUTH_RATE_LIMIT_REDIS_DB", "AUTH_RATE_LIMIT_HMAC_SECRET_FILE", "AUTH_RATE_LIMIT_TRUSTED_PROXIES",
		"AUTH_RATE_LIMIT_LOGIN_IP_CAPACITY", "AUTH_RATE_LIMIT_LOGIN_IP_REFILL",
		"AUTH_RATE_LIMIT_LOGIN_ACCOUNT_CAPACITY", "AUTH_RATE_LIMIT_LOGIN_ACCOUNT_REFILL",
		"AUTH_RATE_LIMIT_REGISTER_IP_CAPACITY", "AUTH_RATE_LIMIT_REGISTER_IP_REFILL",
		"AUTH_RATE_LIMIT_REGISTER_ACCOUNT_CAPACITY", "AUTH_RATE_LIMIT_REGISTER_ACCOUNT_REFILL",
		"AUTH_RATE_LIMIT_REFRESH_IP_CAPACITY", "AUTH_RATE_LIMIT_REFRESH_IP_REFILL",
		"AUTH_RATE_LIMIT_REFRESH_ACCOUNT_CAPACITY", "AUTH_RATE_LIMIT_REFRESH_ACCOUNT_REFILL",
		"AUTH_RATE_LIMIT_BUCKET_TTL")
}

func TestLoad_RateLimitDisabledByDefault(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", validURL)
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RateLimitEnabled {
		t.Fatal("rate limit must be disabled by default")
	}
}

func TestLoad_RateLimitEnabledValid(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.RateLimitEnabled {
		t.Fatal("rate limit must be enabled")
	}
	if cfg.RateLimitRedisAddr != "redis://127.0.0.1:6379/0" {
		t.Errorf("redis addr = %q", cfg.RateLimitRedisAddr)
	}
	if cfg.RateLimitRedisDB != 0 {
		t.Errorf("redis db = %d", cfg.RateLimitRedisDB)
	}
	if cfg.RateLimitHMACSecretFile == "" {
		t.Fatal("hmac secret path not loaded")
	}
	if len(cfg.RateLimitHMACSecret) == 0 {
		t.Fatal("hmac secret bytes not loaded")
	}
	if cfg.RateLimitLoginIPCapacity != rateLimitDefaultLoginIPCapacity {
		t.Errorf("login ip capacity default = %v", cfg.RateLimitLoginIPCapacity)
	}
	if cfg.RateLimitBucketTTL != rateLimitDefaultBucketTTL {
		t.Errorf("ttl = %s", cfg.RateLimitBucketTTL)
	}
	if len(cfg.RateLimitTrustedProxies) != 2 {
		t.Errorf("trusted proxies = %v", cfg.RateLimitTrustedProxies)
	}
}

func TestLoad_RateLimitMissingRedisAddr(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_REDIS_ADDR", "")
	if _, err := Load(); err != ErrRateLimitRedisAddrRequired {
		t.Fatalf("got %v, want ErrRateLimitRedisAddrRequired", err)
	}
}

func TestLoad_RateLimitMissingSecretFile(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_HMAC_SECRET_FILE", "")
	if _, err := Load(); err != ErrRateLimitSecretFileRequired {
		t.Fatalf("got %v, want ErrRateLimitSecretFileRequired", err)
	}
}

func TestLoad_RateLimitSecretUnreadable(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_HMAC_SECRET_FILE", "/nonexistent/path/secret")
	if _, err := Load(); err != ErrRateLimitSecretReadFailed {
		t.Fatalf("got %v, want ErrRateLimitSecretReadFailed", err)
	}
}

func TestLoad_RateLimitSecretTooShort(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "short")
	if err := os.WriteFile(p, []byte("tooshort"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	baseRateLimitEnvs(t, p)
	if _, err := Load(); err != ErrRateLimitSecretTooShort {
		t.Fatalf("got %v, want ErrRateLimitSecretTooShort", err)
	}
}

func TestLoad_RateLimitMissingTrustedProxies(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_TRUSTED_PROXIES", "")
	if _, err := Load(); err != ErrRateLimitTrustedProxiesReqd {
		t.Fatalf("got %v, want ErrRateLimitTrustedProxiesReqd", err)
	}
}

func TestLoad_RateLimitInvalidCIDR(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_TRUSTED_PROXIES", "not-a-cidr")
	if _, err := Load(); err != ErrRateLimitInvalidTrustedProxy {
		t.Fatalf("got %v, want ErrRateLimitInvalidTrustedProxy", err)
	}
}

func TestLoad_RateLimitInvalidRedisURL(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_REDIS_ADDR", "ftp://bad")
	if _, err := Load(); err != ErrRateLimitInvalidRedisURL {
		t.Fatalf("got %v, want ErrRateLimitInvalidRedisURL", err)
	}
}

func TestLoad_RateLimitBareHostPortOK(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_REDIS_ADDR", "127.0.0.1:6379")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("bare host:port must be accepted: %v", err)
	}
	if cfg.RateLimitRedisAddr != "127.0.0.1:6379" {
		t.Errorf("got %q", cfg.RateLimitRedisAddr)
	}
}

func TestLoad_RateLimitInvalidCapacity(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_LOGIN_IP_CAPACITY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("capacity 0 must fail")
	}
}

func TestLoad_RateLimitTTLNegative(t *testing.T) {
	secret := writeSecretFile(t)
	baseRateLimitEnvs(t, secret)
	t.Setenv("AUTH_RATE_LIMIT_BUCKET_TTL", "-5s")
	if _, err := Load(); err == nil {
		t.Fatal("negative TTL must fail")
	}
}

func TestLoad_RateLimitInvalidEnabled(t *testing.T) {
	t.Setenv("AUTH_DATABASE_URL", validURL)
	unsetAuthEnvsExcept(t, "AUTH_DATABASE_URL")
	t.Setenv("AUTH_RATE_LIMIT_ENABLED", "yes")
	if _, err := Load(); err == nil {
		t.Fatal("non-bool enabled must fail")
	}
}

// ensure time import is used (rateLimitDefaultBucketTTL is a time.Duration).
var _ = time.Second
