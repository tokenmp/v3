package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

type prepareCredentialResolver struct {
	calls int
	refs  []string
	err   error
	ctx   context.Context
}

func (r *prepareCredentialResolver) Resolve(ctx context.Context, ref string) (sdk.CredentialSecret, error) {
	r.calls++
	r.refs = append(r.refs, ref)
	r.ctx = ctx
	if r.err != nil {
		return sdk.CredentialSecret{}, r.err
	}
	return sdk.NewCredentialSecret([]byte("prepared-secret")), nil
}

func prepareSnapshot(t *testing.T) *snapshot.CompiledSnapshot {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "prepare-revision",
		Models: map[string]adapter.ModelInput{
			"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		},
		Providers: map[string]adapter.ProviderInput{
			"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"adapter": {ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat, Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"}, Request: adapter.RequestPolicy{AllowedHeaders: []string{"X-Test"}}},
		},
		Routes: []adapter.RouteInput{{ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "upstream", Priority: 7, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "group", Credentials: []adapter.CredentialInput{{ID: "credential", CredentialRef: "vault://private/credential", Priority: 3, Enabled: true}}}},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	frozen, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 42)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	return frozen
}

func prepareCandidate(t *testing.T, resolver *Resolver) Candidate {
	t.Helper()
	plan, err := resolver.Resolve(context.Background(), Selector{Model: "model"})
	if err != nil || len(plan.Candidates) != 1 {
		t.Fatalf("Resolve: candidates=%d err=%v", len(plan.Candidates), err)
	}
	return plan.Candidates[0]
}

func TestResolverPreparePinsPrivateConfigAndRedactsReference(t *testing.T) {
	resolver := resolver(t, prepareSnapshot(t), nil)
	candidate := prepareCandidate(t, resolver)
	credentials := &prepareCredentialResolver{}

	ctx := context.WithValue(context.Background(), struct{ name string }{"attempt"}, "value")
	prepared, err := resolver.Prepare(candidate)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if credentials.calls != 0 {
		t.Fatalf("Prepare credential calls = %d, want 0", credentials.calls)
	}
	secret, err := resolver.ResolveCredential(ctx, candidate, credentials)
	if err != nil {
		t.Fatalf("ResolveCredential: %v", err)
	}
	if credentials.ctx != ctx {
		t.Fatal("ResolveCredential did not pass the attempt context to CredentialResolver")
	}
	if credentials.calls != 1 || credentials.refs[0] != "vault://private/credential" {
		t.Fatalf("credential resolve calls/refs = %d/%q", credentials.calls, credentials.refs)
	}
	if formatted := fmt.Sprintf("%+v", secret); formatted != "[REDACTED]" {
		t.Fatalf("secret formatting = %s", formatted)
	}
	if prepared.Target != (sdk.Target{BaseURL: "https://provider.example/v1", UpstreamModel: "upstream", Protocol: adapter.ProtocolOpenAIChat}) {
		t.Fatalf("Target = %+v", prepared.Target)
	}
	if prepared.Candidate != (sdk.CandidateIdentity{ModelID: "model", ProviderID: "provider", RouteID: "route", CredentialID: "credential", AdapterID: "adapter"}) {
		t.Fatalf("Candidate = %+v", prepared.Candidate)
	}
	if prepared.Revision != "prepare-revision" || prepared.Generation != 42 || prepared.Adapter.Auth.CredentialRef != "" {
		t.Fatalf("prepared pins/redaction = %+v", prepared)
	}
	formatted := fmt.Sprintf("%+v", prepared)
	if strings.Contains(formatted, "vault://") || strings.Contains(formatted, "prepared-secret") {
		t.Fatalf("prepared formatting leaked private data: %s", formatted)
	}

	// All exported mutable prepared fields are fresh copies rather than aliases
	// into the Resolver-owned snapshot or a later prepared attempt.
	prepared.Adapter.Request.AllowedHeaders[0] = "mutated"
	prepared.Retry.Rules = append(prepared.Retry.Rules, adapter.RetryRule{ID: "mutated"})
	again, err := resolver.Prepare(candidate)
	if err != nil {
		t.Fatalf("Prepare second: %v", err)
	}
	if again.Adapter.Request.AllowedHeaders[0] != "X-Test" || len(again.Retry.Rules) != 0 {
		t.Fatalf("prepared attempt retained external mutation: %+v", again.Adapter)
	}
}

func TestResolverPrepareRejectsForgedCandidateDimensions(t *testing.T) {
	resolver := resolver(t, prepareSnapshot(t), nil)
	candidate := prepareCandidate(t, resolver)
	for name, forge := range map[string]func(*Candidate){
		"model":               func(c *Candidate) { c.ModelID = "other" },
		"provider id":         func(c *Candidate) { c.Provider.ID = "other" },
		"provider selector":   func(c *Candidate) { c.Provider.Selector = "other" },
		"route":               func(c *Candidate) { c.RouteID = "other" },
		"group":               func(c *Candidate) { c.Group = "other" },
		"adapter":             func(c *Candidate) { c.AdapterID = "other" },
		"upstream":            func(c *Candidate) { c.Upstream = "other" },
		"protocol":            func(c *Candidate) { c.Protocol = adapter.ProtocolAnthropic },
		"priority":            func(c *Candidate) { c.Priority++ },
		"credential id":       func(c *Candidate) { c.Credential.ID = "other" },
		"credential priority": func(c *Candidate) { c.Credential.Priority++ },
		"revision":            func(c *Candidate) { c.Revision = "other" },
		"generation":          func(c *Candidate) { c.Generation++ },
	} {
		t.Run(name, func(t *testing.T) {
			forged := candidate
			forge(&forged)
			_, err := resolver.Prepare(forged)
			if !errors.Is(err, ErrInvalidCandidate) {
				t.Fatalf("Prepare forged %s error = %v, want ErrInvalidCandidate", name, err)
			}
		})
	}
}

