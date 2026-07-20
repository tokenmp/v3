// Package config loads Executor runtime configuration.
package config

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultHTTPAddr          = "127.0.0.1:8081"
	defaultShutdownTimeout   = 10 * time.Second
	defaultReadHeaderTimeout = 10 * time.Second
	defaultIdleTimeout       = 60 * time.Second
)

// Config is the validated runtime configuration for Executor.
type Config struct {
	HTTPAddr          string
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
}

// Load reads Executor configuration from lookupEnv. An unset value uses its
// default. An explicitly empty HTTP address uses its default, while an HTTP
// address containing only whitespace is rejected. Explicitly empty, invalid,
// and non-positive durations are rejected.
func Load(lookupEnv func(string) (string, bool)) (Config, error) {
	config := Config{
		HTTPAddr:          defaultHTTPAddr,
		ShutdownTimeout:   defaultShutdownTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		IdleTimeout:       defaultIdleTimeout,
	}

	if value, ok := lookupEnv("EXECUTOR_HTTP_ADDR"); ok {
		switch {
		case value == "":
			// Use the default.
		case strings.TrimSpace(value) == "":
			return Config{}, fmt.Errorf("EXECUTOR_HTTP_ADDR must not contain only whitespace")
		default:
			config.HTTPAddr = value
		}
	}

	var err error
	if config.ShutdownTimeout, err = loadPositiveDuration(lookupEnv, "EXECUTOR_SHUTDOWN_TIMEOUT", config.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if config.ReadHeaderTimeout, err = loadPositiveDuration(lookupEnv, "EXECUTOR_READ_HEADER_TIMEOUT", config.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if config.IdleTimeout, err = loadPositiveDuration(lookupEnv, "EXECUTOR_IDLE_TIMEOUT", config.IdleTimeout); err != nil {
		return Config{}, err
	}

	return config, nil
}

func loadPositiveDuration(lookupEnv func(string) (string, bool), key string, defaultValue time.Duration) (time.Duration, error) {
	value, ok := lookupEnv(key)
	if !ok {
		return defaultValue, nil
	}
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return duration, nil
}
