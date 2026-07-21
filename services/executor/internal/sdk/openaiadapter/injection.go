package openaiadapter

import (
	"errors"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// credentialNeedles is the credential-bearing name fragment denylist shared by
// the header and query injection gates. A plan name that contains any of these
// (after alphanumeric normalization) is treated as a credential-like name and
// rejected before any HTTP call, so an injection plan can never smuggle a
// credential through an alternate spelling or punctuation.
var credentialNeedles = []string{"apikey", "accesskey", "privatekey", "secret", "token", "credential"}

// alphanumericLower normalizes v by lowercasing and dropping every non
// alphanumeric rune. This makes the credential denylist robust against case
// and alternate punctuation (for example X-ApiKey, x_api_key, api-key all
// reduce to "apikey"). It does not change matching semantics for already-canonical
// names because no separator survives.
func alphanumericLower(v string) string {
	var b strings.Builder
	b.Grow(len(v))
	for _, r := range v {
		if r >= 'A' && r <= 'Z' {
			r = r + ('a' - 'A')
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchesCredentialNeedle reports whether the alphanumeric-normalized form of
// name contains any credential-bearing needle.
func matchesCredentialNeedle(name string) bool {
	n := alphanumericLower(name)
	for _, needle := range credentialNeedles {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

// deniedHeader reports whether name is a protected, credential-bearing, or
// SDK-control header that an injection plan must never set. This is the
// defense-in-depth gate for "prohibit plan Authorization": the SDK's native
// Authorization (Bearer) is the only credential channel.
//
// The header allowlist itself is enforced at compile time by the adapter
// compiler (RequestPolicy.AllowedHeaders); this package does not re-impose an
// allowlist, only the denylist, so a per-adapter allowlist is never silently
// narrowed.
func deniedHeader(name string) bool {
	n := canonicalHeader(name)
	if n == "" {
		return true
	}
	switch {
	case n == "host", n == "content-length", n == "authorization":
		return true
	case strings.HasPrefix(n, "proxy-"), strings.HasPrefix(n, "forwarded"), n == "forwarded":
		return true
	case strings.HasPrefix(n, "x-forwarded-"):
		return true
	case strings.HasPrefix(n, "x-sdk-"):
		return true
	}
	return matchesCredentialNeedle(n)
}

// canonicalHeader lowercases a header name for comparison.
func canonicalHeader(v string) string { return strings.ToLower(v) }

// rfcToken reports whether v is a valid RFC 7230 header field-name token.
func rfcToken(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

// safeSegment reports whether v is a simple, path-segment-safe identifier used
// for query parameter names. It forbids path traversal and disallows chars that
// could escape a query key.
func safeSegment(v string) bool {
	if v == "" || v == "." || v == ".." {
		return false
	}
	for _, r := range v {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// deniedQuery reports whether name is an authentication or credential-like
// query parameter name that an injection plan must never set. A query parameter
// is never the credential channel for this SDK (the per-call credential is the
// sole Authorization Bearer), and a credential-like query name is a strong
// signal of a smuggling attempt or a misconfiguration. This is the
// defense-in-depth gate for "prohibit plan credential-via-query", comparable
// to the header denylist: it normalizes case and alternate punctuation so a
// denylist cannot be bypassed with spellings like Api-Key, api_key, or
// x-api-key.
//
// The query allowlist itself is enforced at compile time by the adapter
// compiler (RequestPolicy.AllowedQuery); this package does not re-impose an
// allowlist, only the denylist, so a per-adapter allowlist is never silently
// narrowed.
func deniedQuery(name string) bool {
	if name == "" {
		return true
	}
	// Authorization is credential-bearing even though it contains no needle
	// fragment; match it directly against the canonical form. (Header denylist
	// handles this via its explicit case; query has no such host/content-length
	// family, so the only literal needed is authorization.)
	if alphanumericLower(name) == "authorization" {
		return true
	}
	return matchesCredentialNeedle(name)
}

// validateInjection checks that every header/query in plan passes the denylist
// and basic shape rules. It does NOT impose an allowlist: the adapter compiler
// owns the per-adapter allowlist. This gate ensures a manually-constructed
// CompiledAdapter cannot inject a credential or SDK control header even if it
// bypassed compilation.
func validateInjection(plan adapter.InjectionPlan) error {
	for name := range plan.Headers {
		if !rfcToken(name) || deniedHeader(name) {
			return errors.New("openaiadapter: injected header is denied")
		}
	}
	for name := range plan.Query {
		if !safeSegment(name) || deniedQuery(name) {
			return errors.New("openaiadapter: injected query name is denied")
		}
	}
	for _, value := range plan.Headers {
		if hasCTL(value) {
			return errors.New("openaiadapter: injected header value has control character")
		}
	}
	for _, value := range plan.Query {
		if hasCTL(value) {
			return errors.New("openaiadapter: injected query value has control character")
		}
	}
	return nil
}

// injectionOptions converts a validated adapter.InjectionPlan into SDK request
// options. It must be called after [validateInjection] succeeds. Header values
// are set verbatim; query values are URL-encoded by the SDK.
func injectionOptions(plan adapter.InjectionPlan) []option.RequestOption {
	opts := make([]option.RequestOption, 0, len(plan.Headers)+len(plan.Query))
	for name, value := range plan.Headers {
		opts = append(opts, option.WithHeader(name, value))
	}
	for name, value := range plan.Query {
		opts = append(opts, option.WithQuery(name, value))
	}
	return opts
}
