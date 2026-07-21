package credentialenv

import (
	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ValidateCompiled performs startup integrity validation without returning the
// compiled config or revealing references. Every enabled authenticated route
// credential must have exactly one mapped, currently usable env secret; unused
// bindings are rejected to catch initial-snapshot mapping typos. Disabled
// routes and disabled credentials are intentionally not required.
func (r *Resolver) ValidateCompiled(compiled adapter.CompiledConfig) error {
	if r == nil || r.lookupEnv == nil {
		return ErrSnapshotInvalid
	}
	needed := make(map[string]struct{})
	for _, route := range compiled.Routes {
		if !route.Enabled {
			continue
		}
		configuredAdapter, ok := compiled.Adapters[route.AdapterID]
		if !ok {
			return ErrSnapshotInvalid
		}
		if configuredAdapter.Auth.Kind == adapter.AuthNone {
			continue
		}
		enabledCredentials := 0
		for _, credential := range route.Credentials {
			if credential.Enabled {
				enabledCredentials++
				needed[credential.CredentialRef] = struct{}{}
			}
		}
		if enabledCredentials == 0 {
			return ErrSnapshotInvalid
		}
	}
	if len(needed) != len(r.bindings) {
		return ErrSnapshotInvalid
	}
	for ref := range needed {
		envName, ok := r.bindings[ref]
		if !ok {
			return ErrSnapshotInvalid
		}
		value, present := r.lookup(envName)
		if !present || !validSecret(value) {
			return ErrSnapshotInvalid
		}
	}
	return nil
}
