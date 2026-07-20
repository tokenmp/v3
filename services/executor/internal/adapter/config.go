package adapter

import "encoding/json"

// AdapterConfig describes one versioned upstream adaptation strategy. It is a
// raw configuration value; validation and compilation are intentionally kept
// out of this package.
type AdapterConfig struct {
	ID       string
	Name     string
	Version  int
	SDKKind  SDKKind
	Protocol Protocol

	Auth       AuthRule
	Capability CapabilityPolicy
	Thinking   ThinkingPolicy
	Request    RequestPolicy
	Response   ResponsePolicy
	Retry      RetryPolicy
	Timeout    TimeoutPolicy
}

// AuthRule describes how a selected credential is placed into an upstream
// request. CredentialRef identifies a resolver-owned reference, never a secret.
type AuthRule struct {
	Kind          AuthKind
	Header        string
	Query         string
	Prefix        string
	CredentialRef string
}

// CapabilityPolicy declares normalized capabilities required and denied by an
// adapter. Compilation rejects contradictory declarations.
type CapabilityPolicy struct {
	Require []Capability
	Deny    []Capability
}

// ThinkingPolicy maps normalized thinking inputs to a provider's finite scale.
type ThinkingPolicy struct {
	Supported      bool
	DefaultEffort  ThinkingEffort
	EffortMapping  map[ThinkingEffort]ThinkingEffort
	BudgetMapping  map[ThinkingEffort]int
	MinBudgetToken int
	MaxBudgetToken int
}

// RequestPolicy contains only declarative transformations. AllowedHeaders and
// AllowedQuery are the allowlists used when compiling header/query actions.
type RequestPolicy struct {
	AllowedHeaders []string
	AllowedQuery   []string
	Rules          []RequestRule
}

// RequestRule is one finite request transformation. Fields not used by Action
// are ignored by the compiler; semantic validation enforces the required ones.
type RequestRule struct {
	ID      string
	Action  RequestAction
	Path    string
	From    string
	To      string
	Value   json.RawMessage
	EnumMap map[string]string
	Min     *float64
	Max     *float64
	Name    string
	// ValueRef is reserved for a future resolver integration. Compilation
	// rejects every non-empty value until that integration is implemented.
	ValueRef string
}

// ResponsePolicy maps selected upstream outcomes to protocol-safe responses.
type ResponsePolicy struct {
	Rules []ResponseRule
}

// ResponseRule matches a bounded set of upstream attributes and emits a
// sanitized response. Its priority is resolved during compilation.
type ResponseRule struct {
	ID       string
	Priority int
	Match    ResponseMatch
	Output   ResponseOutput
}

// ResponseMatch limits matching to known response metadata; it never exposes
// arbitrary upstream bodies.
type ResponseMatch struct {
	HTTPStatuses     []int
	ErrorCodes       []string
	ErrorTypes       []string
	MessageContains  []string
	FinishReasons    []string
	StreamEventTypes []string
}

// ResponseOutput is the protocol-neutral, sanitized result of a response rule.
type ResponseOutput struct {
	HTTPStatus int
	ErrorCode  string
	ErrorType  string
	Message    string
}

// RetryPolicy controls bounded retry decisions. Nil numeric fields mean the
// layer does not override a lower-precedence value; a non-nil zero explicitly
// disables the corresponding retry limit.
type RetryPolicy struct {
	MaxTotalAttempts      *int
	MaxSameTargetAttempts *int
	MaxTotalDuration      RawDuration
	Backoff               RawDuration
	Rules                 []RetryRule
}

// RetryRule maps a classified upstream failure to a candidate-selection action.
type RetryRule struct {
	ID           string
	Priority     int
	HTTPStatuses []int
	ErrorCodes   []string
	ErrorTypes   []string
	Action       RetryAction
}

// TimeoutPolicy contains raw duration strings. Empty fields inherit from the
// lower-precedence layer; compilation produces time.Duration effective values.
type TimeoutPolicy struct {
	RequestTimeout    RawDuration
	TTFTTimeout       RawDuration
	StreamIdleTimeout RawDuration
	StreamMaxLifetime RawDuration
	RetryBackoff      RawDuration
}
