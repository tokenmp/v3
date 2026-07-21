package routing

import (
	"context"
	"errors"
	"strings"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

var (
	// ErrInvalidCandidate means the supplied public candidate did not exactly
	// match this resolver's private, frozen configuration.
	ErrInvalidCandidate = errors.New("routing candidate is not available")
	// ErrCredentialUnavailable means an authenticated credential could not be
	// resolved. It deliberately does not expose the credential reference.
	ErrCredentialUnavailable = errors.New("routing credential is unavailable")
)

// CredentialResolver resolves a private configured credential reference into
// an opaque call-local secret. Implementations must not return references or
// secret material in errors.
type CredentialResolver interface {
	Resolve(context.Context, string) (sdk.CredentialSecret, error)
}

// PreparedAttempt is the pure, execution-required configuration for one
// selected candidate. It contains neither a credential reference nor a secret.
// Each call to Prepare constructs independent copies of mutable fields.
type PreparedAttempt struct {
	Target        sdk.Target
	Candidate     sdk.CandidateIdentity
	Adapter       adapter.CompiledAdapter
	ModelThinking adapter.ThinkingInput
	Retry         adapter.CompiledRetry
	Timeout       adapter.CompiledTimeout
	Revision      string
	Generation    uint64
}

// Prepare verifies candidate against this Resolver's private frozen config and
// returns only its safe, non-secret execution configuration. Public candidates
// are never trusted as configuration inputs and this method has no I/O.
func (r *Resolver) Prepare(candidate Candidate) (PreparedAttempt, error) {
	route, provider, compiledAdapter, credential, err := r.preparedConfig(candidate)
	if err != nil {
		return PreparedAttempt{}, err
	}
	model, ok := r.config.Models[route.ModelID]
	if !ok || model.ID != route.ModelID {
		return PreparedAttempt{}, ErrInvalidCandidate
	}

	return PreparedAttempt{
		Target: sdk.Target{
			BaseURL:       provider.BaseURL,
			UpstreamModel: route.UpstreamModel,
			Protocol:      route.Protocol,
		},
		Candidate: sdk.CandidateIdentity{
			ModelID:      route.ModelID,
			ProviderID:   provider.ID,
			RouteID:      route.ID,
			CredentialID: credential.ID,
			AdapterID:    compiledAdapter.ID,
		},
		Adapter:       clonePreparedAdapter(compiledAdapter),
		ModelThinking: model.Thinking,
		Retry:         clonePreparedRetry(route.Retry),
		Timeout:       route.Timeout,
		Revision:      r.revision,
		Generation:    r.generation,
	}, nil
}

// ResolveCredential strictly rechecks candidate against this resolver's frozen
// configuration, then privately sends its configured reference to credentials.
// AuthNone routes never call credentials and return the zero opaque secret.
func (r *Resolver) ResolveCredential(ctx context.Context, candidate Candidate, credentials CredentialResolver) (sdk.CredentialSecret, error) {
	if ctx == nil {
		return sdk.CredentialSecret{}, ErrInvalidCandidate
	}
	if err := ctx.Err(); err != nil {
		return sdk.CredentialSecret{}, err
	}
	_, _, compiledAdapter, credential, err := r.preparedConfig(candidate)
	if err != nil {
		return sdk.CredentialSecret{}, err
	}
	if compiledAdapter.Auth.Kind == adapter.AuthNone {
		return sdk.CredentialSecret{}, nil
	}
	if credentials == nil || credential.CredentialRef == "" {
		return sdk.CredentialSecret{}, ErrCredentialUnavailable
	}
	secret, err := credentials.Resolve(ctx, credential.CredentialRef)
	if err == nil {
		return secret, nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return sdk.CredentialSecret{}, contextErr
	}
	if errors.Is(err, context.Canceled) {
		return sdk.CredentialSecret{}, context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.CredentialSecret{}, context.DeadlineExceeded
	}
	return sdk.CredentialSecret{}, ErrCredentialUnavailable
}

// preparedConfig looks up candidate only through the Resolver-owned frozen
// config. Every public candidate dimension must agree with the selected route,
// provider, adapter, and enabled credential before it may reach execution.
func (r *Resolver) preparedConfig(candidate Candidate) (adapter.CompiledRoute, adapter.CompiledProvider, adapter.CompiledAdapter, adapter.CompiledCredential, error) {
	if r == nil || r.config == nil || strings.TrimSpace(r.revision) == "" || r.generation == 0 || candidate.Revision != r.revision || candidate.Generation != r.generation {
		return adapter.CompiledRoute{}, adapter.CompiledProvider{}, adapter.CompiledAdapter{}, adapter.CompiledCredential{}, ErrInvalidCandidate
	}
	for _, route := range r.config.Routes {
		if !route.Enabled || route.ID != candidate.RouteID || route.ModelID != candidate.ModelID || route.ProviderID != candidate.Provider.ID || route.AdapterID != candidate.AdapterID || route.RouteGroup != candidate.Group || route.Priority != candidate.Priority || route.UpstreamModel != candidate.Upstream || route.Protocol != candidate.Protocol {
			continue
		}
		provider, ok := r.config.Providers[route.ProviderID]
		if !ok || provider.ID != candidate.Provider.ID || provider.Selector != candidate.Provider.Selector || provider.Protocol != route.Protocol {
			continue
		}
		compiledAdapter, ok := r.config.Adapters[route.AdapterID]
		if !ok || compiledAdapter.ID != candidate.AdapterID || compiledAdapter.Protocol != route.Protocol || compiledAdapter.SDKKind != provider.SDKKind {
			continue
		}
		if compiledAdapter.Auth.Kind == adapter.AuthNone {
			if candidate.Credential != (Credential{}) {
				continue
			}
			return route, provider, compiledAdapter, adapter.CompiledCredential{}, nil
		}
		for _, credential := range route.Credentials {
			if credential.Enabled && credential.ID == candidate.Credential.ID && credential.Priority == candidate.Credential.Priority {
				return route, provider, compiledAdapter, credential, nil
			}
		}
	}
	return adapter.CompiledRoute{}, adapter.CompiledProvider{}, adapter.CompiledAdapter{}, adapter.CompiledCredential{}, ErrInvalidCandidate
}

func clonePreparedAdapter(in adapter.CompiledAdapter) adapter.CompiledAdapter {
	config := adapter.CloneCompiledConfig(adapter.CompiledConfig{Adapters: map[string]adapter.CompiledAdapter{"attempt": in}})
	out := config.Adapters["attempt"]
	// A reference belongs exclusively to routing and must never escape through
	// a prepared attempt, including AuthNone's already-empty rule.
	out.Auth.CredentialRef = ""
	return out
}

func clonePreparedRetry(in adapter.CompiledRetry) adapter.CompiledRetry {
	config := adapter.CloneCompiledConfig(adapter.CompiledConfig{Routes: []adapter.CompiledRoute{{Retry: in}}})
	return config.Routes[0].Retry
}
