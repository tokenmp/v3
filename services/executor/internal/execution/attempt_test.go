package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution/retry"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
)

func newAttemptPreparerFixture(t *testing.T) (*AttemptPreparer, *countingCredentials, *runnerTestClient, Input) {
	t.Helper()
	resolver, plan := runnerFixture(t)
	client := &runnerTestClient{}
	registry := NewSDKRegistry()
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, client); err != nil {
		t.Fatalf("Register: %v", err)
	}
	in := runnerInput(resolver, plan)
	credentials := &countingCredentials{value: []byte("attempt-session-secret")}
	in.Credentials = credentials
	preparer, err := NewAttemptPreparer(resolver, plan, registry, in.Body, in.Thinking)
	if err != nil {
		t.Fatalf("NewAttemptPreparer: %v", err)
	}
	return preparer, credentials, client, in
}

func TestAttemptPreparerRejectsForeignPlanBeforePreflight(t *testing.T) {
	resolver, _ := runnerFixture(t)
	_, foreignPlan := runnerFixture(t)
	registry := NewSDKRegistry()

	_, err := NewAttemptPreparer(resolver, foreignPlan, registry, []byte(`{}`), adapter.ThinkingRequest{})
	if !errors.Is(err, routing.ErrInvalidPlan) {
		t.Fatalf("NewAttemptPreparer foreign plan error = %v, want routing.ErrInvalidPlan", err)
	}
}

func TestAttemptPreparerPreflightChecksAuthAndRegistryWithoutCredential(t *testing.T) {
	preparer, credentials, _, in := newAttemptPreparerFixture(t)
	preparer.registry = NewSDKRegistry()

	_, err := preparer.Preflight(context.Background(), in.Plan.Candidates[0])
	if !errors.Is(err, ErrSDKClientUnknown) {
		t.Fatalf("Preflight error = %v, want ErrSDKClientUnknown", err)
	}
	if calls, _ := credentials.snapshot(); calls != 0 {
		t.Fatalf("Preflight credential resolutions = %d, want 0", calls)
	}
}

func TestAttemptSessionExecutesOnlyOnceAndRedactsSecret(t *testing.T) {
	preparer, credentials, client, in := newAttemptPreparerFixture(t)
	prepared, err := preparer.Preflight(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if got := fmt.Sprintf("%+v %#v", prepared, prepared); strings.Contains(got, "attempt-session-secret") || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("PreparedCall formatting leaked secret: %q", got)
	}

	state := retry.NewState(in.Plan, &fakeClock{})
	session := prepared.NewAttemptSession(state, prepared.preparedAttempt().Retry, credentials)
	var invoked int32
	attempt, credentialAcquired, began, err := session.Execute(context.Background(), func(gotClient sdk.Client, call sdk.Call) {
		atomic.AddInt32(&invoked, 1)
		if gotClient != client {
			t.Fatal("Execute supplied an unregistered client")
		}
		if rendered := fmt.Sprintf("%+v %#v", call.Secret, call.Secret); strings.Contains(rendered, "attempt-session-secret") || !strings.Contains(rendered, "[REDACTED]") {
			t.Fatalf("CredentialSecret formatting leaked: %q", rendered)
		}
	})
	if err != nil || !credentialAcquired || !began {
		t.Fatalf("Execute result = attempt=%+v credential=%v began=%v err=%v", attempt, credentialAcquired, began, err)
	}
	if got := atomic.LoadInt32(&invoked); got != 1 {
		t.Fatalf("callback calls = %d, want 1", got)
	}
	if calls, _ := credentials.snapshot(); calls != 1 {
		t.Fatalf("credential resolutions = %d, want 1", calls)
	}

	_, credentialAcquired, began, err = session.Execute(context.Background(), func(sdk.Client, sdk.Call) { t.Fatal("reused session called callback") })
	if !errors.Is(err, ErrAttemptSessionUsed) || credentialAcquired || began {
		t.Fatalf("second Execute = credential=%v began=%v err=%v, want unused false and ErrAttemptSessionUsed", credentialAcquired, began, err)
	}
	if calls, _ := credentials.snapshot(); calls != 1 {
		t.Fatalf("second Execute credential resolutions = %d, want 1", calls)
	}
}

func TestAttemptSessionRevokesSavedCallAfterCallback(t *testing.T) {
	preparer, _, _, in := newAttemptPreparerFixture(t)
	prepared, err := preparer.Preflight(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	session := prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, in.Credentials)
	var saved sdk.Call
	_, credentialAcquired, began, err := session.Execute(context.Background(), func(_ sdk.Client, call sdk.Call) {
		saved = call
		if err := call.Secret.Use(func(value []byte) error {
			if got := string(value); got != "attempt-session-secret" {
				t.Fatalf("callback secret = %q", got)
			}
			return nil
		}); err != nil {
			t.Fatalf("callback secret Use: %v", err)
		}
	})
	if err != nil || !credentialAcquired || !began {
		t.Fatalf("Execute = credential=%v began=%v err=%v", credentialAcquired, began, err)
	}
	if err := saved.Secret.Use(func([]byte) error { t.Fatal("saved call secret remained usable"); return nil }); !errors.Is(err, sdk.ErrSecretUnavailable) {
		t.Fatalf("saved call Secret.Use = %v, want sdk.ErrSecretUnavailable", err)
	}
}

