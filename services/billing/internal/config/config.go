// Package config loads and validates the billing service runtime configuration.
//
// Configuration is sourced exclusively from environment variables. The service
// fails fast on missing or invalid required values; optional values fall back
// to safe defaults that never include production credentials.
//
// BILLING_DATABASE_URL is strictly validated: only postgres/postgresql URLs are
// accepted, must carry a host, a non-empty user and a path of exactly
// /tokenmp_billing. Any validation error reports only the failing field, never
// the URL value or its credentials. Numeric and duration tunables fail fast on
// non-parseable or negative input — they never silently fall back.
//
// The connection string is never logged and never echoed in errors.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Config is the validated billing service runtime configuration.
type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	LogLevel          string
	LogFormat         string
	ShutdownTimeout   time.Duration
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	SweeperEnabled      bool
	SweeperInterval     time.Duration
	SweeperPendingGrace time.Duration
	SweeperExpiryBatch  int
	SweeperPendingBatch int
	// RetentionDeadline is how long a pending reservation is retried (via the
	// evidence port) before the unknown-policy applies. Must be >
	// PendingGrace. Default 30m.
	RetentionDeadline time.Duration
	// UnknownPolicy is applied to pending rows older than RetentionDeadline.
	// Default "keep_pending" (never blind-release). Only "release_unknown" /
	// "keep_pending" are accepted.
	UnknownPolicy string
	// LoggingURL is the Logging Service endpoint the reconciler queries for
	// confirmed terminal usage evidence. Empty disables evidence resolution
	// (pending reservations are kept per UnknownPolicy). Never logged in
	// errors; no URL/body leakage.
	LoggingURL string
	// LoggingServiceToken is an optional bearer token for the Billing→Logging
	// evidence request. Empty means no Authorization header. It is a secret:
	// never logged, never echoed in errors, never committed. It is resolved
	// from at most one source: BILLING_LOGGING_SERVICE_TOKEN_FILE (production,
	// read-only Docker secret mount) or BILLING_LOGGING_SERVICE_TOKEN (dev/test
	// inline value). Both set at once is a hard error. A token without a
	// configured BILLING_LOGGING_URL is rejected so a stale token cannot be
	// silently carried into a no-evidence deployment.
	LoggingServiceToken string
	// LoggingTimeout bounds each evidence lookup. Default 10s. Must be > 0.
	LoggingTimeout time.Duration
}

const (
	defaultHTTPAddr          = ":8085"
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	defaultShutdownTimeout   = 30 * time.Second
	defaultDBMaxOpenConns    = 10
	defaultDBMaxIdleConns    = 2
	defaultDBConnMaxLifetime = 30 * time.Minute

	// requiredDatabaseName is the only accepted database path. The billing
	// service must never connect to any other database.
	requiredDatabaseName = "tokenmp_billing"
)

// Sentinel validation errors. None of them embed the URL, credentials, file
// path, or file content.
var (
	ErrMissingDatabaseURL = errors.New("BILLING_DATABASE_URL is required")
	ErrInvalidDatabaseURL = errors.New("BILLING_DATABASE_URL is not a valid postgres URL")

	// Logging service token-file sentinels. Each is a non-wrapping value so
	// errors.Unwrap returns nil and no caller can surface the path or content.
	ErrLoggingTokenFileBlank      = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE path is blank")
	ErrLoggingTokenFileNotFound   = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE not found or inaccessible")
	ErrLoggingTokenFileNotRegular = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE is not a regular file")
	ErrLoggingTokenFileTooLarge   = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE exceeds maximum size")
	ErrLoggingTokenFileEmpty      = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE is empty")
	ErrLoggingTokenFileUnreadable = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE could not be read")
	ErrLoggingTokenFileInvalid    = errors.New("BILLING_LOGGING_SERVICE_TOKEN_FILE is not valid UTF-8 or contains forbidden bytes")
	ErrLoggingTokenInvalid        = errors.New("BILLING_LOGGING_SERVICE_TOKEN is not valid UTF-8 or contains forbidden bytes")
	ErrLoggingTokenConflict       = errors.New("BILLING_LOGGING_SERVICE_TOKEN and BILLING_LOGGING_SERVICE_TOKEN_FILE are mutually exclusive")
	ErrLoggingTokenWithoutURL     = errors.New("a Logging service token is configured but BILLING_LOGGING_URL is empty")
)

