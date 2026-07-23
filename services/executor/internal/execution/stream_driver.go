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
	QuotaIdentity QuotaIdentity
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
	Quota          quota.Repository
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
	if strings.TrimSpace(in.RequestID) == "" || quota.ValidateRequestID(in.RequestID) != nil {
		return StreamResult{}, ErrInvalidRequestID
	}
	if quota.ValidateQuotaIdentity(in.QuotaIdentity.Subject, in.QuotaIdentity.KeyID, in.QuotaIdentity.Protocol) != nil {
		return StreamResult{}, ErrInvalidQuotaIdentity
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
	if !validStreamTimeouts(firstPrepared) || in.QuotaIdentity.Protocol != string(firstPrepared.Target.Protocol) {
		return StreamResult{}, ErrMisconfigured
	}
	policy := firstPrepared.Retry
	state := retry.NewState(in.Plan, d.Clock)
	reservation := quota.ReserveRequest{ID: quota.ReservationID(in.ReservationID), Metadata: firstCall.ReservationMetadata(in.RequestID, in.QuotaIdentity), Estimate: quota.Estimate{Basis: quota.BasisNone}}
	if err := reservation.Metadata.Validate(); err != nil {
		return StreamResult{}, ErrMisconfigured
	}
	if _, err := d.Quota.ReserveReservation(ctx, reservation); err != nil {
		return StreamResult{}, ErrQuotaReserve
	}
	terminalizer := NewTerminalizer(d.Quota, quota.ReservationID(in.ReservationID))

	d.logReserved(ctx, in, firstPrepared)

	candidate := first
	attemptNo := 0
	for {
		preparedCall, err := preparer.PreflightStream(ctx, candidate)
		if err != nil {
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, parentError(ctx, err), in, firstPrepared, 0)
		}
		prepared := preparedCall.preparedAttempt()
		if !validStreamTimeouts(prepared) {
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, ErrMisconfigured, in, prepared, attemptNo+1)
		}

		attemptNo++
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, prepared.Timeout.Request)
		attemptStart := d.now()
		attempt, acquired, began, opened, openErr := preparedCall.NewAttemptSession(state, policy, in.Credentials).ExecuteStream(attemptCtx, func(client sdk.StreamClient, call sdk.StreamCall) (sdk.StreamOpen, error) {
			return client.Stream(attemptCtx, call)
		})
		latency := d.now().Sub(attemptStart)
		if openErr != nil {
			cancelAttempt()
			if !acquired {
				return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, parentError(ctx, openErr), in, prepared, attemptNo)
			}
			if !began {
				return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, parentError(ctx, ErrBudgetExhausted), in, prepared, attemptNo)
			}
			if err := ctx.Err(); err != nil {
				_ = state.Cancel()
				return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, err, in, prepared, attemptNo)
			}
			if errors.Is(openErr, context.Canceled) {
				_ = state.Cancel()
				return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, context.Canceled, in, prepared, attemptNo)
			}
			classified := classifyStreamError(openErr)
			if classified == nil {
				d.logFailure(ctx, in, prepared, attemptNo, latency, false, nil, retry.Decision{})
				_ = state.Cancel()
				if errors.Is(openErr, ErrMisconfigured) {
					return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, ErrMisconfigured, in, prepared, attemptNo)
				}
				return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, ErrUnclassified, in, prepared, attemptNo)
			}
			if result, next, done, err := d.retryPrecommit(ctx, terminalizer, state, attempt, policy, prepared, classified, in, attemptNo, latency); done {
				return result, err
			} else {
				candidate = next
				continue
			}
		}
		if isNilInterface(opened.Source) {
			cancelAttempt()
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, ErrMisconfigured, in, prepared, attemptNo)
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
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, primary, in, prepared, attemptNo)
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
				if err := d.settleCommitted(ctx, terminalizer, outcome); err != nil {
					d.logFailure(ctx, in, prepared, attemptNo, latency, true, source.LastClassified(), retry.Decision{})
					return StreamResult{}, errors.Join(parentErr, terminalizationError("terminal"))
				}
				d.logFailure(ctx, in, prepared, attemptNo, latency, true, source.LastClassified(), retry.Decision{})
				d.logReleased(ctx, in, prepared, attemptNo, releaseReason(parentErr))
				return StreamResult{}, parentErr
			}
			_ = state.Commit()
			if outcome.State == streaming.StateCompleted {
				_ = state.RecordSuccess(ctx, attempt)
			}
			if err := d.settleCommitted(ctx, terminalizer, outcome); err != nil {
				d.logFailure(ctx, in, prepared, attemptNo, latency, true, source.LastClassified(), retry.Decision{})
				d.logTerminalizationUnknown(ctx, in, prepared, attemptNo)
				return StreamResult{}, terminalizationError("terminal")
			}
			if outcome.State == streaming.StateCompleted {
				d.logSuccess(ctx, in, prepared, attemptNo, latency, outcome)
				d.logCommitted(ctx, in, prepared, attemptNo, outcome)
				d.logFinalized(ctx, in, prepared, attemptNo, outcome)
			} else {
				d.logFailure(ctx, in, prepared, attemptNo, latency, true, source.LastClassified(), retry.Decision{})
				d.logFinalized(ctx, in, prepared, attemptNo, outcome)
			}
			return StreamResult{Outcome: outcome}, nil
		}
		if err := ctx.Err(); err != nil {
			d.logFailure(ctx, in, prepared, attemptNo, latency, false, source.LastClassified(), retry.Decision{})
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, err, in, prepared, attemptNo)
		}
		// Bridge reports any context cancellation as StateClientCancelled. A
		// parent cancellation already won above; an attempt-only deadline remains
		// a safe timeout classification and may use the frozen retry policy.
		if bridgeErr == nil && outcome.State == streaming.StateClientCancelled && !errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			d.logFailure(ctx, in, prepared, attemptNo, latency, false, source.LastClassified(), retry.Decision{})
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, context.Canceled, in, prepared, attemptNo)
		}
		classified := classifyBridgeError(bridgeErr, source.LastClassified(), attemptCtx.Err())
		if classified == nil {
			d.logFailure(ctx, in, prepared, attemptNo, latency, false, nil, retry.Decision{})
			_ = state.Cancel()
			return StreamResult{}, d.releaseFailureWithLog(ctx, terminalizer, ErrUnclassified, in, prepared, attemptNo)
		}
		if result, next, done, err := d.retryPrecommit(ctx, terminalizer, state, attempt, policy, prepared, classified, in, attemptNo, latency); done {
			return result, err
		} else {
			candidate = next
		}
	}
}

