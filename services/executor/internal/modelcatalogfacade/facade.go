// Package modelcatalogfacade is the transport-neutral composition root for the
// model catalog listing. It implements modelcatalog.CatalogProvider by pinning
// the current compiled snapshot per request, filtering models by enabled routes
// and quarantine state, and mapping compiled models to safe catalog entries.
//
// The facade owns no HTTP, database, env, main, or route registration, and it
// imports no transport code: it composes against the transport-neutral
// modelcatalog port so a transport layer may wire it without coupling. Every
// unsafe input (nil/typed-nil dependency, missing or invalid Principal, missing
// snapshot) fails closed to a safe sentinel that a transport renderer reduces
// to a protocol-native response carrying no upstream, request, credential, or
// routing detail.
package modelcatalogfacade

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/modelcatalog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// Bound the safe Principal fields. These mirror nonstreamfacade's bounds and
// identityenv's safe surface.
const (
	maxSubjectBytes = 256
	maxKeyIDBytes   = 128
)

// Options configures a Facade. The named dependencies fall into two classes:
//
//   - Required (no safe default): Store and Quarantine. A nil or typed-nil
//     value for any of these fails every request closed with ErrMisconfigured
//     before any snapshot read, identity check, or quarantine consultation.
//   - Optional with a true documented default: Clock. A clean, untyped nil
//     uses wall time; a typed-nil injection fails closed.
type Options struct {
	// Store is the atomic compiled-snapshot store. The current snapshot is
	// pinned per request. Required.
	Store *snapshot.Store
	// Quarantine is the routing quarantine reader. Required: a nil or typed-nil
	// value would silently bypass quarantine filtering and fails closed.
	Quarantine routing.QuarantineReader
	// Clock supplies the instant used to evaluate quarantine expiry. A clean
	// nil or typed-nil value uses wall time.
	Clock routing.Clock
}

// Facade implements modelcatalog.CatalogProvider by composing per-request
// snapshot pinning, quarantine filtering, and model-to-entry mapping.
type Facade struct {
	store      *snapshot.Store
	quarantine routing.QuarantineReader
	clock      routing.Clock
}

var _ modelcatalog.CatalogProvider = (*Facade)(nil)

// New returns a Facade over opts. New itself does not fail: every dependency
// is revalidated on each ListModels so a facade constructed before its store
// is published still fails closed per request rather than panicking.
func New(opts Options) *Facade {
	return &Facade{
		store:      opts.Store,
		quarantine: opts.Quarantine,
		clock:      opts.Clock,
	}
}

// ListModels returns the catalog of models visible to the authenticated caller.
// It pins the current snapshot, filters models by enabled routes and quarantine
// state, maps compiled models to safe catalog entries, and returns them sorted
// by ID for deterministic output.
func (f *Facade) ListModels(ctx context.Context, req modelcatalog.CatalogRequest) (modelcatalog.CatalogResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return modelcatalog.CatalogResult{}, err
	}

	// Fail closed on a nil facade or a missing/broken required dependency
	// before any snapshot read, identity check, or quarantine consultation.
	if f == nil || isNilStore(f.store) || isNilQuarantine(f.quarantine) {
		return modelcatalog.CatalogResult{}, modelcatalog.ErrMisconfigured
	}

	// A trusted, defensively revalidated Principal is required.
	if !validPrincipal(req.Principal) {
		return modelcatalog.CatalogResult{}, modelcatalog.ErrUnauthenticated
	}

	// Pin the current snapshot for this request.
	source, err := f.store.Current()
	if err != nil {
		return modelcatalog.CatalogResult{}, modelcatalog.ErrNoSnapshot
	}
	compiled := source.Value()
	if compiled == nil {
		return modelcatalog.CatalogResult{}, modelcatalog.ErrNoSnapshot
	}

	createdUnix := 0
	if t := source.CreatedAt(); !t.IsZero() {
		createdUnix = int(t.Unix())
	}

	// Build the set of model IDs that have at least one enabled route.
	enabledModels := make(map[string]bool, len(compiled.Routes))
	for _, route := range compiled.Routes {
		if route.Enabled {
			enabledModels[route.ModelID] = true
		}
	}

	var entries []modelcatalog.CatalogEntry
	for id, m := range compiled.Models {
		// Skip models with no enabled route.
		if !enabledModels[id] {
			continue
		}

		// Check model-level quarantine. Active quarantine excludes the model;
		// read error fails closed.
		qTarget := routing.QuarantineTarget{ModelID: id}
		qState, qErr := f.quarantine.GetQuarantine(ctx, qTarget)
		if qErr != nil {
			if errors.Is(qErr, routing.ErrNotFound) {
				// Not quarantined; include the model.
			} else if errors.Is(qErr, context.Canceled) || errors.Is(qErr, context.DeadlineExceeded) {
				return modelcatalog.CatalogResult{}, qErr
			} else {
				// Quarantine unavailable: fail closed.
				return modelcatalog.CatalogResult{}, modelcatalog.ErrQuarantineUnavailable
			}
		} else {
			// Quarantine state found; check if it is still active.
			now := nowTime(f.clock)
			if qState.Until.After(now) {
				// Model is actively quarantined; exclude it.
				continue
			}
		}

		entry := modelcatalog.CatalogEntry{
			ID:           m.ID,
			Capabilities: modelcatalog.MapCapabilities(m.Capabilities),
			Thinking:     modelcatalog.MapThinking(m.Thinking),
			Created:      createdUnix,
		}
		entries = append(entries, entry)
	}

	// Sort by ID for deterministic output.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	return modelcatalog.CatalogResult{Models: entries}, nil
}

// nowTime returns the current time, using the injected clock if available
// or wall time otherwise.
func nowTime(clock routing.Clock) time.Time {
	if clock != nil {
		return clock.Now()
	}
	return time.Now()
}

// validPrincipal reports whether p is a trusted, active service/admin caller
// with non-empty bounded printable subject and keyID. It is defense-in-depth.
func validPrincipal(p modelcatalog.Principal) bool {
	if p.Status != modelcatalog.StatusActive {
		return false
	}
	if p.Role != modelcatalog.RoleService && p.Role != modelcatalog.RoleAdmin {
		return false
	}
	return validSafeToken(p.Subject, maxSubjectBytes) && validSafeToken(p.KeyID, maxKeyIDBytes)
}

// validSafeToken reports whether v is non-empty, bounded, UTF-8 valid, and
// printable (0x21..0x7e), mirroring identityenv.validToken.
func validSafeToken(v string, max int) bool {
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

func isNilStore(store *snapshot.Store) bool {
	if store == nil {
		return true
	}
	v := reflect.ValueOf(store)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func isNilQuarantine(q routing.QuarantineReader) bool {
	if q == nil {
		return true
	}
	v := reflect.ValueOf(q)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}

func isNilClock(clock routing.Clock) bool {
	if clock == nil {
		return true
	}
	v := reflect.ValueOf(clock)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}
