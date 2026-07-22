// Package modelcatalog is the transport-neutral boundary for the Executor
// model catalog. It defines the request/result shapes, the narrow
// CatalogProvider port, and the capability mapping from adapter-internal
// Capability values to contract-facing string tags.
//
// The package imports no HTTP, chi, generated contract, or transport code: it
// depends only on the adapter domain package for Capability. A transport-facing
// facade composes against this port and is the only caller permitted to
// construct a CatalogRequest carrying a trusted Principal.
package modelcatalog

import (
	"context"
	"errors"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// Principal is the secret-free, transport-neutral authenticated caller value
// carried on a CatalogRequest. It mirrors nonstream.Principal so this package
// remains decoupled from the nonstream boundary.
type Principal struct {
	Subject string
	KeyID   string
	Role    string
	Status  string
}

// Canonical role values accepted by a facade's defensive revalidation.
const (
	RoleService = "service"
	RoleAdmin   = "admin"
)

// Canonical status values accepted by a facade's defensive revalidation.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// CatalogRequest is the protocol-normalized input to a model catalog listing.
// Principal is the only authenticated caller value the catalog path may read.
type CatalogRequest struct {
	Principal Principal
}

// ThinkingConfig describes a model's thinking/reasoning capabilities for the
// catalog response. It is the transport-neutral shape that a renderer maps to
// the generated ModelThinkingConfig contract type.
type ThinkingConfig struct {
	Supported       bool
	DefaultEffort   string
	MaxEffort       string
	EffortLevels    []string
	MinBudgetTokens *int
	MaxBudgetTokens *int
}

// CatalogEntry is one model in the catalog result. It carries only safe,
// public-facing fields: no FallbackModelIDs, DisplayName, route, adapter, or
// credential detail.
type CatalogEntry struct {
	ID           string
	Capabilities []string
	Thinking     *ThinkingConfig
	Created      int
}

// CatalogResult is the transport-neutral catalog listing result.
type CatalogResult struct {
	Models []CatalogEntry
}

// CatalogProvider is the narrow catalog boundary. A facade implements it by
// composing snapshot, quarantine, and clock dependencies.
type CatalogProvider interface {
	ListModels(ctx context.Context, req CatalogRequest) (CatalogResult, error)
}

// Safe sentinel errors. None carries a selector, snapshot, routing, request,
// response, credential, or upstream detail. A transport renderer reduces them
// to protocol-native responses; callers must not unwrap or string-match them.
var (
	// ErrUnauthenticated means the request carried no trusted authenticated
	// Principal or the Principal failed defensive revalidation.
	ErrUnauthenticated = errors.New("modelcatalog: unauthorized")

	// ErrNoSnapshot means no compiled snapshot is currently published.
	ErrNoSnapshot = errors.New("modelcatalog: no compiled snapshot")

	// ErrMisconfigured means a required facade dependency is nil or typed-nil.
	ErrMisconfigured = errors.New("modelcatalog: misconfigured")

	// ErrQuarantineUnavailable means quarantine state could not be read. It
	// fails closed rather than risking a known-bad model appearing in the
	// catalog.
	ErrQuarantineUnavailable = errors.New("modelcatalog: quarantine unavailable")
)

// capabilityMap maps adapter-internal Capability values to contract-facing
// string tags. Capabilities not in this map are omitted from the catalog.
var capabilityMap = map[adapter.Capability]string{
	adapter.CapabilityChat:     "text",
	adapter.CapabilityTools:    "function_calling",
	adapter.CapabilityVision:   "vision",
	adapter.CapabilityThinking: "thinking",
	adapter.CapabilityImages:   "image",
}

// MapCapabilities maps adapter-internal capabilities to contract-facing string
// tags. Capabilities not in the public map (streaming, messages, responses)
// are omitted. The output is sorted deterministically.
func MapCapabilities(caps []adapter.Capability) []string {
	seen := make(map[string]bool, len(caps))
	var out []string
	for _, c := range caps {
		if mapped, ok := capabilityMap[c]; ok && !seen[mapped] {
			seen[mapped] = true
			out = append(out, mapped)
		}
	}
	sortStrings(out)
	return out
}

// MapThinking maps adapter-internal ThinkingInput to the transport-neutral
// ThinkingConfig. Returns nil when thinking is not supported.
func MapThinking(t adapter.ThinkingInput) *ThinkingConfig {
	if !t.Supported {
		return nil
	}
	levels := effortLevels(t.DefaultEffort, t.MaxEffort)
	cfg := &ThinkingConfig{
		Supported:     true,
		DefaultEffort: string(t.DefaultEffort),
		MaxEffort:     string(t.MaxEffort),
		EffortLevels:  levels,
	}
	if t.MinBudgetToken > 0 {
		v := t.MinBudgetToken
		cfg.MinBudgetTokens = &v
	}
	if t.MaxBudgetToken > 0 {
		v := t.MaxBudgetToken
		cfg.MaxBudgetTokens = &v
	}
	return cfg
}

// effortLevels returns the ordered effort levels from default to max inclusive.
func effortLevels(defaultEffort, maxEffort adapter.ThinkingEffort) []string {
	const allLevels = "none,minimal,low,medium,high,xhigh,max"
	// Ordered from lowest to highest.
	ordered := []adapter.ThinkingEffort{
		adapter.ThinkingNone, adapter.ThinkingMinimal, adapter.ThinkingLow,
		adapter.ThinkingMedium, adapter.ThinkingHigh, adapter.ThinkingXHigh, adapter.ThinkingMax,
	}
	start, end := -1, -1
	for i, e := range ordered {
		if e == defaultEffort && start < 0 {
			start = i
		}
		if e == maxEffort {
			end = i
		}
	}
	if start < 0 || end < 0 || start > end {
		return []string{string(defaultEffort)}
	}
	out := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, string(ordered[i]))
	}
	return out
}

// sortStrings sorts a string slice in place. This avoids importing sort for
// the single use in MapCapabilities.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
