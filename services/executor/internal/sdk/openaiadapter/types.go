package openaiadapter

import (
	"context"
	"net/http"

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

// sanitizingRoundTripper is the final request boundary. openai-go v3.44 reads
// OPENAI_CUSTOM_HEADERS during client construction and provides no env-free
// constructor. Rather than mutating process environment, it discards every
// SDK-assembled header and reconstructs only protocol-required SDK headers,
// validated plan headers, and the per-call bearer credential.
type sanitizingRoundTripper struct {
	next    http.RoundTripper
	headers map[string]string
	apiKey  string
}

func (t sanitizingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return t.next.RoundTrip(request)
	}
	r := request.Clone(request.Context())
	r.Header = make(http.Header)
	// Do not retain any SDK-assembled value: OPENAI_CUSTOM_HEADERS can use any
	// header name, including names that look like SDK telemetry headers. Rebuild
	// the small protocol allowlist instead.
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Content-Type", "application/json")
	for name, value := range t.headers {
		r.Header.Set(name, value)
	}
	// Set, rather than Add, provides one and only one authoritative value.
	r.Header.Set("Authorization", "Bearer "+t.apiKey)
	return t.next.RoundTrip(r)
}
