package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution/retry"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

var (
	// ErrAttemptSessionUsed means a caller tried to acquire credentials or begin
	// the same actual attempt more than once. A session is deliberately
	// single-use so a credential can never be silently reused for another wire
	// attempt.
	ErrAttemptSessionUsed = errors.New("execution: attempt session already used")
	// ErrStreamCleanup is a safe envelope for a provider-owned source Close
	// panic or error. Provider text is intentionally never wrapped.
	ErrStreamCleanup = errors.New("execution: stream cleanup failed")
)

// AttemptPreparer owns the pure, per-candidate preparation shared by execution
// modes. It validates the resolver-owned Plan once at construction, then each
// Preflight performs Prepare, Engine.Apply, SDK/auth compatibility validation,
// and exact SDK registry lookup. It neither resolves credentials nor invokes an
// SDK.
//
// It is request-local. Its fields are private because the raw request body and
// provider target are execution-only inputs, not observer-facing values.
type AttemptPreparer struct {
	resolver *routing.Resolver
	registry *SDKRegistry
	body     json.RawMessage
	thinking adapter.ThinkingRequest
}

// NewAttemptPreparer validates that plan is a capability issued by resolver.
// This check is intentionally before candidate preparation, credential lookup,
// or any external side effect.
func NewAttemptPreparer(resolver *routing.Resolver, plan routing.Plan, registry *SDKRegistry, body json.RawMessage, thinking adapter.ThinkingRequest) (*AttemptPreparer, error) {
	if resolver == nil || registry == nil {
		return nil, ErrMisconfigured
	}
	if err := resolver.ValidatePlan(plan); err != nil {
		return nil, err
	}
	return &AttemptPreparer{
		resolver: resolver,
		registry: registry,
		body:     append(json.RawMessage(nil), body...),
		thinking: thinking,
	}, nil
}

// PreparedCall is a prepared but credential-free actual-call description. Its
// fields intentionally remain opaque: it carries a target and applied request,
// neither of which may be rendered into logs or errors. Use NewAttemptSession
// to acquire a call-local credential and start one logical attempt.
type PreparedCall struct {
	prepared     routing.PreparedAttempt
	applied      adapter.AppliedRequest
	client       sdk.Client
	streamClient sdk.StreamClient
	resolver     *routing.Resolver
	candidate    routing.Candidate
}

// String, GoString, and Format prevent accidental rendering of an applied
// request or provider target. They are deliberately useful only as a fixed
// diagnostic marker.
func (PreparedCall) String() string   { return "execution.PreparedCall([REDACTED])" }
func (PreparedCall) GoString() string { return "execution.PreparedCall([REDACTED])" }
func (PreparedCall) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("execution.PreparedCall([REDACTED])"))
}

// Preflight returns one independent prepared call for candidate. It has no
// credential, quota, retry-state, or SDK-call side effect.
func (p *AttemptPreparer) Preflight(ctx context.Context, candidate routing.Candidate) (PreparedCall, error) {
	return p.preflight(ctx, candidate, false)
}

// PreflightStream performs the same credential-free preparation as Preflight,
// but requires an independently registered exact streaming capability. It does
// not resolve credentials or open a provider stream.
func (p *AttemptPreparer) PreflightStream(ctx context.Context, candidate routing.Candidate) (PreparedCall, error) {
	return p.preflight(ctx, candidate, true)
}