// maxLoggingTokenBytes bounds a Logging service token file. A bearer token
// longer than this is rejected; 8 KiB is far above any legitimate token and
// keeps the read bounded.
const maxLoggingTokenBytes int64 = 8 << 10 // 8 KiB

// Load reads and validates configuration from the environment.
func Load() (Config, error) {
	rawURL := os.Getenv("BILLING_DATABASE_URL")
	if strings.TrimSpace(rawURL) == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	if err := validateDatabaseURL(rawURL); err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := getduration("BILLING_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	maxOpen, err := getint("BILLING_DB_MAX_OPEN_CONNS", defaultDBMaxOpenConns)
	if err != nil {
		return Config{}, err
	}
	maxIdle, err := getint("BILLING_DB_MAX_IDLE_CONNS", defaultDBMaxIdleConns)
	if err != nil {
		return Config{}, err
	}
	connMaxLifetime, err := getduration("BILLING_DB_CONN_MAX_LIFETIME", defaultDBConnMaxLifetime)
	if err != nil {
		return Config{}, err
	}

	sweeperEnabled, err := getbool("BILLING_SWEEPER_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	sweeperInterval, err := getduration("BILLING_SWEEPER_INTERVAL", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	sweeperGrace, err := getduration("BILLING_SWEEPER_PENDING_GRACE", 2*time.Minute)
	if err != nil {
		return Config{}, err
	}
	expiryBatch, err := getint("BILLING_SWEEPER_EXPIRY_BATCH", 100)
	if err != nil {
		return Config{}, err
	}
	pendingBatch, err := getint("BILLING_SWEEPER_PENDING_BATCH", 100)
	if err != nil {
		return Config{}, err
	}
	retention, err := getduration("BILLING_SWEEPER_RETENTION_DEADLINE", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	unknownPolicy := getenv("BILLING_SWEEPER_UNKNOWN_POLICY", "keep_pending")
	loggingURL := getenv("BILLING_LOGGING_URL", "")
	loggingToken, err := resolveLoggingToken(loggingURL)
	if err != nil {
		return Config{}, err
	}
	loggingTimeout, err := getduration("BILLING_LOGGING_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:            getenv("BILLING_HTTP_ADDR", defaultHTTPAddr),
		DatabaseURL:         rawURL,
		LogLevel:            strings.ToLower(getenv("BILLING_LOG_LEVEL", defaultLogLevel)),
		LogFormat:           strings.ToLower(getenv("BILLING_LOG_FORMAT", defaultLogFormat)),
		ShutdownTimeout:     shutdownTimeout,
		DBMaxOpenConns:      maxOpen,
		DBMaxIdleConns:      maxIdle,
		DBConnMaxLifetime:   connMaxLifetime,
		SweeperEnabled:      sweeperEnabled,
		SweeperInterval:     sweeperInterval,
		SweeperPendingGrace: sweeperGrace,
		SweeperExpiryBatch:  expiryBatch,
		SweeperPendingBatch: pendingBatch,
		RetentionDeadline:   retention,
		UnknownPolicy:       unknownPolicy,
		LoggingURL:          loggingURL,
		LoggingServiceToken: loggingToken,
		LoggingTimeout:      loggingTimeout,
	}

	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}
	if err := validateLogFormat(cfg.LogFormat); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout < 0 {
		return Config{}, fmt.Errorf("BILLING_SHUTDOWN_TIMEOUT must be >= 0, got %s", cfg.ShutdownTimeout)
	}
	if cfg.DBMaxOpenConns < 0 {
		return Config{}, fmt.Errorf("BILLING_DB_MAX_OPEN_CONNS must be >= 0, got %d", cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns < 0 {
		return Config{}, fmt.Errorf("BILLING_DB_MAX_IDLE_CONNS must be >= 0, got %d", cfg.DBMaxIdleConns)
	}
	if cfg.DBConnMaxLifetime < 0 {
		return Config{}, fmt.Errorf("BILLING_DB_CONN_MAX_LIFETIME must be >= 0, got %s", cfg.DBConnMaxLifetime)
	}
	if cfg.SweeperInterval < 0 {
		return Config{}, fmt.Errorf("BILLING_SWEEPER_INTERVAL must be >= 0, got %s", cfg.SweeperInterval)
	}
	if cfg.SweeperPendingGrace < 0 {
		return Config{}, fmt.Errorf("BILLING_SWEEPER_PENDING_GRACE must be >= 0, got %s", cfg.SweeperPendingGrace)
	}
	if cfg.SweeperExpiryBatch <= 0 {
		return Config{}, fmt.Errorf("BILLING_SWEEPER_EXPIRY_BATCH must be > 0, got %d", cfg.SweeperExpiryBatch)
	}
	if cfg.SweeperPendingBatch <= 0 {
		return Config{}, fmt.Errorf("BILLING_SWEEPER_PENDING_BATCH must be > 0, got %d", cfg.SweeperPendingBatch)
	}
	if cfg.RetentionDeadline <= 0 {
		return Config{}, fmt.Errorf("BILLING_SWEEPER_RETENTION_DEADLINE must be > 0, got %s", cfg.RetentionDeadline)
	}
	if cfg.RetentionDeadline <= cfg.SweeperPendingGrace {
		return Config{}, fmt.Errorf("BILLING_SWEEPER_RETENTION_DEADLINE (%s) must be > BILLING_SWEEPER_PENDING_GRACE (%s)", cfg.RetentionDeadline, cfg.SweeperPendingGrace)
	}
	switch cfg.UnknownPolicy {
	case "keep_pending", "release_unknown":
		// valid
	default:
		return Config{}, fmt.Errorf("BILLING_SWEEPER_UNKNOWN_POLICY must be keep_pending or release_unknown, got %q", cfg.UnknownPolicy)
	}
	if cfg.LoggingTimeout <= 0 {
		return Config{}, fmt.Errorf("BILLING_LOGGING_TIMEOUT must be > 0, got %s", cfg.LoggingTimeout)
	}
	return cfg, nil
}

// validateDatabaseURL parses rawURL and enforces the billing service connection
// contract without ever echoing the URL or its credentials in the returned
// error. The error is a stable sentinel so callers can log it safely.
//
// Two forms are accepted:
//   - host form: scheme postgres/postgresql, non-empty host, non-empty user,
//     path exactly /tokenmp_billing.
//   - socket form: scheme postgres/postgresql, empty host but a non-empty
//     host= query param (Unix socket path), path exactly /tokenmp_billing.
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

// getbool parses a boolean env var. Missing/blank falls back; a present but
// non-boolean value is a hard error so misconfiguration cannot be masked.
func getbool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return b, nil
}

func validateLogLevel(level string) error {
	switch level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("BILLING_LOG_LEVEL %q must be one of debug|info|warn|error", level)
	}
}

func validateLogFormat(format string) error {
	switch format {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("BILLING_LOG_FORMAT %q must be json or text", format)
	}
}

// resolveLoggingToken resolves the optional Billing→Logging bearer token from
// at most one source and enforces the URL-gated policy. It accepts either:
//
//   - BILLING_LOGGING_SERVICE_TOKEN_FILE: a read-only Docker secret mount path
//     whose content is read with the same strict, fail-closed secret-file
//     discipline used elsewhere in the project (Lstat rejects symlinks and
//     non-regular files; a post-open SameFile check closes the Lstat→Open
//     TOCTOU; an 8 KiB LimitReader bounds the read; strict UTF-8; NUL and
//     CR/LF rejected; surrounding whitespace trimmed). Production deployments
//     use this source exclusively.
//   - BILLING_LOGGING_SERVICE_TOKEN: an inline token value, intended only for
//     dev/test. The same content validation (UTF-8, no NUL/CR/LF, trim) applies.
//
// Setting both is a hard error (ErrLoggingTokenConflict) so an operator cannot
// accidentally carry two divergent credentials. A non-empty token resolved from
// either source while BILLING_LOGGING_URL is empty is rejected
// (ErrLoggingTokenWithoutURL): a token without an endpoint is a stale
// configuration that must not be silently retained, and an endpoint without a
// token is valid (Logging may run unauthenticated).
//
// No path or content is ever embedded in the returned error.
func resolveLoggingToken(loggingURL string) (string, error) {
	fileVal := strings.TrimSpace(os.Getenv("BILLING_LOGGING_SERVICE_TOKEN_FILE"))
	inlineVal, inlineSet := os.LookupEnv("BILLING_LOGGING_SERVICE_TOKEN")

	hasFile := fileVal != ""
	hasInline := inlineSet && strings.TrimSpace(inlineVal) != ""

	if hasFile && hasInline {
		return "", ErrLoggingTokenConflict
	}

	var token string
	switch {
	case hasFile:
		raw, err := readTokenFile(fileVal)
		if err != nil {
			return "", err
		}
		token = raw
	case hasInline:
		clean, err := validateTokenBytes([]byte(inlineVal))
		if err != nil {
			return "", ErrLoggingTokenInvalid
		}
		token = clean
	default:
		// No token configured. Valid whether or not a URL is set: an
		// unauthenticated Logging endpoint needs no token, and a missing
		// endpoint with no token is the default no-evidence deployment.
		return "", nil
	}

	// A token without a URL is a stale/erroneous configuration. Reject it so
	// the deployment fails fast instead of silently carrying a secret that is
	// never used (and never validated against a live endpoint).
	if strings.TrimSpace(loggingURL) == "" {
		return "", ErrLoggingTokenWithoutURL
	}
	return token, nil
}

// readTokenFile reads and validates a Logging service token file with the
// project's strict secret-file discipline. It never leaks the path or content:
// every failure is a stable non-wrapping sentinel.
//
//   - Lstat rejects symlinks and non-regular files without following links.
//   - After opening, a post-open Stat + os.SameFile check confirms the open
//     descriptor is still a regular file with the same identity as the prior
//     Lstat, closing the Lstat→Open TOCTOU (a path swapped to a symlink or a
//     different file between the two calls fails closed).
//   - The read is bounded by maxLoggingTokenBytes via a LimitReader so a file
//     that lies about its size or grows mid-read cannot exhaust memory.
//   - Content must be strict UTF-8 and free of NUL/CR/LF; surrounding
//     whitespace is trimmed.
func readTokenFile(path string) (string, error) {
	if path == "" {
		return "", ErrLoggingTokenFileBlank
	}

	info, err := os.Lstat(path)
	if err != nil {
		return "", ErrLoggingTokenFileNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", ErrLoggingTokenFileNotRegular
	}
	if !info.Mode().IsRegular() {
		return "", ErrLoggingTokenFileNotRegular
	}
	if info.Size() > maxLoggingTokenBytes {
		return "", ErrLoggingTokenFileTooLarge
	}
	if info.Size() == 0 {
		return "", ErrLoggingTokenFileEmpty
	}

	f, err := os.Open(path)
	if err != nil {
		return "", ErrLoggingTokenFileUnreadable
	}
	defer f.Close()

	// Fail-closed post-open verification: the open descriptor must be a regular
	// file and must refer to the same file identity as the prior Lstat. This
	// rejects a path swapped to a symlink (or otherwise replaced) between Lstat
	// and Open.
	fi, err := f.Stat()
	if err != nil {
		return "", ErrLoggingTokenFileUnreadable
	}
	if !fi.Mode().IsRegular() || !os.SameFile(info, fi) {
		return "", ErrLoggingTokenFileNotRegular
	}

	// LimitReader bounds the read at maxLoggingTokenBytes+1 so an oversize file
	// that grew since the stat is detected rather than truncated.
	raw, err := io.ReadAll(io.LimitReader(f, maxLoggingTokenBytes+1))
	if err != nil {
		return "", ErrLoggingTokenFileUnreadable
	}
	if int64(len(raw)) > maxLoggingTokenBytes {
		return "", ErrLoggingTokenFileTooLarge
	}
	if len(raw) == 0 {
		return "", ErrLoggingTokenFileEmpty
	}

	clean, err := validateTokenBytes(raw)
	if err != nil {
		return "", ErrLoggingTokenFileInvalid
	}
	return clean, nil
}

// validateTokenBytes enforces the content contract for a Logging service
// token regardless of source: strict UTF-8, no NUL/CR/LF bytes within the
// token, and trimmed of surrounding whitespace. It returns the trimmed token.
// Surrounding whitespace (including the trailing newline that Docker secret
// mounts commonly append) is trimmed first; any remaining NUL/CR/LF inside the
// token body is rejected, as is non-UTF-8 content.
func validateTokenBytes(raw []byte) (string, error) {
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("invalid utf-8")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("blank after trim")
	}
	if strings.ContainsRune(token, 0) {
		return "", fmt.Errorf("nul byte")
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("carriage return or newline")
	}
	return token, nil
}
