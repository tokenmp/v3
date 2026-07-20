// Package adapter defines the data-driven, finite rule types used to adapt
// Executor requests to upstream providers.
package adapter

// SDKKind identifies the controlled upstream client implementation.
type SDKKind string

const (
	SDKKindOpenAI      SDKKind = "openai"
	SDKKindAnthropic   SDKKind = "anthropic"
	SDKKindGenericHTTP SDKKind = "generic_http"
)

// Valid reports whether k is a supported SDK kind.
func (k SDKKind) Valid() bool {
	switch k {
	case SDKKindOpenAI, SDKKindAnthropic, SDKKindGenericHTTP:
		return true
	default:
		return false
	}
}

// Protocol identifies an externally visible request/response protocol.
type Protocol string

const (
	ProtocolOpenAIChat      Protocol = "openai_chat"
	ProtocolOpenAIResponses Protocol = "openai_responses"
	ProtocolAnthropic       Protocol = "anthropic_messages"
	ProtocolOpenAIImages    Protocol = "openai_images"
)

// Valid reports whether p is a supported protocol.
func (p Protocol) Valid() bool {
	switch p {
	case ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolAnthropic, ProtocolOpenAIImages:
		return true
	default:
		return false
	}
}

// RetryAction selects the next candidate scope after a failed attempt.
type RetryAction string

const (
	RetryNone           RetryAction = "none"
	RetrySameCredential RetryAction = "same_credential"
	RetryNextCredential RetryAction = "next_credential"
	RetryNextRoute      RetryAction = "next_route"
	RetryNextProvider   RetryAction = "next_provider"
	RetryNextModel      RetryAction = "next_model"
)

// Valid reports whether a is a supported retry action.
func (a RetryAction) Valid() bool {
	switch a {
	case RetryNone, RetrySameCredential, RetryNextCredential, RetryNextRoute, RetryNextProvider, RetryNextModel:
		return true
	default:
		return false
	}
}

// RawDuration is an unparsed duration from a configuration snapshot, such as
// "45s". It is deliberately distinct from time.Duration: compilation parses
// it into an effective duration after applying defaults and hard limits.
type RawDuration string

// AuthKind limits the authentication mechanisms an adapter can configure.
type AuthKind string

const (
	AuthNone         AuthKind = "none"
	AuthBearerHeader AuthKind = "bearer_header"
	AuthAPIKeyHeader AuthKind = "api_key_header"
	AuthAPIKeyQuery  AuthKind = "api_key_query"
)

// Valid reports whether k is a supported authentication mechanism.
func (k AuthKind) Valid() bool {
	switch k {
	case AuthNone, AuthBearerHeader, AuthAPIKeyHeader, AuthAPIKeyQuery:
		return true
	default:
		return false
	}
}

// Capability is a feature which may be required or forbidden by an adapter.
type Capability string

const (
	CapabilityChat      Capability = "chat"
	CapabilityResponses Capability = "responses"
	CapabilityMessages  Capability = "messages"
	CapabilityImages    Capability = "images"
	CapabilityStreaming Capability = "streaming"
	CapabilityTools     Capability = "tools"
	CapabilityVision    Capability = "vision"
	CapabilityThinking  Capability = "thinking"
)

// Valid reports whether c is a known capability.
func (c Capability) Valid() bool {
	switch c {
	case CapabilityChat, CapabilityResponses, CapabilityMessages, CapabilityImages, CapabilityStreaming, CapabilityTools, CapabilityVision, CapabilityThinking:
		return true
	default:
		return false
	}
}

// ThinkingEffort is the finite normalized reasoning effort scale.
type ThinkingEffort string

const (
	ThinkingNone    ThinkingEffort = "none"
	ThinkingMinimal ThinkingEffort = "minimal"
	ThinkingLow     ThinkingEffort = "low"
	ThinkingMedium  ThinkingEffort = "medium"
	ThinkingHigh    ThinkingEffort = "high"
	ThinkingXHigh   ThinkingEffort = "xhigh"
	ThinkingMax     ThinkingEffort = "max"
)

// Valid reports whether e is a known normalized thinking effort.
func (e ThinkingEffort) Valid() bool {
	switch e {
	case ThinkingNone, ThinkingMinimal, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingXHigh, ThinkingMax:
		return true
	default:
		return false
	}
}

// RequestAction is a permitted, non-Turing-complete request transformation.
type RequestAction string

const (
	RequestSet         RequestAction = "set"
	RequestCopy        RequestAction = "copy"
	RequestRemove      RequestAction = "remove"
	RequestRename      RequestAction = "rename"
	RequestMapEnum     RequestAction = "map_enum"
	RequestClampNumber RequestAction = "clamp_number"
	RequestSetHeader   RequestAction = "set_header"
	RequestSetQuery    RequestAction = "set_query"
)

// Valid reports whether a is a permitted request action.
func (a RequestAction) Valid() bool {
	switch a {
	case RequestSet, RequestCopy, RequestRemove, RequestRename, RequestMapEnum, RequestClampNumber, RequestSetHeader, RequestSetQuery:
		return true
	default:
		return false
	}
}
