package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution/retry"
	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

// Runner-local sentinel errors. None of them, nor the errors they wrap on the
// failure path, carry a credential reference, secret, request body, response
// body, or URL: preflight/apply/registry failures surface only the already-safe
// sentinels produced by those packages, and upstream failures surface either a
// safe *sdk.ClassifiedError (category + sanitized code/type only) or one of the
// generic execution sentinels below. RawJSON is only ever returned inside a
// success Result and never reaches an error.
var (
	// ErrMisconfigured means the Runner or Input was not usable. It is returned
	// before any reservation or upstream call.
	ErrMisconfigured = errors.New("execution: runner misconfigured")
	// ErrNoCandidate means the supplied Plan had no candidates. It is returned
	// before any reservation.
	ErrNoCandidate = errors.New("execution: no candidate")
	// ErrInvalidReservation means ReservationID is empty or whitespace-only.
	// It is returned before plan validation, preflight, credential resolution,
	// quota reservation, logging, or an upstream call.
	ErrInvalidReservation = errors.New("execution: invalid reservation id")
	// ErrInvalidRequestID means RequestID is empty or whitespace-only. Like an
	// invalid reservation ID, it is rejected before all request side effects.
	ErrInvalidRequestID = errors.New("execution: invalid request id")
	// ErrUnclassified means an upstream call returned a failure that was not a
	// safe *sdk.ClassifiedError. The Runner fails closed: it releases the
	// reservation and returns this sentinel rather than the raw error, which
	// could carry request/response material.
	ErrUnclassified = errors.New("execution: upstream failure unclassified")
	// ErrBudgetExhausted means the retry State refused to begin another attempt.
	ErrBudgetExhausted = errors.New("execution: retry budget exhausted")
	// ErrIncompatibleAuth means the configured adapter authentication cannot be
	// implemented by the selected official SDK. It is rejected during preflight,
	// before quota reservation, credential use beyond routing preparation, or an
	// SDK call.
	ErrIncompatibleAuth = errors.New("execution: incompatible sdk authentication")
	// ErrTerminalization indicates that a quota terminal operation was attempted
	// but its outcome is unknown. The port error is intentionally never exposed.
	ErrTerminalization = errors.New("execution: terminalization outcome unknown")
	// ErrQuotaReserve indicates that quota reservation could not be confirmed.
	// The quota port error is deliberately not wrapped: it can contain a
	// reservation identifier, provider URL, or other sensitive backend detail.
	ErrQuotaReserve = errors.New("execution: quota reservation failed")
)

// TerminalizationError is the safe envelope for a failed quota terminal call.
// Operation is either "finalize" or "release" and Outcome is always "unknown".
// It deliberately carries neither the reservation ID nor the raw port error.
type TerminalizationError struct {
	Operation string
	Outcome   string
}

func (*TerminalizationError) Error() string { return "execution: terminalization outcome unknown" }

// Is preserves a stable sentinel without unwrapping the raw terminal port error.
func (e *TerminalizationError) Is(target error) bool { return target == ErrTerminalization }

func terminalizationError(operation string) error {
	return &TerminalizationError{Operation: operation, Outcome: "unknown"}
}

// defaultCleanupTimeout bounds a terminal action after the request context has
// already been canceled or exhausted. defaultLogTimeout independently bounds a
// best-effort log write. Both are used only when their Runner field is unset.
const (
	defaultCleanupTimeout = 10 * time.Second
	defaultLogTimeout     = 10 * time.Second
)

// Input is the complete, request-local input for one non-streaming Run. The
// Resolver is the same frozen resolver that produced Plan; Runner re-runs
// Prepare on every attempt. Body is the raw JSON object to be adapted.
type Input struct {
	RequestID     string
	ReservationID string
	Plan          routing.Plan
	Resolver      *routing.Resolver
	Credentials   routing.CredentialResolver
	Body          json.RawMessage
	Thinking      adapter.ThinkingRequest
}

