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
