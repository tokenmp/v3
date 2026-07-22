// Package retry implements request-local, serial retry state.
package retry

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

var (
	// ErrInvalidAttempt means an Attempt did not originate from this State.
	ErrInvalidAttempt = errors.New("retry: invalid attempt")
	// ErrAttemptComplete means an attempt was already recorded.
	ErrAttemptComplete = errors.New("retry: attempt already complete")
	// ErrAttemptActive means a request-local State permits only one active attempt.
	ErrAttemptActive = errors.New("retry: attempt already active")
	// ErrCommitted means the request reached its irreversible commit gate.
	ErrCommitted = errors.New("retry: request committed")
	// ErrCanceled means the request reached its cancellation gate.
	ErrCanceled = errors.New("retry: request canceled")
	// ErrCandidateNotAllowed means a candidate was not selected by the pinned
	// plan or by the immediately preceding Plan.Next decision.
	ErrCandidateNotAllowed = errors.New("retry: candidate not allowed")
	// ErrBudgetExceeded means starting the candidate would violate the policy.
	ErrBudgetExceeded = errors.New("retry: attempt budget exhausted")
	// ErrPolicyMismatch means a later operation supplied a policy other than
	// the immutable policy pinned by the request's first attempt.
	ErrPolicyMismatch = errors.New("retry: policy mismatch")
	// ErrAttemptNotReady means a retry was started before its decision delay.
	ErrAttemptNotReady = errors.New("retry: attempt not ready")
)

// Clock supplies retry-budget time. A nil Clock uses wall time.
type Clock interface{ Now() time.Time }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// StopReason explains why Decision does not permit another attempt. It contains
// neither raw provider errors nor credential material.
type StopReason string

const (
	StopNone                  StopReason = ""
	StopCommitted             StopReason = "committed"
	StopCanceled              StopReason = "canceled"
	StopDeadline              StopReason = "deadline"
	StopMaxTotalAttempts      StopReason = "max_total_attempts"
	StopMaxSameTargetAttempts StopReason = "max_same_target_attempts"
	StopMaxTotalDuration      StopReason = "max_total_duration"
	StopUnclassified          StopReason = "unclassified"
	StopRetryNone             StopReason = "retry_none"
	StopNoMatch               StopReason = "no_match"
	StopNoCandidate           StopReason = "no_candidate"
)

// Failure contains only classified, safe upstream metadata and an optional
// provider retry hint. Negative RetryAfter values are treated as zero.
type Failure struct {
	Classified *sdk.ClassifiedError
	RetryAfter *time.Duration
}

// Attempt is an opaque request-local attempt token.
type Attempt struct {
	state     *State
	id        uint64
	candidate routing.Candidate
}

// Decision is a retry proposal; callers schedule Delay themselves. Candidate
// is the safe routing candidate and never contains a credential reference or
// secret. A zero Candidate and non-empty Stop means no retry is allowed.
type Decision struct {
	Candidate routing.Candidate
	Delay     time.Duration
	Action    adapter.RetryAction
	RuleID    string
	Stop      StopReason
}

// Retry reports whether a further attempt is permitted.
func (d Decision) Retry() bool { return d.Stop == StopNone }

// State is deliberately request-local and serial: its methods must be called
// by one request lifecycle, not concurrently. It does no waiting or I/O.
type State struct {
	plan  routing.Plan
	clock Clock

	nextID          uint64
	active          uint64
	attempts        int
	byTarget        map[routing.QuarantineTarget]int
	visited         map[routing.QuarantineTarget]struct{}
	firstFailure    time.Time
	hasFirstFailure bool
	committed       bool
	canceled        bool
	complete        map[uint64]bool
	policy          adapter.CompiledRetry
	hasPolicy       bool
	pending         *pendingAttempt
}

type pendingAttempt struct {
	candidate routing.Candidate
	notBefore time.Time
}

// NewState pins plan for one request. The plan is copied by value; Plan.Next
// retains its already-frozen private candidate universe.
func NewState(pinned routing.Plan, clock Clock) *State {
	if clock == nil {
		clock = wallClock{}
	}
	return &State{
		plan:     clonePlan(pinned),
		clock:    clock,
		byTarget: make(map[routing.QuarantineTarget]int),
		visited:  make(map[routing.QuarantineTarget]struct{}),
		complete: make(map[uint64]bool),
	}
}

