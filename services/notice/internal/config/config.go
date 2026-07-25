// Package config loads and validates the notice service runtime configuration.
//
// Configuration is sourced exclusively from environment variables. The service
// fails fast on missing or invalid required values; optional values fall back
// to safe defaults that never include production credentials.
//
// NOTICE_DATABASE_URL is strictly validated: only postgres/postgresql URLs
// are accepted, must carry a host, a non-empty user and a path of exactly
// /tokenmp_biz. Any validation error reports only the failing field, never
// the URL value or its credentials.
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

// Config is the validated notice service runtime configuration.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        string
	LogFormat       string
	ShutdownTimeout time.Duration
	DBMaxOpenConns  int
	DBMaxIdleConns  int
	ConnMaxLifetime time.Duration

	// JWT verification. The notice service verifies tokens with the Auth
	// Service public key; it does not issue tokens. The PEM is parsed by the
	// jwtverifier package at startup; the path is never echoed in errors.
	JWTPublicKeyFile string
	JWTIssuer        string
	JWTAudience      string
}

const (
	defaultHTTPAddr        = ":8081"
	defaultLogLevel        = "info"
	defaultLogFormat       = "json"
	defaultShutdownTimeout = 30 * time.Second
	defaultDBMaxOpenConns  = 25
	defaultDBMaxIdleConns  = 5
	defaultConnMaxLifetime = 30 * time.Minute

	requiredDatabaseName = "tokenmp_biz"

	defaultJWTIssuer   = "tokenmp-auth"
	defaultJWTAudience = "tokenmp-web"
)

// Sentinel validation errors. None embed the URL or credentials.
var (
	ErrMissingDatabaseURL  = errors.New("NOTICE_DATABASE_URL is required")
	ErrInvalidDatabaseURL  = errors.New("NOTICE_DATABASE_URL is not a valid postgres URL")
	ErrMissingJWTPublicKey = errors.New("NOTICE_JWT_PUBLIC_KEY_FILE is required")
)

// Load reads and validates configuration from the environment.
func Load() (Config, error) {
	rawURL := os.Getenv("NOTICE_DATABASE_URL")
	if strings.TrimSpace(rawURL) == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	if err := validateDatabaseURL(rawURL); err != nil {
		return Config{}, err
	}

	pubKeyFile := strings.TrimSpace(os.Getenv("NOTICE_JWT_PUBLIC_KEY_FILE"))
	if pubKeyFile == "" {
		return Config{}, ErrMissingJWTPublicKey
	}

	shutdownTimeout, err := getduration("NOTICE_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	maxOpen, err := getint("NOTICE_DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns)
	if err != nil {
		return Config{}, err
	}
	maxIdle, err := getint("NOTICE_DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
	if err != nil {
		return Config{}, err
	}
	connMaxLifetime, err := getduration("NOTICE_DB_CONN_MAX_LIFETIME", defaultConnMaxLifetime)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:        getenv("NOTICE_HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:     rawURL,
		LogLevel:        strings.ToLower(getenv("NOTICE_LOG_LEVEL", defaultLogLevel)),
		LogFormat:       strings.ToLower(getenv("NOTICE_LOG_FORMAT", defaultLogFormat)),
		ShutdownTimeout: shutdownTimeout,
		DBMaxOpenConns:  maxOpen,
		DBMaxIdleConns:  maxIdle,
		ConnMaxLifetime: connMaxLifetime,

		JWTPublicKeyFile: pubKeyFile,
		JWTIssuer:        getenv("NOTICE_JWT_ISSUER", defaultJWTIssuer),
		JWTAudience:      getenv("NOTICE_JWT_AUDIENCE", defaultJWTAudience),
	}

	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}
	if err := validateLogFormat(cfg.LogFormat); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout < 0 {
		return Config{}, fmt.Errorf("NOTICE_SHUTDOWN_TIMEOUT must be >= 0, got %s", cfg.ShutdownTimeout)
	}
	if cfg.DBMaxOpenConns < 0 {
		return Config{}, fmt.Errorf("NOTICE_DB_MAX_OPEN_CONNS must be >= 0, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns < 0 {
		return Config{}, fmt.Errorf("NOTICE_DB_MAX_IDLE_CONNS must be >= 0, got %d", cfg.DBMaxIdleConns)
	}
	if cfg.ConnMaxLifetime < 0 {
		return Config{}, fmt.Errorf("NOTICE_DB_CONN_MAX_LIFETIME must be >= 0, got %s", cfg.ConnMaxLifetime)
	}
	if cfg.JWTIssuer == "" {
		return Config{}, fmt.Errorf("NOTICE_JWT_ISSUER must not be empty")
	}
	if cfg.JWTAudience == "" {
		return Config{}, fmt.Errorf("NOTICE_JWT_AUDIENCE must not be empty")
	}
	return cfg, nil
}

// validateDatabaseURL enforces the notice service connection contract without
// ever echoing the URL or credentials. The error is a stable sentinel.
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
		return fmt.Errorf("NOTICE_LOG_LEVEL %q must be one of debug|info|warn|error", level)
	}
}

func validateLogFormat(format string) error {
	switch format {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("NOTICE_LOG_FORMAT %q must be json or text", format)
	}
}
