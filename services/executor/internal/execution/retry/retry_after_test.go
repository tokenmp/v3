package retry

import (
	"context"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func TestRecordFailure_RetryAfterClampedToHardMax(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.Backoff = 0
	policy.MaxTotalDuration = 2 * sdk.HardMaxRetryAfter // Allow enough room.
	state := NewState(plan, &fakeClock{})
	a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	// Supply a RetryAfter exceeding HardMaxRetryAfter.
	hugeRA := 2 * sdk.HardMaxRetryAfter
	decision, err := state.RecordFailure(context.Background(), a, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &hugeRA}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Retry() {
		t.Fatalf("decision.Stop = %q, want retry", decision.Stop)
	}
	if decision.Delay != sdk.HardMaxRetryAfter {
		t.Fatalf("delay = %v, want %v (clamped to HardMaxRetryAfter)", decision.Delay, sdk.HardMaxRetryAfter)
	}
}

func TestRecordFailure_RetryAfterInteractsWithBackoff(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.Backoff = 10 * time.Second
	state := NewState(plan, &fakeClock{})
	a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	// RetryAfter > Backoff → use RetryAfter.
	ra := 30 * time.Second
	decision, err := state.RecordFailure(context.Background(), a, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &ra}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Delay != 30*time.Second {
		t.Fatalf("delay = %v, want 30s (RetryAfter > Backoff)", decision.Delay)
	}

	// RetryAfter < Backoff → use Backoff.
	state = NewState(plan, &fakeClock{})
	a, _ = state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	smallRA := 2 * time.Second
	decision, err = state.RecordFailure(context.Background(), a, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &smallRA}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Delay != 10*time.Second {
		t.Fatalf("delay = %v, want 10s (Backoff > RetryAfter)", decision.Delay)
	}
}

func TestRecordFailure_RetryAfterWithMaxTotalDuration(t *testing.T) {
	plan := testPlan(t)
	policy := retryPolicy(adapter.RetryRule{Action: adapter.RetryNextCredential})
	policy.Backoff = 0
	policy.MaxTotalDuration = 10 * time.Second
	clock := &fakeClock{}
	state := NewState(plan, clock)
	a, err := state.BeginAttempt(context.Background(), plan.Candidates[0], policy)
	if err != nil {
		t.Fatal(err)
	}
	// RetryAfter exceeds MaxTotalDuration → stop.
	ra := 30 * time.Second
	decision, err := state.RecordFailure(context.Background(), a, Failure{Classified: failure(503, "", "").Classified, RetryAfter: &ra}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Stop != StopMaxTotalDuration {
		t.Fatalf("stop = %q, want %q", decision.Stop, StopMaxTotalDuration)
	}
}