// BeginAttempt reserves the supplied candidate as the single active logical
// attempt. It increments this State's budget before any provider invocation;
// it neither calls an SDK nor proves that a RoundTrip, network write, or wire
// attempt occurred. Its first call freezes the policy for this State; later
// calls must supply an equal policy. Explicit zero limits allow the initial
// attempt only.
func (s *State) BeginAttempt(ctx context.Context, candidate routing.Candidate, policy adapter.CompiledRetry) (Attempt, error) {
	if err := ctx.Err(); err != nil {
		return Attempt{}, err
	}
	if s == nil {
		return Attempt{}, ErrInvalidAttempt
	}
	if err := s.checkOrFreezePolicy(policy); err != nil {
		return Attempt{}, err
	}
	if s.active != 0 {
		return Attempt{}, ErrAttemptActive
	}
	if s.committed {
		return Attempt{}, ErrCommitted
	}
	if s.canceled {
		return Attempt{}, ErrCanceled
	}
	now := s.clock.Now()
	if s.pending != nil {
		if candidate != s.pending.candidate {
			return Attempt{}, ErrCandidateNotAllowed
		}
		if now.Before(s.pending.notBefore) {
			return Attempt{}, ErrAttemptNotReady
		}
		if s.policy.MaxTotalDuration > 0 && !now.Before(s.firstFailure.Add(s.policy.MaxTotalDuration)) {
			return Attempt{}, ErrBudgetExceeded
		}
		if deadline, ok := ctx.Deadline(); ok && !now.Before(deadline) {
			return Attempt{}, context.DeadlineExceeded
		}
	} else if !s.allowedNext(candidate) {
		return Attempt{}, ErrCandidateNotAllowed
	}
	if !s.canBegin(candidate) {
		return Attempt{}, ErrBudgetExceeded
	}
	s.pending = nil
	target := candidate.Target()
	s.attempts++
	s.byTarget[target]++
	s.visited[target] = struct{}{}
	s.nextID++
	s.active = s.nextID
	return Attempt{state: s, id: s.nextID, candidate: candidate}, nil
}

// RecordFailure completes attempt and returns a non-blocking retry proposal.
func (s *State) RecordFailure(ctx context.Context, attempt Attempt, failure Failure, policy adapter.CompiledRetry) (Decision, error) {
	if s == nil {
		return Decision{}, ErrInvalidAttempt
	}
	if err := s.checkOrFreezePolicy(policy); err != nil {
		return Decision{}, err
	}
	if err := s.finish(attempt); err != nil {
		return Decision{}, err
	}
	if s.committed {
		return stopped(StopCommitted), nil
	}
	if s.canceled {
		return stopped(StopCanceled), nil
	}
	if err := ctx.Err(); err != nil {
		return stopped(contextStop(err)), nil
	}

	now := s.clock.Now()
	if !s.hasFirstFailure {
		s.firstFailure = now
		s.hasFirstFailure = true
	}
	if failure.Classified == nil {
		return stopped(StopUnclassified), nil
	}
	rule, ok := matchingRule(s.policy.Rules, failure.Classified)
	if !ok {
		return stopped(StopNoMatch), nil
	}
	if rule.Action == adapter.RetryNone {
		return Decision{Action: rule.Action, RuleID: rule.ID, Stop: StopRetryNone}, nil
	}
	current := attempt.candidate.Target()
	next, ok := s.plan.Next(rule.Action, current, s.visited)
	if !ok {
		return Decision{Action: rule.Action, RuleID: rule.ID, Stop: StopNoCandidate}, nil
	}
	if reason := s.beginStop(next); reason != StopNone {
		return Decision{Action: rule.Action, RuleID: rule.ID, Stop: reason}, nil
	}
	delay := s.policy.Backoff
	if delay < 0 {
		delay = 0
	}
	if failure.RetryAfter != nil && *failure.RetryAfter > delay {
		delay = *failure.RetryAfter
	}
	// Clamp delay to the global hard cap so no Retry-After value can impose an
	// unbounded delay on the retry loop.
	if delay > sdk.HardMaxRetryAfter {
		delay = sdk.HardMaxRetryAfter
	}
	// Equal deadlines are deliberately rejected: there must be time left for
	// the next attempt, not merely enough time to wake at the deadline.
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline && !now.Add(delay).Before(deadline) {
		return Decision{Action: rule.Action, RuleID: rule.ID, Stop: StopDeadline}, nil
	}
	if s.policy.MaxTotalDuration > 0 && !now.Add(delay).Before(s.firstFailure.Add(s.policy.MaxTotalDuration)) {
		return Decision{Action: rule.Action, RuleID: rule.ID, Stop: StopMaxTotalDuration}, nil
	}
	s.pending = &pendingAttempt{candidate: next, notBefore: now.Add(delay)}
	return Decision{Candidate: next, Delay: delay, Action: rule.Action, RuleID: rule.ID}, nil
}

