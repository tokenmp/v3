// Package config loads API Service (Edge/BFF) runtime configuration from
// environment variables. All values are read once at startup; there is no
// hot-reload.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
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

	// ConfigServiceToken is the opaque shared secret forwarded to the Config
	// Service as X-Admin-Token for service-to-service admin authorization.
	// Required when ConfigAdminProxyEnabled is true (fail-fast at startup so the
	// admin proxy never runs half-secured). Optional for the read-only client
	// (anonymous snapshot/model reads). Never logged.
	//
	// Resolution order at Load time:
	//   1. API_CONFIG_SERVICE_TOKEN_FILE (production): read from a regular file
	//      via the strict secret-file loader (Lstat rejects symlink/non-regular,
	//      post-open SameFile TOCTOU guard, size cap, strict UTF-8, no NUL/newline,
	//      trimmed). This is the only source compatible with a Compose
	//      read-only secret mount.
	//   2. API_CONFIG_SERVICE_TOKEN (dev/test only): the literal token value is
	//      read directly from the environment. This exists solely for local
	//      development and the no-database unit test suite; it MUST NOT be used
	//      in production (the value would land in the process environment / a
	//      Compose env file). Providing BOTH sources is a hard startup error.
	ConfigServiceToken string

	// ConfigAdminProxyEnabled gates registration of the admin config CRUD proxy
	// routes (/api/v1/admin/config/*). When true, ConfigServiceToken is
	// required at startup (fail-fast). When false (default), no admin proxy is
	// registered; the read-only Config Service client (model catalog) can still
	// be used without a token. Explicit opt-in avoids a default-open write path.
	ConfigAdminProxyEnabled bool

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
	if err := loadConfigServiceToken(cfg); err != nil {
		return nil, err
	}
	if v, ok := os.LookupEnv("API_CONFIG_ADMIN_PROXY_ENABLED"); ok {
		enabled, err := parseStrictBool("API_CONFIG_ADMIN_PROXY_ENABLED", v)
		if err != nil {
			return nil, err
		}
		cfg.ConfigAdminProxyEnabled = enabled
	}
	// Fail-fast: the admin proxy must never start half-secured. When the proxy
	// is enabled, the shared secret is mandatory (read-only use does not set
	// this flag, so it is unaffected).
	if cfg.ConfigAdminProxyEnabled && strings.TrimSpace(cfg.ConfigServiceToken) == "" {
		return nil, errors.New("API_CONFIG_SERVICE_TOKEN_FILE (or API_CONFIG_SERVICE_TOKEN in dev/test) is required when API_CONFIG_ADMIN_PROXY_ENABLED=true")
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

// maxConfigTokenBytes bounds the size of a Config Service admin token file so
// a hostile or accidentally huge file cannot exhaust memory. A shared secret
// is far smaller than this cap; anything larger is rejected.
const maxConfigTokenBytes int64 = 8 << 10 // 8 KiB

// loadConfigServiceToken resolves the Config Service admin shared secret into
// cfg.ConfigServiceToken. The production source is
// API_CONFIG_SERVICE_TOKEN_FILE, read via loadTokenFile (Lstat rejects
// symlink/non-regular, post-open SameFile TOCTOU guard, size cap, strict
// UTF-8, no NUL/newline, trimmed). The legacy direct
// API_CONFIG_SERVICE_TOKEN is retained solely for local dev/test so the
// no-database unit test suite can inject a literal value without touching the
// filesystem; it MUST NOT be used in production. Providing BOTH sources is a
// hard startup error. All failures are stable sentinel errors that never
// echo the file path, OS error text, or token content.
func loadConfigServiceToken(cfg *Config) error {
	tokenFile := strings.TrimSpace(os.Getenv("API_CONFIG_SERVICE_TOKEN_FILE"))
	directToken := os.Getenv("API_CONFIG_SERVICE_TOKEN")

	if tokenFile != "" && directToken != "" {
		return errors.New("API_CONFIG_SERVICE_TOKEN_FILE and API_CONFIG_SERVICE_TOKEN are mutually exclusive; set only one")
	}

	if tokenFile != "" {
		t, err := loadTokenFile(tokenFile)
		if err != nil {
			return err
		}
		cfg.ConfigServiceToken = t
		return nil
	}

	// Direct env (dev/test only). Trim exactly as the file path would so the
	// two sources are interchangeable for callers.
	cfg.ConfigServiceToken = strings.TrimSpace(directToken)
	return nil
}

// loadTokenFile reads the shared secret from a regular file using the project's
// established secret-file safety pattern (mirroring the Executor configsource
// and Auth key-file loaders). It is fail-closed: every non-happy path returns
// a stable sentinel error that never leaks the path, OS error text, or token
// content.
//
// Safety properties:
//   - The path is trimmed and rejected as blank before any filesystem access.
//   - Lstat rejects symlinks and non-regular files without following links.
//   - After opening, a post-open f.Stat verifies the descriptor is still a
//     regular file referring to the same identity (os.SameFile) as the prior
//     Lstat. This closes the Lstat-then-Open TOCTOU window in which the path
//     is swapped (e.g. to a symlink) between the two calls.
//   - The file size is bounded by maxConfigTokenBytes via both the stat and a
//     LimitReader so a file that lies about its size or grows mid-read cannot
//     exhaust memory.
//   - The content is validated for strict UTF-8 and must not contain NUL bytes
//     or newline characters (a single-line shared secret has none), then is
//     trimmed of surrounding whitespace.
//   - An empty result (blank after trim) is rejected.
func loadTokenFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errTokenFileRequired
	}

	info, err := os.Lstat(path)
	if err != nil {
		return "", errTokenLoadFailed
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errTokenLoadFailed
	}
	if info.Size() > maxConfigTokenBytes {
		return "", errTokenLoadFailed
	}

	f, err := os.Open(path)
	if err != nil {
		return "", errTokenLoadFailed
	}
	defer f.Close()

	// Fail-closed post-open verification: the open descriptor must be a regular
	// file and refer to the same file identity as the prior Lstat. f.Stat
	// follows symlinks, so a path swapped to a symlink between Lstat and Open
	// is caught here.
	fi, err := f.Stat()
	if err != nil {
		return "", errTokenLoadFailed
	}
	if !fi.Mode().IsRegular() || !os.SameFile(info, fi) {
		return "", errTokenLoadFailed
	}

	raw, err := io.ReadAll(io.LimitReader(f, maxConfigTokenBytes+1))
	if err != nil {
		return "", errTokenLoadFailed
	}
	if int64(len(raw)) > maxConfigTokenBytes {
		return "", errTokenLoadFailed
	}

	t := strings.TrimSpace(string(raw))
	if t == "" {
		return "", errTokenLoadFailed
	}
	if !utf8.ValidString(t) {
		return "", errTokenLoadFailed
	}
	if strings.ContainsRune(t, '\x00') || strings.ContainsAny(t, "\r\n") {
		return "", errTokenLoadFailed
	}
	return t, nil
}

// Sentinel errors for the Config Service admin token file loader. They never
// echo the file path, OS error text, or token content; callers classify with
// errors.Is. They are non-wrapping (errors.Unwrap returns nil).
var (
	errTokenFileRequired = errors.New("config: API_CONFIG_SERVICE_TOKEN_FILE is required but not configured")
	errTokenLoadFailed   = errors.New("config: API_CONFIG_SERVICE_TOKEN_FILE could not be loaded")
)
