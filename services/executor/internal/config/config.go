// Package config loads Executor runtime configuration.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHTTPAddr          = "127.0.0.1:8081"
	defaultShutdownTimeout   = 10 * time.Second
	defaultReadHeaderTimeout = 10 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultJWTIssuer         = "tokenmp-auth"
	defaultJWTAudience       = "tokenmp-web"
	defaultMetricsEnabled    = true
	defaultMetricsPath       = "/metrics"
)

// Required environment variables. Each must be present and non-empty at load
// time so startup fails closed before the process listens. Their values are
// never echoed into error messages: EXECUTOR_CREDENTIAL_REF_MAP_JSON and
// EXECUTOR_IDENTITY_MAP_JSON can be long and carry non-secret mapping topology,
// and EXECUTOR_CONFIG_FILE names a filesystem path. Errors name only the
// variable and a generic reason.
const (
	// EnvConfigFile names the strict secret-free configuration file path
	// consumed by the config source at composition time.
	EnvConfigFile = "EXECUTOR_CONFIG_FILE"
	// EnvConfigServiceURL is the optional Config Service latest-snapshot URL.
	// When set it is preferred over EnvConfigFile.
	EnvConfigServiceURL = "EXECUTOR_CONFIG_SERVICE_URL"
	// EnvCredentialRefMapJSON is the non-secret vault credential-ref →
	// EXECUTOR_CREDENTIAL_* environment-name JSON mapping consumed by the
	// credential environment resolver.
	EnvCredentialRefMapJSON = "EXECUTOR_CREDENTIAL_REF_MAP_JSON"
	// EnvIdentityMapJSON is the non-secret entry ID → identity mapping
	// consumed by the identity environment resolver.
	EnvIdentityMapJSON = "EXECUTOR_IDENTITY_MAP_JSON"
	// EnvJWTPublicKeyFile is the path to the PKIX PEM-encoded Ed25519
	// public key file used for JWT verification.
	EnvJWTPublicKeyFile = "EXECUTOR_JWT_PUBLIC_KEY_FILE"
	// EnvJWTIssuer is the expected JWT issuer claim.
	EnvJWTIssuer = "EXECUTOR_JWT_ISSUER"
	// EnvJWTAudience is the expected JWT audience claim.
	EnvJWTAudience = "EXECUTOR_JWT_AUDIENCE"
	// EnvConfigReloadInterval is the optional stat-based file change polling
	// interval. When unset or zero, only SIGHUP triggers reloads.
	EnvConfigReloadInterval = "EXECUTOR_CONFIG_RELOAD_INTERVAL"
	// EnvMetricsEnabled controls whether the /metrics endpoint is served.
	// Defaults to true when unset or empty.
	EnvMetricsEnabled = "EXECUTOR_METRICS_ENABLED"
	// EnvMetricsPath is the URL path for the metrics endpoint.
	// Defaults to /metrics when unset or empty.
	EnvMetricsPath = "EXECUTOR_METRICS_PATH"
	// EnvLoggingServiceURL is the optional Logging Service base URL. When
	// set, the runtime composition wraps the in-memory execution log with a
	// remote sink that best-effort forwards lifecycle events to the Logging
	// Service ingest endpoint. When unset or empty, only the local
	// in-memory log is used and no remote forwarding occurs.
	EnvLoggingServiceURL = "EXECUTOR_LOGGING_SERVICE_URL"
)

// Config is the validated runtime configuration for Executor.
type Config struct {
	HTTPAddr          string
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	// ConfigFile is the optional fallback configuration file path. It is used
	// when ConfigServiceURL is empty.
	ConfigFile string
	// ConfigServiceURL is an optional validated HTTP(S) Config Service latest
	// snapshot endpoint. When non-empty it takes precedence over ConfigFile.
	ConfigServiceURL string
	// CredentialRefMapJSON is the non-secret credential-ref mapping JSON. It
	// is retained verbatim for the composition root; its contents are never
	// surfaced through formatting or errors.
	CredentialRefMapJSON string
	// JWTPublicKeyFile is the path to the Ed25519 public key PEM file. When
	// non-empty, JWT verification is used as the primary identity source.
	// When empty, the identityenv source is used as fallback.
	JWTPublicKeyFile string
	// JWTIssuer is the expected JWT issuer. Defaults to "tokenmp-auth".
	JWTIssuer string
	// JWTAudience is the expected JWT audience. Defaults to "tokenmp-web".
	JWTAudience string
	// ConfigReloadInterval controls the stat-based file change polling
	// interval. When zero (default), stat-based polling is disabled and
	// only SIGHUP triggers reloads. When positive, the polling goroutine
	// stats the config file and triggers a reload when mtime or size
	// changes.
	ConfigReloadInterval time.Duration
	// MetricsEnabled controls whether the /metrics Prometheus endpoint is
	// served. Defaults to true.
	MetricsEnabled bool
	// MetricsPath is the URL path for the metrics endpoint. Must be
	// non-empty and start with '/'. Defaults to /metrics.
	MetricsPath string
	// LoggingServiceURL is the optional Logging Service base URL
	// (http(s)://host[:port] with no userinfo, query, or fragment, and path
	// empty or "/"). When non-empty, the runtime composition wraps the
	// in-memory execution log with a remote sink that best-effort forwards
	// lifecycle events to the Logging Service /v1/logs/ingest endpoint. When
	// empty, only the local in-memory execution log is used.
	LoggingServiceURL string
}

