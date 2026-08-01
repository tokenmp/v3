package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// Client calls the Config Service admin API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// RequestMeta carries the explicitly-allowlisted request metadata forwarded to
// the Config Service. Only If-Match (the optimistic-concurrency version) is
// permitted from the inbound request; Authorization, Cookie, X-Admin-Token and
// every other client header are NEVER forwarded. The X-Admin-Token is injected
// solely by the client from its configured shared secret and cannot be
// overridden or supplied by the caller.
type RequestMeta struct {
	// IfMatch is the optimistic-concurrency version sent as the If-Match header
	// on PATCH /drafts/{id}. It is validated to be a small positive integer;
	// empty means "no If-Match" (Config falls back to the current version).
	IfMatch string
}

// ProxyResult holds the raw response body, status and the allowlisted
// response headers (ETag, Cache-Control). No other upstream headers are
// surfaced to the caller, so Config-internal or sensitive headers cannot leak
// back to the edge client.
type ProxyResult struct {
	Body    []byte
	Status  int
	Headers http.Header
}

// proxyResponseAllowlist is the exhaustive set of upstream response headers
// the edge forwards back to its caller. ETag carries the draft version for
// optimistic concurrency; Cache-Control is the contract no-store directive.
// Content-Type is intentionally excluded — the handler sets it explicitly.
var proxyResponseAllowlist = map[string]bool{
	"Etag":          true,
	"Cache-Control": true,
}

// ErrConfigUnavailable is a stable sentinel for transport failures. It never
// wraps the underlying error (which may carry the upstream URL/host) so callers
// can log it safely.
var ErrConfigUnavailable = errors.New("config service unavailable")

// requestTimeout bounds every outbound Config Service request.
const requestTimeout = 15 * time.Second

// Option configures a Config Service client.
type Option func(*Client)

// WithHTTPClient overrides the outbound HTTP client, primarily for tests that
// need a custom TLS trust store (e.g. httptest.NewTLSServer). The redirect
// rejection and request timeout policy are re-applied to the provided client
// so the security invariants (no cross-origin X-Admin-Token forwarding, finite
// request bound) are preserved regardless of the caller's transport.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		h.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
		h.Timeout = requestTimeout
		c.http = h
	}
}

// NewClient returns a Config Service client. baseURL should not have trailing
// slash. token is the opaque shared secret forwarded as X-Admin-Token for
// service-to-service admin authorization; it is never logged.
//
// Security: the underlying http.Client rejects all redirects (CheckRedirect
// returns ErrUseLastResponse) so the X-Admin-Token can never be forwarded to a
// different origin, and applies a fixed request timeout. Transport errors are
// mapped to ErrConfigUnavailable without leaking the URL, host, token or body.
func NewClient(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// do performs an HTTP request and returns the raw response body plus the
// allowlisted response headers. On non-2xx it returns a compact error without
// leaking the upstream URL; the body/status/headers are still populated so the
// proxy can pass through 4xx (412/409/404) responses. Transport errors map to
// ErrConfigUnavailable.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, meta RequestMeta) (ProxyResult, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return ProxyResult{Status: http.StatusBadGateway}, ErrConfigUnavailable
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// X-Admin-Token is injected solely from the configured secret; it can never
	// be overridden or supplied by the inbound request (no client header is
	// copied onto the outbound request except the validated If-Match below).
	if c.token != "" {
		req.Header.Set("X-Admin-Token", c.token)
	}
	// Only If-Match is forwarded from the inbound request (validated to a small
	// positive integer). Authorization, Cookie, X-Admin-Token and all other
	// client headers are never copied onto the outbound request.
	if im := sanitizeIfMatch(meta.IfMatch); im != "" {
		req.Header.Set("If-Match", im)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ProxyResult{Status: http.StatusBadGateway}, ErrConfigUnavailable
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	res := ProxyResult{Body: data, Status: resp.StatusCode, Headers: allowlistResponseHeaders(resp.Header)}
	if resp.StatusCode >= 400 {
		return res, fmt.Errorf("config service returned %d", resp.StatusCode)
	}
	return res, nil
}

// allowlistResponseHeaders copies only the allowlisted upstream response
// headers (ETag, Cache-Control) into a fresh header map. Every other upstream
// header (including any Config-internal or sensitive header) is dropped.
func allowlistResponseHeaders(h http.Header) http.Header {
	out := http.Header{}
	for k, vs := range h {
		if proxyResponseAllowlist[http.CanonicalHeaderKey(k)] {
			out[k] = append([]string(nil), vs...)
		}
	}
	return out
}

// sanitizeIfMatch validates an inbound If-Match value. The Config Service
// interprets If-Match as an optimistic-concurrency integer version; we accept
// only a small positive integer (or empty) to prevent header injection (CRLF)
// and to avoid forwarding arbitrary client-controlled content. A quoted ETag
// form ("3") is normalized to its digits so a client echoing the upstream
// ETag is still honored.
func sanitizeIfMatch(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	// Strip one pair of surrounding quotes (ETag quoting) if present.
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	if len(v) == 0 || len(v) > 10 {
		return ""
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return ""
		}
	}
	// Reject all-zero (not a valid version).
	isZero := true
	for _, r := range v {
		if r != '0' {
			isZero = false
			break
		}
	}
	if isZero {
		return ""
	}
	return v
}

// Proxy forwards a request to the Config Service, preserving the path suffix
// after the given prefix. It returns the raw body, status code and the
// allowlisted response headers. Only the validated If-Match from meta is
// forwarded from the inbound request; the X-Admin-Token is injected solely by
// the client from its configured secret.
func (c *Client) Proxy(ctx context.Context, method, path string, body io.Reader, meta RequestMeta) (ProxyResult, error) {
	return c.do(ctx, method, path, body, meta)
}

// GetModelIDs returns the list of active model IDs (for plan allowedModels).
func (c *Client) GetModelIDs(ctx context.Context) ([]string, error) {
	res, err := c.do(ctx, http.MethodGet, "/v1/config/models/catalog", nil, RequestMeta{})
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := httpresp.UnwrapData(res.Body, &ids); err != nil {
		return nil, fmt.Errorf("config client: decode model ids: %w", err)
	}
	return ids, nil
}

// Paginate returns the limit and offset query params for the given page.
func Paginate(page, pageSize int) string {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	limit := pageSize
	offset := (page - 1) * pageSize
	return "limit=" + strconv.Itoa(limit) + "&offset=" + strconv.Itoa(offset)
}

// BuildListPath appends pagination query to a base path.
func BuildListPath(base string, rawQuery string) string {
	q := url.Values{}
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			q.Set(kv[0], kv[1])
		}
	}
	if !q.Has("limit") {
		q.Set("limit", "50")
	}
	if !q.Has("offset") {
		q.Set("offset", "0")
	}
	enc := q.Encode()
	if enc == "" {
		return base
	}
	return base + "?" + enc
}