func (d *StreamDriver) retryPrecommit(ctx context.Context, terminalizer *Terminalizer, state *retry.State, attempt retry.Attempt, policy adapter.CompiledRetry, prepared routing.PreparedAttempt, classified *sdk.ClassifiedError, in StreamInput, attemptNo int, latency time.Duration) (StreamResult, routing.Candidate, bool, error) {
	var retryAfter *time.Duration
	if ra, ok := classified.RetryAfter(); ok {
		retryAfter = &ra
	}
	decision, err := state.RecordFailure(ctx, attempt, retry.Failure{Classified: classified, RetryAfter: retryAfter}, policy)
	if err != nil {
		_ = state.Cancel()
		resultErr := d.releaseFailureWithLog(ctx, terminalizer, parentError(ctx, ErrBudgetExhausted), in, prepared, attemptNo)
		// This is terminal (or safely terminalization-unknown), so logging may
		// record failure but never claims a confirmed successful outcome.
		d.logFailure(ctx, in, prepared, attemptNo, latency, false, classified, retry.Decision{})
		return StreamResult{}, routing.Candidate{}, true, resultErr
	}
	if !decision.Retry() {
		mapped := (adapter.Engine{}).MapResponse(prepared.Adapter, classified.ToUpstreamResponse())
		releaseReasonVal := releaseReason(classified)
		if err := d.releaseCleanupReason(ctx, terminalizer, releaseReasonVal); err != nil {
			d.logFailure(ctx, in, prepared, attemptNo, latency, false, classified, decision)
			d.logTerminalizationUnknown(ctx, in, prepared, attemptNo)
			return StreamResult{}, routing.Candidate{}, true, errors.Join(classified, terminalizationError("release"))
		}
		d.logFailure(ctx, in, prepared, attemptNo, latency, false, classified, decision)
		d.logReleased(ctx, in, prepared, attemptNo, releaseReasonVal)
		applyRetryAfter(&mapped, classified)
		return StreamResult{Failure: &mapped}, routing.Candidate{}, true, nil
	}
	// A retry decision is an attempt observation, not a terminal outcome.
	d.logFailure(ctx, in, prepared, attemptNo, latency, false, classified, decision)
	if err := d.sleeper().Sleep(ctx, decision.Delay); err != nil {
		_ = state.Cancel()
		resultErr := d.releaseFailureWithLog(ctx, terminalizer, parentError(ctx, err), in, prepared, attemptNo)
		d.logFailure(ctx, in, prepared, attemptNo, latency, false, classified, retry.Decision{})
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
	return t.Release(cleanup, quota.ReleaseFailed)
}
func (d *StreamDriver) releaseCleanupReason(ctx context.Context, t *Terminalizer, reason quota.ReleaseReason) error {
	if t == nil {
		return ErrTerminalConflict
	}
	cleanup, cancel := d.cleanupContext(ctx)
	defer cancel()
	return t.Release(cleanup, reason)
}
func (d *StreamDriver) settleCommitted(ctx context.Context, t *Terminalizer, outcome streaming.Outcome) error {
	if outcome.UnresolvedCost {
		return d.releaseCleanupReason(ctx, t, quota.ReleaseUnresolved)
	}
	usage, ok := confirmedUsage(outcome.Usage, outcome.UsageKnown)
	if !ok {
		return d.releaseCleanupReason(ctx, t, quota.ReleaseUnresolved)
	}
	completion := quota.OutcomeAfterCommitError
	switch outcome.State {
	case streaming.StateCompleted:
		completion = quota.OutcomeCompleted
	case streaming.StateClientCancelled:
		completion = quota.OutcomeClientCancelled
	}
	if t == nil {
		return ErrTerminalConflict
	}
	cleanup, cancel := d.cleanupContext(ctx)
	defer cancel()
	return t.Finalize(cleanup, quota.FinalizeOutcome{
		Disposition: quota.AccountingConfirmedUsage,
		Outcome:     completion,
		Usage:       usage,
	})
}

func confirmedUsage(usage streaming.Usage, known bool) (quota.ConfirmedUsage, bool) {
	// A zero-valued usage event is billable only when the source explicitly
	// confirmed usage. Absence is unresolved; never infer confirmation from
	// all-zero counters.
	if !known || usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 {
		return quota.ConfirmedUsage{}, false
	}
	prompt, completion, total := uint64(usage.PromptTokens), uint64(usage.CompletionTokens), uint64(usage.TotalTokens)
	if prompt > total || completion > total-prompt || prompt+completion != total {
		return quota.ConfirmedUsage{}, false
	}
	confirmed := quota.ConfirmedUsage{InputTokens: prompt, OutputTokens: completion, TotalTokens: total}
	return confirmed, confirmed.Valid()
}
func (d *StreamDriver) releaseFailure(ctx context.Context, t *Terminalizer, primary error) error {
	if err := d.releaseCleanupReason(ctx, t, releaseReason(primary)); err != nil {
		return errors.Join(primary, terminalizationError("release"))
	}
	return primary
}

// releaseFailureWithLog is like releaseFailure but also records a released or
// terminalization-unknown lifecycle event.
func (d *StreamDriver) releaseFailureWithLog(ctx context.Context, t *Terminalizer, primary error, in StreamInput, prepared routing.PreparedAttempt, attemptNo int) error {
	reason := releaseReason(primary)
	if err := d.releaseCleanupReason(ctx, t, reason); err != nil {
		d.logTerminalizationUnknown(ctx, in, prepared, attemptNo)
		return errors.Join(primary, terminalizationError("release"))
	}
	d.logReleased(ctx, in, prepared, attemptNo, reason)
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

func (d *StreamDriver) baseEvent(in StreamInput, prepared routing.PreparedAttempt, attemptNo int) requestlog.ExecutionEvent {
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
		Kind:      requestlog.KindAttempt,
		Subject:   in.QuotaIdentity.Subject,
		KeyID:     in.QuotaIdentity.KeyID,
		Timestamp: d.now(),
	}
}

func (d *StreamDriver) logReserved(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := requestlog.ExecutionEvent{
		RequestID:     in.RequestID,
		ReservationID: in.ReservationID,
		Revision:      prepared.Revision,
		Generation:    prepared.Generation,
		Candidate: requestlog.ExecutionCandidate{
			ModelID:      prepared.Candidate.ModelID,
			ProviderID:   prepared.Candidate.ProviderID,
			RouteID:      prepared.Candidate.RouteID,
			CredentialID: prepared.Candidate.CredentialID,
			AdapterID:    prepared.Candidate.AdapterID,
		},
		Protocol:  string(prepared.Target.Protocol),
		Kind:      requestlog.KindReserved,
		Subject:   in.QuotaIdentity.Subject,
		KeyID:     in.QuotaIdentity.KeyID,
		Timestamp: d.now(),
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) logSuccess(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, latency time.Duration, outcome streaming.Outcome) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := d.baseEvent(in, prepared, attemptNo)
	event.Status = "success"
	event.Latency = latency
	event.Committed = true
	if outcome.UsageKnown {
		event.Usage = requestlog.ExecutionUsage{
			InputTokens:  uint64(outcome.Usage.PromptTokens),
			OutputTokens: uint64(outcome.Usage.CompletionTokens),
			TotalTokens:  uint64(outcome.Usage.TotalTokens),
		}
		event.UsageKnown = true
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) logFailure(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, latency time.Duration, committed bool, classified *sdk.ClassifiedError, decision retry.Decision) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := d.baseEvent(in, prepared, attemptNo)
	event.Status = "failed"
	event.Latency = latency
	event.Committed = committed
	if classified != nil {
		event.Code = classified.Code()
		event.Type = classified.Type()
	}
	event.RuleID = decision.RuleID
	event.Action = string(decision.Action)
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) logCommitted(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, outcome streaming.Outcome) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := requestlog.ExecutionEvent{
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
		Kind:      requestlog.KindCommitted,
		Subject:   in.QuotaIdentity.Subject,
		KeyID:     in.QuotaIdentity.KeyID,
		Timestamp: d.now(),
		Committed: true,
	}
	if outcome.UsageKnown {
		event.Usage = requestlog.ExecutionUsage{
			InputTokens:  uint64(outcome.Usage.PromptTokens),
			OutputTokens: uint64(outcome.Usage.CompletionTokens),
			TotalTokens:  uint64(outcome.Usage.TotalTokens),
		}
		event.UsageKnown = true
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) logFinalized(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, outcome streaming.Outcome) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	disposition := string(quota.AccountingConfirmedUsage)
	completionOutcome := string(quota.OutcomeAfterCommitError)
	switch outcome.State {
	case streaming.StateCompleted:
		completionOutcome = string(quota.OutcomeCompleted)
	case streaming.StateClientCancelled:
		completionOutcome = string(quota.OutcomeClientCancelled)
	}
	event := requestlog.ExecutionEvent{
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
		Kind:      requestlog.KindFinalized,
		Subject:   in.QuotaIdentity.Subject,
		KeyID:     in.QuotaIdentity.KeyID,
		Timestamp: d.now(),
		Settlement: requestlog.ExecutionSettlement{
			Disposition: disposition,
			Outcome:     completionOutcome,
		},
	}
	if outcome.UsageKnown {
		event.Usage = requestlog.ExecutionUsage{
			InputTokens:  uint64(outcome.Usage.PromptTokens),
			OutputTokens: uint64(outcome.Usage.CompletionTokens),
			TotalTokens:  uint64(outcome.Usage.TotalTokens),
		}
		event.UsageKnown = true
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) logReleased(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int, reason quota.ReleaseReason) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := requestlog.ExecutionEvent{
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
		Kind:      requestlog.KindReleased,
		Subject:   in.QuotaIdentity.Subject,
		KeyID:     in.QuotaIdentity.KeyID,
		Timestamp: d.now(),
		Settlement: requestlog.ExecutionSettlement{
			Reason: string(reason),
		},
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) logTerminalizationUnknown(ctx context.Context, in StreamInput, prepared routing.PreparedAttempt, attemptNo int) {
	if d == nil || isNilInterface(d.Logger) {
		return
	}
	event := requestlog.ExecutionEvent{
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
		Kind:      requestlog.KindReleased,
		Subject:   in.QuotaIdentity.Subject,
		KeyID:     in.QuotaIdentity.KeyID,
		Timestamp: d.now(),
		Settlement: requestlog.ExecutionSettlement{
			Reason: "unknown",
		},
	}
	logCtx, cancel := d.logContext(ctx)
	defer cancel()
	_ = d.Logger.RecordExecution(logCtx, event)
}

func (d *StreamDriver) sleeper() Sleeper {
	if !isNilInterface(d.Sleeper) {
		return d.Sleeper
	}
	return contextSleeper{}
}
