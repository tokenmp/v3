// Package quarantinebridge adapts a runtime.Port into a routing.QuarantineReader.
//
// The resolver consults quarantine state one dimension at a time (model,
// provider, route, or credential). The runtime port keys state by a single
// opaque RuntimeTarget string. This bridge maps each routing.QuarantineTarget
// dimension to a distinct, prefixed RuntimeTarget so that overlapping IDs
// across dimensions (for example a model and a route that share the value
// "primary") never collide.
//
// The bridge is a fail-closed anti-corruption layer: it never leaks raw
// upstream error text into the routing package. not-found is preserved as
// routing.ErrNotFound, context cancellation is normalized to the bare
// context.Canceled/context.DeadlineExceeded sentinel (with ctx.Err()
// taking precedence, so errors.Is keeps working and no wrapper text leaks),
// and every other read failure — including a nil or typed-nil port — surfaces
// as routing.ErrQuarantineUnavailable, which the resolver maps to a closed
// resolution failure.
package quarantinebridge

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/runtime"
)

// Compile-time contract: Bridge satisfies routing.QuarantineReader.
var _ routing.QuarantineReader = (*Bridge)(nil)

// Bridge adapts a runtime.Port to a routing.QuarantineReader.
//
// A Bridge with a nil or typed-nil port is deliberately usable: every
// GetQuarantine call fails closed with routing.ErrQuarantineUnavailable so a
// missing runtime port can never silently admit a quarantined target.
type Bridge struct {
	port runtime.Port
}

// New returns a Bridge over port. port may be nil or a typed-nil interface
// value; the returned Bridge still satisfies routing.QuarantineReader and
// fails closed on every read.
func New(port runtime.Port) *Bridge {
	return &Bridge{port: port}
}

// GetQuarantine implements routing.QuarantineReader.
//
// A cancelled or deadline-exceeded context fails closed before any port
// access: the exact context error is returned unchanged so errors.Is keeps
// working, and a runtime port that ignores ctx can never report stale data.
// If the port itself later returns a wrapped context.Canceled or
// context.DeadlineExceeded, ctx.Err() takes precedence (returned exactly when
// non-nil); otherwise the wrapper is dropped and the bare sentinel is
// returned, so no raw upstream marker text leaks while errors.Is keeps
// working.
//
// Each non-empty dimension of target maps to a distinct runtime target. Empty
// dimensions are not queried, and a fully empty target returns
// routing.ErrNotFound (there is nothing to read). When several dimensions are
// set, the bridge queries each one and returns the latest expiry so the
// combined target is excluded as long as any dimension is excluded.
func (b *Bridge) GetQuarantine(ctx context.Context, target routing.QuarantineTarget) (routing.Quarantine, error) {
	// Fail closed on a cancelled or expired context before touching the port:
	// the runtime port (e.g. InMemory) is free to ignore ctx and could otherwise
	// report stale quarantine data. Preserve the exact context error so callers'
	// errors.Is(err, context.Canceled|context.DeadlineExceeded) keeps working.
	if err := ctx.Err(); err != nil {
		return routing.Quarantine{}, err
	}
	if b == nil || isNilPort(b.port) {
		return routing.Quarantine{}, routing.ErrQuarantineUnavailable
	}
	targets := dimensionTargets(target)
	if len(targets) == 0 {
		return routing.Quarantine{}, routing.ErrNotFound
	}
	var latest time.Time
	var found bool
	for _, rt := range targets {
		state, err := b.port.GetQuarantine(ctx, rt)
		if err == nil {
			if !found || state.Until.After(latest) {
				latest = state.Until
			}
			found = true
			continue
		}
		if errors.Is(err, runtime.ErrNotFound) {
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// The port reported a context cancellation. ctx.Err() is
			// authoritative: if ctx is now done, return the exact ctx.Err()
			// sentinel (which may differ from the port's wrapped variant).
			// Otherwise normalize any wrapped context.Canceled or
			// context.DeadlineExceeded to the bare sentinel so no raw upstream
			// marker text survives in the returned error string, while
			// errors.Is(err, context.Canceled|context.DeadlineExceeded) keeps
			// working. The raw wrapper is never preserved.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return routing.Quarantine{}, ctxErr
			}
			if errors.Is(err, context.Canceled) {
				return routing.Quarantine{}, context.Canceled
			}
			return routing.Quarantine{}, context.DeadlineExceeded
		}
		return routing.Quarantine{}, routing.ErrQuarantineUnavailable
	}
	if !found {
		return routing.Quarantine{}, routing.ErrNotFound
	}
	return routing.Quarantine{Until: latest}, nil
}

// dimensionTargets maps the non-empty dimensions of target to distinct,
// unambiguous runtime targets. The order is fixed (model, provider, route,
// credential) so combined reads are deterministic. Empty dimensions remain
// empty and are omitted.
func dimensionTargets(target routing.QuarantineTarget) []runtime.RuntimeTarget {
	var targets []runtime.RuntimeTarget
	if target.ModelID != "" {
		targets = append(targets, runtime.RuntimeTarget("model:"+target.ModelID))
	}
	if target.ProviderID != "" {
		targets = append(targets, runtime.RuntimeTarget("provider:"+target.ProviderID))
	}
	if target.RouteID != "" {
		targets = append(targets, runtime.RuntimeTarget("route:"+target.RouteID))
	}
	if target.CredentialID != "" {
		targets = append(targets, runtime.RuntimeTarget("credential:"+target.CredentialID))
	}
	return targets
}

// isNilPort reports whether port is an untyped nil or a typed-nil pointer
// implementing runtime.Port.
func isNilPort(port runtime.Port) bool {
	if port == nil {
		return true
	}
	v := reflect.ValueOf(port)
	return v.Kind() == reflect.Ptr && v.IsNil()
}
