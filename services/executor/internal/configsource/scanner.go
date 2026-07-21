// Package configsource provides the strict, fail-closed file source that loads
// a raw Executor configuration snapshot from disk and bootstraps the initial
// immutable compiled snapshot.
//
// The secret scanner in this file is the production implementation. It is
// intentionally independent of any test-only helper so that production code
// never depends on _test.go symbols.
package configsource

import (
	"bytes"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// SecretFindingKind classifies one detected piece of forbidden secret-bearing
// material in a raw configuration file. It deliberately carries no matched
// content: a finding never lets a caller recover raw JSON, a key name, or a
// secret value. Only the kind is exposed so logs and errors stay safe.
type SecretFindingKind string

const (
	// SecretForbiddenKey marks a JSON object key that must never appear in a
	// sanitized configuration (for example "secret", "password", "token").
	SecretForbiddenKey SecretFindingKind = "forbidden_secret_key"
	// SecretValueMarker marks a literal secret-bearing value prefix such as
	// "sk-" or "Bearer ". The marker category is returned, never the value.
	SecretValueMarker SecretFindingKind = "secret_value_marker"
	// SecretJWT marks a JWT-shaped value (three dot-separated base64url
	// segments starting with "eyJ").
	SecretJWT SecretFindingKind = "jwt_shaped_secret"
	// SecretURLCredential marks a URL whose query string carries a credential
	// parameter (api_key, token, secret, password, ...).
	SecretURLCredential SecretFindingKind = "url_query_credential"
)

// SecretFinding is one safe, content-free classification of forbidden material.
type SecretFinding struct {
	Kind SecretFindingKind
}

// credentialKeyAlternation is the single source of truth for credential-
// bearing key names. It lists every name that must never appear as a JSON
// object key or as a URL query parameter in a sanitized configuration,
// case-insensitively, with common separator variants (underscore, hyphen, or
// none). Both the forbidden-key matchers and the URL-query-credential matcher
// are built from it so the two surfaces can never drift out of sync.
//
// The set intentionally includes access_key, private_key, secret_key,
// client_secret, auth_token, authtoken, authorization/authorisation and
// bearer_token in addition to the original secret/password/credential/api_key/
// token names: they all name the channel that carries secret material, not a
// public identity.
const credentialKeyAlternation = `secret|password|credential|api[_-]?key|apikey|access[_-]?key|private[_-]?key|secret[_-]?key|client[_-]?secret|auth[_-]?token|authtoken|authorization|authorisation|bearer[_-]?token|token`

// forbiddenSecretKey matches a JSON object key that must never appear in a
// sanitized configuration. It anchors on the opening quote and the trailing
// ":" so it never matches substrings inside longer field names such as
// "MaxBudgetToken" or enum values such as "api_key_header", and never matches
// the same word when it appears as a value (for example the fixture adapter
// field "Header": "Authorization" is safe because the value is not a key). It
// is case insensitive so "Secret", "SECRET" and "secret" are all rejected,
// while "CredentialRef" and "Credentials" are not (the closing quote does not
// immediately follow "credential"). "authorization" and the normalized
// "auth_token"/"authtoken" variants are included because they name the header
// that carries the secret, not a public identity.
//
// This is the lexical (raw-byte) counterpart: it cannot see JSON escape
// sequences such as "se\u0063ret". The semantic scan below closes that gap by
// decoding string tokens before applying forbiddenSecretKeyName.
var forbiddenSecretKey = regexp.MustCompile(`(?i)"(` + credentialKeyAlternation + `)"\s*:`)

// forbiddenSecretKeyName is the semantic counterpart of forbiddenSecretKey. It
// anchors the whole decoded key (^...$) so substrings such as "CredentialRef",
// "Credentials", "MaxBudgetToken" or "AccessTokenMode" never match, exactly
// mirroring the quote-delimited behavior of the lexical regex. It is applied
// to decoded JSON string keys (and decoded URL query parameter names) so that
// escape-based or percent-encoding obfuscation cannot bypass the scanner.
var forbiddenSecretKeyName = regexp.MustCompile(`(?i)^(` + credentialKeyAlternation + `)$`)

// secretJWT matches a compact JWT: header.payload.signature where the first two
// segments begin with the base64url "eyJ" prefix.
var secretJWT = regexp.MustCompile(`(?i)(?:eyJ[a-zA-Z0-9_-]+)\.(?:eyJ[a-zA-Z0-9_-]+)\.[a-zA-Z0-9_-]+`)

// secretURLQuery matches an HTTP(S) URL whose query string contains a
// credential-bearing parameter. It is deliberately scoped to the query
// component so safe URLs without such parameters are accepted. It is built
// from the same credentialKeyAlternation as the forbidden-key matchers so the
// URL surface covers the identical key set (access_key, private_key,
// secret_key, client_secret, auth_token, authtoken, authorization,
// bearer_token, ...). The lexical regex cannot see JSON escapes or URL
// percent-encoding; the semantic pass (urlHasCredentialQuery) closes those gaps.
var secretURLQuery = regexp.MustCompile(`(?i)https?://[^\s"']*[?&](?:` + credentialKeyAlternation + `)=[^&#\s"']+`)

// secretValueMarkers are literal secret-bearing value prefixes that must never
// appear in a sanitized raw configuration. None collide with allowed enum
// values or field names present in the fixtures (for example the OpenAI
// adapter uses "Bearer" without a trailing space as an auth Prefix, which does
// not match the "Bearer " marker).
var secretValueMarkers = []string{
	"sk-",
	"Bearer ",
	"BEGIN PRIVATE KEY",
	"AKIA",
	"xox-",
	"ghp_",
	"gho_",
	"glpat-",
}

// ScanSecrets inspects raw configuration bytes for forbidden secret-bearing
// material and returns one safe, content-free finding per detection. The
// returned findings never contain the matched bytes, key names, or values, so
// they may safely appear in errors and logs.
//
// The scan is defense-in-depth and combines two independent passes:
//
//  1. A raw lexical pass over the file bytes. It catches secrets regardless of
//     JSON well-formedness, including material in non-JSON positions.
//
//  2. A semantic JSON token pass that decodes string keys and values before
//     applying the same classifiers. It closes the JSON-escape bypass where a
//     forbidden key or value is obfuscated with escapes such as
//     "se\u0063ret" or "sk\u002d..." which the lexical pass cannot see.
//
// Findings are de-duplicated by kind: at most one finding per kind is
// returned, matching the existing content-free contract. The semantic pass is
// best-effort: malformed JSON terminates it early without panicking, and the
// lexical pass still runs over the raw bytes, so malformed input can never
// silence the scanner.
func ScanSecrets(raw []byte) []SecretFinding {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[SecretFindingKind]struct{})
	var findings []SecretFinding
	add := func(kind SecretFindingKind) {
		if _, ok := seen[kind]; ok {
			return
		}
		seen[kind] = struct{}{}
		findings = append(findings, SecretFinding{Kind: kind})
	}

	// 1. Raw lexical pass.
	if forbiddenSecretKey.Match(raw) {
		add(SecretForbiddenKey)
	}
	for _, marker := range secretValueMarkers {
		if bytes.Contains(raw, []byte(marker)) {
			add(SecretValueMarker)
			// One value-marker finding is sufficient to fail-closed; reporting
			// every marker would not add safety and could hint at content.
			break
		}
	}
	if secretJWT.Match(raw) {
		add(SecretJWT)
	}
	if secretURLQuery.Match(raw) {
		add(SecretURLCredential)
	}

	// 2. Semantic decoded pass (closes the JSON-escape bypass).
	scanSecretsSemantic(raw, add)

	return findings
}