// Load reads Executor configuration from lookupEnv. An unset value uses its
// default. An explicitly empty HTTP address uses its default, while an HTTP
// address containing only whitespace is rejected. Explicitly empty, invalid,
// and non-positive durations are rejected.
//
// At least one of EXECUTOR_CONFIG_FILE and EXECUTOR_CONFIG_SERVICE_URL is required; the service URL takes precedence when both are set. EXECUTOR_CREDENTIAL_REF_MAP_JSON is required.
// EXECUTOR_IDENTITY_MAP_JSON is required when EXECUTOR_JWT_PUBLIC_KEY_FILE is
// not set; when JWT is configured, identityenv becomes optional fallback.
// Values are never included in error messages.
//
// EXECUTOR_JWT_PUBLIC_KEY_FILE is optional. When set, JWT verification is the
// primary identity source. EXECUTOR_JWT_ISSUER defaults to "tokenmp-auth" and
// EXECUTOR_JWT_AUDIENCE defaults to "tokenmp-web".
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

	config.ConfigFile, _ = lookupEnv(EnvConfigFile)
	config.ConfigFile = strings.TrimSpace(config.ConfigFile)
	config.ConfigServiceURL, _ = lookupEnv(EnvConfigServiceURL)
	config.ConfigServiceURL = strings.TrimSpace(config.ConfigServiceURL)
	if config.ConfigServiceURL != "" {
		if !validConfigServiceURL(config.ConfigServiceURL) {
			return Config{}, fmt.Errorf("%s must be the http(s) latest snapshot endpoint without query, fragment, or userinfo", EnvConfigServiceURL)
		}
	} else if config.ConfigFile == "" {
		return Config{}, fmt.Errorf("%s or %s must be set", EnvConfigServiceURL, EnvConfigFile)
	}
	if config.CredentialRefMapJSON, err = requireNonEmpty(lookupEnv, EnvCredentialRefMapJSON); err != nil {
		return Config{}, err
	}
	// JWT configuration: optional. When JWTPublicKeyFile is set, JWT
	// verification is the primary identity source; identityenv is fallback.
	config.JWTPublicKeyFile, _ = lookupEnv(EnvJWTPublicKeyFile)
	config.JWTPublicKeyFile = strings.TrimSpace(config.JWTPublicKeyFile)

	config.JWTIssuer = defaultJWTIssuer
	if value, ok := lookupEnv(EnvJWTIssuer); ok && strings.TrimSpace(value) != "" {
		config.JWTIssuer = strings.TrimSpace(value)
	}
	config.JWTAudience = defaultJWTAudience
	if value, ok := lookupEnv(EnvJWTAudience); ok && strings.TrimSpace(value) != "" {
		config.JWTAudience = strings.TrimSpace(value)
	}

	// EXECUTOR_IDENTITY_MAP_JSON is required when JWT is not configured.
	// When JWT is configured, it becomes optional (identityenv is fallback).
	if config.JWTPublicKeyFile == "" {
		if _, err = requireNonEmpty(lookupEnv, EnvIdentityMapJSON); err != nil {
			return Config{}, err
		}
	}

	// EXECUTOR_CONFIG_RELOAD_INTERVAL is optional. Zero (default) disables
	// stat-based polling; only SIGHUP triggers reloads. A positive duration
	// enables mtime+size polling.
	if value, ok := lookupEnv(EnvConfigReloadInterval); ok && strings.TrimSpace(value) != "" {
		interval, err := time.ParseDuration(value)
		if err != nil || interval < 0 {
			return Config{}, fmt.Errorf("%s must be a valid non-negative duration", EnvConfigReloadInterval)
		}
		config.ConfigReloadInterval = interval
	}

	// Metrics configuration: optional. Defaults to enabled at /metrics.
	config.MetricsEnabled = defaultMetricsEnabled
	if value, ok := lookupEnv(EnvMetricsEnabled); ok && strings.TrimSpace(value) != "" {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "false", "0", "no", "off":
			config.MetricsEnabled = false
		default:
			config.MetricsEnabled = true
		}
	}

	config.MetricsPath = defaultMetricsPath
	if value, ok := lookupEnv(EnvMetricsPath); ok && strings.TrimSpace(value) != "" {
		path := strings.TrimSpace(value)
		if path == "" || path[0] != '/' {
			return Config{}, fmt.Errorf("%s must be non-empty and start with '/'", EnvMetricsPath)
		}
		config.MetricsPath = path
	}

	// Logging Service URL is optional. When set it must be a valid http(s)
	// base URL without userinfo, query, or fragment so the remote sink cannot
	// be pointed at an unexpected resource or carry embedded credentials.
	config.LoggingServiceURL, _ = lookupEnv(EnvLoggingServiceURL)
	config.LoggingServiceURL = strings.TrimSpace(config.LoggingServiceURL)
	if config.LoggingServiceURL != "" && !validLoggingServiceURL(config.LoggingServiceURL) {
		return Config{}, fmt.Errorf("%s must be an http(s) base url without userinfo, query, or fragment", EnvLoggingServiceURL)
	}

	return config, nil
}

// requireNonEmpty returns a present, non-empty (after trimming) environment
// value, or a redacted error that names only the variable. It never includes
// the value, JSON content, or path in the error.
func requireNonEmpty(lookupEnv func(string) (string, bool), key string) (string, error) {
	value, ok := lookupEnv(key)
	if !ok {
		return "", fmt.Errorf("%s must be set", key)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return value, nil
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

// validConfigServiceURL accepts only absolute HTTP(S) endpoints without URL
// components that could carry credentials or alter the requested resource.
func validConfigServiceURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Path == "/v1/config/snapshots/latest"
}

// validLoggingServiceURL accepts only an absolute http(s):// base URL with a
// non-empty host and no userinfo, query, or fragment. The path must be empty
// or "/"; the runtime composition appends the fixed /v1/logs/ingest path.
// This mirrors the strictness of validConfigServiceURL but for the Logging
// Service base URL rather than a fixed snapshot endpoint.
func validLoggingServiceURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u == nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}
