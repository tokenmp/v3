// Package openaiadapter implements the official non-streaming OpenAI Chat
// Completions upstream SDK adapter for the Executor service.
package openaiadapter

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// Compile-time assertion that *Client satisfies the [sdk.Client] port.
var _ sdk.Client = (*Client)(nil)

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
			return errors.New("openaiadapter: http client must not be nil")
		}
		if hc.Transport == nil {
			return errors.New("openaiadapter: http client must have a transport")
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
			return nil, errors.New("openaiadapter: nil option")
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

// parseBaseURL validates a call-local provider destination. Paths are retained,
// including a provider's API prefix; the SDK appends its endpoint below it.
func parseBaseURL(base string) (*url.URL, error) {
	if strings.TrimSpace(base) == "" {
		return nil, errors.New("openaiadapter: base URL is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("openaiadapter: invalid base URL")
	}
	return u, nil
}

func defaultHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 10 * time.Minute
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func noRedirectClient(hc *http.Client) *http.Client {
	return &http.Client{Transport: hc.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Jar: hc.Jar, Timeout: hc.Timeout}
}

// perCallHTTPClient applies the last-boundary header scrubber before the
// underlying transport. This is necessary because openai.NewClient v3.44 reads
// OPENAI_CUSTOM_HEADERS and does not expose an env-free constructor.
func (c *Client) perCallHTTPClient(call sdk.Call, apiKey string) *http.Client {
	var tr http.RoundTripper = sanitizingRoundTripper{next: c.hc.Transport, headers: call.Request.InjectionPlan.Headers, apiKey: apiKey}
	if c.observer != nil {
		tr = ObservingRoundTripper(tr, c.observer, sdk.AttemptMetadata{CandidateIdentity: call.Candidate, Protocol: call.Target.Protocol})
	}
	return &http.Client{Transport: tr, CheckRedirect: c.hc.CheckRedirect, Jar: c.hc.Jar, Timeout: c.hc.Timeout}
}