func (p *AttemptPreparer) preflight(ctx context.Context, candidate routing.Candidate, stream bool) (PreparedCall, error) {
	if p == nil || p.resolver == nil || p.registry == nil {
		return PreparedCall{}, ErrMisconfigured
	}
	if ctx == nil {
		return PreparedCall{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return PreparedCall{}, err
	}
	prepared, err := p.resolver.Prepare(candidate)
	if err != nil {
		return PreparedCall{}, err
	}
	applied, err := (adapter.Engine{}).Apply(ctx, adapter.ApplyInput{
		Adapter:       prepared.Adapter,
		ModelThinking: prepared.ModelThinking,
		Body:          p.body,
		Thinking:      p.thinking,
	})
	if err != nil {
		return PreparedCall{}, err
	}
	if !sdkAuthCompatible(prepared.Adapter.SDKKind, prepared.Adapter.Auth.Kind) {
		return PreparedCall{}, ErrIncompatibleAuth
	}
	call := PreparedCall{prepared: prepared, applied: applied, resolver: p.resolver, candidate: candidate}
	if stream {
		client, err := p.registry.StreamClient(prepared.Adapter.SDKKind, prepared.Adapter.Protocol)
		if err != nil {
			return PreparedCall{}, err
		}
		call.streamClient = client
		return call, nil
	}
	client, err := p.registry.Client(prepared.Adapter.SDKKind, prepared.Adapter.Protocol)
	if err != nil {
		return PreparedCall{}, err
	}
	call.client = client
	return call, nil
}

// preparedAttempt returns a value copy only to orchestration in this package.
// Keeping it unexported preserves PreparedCall as an opaque lifecycle boundary
// for other packages: neither provider target nor applied request can be
// inspected or retained outside execution.
func (p PreparedCall) preparedAttempt() routing.PreparedAttempt { return p.prepared }

// AttemptSession is the single-use transition from a credential-free
// PreparedCall to one actual attempt. It stores no credential material. Execute
// resolves the credential exactly once, immediately begins the retry attempt,
// and makes a revocable resulting sdk.Call available only to its synchronous
// callback. It never calls an SDK itself, so non-stream and future stream
// callers retain their distinct call/complete lifecycles.
type AttemptSession struct {
	prepared    PreparedCall
	state       *retry.State
	policy      adapter.CompiledRetry
	credentials routing.CredentialResolver

	mu   sync.Mutex
	used bool
}

// NewAttemptSession returns a one-use session. The returned session performs
// no credential resolution until Execute is called.
func (p PreparedCall) NewAttemptSession(state *retry.State, policy adapter.CompiledRetry, credentials routing.CredentialResolver) *AttemptSession {
	return &AttemptSession{prepared: p, state: state, policy: policy, credentials: credentials}
}

// Execute acquires the credential and begins exactly one logical attempt before
// synchronously invoking call. It converts the resolver's per-call opaque
// secret into a scoped SDK secret, then revokes it before Execute returns; a
// callback that retains sdk.Call therefore cannot use its Secret after the
// callback returns. A nil callback is rejected before credential resolution or
// BeginAttempt.
func (s *AttemptSession) Execute(ctx context.Context, call func(sdk.Client, sdk.Call)) (retry.Attempt, bool, bool, error) {
	if s == nil || s.prepared.resolver == nil || s.state == nil || call == nil || isTypedNil(s.credentials) {
		return retry.Attempt{}, false, false, ErrMisconfigured
	}
	if ctx == nil {
		return retry.Attempt{}, false, false, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return retry.Attempt{}, false, false, err
	}

	s.mu.Lock()
	if s.used {
		s.mu.Unlock()
		return retry.Attempt{}, false, false, ErrAttemptSessionUsed
	}
	s.used = true
	s.mu.Unlock()

	// Keep resolution adjacent to BeginAttempt: no fallible operation may be
	// inserted between acquiring the per-attempt secret and reserving its
	// logical budget. The callback runs immediately after BeginAttempt.
	resolved, err := s.prepared.resolver.ResolveCredential(ctx, s.prepared.candidate, s.credentials)
	if err != nil {
		return retry.Attempt{}, false, false, err
	}

	// Only a temporary Use copy is made into a scoped secret. Use clears that
	// temporary copy before returning; revoke clears the scoped backing and
	// prevents a retained sdk.Call from accessing it after this callback scope.
	var secret sdk.CredentialSecret
	var revoke func()
	if err := resolved.Use(func(value []byte) error {
		secret, revoke = sdk.NewScopedCredentialSecret(value)
		return nil
	}); err != nil {
		return retry.Attempt{}, false, false, err
	}
	defer revoke()

	attempt, err := s.state.BeginAttempt(ctx, s.prepared.candidate, s.policy)
	if err != nil {
		return retry.Attempt{}, true, false, err
	}
	call(s.prepared.client, sdk.Call{
		Candidate: s.prepared.prepared.Candidate,
		Target:    s.prepared.prepared.Target,
		Request:   s.prepared.applied,
		Secret:    secret,
	})
	return attempt, true, true, nil
}

// ExecuteStream acquires the credential, begins exactly one logical attempt,
// then synchronously invokes open with the stream capability selected by
// PreflightStream. It revokes the scoped secret before returning. The returned
// source, when any, is caller-owned; it must not depend on access to the
// StreamCall secret after open returns. If opening fails with a source, this
// method closes that source before returning to avoid a provider resource leak.
// TTFT and stream lifecycle timing are deliberately not owned by this layer.
func (s *AttemptSession) ExecuteStream(ctx context.Context, open func(sdk.StreamClient, sdk.StreamCall) (sdk.StreamOpen, error)) (retry.Attempt, bool, bool, sdk.StreamOpen, error) {
	if s == nil || s.prepared.resolver == nil || isNilInterface(s.prepared.streamClient) || s.state == nil || open == nil || isTypedNil(s.credentials) {
		return retry.Attempt{}, false, false, sdk.StreamOpen{}, ErrMisconfigured
	}
	if ctx == nil {
		return retry.Attempt{}, false, false, sdk.StreamOpen{}, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return retry.Attempt{}, false, false, sdk.StreamOpen{}, err
	}

	s.mu.Lock()
	if s.used {
		s.mu.Unlock()
		return retry.Attempt{}, false, false, sdk.StreamOpen{}, ErrAttemptSessionUsed
	}
	s.used = true
	s.mu.Unlock()

	resolved, err := s.prepared.resolver.ResolveCredential(ctx, s.prepared.candidate, s.credentials)
	if err != nil {
		return retry.Attempt{}, false, false, sdk.StreamOpen{}, err
	}
	var secret sdk.CredentialSecret
	var revoke func()
	if err := resolved.Use(func(value []byte) error {
		secret, revoke = sdk.NewScopedCredentialSecret(value)
		return nil
	}); err != nil {
		return retry.Attempt{}, false, false, sdk.StreamOpen{}, err
	}
	defer revoke()

	attempt, err := s.state.BeginAttempt(ctx, s.prepared.candidate, s.policy)
	if err != nil {
		return retry.Attempt{}, true, false, sdk.StreamOpen{}, err
	}
	streamCall := sdk.StreamCall{
		Candidate: s.prepared.prepared.Candidate,
		Target:    s.prepared.prepared.Target,
		Request:   s.prepared.applied,
		Secret:    secret,
	}
	opened, err := open(s.prepared.streamClient, streamCall)
	if err != nil {
		if cleanupErr := safeCloseStreamSource(opened.Source); cleanupErr != nil {
			// Preserve an already-classified opening failure for errors.As while
			// never exposing a provider Close error or panic.
			err = errors.Join(err, cleanupErr)
		}
		return attempt, true, true, sdk.StreamOpen{}, err
	}
	if isNilInterface(opened.Source) {
		// An unusable successful opening still owns its source. Close any genuine
		// source and expose only the fixed cleanup sentinel on failure.
		if cleanupErr := safeCloseStreamSource(opened.Source); cleanupErr != nil {
			return attempt, true, true, sdk.StreamOpen{}, errors.Join(ErrMisconfigured, cleanupErr)
		}
		return attempt, true, true, sdk.StreamOpen{}, ErrMisconfigured
	}
	return attempt, true, true, opened, nil
}

// safeCloseStreamSource closes only a genuinely non-nil source. Provider
// implementations are outside this package, so Close failures and panics are
// reduced to one safe sentinel rather than leaking provider-controlled text.
func safeCloseStreamSource(source sdk.StreamSource) (err error) {
	if isNilInterface(source) {
		return nil
	}
	defer func() {
		if recover() != nil {
			err = ErrStreamCleanup
		}
	}()
	if source.Close() != nil {
		return ErrStreamCleanup
	}
	return nil
}
