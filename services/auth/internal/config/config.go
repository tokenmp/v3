// Package config loads and validates the auth service runtime configuration.
//
// Configuration is sourced exclusively from environment variables. The service
// fails fast on missing or invalid required values; optional values fall back
// to safe defaults that never include production credentials.
//
// AUTH_DATABASE_URL is strictly validated: only postgres/postgresql URLs are
// accepted, must carry a host, a non-empty user and a path of exactly
// /tokenmp_auth. Any validation error reports only the failing field, never
// the URL value or its credentials. Numeric and duration tunables
// (AUTH_DB_MAX_*, AUTH_DB_CONN_MAX_LIFETIME, AUTH_SHUTDOWN_TIMEOUT) fail fast
// on non-parseable or negative input — they never silently fall back.
//
// The connection string is never logged and never echoed in errors.
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

// Config is the validated auth service runtime configuration.
type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	LogLevel          string
	LogFormat         string
	ShutdownTimeout   time.Duration
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// JWT / refresh token configuration. Key file paths are read here but
	// the PEM is parsed and validated by the jwt package at startup; paths are
	// never echoed in errors. An empty path is allowed at the config layer so
	// the service can be assembled for tests with an injected in-memory key
	// pair; main.go fails fast if a path is missing in a real deployment.
	JWTPrivateKeyFile string
	JWTPublicKeyFile  string
	JWTIssuer         string
	JWTAudience       string
	AccessTokenTTL    time.Duration
	RefreshTokenTTL   time.Duration

	// Rate limiting (Redis shared token bucket). Disabled by default; when
	// RateLimitEnabled is true, all of RedisAddr / HMACSecretFile /
	// TrustedProxyCIDRs must be valid and non-empty. The HMAC secret is read
	// from a file path (never an env var) into the short-lived
	// RateLimitHMACSecret []byte; the path is never echoed in logs or errors.
	// main.go consumes the bytes, builds the KeyDeriver, then zeroes them.
	// Fail closed on protected endpoints when Redis is unreachable.
	//
	// Each operation is gated by two independent buckets (pure-IP and
	// account/token) with independent keys; the IP and account dimensions MAY
	// share the same rate but their keys are always independent.
	RateLimitEnabled              bool
	RateLimitRedisAddr            string
	RateLimitRedisDB              int
	RateLimitHMACSecretFile       string
	RateLimitHMACSecret           []byte
	RateLimitTrustedProxies       []string
	RateLimitLoginIPCapacity      float64
	RateLimitLoginIPRefill        float64
	RateLimitLoginAcctCapacity    float64
	RateLimitLoginAcctRefill      float64
	RateLimitRegisterIPCapacity   float64
	RateLimitRegisterIPRefill     float64
	RateLimitRegisterAcctCapacity float64
	RateLimitRegisterAcctRefill   float64
	RateLimitRefreshIPCapacity    float64
	RateLimitRefreshIPRefill      float64
	RateLimitRefreshAcctCapacity  float64
	RateLimitRefreshAcctRefill    float64
	RateLimitBucketTTL            time.Duration
}

