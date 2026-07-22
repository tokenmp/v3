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
	"github.com/tokenmp/v3/services/executor/internal/requestid"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// StreamInput is the secret-free request-local input for one streaming run.
// Sink owns protocol rendering; the driver owns neither HTTP nor SSE framing.
type StreamInput struct {
	RequestID     string
	ReservationID string
	Plan          routing.Plan
	Resolver      *routing.Resolver
	Credentials   routing.CredentialResolver
	Body          json.RawMessage
	Thinking      adapter.ThinkingRequest
	Sink          ProtocolSink
}

// StreamResult is returned only after the selected quota terminal operation is
// confirmed. It contains the transport-neutral, safe streaming outcome.
type StreamResult struct {
	Outcome streaming.Outcome
	Failure *adapter.MappedResponse
}

// StreamDriver performs request-local streaming orchestration. It reserves
// quota once, retries only failures before Bridge commit, and never owns
// transport or runtime wiring. It is not safe for concurrent reuse.
type StreamDriver struct {
	Quota          quota.Port
	SDKRegistry    *SDKRegistry
	Clock          retry.Clock
	Sleeper        Sleeper
	CleanupTimeout time.Duration
	LogTimeout     time.Duration
	Logger         requestlog.ExecutionPort
}

// Run opens and bridges one stream. Parent cancellation always wins over an
// upstream result. The opening attempt context deliberately remains live until
// Bridge.Run returns, so its request deadline covers opening and the stream.
func (d *StreamDriver) Run(ctx context.Context, in StreamInput) (StreamResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return StreamResult{}, err
	}
	// Nil clock/sleeper use safe defaults, but typed-nil interfaces would panic
	// on use. Credentials may be nil only for AuthNone routes; typed-nil is
	// never a valid resolver.
	if d == nil || isNilInterface(d.Quota) || d.SDKRegistry == nil || isNilInterface(d.Logger) ||
		isTypedNil(d.Clock) || isTypedNil(d.Sleeper) || isTypedNil(in.Credentials) ||
		in.Resolver == nil || isNilStreamPayloadInterface(in.Sink) {
		return StreamResult{}, ErrMisconfigured
	}
	if !requestid.ValidReservationID(in.ReservationID) {
		return StreamResult{}, ErrInvalidReservation
	}
	if strings.TrimSpace(in.RequestID) == "" {
		return StreamResult{}, ErrInvalidRequestID
	}
	if len(in.Plan.Candidates) == 0 {
		return StreamResult{}, ErrNoCandidate
	}
	// Constructing the preparer validates this is a resolver-issued plan before
	// either quota or credential side effects.
	preparer, err := NewAttemptPreparer(in.Resolver, in.Plan, d.SDKRegistry, in.Body, in.Thinking)
	if err != nil {
		return StreamResult{}, err
	}
	first := in.Plan.Candidates[0]
	firstCall, err := preparer.PreflightStream(ctx, first)
	if err != nil {
		return StreamResult{}, err
	}
	firstPrepared := firstCall.preparedAttempt()
	if !validStreamTimeouts(firstPrepared) {
		return StreamResult{}, ErrMisconfigured
	}
	policy := firstPrepared.Retry
	state := retry.NewState(in.Plan, d.Clock)
	if _, err := d.Quota.Reserve(ctx, in.ReservationID); err != nil {
		return StreamResult{}, ErrQuotaReserve
	}
	terminalizer := NewTerminalizer(d.Quota, in.ReservationID)

	candidate := first
	attemptNo := 0
	for {
		preparedCall, err := preparer.PreflightStream(ctx, candidate)
		if err != nil {
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, parentError(ctx, err))
		}
		prepared := preparedCall.preparedAttempt()
		if !validStreamTimeouts(prepared) {
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, ErrMisconfigured)
		}

		attemptNo++
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, prepared.Timeout.Request)
		attempt, acquired, began, opened, openErr := preparedCall.NewAttemptSession(state, policy, in.Credentials).ExecuteStream(attemptCtx, func(client sdk.StreamClient, call sdk.StreamCall) (sdk.StreamOpen, error) {
			return client.Stream(attemptCtx, call)
		})
		if openErr != nil {
			cancelAttempt()
			if !acquired {
				return StreamResult{}, d.releaseFailure(ctx, terminalizer, parentError(ctx, openErr))
			}
			if !began {
				return StreamResult{}, d.releaseFailure(ctx, terminalizer, parentError(ctx, ErrBudgetExhausted))
			}
			if err := ctx.Err(); err != nil {
				_ = state.Cancel()
				return StreamResult{}, d.releaseFailure(ctx, terminalizer, err)
			}
			if errors.Is(openErr, context.Canceled) {
				_ = state.Cancel()
				return StreamResult{}, d.releaseFailure(ctx, terminalizer, context.Canceled)
			}
			classified := classifyStreamError(openErr)
			if classified == nil {
				d.logFailure(ctx, in, prepared, attemptNo, nil, retry.Decision{})
				_ = state.Cancel()
				if errors.Is(openErr, ErrMisconfigured) {
					return StreamResult{}, d.releaseFailure(ctx, terminalizer, ErrMisconfigured)
				}
				return StreamResult{}, d.releaseFailure(ctx, terminalizer, ErrUnclassified)
			}
			if result, next, done, err := d.retryPrecommit(ctx, terminalizer, state, attempt, policy, prepared, classified, in, attemptNo); done {
				return result, err
			} else {
				candidate = next
				continue
			}
		}
		if isNilInterface(opened.Source) {
			cancelAttempt()
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, ErrMisconfigured)
		}
		source, sink, err := newSDKPayloadSource(opened.Source, in.Sink)
		if err != nil {
			// newSDKPayloadSource rejected an otherwise opened provider source.
			// It is still ours to close; never expose provider cleanup details.
			cleanupErr := safeCloseStreamSource(opened.Source)
			cancelAttempt()
			_ = state.Cancel()
			primary := error(ErrMisconfigured)
			if cleanupErr != nil {
				primary = errors.Join(primary, cleanupErr)
			}
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, primary)
		}
		bridge := streaming.Bridge{Source: source, Sink: sink, Timeouts: streamTimeouts(prepared)}
		outcome, bridgeErr := bridge.Run(attemptCtx)
		sink.Discard()
		cancelAttempt() // only after Bridge has closed/drained its source.

		// Bridge may return a committed outcome immediately before parent
		// cancellation. Recheck before every state/log/quota side effect: caller
		// cancellation wins and returns a zero result, but committed usage still
		// determines its terminal quota intent.
		if outcome.Committed {
			if parentErr := ctx.Err(); parentErr != nil {
				_ = state.Cancel()
				if outcome.UnresolvedCost {
					if err := d.releaseCleanup(ctx, terminalizer); err != nil {
						d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
						return StreamResult{}, errors.Join(parentErr, terminalizationError("release"))
					}
				} else if err := d.finalizeCleanup(ctx, terminalizer); err != nil {
					d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
					return StreamResult{}, errors.Join(parentErr, terminalizationError("finalize"))
				}
				d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
				return StreamResult{}, parentErr
			}
			_ = state.Commit()
			if outcome.State == streaming.StateCompleted {
				_ = state.RecordSuccess(ctx, attempt)
			}
			if outcome.UnresolvedCost {
				if err := d.releaseCleanup(ctx, terminalizer); err != nil {
					d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
					return StreamResult{}, terminalizationError("release")
				}
				// A completed stream with unresolved cost was released, not a
				// confirmed successful terminal operation.
				d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
				return StreamResult{Outcome: outcome}, nil
			}
			if err := d.finalizeCleanup(ctx, terminalizer); err != nil {
				d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
				return StreamResult{}, terminalizationError("finalize")
			}
			if outcome.State == streaming.StateCompleted {
				d.logSuccess(ctx, in, prepared, attemptNo)
			} else {
				d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
			}
			return StreamResult{Outcome: outcome}, nil
		}
		if err := ctx.Err(); err != nil {
			d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, err)
		}
		// Bridge reports any context cancellation as StateClientCancelled. A
		// parent cancellation already won above; an attempt-only deadline remains
		// a safe timeout classification and may use the frozen retry policy.
		if bridgeErr == nil && outcome.State == streaming.StateClientCancelled && !errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			d.logFailure(ctx, in, prepared, attemptNo, source.LastClassified(), retry.Decision{})
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, context.Canceled)
		}
		classified := classifyBridgeError(bridgeErr, source.LastClassified(), attemptCtx.Err())
		if classified == nil {
			d.logFailure(ctx, in, prepared, attemptNo, nil, retry.Decision{})
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailure(ctx, terminalizer, ErrUnclassified)
		}
		if result, next, done, err := d.retryPrecommit(ctx, terminalizer, state, attempt, policy, prepared, classified, in, attemptNo); done {
			return result, err
		} else {
			candidate = next
		}
	}
}

