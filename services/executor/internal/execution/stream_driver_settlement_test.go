package execution

import (
	"context"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func TestStreamDriverSettleCommittedRequiresExplicitUsagePresence(t *testing.T) {
	tests := []struct {
		name       string
		outcome    streaming.Outcome
		wantState  quota.ReservationState
		wantReason quota.ReleaseReason
		wantResult quota.CompletionOutcome
		wantUsage  quota.ConfirmedUsage
	}{
		{
			name:      "completed absent usage releases unresolved",
			outcome:   streaming.Outcome{Committed: true, State: streaming.StateCompleted},
			wantState: quota.ReservationReleased, wantReason: quota.ReleaseUnresolved,
		},
		{
			name:      "confirmed zero usage finalizes",
			outcome:   streaming.Outcome{Committed: true, State: streaming.StateCompleted, UsageKnown: true},
			wantState: quota.ReservationFinalized, wantResult: quota.OutcomeCompleted,
		},
		{
			name:      "after commit error confirmed usage finalizes",
			outcome:   streaming.Outcome{Committed: true, State: streaming.StateFailedAfterCommit, UsageKnown: true, Usage: streaming.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}},
			wantState: quota.ReservationFinalized, wantResult: quota.OutcomeAfterCommitError,
			wantUsage: quota.ConfirmedUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		},
		{
			name:      "client cancellation confirmed usage finalizes",
			outcome:   streaming.Outcome{Committed: true, State: streaming.StateClientCancelled, UsageKnown: true, Usage: streaming.Usage{TotalTokens: 0}},
			wantState: quota.ReservationFinalized, wantResult: quota.OutcomeClientCancelled,
		},
		{
			name:      "explicit unresolved cost releases",
			outcome:   streaming.Outcome{Committed: true, State: streaming.StateFailedAfterCommit, UsageKnown: true, UnresolvedCost: true},
			wantState: quota.ReservationReleased, wantReason: quota.ReleaseUnresolved,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := quota.NewDomainInMemory()
			terminalReserve(t, repo)
			driver := StreamDriver{CleanupTimeout: 1000000000}
			if err := driver.settleCommitted(context.Background(), NewTerminalizer(repo, terminalID()), tc.outcome); err != nil {
				t.Fatalf("settleCommitted = %v", err)
			}
			got, err := repo.Lookup(context.Background(), terminalID())
			if err != nil || got.State != tc.wantState {
				t.Fatalf("settlement state=%q err=%v, want %q", got.State, err, tc.wantState)
			}
			if tc.wantState == quota.ReservationReleased {
				if got.Settlement.Reason == nil || *got.Settlement.Reason != tc.wantReason {
					t.Fatalf("release reason=%v, want %q", got.Settlement.Reason, tc.wantReason)
				}
				return
			}
			if got.Settlement.Outcome == nil || got.Settlement.Outcome.Outcome != tc.wantResult || got.Settlement.Outcome.Usage != tc.wantUsage {
				t.Fatalf("finalize outcome=%+v, want result=%q usage=%+v", got.Settlement.Outcome, tc.wantResult, tc.wantUsage)
			}
		})
	}
}

func TestConfirmedUsageRejectsAbsentOrInconsistentUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage streaming.Usage
		known bool
	}{
		{"absent zero", streaming.Usage{}, false},
		{"inconsistent total", streaming.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 3}, true},
		{"negative", streaming.Usage{PromptTokens: -1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := confirmedUsage(tc.usage, tc.known); ok {
				t.Fatal("confirmedUsage accepted invalid or absent usage")
			}
		})
	}
}
