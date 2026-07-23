package execution

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func TestRunnerRetryAfterPropagatesToSleepDelay(t *testing.T) {
	// Return a ClassifiedError with RetryAfter on first call; succeed on second.
	var callCount int32
	client := &runnerTestClient{completeFn: func(ctx context.Context, call sdk.Call) (sdk.Completion, error) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			return sdk.Completion{}, sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrUnavailable, 503, "", "", "", 2*time.Second, true)
		}
		return sdk.Completion{RawJSON: []byte(`{"ok":true}`), Status: 200, RequestID: "req_ok"}, nil
	}}
	clock := &fakeClock{now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	sleeper := &recordingSleeper{clock: clock}
	log := requestlog.NewInMemoryExecution()
	resolver, plan := runnerFixture(t)
	quotaPort := quota.NewTypedMock()
	registry := NewSDKRegistry()
	_ = registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client)
	runner := &Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      log,
		Clock:       clock,
		Sleeper:     sleeper,
	}

	result, err := runner.Run(context.Background(), runnerInput(resolver, plan))
	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if result.Completion.Status != 200 {
		t.Fatalf("Completion.Status = %d, want 200", result.Completion.Status)
	}
	if client.callCount() != 2 {
		t.Fatalf("calls = %d, want 2", client.callCount())
	}
	// Verify the sleeper saw a delay of at least 2s (RetryAfter > Backoff).
	sleeper.mu.Lock()
	defer sleeper.mu.Unlock()
	if len(sleeper.delays) == 0 {
		t.Fatal("no sleep recorded")
	}
	if sleeper.delays[0] < 2*time.Second {
		t.Fatalf("sleep delay = %v, want >= 2s (RetryAfter)", sleeper.delays[0])
	}
}

func TestStreamDriverRetryAfterPropagatesToSleepDelay(t *testing.T) {
	// First open returns ClassifiedError with RetryAfter; second succeeds.
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{}
	client.open = func(ctx context.Context, call sdk.StreamCall) (sdk.StreamOpen, error) {
		if client.opens == 1 {
			return sdk.StreamOpen{}, sdk.NewClassifiedErrorWithRetryAfter(sdk.ErrUnavailable, 503, "", "", "", 3*time.Second, true)
		}
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), streamFinish(3)}}}, nil
	}
	driver, _ := streamDriver(t, client)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.opens != 2 || !result.Outcome.Committed {
		t.Fatalf("opens/outcome = %d/%+v", client.opens, result.Outcome)
	}
	// Verify the sleeper saw a delay of at least 3s.
	rs := driver.Sleeper.(*recordingSleeper)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.delays) == 0 {
		t.Fatal("no sleep recorded")
	}
	if rs.delays[0] < 3*time.Second {
		t.Fatalf("sleep delay = %v, want >= 3s (RetryAfter)", rs.delays[0])
	}
}
