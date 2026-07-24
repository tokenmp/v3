// Package config loads and validates the logging service runtime configuration.
//
// Configuration is sourced exclusively from environment variables. The service
// fails fast on missing or invalid required values; optional values fall back
// to safe defaults that never include production credentials.
//
// LOGGING_DATABASE_URL is strictly validated: only postgres/postgresql URLs are
// accepted, must carry a host, a non-empty user and a path of exactly
// /tokenmp_logging. Any validation error reports only the failing field, never
// the URL value or its credentials. Numeric and duration tunables fail fast on
// non-parseable or negative input — they never silently fall back.
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

// Config is the validated logging service runtime configuration.
type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	LogLevel          string
	LogFormat         string
	ShutdownTimeout   time.Duration
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
}

const (
	defaultHTTPAddr          = ":8083"
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	defaultShutdownTimeout   = 30 * time.Second
	defaultDBMaxOpenConns    = 10
	defaultDBMaxIdleConns    = 2
	defaultDBConnMaxLifetime = 30 * time.Minute

	// requiredDatabaseName is the only accepted database path. The logging
	// service must never connect to any other database.
	requiredDatabaseName = "tokenmp_logging"
)

// Sentinel validation errors. None of them embed the URL or credentials.
var (
	ErrMissingDatabaseURL = errors.New("LOGGING_DATABASE_URL is required")
	ErrInvalidDatabaseURL = errors.New("LOGGING_DATABASE_URL is not a valid postgres URL")
)

// Load reads and validates configuration from the environment.
func Load() (Config, error) {
	rawURL := os.Getenv("LOGGING_DATABASE_URL")
	if strings.TrimSpace(rawURL) == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	if err := validateDatabaseURL(rawURL); err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := getduration("LOGGING_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	maxOpen, err := getint("LOGGING_DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns)
	if err != nil {
		return Config{}, err
	}
	maxIdle, err := getint("LOGGING_DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
	if err != nil {
		return Config{}, err
	}
	connMaxLifetime, err := getduration("LOGGING_DB_CONN_MAX_LIFETIME", defaultDBConnMaxLifetime)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:          getenv("LOGGING_HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:       rawURL,
		LogLevel:          strings.ToLower(getenv("LOGGING_LOG_LEVEL", defaultLogLevel)),
		LogFormat:         strings.ToLower(getenv("LOGGING_LOG_FORMAT", defaultLogFormat)),
		ShutdownTimeout:   shutdownTimeout,
		DBMaxOpenConns:    maxOpen,
		DBMaxIdleConns:    maxIdle,
		DBConnMaxLifetime: connMaxLifetime,
	}

	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}
	if err := validateLogFormat(cfg.LogFormat); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout < 0 {
		return Config{}, fmt.Errorf("LOGGING_SHUTDOWN_TIMEOUT must be >= 0, got %s", cfg.ShutdownTimeout)
	}
	if cfg.DBMaxOpenConns < 0 {
		return Config{}, fmt.Errorf("LOGGING_DB_MAX_OPEN_CONNS must be >= 0, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns < 0 {
		return Config{}, fmt.Errorf("LOGGING_DB_MAX_IDLE_CONNS must be >= 0, got %d", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime < 0 {
		return Config{}, fmt.Errorf("LOGGING_DB_CONN_MAX_LIFETIME must be >= 0, got %s", cfg.DBConnMaxLifetime)
	}
	return cfg, nil
}

// validateDatabaseURL parses rawURL and enforces the logging service connection
// contract without ever echoing the URL or its credentials in the returned
// error. The error is a stable sentinel so callers can log it safely.
//
// Two forms are accepted:
//   - host form: scheme postgres/postgresql, non-empty host, non-empty user,
//     path exactly /tokenmp_logging.
//   - socket form: scheme postgres/postgresql, empty host but a non-empty
//     host= query param (Unix socket path), path exactly /tokenmp_logging.
//     The user may be inherited from the OS in this form.
func validateDatabaseURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidDatabaseURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "postgres" && scheme != "postgresql" {
		return ErrInvalidDatabaseURL
	}
	if u.Path != "/"+requiredDatabaseName {
		return ErrInvalidDatabaseURL
	}
	// host form requires host + user
	if u.Host != "" {
		if u.User == nil || u.User.Username() == "" {
			return ErrInvalidDatabaseURL
		}
		return nil
	}
	// socket form: empty host must be backed by a host= query param
	if q := u.Query().Get("host"); strings.TrimSpace(q) == "" {
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
		return fmt.Errorf("LOGGING_LOG_LEVEL %q must be one of debug|info|warn|error", level)
	}
}

func validateLogFormat(format string) error {
	switch format {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("LOGGING_LOG_FORMAT %q must be json or text", format)
	}
}
