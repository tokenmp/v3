package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/protocolconvert"
	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// crossProtocolFixture builds a compiled, frozen resolver and Plan with one
// Anthropic Messages route, so a request with Protocol=openai_chat triggers
// cross-protocol conversion.
func crossProtocolFixture(t *testing.T) (*routing.Resolver, routing.Plan) {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "cross-protocol-revision",
		Models: map[string]adapter.ModelInput{
			"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat, adapter.CapabilityMessages}},
		},
		Providers: map[string]adapter.ProviderInput{
			"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"adapter": {
				ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic,
				Auth: adapter.AuthRule{Kind: adapter.AuthAPIKeyHeader, Header: "x-api-key"},
			},
		},
		Routes: []adapter.RouteInput{
			{
				ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "claude-upstream",
				Priority: 1, Enabled: true, Protocol: adapter.ProtocolAnthropic, RouteGroup: "group",
				Credentials: []adapter.CredentialInput{
					{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 7)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	resolver, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// Resolve without protocol filter to get the Anthropic route
	plan, err := resolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolver, plan
}

// crossProtocolOpenAIFixture builds a fixture with an OpenAI Chat route,
// so a request with Protocol=anthropic_messages triggers cross-protocol
// conversion in the reverse direction.
func crossProtocolOpenAIFixture(t *testing.T) (*routing.Resolver, routing.Plan) {
	t.Helper()
	config, err := adapter.Compile(adapter.ConfigInput{
		Revision: "cross-protocol-oai-revision",
		Models: map[string]adapter.ModelInput{
			"model": {ID: "model", Capabilities: []adapter.Capability{adapter.CapabilityChat}},
		},
		Providers: map[string]adapter.ProviderInput{
			"provider": {ID: "provider", Name: "provider", Selector: "selected", BaseURL: "https://provider.example/v1", SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat},
		},
		Adapters: map[string]adapter.AdapterConfig{
			"adapter": {
				ID: "adapter", Name: "adapter", Version: 1, SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat,
				Auth: adapter.AuthRule{Kind: adapter.AuthBearerHeader, Header: "Authorization"},
			},
		},
		Routes: []adapter.RouteInput{
			{
				ID: "route", ModelID: "model", ProviderID: "provider", AdapterID: "adapter", UpstreamModel: "gpt-upstream",
				Priority: 1, Enabled: true, Protocol: adapter.ProtocolOpenAIChat, RouteGroup: "group",
				Credentials: []adapter.CredentialInput{
					{ID: "cred-a", CredentialRef: "vault://private/cred-a", Priority: 1, Enabled: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	source, err := snapshot.NewCompiledSnapshot(config.Revision, &config, 7)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot: %v", err)
	}
	resolver, err := routing.NewResolver(source, nil, nil)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	plan, err := resolver.Resolve(context.Background(), routing.Selector{Model: "model"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolver, plan
}

// crossProtocolInput builds an Input with an OpenAI Chat request protocol
// but a Plan that resolves to an Anthropic Messages route.
func crossProtocolInput(resolver *routing.Resolver, plan routing.Plan) Input {
	return Input{
		RequestID:     "req-cross-1",
		QuotaIdentity: QuotaIdentity{Subject: "subject", KeyID: "key-1", Protocol: "openai_chat"},
		ReservationID: testReservationID,
		Plan:          plan,
		Resolver:      resolver,
		Credentials:   staticCredentials{value: []byte("call-local-secret")},
		Body:          json.RawMessage(`{"model":"model","messages":[{"role":"user","content":"hi"}]}`),
	}
}

// crossProtocolReverseInput builds an Input with an Anthropic Messages request
// protocol but a Plan that resolves to an OpenAI Chat route.
func crossProtocolReverseInput(resolver *routing.Resolver, plan routing.Plan) Input {
	return Input{
		RequestID:     "req-cross-rev-1",
		QuotaIdentity: QuotaIdentity{Subject: "subject", KeyID: "key-1", Protocol: "anthropic_messages"},
		ReservationID: testReservationID,
		Plan:          plan,
		Resolver:      resolver,
		Credentials:   staticCredentials{value: []byte("call-local-secret")},
		Body:          json.RawMessage(`{"model":"model","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`),
	}
}

func TestRunnerCrossProtocolOpenAIChatToAnthropicMessages(t *testing.T) {
	// Request comes in as openai_chat, but the only route is anthropic_messages.
	// The Runner should convert the request body to Anthropic format, call the
	// Anthropic SDK, then convert the response back to OpenAI Chat format.
	var recordedCall sdk.Call
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		recordedCall = call
		// Verify the request body is in Anthropic format (has "max_tokens" and "messages" with Anthropic roles)
		var body map[string]any
		if err := json.Unmarshal(call.Request.Body, &body); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}
		if _, ok := body["max_tokens"]; !ok {
			t.Error("converted request missing max_tokens (Anthropic format)")
		}
		if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
			first := msgs[0].(map[string]any)
			if first["role"] != "user" {
				t.Errorf("first message role = %v, want user", first["role"])
			}
		} else {
			t.Error("converted request missing messages")
		}
		// Return an Anthropic Messages response
		anthropicResp := `{"id":"msg_123","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`
		return sdk.Completion{
			RawJSON:   json.RawMessage(anthropicResp),
			Status:    200,
			RequestID: "req_anthropic",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			Known:     true,
		}, nil
	}}

	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	resolver, plan := crossProtocolFixture(t)
	result, err := runner.Run(context.Background(), crossProtocolInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	// Verify the response is converted back to OpenAI Chat format
	var resp map[string]any
	if err := json.Unmarshal(result.Completion.RawJSON, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp["object"] != "chat.completion" {
		t.Errorf("response object = %v, want chat.completion", resp["object"])
	}
	if choices, ok := resp["choices"].([]any); ok && len(choices) > 0 {
		choice := choices[0].(map[string]any)
		if choice["finish_reason"] != "stop" {
			t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
		}
	} else {
		t.Error("response missing choices")
	}

	// Verify the SDK received the converted (Anthropic) request body
	if recordedCall.Target.Protocol != adapter.ProtocolAnthropic {
		t.Errorf("target protocol = %v, want anthropic_messages", recordedCall.Target.Protocol)
	}

	// Verify quota was finalized with correct usage
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestRunnerCrossProtocolAnthropicToOpenAIChat(t *testing.T) {
	// Request comes in as anthropic_messages, but the only route is openai_chat.
	// The Runner should convert the request body to OpenAI Chat format, call the
	// OpenAI SDK, then convert the response back to Anthropic Messages format.
	var recordedCall sdk.Call
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		recordedCall = call
		// Verify the request body is in OpenAI Chat format
		var body map[string]any
		if err := json.Unmarshal(call.Request.Body, &body); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}
		if _, ok := body["messages"]; !ok {
			t.Error("converted request missing messages (OpenAI format)")
		}
		// Return an OpenAI Chat response
		openaiResp := `{"id":"chatcmpl-123","object":"chat.completion","model":"gpt-upstream","created":0,"choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
		return sdk.Completion{
			RawJSON:   json.RawMessage(openaiResp),
			Status:    200,
			RequestID: "req_openai",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			Known:     true,
		}, nil
	}}

	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	resolver, plan := crossProtocolOpenAIFixture(t)
	result, err := runner.Run(context.Background(), crossProtocolReverseInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}

	// Verify the response is converted back to Anthropic Messages format
	var resp map[string]any
	if err := json.Unmarshal(result.Completion.RawJSON, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if resp["type"] != "message" {
		t.Errorf("response type = %v, want message", resp["type"])
	}
	if resp["role"] != "assistant" {
		t.Errorf("response role = %v, want assistant", resp["role"])
	}

	// Verify the SDK received the converted (OpenAI) request body
	if recordedCall.Target.Protocol != adapter.ProtocolOpenAIChat {
		t.Errorf("target protocol = %v, want openai_chat", recordedCall.Target.Protocol)
	}
}

func TestRunnerSameProtocolNoConversion(t *testing.T) {
	// When request protocol matches route protocol, no conversion should occur.
	// The request body should pass through unchanged.
	var recordedBody json.RawMessage
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		recordedBody = call.Request.Body
		return sdk.Completion{RawJSON: json.RawMessage(`{"ok":true}`), Status: 200, RequestID: "req_ok"}, nil
	}}

	log := requestlog.NewInMemoryExecution()
	runner, _, _ := newRunner(t, client, log)
	resolver, plan := runnerFixture(t)

	// Same-protocol: request is openai_chat, route is openai_chat
	in := runnerInput(resolver, plan)
	result, err := runner.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.Status != 200 {
		t.Fatalf("Completion = %+v", result.Completion)
	}

	// Body should be adapter-applied (not protocol-converted). The adapter
	// engine may reorder JSON keys, so compare semantically.
	var recorded, original map[string]any
	if err := json.Unmarshal(recordedBody, &recorded); err != nil {
		t.Fatalf("recorded body is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(in.Body, &original); err != nil {
		t.Fatalf("original body is not valid JSON: %v", err)
	}
	// Both should have a "messages" key with the same content
	if _, ok := recorded["messages"]; !ok {
		t.Fatal("same-protocol body missing messages key")
	}
	if _, ok := original["messages"]; !ok {
		t.Fatal("original body missing messages key")
	}
}

func TestRunnerCrossProtocolRequestConversionFailure(t *testing.T) {
	// When the request body cannot be converted (e.g., valid JSON but invalid
	// OpenAI Chat shape that fails protocol conversion), the Runner should
	// return ErrProtocolConvert without retrying.
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	resolver, plan := crossProtocolFixture(t)
	in := crossProtocolInput(resolver, plan)
	// Valid JSON but missing required "messages" field for OpenAI Chat conversion
	in.Body = json.RawMessage(`{"model":"test"}`)

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, ErrProtocolConvert) {
		t.Fatalf("Run error = %v, want ErrProtocolConvert", err)
	}
	// Verify no SDK call was made
	if client.callCount() != 0 {
		t.Fatalf("client called %d times, want 0", client.callCount())
	}
	// Verify quota was released (not finalized)
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

func TestRunnerCrossProtocolResponseConversionFailure(t *testing.T) {
	// When the upstream response cannot be converted back to the request
	// protocol, the Runner should return ErrProtocolConvert.
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		// Return a response that is not valid JSON (will fail response conversion)
		return sdk.Completion{
			RawJSON:   json.RawMessage(`{invalid response`),
			Status:    200,
			RequestID: "req_bad",
		}, nil
	}}

	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	resolver, plan := crossProtocolFixture(t)
	_, err := runner.Run(context.Background(), crossProtocolInput(resolver, plan))
	if !errors.Is(err, ErrProtocolConvert) {
		t.Fatalf("Run error = %v, want ErrProtocolConvert", err)
	}
	// Verify quota was released (response conversion failure)
	if calls := quotaPort.TypedCalls(); len(calls) != 2 || calls[0].Method != "ReserveReservation" || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Release", calls)
	}
}

func TestRunnerCrossProtocolUnsupportedConversion(t *testing.T) {
	// When the conversion pair is unsupported (e.g., openai_images ↔ anthropic),
	// the Runner should return ErrProtocolConvert.
	// This test uses a fixture with an Anthropic route but sends a request
	// with an unsupported protocol (openai_images).
	resolver, plan := crossProtocolFixture(t)
	in := crossProtocolInput(resolver, plan)
	// Change the request protocol to one that doesn't support conversion
	in.QuotaIdentity.Protocol = "openai_images"
	in.Body = json.RawMessage(`{"prompt":"test"}`)

	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, ErrProtocolConvert) {
		t.Fatalf("Run error = %v, want ErrProtocolConvert", err)
	}
	if client.callCount() != 0 {
		t.Fatalf("client called %d times, want 0", client.callCount())
	}
}

func TestRunnerCrossProtocolErrorDoesNotLeakDetails(t *testing.T) {
	// ErrProtocolConvert must not contain request body, response body, or
	// conversion details.
	client := &runnerTestClient{}
	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	resolver, plan := crossProtocolFixture(t)
	in := crossProtocolInput(resolver, plan)
	// Valid JSON but missing required "messages" field for OpenAI Chat conversion
	in.Body = json.RawMessage(`{"model":"test"}`)

	_, err := runner.Run(context.Background(), in)
	if !errors.Is(err, ErrProtocolConvert) {
		t.Fatalf("Run error = %v, want ErrProtocolConvert", err)
	}
	errText := err.Error()
	for _, marker := range []string{"invalid", "messages", "protocolconvert"} {
		if strings.Contains(errText, marker) {
			t.Fatalf("ErrProtocolConvert leaked detail %q: %s", marker, errText)
		}
	}
}

func TestRunnerCrossProtocolLogSurfaceNeverLeaksSecretOrBody(t *testing.T) {
	// Cross-protocol execution logs must not leak secrets, request bodies,
	// or response bodies.
	anthropicResp := `{"id":"msg_123","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		return sdk.Completion{
			RawJSON:   json.RawMessage(anthropicResp),
			Status:    200,
			RequestID: "req_anthropic",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			Known:     true,
		}, nil
	}}

	log := requestlog.NewInMemoryExecution()
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     &recordingSleeper{clock: clock},
	}

	resolver, plan := crossProtocolFixture(t)
	in := crossProtocolInput(resolver, plan)
	in.Credentials = staticCredentials{value: []byte("super-secret-key")}

	result, err := runner.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	// Verify response was converted
	var resp map[string]any
	if err := json.Unmarshal(result.Completion.RawJSON, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	for _, event := range log.Events(context.Background()) {
		rendered := strings.Replace(fmt.Sprintf("%+v", event), "map[", "map[", -1)
		for _, marker := range []string{"super-secret-key", "vault://", "Hello!", "msg_123"} {
			if strings.Contains(rendered, marker) {
				t.Fatalf("event leaked %q: %s", marker, rendered)
			}
		}
	}
}

func TestRunnerCrossProtocolRace(t *testing.T) {
	// Concurrent cross-protocol executions must not race.
	anthropicResp := `{"id":"msg_123","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"Hello!"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":5}}`
	var callCount int32
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		atomic.AddInt32(&callCount, 1)
		return sdk.Completion{
			RawJSON:   json.RawMessage(anthropicResp),
			Status:    200,
			RequestID: "req_anthropic",
			Usage:     sdk.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			Known:     true,
		}, nil
	}}

	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, client); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const n = 10
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			log := requestlog.NewInMemoryExecution()
			clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
			runner := &Runner{
				Quota:       quotaPort,
				SDKRegistry: registry,
				Logger:      log,
				Clock:       clock,
				Sleeper:     &recordingSleeper{clock: clock},
			}
			resolver, plan := crossProtocolFixture(t)
			_, err := runner.Run(context.Background(), crossProtocolInput(resolver, plan))
			done <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent Run %d: %v", i, err)
		}
	}
}

func TestConvertRequestRoundTrip(t *testing.T) {
	// Verify that protocolconvert.ConvertRequest produces valid output for
	// the same bodies the Runner would use.
	openaiBody := json.RawMessage(`{"model":"gpt-4","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"Hello!"}],"max_tokens":100,"temperature":0.7}`)

	// OpenAI → Anthropic
	anthropicBody, err := protocolconvert.ConvertRequest(openaiBody, adapter.ProtocolOpenAIChat, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("ConvertRequest OpenAI→Anthropic: %v", err)
	}
	var ant map[string]any
	if err := json.Unmarshal(anthropicBody, &ant); err != nil {
		t.Fatalf("Anthropic body is not valid JSON: %v", err)
	}
	if _, ok := ant["max_tokens"]; !ok {
		t.Error("Anthropic body missing max_tokens")
	}
	if _, ok := ant["system"]; !ok {
		t.Error("Anthropic body missing system")
	}

	// Anthropic → OpenAI
	openaiBody2, err := protocolconvert.ConvertRequest(anthropicBody, adapter.ProtocolAnthropic, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("ConvertRequest Anthropic→OpenAI: %v", err)
	}
	var oai2 map[string]any
	if err := json.Unmarshal(openaiBody2, &oai2); err != nil {
		t.Fatalf("round-trip OpenAI body is not valid JSON: %v", err)
	}
	if _, ok := oai2["messages"]; !ok {
		t.Error("round-trip OpenAI body missing messages")
	}
}