func (d *StreamDriver) retryPrecommit(ctx context.Context, terminalizer *Terminalizer, state *retry.State, attempt retry.Attempt, policy adapter.CompiledRetry, prepared routing.PreparedAttempt, classified *sdk.ClassifiedError, in StreamInput, attemptNo int) (StreamResult, routing.Candidate, bool, error) {
	decision, err := state.RecordFailure(ctx, attempt, retry.Failure{Classified: classified}, policy)
	if err != nil {
		_ = state.Cancel()
		resultErr := d.releaseFailure(ctx, terminalizer, parentError(ctx, ErrBudgetExhausted))
		// This is terminal (or safely terminalization-unknown), so logging may
		// record failure but never claims a confirmed successful outcome.
		d.logFailure(ctx, in, prepared, attemptNo, classified, retry.Decision{})
		return StreamResult{}, routing.Candidate{}, true, resultErr
	}
	if !decision.Retry() {
		mapped := (adapter.Engine{}).MapResponse(prepared.Adapter, classified.ToUpstreamResponse())
		if err := d.releaseCleanup(ctx, terminalizer); err != nil {
			d.logFailure(ctx, in, prepared, attemptNo, classified, decision)
			return StreamResult{}, routing.Candidate{}, true, errors.Join(classified, terminalizationError("release"))
		}
		d.logFailure(ctx, in, prepared, attemptNo, classified, decision)
		return StreamResult{Failure: &mapped}, routing.Candidate{}, true, nil
	}
	// A retry decision is an attempt observation, not a terminal outcome.
	d.logFailure(ctx, in, prepared, attemptNo, classified, decision)
	if err := d.sleeper().Sleep(ctx, decision.Delay); err != nil {
		_ = state.Cancel()
		resultErr := d.releaseFailure(ctx, terminalizer, parentError(ctx, err))
		d.logFailure(ctx, in, prepared, attemptNo, classified, retry.Decision{})
		return StreamResult{}, routing.Candidate{}, true, resultErr
	}
	return StreamResult{}, decision.Candidate, false, nil
}

