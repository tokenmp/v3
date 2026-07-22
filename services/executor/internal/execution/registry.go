package execution

import (
	"errors"
	"reflect"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

var (
	// ErrSDKClientUnknown marks a lookup for an SDK kind/protocol pair for
	// which no client has been registered. Callers can use errors.Is or
	// errors.As(err, *UnknownSDKClientError) without relying on an error string.
	ErrSDKClientUnknown = errors.New("execution: sdk client unknown")

	// ErrSDKClientDuplicate marks an attempted second registration for the
	// same SDK kind/protocol pair. Registrations are write-once: the original
	// client remains registered and is never silently replaced.
	ErrSDKClientDuplicate = errors.New("execution: sdk client already registered")

	// ErrSDKClientNil marks an attempt to register a nil sdk.Client.
	ErrSDKClientNil = errors.New("execution: sdk client is nil")
)

// SDKClientKey identifies an SDK implementation by the exact compiled adapter
// SDK kind and protocol. It deliberately contains no request-specific data.
type SDKClientKey struct {
	SDKKind  adapter.SDKKind
	Protocol adapter.Protocol
}

// UnknownSDKClientError is returned when no client is registered for Key.
// Key is restricted to configuration enum values and cannot contain a URL,
// secret, request, or response body.
type UnknownSDKClientError struct {
	Key SDKClientKey
}

func (e *UnknownSDKClientError) Error() string { return ErrSDKClientUnknown.Error() }
func (e *UnknownSDKClientError) Unwrap() error { return ErrSDKClientUnknown }

// DuplicateSDKClientError is returned when registration would overwrite an
// existing client. The registry deliberately keeps the first registration so a
// duplicate cannot change the process's execution behavior at runtime.
type DuplicateSDKClientError struct {
	Key SDKClientKey
}

func (e *DuplicateSDKClientError) Error() string { return ErrSDKClientDuplicate.Error() }
func (e *DuplicateSDKClientError) Unwrap() error { return ErrSDKClientDuplicate }

// SDKRegistry is an in-memory, process-local registry of target-agnostic SDK
// clients. It indexes clients only by the exact (adapter.SDKKind,
// adapter.Protocol) pair. In particular, it does not retain credentials,
// targets/URLs, headers, request bodies, or response bodies; those remain
// call-local inputs to sdk.Client.Complete.
//
// SDKRegistry is safe for concurrent registration and lookup. Registration is
// write-once per key: the first client wins and later registrations return a
// typed duplicate error without replacing it.
type SDKRegistry struct {
	mu            sync.RWMutex
	clients       map[SDKClientKey]sdk.Client
	streamClients map[SDKClientKey]sdk.StreamClient
}

// NewSDKRegistry returns an empty SDKRegistry ready for concurrent use. The
// zero value of SDKRegistry is also ready for use.
func NewSDKRegistry() *SDKRegistry {
	return &SDKRegistry{clients: make(map[SDKClientKey]sdk.Client), streamClients: make(map[SDKClientKey]sdk.StreamClient)}
}

// Register associates client with the exact SDK kind/protocol pair. A nil
// client is rejected. If the pair is already registered, Register returns a
// typed duplicate error and preserves the original client.
func (r *SDKRegistry) Register(kind adapter.SDKKind, protocol adapter.Protocol, client sdk.Client) error {
	if isNilSDKClient(client) {
		return ErrSDKClientNil
	}

	key := SDKClientKey{SDKKind: kind, Protocol: protocol}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.clients == nil {
		r.clients = make(map[SDKClientKey]sdk.Client)
	}
	if _, exists := r.clients[key]; exists {
		return &DuplicateSDKClientError{Key: key}
	}
	r.clients[key] = client
	return nil
}

// Client returns the client registered for the exact SDK kind/protocol pair.
// It returns a typed unknown-client error when no exact match exists.
func (r *SDKRegistry) Client(kind adapter.SDKKind, protocol adapter.Protocol) (sdk.Client, error) {
	key := SDKClientKey{SDKKind: kind, Protocol: protocol}
	r.mu.RLock()
	client, ok := r.clients[key]
	r.mu.RUnlock()
	if !ok {
		return nil, &UnknownSDKClientError{Key: key}
	}
	return client, nil
}

func isNilSDKClient(client sdk.Client) bool { return isNilInterface(client) }

func isNilStreamClient(client sdk.StreamClient) bool { return isNilInterface(client) }

// isNilInterface rejects nil and typed-nil capability implementations before
// they enter the write-once registry.
func isNilInterface(client any) bool {
	return client == nil || isTypedNil(client)
}

// isTypedNil distinguishes an omitted interface (which some optional
// dependencies deliberately default) from an interface that holds a nil
// concrete pointer and would panic when called.
func isTypedNil(client any) bool {
	if client == nil {
		return false
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// RegisterStream associates a streaming client with the exact SDK
// kind/protocol pair. Complete and Stream are independently registered
// capabilities: registering either never replaces or implies the other.
func (r *SDKRegistry) RegisterStream(kind adapter.SDKKind, protocol adapter.Protocol, client sdk.StreamClient) error {
	if isNilStreamClient(client) {
		return ErrSDKClientNil
	}
	key := SDKClientKey{SDKKind: kind, Protocol: protocol}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.streamClients == nil {
		r.streamClients = make(map[SDKClientKey]sdk.StreamClient)
	}
	if _, exists := r.streamClients[key]; exists {
		return &DuplicateSDKClientError{Key: key}
	}
	r.streamClients[key] = client
	return nil
}

// StreamClient returns the independently registered stream capability for the
// exact SDK kind/protocol pair.
func (r *SDKRegistry) StreamClient(kind adapter.SDKKind, protocol adapter.Protocol) (sdk.StreamClient, error) {
	key := SDKClientKey{SDKKind: kind, Protocol: protocol}
	r.mu.RLock()
	client, ok := r.streamClients[key]
	r.mu.RUnlock()
	if !ok {
		return nil, &UnknownSDKClientError{Key: key}
	}
	return client, nil
}
