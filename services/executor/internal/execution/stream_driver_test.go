package execution

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

type driverSource struct {
	events []sdk.StreamEvent
	index  int
	closed bool
	mu     sync.Mutex
}

func (s *driverSource) Next(ctx context.Context) (sdk.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return sdk.StreamEvent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index == len(s.events) {
		return sdk.StreamEvent{}, streaming.ErrEndOfStream
	}
	e := s.events[s.index]
	s.index++
	return e, nil
}
func (s *driverSource) Close() error { s.mu.Lock(); s.closed = true; s.mu.Unlock(); return nil }

type driverSink struct{ committed, writes, flushes int }

func (s *driverSink) Commit(context.Context, []sdk.StreamEvent) error   { s.committed++; return nil }
func (s *driverSink) WriteEvent(context.Context, sdk.StreamEvent) error { s.writes++; return nil }
func (s *driverSink) Flush(context.Context) error                       { s.flushes++; return nil }

type cancelAfterFlushSink struct {
	driverSink
	cancel context.CancelFunc
}

func (s *cancelAfterFlushSink) Flush(ctx context.Context) error { s.flushes++; s.cancel(); return nil }

type typedNilDriverSink struct{}

func (*typedNilDriverSink) Commit(context.Context, []sdk.StreamEvent) error   { panic("must not call") }
func (*typedNilDriverSink) WriteEvent(context.Context, sdk.StreamEvent) error { panic("must not call") }
func (*typedNilDriverSink) Flush(context.Context) error                       { panic("must not call") }

type typedNilQuota struct{}

func (*typedNilQuota) ReserveReservation(context.Context, quota.ReserveRequest) (quota.Reservation, error) {
	panic("must not call")
}
func (*typedNilQuota) FinalizeReservation(context.Context, quota.FinalizeRequest) (quota.Reservation, error) {
	panic("must not call")
}
func (*typedNilQuota) ReleaseReservation(context.Context, quota.ReleaseRequest) (quota.Reservation, error) {
	panic("must not call")
}
func (*typedNilQuota) Lookup(context.Context, quota.ReservationID) (quota.Reservation, error) {
	panic("must not call")
}

type typedNilLogger struct{}

func (*typedNilLogger) RecordExecution(context.Context, requestlog.ExecutionEvent) error {
	panic("must not call")
}

type typedNilDriverClock struct{}

func (*typedNilDriverClock) Now() time.Time { panic("must not call") }

type typedNilDriverSleeper struct{}

func (*typedNilDriverSleeper) Sleep(context.Context, time.Duration) error { panic("must not call") }

type typedNilDriverCredentials struct{}

func (*typedNilDriverCredentials) Resolve(context.Context, string) (sdk.CredentialSecret, error) {
	panic("must not call")
}

type driverStreamClient struct {
	opens int
	open  func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error)
}

func (c *driverStreamClient) Stream(ctx context.Context, call sdk.StreamCall) (sdk.StreamOpen, error) {
	c.opens++
	return c.open(ctx, call)
}

func streamEvent(sequence uint64, kind streaming.EventKind) sdk.StreamEvent {
	return sdk.StreamEvent{Sequence: sequence, Meta: streaming.Event{Sequence: sequence, Kind: kind}, Data: []byte(`{"x":1}`)}
}
func streamFinish(sequence uint64) sdk.StreamEvent {
	e := streamEvent(sequence, streaming.EventFinish)
	e.Meta.FinishReason = "stop"
	return e
}
func streamUsage(sequence uint64) sdk.StreamEvent {
	e := streamEvent(sequence, streaming.EventUsage)
	e.Meta.Usage = &streaming.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}
	return e
}

func streamDriver(t *testing.T, client sdk.StreamClient) (*StreamDriver, *quota.TypedMock) {
	t.Helper()
	port := quota.NewTypedMock()
	registry := NewSDKRegistry()
	if err := registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	clock := &fakeClock{}
	return &StreamDriver{Quota: port, SDKRegistry: registry, Logger: requestlog.NewInMemoryExecution(), Clock: clock, Sleeper: &recordingSleeper{clock: clock}}, port
}
func streamDriverInput(resolver *routing.Resolver, plan routing.Plan, sink ProtocolSink) StreamInput {
	return StreamInput{
		RequestID:     "req",
		QuotaIdentity: QuotaIdentity{Subject: "subject", KeyID: "key-1", Protocol: "openai_chat"},
		ReservationID: testReservationID,
		Plan:          plan,
		Resolver:      resolver,
		Credentials:   staticCredentials{value: []byte("secret")},
		Body:          []byte(`{"messages":[]}`),
		Sink:          sink,
	}
}

