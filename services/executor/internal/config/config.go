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
	defaultJWTIssuer         = "tokenmp-auth"
	defaultJWTAudience       = "tokenmp-web"
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
)

// Config is the validated runtime configuration for Executor.
type Config struct {
	HTTPAddr          string
	ShutdownTimeout   time.Duration
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	// ConfigFile is the validated, non-empty configuration file path. The
	// composition root loads, scans, and compiles it at startup.
	ConfigFile string
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
}

// Load reads Executor configuration from lookupEnv. An unset value uses its
// default. An explicitly empty HTTP address uses its default, while an HTTP
// address containing only whitespace is rejected. Explicitly empty, invalid,
// and non-positive durations are rejected.
//
// EXECUTOR_CONFIG_FILE and EXECUTOR_CREDENTIAL_REF_MAP_JSON are required.
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

	if config.ConfigFile, err = requireNonEmpty(lookupEnv, EnvConfigFile); err != nil {
		return Config{}, err
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