func validStreamTimeouts(prepared routing.PreparedAttempt) bool {
	return prepared.Timeout.Request > 0 && streamTimeouts(prepared).Validate()
}
func streamTimeouts(prepared routing.PreparedAttempt) streaming.Timeouts {
	return streaming.Timeouts{TTFT: prepared.Timeout.TTFT, StreamIdle: prepared.Timeout.StreamIdle, StreamLifetime: prepared.Timeout.StreamMaxLifetime}
}
func classifyStreamError(err error) *sdk.ClassifiedError {
	var classified *sdk.ClassifiedError
	if errors.As(err, &classified) && classified != nil {
		return classified
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "timeout", "timeout")
	}
	return nil
}
func classifyBridgeError(err error, upstream *sdk.ClassifiedError, attemptErr error) *sdk.ClassifiedError {
	if upstream != nil {
		return upstream
	}
	if errors.Is(err, streaming.ErrTTFTTimeout) || errors.Is(attemptErr, context.DeadlineExceeded) {
		return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "timeout", "timeout")
	}
	return nil
}
func parentError(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}
func (d *StreamDriver) cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := d.CleanupTimeout
	if timeout <= 0 {
		timeout = defaultCleanupTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}
func (d *StreamDriver) releaseCleanup(ctx context.Context, t *Terminalizer) error {
	if t == nil {
		return ErrTerminalConflict
	}
	cleanup, cancel := d.cleanupContext(ctx)
	defer cancel()
	return t.Release(cleanup)
}
func (d *StreamDriver) finalizeCleanup(ctx context.Context, t *Terminalizer) error {
	if t == nil {
		return ErrTerminalConflict
	}
	cleanup, cancel := d.cleanupContext(ctx)
	defer cancel()
	return t.Finalize(cleanup)
}
func (d *StreamDriver) releaseFailure(ctx context.Context, t *Terminalizer, primary error) error {
	if err := d.releaseCleanup(ctx, t); err != nil {
		return errors.Join(primary, terminalizationError("release"))
	}
	return primary
}
func (d *StreamDriver) logContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := d.LogTimeout
	if timeout <= 0 {
		timeout = defaultLogTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (d *StreamDriver) now() time.Time {
	if !isNilInterface(d.Clock) {
		return d.Clock.Now()
	}
	return time.Now()
}

func (d *StreamDriver) logEvent(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, status string, classified *sdk.ClassifiedError, decision retry.Decision) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := requestlog.ExecutionEvent{
		RequestID: in.RequestID, ReservationID: in.ReservationID,
		Revision: prepared.Revision, Generation: prepared.Generation, Attempt: attemptNo,
		Candidate: requestlog.ExecutionCandidate{ModelID: prepared.Candidate.ModelID, ProviderID: prepared.Candidate.ProviderID, RouteID: prepared.Candidate.RouteID, CredentialID: prepared.Candidate.CredentialID, AdapterID: prepared.Candidate.AdapterID},
		Protocol:  string(prepared.Target.Protocol), Kind: "attempt", Status: status,
		RuleID: decision.RuleID, Action: string(decision.Action), Timestamp: d.now(),
	}
	if classified != nil {
		event.Code, event.Type = classified.Code(), classified.Type()
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}
func (d *StreamDriver) logSuccess(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int) {
	d.logEvent(ctx, in, prepared, attemptNo, "success", nil, retry.Decision{})
}
func (d *StreamDriver) logFailure(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, classified *sdk.ClassifiedError, decision retry.Decision) {
	d.logEvent(ctx, in, prepared, attemptNo, "failed", classified, decision)
}

func (d *StreamDriver) sleeper() Sleeper {
	if !isNilInterface(d.Sleeper) {
		return d.Sleeper
	}
	return contextSleeper{}
}