// Result is returned only after the quota terminal action is confirmed and
// Run returns nil error: Completion.RawJSON is populated only after successful
// Finalize, and Failure is populated only after a final classified upstream
// stop is successfully Released. Any preflight, context, unclassified, or
// terminalization-unknown outcome returns the zero Result.
type Result struct {
	Completion sdk.Completion
	Failure    *adapter.MappedResponse
}

// Sleeper pauses for a retry delay while respecting context cancellation. A
// Sleep that observes ctx.Done must return ctx.Err() (or an error wrapping it)
// rather than completing the full delay.
type Sleeper interface {
	Sleep(ctx context.Context, d time.Duration) error
}

// contextSleeper is the default context-aware Sleeper.
type contextSleeper struct{}

func (contextSleeper) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Runner executes one non-streaming request against the upstream SDK selected
// by the routing Plan. It owns the single quota Reserve for the request and the
// Terminalizer constructed only after that Reserve succeeds. Each actual
// attempt re-runs Prepare, Engine.Apply, credential/client lookup, and then
// begins a retry State attempt immediately before the SDK Complete call.
//
// Runner is not safe for concurrent reuse: one Run drives one request lifecycle.
type Runner struct {
	Quota          quota.Port
	SDKRegistry    *SDKRegistry
	Logger         requestlog.ExecutionPort
	Clock          retry.Clock
	Sleeper        Sleeper
	CleanupTimeout time.Duration
	LogTimeout     time.Duration
}

