package nonstreamfacade

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// staticCredentials is a routing.CredentialResolver that returns a fixed
// opaque secret for any reference. It mirrors the execution test double.
type staticCredentials struct{ value []byte }

func (s staticCredentials) Resolve(context.Context, string) (sdk.CredentialSecret, error) {
	return sdk.NewCredentialSecret(s.value), nil
}

// recordingRunner is a Runner double that records exactly the Input it
// received and counts Run calls. It returns a canned result/error.
type recordingRunner struct {
	mu       sync.Mutex
	calls    int
	input    execution.Input
	result   execution.Result
	runErr   error
	onceGate bool // when true, a second Run call panics to assert exactly-once
}

func (r *recordingRunner) Run(_ context.Context, in execution.Input) (execution.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.onceGate && r.calls > 1 {
		panic("recordingRunner: Run called more than once")
	}
	r.input = in
	return r.result, r.runErr
}

func (r *recordingRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingRunner) lastInput() execution.Input {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.input
}

// trustedPrincipal is the trusted authenticated Principal a transport auth
// boundary would carry on a normalized request.
func trustedPrincipal() nonstream.Principal {
	return nonstream.Principal{Subject: "svc-1", KeyID: "key-1", Role: nonstream.RoleService, Status: nonstream.StatusActive}
}

// buildStore compiles a config with one OpenAI Chat route and one Anthropic
// Messages route (both authenticated) and publishes generation 1.
func buildStore(t *testing.T) *snapshot.Store {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "facade-revision",
		Models: map[string]adapter.ModelInput{
			"chat-model":      {ID: "chat-model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
			"anthropic-model": {ID: "anthropic-model", Capabilities: []adapter.Capability{adapter.CapabilityMessages}},
		},
		Providers: map[string]adapter.ProviderInput{
			"openai":    {ID: "openai", Name: "openai", Selector: "openai", BaseURL: "https://openai.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
			"anthropic": {ID: "anthropic", Name: "anthropic", Selector: "anthropic", BaseURL: "https://anthropic.example/v1", SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"chat-adapter": {
				ID: "chat-adapter", Name: "chat-adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat,
				Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
			},
			"anthropic-adapter": {
				ID: "anthropic-adapter", Name: "anthropic-adapter", Version: 1, SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic,
				Auth: adapter.AuthRule{Kind: adapter.AuthAPIKeyHeader, Header: "x-api-key"},
			},
		},
		Routes: []adapter.RouteInput{
			{
				ID: "chat-route", ModelID: "chat-model", ProviderID: "openai", AdapterID: "chat-adapter", UpstreamModel: "gpt-upstream",
				Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat,
				Credentials: []adapter.CredentialInput{{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true}},
			},
			{
				ID: "anthropic-route", ModelID: "anthropic-model", ProviderID: "anthropic", AdapterID: "anthropic-adapter", UpstreamModel: "claude-upstream",
				Priority: 1, Enabled: true, Protocol: adapter.ProtocolAnthropic,
				Credentials: []adapter.CredentialInput{{ID: "cred-b", CredentialRef: "vault://private/cred-b", Priority: 1, Enabled: true}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 1)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	store := &snapshot.Store{}
	if err := store.Publish(source); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return store
}

// chatRequest builds a normalized OpenAI Chat nonstream.Request carrying a
// trusted Principal.
func chatRequest(model, requestID string) nonstream.Request {
	return nonstream.Request{
		Protocol:  adapter.ProtocolOpenAIChat,
		Selector:  model,
		Body:      json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`),
		RequestID: requestID,
		Principal: trustedPrincipal(),
	}
}

// messageRequest builds a normalized Anthropic Messages nonstream.Request
// carrying a trusted Principal.
func messageRequest(model, requestID string) nonstream.Request {
	return nonstream.Request{
		Protocol:  adapter.ProtocolAnthropic,
		Selector:  model,
		Body:      json.RawMessage(`{"model":"` + model + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`),
		RequestID: requestID,
		Principal: trustedPrincipal(),
	}
}

// unauthenticated returns req with no Principal, simulating a request that
// reached the facade without a trusted transport auth boundary.
func unauthenticated(req nonstream.Request) nonstream.Request {
	req.Principal = nonstream.Principal{}
	return req
}

// stubErrQuarantine is a QuarantineReader that always fails closed.
type stubErrQuarantine struct{ err error }

func (s stubErrQuarantine) GetQuarantine(context.Context, routing.QuarantineTarget) (routing.Quarantine, error) {
	return routing.Quarantine{}, s.err
}

// noopQuarantine is a QuarantineReader that consults state and finds nothing
// excluded for any target: it returns routing.ErrNotFound, the sentinel a
// reader returns when no state has been stored. This is the safe, non-nil
// default reader a deployment with no quarantine state must inject; it is
// distinct from a nil reader, which the facade rejects because nil would
// silently skip quarantine consultation entirely.
type noopQuarantine struct{}

func (noopQuarantine) GetQuarantine(_ context.Context, _ routing.QuarantineTarget) (routing.Quarantine, error) {
	return routing.Quarantine{}, routing.ErrNotFound
}
