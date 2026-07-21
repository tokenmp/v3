// Package identityenv resolves Executor API-key identities from a strictly
// validated, non-secret environment mapping. Keys are read for every request
// so rotations take effect without retaining raw API keys in process state.
package identityenv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/identity"
)

const (
	// IdentityMapEnv is the non-secret JSON mapping environment variable.
	IdentityMapEnv  = "EXECUTOR_IDENTITY_MAP_JSON"
	MaxMappingBytes = 64 << 10
	maxEntries      = 256
	maxIDBytes      = 128
	maxSubjectBytes = 256
	maxKeyIDBytes   = 128
	maxAPIKeyBytes  = 512
)

var (
	envNamePattern       = regexp.MustCompile(`^EXECUTOR_API_KEY_[A-Z0-9_]{1,96}$`)
	ErrMappingMalformed  = errors.New("identity environment mapping is malformed")
	ErrMappingTooLarge   = errors.New("identity environment mapping exceeds maximum size")
	ErrSourceUnavailable = errors.New("identity environment source is unavailable")
	ErrKeyUnavailable    = errors.New("identity API key is unavailable")
)

type entry struct {
	id, envName string
	identity    identity.Identity
}

// Source implements identity.Port. Its entries contain only public identity
// metadata and allowlisted environment names; raw API keys are never retained.
type Source struct {
	lookupEnv func(string) (string, bool)
	entries   []entry // sorted by mapping ID for deterministic complete scans
}

var _ identity.Port = (*Source)(nil)

// NewFromEnv reads the non-secret mapping from EXECUTOR_IDENTITY_MAP_JSON. The
// same lookup function is retained only for the allowlisted per-request keys.
func NewFromEnv(ctx context.Context, lookupEnv func(string) (string, bool)) (*Source, error) {
	if lookupEnv == nil {
		return nil, ErrSourceUnavailable
	}
	rawMap, ok, panicked := lookupMapping(lookupEnv)
	if panicked {
		return nil, ErrSourceUnavailable
	}
	if !ok {
		return nil, ErrMappingMalformed
	}
	return NewFromJSON(ctx, rawMap, lookupEnv)
}

// lookupMapping isolates the initial mapping lookup because callers can supply
// a custom environment function. A panic is treated exactly as an unavailable
// source and is never exposed to callers.
func lookupMapping(lookupEnv func(string) (string, bool)) (value string, present, panicked bool) {
	defer func() {
		if recover() != nil {
			value, present, panicked = "", false, true
		}
	}()
	value, present = lookupEnv(IdentityMapEnv)
	return value, present, false
}