func TestResolverResolveCredentialRejectsForgedCandidateWithoutSideEffect(t *testing.T) {
	resolver := resolver(t, prepareSnapshot(t), nil)
	candidate := prepareCandidate(t, resolver)
	candidate.Credential.ID = "forged"
	credentials := &prepareCredentialResolver{}
	_, err := resolver.ResolveCredential(context.Background(), candidate, credentials)
	if !errors.Is(err, ErrInvalidCandidate) || credentials.calls != 0 {
		t.Fatalf("ResolveCredential forged err/calls = %v/%d, want ErrInvalidCandidate/0", err, credentials.calls)
	}
}

func TestResolverPrepareAuthNoneAndContextAndSafeErrors(t *testing.T) {
	t.Run("auth none skips resolver", func(t *testing.T) {
		resolver := resolver(t, authNoneSnapshot(t), nil)
		candidate := prepareCandidate(t, resolver)
		credentials := &prepareCredentialResolver{err: errors.New("must not be called")}
		prepared, err := resolver.Prepare(candidate)
		if err != nil || credentials.calls != 0 {
			t.Fatalf("Prepare(AuthNone) err/calls = %v/%d", err, credentials.calls)
		}
		if prepared.Candidate.CredentialID != "" || prepared.Adapter.Auth.CredentialRef != "" {
			t.Fatalf("AuthNone prepared credential/auth = %+v/%+v", prepared.Candidate, prepared.Adapter.Auth)
		}
		secret, err := resolver.ResolveCredential(context.Background(), candidate, credentials)
		if err != nil || credentials.calls != 0 {
			t.Fatalf("ResolveCredential(AuthNone) err/calls = %v/%d", err, credentials.calls)
		}
		if err := secret.Use(func(value []byte) error {
			if len(value) != 0 {
				t.Fatalf("AuthNone secret = %q", value)
			}
			return nil
		}); err != nil {
			t.Fatalf("AuthNone secret Use: %v", err)
		}
	})

	t.Run("legacy credential reference never escapes prepared values or errors", func(t *testing.T) {
		config := integrationDefaultConfig(t)
		frozen := compileRoutingIntegrationSnapshot(t, config, 43)
		resolver := resolver(t, frozen, nil)
		plan, resolveErr := resolver.Resolve(context.Background(), Selector{Model: "chat-default"})
		if resolveErr != nil || len(plan.Candidates) == 0 {
			t.Fatalf("Resolve legacy: candidates=%d err=%v", len(plan.Candidates), resolveErr)
		}
		candidate := plan.Candidates[0]
		legacyRef := config.Adapters[config.Routes[0].AdapterID].Auth.CredentialRef
		credentials := &prepareCredentialResolver{err: fmt.Errorf("legacy resolver failed for %s", legacyRef)}
		prepared, err := resolver.Prepare(candidate)
		if err != nil || strings.Contains(fmt.Sprintf("%+v", prepared), legacyRef) {
			t.Fatalf("legacy Prepare leaked or wrong value: err=%v prepared=%+v", err, prepared)
		}
		_, err = resolver.ResolveCredential(context.Background(), candidate, credentials)
		if !errors.Is(err, ErrCredentialUnavailable) || strings.Contains(fmt.Sprint(err), legacyRef) {
			t.Fatalf("legacy ResolveCredential leaked or wrong error: %v", err)
		}
	})

	t.Run("resolver error and cancellation disclose neither reference nor raw candidate", func(t *testing.T) {
		resolver := resolver(t, prepareSnapshot(t), nil)
		candidate := prepareCandidate(t, resolver)
		candidate.Upstream = "raw-upstream-must-not-appear"
		_, err := resolver.Prepare(candidate)
		if !errors.Is(err, ErrInvalidCandidate) || strings.Contains(fmt.Sprint(err), "raw-upstream") || strings.Contains(fmt.Sprint(err), "vault://") {
			t.Fatalf("forged error leaked or wrong: %v", err)
		}

		candidate = prepareCandidate(t, resolver)
		credentials := &prepareCredentialResolver{err: fmt.Errorf("resolver failed for vault://private/credential")}
		_, err = resolver.ResolveCredential(context.Background(), candidate, credentials)
		if !errors.Is(err, ErrCredentialUnavailable) || strings.Contains(fmt.Sprint(err), "vault://") {
			t.Fatalf("resolver error leaked or wrong: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = resolver.ResolveCredential(ctx, candidate, credentials)
		if !errors.Is(err, context.Canceled) || credentials.calls != 1 {
			t.Fatalf("ResolveCredential canceled err/calls = %v/%d", err, credentials.calls)
		}
	})
}
