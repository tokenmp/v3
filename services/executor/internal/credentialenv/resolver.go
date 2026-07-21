// Package credentialenv resolves configured credential references through an
// explicitly allowlisted environment-variable map. It never exposes a
// reference, environment name, or secret through formatting or errors.
package credentialenv

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

const (
	// MaxMappingBytes bounds the supplied non-secret JSON mapping.
	MaxMappingBytes = 64 << 10
	maxMappings     = 256
	maxRefBytes     = 512
	maxEnvNameBytes = 128
	// MaxSecretBytes bounds a single opaque credential acquired from env.
	MaxSecretBytes = 16 << 10
)

var (
	// ErrMappingMalformed covers all invalid mapping inputs without disclosing
	// their contents.
	ErrMappingMalformed = errors.New("credential environment mapping is malformed")
	// ErrMappingTooLarge means the mapping exceeds its bounded input size.
	ErrMappingTooLarge = errors.New("credential environment mapping exceeds maximum size")
	// ErrResolverUnavailable means construction cannot safely use the lookup
	// function supplied by its caller.
	ErrResolverUnavailable = errors.New("credential environment resolver is unavailable")
	// ErrCredentialNotMapped means a reference is not allowlisted by the map.
	ErrCredentialNotMapped = errors.New("credential is not mapped")
	// ErrCredentialUnavailable means a mapped credential is absent, invalid,
	// or too large at resolution time.
	ErrCredentialUnavailable = errors.New("credential is unavailable")
	// ErrSnapshotInvalid means enabled authenticated routes do not exactly
	// match this resolver's usable allowlist.
	ErrSnapshotInvalid = errors.New("credential environment snapshot validation failed")
)

var _ routing.CredentialResolver = (*Resolver)(nil)

// Resolver is safe to share concurrently. Its private map stores only public
// reference-to-environment-name bindings; secrets are fetched per Resolve to
// support environment rotation and are never retained by Resolver.
type Resolver struct {
	lookupEnv func(string) (string, bool)
	bindings  map[string]string
}

// NewFromJSON validates a bounded strict JSON object of credentialRef →
// EXECUTOR_CREDENTIAL_* environment name bindings. The JSON carries no secret
// material. Multiple references may deliberately share one environment name.
func NewFromJSON(ctx context.Context, rawMap string, lookupEnv func(string) (string, bool)) (*Resolver, error) {
	if ctx == nil {
		return nil, ErrMappingMalformed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if lookupEnv == nil {
		return nil, ErrResolverUnavailable
	}
	bindings, err := parseMapping([]byte(rawMap))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Resolver{lookupEnv: lookupEnv, bindings: bindings}, nil
}

// Resolve implements routing.CredentialResolver. It reads the mapped env value
// for each attempt so rotations are observed. sdk.NewCredentialSecret copies
// the transient bytes; this resolver retains no secret material.
func (r *Resolver) Resolve(ctx context.Context, ref string) (sdk.CredentialSecret, error) {
	if ctx == nil {
		return sdk.CredentialSecret{}, ErrCredentialUnavailable
	}
	if err := ctx.Err(); err != nil {
		return sdk.CredentialSecret{}, err
	}
	if r == nil || r.lookupEnv == nil {
		return sdk.CredentialSecret{}, ErrCredentialUnavailable
	}
	envName, ok := r.bindings[ref]
	if !ok {
		return sdk.CredentialSecret{}, ErrCredentialNotMapped
	}
	value, ok := r.lookup(envName)
	if err := ctx.Err(); err != nil {
		return sdk.CredentialSecret{}, err
	}
	if !ok || !validSecret(value) {
		return sdk.CredentialSecret{}, ErrCredentialUnavailable
	}
	secret := sdk.NewCredentialSecret([]byte(value))
	return secret, nil
}

// String, GoString, and Format intentionally reveal neither map keys nor
// environment names. This prevents accidental disclosure of private topology.
func (Resolver) String() string   { return "credentialenv.Resolver([REDACTED])" }
func (Resolver) GoString() string { return "credentialenv.Resolver([REDACTED])" }
func (Resolver) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("credentialenv.Resolver([REDACTED])"))
}

// lookup contains a caller-provided lookupEnv panic so a faulty environment
// adapter cannot turn an invalid credential into a process-wide crash. It
// returns no panic details and callers classify the missing result safely.
func (r *Resolver) lookup(envName string) (value string, present bool) {
	defer func() {
		if recover() != nil {
			value, present = "", false
		}
	}()
	return r.lookupEnv(envName)
}

func validSecret(value string) bool {
	if len(value) == 0 || len(value) > MaxSecretBytes || !utf8.ValidString(value) {
		return false
	}
	// Provider API keys are opaque but their supported wire encodings are
	// printable ASCII. Reject whitespace, controls, and non-ASCII bytes so
	// copy/paste corruption cannot silently reach an Authorization header.
	for _, r := range value {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}
