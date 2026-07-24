// Package config loads API Service (Edge/BFF) runtime configuration from
// environment variables. All values are read once at startup; there is no
// hot-reload.
package config

import (
	"errors"
	"fmt"
	"os"
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

	// JWTPublicKeyFile is the path to the Ed25519 public key PEM file used
	// to verify client JWTs. Optional; when empty, JWT verification is
	// disabled (dev-only; production must set this).
	JWTPublicKeyFile string

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

	cfg.JWTPublicKeyFile = strings.TrimSpace(os.Getenv("API_JWT_PUBLIC_KEY_FILE"))

	if v := strings.TrimSpace(os.Getenv("API_JWT_ISSUER")); v != "" {
		cfg.JWTIssuer = v
	}
	if v := strings.TrimSpace(os.Getenv("API_JWT_AUDIENCE")); v != "" {
		cfg.JWTAudience = v
	}

	cfg.ExecutorToken = os.Getenv("API_EXECUTOR_TOKEN")
	// ExecutorToken is optional (JWT passthrough mode when empty).

	return cfg, nil
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
