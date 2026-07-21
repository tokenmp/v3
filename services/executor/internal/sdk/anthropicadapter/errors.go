package anthropicadapter

import (
	"errors"
)

// Sentinel errors for programming-level failures. They carry no remote
// payload: error text is generic and never echoes an upstream message, URL, or
// body. Upstream HTTP/protocol failures are surfaced as [ClassifiedError]
// (see classifyError), not as these sentinels.
var (
	// ErrUnsupportedProtocol is returned when the call's target protocol is not
	// anthropic_messages. Non-stream Complete is the only supported path.
	ErrUnsupportedProtocol = errors.New("anthropicadapter: unsupported protocol")
	// ErrInvalidBaseURL is returned when call-time base URL validation fails.
	ErrInvalidBaseURL = errors.New("anthropicadapter: invalid base url")
	// ErrInvalidRequest is returned when the AppliedRequest body fails strict
	// validation before any network call.
	ErrInvalidRequest = errors.New("anthropicadapter: invalid request")
	// ErrMissingUpstreamModel is returned when the forced target model is empty.
	ErrMissingUpstreamModel = errors.New("anthropicadapter: missing upstream model")
	// ErrMissingAPIKey is returned when the per-call credential secret is empty.
	ErrMissingAPIKey = errors.New("anthropicadapter: missing api key")
	// ErrInvalidInjection is returned when the injection plan contains a denied
	// header/query name, including an x-api-key header.
	ErrInvalidInjection = errors.New("anthropicadapter: invalid injection plan")
)

// maxSafeTokenBytes bounds the length of a sanitized upstream code/type token
// so a misbehaving upstream can never flood observer or error metadata.
const maxSafeTokenBytes = 128

// safeToken sanitizes an upstream-supplied string to a stable, safe identifier
// subset consisting only of [A-Za-z0-9_-]. Anything outside that set truncates
// the result to empty rather than risk surfacing arbitrary remote content
// through error metadata, the completion RequestID, or the attempt observer.
// This is the single chokepoint for "no remote messages".
func safeToken(v string) string {
	if len(v) == 0 || len(v) > maxSafeTokenBytes {
		return ""
	}
	for _, r := range v {
		if !isSafeIdentRune(r) {
			return ""
		}
	}
	return v
}

// isSafeIdentRune reports whether r is permitted in a safe upstream-supplied
// identifier token. The set is deliberately small (only [A-Za-z0-9_-]) and
// excludes whitespace, control chars, and all punctuation other than '-' and
// '_'.
func isSafeIdentRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return r == '-' || r == '_'
}

// hasCTL reports whether v contains an ASCII control character (including
// CR/LF and DEL). Header/query values must be CTL-free.
func hasCTL(v string) bool {
	for _, r := range v {
		if r <= 0x1f || r == 0x7f {
			return true
		}
	}
	return false
}
