// Package config loads API Service runtime configuration from environment
// variables. All values are read once at startup; there is no hot-reload.
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
	}

	if v := os.Getenv("API_HTTP_ADDR"); v != "" {
		if strings.TrimSpace(v) == "" {
			return nil, errors.New("API_HTTP_ADDR must not be blank")
		}
		cfg.HTTPAddr = v
	}

	if v := os.Getenv("API_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("API_SHUTDOWN_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("API_SHUTDOWN_TIMEOUT must be positive")
		}
		cfg.ShutdownTimeout = d
	}

	if v := os.Getenv("API_READ_HEADER_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("API_READ_HEADER_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("API_READ_HEADER_TIMEOUT must be positive")
		}
		cfg.ReadHeaderTimeout = d
	}

	if v := os.Getenv("API_IDLE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("API_IDLE_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return nil, errors.New("API_IDLE_TIMEOUT must be positive")
		}
		cfg.IdleTimeout = d
	}

	return cfg, nil
}
