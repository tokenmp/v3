package credentialenv

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

const (
	testRef  = "vault://openai-default/credential/default"
	testEnv  = "EXECUTOR_CREDENTIAL_OPENAI_DEFAULT"
	testKey  = "sk-test-value"
	otherRef = "vault://anthropic-default/credential/default"
)

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestNewFromJSONAndResolveRotatesWithoutLeaks(t *testing.T) {
	values := map[string]string{testEnv: testKey}
	resolver, err := NewFromJSON(context.Background(), `{"`+testRef+`":"`+testEnv+`"}`, mapLookup(values))
	if err != nil {
		t.Fatalf("NewFromJSON: %v", err)
	}
	secret, err := resolver.Resolve(context.Background(), testRef)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var received []byte
	if err := secret.Use(func(value []byte) error { received = append([]byte(nil), value...); value[0] = 'X'; return nil }); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if string(received) != testKey {
		t.Fatalf("secret = %q", received)
	}
	if err := secret.Use(func(value []byte) error {
		if string(value) != testKey {
			t.Fatalf("secret changed after callback mutation: %q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var temporary []byte
	if err := secret.Use(func(value []byte) error {
		temporary = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, value := range temporary {
		if value != 0 {
			t.Fatal("temporary SDK credential copy was not cleared")
		}
	}
	values[testEnv] = "sk-rotated"
	rotated, err := resolver.Resolve(context.Background(), testRef)
	if err != nil {
		t.Fatalf("rotated Resolve: %v", err)
	}
	if err := rotated.Use(func(value []byte) error {
		if string(value) != "sk-rotated" {
			t.Fatalf("rotated secret = %q", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []any{resolver, *resolver} {
		for _, rendered := range []string{fmt.Sprint(subject), fmt.Sprintf("%+v", subject), fmt.Sprintf("%#v", subject)} {
			if strings.Contains(rendered, testRef) || strings.Contains(rendered, testEnv) || strings.Contains(rendered, testKey) {
				t.Fatalf("resolver leaked private data: %q", rendered)
			}
		}
	}
}

func TestNewFromJSONRejectsUnsafeAndMalformedMappings(t *testing.T) {
	cases := []string{
		``, `[]`, `{"` + testRef + `":1}`, `{"` + testRef + `":"PATH"}`,
		`{"https://provider/credential/default":"` + testEnv + `"}`,
		`{"vault://user@provider/credential/default":"` + testEnv + `"}`,
		`{"vault://provider/credential/default?token=x":"` + testEnv + `"}`,
		`{"` + testRef + `":"` + testEnv + `","` + testRef + `":"` + testEnv + `"}`,
		`{"__proto__":"` + testEnv + `"}`, `{"` + testRef + `":"` + testEnv + `"} trailing`,
	}
	for _, raw := range cases {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			_, err := NewFromJSON(context.Background(), raw, mapLookup(nil))
			if !errors.Is(err, ErrMappingMalformed) {
				t.Fatalf("error = %v, want malformed", err)
			}
			if errors.Unwrap(err) != nil {
				t.Fatalf("error wraps: %v", err)
			}
		})
	}
	over := `{"` + testRef + `":"` + testEnv + `"}` + strings.Repeat(" ", MaxMappingBytes)
	_, err := NewFromJSON(context.Background(), over, mapLookup(nil))
	if !errors.Is(err, ErrMappingTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestResolverHandlesNilAndPanickingLookup(t *testing.T) {
	var nilResolver *Resolver
	if _, err := nilResolver.Resolve(context.Background(), testRef); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("nil resolver error = %v", err)
	}
	resolver, err := NewFromJSON(context.Background(), `{"`+testRef+`":"`+testEnv+`"}`, func(string) (string, bool) { panic("private") })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), testRef); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("panic lookup Resolve error = %v", err)
	}
	if err := resolver.ValidateCompiled(compiledForValidation(adapter.AuthBearerHeader, []adapter.CompiledRoute{{AdapterID: "a", Enabled: true, Credentials: []adapter.CompiledCredential{{CredentialRef: testRef, Enabled: true}}}})); !errors.Is(err, ErrSnapshotInvalid) {
		t.Fatalf("panic lookup validation error = %v", err)
	}
}

func TestResolveSafeFailuresContextAndSecretBounds(t *testing.T) {
	resolver, err := NewFromJSON(context.Background(), `{"`+testRef+`":"`+testEnv+`"}`, mapLookup(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"vault://unknown/credential/default", testRef} {
		_, got := resolver.Resolve(context.Background(), ref)
		if got == nil {
			t.Fatal("Resolve unexpectedly succeeded")
		}
		if strings.Contains(got.Error(), ref) || strings.Contains(got.Error(), testEnv) {
			t.Fatalf("error leaked private data: %v", got)
		}
		if errors.Unwrap(got) != nil {
			t.Fatalf("error wraps: %v", got)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolver.Resolve(cancelled, testRef)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Resolve = %v", err)
	}
	for _, secret := range []string{"", "bad\nsecret", "bad secret", "秘密", string([]byte{0xff}), strings.Repeat("a", MaxSecretBytes+1)} {
		bad, err := NewFromJSON(context.Background(), `{"`+testRef+`":"`+testEnv+`"}`, mapLookup(map[string]string{testEnv: secret}))
		if err != nil {
			t.Fatal(err)
		}
		_, err = bad.Resolve(context.Background(), testRef)
		if !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatalf("secret bound error = %v", err)
		}
	}
}

func compiledForValidation(auth adapter.AuthKind, routes []adapter.CompiledRoute) adapter.CompiledConfig {
	return adapter.CompiledConfig{Adapters: map[string]adapter.CompiledAdapter{"a": {ID: "a", Auth: adapter.AuthRule{Kind: auth}}}, Routes: routes}
}

func TestValidateCompiledExactEnabledBindings(t *testing.T) {
	resolver, err := NewFromJSON(context.Background(), `{"`+testRef+`":"`+testEnv+`","`+otherRef+`":"`+testEnv+`"}`, mapLookup(map[string]string{testEnv: testKey}))
	if err != nil {
		t.Fatal(err)
	}
	compiled := compiledForValidation(adapter.AuthBearerHeader, []adapter.CompiledRoute{{ID: "one", AdapterID: "a", Enabled: true, Credentials: []adapter.CompiledCredential{{CredentialRef: testRef, Enabled: true}}}, {ID: "two", AdapterID: "a", Enabled: true, Credentials: []adapter.CompiledCredential{{CredentialRef: otherRef, Enabled: true}}}, {ID: "disabled", AdapterID: "a", Credentials: []adapter.CompiledCredential{{CredentialRef: "vault://unused/credential/default", Enabled: true}}}})
	if err := resolver.ValidateCompiled(compiled); err != nil {
		t.Fatalf("ValidateCompiled: %v", err)
	}
	for _, broken := range []adapter.CompiledConfig{
		compiledForValidation(adapter.AuthBearerHeader, []adapter.CompiledRoute{{AdapterID: "a", Enabled: true, Credentials: []adapter.CompiledCredential{{CredentialRef: testRef, Enabled: true}}}}),
		compiledForValidation(adapter.AuthBearerHeader, []adapter.CompiledRoute{{AdapterID: "a", Enabled: true}}),
		compiledForValidation(adapter.AuthBearerHeader, []adapter.CompiledRoute{{AdapterID: "missing", Enabled: true, Credentials: []adapter.CompiledCredential{{CredentialRef: testRef, Enabled: true}}}}),
	} {
		if err := resolver.ValidateCompiled(broken); !errors.Is(err, ErrSnapshotInvalid) {
			t.Fatalf("ValidateCompiled error = %v", err)
		}
	}
	noAuth, err := NewFromJSON(context.Background(), `{}`, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := noAuth.ValidateCompiled(compiledForValidation(adapter.AuthNone, []adapter.CompiledRoute{{AdapterID: "a", Enabled: true}})); err != nil {
		t.Fatalf("AuthNone validation: %v", err)
	}
}

func TestResolverConcurrentResolve(t *testing.T) {
	var mu sync.RWMutex
	value := testKey
	resolver, err := NewFromJSON(context.Background(), `{"`+testRef+`":"`+testEnv+`"}`, func(string) (string, bool) { mu.RLock(); defer mu.RUnlock(); return value, true })
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				secret, err := resolver.Resolve(context.Background(), testRef)
				if err != nil {
					t.Errorf("Resolve: %v", err)
					return
				}
				_ = secret.Use(func([]byte) error { return nil })
			}
		}()
	}
	mu.Lock()
	value = "sk-new"
	mu.Unlock()
	wg.Wait()
}

func FuzzNewFromJSON(f *testing.F) {
	f.Add(`{"vault://provider/credential/default":"EXECUTOR_CREDENTIAL_PROVIDER"}`)
	f.Add(`{"vault://provider/credential/default":"PATH"}`)
	f.Fuzz(func(t *testing.T, raw string) {
		resolver, err := NewFromJSON(context.Background(), raw, mapLookup(map[string]string{"EXECUTOR_CREDENTIAL_PROVIDER": testKey}))
		if err == nil {
			_, _ = resolver.Resolve(context.Background(), testRef)
		}
	})
}