// Run executes the request. On success it finalizes the reservation under a
// cleanup context detached from request cancellation and bounded by
// CleanupTimeout, then records a success log only after finalization succeeds;
// it never Releases after Finalize, even if Finalize itself fails. On any
// failure path after Reserve it Releases under an independent cleanup context
// and joins a safe terminalization uncertainty when Release cannot be confirmed.
// Logs are best-effort and never alter the verdict.
func (r *Runner) Run(ctx context.Context, in Input) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if r == nil || r.Quota == nil || r.SDKRegistry == nil || in.Resolver == nil {
		return Result{}, ErrMisconfigured
	}
	// Reject malformed request identifiers before every preflight or quota side
	// effect. Preserve IDs verbatim once valid; trimming is validation only.
	if strings.TrimSpace(in.ReservationID) == "" {
		return Result{}, ErrInvalidReservation
	}
	if strings.TrimSpace(in.RequestID) == "" {
		return Result{}, ErrInvalidRequestID
	}
	if len(in.Plan.Candidates) == 0 {
		return Result{}, ErrNoCandidate
	}
	// A Plan is a resolver-bound capability, not caller-owned input. Validate it
	// before Prepare, credential resolution, or Reserve.
	if err := in.Resolver.ValidatePlan(in.Plan); err != nil {
		return Result{}, err
	}

	first := in.Plan.Candidates[0]

	// Preflight the first plan candidate before a single Reserve: Prepare,
	// Engine.Apply, SDK client lookup, SDK/auth compatibility, and a positive
	// request timeout must all succeed before the Runner commits quota. Its
	// effective retry policy is frozen for the entire request: fallback routes
	// may have different configured policies, but they cannot widen, narrow, or
	// otherwise change the attempt budget after this point.
	firstPrepared, _, _, err := r.preflight(ctx, in, first)
	if err != nil {
		return Result{}, err
	}
	if firstPrepared.Timeout.Request <= 0 {
		return Result{}, ErrMisconfigured
	}
	retryPolicy := firstPrepared.Retry
	state := retry.NewState(in.Plan, r.Clock)

	if _, err := r.Quota.Reserve(ctx, in.ReservationID); err != nil {
		// Reserve is the only pre-terminal quota operation. Its raw failure is
		// backend-owned and must not cross the execution boundary.
		return Result{}, ErrQuotaReserve
	}
	terminalizer := NewTerminalizer(r.Quota, in.ReservationID)

	candidate := first
	attemptNo := 0
	for {
		attemptNo++
		prepared, applied, client, err := r.preflight(ctx, in, candidate)
		if err != nil {
			// Per-attempt preflight failure after Reserve: retain its safe primary
			// error, while reporting an uncertain Release separately if necessary.
			if cerr := ctx.Err(); cerr != nil {
				err = cerr
			}
			return Result{}, r.releaseFailure(ctx, terminalizer, err)
		}
		if prepared.Timeout.Request <= 0 {
			return Result{}, r.releaseFailure(ctx, terminalizer, ErrMisconfigured)
		}
		// Resolve exactly once per actual attempt, after its pure preflight and
		// before BeginAttempt so no secret resolution separates the attempt budget
		// reservation from the wire call.
		secret, err := in.Resolver.ResolveCredential(ctx, candidate, in.Credentials)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				err = cerr
			}
			return Result{}, r.releaseFailure(ctx, terminalizer, err)
		}

		// BeginAttempt is immediately adjacent to Complete: nothing between
		// them may fail the request, so the logical attempt and the wire call
		// start from the same budget reservation.
		attempt, err := state.BeginAttempt(ctx, candidate, retryPolicy)
		if err != nil {
			primary := error(ErrBudgetExhausted)
			if cerr := ctx.Err(); cerr != nil {
				primary = cerr
			}
			return Result{}, r.releaseFailure(ctx, terminalizer, primary)
		}

		// Each SDK invocation has the route's compiled request timeout, bounded
		// by the caller context. Provider adapters classify deadline expiry as a
		// safe timeout; always cancel promptly to release the timer.
		attemptCtx, attemptCancel := context.WithTimeout(ctx, prepared.Timeout.Request)
		completion, err := client.Complete(attemptCtx, sdk.Call{
			Candidate: prepared.Candidate,
			Target:    prepared.Target,
			Request:   applied,
			Secret:    secret,
		})
		attemptCancel()

		// The caller's lifecycle always wins once Complete has returned. In
		// particular, a parent deadline must not be reclassified as an upstream
		// timeout merely because attemptCtx inherited that deadline. Do this
		// before inspecting either completion or upstream error so cancellation
		// racing with a provider result cannot enter response mapping or retry.
		if parentErr := ctx.Err(); parentErr != nil {
			_ = state.Cancel()
			r.logFailure(ctx, in, prepared, attemptNo, nil, adapter.MappedResponse{}, retry.Decision{})
			return Result{}, r.releaseFailure(ctx, terminalizer, parentErr)
		}
		if err == nil {
			// Success is irreversible: RecordSuccess and Commit freeze the
			// verdict before any terminal action. Finalize is attempted on a
			// cleanup context detached from request cancellation and bounded by
			// CleanupTimeout, mirroring the failure Release path, so a stuck or
			// slow Finalize cannot be stranded by request cancellation. Its
			// failure never triggers a compensating Release (no opposite intent),
			// and the success Result is still returned. The success log is
			// recorded after the Finalize attempt; a recording error is
			// intentionally ignored and never changes the committed verdict.
			_ = state.RecordSuccess(ctx, attempt)
			_ = state.Commit()
			cleanupCtx, cleanupCancel := r.cleanupContext(ctx)
			ferr := terminalizer.Finalize(cleanupCtx)
			cleanupCancel()
			if ferr != nil {
				// Terminal state is unknown. Do not Release, log success, or return a
				// Completion that callers could mistake for a confirmed outcome.
				return Result{}, terminalizationError("finalize")
			}
			r.logSuccess(ctx, in, prepared, attemptNo)
			return Result{Completion: completion}, nil
		}

		// Cancellation is surfaced as the context sentinel rather than as an
		// upstream failure. The parent context was checked immediately after
		// Complete above, so a DeadlineExceeded here can only be the live
		// attempt context (or a provider timeout) and is safe to classify.
		if errors.Is(err, context.Canceled) {
			_ = state.Cancel()
			r.logFailure(ctx, in, prepared, attemptNo, nil, adapter.MappedResponse{}, retry.Decision{})
			return Result{}, r.releaseFailure(ctx, terminalizer, context.Canceled)
		}

		var classified *sdk.ClassifiedError
		if errors.Is(err, context.DeadlineExceeded) {
			// Provider SDKs normally return this classification themselves. Keep
			// the runner boundary safe for compliant clients that return their
			// attempt context's deadline directly.
			// Preserve the retry-visible timeout identifiers as well as the
			// context deadline classification. retry.State matches ClassifiedError
			// metadata directly, while response mapping uses the same safe values.
			classified = sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "timeout", "timeout")
		}
		if classified == nil && (!errors.As(err, &classified) || classified == nil) {
			// Unclassified failures are fail-closed: never retry, release, and
			// return a generic sentinel so the raw error cannot leak.
			_ = state.Cancel()
			r.logFailure(ctx, in, prepared, attemptNo, nil, adapter.MappedResponse{}, retry.Decision{})
			return Result{}, r.releaseFailure(ctx, terminalizer, ErrUnclassified)
		}

		decision, ferr := state.RecordFailure(ctx, attempt, retry.Failure{Classified: classified}, retryPolicy)
		if ferr != nil {
			_ = state.Cancel()
			primary := error(ErrBudgetExhausted)
			if cerr := ctx.Err(); cerr != nil {
				primary = cerr
			}
			return Result{}, r.releaseFailure(ctx, terminalizer, primary)
		}
		// The attempt log is best-effort: a recording fault never changes the
		// verdict or the retry decision already recorded by the State.
		mapped := (adapter.Engine{}).MapResponse(prepared.Adapter, classified.ToUpstreamResponse())
		r.logFailure(ctx, in, prepared, attemptNo, classified, mapped, decision)

		if !decision.Retry() {
			// The mapped Failure is a confirmed terminal Result only after Release
			// succeeds. If Release is unknown, retain the safe classified primary in
			// the error but return no Result.
			if err := r.releaseCleanup(ctx, terminalizer); err != nil {
				return Result{}, errors.Join(classified, terminalizationError("release"))
			}
			return Result{Failure: &mapped}, nil
		}

		if err := r.sleeper().Sleep(ctx, decision.Delay); err != nil {
			_ = state.Cancel()
			if cerr := ctx.Err(); cerr != nil {
				err = cerr
			}
			return Result{}, r.releaseFailure(ctx, terminalizer, err)
		}
		candidate = decision.Candidate
	}
}