func TestAttemptSessionConcurrentUseHasOneCredentialAndCallback(t *testing.T) {
	preparer, credentials, _, in := newAttemptPreparerFixture(t)
	prepared, err := preparer.Preflight(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	session := prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, credentials)
	var callbacks int32
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, _, err := session.Execute(context.Background(), func(sdk.Client, sdk.Call) { atomic.AddInt32(&callbacks, 1) })
			results <- err
		}()
	}
	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrAttemptSessionUsed) {
			t.Fatalf("concurrent Execute error = %v", err)
		}
	}
	if successes != 1 || atomic.LoadInt32(&callbacks) != 1 {
		t.Fatalf("concurrent successes/callbacks = %d/%d, want 1/1", successes, callbacks)
	}
	if calls, _ := credentials.snapshot(); calls != 1 {
		t.Fatalf("concurrent credential resolutions = %d, want 1", calls)
	}
}

type attemptStreamClient struct{}

func (attemptStreamClient) Stream(context.Context, sdk.StreamCall) (sdk.StreamOpen, error) {
	return sdk.StreamOpen{}, nil
}

type attemptStreamSource struct{ closed atomic.Int32 }

func (s *attemptStreamSource) Next(context.Context) (sdk.StreamEvent, error) {
	return sdk.StreamEvent{}, nil
}
func (s *attemptStreamSource) Close() error { s.closed.Add(1); return nil }

type typedNilAttemptStreamSource struct{}

func (*typedNilAttemptStreamSource) Next(context.Context) (sdk.StreamEvent, error) {
	panic("must not call")
}
func (*typedNilAttemptStreamSource) Close() error { panic("must not call") }

type panickingCloseStreamSource struct{ closed atomic.Int32 }

func (*panickingCloseStreamSource) Next(context.Context) (sdk.StreamEvent, error) {
	return sdk.StreamEvent{}, nil
}
func (s *panickingCloseStreamSource) Close() error { s.closed.Add(1); panic("provider close panic") }

type errorCloseStreamSource struct{ closed atomic.Int32 }

func (*errorCloseStreamSource) Next(context.Context) (sdk.StreamEvent, error) {
	return sdk.StreamEvent{}, nil
}
func (s *errorCloseStreamSource) Close() error {
	s.closed.Add(1)
	return errors.New("raw-provider-close-secret")
}

func TestAttemptPreparerPreflightStreamRequiresOnlyStreamCapability(t *testing.T) {
	preparer, credentials, _, in := newAttemptPreparerFixture(t)
	stream := attemptStreamClient{}
	if err := preparer.registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, stream); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	prepared, err := preparer.PreflightStream(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatalf("PreflightStream: %v", err)
	}
	if prepared.streamClient != stream || prepared.client != nil {
		t.Fatal("PreflightStream did not retain only stream capability")
	}
	if calls, _ := credentials.snapshot(); calls != 0 {
		t.Fatalf("PreflightStream credential resolutions = %d, want 0", calls)
	}
}

func TestAttemptSessionExecuteStreamRevokesSavedCallAndClosesFailedOpen(t *testing.T) {
	preparer, credentials, _, in := newAttemptPreparerFixture(t)
	streamClient := attemptStreamClient{}
	if err := preparer.registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, streamClient); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	prepared, err := preparer.PreflightStream(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatalf("PreflightStream: %v", err)
	}
	session := prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, credentials)
	var saved sdk.StreamCall
	source := &attemptStreamSource{}
	want := errors.New("safe open failure")
	attempt, acquired, began, opened, err := session.ExecuteStream(context.Background(), func(client sdk.StreamClient, call sdk.StreamCall) (sdk.StreamOpen, error) {
		if client != streamClient {
			t.Fatal("wrong stream client")
		}
		saved = call
		if err := call.Secret.Use(func(value []byte) error {
			if string(value) != "attempt-session-secret" {
				t.Fatal("unexpected scoped secret")
			}
			return nil
		}); err != nil {
			t.Fatalf("Secret.Use: %v", err)
		}
		return sdk.StreamOpen{Source: source}, want
	})
	if attempt == (retry.Attempt{}) || !acquired || !began || !errors.Is(err, want) || opened.Source != nil {
		t.Fatalf("ExecuteStream = (%+v, %v, %v, %+v, %v)", attempt, acquired, began, opened, err)
	}
	if got := source.closed.Load(); got != 1 {
		t.Fatalf("failed-open source closes = %d, want 1", got)
	}
	if err := saved.Secret.Use(func([]byte) error { t.Fatal("saved stream secret usable"); return nil }); !errors.Is(err, sdk.ErrSecretUnavailable) {
		t.Fatalf("saved StreamCall Secret.Use = %v", err)
	}
	if calls, _ := credentials.snapshot(); calls != 1 {
		t.Fatalf("credential resolutions = %d, want 1", calls)
	}
	_, acquired, began, _, err = session.ExecuteStream(context.Background(), func(sdk.StreamClient, sdk.StreamCall) (sdk.StreamOpen, error) {
		t.Fatal("reused session opened stream")
		return sdk.StreamOpen{}, nil
	})
	if !errors.Is(err, ErrAttemptSessionUsed) || acquired || began {
		t.Fatalf("reused ExecuteStream = acquired=%v began=%v err=%v", acquired, began, err)
	}
}

