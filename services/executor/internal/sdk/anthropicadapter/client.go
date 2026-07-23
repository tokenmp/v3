// Package anthropicadapter implements the official non-streaming Anthropic
// Messages upstream SDK adapter for the Executor service.
package anthropicadapter

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// Client holds only the process-local HTTP policy and optional safe observer.
// A target URL, SDK client, and credential are all call-local.
//
// It implements [sdk.Client] by exposing only Complete.
type Client struct {
	hc       *http.Client
	observer sdk.AttemptObserver
}

// Compile-time assertions that *Client supplies both independently registered
// completion and stream capabilities used by runtime composition.
var _ sdk.Client = (*Client)(nil)
var _ sdk.StreamClient = (*Client)(nil)

// Option configures a Client.
type Option func(*Config) error

// Config carries Client configuration.
type Config struct {
	httpClient *http.Client
	observer   sdk.AttemptObserver
}

// WithHTTPClient sets the HTTP client used for upstream calls. Its transport is
// shared, but redirects are disabled on a derived client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Config) error {
		if hc == nil {
			return errors.New("anthropicadapter: http client must not be nil")
		}
		if hc.Transport == nil {
			return errors.New("anthropicadapter: http client must have a transport")
		}
		c.httpClient = hc
		return nil
	}
}

// WithAttemptObserver installs an observer. Panics from it are contained.
func WithAttemptObserver(o sdk.AttemptObserver) Option {
	return func(c *Config) error { c.observer = o; return nil }
}

// NewClient constructs a target-agnostic client. Base URLs are deliberately
// supplied and validated on every Complete call.
func NewClient(opts ...Option) (*Client, error) {
	var cfg Config
	for _, opt := range opts {
		if opt == nil {
			return nil, errors.New("anthropicadapter: nil option")
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	hc := defaultHTTPClient()
	if cfg.httpClient != nil {
		hc = noRedirectClient(cfg.httpClient)
	}
	return &Client{hc: hc, observer: cfg.observer}, nil
}

// parseBaseURL validates a call-local provider destination. It accepts an
// HTTPS origin or a canonical, unescaped path prefix. The SDK resolves its
// relative endpoint (v1/messages) below this URL, so prefixes are normalized
// to end in one slash. Ambiguous path spellings are rejected before the SDK
// can resolve them differently from policy validation.
func parseBaseURL(base string) (*url.URL, error) {
	if strings.TrimSpace(base) == "" {
		return nil, errors.New("anthropicadapter: base URL is required")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !safeBasePath(u) {
		return nil, errors.New("anthropicadapter: invalid base URL")
	}
	if u.Path == "" {
		u.Path = "/"
	} else if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u, nil
}

func safeBasePath(u *url.URL) bool {
	if u.RawPath != "" || strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "//") {
		return false
	}
	for _, r := range u.Path {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func defaultHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 10 * time.Minute
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func noRedirectClient(hc *http.Client) *http.Client {
	return &http.Client{Transport: hc.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Jar: hc.Jar, Timeout: hc.Timeout}
}

// perCallHTTPClient applies the last-boundary header and query scrubber before
// the underlying transport. Complete passes option.WithoutEnvironmentDefaults to
// anthropic.NewClient so the SDK reads no environment at all; the scrubber
// remains as defense-in-depth, rebuilding only the protocol allowlist, the
// fixed anthropic-version, validated plan headers, validated plan query, and
// the per-call credential.
func (c *Client) perCallHTTPClient(call sdk.Call, apiKey, accept string) *http.Client {
	var tr http.RoundTripper = sanitizingRoundTripper{
		next:    c.hc.Transport,
		headers: call.Request.InjectionPlan.Headers,
		query:   call.Request.InjectionPlan.Query,
		apiKey:  apiKey,
		accept:  accept,
	}
	if c.observer != nil {
		tr = ObservingRoundTripper(tr, c.observer, sdk.AttemptMetadata{CandidateIdentity: call.Candidate, Protocol: call.Target.Protocol})
	}
	return &http.Client{Transport: tr, CheckRedirect: c.hc.CheckRedirect, Jar: c.hc.Jar, Timeout: c.hc.Timeout}
}