// scanSecretsSemantic walks the JSON token stream in raw, decoding string
// tokens so escape-obfuscated keys and values are classified. It is purely
// additive via the provided add callback and is best-effort: any decode error
// (including malformed, truncated, or hostile input) terminates the walk
// without panicking.
//
// It is a small explicit state machine over a container frame stack. Each
// frame records whether it is an object or an array, and for objects a single
// boolean expectKey that toggles key<->value. The crucial correctness
// property is that *every* value position — a scalar string, a scalar
// number/bool/null, or a whole nested container — flips the enclosing object
// back to expecting a key. The closing delimiter of a nested container is
// consumed here in the same loop that opened it; when that container occupied
// the value slot of a parent object, the parent's expectKey is reset to true
// on pop. Without that reset, a sibling key following a nested object/array
// value would be misclassified as a value, so an escaped forbidden key such as
// {"a":{"b":1},"se\u0063ret":"x"} would bypass the scanner. A safe value
// (e.g. "Authorization") is never misclassified as a key because only the
// key position applies forbiddenSecretKeyName; values use scanDecodedStringValue.
//
// The stack is a plain slice (heap) so a deeply nested document cannot overflow
// the goroutine stack; accepted configs are bounded by the caller's structural
// validator, and arbitrary fuzz input is bounded only by available memory.
func scanSecretsSemantic(raw []byte, add func(SecretFindingKind)) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Each frame describes the enclosing container. For objects, expectKey
	// toggles: true when the next string token is a key, false when it is a
	// value. Arrays never treat their elements as keys.
	type frame struct {
		isObject  bool
		expectKey bool
	}
	var stack []frame
	for {
		tok, err := dec.Token()
		if err != nil {
			return
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				stack = append(stack, frame{isObject: true, expectKey: true})
			case '[':
				stack = append(stack, frame{isObject: false})
			case '}', ']':
				// Pop the closing container. When the popped container occupied the
				// value slot of a parent object, that parent must next expect a key
				// again. JSON keys are always strings, so any '{'/'[' that appeared
				// while the parent expected a value was a value container; its close
				// therefore consumes the value slot. Resetting expectKey here is what
				// catches a sibling forbidden key after a nested object/array value,
				// including escaped keys the lexical pass cannot see.
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				if n := len(stack); n > 0 {
					parent := &stack[n-1]
					if parent.isObject && !parent.expectKey {
						parent.expectKey = true
					}
				}
			}
		case string:
			isKey := false
			if n := len(stack); n > 0 {
				f := &stack[n-1]
				if f.isObject && f.expectKey {
					isKey = true
					f.expectKey = false
				} else if f.isObject {
					// Value position in an object: the next string is a key again.
					f.expectKey = true
				}
			}
			if isKey {
				if forbiddenSecretKeyName.MatchString(v) {
					add(SecretForbiddenKey)
				}
			} else {
				scanDecodedStringValue(v, add)
			}
		default:
			// Any non-string scalar value (bool, number, json.Number, nil) in
			// an object: the next string is a key again.
			if n := len(stack); n > 0 && stack[n-1].isObject {
				stack[n-1].expectKey = true
			}
		}
	}
}