// NewFromJSON creates a source from its non-secret map and validates that all
// mapped key values are presently usable and unique. lookupEnv is used again on
// every authentication request to observe key rotation.
func NewFromJSON(ctx context.Context, rawMap string, lookupEnv func(string) (string, bool)) (*Source, error) {
	if ctx == nil {
		return nil, ErrMappingMalformed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lookupEnv == nil {
		return nil, ErrSourceUnavailable
	}
	entries, err := parseMapping([]byte(rawMap))
	if err != nil {
		return nil, err
	}
	s := &Source{lookupEnv: lookupEnv, entries: entries}
	if err := s.validateCurrent(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Authenticate is a named alias for LookupByKey for HTTP middleware callers.
func (s *Source) Authenticate(ctx context.Context, apiKey string) (identity.Identity, error) {
	return s.LookupByKey(ctx, apiKey)
}

// LookupByKey reads every mapped key on every call and compares SHA-256
// digests in constant time. It never short-circuits on a match, preventing
// entry-position timing from revealing which configured key matched.
func (s *Source) LookupByKey(ctx context.Context, apiKey string) (identity.Identity, error) {
	if ctx == nil {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	if err := ctx.Err(); err != nil {
		return identity.Identity{}, err
	}
	if s == nil || s.lookupEnv == nil || !validAPIKey(apiKey) {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	provided := sha256.Sum256([]byte(apiKey))
	var matched identity.Identity
	matches := 0
	for _, e := range s.entries {
		if err := ctx.Err(); err != nil {
			return identity.Identity{}, err
		}
		value, present := s.lookup(e.envName)
		// Hash and compare every entry, even an absent or malformed current env
		// value. The validity flag gates a match, while the fixed digest comparison
		// avoids turning configuration state into a match-position timing oracle.
		digest := sha256.Sum256([]byte(value))
		comparison := subtle.ConstantTimeCompare(provided[:], digest[:])
		if present && validAPIKey(value) && comparison == 1 {
			matched = e.identity
			matches++
		}
	}
	if err := ctx.Err(); err != nil {
		return identity.Identity{}, err
	}
	if matches != 1 {
		return identity.Identity{}, identity.ErrUnknownKey
	}
	if matched.Status != identity.StatusActive {
		return identity.Identity{}, identity.ErrKeyDisabled
	}
	return matched, nil
}

func (s *Source) validateCurrent(ctx context.Context) error {
	seen := make(map[[sha256.Size]byte]struct{}, len(s.entries))
	for _, e := range s.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, ok := s.lookup(e.envName)
		if !ok || !validAPIKey(value) {
			return ErrKeyUnavailable
		}
		digest := sha256.Sum256([]byte(value))
		if _, duplicate := seen[digest]; duplicate {
			return ErrMappingMalformed
		}
		seen[digest] = struct{}{}
	}
	return nil
}

func (s *Source) lookup(name string) (value string, present bool) {
	defer func() {
		if recover() != nil {
			value, present = "", false
		}
	}()
	return s.lookupEnv(name)
}

func parseMapping(raw []byte) ([]entry, error) {
	if len(raw) > MaxMappingBytes {
		return nil, ErrMappingTooLarge
	}
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, ErrMappingMalformed
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, ErrMappingMalformed
	}
	entries := make([]entry, 0)
	ids := make(map[string]struct{})
	for dec.More() {
		key, ok := mustString(dec)
		if !ok || !validToken(key, maxIDBytes) || forbiddenKey(key) {
			return nil, ErrMappingMalformed
		}
		if _, exists := ids[key]; exists {
			return nil, ErrMappingMalformed
		}
		ids[key] = struct{}{}
		value, err := decodeEntry(dec)
		if err != nil {
			return nil, ErrMappingMalformed
		}
		value.id = key
		entries = append(entries, value)
		if len(entries) > maxEntries {
			return nil, ErrMappingMalformed
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return nil, ErrMappingMalformed
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrMappingMalformed
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	return entries, nil
}

func mustString(dec *json.Decoder) (string, bool) {
	v, err := dec.Token()
	s, ok := v.(string)
	return s, err == nil && ok
}

func decodeEntry(dec *json.Decoder) (entry, error) {
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return entry{}, ErrMappingMalformed
	}
	var e entry
	fields := map[string]bool{}
	for dec.More() {
		name, ok := mustString(dec)
		if !ok || fields[name] || forbiddenKey(name) {
			return entry{}, ErrMappingMalformed
		}
		fields[name] = true
		value, ok := mustString(dec)
		if !ok {
			return entry{}, ErrMappingMalformed
		}
		switch name {
		case "subject":
			e.identity.Subject = value
		case "key_id":
			e.identity.KeyID = value
		case "role":
			e.identity.Role = identity.Role(value)
		case "status":
			e.identity.Status = identity.Status(value)
		case "api_key_env":
			e.envName = value
		default:
			return entry{}, ErrMappingMalformed
		}
	}
	end, err := dec.Token()
	if err != nil || end != json.Delim('}') {
		return entry{}, ErrMappingMalformed
	}
	if len(fields) != 5 || !validToken(e.identity.Subject, maxSubjectBytes) || !validToken(e.identity.KeyID, maxKeyIDBytes) ||
		(e.identity.Role != identity.RoleService && e.identity.Role != identity.RoleAdmin) ||
		(e.identity.Status != identity.StatusActive && e.identity.Status != identity.StatusDisabled) || !envNamePattern.MatchString(e.envName) {
		return entry{}, ErrMappingMalformed
	}
	return e, nil
}

func forbiddenKey(v string) bool { return v == "__proto__" || v == "prototype" || v == "constructor" }

func validToken(v string, max int) bool {
	if len(v) == 0 || len(v) > max || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}
func validAPIKey(v string) bool { return validToken(v, maxAPIKeyBytes) }

func (Source) String() string   { return "identityenv.Source([REDACTED])" }
func (Source) GoString() string { return "identityenv.Source([REDACTED])" }
func (Source) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("identityenv.Source([REDACTED])"))
}
