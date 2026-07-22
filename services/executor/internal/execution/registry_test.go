package execution

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

type registryTestClient struct{ id string }

func (registryTestClient) Complete(context.Context, sdk.Call) (sdk.Completion, error) {
	return sdk.Completion{}, nil
}

func TestSDKRegistryExactKeyLookup(t *testing.T) {
	t.Parallel()

	registry := NewSDKRegistry()
	openAIChat := registryTestClient{id: "openai-chat"}
	anthropic := registryTestClient{id: "anthropic-messages"}
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, openAIChat); err != nil {
		t.Fatalf("Register(openai chat) error = %v", err)
	}
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, anthropic); err != nil {
		t.Fatalf("Register(anthropic messages) error = %v", err)
	}

	got, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("Client(openai chat) error = %v", err)
	}
	if got != openAIChat {
		t.Errorf("Client(openai chat) = %#v, want %#v", got, openAIChat)
	}

	got, err = registry.Client(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic)
	if err != nil {
		t.Fatalf("Client(anthropic messages) error = %v", err)
	}
	if got != anthropic {
		t.Errorf("Client(anthropic messages) = %#v, want %#v", got, anthropic)
	}
}

func TestSDKRegistryZeroValueIsReady(t *testing.T) {
	t.Parallel()

	var registry SDKRegistry
	client := registryTestClient{id: "zero-value"}
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	got, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat)
	if err != nil || got != client {
		t.Errorf("Client() = %#v, %v; want %#v, nil", got, err, client)
	}
}

func TestSDKRegistryUnknownIsTypedAndExact(t *testing.T) {
	t.Parallel()

	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, registryTestClient{}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	client, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIResponses)
	if client != nil {
		t.Errorf("Client() = %#v, want nil", client)
	}
	if !errors.Is(err, ErrSDKClientUnknown) {
		t.Fatalf("Client() error = %v, want ErrSDKClientUnknown", err)
	}
	var unknown *UnknownSDKClientError
	if !errors.As(err, &unknown) {
		t.Fatalf("Client() error = %T, want *UnknownSDKClientError", err)
	}
	if want := (SDKClientKey{SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIResponses}); unknown.Key != want {
		t.Errorf("unknown key = %+v, want %+v", unknown.Key, want)
	}
}

func TestSDKRegistryDuplicatePreservesFirstClient(t *testing.T) {
	t.Parallel()

	registry := NewSDKRegistry()
	first := registryTestClient{id: "first"}
	second := registryTestClient{id: "second"}
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, first); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, second)
	if !errors.Is(err, ErrSDKClientDuplicate) {
		t.Fatalf("second Register() error = %v, want ErrSDKClientDuplicate", err)
	}
	var duplicate *DuplicateSDKClientError
	if !errors.As(err, &duplicate) {
		t.Fatalf("second Register() error = %T, want *DuplicateSDKClientError", err)
	}
	if want := (SDKClientKey{SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat}); duplicate.Key != want {
		t.Errorf("duplicate key = %+v, want %+v", duplicate.Key, want)
	}

	got, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat)
	if err != nil {
		t.Fatalf("Client() error = %v", err)
	}
	if got != first {
		t.Errorf("Client() = %#v, want original %#v", got, first)
	}
}

func TestSDKRegistryRejectsNilClients(t *testing.T) {
	t.Parallel()

	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, nil); !errors.Is(err, ErrSDKClientNil) {
		t.Errorf("Register(nil) error = %v, want ErrSDKClientNil", err)
	}

	var typedNil *registryTestPointerClient
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, typedNil); !errors.Is(err, ErrSDKClientNil) {
		t.Errorf("Register(typed nil) error = %v, want ErrSDKClientNil", err)
	}
}

type registryTestPointerClient struct{}

func (*registryTestPointerClient) Complete(context.Context, sdk.Call) (sdk.Completion, error) {
	return sdk.Completion{}, nil
}

func TestSDKRegistryConcurrentRegistrationAndLookup(t *testing.T) {
	t.Parallel()

	registry := NewSDKRegistry()
	client := registryTestClient{id: "only"}
	const callers = 32

	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, duplicates int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSDKClientDuplicate):
			duplicates++
		default:
			t.Errorf("Register() error = %v", err)
		}
	}
	if successes != 1 || duplicates != callers-1 {
		t.Errorf("registration results: successes=%d duplicates=%d, want 1 and %d", successes, duplicates, callers-1)
	}

	got, err := registry.Client(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat)
	if err != nil || got != client {
		t.Errorf("Client() = %#v, %v; want %#v, nil", got, err, client)
	}
}

type registryTestStreamClient struct{ id string }

func (registryTestStreamClient) Stream(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
	return sdk.StreamOpen{}, nil
}

func TestSDKRegistryStreamCapabilityIsIndependentAndExact(t *testing.T) {
	t.Parallel()
	registry := NewSDKRegistry()
	complete := registryTestClient{id: "complete"}
	stream := registryTestStreamClient{id: "stream"}
	keyKind, keyProtocol := adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat
	if err := registry.Register(keyKind, keyProtocol, complete); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.RegisterStream(keyKind, keyProtocol, stream); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	if got, err := registry.Client(keyKind, keyProtocol); err != nil || got != complete {
		t.Fatalf("Client = %#v, %v", got, err)
	}
	if got, err := registry.StreamClient(keyKind, keyProtocol); err != nil || got != stream {
		t.Fatalf("StreamClient = %#v, %v", got, err)
	}
	if _, err := registry.StreamClient(keyKind, adapter.ProtocolOpenAIResponses); !errors.Is(err, ErrSDKClientUnknown) {
		t.Fatalf("wrong exact stream key error = %v", err)
	}
	if err := registry.RegisterStream(keyKind, keyProtocol, registryTestStreamClient{id: "replacement"}); !errors.Is(err, ErrSDKClientDuplicate) {
		t.Fatalf("duplicate stream registration = %v", err)
	}
}

func TestSDKRegistryRejectsNilStreamClients(t *testing.T) {
	t.Parallel()
	registry := NewSDKRegistry()
	if err := registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, nil); !errors.Is(err, ErrSDKClientNil) {
		t.Fatalf("RegisterStream(nil) = %v", err)
	}
	var typedNil *registryTestPointerStreamClient
	if err := registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, typedNil); !errors.Is(err, ErrSDKClientNil) {
		t.Fatalf("RegisterStream(typed nil) = %v", err)
	}
}

type registryTestPointerStreamClient struct{}

func (*registryTestPointerStreamClient) Stream(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
	return sdk.StreamOpen{}, nil
}

func TestSDKRegistryConcurrentStreamRegistrationAndLookup(t *testing.T) {
	t.Parallel()
	registry := NewSDKRegistry()
	client := registryTestStreamClient{id: "only-stream"}
	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	var successes, duplicates int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrSDKClientDuplicate):
			duplicates++
		default:
			t.Errorf("RegisterStream() error = %v", err)
		}
	}
	if successes != 1 || duplicates != callers-1 {
		t.Fatalf("stream registrations = %d successes, %d duplicates", successes, duplicates)
	}
	if got, err := registry.StreamClient(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat); err != nil || got != client {
		t.Fatalf("StreamClient = %#v, %v", got, err)
	}
}