func TestAttemptSessionExecuteStreamRejectsTypedNilSourceAndRecoversClosePanic(t *testing.T) {
	preparer, credentials, _, in := newAttemptPreparerFixture(t)
	if err := preparer.registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, attemptStreamClient{}); err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.PreflightStream(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var typedNil *typedNilAttemptStreamSource
	session := prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, credentials)
	_, acquired, began, _, err := session.ExecuteStream(context.Background(), func(sdk.StreamClient, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: typedNil}, nil
	})
	if !acquired || !began || !errors.Is(err, ErrMisconfigured) {
		t.Fatalf("typed-nil source = acquired=%v began=%v err=%v", acquired, began, err)
	}
	panicSource := &panickingCloseStreamSource{}
	session = prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, credentials)
	opening := sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "", "", "")
	_, _, _, _, err = session.ExecuteStream(context.Background(), func(sdk.StreamClient, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: panicSource}, opening
	})
	var classified *sdk.ClassifiedError
	if !errors.Is(err, ErrStreamCleanup) || !errors.As(err, &classified) || classified != opening || panicSource.closed.Load() != 1 {
		t.Fatalf("safe Close failure = err=%v classified=%v closes=%d", err, classified, panicSource.closed.Load())
	}
	errorSource := &errorCloseStreamSource{}
	session = prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, credentials)
	_, _, _, _, err = session.ExecuteStream(context.Background(), func(sdk.StreamClient, sdk.StreamCall) (sdk.StreamOpen, error) {
		return sdk.StreamOpen{Source: errorSource}, errors.New("safe opening failure")
	})
	if !errors.Is(err, ErrStreamCleanup) || strings.Contains(err.Error(), "raw-provider-close-secret") || errorSource.closed.Load() != 1 {
		t.Fatalf("close error leaked or was not normalized: %v", err)
	}
}

func TestAttemptPreparerStreamSharesForeignPlanAndAuthGuards(t *testing.T) {
	resolver, _ := runnerFixture(t)
	_, foreignPlan := runnerFixture(t)
	registry := NewSDKRegistry()
	if _, err := NewAttemptPreparer(resolver, foreignPlan, registry, []byte(`{}`), adapter.ThinkingRequest{}); !errors.Is(err, routing.ErrInvalidPlan) {
		t.Fatalf("foreign plan = %v, want ErrInvalidPlan", err)
	}
	// Stream and complete both use the same auth guard; this is intentionally
	// asserted directly rather than mutating resolver-private compiled state.
	if sdkAuthCompatible(adapter.SDKKindOpenAI, adapter.AuthNone) {
		t.Fatal("OpenAI stream auth unexpectedly compatible with AuthNone")
	}
}

func TestAttemptSessionExecuteStreamConcurrentUseHasOneCredentialAndOpen(t *testing.T) {
	preparer, credentials, _, in := newAttemptPreparerFixture(t)
	if err := preparer.registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, attemptStreamClient{}); err != nil {
		t.Fatalf("RegisterStream: %v", err)
	}
	prepared, err := preparer.PreflightStream(context.Background(), in.Plan.Candidates[0])
	if err != nil {
		t.Fatalf("PreflightStream: %v", err)
	}
	session := prepared.NewAttemptSession(retry.NewState(in.Plan, &fakeClock{}), prepared.preparedAttempt().Retry, credentials)
	var opens atomic.Int32
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, _, _, err := session.ExecuteStream(context.Background(), func(sdk.StreamClient, sdk.StreamCall) (sdk.StreamOpen, error) {
				opens.Add(1)
				return sdk.StreamOpen{}, nil
			})
			results <- err
		}()
	}
	for range 2 {
		err := <-results
		if errors.Is(err, ErrAttemptSessionUsed) || errors.Is(err, ErrMisconfigured) {
			continue
		}
		t.Fatalf("concurrent ExecuteStream error = %v", err)
	}
	if opens.Load() != 1 {
		t.Fatalf("stream opens = %d, want 1", opens.Load())
	}
	if calls, _ := credentials.snapshot(); calls != 1 {
		t.Fatalf("credential resolutions = %d, want 1", calls)
	}
}
