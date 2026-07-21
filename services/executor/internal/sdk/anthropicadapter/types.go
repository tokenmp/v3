package anthropicadapter

import (
	"context"
	"net/http"
	"net/url"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// observingRoundTripper wraps next and reports exactly one attempt immediately
// before each RoundTrip. The observer receives only [sdk.AttemptMetadata]. A
// panic in the observer is contained so observability can never turn a valid
// provider request into a process crash or a failed attempt.
type observingRoundTripper struct {
	next     http.RoundTripper
	observer sdk.AttemptObserver
	metadata sdk.AttemptMetadata
}

// ObservingRoundTripper wraps next and reports exactly one attempt immediately
// before each RoundTrip. The observer receives only [sdk.AttemptMetadata].
func ObservingRoundTripper(next http.RoundTripper, observer sdk.AttemptObserver, metadata sdk.AttemptMetadata) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	if observer == nil {
		return next
	}
	return observingRoundTripper{next: next, observer: observer, metadata: metadata}
}

func (t observingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := context.Background()
	if request != nil && request.Context() != nil {
		ctx = request.Context()
	}
	// Observability must never turn a valid provider request into a process
	// crash or a failed attempt.
	func() {
		defer func() { _ = recover() }()
		t.observer.OnAttempt(ctx, t.metadata)
	}()
	return t.next.RoundTrip(request)
}

// sanitizingRoundTripper is the final request boundary. It discards every
// SDK-assembled header and query parameter (including anything the SDK could
// have absorbed from the environment, which Complete already suppresses via
// option.WithoutEnvironmentDefaults) and reconstructs only the protocol
// allowlist, the validated plan headers, the validated plan query, and the
// per-call x-api-key credential. anthropic-version is pinned to the fixed
// protocol version 2023-06-01 here rather than copied from the SDK-assembled
// request, so an SDK upgrade or any default can never drift the wire protocol
// away from the version this adapter is validated against.
//
// Anthropic authenticates via the x-api-key header (set by the SDK's
// WithAPIKey option), not a Bearer token. The credential is set exactly once
// from the per-call secret.
//
// The request URL query is rebuilt solely from the validated plan query: any
// query parameter the SDK or environment added (for example beta feature
// flags) is dropped, so the wire query is exactly the compiler-validated plan.
type sanitizingRoundTripper struct {
	next    http.RoundTripper
	headers map[string]string
	query   map[string]string
	apiKey  string
}

func (t sanitizingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return t.next.RoundTrip(request)
	}
	r := request.Clone(request.Context())
	r.Header = make(http.Header)
	// Do not retain any caller-or-env-derived value: rebuild the small protocol
	// allowlist instead.
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Content-Type", "application/json")
	// Pin the protocol version rather than preserving whatever the SDK wrote.
	r.Header.Set("anthropic-version", "2023-06-01")
	for name, value := range t.headers {
		r.Header.Set(name, value)
	}
	// Set, rather than Add, provides one and only one authoritative value.
	r.Header.Set("x-api-key", t.apiKey)
	// Rebuild the request URL query solely from the validated plan query,
	// dropping every query parameter the SDK or environment assembled. A nil
	// or empty plan query yields an empty RawQuery so no smuggled parameter
	// survives. url.Values.Encode is deterministic (sorted by key), so the
	// reconstructed query is stable across map iteration and runs.
	q := make(url.Values, len(t.query))
	for name, value := range t.query {
		q.Set(name, value)
	}
	r.URL.RawQuery = q.Encode()
	return t.next.RoundTrip(r)
}
