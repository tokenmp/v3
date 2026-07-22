package streamfacade

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
	"github.com/tokenmp/v3/services/executor/internal/stream"
)

// staticCredentials is a routing.CredentialResolver that returns a fixed
// opaque secret for any reference. It mirrors the execution test double.
type staticCredentials struct{ value []byte }

func (s staticCredentials) Resolve(context.Context, string) (sdk.CredentialSecret, error) {
	return sdk.NewCredentialSecret(s.value), nil
}

// recordingDriver is a Driver double that records exactly the Input it
// received and counts Run calls. It returns a canned result/error.
type recordingDriver struct {
	mu       sync.Mutex
	calls    int
	input    execution.StreamInput
	result   execution.StreamResult
	runErr   error
	onceGate bool // when true, a second Run call panics to assert exactly-once
}

func (r *recordingDriver) Run(_ context.Context, in execution.StreamInput) (execution.StreamResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.onceGate && r.calls > 1 {
		panic("recordingDriver: Run called more than once")
	}
	r.input = in
	return r.result, r.runErr
}

func (r *recordingDriver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *recordingDriver) lastInput() execution.StreamInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.input
}

// trustedPrincipal is the trusted authenticated Principal a transport auth
// boundary would carry on a normalized request.
type recordingSink struct{}

func (recordingSink) Commit(context.Context, []sdk.StreamEvent) error   { return nil }
func (recordingSink) WriteEvent(context.Context, sdk.StreamEvent) error { return nil }
func (recordingSink) Flush(context.Context) error                       { return nil }

func trustedPrincipal() stream.Principal {
	return stream.Principal{Subject: "svc-1", KeyID: "key-1", Role: stream.RoleService, Status: stream.StatusActive}
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

// chatStreamRequest builds a normalized OpenAI Chat stream.Request carrying a
// trusted Principal.
func chatStreamRequest(model, requestID string) stream.Request {
	return stream.Request{
		Protocol:  adapter.ProtocolOpenAIChat,
		Selector:  model,
		Body:      json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`),
		RequestID: requestID,
		Principal: trustedPrincipal(),
		Sink:      recordingSink{},
	}
}

// messageStreamRequest builds a normalized Anthropic Messages stream.Request
// carrying a trusted Principal.
func messageStreamRequest(model, requestID string) stream.Request {
	return stream.Request{
		Protocol:  adapter.ProtocolAnthropic,
		Selector:  model,
		Body:      json.RawMessage(`{"model":"` + model + `","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`),
		RequestID: requestID,
		Principal: trustedPrincipal(),
		Sink:      recordingSink{},
	}
}

// unauthenticated returns req with no Principal, simulating a request that
// reached the facade without a trusted transport auth boundary.
func unauthenticated(req stream.Request) stream.Request {
	req.Principal = stream.Principal{}
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