const (
	defaultHTTPAddr          = ":8080"
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	defaultShutdownTimeout   = 30 * time.Second
	defaultDBMaxOpenConns    = 25
	defaultDBMaxIdleConns    = 5
	defaultDBConnMaxLifetime = 30 * time.Minute

	// requiredDatabaseName is the only accepted database path. The auth
	// service must never connect to any other database.
	requiredDatabaseName = "tokenmp_auth"

	// JWT / refresh token defaults. Access tokens are short-lived (15m);
	// refresh tokens live 30d. The issuer/audience defaults are the stable
	// TokenMP identifiers used by this service.
	defaultJWTIssuer       = "tokenmp-auth"
	defaultJWTAudience     = "tokenmp-web"
	defaultAccessTokenTTL  = 15 * time.Minute
	defaultRefreshTokenTTL = 30 * 24 * time.Hour

	// Rate-limit defaults: conservative bursts per dimension. Capacity is the
	// max burst; refill is tokens per second (1/s == steady 1 req/s). Each
	// operation has independent IP and account/token dimensions; the defaults
	// share a conservative rate but the keys are always independent.
	rateLimitDefaultLoginIPCapacity      = 10.0
	rateLimitDefaultLoginIPRefill        = 0.5 // ~30 req/min steady per IP
	rateLimitDefaultLoginAcctCapacity    = 10.0
	rateLimitDefaultLoginAcctRefill      = 0.5 // ~30 req/min steady per account
	rateLimitDefaultRegisterIPCapacity   = 5.0
	rateLimitDefaultRegisterIPRefill     = 0.1 // ~6 req/min per IP
	rateLimitDefaultRegisterAcctCapacity = 5.0
	rateLimitDefaultRegisterAcctRefill   = 0.1
	rateLimitDefaultRefreshIPCapacity    = 30.0
	rateLimitDefaultRefreshIPRefill      = 1.0
	rateLimitDefaultRefreshAcctCapacity  = 30.0
	rateLimitDefaultRefreshAcctRefill    = 1.0
	rateLimitDefaultBucketTTL            = 10 * time.Minute
)

// Sentinel validation errors. None of them embed the URL or credentials.
var (
	ErrMissingDatabaseURL = errors.New("AUTH_DATABASE_URL is required")
	ErrInvalidDatabaseURL = errors.New("AUTH_DATABASE_URL is not a valid postgres URL")

	// Rate-limit configuration errors. They reference the env name only,
	// never the secret value, the Redis URL, or trusted-proxy topology.
	ErrRateLimitRedisAddrRequired   = errors.New("AUTH_RATE_LIMIT_REDIS_ADDR is required when rate limiting is enabled")
	ErrRateLimitSecretFileRequired  = errors.New("AUTH_RATE_LIMIT_HMAC_SECRET_FILE is required when rate limiting is enabled")
	ErrRateLimitSecretReadFailed    = errors.New("AUTH_RATE_LIMIT_HMAC_SECRET_FILE could not be read")
	ErrRateLimitSecretTooShort      = errors.New("AUTH_RATE_LIMIT_HMAC_SECRET_FILE must be at least 32 bytes")
	ErrRateLimitTrustedProxiesReqd  = errors.New("AUTH_RATE_LIMIT_TRUSTED_PROXIES must be configured when rate limiting is enabled")
	ErrRateLimitInvalidTrustedProxy = errors.New("AUTH_RATE_LIMIT_TRUSTED_PROXIES contains an invalid CIDR")
	ErrRateLimitInvalidRedisURL     = errors.New("AUTH_RATE_LIMIT_REDIS_ADDR is not a valid redis URL")
	ErrRateLimitInvalidCapacity     = errors.New("AUTH_RATE_LIMIT_*_CAPACITY must be a positive number")
	ErrRateLimitInvalidRefill       = errors.New("AUTH_RATE_LIMIT_*_REFILL must be a non-negative number")
)

