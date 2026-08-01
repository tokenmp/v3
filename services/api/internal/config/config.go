// Package config loads API Service (Edge/BFF) runtime configuration from
// environment variables. All values are read once at startup; there is no
// hot-reload.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the resolved runtime configuration for the API Service.
type Config struct {
	// HTTPAddr is the address to listen on (default "127.0.0.1:3002").
	HTTPAddr string

	// ShutdownTimeout is the maximum duration to wait for in-flight requests
	// during graceful shutdown (default "10s").
	ShutdownTimeout time.Duration

	// ReadHeaderTimeout is the maximum duration to read request headers (default "10s").
	ReadHeaderTimeout time.Duration

	// IdleTimeout is the maximum idle time between requests (default "60s").
	IdleTimeout time.Duration

	// ExecutorURL is the base URL of the Executor service
	// (e.g. "http://127.0.0.1:8081"). Required.
	ExecutorURL string

	// BillingURL is the base URL of the Billing service
	// (e.g. "http://127.0.0.1:8085"). Optional; when empty, quota
	// reserve/finalize is skipped (degraded mode for dev).
	BillingURL string

	// LoggingURL is the base URL of the Logging service
	// (e.g. "http://127.0.0.1:8083"). Optional; when empty, edge log
	// push is skipped.
	LoggingURL string

	// AuthURL is the base URL of the Auth service
	// (e.g. "http://127.0.0.1:8080"). Optional; when empty, API key
	// management endpoints (/api/v1/keys*) return 503 because the edge
	// cannot reach Auth. When set, the edge forwards the client Bearer
	// token to Auth's /api/v1/auth/keys* management API.
	AuthURL string

	// ConfigServiceURL is the base URL of the Config Service
	// (e.g. "http://127.0.0.1:8082"). Optional; when empty, admin config
	// CRUD endpoints return 503. When set, the edge proxies admin config
	// management routes to the Config Service.
	ConfigServiceURL string

	// JWTPublicKeyFile is the path to the Ed25519 public key PEM file used
	// to verify client JWTs. It is required unless AllowNoopAuth is explicitly
	// enabled for local development or tests.
	JWTPublicKeyFile string

	// AllowNoopAuth permits the development-only verifier that accepts any
	// non-empty Bearer token. It must be explicitly set with API_ALLOW_NOOP_AUTH=true.
	AllowNoopAuth bool

	// JWTIssuer is the expected JWT issuer. Defaults to "tokenmp-auth".
	JWTIssuer string

	// JWTAudience is the expected JWT audience. Defaults to "tokenmp-web".
	JWTAudience string

	// ExecutorToken is the service-level Bearer token the edge uses to
	// authenticate to the executor when it runs in API-key (identityenv) mode.
	// Optional; when empty, the proxy forwards the client's Authorization
	// header as-is (JWT passthrough mode, both edge and executor verify the
	// same JWT with the Auth service public key).
	ExecutorToken string

	// Rate limiting (Redis shared token bucket). Disabled by default; when
	// RateLimitEnabled is true, RedisAddr / HMACSecretFile / TrustedProxies
	// must all be valid and non-empty. The HMAC secret is read from a file
	// path (never an env var) into the short-lived RateLimitHMACSecret []byte;
	// main consumes the bytes and zeroes them. Protected metered execution
	// endpoints fail closed (503) when Redis is unreachable; denied requests
	// get 429 + Retry-After. Health and read-only endpoints are not limited.
	RateLimitEnabled        bool
	RateLimitRedisAddr      string
	RateLimitRedisDB        int
	RateLimitHMACSecretFile string
	RateLimitHMACSecret     []byte
	RateLimitTrustedProxies []string
	RateLimitIPCapacity     float64
	RateLimitIPRefill       float64
	RateLimitSubjCapacity   float64
	RateLimitSubjRefill     float64
	RateLimitBucketTTL      time.Duration
}

