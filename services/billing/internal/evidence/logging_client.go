package evidence

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tokenmp/v3/packages/go/httpresp"
)

// LoggingClient resolves terminal usage evidence by querying the Logging
// Service HTTP API (GET /v1/logs/{request_id}). It does NOT read the Logging
// DB. It is strict: a bounded timeout, redirects disabled, no URL/host/body
// in any error, and an optional service token sent as a Bearer header.
//
// baseURL may be empty; in that case NewClient returns a NilLookup so a
// billing deployment degrades to the default-safe "keep pending and alert"
// policy rather than blind-releasing pending reservations.
type LoggingClient struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// NewClient builds a Logging-backed Lookup. An empty baseURL returns a
// NilLookup (the service can still run without evidence resolution, keeping
// pending rows rather than releasing them). The timeout bounds every call
// (default 10s when <=0). The optional token, when non-empty, is sent as
// "Authorization: Bearer <token>"; it is never logged or echoed in errors.
func NewClient(baseURL, token string, timeout time.Duration) Lookup {
	if strings.TrimSpace(baseURL) == "" {
		return NilLookup{}
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &LoggingClient{
		httpClient: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
	}
}

// terminalLog is the minimal projection of the "log" field inside the
// {code,data,message} envelope returned by Logging's GET /v1/logs/{request_id}.
// Field names align with the Logging Service request_logs json tags.
type terminalLog struct {
	RequestID    string `json:"request_id"`
	FinalStatus  string `json:"final_status"`
	UsageStatus  string `json:"usage_status,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	TotalTokens  int    `json:"total_tokens,omitempty"`
}

// logEnvelope is the data payload of the unified envelope: {log, attempts,
// events}. Only the log projection is decoded.
type logEnvelope struct {
	Log terminalLog `json:"log"`
}

// TerminalUsage implements Lookup.
func (c *LoggingClient) TerminalUsage(ctx context.Context, requestID string) (Evidence, error) {
	if requestID == "" {
		return Evidence{}, ErrNotFound
	}
	target := c.baseURL + "/v1/logs/" + url.PathEscape(requestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Evidence{}, ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Context cancellation/timeout also lands here; treat as unavailable
		// (retryable) so the reconciler keeps the reservation pending.
		return Evidence{}, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	// Bound the response: a misbehaving Logging must not exhaust memory.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Evidence{}, ErrNotFound
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return Evidence{}, ErrUnavailable
	}
	// Logging wraps all 2xx JSON responses in the unified {code,data,message}
	// envelope via httpresp.OK, so the actual payload lives at data.{log,...}.
	// Reuse httpresp.UnwrapData to unwrap the envelope rather than hand-rolling
	// a parser that could drift from the canonical envelope shape. A malformed
	// envelope (or a non-zero code leaking through a 2xx) is treated as
	// unavailable — Billing never guesses, it keeps the reservation pending.
	var env logEnvelope
	if err := httpresp.UnwrapData(body, &env); err != nil {
		return Evidence{}, ErrUnavailable
	}
	// Only usage_status="final" is confirmed terminal evidence. Any other
	// value (processing/pending/estimated/missing/empty) is retriable.
	if env.Log.UsageStatus != "final" {
		return Evidence{}, ErrNotTerminal
	}
	total := int64(env.Log.TotalTokens)
	if total <= 0 {
		total = int64(env.Log.InputTokens) + int64(env.Log.OutputTokens)
	}
	if total < 0 {
		total = 0
	}
	// Requests are not stored per-log; the reservation's own reserved count is
	// the unit. Evidence only certifies the token count here; the reconciler
	// passes the reservation's request count alongside.
	return Evidence{Known: true, Requests: 0, Tokens: total}, nil
}

// Compile-time assertion.
var _ Lookup = (*LoggingClient)(nil)