// Load reads and validates configuration from the environment.
func Load() (Config, error) {
	rawURL := os.Getenv("AUTH_DATABASE_URL")
	if strings.TrimSpace(rawURL) == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	if err := validateDatabaseURL(rawURL); err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := getduration("AUTH_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	maxOpen, err := getint("AUTH_DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns)
	if err != nil {
		return Config{}, err
	}
	maxIdle, err := getint("AUTH_DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
	if err != nil {
		return Config{}, err
	}
	connMaxLifetime, err := getduration("AUTH_DB_CONN_MAX_LIFETIME", defaultDBConnMaxLifetime)
	if err != nil {
		return Config{}, err
	}
	accessTTL, err := getduration("AUTH_JWT_ACCESS_TOKEN_TTL", defaultAccessTokenTTL)
	if err != nil {
		return Config{}, err
	}
	refreshTTL, err := getduration("AUTH_JWT_REFRESH_TOKEN_TTL", defaultRefreshTokenTTL)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:          getenv("AUTH_HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:       rawURL,
		LogLevel:          strings.ToLower(getenv("AUTH_LOG_LEVEL", defaultLogLevel)),
		LogFormat:         strings.ToLower(getenv("AUTH_LOG_FORMAT", defaultLogFormat)),
		ShutdownTimeout:   shutdownTimeout,
		DBMaxOpenConns:    maxOpen,
		DBMaxIdleConns:    maxIdle,
		DBConnMaxLifetime: connMaxLifetime,

		JWTPrivateKeyFile: strings.TrimSpace(os.Getenv("AUTH_JWT_PRIVATE_KEY_FILE")),
		JWTPublicKeyFile:  strings.TrimSpace(os.Getenv("AUTH_JWT_PUBLIC_KEY_FILE")),
		JWTIssuer:         getenv("AUTH_JWT_ISSUER", defaultJWTIssuer),
		JWTAudience:       getenv("AUTH_JWT_AUDIENCE", defaultJWTAudience),
		AccessTokenTTL:    accessTTL,
		RefreshTokenTTL:   refreshTTL,
	}

	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}
	if err := validateLogFormat(cfg.LogFormat); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout < 0 {
		return Config{}, fmt.Errorf("AUTH_SHUTDOWN_TIMEOUT must be >= 0, got %s", cfg.ShutdownTimeout)
	}
	if cfg.DBMaxOpenConns < 0 {
		return Config{}, fmt.Errorf("AUTH_DB_MAX_OPEN_CONNS must be >= 0, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns < 0 {
		return Config{}, fmt.Errorf("AUTH_DB_MAX_IDLE_CONNS must be >= 0, got %d", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime < 0 {
		return Config{}, fmt.Errorf("AUTH_DB_CONN_MAX_LIFETIME must be >= 0, got %s", cfg.DBConnMaxLifetime)
	}
	if cfg.AccessTokenTTL <= 0 {
		return Config{}, fmt.Errorf("AUTH_JWT_ACCESS_TOKEN_TTL must be > 0, got %s", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL <= 0 {
		return Config{}, fmt.Errorf("AUTH_JWT_REFRESH_TOKEN_TTL must be > 0, got %s", cfg.RefreshTokenTTL)
	}
	// Refresh tokens must outlive access tokens; otherwise rotation would
	// mint refresh tokens that expire before the access token they pair with.
	if cfg.RefreshTokenTTL <= cfg.AccessTokenTTL {
		return Config{}, fmt.Errorf("AUTH_JWT_REFRESH_TOKEN_TTL (%s) must be greater than AUTH_JWT_ACCESS_TOKEN_TTL (%s)", cfg.RefreshTokenTTL, cfg.AccessTokenTTL)
	}
	if cfg.JWTIssuer == "" {
		return Config{}, fmt.Errorf("AUTH_JWT_ISSUER must not be empty")
	}
	if cfg.JWTAudience == "" {
		return Config{}, fmt.Errorf("AUTH_JWT_AUDIENCE must not be empty")
	}
	if err := loadRateLimit(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validateDatabaseURL parses rawURL and enforces the auth service connection
// contract without ever echoing the URL or its credentials in the returned
// error. The error is a stable sentinel so callers can log it safely.
func validateDatabaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidDatabaseURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "postgres" && scheme != "postgresql" {
		return ErrInvalidDatabaseURL
	}
	if u.Host == "" {
		return ErrInvalidDatabaseURL
	}
	if u.User == nil || u.User.Username() == "" {
		return ErrInvalidDatabaseURL
	}
	// Path must be exactly "/tokenmp_auth". A trailing slash, additional
	// segments or an empty path are rejected so the service never connects to
	// an unexpected database.
	if u.Path != "/"+requiredDatabaseName {
		return ErrInvalidDatabaseURL
	}
	return nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

// getint parses an integer env var. A missing or blank value falls back to the
// default. A present but non-integer value is a hard error — never a silent
// fallback — so misconfiguration cannot be masked by a default.
func getint(key string, fallback int) (int, error) {
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

// getduration parses a duration env var. Missing/blank falls back; a present
// but unparseable value is a hard error.
func getduration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration (e.g. 30s, 10m)", key)
	}
	return d, nil
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

func validateLogLevel(level string) error {
	switch level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("AUTH_LOG_LEVEL %q must be one of debug|info|warn|error", level)
	}
}

func validateLogFormat(format string) error {
	switch format {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("AUTH_LOG_FORMAT %q must be json or text", format)
	}
}

// loadRateLimit reads and validates the optional rate-limiting configuration.
// When AUTH_RATE_LIMIT_ENABLED is not explicitly "true", rate limiting is
// disabled and the remaining fields are ignored. When enabled, the Redis
// address, HMAC secret file and trusted-proxy CIDRs are all required and
// fail fast; errors never echo the secret, the Redis URL, or proxy topology.
func loadRateLimit(cfg *Config) error {
	enabledRaw := strings.TrimSpace(os.Getenv("AUTH_RATE_LIMIT_ENABLED"))
	if enabledRaw == "" {
		cfg.RateLimitEnabled = false
		return nil
	}
	enabled, err := parseStrictBool("AUTH_RATE_LIMIT_ENABLED", enabledRaw)
	if err != nil {
		return err
	}
	cfg.RateLimitEnabled = enabled
	if !enabled {
		return nil
	}

	rawRedis := strings.TrimSpace(os.Getenv("AUTH_RATE_LIMIT_REDIS_ADDR"))
	if rawRedis == "" {
		return ErrRateLimitRedisAddrRequired
	}
	if err := validateRedisURL(rawRedis); err != nil {
		return err
	}
	cfg.RateLimitRedisAddr = rawRedis

	db, err := getint("AUTH_RATE_LIMIT_REDIS_DB", 0)
	if err != nil {
		return err
	}
	if db < 0 {
		return fmt.Errorf("AUTH_RATE_LIMIT_REDIS_DB must be >= 0, got %d", db)
	}
	cfg.RateLimitRedisDB = db

	secretPath := strings.TrimSpace(os.Getenv("AUTH_RATE_LIMIT_HMAC_SECRET_FILE"))
	if secretPath == "" {
		return ErrRateLimitSecretFileRequired
	}
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		return ErrRateLimitSecretReadFailed
	}
	if len(secret) < 32 {
		return ErrRateLimitSecretTooShort
	}
	// Store the path and the short-lived secret bytes on the config; main
	// consumes the bytes, builds the deriver, and zeroes them. Neither the
	// path nor the content is ever echoed in errors.
	cfg.RateLimitHMACSecretFile = secretPath
	cfg.RateLimitHMACSecret = secret

	trusted := parseCSV(os.Getenv("AUTH_RATE_LIMIT_TRUSTED_PROXIES"))
	if len(trusted) == 0 {
		return ErrRateLimitTrustedProxiesReqd
	}
	for _, c := range trusted {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return ErrRateLimitInvalidTrustedProxy
		}
	}
	cfg.RateLimitTrustedProxies = trusted

	cfg.RateLimitLoginIPCapacity, err = getfloat("AUTH_RATE_LIMIT_LOGIN_IP_CAPACITY", rateLimitDefaultLoginIPCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitLoginIPRefill, err = getfloat("AUTH_RATE_LIMIT_LOGIN_IP_REFILL", rateLimitDefaultLoginIPRefill)
	if err != nil {
		return err
	}
	cfg.RateLimitLoginAcctCapacity, err = getfloat("AUTH_RATE_LIMIT_LOGIN_ACCOUNT_CAPACITY", rateLimitDefaultLoginAcctCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitLoginAcctRefill, err = getfloat("AUTH_RATE_LIMIT_LOGIN_ACCOUNT_REFILL", rateLimitDefaultLoginAcctRefill)
	if err != nil {
		return err
	}
	cfg.RateLimitRegisterIPCapacity, err = getfloat("AUTH_RATE_LIMIT_REGISTER_IP_CAPACITY", rateLimitDefaultRegisterIPCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitRegisterIPRefill, err = getfloat("AUTH_RATE_LIMIT_REGISTER_IP_REFILL", rateLimitDefaultRegisterIPRefill)
	if err != nil {
		return err
	}
	cfg.RateLimitRegisterAcctCapacity, err = getfloat("AUTH_RATE_LIMIT_REGISTER_ACCOUNT_CAPACITY", rateLimitDefaultRegisterAcctCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitRegisterAcctRefill, err = getfloat("AUTH_RATE_LIMIT_REGISTER_ACCOUNT_REFILL", rateLimitDefaultRegisterAcctRefill)
	if err != nil {
		return err
	}
	cfg.RateLimitRefreshIPCapacity, err = getfloat("AUTH_RATE_LIMIT_REFRESH_IP_CAPACITY", rateLimitDefaultRefreshIPCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitRefreshIPRefill, err = getfloat("AUTH_RATE_LIMIT_REFRESH_IP_REFILL", rateLimitDefaultRefreshIPRefill)
	if err != nil {
		return err
	}
	cfg.RateLimitRefreshAcctCapacity, err = getfloat("AUTH_RATE_LIMIT_REFRESH_ACCOUNT_CAPACITY", rateLimitDefaultRefreshAcctCapacity)
	if err != nil {
		return err
	}
	cfg.RateLimitRefreshAcctRefill, err = getfloat("AUTH_RATE_LIMIT_REFRESH_ACCOUNT_REFILL", rateLimitDefaultRefreshAcctRefill)
	if err != nil {
		return err
	}
	for _, c := range []struct {
		name      string
		v         float64
		allowZero bool
	}{
		{"AUTH_RATE_LIMIT_LOGIN_IP_CAPACITY", cfg.RateLimitLoginIPCapacity, false},
		{"AUTH_RATE_LIMIT_LOGIN_IP_REFILL", cfg.RateLimitLoginIPRefill, true},
		{"AUTH_RATE_LIMIT_LOGIN_ACCOUNT_CAPACITY", cfg.RateLimitLoginAcctCapacity, false},
		{"AUTH_RATE_LIMIT_LOGIN_ACCOUNT_REFILL", cfg.RateLimitLoginAcctRefill, true},
		{"AUTH_RATE_LIMIT_REGISTER_IP_CAPACITY", cfg.RateLimitRegisterIPCapacity, false},
		{"AUTH_RATE_LIMIT_REGISTER_IP_REFILL", cfg.RateLimitRegisterIPRefill, true},
		{"AUTH_RATE_LIMIT_REGISTER_ACCOUNT_CAPACITY", cfg.RateLimitRegisterAcctCapacity, false},
		{"AUTH_RATE_LIMIT_REGISTER_ACCOUNT_REFILL", cfg.RateLimitRegisterAcctRefill, true},
		{"AUTH_RATE_LIMIT_REFRESH_IP_CAPACITY", cfg.RateLimitRefreshIPCapacity, false},
		{"AUTH_RATE_LIMIT_REFRESH_IP_REFILL", cfg.RateLimitRefreshIPRefill, true},
		{"AUTH_RATE_LIMIT_REFRESH_ACCOUNT_CAPACITY", cfg.RateLimitRefreshAcctCapacity, false},
		{"AUTH_RATE_LIMIT_REFRESH_ACCOUNT_REFILL", cfg.RateLimitRefreshAcctRefill, true},
	} {
		if c.v < 0 || (!c.allowZero && c.v <= 0) {
			if c.allowZero {
				return fmt.Errorf("%s must be >= 0", c.name)
			}
			return fmt.Errorf("%s must be > 0", c.name)
		}
	}

	ttl, err := getduration("AUTH_RATE_LIMIT_BUCKET_TTL", rateLimitDefaultBucketTTL)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("AUTH_RATE_LIMIT_BUCKET_TTL must be > 0")
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
			return ErrRateLimitInvalidRedisURL
		}
		return nil
	}
	// Bare host:port form. Reject anything carrying a scheme separator.
	if strings.Contains(raw, "://") {
		return ErrRateLimitInvalidRedisURL
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" || port == "" {
		return ErrRateLimitInvalidRedisURL
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return ErrRateLimitInvalidRedisURL
		}
	}
	return nil
}

// parseCSV splits a comma-separated env value into trimmed, non-empty parts.
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

// getfloat parses a float env var. Missing/blank falls back; a present but
// non-float value is a hard error.
func getfloat(key string, fallback float64) (float64, error) {
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