// Load reads configuration from environment variables and returns a validated
// Config. It returns a non-nil error if any required variable is missing or
// contains an invalid value. Error messages reference variable names but never
// echo values.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:          "127.0.0.1:3002",
		ShutdownTimeout:   10 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		JWTIssuer:         "tokenmp-auth",
		JWTAudience:       "tokenmp-web",
	}

	if v := os.Getenv("API_HTTP_ADDR"); v != "" {
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("API_HTTP_ADDR must not be blank")
		}
		cfg.HTTPAddr = v
	}

	if v, ok := os.LookupEnv("API_SHUTDOWN_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("API_SHUTDOWN_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("API_SHUTDOWN_TIMEOUT must be positive")
		}
		cfg.ShutdownTimeout = d
	}

	if v, ok := os.LookupEnv("API_READ_HEADER_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("API_READ_HEADER_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("API_READ_HEADER_TIMEOUT must be positive")
		}
		cfg.ReadHeaderTimeout = d
	}

	if v, ok := os.LookupEnv("API_IDLE_TIMEOUT"); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("API_IDLE_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("API_IDLE_TIMEOUT must be positive")
		}
		cfg.IdleTimeout = d
	}

	cfg.ExecutorURL = strings.TrimSpace(os.Getenv("API_EXECUTOR_URL"))
	if cfg.ExecutorURL == "" {
		return nil, errors.New("API_EXECUTOR_URL is required")
	}
	if !validHTTPBaseURL(cfg.ExecutorURL) {
		return nil, errors.New("API_EXECUTOR_URL must be an http(s) base URL without query or fragment")
	}

	cfg.BillingURL = strings.TrimSpace(os.Getenv("API_BILLING_URL"))
	if cfg.BillingURL != "" && !validHTTPBaseURL(cfg.BillingURL) {
		return nil, errors.New("API_BILLING_URL must be an http(s) base URL without query or fragment")
	}

	cfg.LoggingURL = strings.TrimSpace(os.Getenv("API_LOGGING_URL"))
	if cfg.LoggingURL != "" && !validHTTPBaseURL(cfg.LoggingURL) {
		return nil, errors.New("API_LOGGING_URL must be an http(s) base URL without query or fragment")
	}

	cfg.AuthURL = strings.TrimSpace(os.Getenv("API_AUTH_URL"))
	if cfg.AuthURL != "" && !validHTTPBaseURL(cfg.AuthURL) {
		return nil, errors.New("API_AUTH_URL must be an http(s) base URL without query or fragment")
	}

	cfg.ConfigServiceURL = strings.TrimSpace(os.Getenv("API_CONFIG_SERVICE_URL"))
	if cfg.ConfigServiceURL != "" && !validHTTPBaseURL(cfg.ConfigServiceURL) {
		return nil, errors.New("API_CONFIG_SERVICE_URL must be an http(s) base URL without query or fragment")
	}

	cfg.JWTPublicKeyFile = strings.TrimSpace(os.Getenv("API_JWT_PUBLIC_KEY_FILE"))
	if v, ok := os.LookupEnv("API_ALLOW_NOOP_AUTH"); ok {
		allowNoopAuth, err := parseStrictBool("API_ALLOW_NOOP_AUTH", v)
		if err != nil {
			return nil, err
		}
		cfg.AllowNoopAuth = allowNoopAuth
	}
	if cfg.JWTPublicKeyFile == "" && !cfg.AllowNoopAuth {
		return nil, errors.New("API_JWT_PUBLIC_KEY_FILE is required unless API_ALLOW_NOOP_AUTH=true")
	}

	if v := strings.TrimSpace(os.Getenv("API_JWT_ISSUER")); v != "" {
		cfg.JWTIssuer = v
	}
	if v := strings.TrimSpace(os.Getenv("API_JWT_AUDIENCE")); v != "" {
		cfg.JWTAudience = v
	}

	cfg.ExecutorToken = os.Getenv("API_EXECUTOR_TOKEN")
	// ExecutorToken is optional (JWT passthrough mode when empty).

	if err := loadRateLimit(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseStrictBool accepts only explicit true or false values.
func parseStrictBool(name, raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

const (
	rlDefaultIPCapacity   = 60.0
	rlDefaultIPRefill     = 2.0 // ~120 req/min steady per IP
	rlDefaultSubjCapacity = 120.0
	rlDefaultSubjRefill   = 4.0 // ~240 req/min steady per subject
	rlDefaultBucketTTL    = 10 * time.Minute
)

// loadRateLimit reads and validates the optional Edge rate-limiting config.
// When API_RATE_LIMIT_ENABLED is not "true", rate limiting is disabled. When
// enabled, the Redis address, HMAC secret file and trusted-proxy CIDRs are
// all required and fail fast. Errors never echo the secret, Redis URL, or
// proxy topology.
func loadRateLimit(cfg *Config) error {
	enabledRaw := strings.TrimSpace(os.Getenv("API_RATE_LIMIT_ENABLED"))
	if enabledRaw == "" {
		cfg.RateLimitEnabled = false
		return nil
	}
	enabled, err := parseStrictBool("API_RATE_LIMIT_ENABLED", enabledRaw)
	if err != nil {
		return err
	}
	cfg.RateLimitEnabled = enabled
	if !enabled {
		return nil
	}

	rawRedis := strings.TrimSpace(os.Getenv("API_RATE_LIMIT_REDIS_ADDR"))
	if rawRedis == "" {
		return ErrRLRedisAddrRequired
	}
	if err := validateRedisURL(rawRedis); err != nil {
		return err
	}
	cfg.RateLimitRedisAddr = rawRedis

	db, err := parseIntDefault("API_RATE_LIMIT_REDIS_DB", 0)
	if err != nil {
		return err
	}
	if db < 0 {
		return fmt.Errorf("API_RATE_LIMIT_REDIS_DB must be >= 0, got %d", db)
	}
	cfg.RateLimitRedisDB = db

	secretPath := strings.TrimSpace(os.Getenv("API_RATE_LIMIT_HMAC_SECRET_FILE"))
	if secretPath == "" {
		return ErrRLSecretFileRequired
	}
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		return ErrRLSecretReadFailed
	}
	if len(secret) < 32 {
		return ErrRLSecretTooShort
	}
	cfg.RateLimitHMACSecretFile = secretPath
	cfg.RateLimitHMACSecret = secret

	trusted := parseCSV(os.Getenv("API_RATE_LIMIT_TRUSTED_PROXIES"))
	if len(trusted) == 0 {
		return ErrRLTrustedProxiesReqd
	}
	for _, c := range trusted {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return ErrRLInvalidTrustedProxy
		}
	}
	cfg.RateLimitTrustedProxies = trusted

	cfg.RateLimitIPCapacity, err = parseFloatDefault("API_RATE_LIMIT_IP_CAPACITY", rlDefaultIPCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitIPRefill, err = parseFloatDefault("API_RATE_LIMIT_IP_REFILL", rlDefaultIPRefill)
	if err != nil {
		return err
	}
	cfg.RateLimitSubjCapacity, err = parseFloatDefault("API_RATE_LIMIT_SUBJECT_CAPACITY", rlDefaultSubjCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitSubjRefill, err = parseFloatDefault("API_RATE_LIMIT_SUBJECT_REFILL", rlDefaultSubjRefill)
	if err != nil {
		return err
	}
	for _, c := range []struct {
		name      string
		v         float64
		allowZero bool
	}{
		{"API_RATE_LIMIT_IP_CAPACITY", cfg.RateLimitIPCapacity, false},
		{"API_RATE_LIMIT_IP_REFILL", cfg.RateLimitIPRefill, true},
		{"API_RATE_LIMIT_SUBJECT_CAPACITY", cfg.RateLimitSubjCapacity, false},
		{"API_RATE_LIMIT_SUBJECT_REFILL", cfg.RateLimitSubjRefill, true},
	} {
		if c.v < 0 || (!c.allowZero && c.v <= 0) {
			if c.allowZero {
				return fmt.Errorf("%s must be >= 0", c.name)
			}
			return fmt.Errorf("%s must be > 0", c.name)
		}
	}

	ttl, err := parseDurationDefault("API_RATE_LIMIT_BUCKET_TTL", rlDefaultBucketTTL)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("API_RATE_LIMIT_BUCKET_TTL must be > 0")
	}
	cfg.RateLimitBucketTTL = ttl
	return nil
}

// validateRedisURL accepts redis://, rediss://, or a bare host:port with a
// numeric port. It never echoes the URL in the error.
func validateRedisURL(raw string) error {
	if strings.HasPrefix(raw, "redis://") || strings.HasPrefix(raw, "rediss://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return ErrRLInvalidRedisURL
		}
		return nil
	}
	if strings.Contains(raw, "://") {
		return ErrRLInvalidRedisURL
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" || port == "" {
		return ErrRLInvalidRedisURL
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return ErrRLInvalidRedisURL
		}
	}
	return nil
}

func parseCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseIntDefault(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return n, nil
}

func parseFloatDefault(key string, fallback float64) (float64, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", key)
	}
	return f, nil
}

func parseDurationDefault(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

// validHTTPBaseURL checks that raw is an http(s) URL with no query or
// fragment.
func validHTTPBaseURL(raw string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	// Reject query/fragment.
	if strings.ContainsAny(raw, "?#") {
		return false
	}
	return true
}

// Rate-limit configuration sentinel errors. They reference the env name only,
// never the secret value, Redis URL, or proxy topology.
var (
	ErrRLRedisAddrRequired   = errors.New("API_RATE_LIMIT_REDIS_ADDR is required when rate limiting is enabled")
	ErrRLSecretFileRequired  = errors.New("API_RATE_LIMIT_HMAC_SECRET_FILE is required when rate limiting is enabled")
	ErrRLSecretReadFailed    = errors.New("API_RATE_LIMIT_HMAC_SECRET_FILE could not be read")
	ErrRLSecretTooShort      = errors.New("API_RATE_LIMIT_HMAC_SECRET_FILE must be at least 32 bytes")
	ErrRLTrustedProxiesReqd  = errors.New("API_RATE_LIMIT_TRUSTED_PROXIES must be configured when rate limiting is enabled")
	ErrRLInvalidTrustedProxy = errors.New("API_RATE_LIMIT_TRUSTED_PROXIES contains an invalid CIDR")
	ErrRLInvalidRedisURL     = errors.New("API_RATE_LIMIT_REDIS_ADDR is not a valid redis URL")
)