// preflight pins the candidate, applies the request, verifies SDK auth, and
// looks up the SDK client. It is deliberately pure with respect to credentials:
// it must not resolve a secret and is safe to run before quota Reserve.
func (r *Runner) preflight(ctx context.Context, in Input, candidate routing.Candidate) (routing.PreparedAttempt, adapter.AppliedRequest, sdk.Client, error) {
	prepared, err := in.Resolver.Prepare(candidate)
	if err != nil {
		return routing.PreparedAttempt{}, adapter.AppliedRequest{}, nil, err
	}
	engine := adapter.Engine{}
	applied, err := engine.Apply(ctx, adapter.ApplyInput{
		Adapter:       prepared.Adapter,
		ModelThinking: prepared.ModelThinking,
		Body:          in.Body,
		Thinking:      in.Thinking,
	})
	if err != nil {
		return routing.PreparedAttempt{}, adapter.AppliedRequest{}, nil, err
	}
	if !sdkAuthCompatible(prepared.Adapter.SDKKind, prepared.Adapter.Auth.Kind) {
		return routing.PreparedAttempt{}, adapter.AppliedRequest{}, nil, ErrIncompatibleAuth
	}
	client, err := r.SDKRegistry.Client(prepared.Adapter.SDKKind, prepared.Adapter.Protocol)
	if err != nil {
		return routing.PreparedAttempt{}, adapter.AppliedRequest{}, nil, err
	}
	return prepared, applied, client, nil
}

// sdkAuthCompatible constrains official SDKs to their sole credential channel.
// Generic SDK kinds deliberately remain registry-governed until a concrete
// implementation declares its own compatibility contract.
func sdkAuthCompatible(kind adapter.SDKKind, auth adapter.AuthKind) bool {
	switch kind {
	case adapter.SDKKindOpenAI:
		return auth == adapter.AuthBearerHeader
	case adapter.SDKKindAnthropic:
		return auth == adapter.AuthAPIKeyHeader
	default:
		return true
	}
}