// RecordSuccess completes attempt. A committed or canceled request cannot be
// made successful again, but a first successful record is otherwise idempotent
// only through its opaque attempt token.
func (s *State) RecordSuccess(_ context.Context, attempt Attempt) error { return s.finish(attempt) }

// Commit irreversibly disables retries. Repeated calls are harmless.
func (s *State) Commit() error {
	if s == nil {
		return ErrInvalidAttempt
	}
	// The first terminal gate wins; replaying either terminal operation is
	// harmless and cannot rewrite the reason exposed to the caller.
	if !s.canceled {
		s.committed = true
		s.pending = nil
	}
	return nil
}

// Cancel irreversibly disables retries. Repeated calls are harmless.
func (s *State) Cancel() error {
	if s == nil {
		return ErrInvalidAttempt
	}
	// See Commit: a prior commit remains the terminal reason.
	if !s.committed {
		s.canceled = true
		s.pending = nil
	}
	return nil
}

func (s *State) finish(a Attempt) error {
	if s == nil || a.state != s || a.id == 0 {
		return ErrInvalidAttempt
	}
	if s.complete[a.id] {
		return ErrAttemptComplete
	}
	if s.active != a.id {
		return ErrInvalidAttempt
	}
	s.complete[a.id] = true
	s.active = 0
	return nil
}

func (s *State) allowedNext(candidate routing.Candidate) bool {
	// Before the first failure, only the resolver's initially selected
	// candidates are valid. Later candidates must come from Plan.Next.
	if s.attempts != 0 {
		return false
	}
	for _, allowed := range s.plan.Candidates {
		if allowed == candidate {
			return true
		}
	}
	return false
}

func (s *State) canBegin(candidate routing.Candidate) bool {
	return s.beginStop(candidate) == StopNone
}

func (s *State) beginStop(candidate routing.Candidate) StopReason {
	maxTotal := s.policy.MaxTotalAttempts
	if maxTotal == 0 {
		maxTotal = 1
	}
	if s.attempts >= maxTotal {
		return StopMaxTotalAttempts
	}
	maxSame := s.policy.MaxSameTargetAttempts
	if maxSame == 0 {
		maxSame = 1
	}
	if s.byTarget[candidate.Target()] >= maxSame {
		return StopMaxSameTargetAttempts
	}
	return StopNone
}

func (s *State) checkOrFreezePolicy(policy adapter.CompiledRetry) error {
	if s.hasPolicy {
		if !reflect.DeepEqual(s.policy, policy) {
			return ErrPolicyMismatch
		}
		return nil
	}
	s.policy = clonePolicy(policy)
	s.hasPolicy = true
	return nil
}

func clonePolicy(policy adapter.CompiledRetry) adapter.CompiledRetry {
	if policy.Rules == nil {
		return policy
	}
	rules := policy.Rules
	policy.Rules = make([]adapter.RetryRule, len(rules))
	for i, rule := range rules {
		policy.Rules[i] = rule
		policy.Rules[i].HTTPStatuses = cloneInts(rule.HTTPStatuses)
		policy.Rules[i].ErrorCodes = cloneStrings(rule.ErrorCodes)
		policy.Rules[i].ErrorTypes = cloneStrings(rule.ErrorTypes)
	}
	return policy
}

func cloneInts(values []int) []int {
	if values == nil {
		return nil
	}
	return append([]int(nil), values...)
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func clonePlan(plan routing.Plan) routing.Plan {
	plan.Candidates = append([]routing.Candidate(nil), plan.Candidates...)
	return plan
}

func stopped(reason StopReason) Decision { return Decision{Stop: reason} }

func contextStop(err error) StopReason {
	if errors.Is(err, context.DeadlineExceeded) {
		return StopDeadline
	}
	return StopCanceled
}

func matchingRule(rules []adapter.RetryRule, classified *sdk.ClassifiedError) (adapter.RetryRule, bool) {
	status, code, typ := 0, "", ""
	if classified != nil {
		status, code, typ = classified.Status(), classified.Code(), classified.Type()
	}
	for _, rule := range rules {
		if matchesInt(rule.HTTPStatuses, status) && matchesString(rule.ErrorCodes, code) && matchesString(rule.ErrorTypes, typ) {
			return rule, true
		}
	}
	return adapter.RetryRule{}, false
}

func matchesInt(values []int, value int) bool {
	if len(values) == 0 {
		return true
	}
	for _, allowed := range values {
		if value == allowed {
			return true
		}
	}
	return false
}

func matchesString(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, allowed := range values {
		if value == allowed {
			return true
		}
	}
	return false
}