// scanDecodedStringValue classifies a decoded JSON string value (never a key)
// against the value-marker, JWT, and URL-query-credential classifiers. The
// same classifiers as the lexical pass are applied to the decoded text so that
// escape-obfuscated values (e.g. "sk\u002d...") are caught. URL query
// credentials are additionally resolved via urlHasCredentialQuery so that
// percent-encoded parameter names the lexical regex cannot see are covered.
func scanDecodedStringValue(s string, add func(SecretFindingKind)) {
	for _, marker := range secretValueMarkers {
		if strings.Contains(s, marker) {
			add(SecretValueMarker)
			break
		}
	}
	if secretJWT.MatchString(s) {
		add(SecretJWT)
	}
	if secretURLQuery.MatchString(s) {
		add(SecretURLCredential)
	}
	if urlHasCredentialQuery(s) {
		add(SecretURLCredential)
	}
}

// urlHasCredentialQuery reports whether s is an HTTP(S) URL whose query string
// carries a parameter whose decoded name matches the unified credential key
// set. It decodes percent-encoded parameter names (for example api%5Fkey,
// where %5F is '_') so that URL-encoding obfuscation cannot bypass the scanner
// the way it bypasses the lexical regex. Only parameter NAMES are matched; a
// non-empty value is required so a bare ?token (no secret) is not flagged.
//
// It never reports a CredentialRef as a positive: CredentialRefs use a
// non-http(s) scheme (e.g. vault://) or carry no query string, both of which
// return false here, mirroring safeCredentialRef in the compiler which rejects
// any RawQuery on a credential reference.
func urlHasCredentialQuery(s string) bool {
	if !strings.Contains(s, "://") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.RawQuery == "" {
		return false
	}
	vals, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	for name, values := range vals {
		if !forbiddenSecretKeyName.MatchString(name) {
			continue
		}
		for _, v := range values {
			if v != "" {
				return true
			}
		}
	}
	return false
}

// HasSecret reports whether raw configuration bytes contain any forbidden
// secret-bearing material. It is a convenience wrapper around ScanSecrets for
// callers that only need a boolean verdict.
func HasSecret(raw []byte) bool {
	return len(ScanSecrets(raw)) != 0
}