// cleanupContext returns an independent context whose lifetime is bounded by
// CleanupTimeout and which is detached from request cancellation. It is used
// for terminal actions (Finalize on success, Release on failure) so a stuck or
// slow cleanup cannot be stranded by request cancellation nor block a request
// goroutine indefinitely. The caller owns the returned cancel function.
func (r *Runner) cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.CleanupTimeout
	if timeout <= 0 {
		timeout = defaultCleanupTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

// releaseCleanup Releases under an independent cleanup context and returns the
// terminal call result. Its caller must envelope any non-nil result; raw quota
// port errors are never suitable to expose outside this package.
func (r *Runner) releaseCleanup(ctx context.Context, terminalizer *Terminalizer) error {
	if terminalizer == nil {
		return ErrTerminalConflict
	}
	cleanupCtx, cancel := r.cleanupContext(ctx)
	defer cancel()
	return terminalizer.Release(cleanupCtx)
}

// releaseFailure retains the safe primary verdict and adds a safe uncertainty
// envelope when the one required post-reserve Release cannot be confirmed.
func (r *Runner) releaseFailure(ctx context.Context, terminalizer *Terminalizer, primary error) error {
	if err := r.releaseCleanup(ctx, terminalizer); err != nil {
		return errors.Join(primary, terminalizationError("release"))
	}
	return primary
}

// logContext returns a live, bounded context even when the request was
// canceled. Logging is observational only, so it must neither inherit request
// cancellation nor extend indefinitely.
func (r *Runner) logContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := r.LogTimeout
	if timeout <= 0 {
		timeout = defaultLogTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (r *Runner) sleeper() Sleeper {
	if r.Sleeper != nil {
		return r.Sleeper
	}
	return contextSleeper{}
}

func (r *Runner) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now()
}

func (r *Runner) baseEvent(in Input, prepared routing.PreparedAttempt, attemptNo int) requestlog.ExecutionEvent {
	return requestlog.ExecutionEvent{
		RequestID:     in.RequestID,
		ReservationID: in.ReservationID,
		Revision:      prepared.Revision,
		Generation:    prepared.Generation,
		Attempt:       attemptNo,
		Candidate: requestlog.ExecutionCandidate{
			ModelID:      prepared.Candidate.ModelID,
			ProviderID:   prepared.Candidate.ProviderID,
			RouteID:      prepared.Candidate.RouteID,
			CredentialID: prepared.Candidate.CredentialID,
			AdapterID:    prepared.Candidate.AdapterID,
		},
		Protocol:  string(prepared.Target.Protocol),
		Kind:      "attempt",
		Timestamp: r.now(),
	}
}

func (r *Runner) logSuccess(ctx context.Context, in Input, prepared routing.PreparedAttempt, attemptNo int) {
	if r.Logger == nil {
		return
	}
	event := r.baseEvent(in, prepared, attemptNo)
	event.Status = "success"
	// Logs never alter the verdict: a recording error is intentionally ignored.
	logCtx, cancel := r.logContext(ctx)
	defer cancel()
	_ = r.Logger.RecordExecution(logCtx, event)
}

func (r *Runner) logFailure(ctx context.Context, in Input, prepared routing.PreparedAttempt, attemptNo int, classified *sdk.ClassifiedError, mapped adapter.MappedResponse, decision retry.Decision) {
	if r.Logger == nil {
		return
	}
	event := r.baseEvent(in, prepared, attemptNo)
	event.Status = "failed"
	// Record adapter-mapped public error identifiers rather than raw upstream
	// metadata. ExecutionEvent has no numeric-status field; its Status remains
	// the attempt outcome, while Result.Failure carries mapped HTTPStatus.
	if classified != nil {
		event.Code = mapped.ErrorCode
		event.Type = mapped.ErrorType
	}
	event.RuleID = decision.RuleID
	event.Action = string(decision.Action)
	// Logs never alter the verdict: a recording error is intentionally ignored.
	logCtx, cancel := r.logContext(ctx)
	defer cancel()
	_ = r.Logger.RecordExecution(logCtx, event)
}
