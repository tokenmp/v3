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
)

// Sentinel validation errors. None of them embed the URL or credentials.
var (
	ErrMissingDatabaseURL = errors.New("AUTH_DATABASE_URL is required")
	ErrInvalidDatabaseURL = errors.New("AUTH_DATABASE_URL is not a valid postgres URL")
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