func TestStreamDriverOpeningRetryThenCompletes(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{}
	client.open = func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		if client.opens == 1 {
			return sdk.StreamOpen{}, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
		}
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), streamFinish(3)}}}, nil
	}
	driver, port := streamDriver(t, client)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.opens != 2 || !result.Outcome.Committed || result.Outcome.State != streaming.StateCompleted {
		t.Fatalf("opens/outcome = %d/%+v", client.opens, result.Outcome)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestStreamDriverNativePrecommitClassifiedErrorRetries(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{}
	client.open = func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		if client.opens == 1 {
			return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{{Sequence: 1, Meta: streaming.Event{Sequence: 1, Kind: streaming.EventNativeError}, Classified: sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "unavailable", "upstream")}}}}, nil
		}
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), streamFinish(3)}}}, nil
	}
	driver, port := streamDriver(t, client)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if err != nil || client.opens != 2 || result.Outcome.State != streaming.StateCompleted {
		t.Fatalf("result/err/opens = %+v/%v/%d", result, err, client.opens)
	}
	if calls := port.TypedCalls(); len(calls) != 2 || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestStreamDriverSemanticCompleteFinalizes(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), streamFinish(3)}}}, nil
	}}
	driver, port := streamDriver(t, client)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if err != nil || result.Outcome.State != streaming.StateCompleted || result.Outcome.UnresolvedCost {
		t.Fatalf("result/err = %+v / %v", result, err)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestStreamDriverPrecommitFailureReleases(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamFinish(1)}}}, nil
	}}
	driver, port := streamDriver(t, client)
	_, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if !errors.Is(err, ErrUnclassified) {
		t.Fatalf("Run error = %v", err)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestStreamDriverPostcommitNeverRetries(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), {Sequence: 2, Meta: streaming.Event{Sequence: 2, Kind: streaming.EventNativeError}, Classified: sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "stream_error", "protocol")}}}}, nil
	}}
	driver, port := streamDriver(t, client)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if err != nil || client.opens != 1 || !result.Outcome.Committed || result.Outcome.State != streaming.StateFailedAfterCommit {
		t.Fatalf("result/err/opens = %+v/%v/%d", result, err, client.opens)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}

func TestStreamDriverTypedNilDependenciesFailClosed(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) { panic("must not open") }}
	driver, _ := streamDriver(t, client)
	base := streamDriverInput(resolver, plan, &driverSink{})
	var nilQuota *typedNilQuota
	driver.Quota = nilQuota
	if _, err := driver.Run(context.Background(), base); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil quota = %v", err)
	}
	driver, _ = streamDriver(t, client)
	var nilLogger *typedNilLogger
	driver.Logger = nilLogger
	if _, err := driver.Run(context.Background(), base); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil logger = %v", err)
	}
	driver, _ = streamDriver(t, client)
	var nilSink *typedNilDriverSink
	base.Sink = nilSink
	if _, err := driver.Run(context.Background(), base); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil sink = %v", err)
	}
	driver, _ = streamDriver(t, client)
	var nilClock *typedNilDriverClock
	driver.Clock = nilClock
	if _, err := driver.Run(context.Background(), base); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil clock = %v", err)
	}
	driver, _ = streamDriver(t, client)
	var nilSleeper *typedNilDriverSleeper
	driver.Sleeper = nilSleeper
	if _, err := driver.Run(context.Background(), base); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil sleeper = %v", err)
	}
	driver, _ = streamDriver(t, client)
	var nilCredentials *typedNilDriverCredentials
	base.Sink = &driverSink{}
	base.Credentials = nilCredentials
	if _, err := driver.Run(context.Background(), base); !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil credentials = %v", err)
	}
}

func TestStreamDriverPostCommitConfirmedUsageFinalizesAndLogsFailure(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), {Sequence: 3, Meta: streaming.Event{Sequence: 3, Kind: streaming.EventNativeError}, Classified: sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "stream_error", "protocol")}}}}, nil
	}}
	driver, port := streamDriver(t, client)
	log := driver.Logger.(*requestlog.InMemoryExecution)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if err != nil || result.Outcome.State != streaming.StateFailedAfterCommit || result.Outcome.UnresolvedCost {
		t.Fatalf("result/err = %+v/%v", result, err)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
	events := log.Events(context.Background())
	if len(events) != 1 || events[0].Status != "failed" || events[0].Code != "stream_error" || events[0].Type != "protocol" {
		t.Fatalf("log events = %+v", events)
	}
}

func TestStreamDriverCancellationImmediatelyAfterBridgeDoesNotClaimSuccess(t *testing.T) {
	resolver, plan := runnerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), streamFinish(3)}}}, nil
	}}
	driver, port := streamDriver(t, client)
	log := driver.Logger.(*requestlog.InMemoryExecution)
	sink := &cancelAfterFlushSink{cancel: cancel}
	in := streamDriverInput(resolver, plan, sink)
	result, err := driver.Run(ctx, in)
	if !errors.Is(err, context.Canceled) || result != (StreamResult{}) {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "FinalizeReservation" {
		t.Fatalf("quota calls = %+v, want Reserve+Finalize", calls)
	}
	for _, event := range log.Events(context.Background()) {
		if event.Status == "success" {
			t.Fatalf("unexpected success log: %+v", event)
		}
	}
}

func TestStreamDriverFinalizeFailureNeverLogsSuccess(t *testing.T) {
	resolver, plan := runnerFixture(t)
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic), streamUsage(2), streamFinish(3)}}}, nil
	}}
	driver, port := streamDriver(t, client)
	port.SetFinalizeReservationFn(func(context.Context, quota.FinalizeRequest) (quota.Reservation, error) {
		return quota.Reservation{}, errors.New("provider raw finalize error")
	})
	log := driver.Logger.(*requestlog.InMemoryExecution)
	result, err := driver.Run(context.Background(), streamDriverInput(resolver, plan, &driverSink{}))
	if result != (StreamResult{}) || !errors.Is(err, ErrTerminalization) {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	for _, event := range log.Events(context.Background()) {
		if event.Status == "success" {
			t.Fatalf("unexpected success log: %+v", event)
		}
	}
}

func TestStreamDriverParentCancelWins(t *testing.T) {
	resolver, plan := runnerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	client := &driverStreamClient{open: func(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
		cancel()
		return sdk.StreamOpen{Source: &driverSource{events: []sdk.StreamEvent{streamEvent(1, streaming.EventSemantic)}}}, nil
	}}
	driver, port := streamDriver(t, client)
	_, err := driver.Run(ctx, streamDriverInput(resolver, plan, &driverSink{}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
	calls := port.TypedCalls()
	if len(calls) != 2 || calls[1].Method != "ReleaseReservation" {
		t.Fatalf("quota calls = %+v", calls)
	}
}
