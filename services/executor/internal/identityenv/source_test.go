package identityenv

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/identity"
)

const testMap = `{"first":{"subject":"alice","key_id":"kid-a","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_A"},"second":{"subject":"bob","key_id":"kid-b","role":"admin","status":"disabled","api_key_env":"EXECUTOR_API_KEY_B"}}`

func testLookup(values map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := values[k]; return v, ok }
}
func TestNewFromEnv(t *testing.T) {
	values := map[string]string{IdentityMapEnv: testMap, "EXECUTOR_API_KEY_A": "tm-a", "EXECUTOR_API_KEY_B": "tm-b"}
	if _, err := NewFromEnv(context.Background(), testLookup(values)); err != nil {
		t.Fatal(err)
	}
}

func TestNewFromEnvRejectsUnavailableLookup(t *testing.T) {
	var typedNil func(string) (string, bool)
	for name, lookup := range map[string]func(string) (string, bool){
		"nil":       nil,
		"typed nil": typedNil,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewFromEnv(context.Background(), lookup); err != ErrSourceUnavailable {
				t.Fatalf("error = %v, want ErrSourceUnavailable", err)
			}
		})
	}
}

func TestNewFromEnvRecoversMappingLookupPanic(t *testing.T) {
	const panicValue = "mapping lookup panic must not leak"
	lookup := func(name string) (string, bool) {
		if name == IdentityMapEnv {
			panic(panicValue)
		}
		return "", false
	}

	if _, err := NewFromEnv(context.Background(), lookup); err != ErrSourceUnavailable {
		t.Fatalf("error = %v, want ErrSourceUnavailable", err)
	}
}

func TestNewFromJSONRecoversPerKeyLookupPanic(t *testing.T) {
	const panicValue = "API key lookup panic must not leak"
	lookup := func(name string) (string, bool) {
		if name == "EXECUTOR_API_KEY_A" {
			panic(panicValue)
		}
		return "tm-b", true
	}

	if _, err := NewFromJSON(context.Background(), testMap, lookup); err != ErrKeyUnavailable {
		t.Fatalf("error = %v, want ErrKeyUnavailable", err)
	}
}

func TestSourceAuthenticateRotationAndStatus(t *testing.T) {
	values := map[string]string{"EXECUTOR_API_KEY_A": "tm-first", "EXECUTOR_API_KEY_B": "tm-disabled"}
	s, err := NewFromJSON(context.Background(), testMap, testLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Authenticate(context.Background(), "tm-first")
	if err != nil || got.Subject != "alice" || got.Role != identity.RoleService {
		t.Fatalf("got %#v, %v", got, err)
	}
	values["EXECUTOR_API_KEY_A"] = "tm-rotated"
	if _, err := s.Authenticate(context.Background(), "tm-first"); !errors.Is(err, identity.ErrUnknownKey) {
		t.Fatalf("old key error %v", err)
	}
	if got, err := s.Authenticate(context.Background(), "tm-rotated"); err != nil || got.KeyID != "kid-a" {
		t.Fatalf("rotated got %#v, %v", got, err)
	}
	if _, err := s.Authenticate(context.Background(), "tm-disabled"); !errors.Is(err, identity.ErrKeyDisabled) {
		t.Fatalf("disabled error %v", err)
	}
}
func TestSourceRejectsBadMapsAndValues(t *testing.T) {
	valid := map[string]string{"EXECUTOR_API_KEY_A": "tm-a", "EXECUTOR_API_KEY_B": "tm-b"}
	for _, raw := range []string{"", `[]`, `{"x":{"subject":"a","key_id":"b","role":"service","status":"active","api_key_env":"BAD"}}`, `{"__proto__":{"subject":"a","key_id":"b","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_A"}}`, `{"x":{"subject":"a","key_id":"b","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_A","extra":"x"}}`, `{"x":{"subject":"a","key_id":"b","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_A"},"y":{"subject":"c","key_id":"d","role":"admin","status":"active","api_key_env":"EXECUTOR_API_KEY_A"}}`} {
		if _, err := NewFromJSON(context.Background(), raw, testLookup(valid)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := NewFromJSON(context.Background(), `{"x":{"subject":"a","key_id":"b","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_A"}}`, testLookup(map[string]string{"EXECUTOR_API_KEY_A": "has space"})); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("bad key: %v", err)
	}
}
func TestSourceConcurrent(t *testing.T) {
	var mu sync.RWMutex
	values := map[string]string{"EXECUTOR_API_KEY_A": "tm-a", "EXECUTOR_API_KEY_B": "tm-b"}
	lookup := func(k string) (string, bool) { mu.RLock(); defer mu.RUnlock(); v, ok := values[k]; return v, ok }
	s, err := NewFromJSON(context.Background(), testMap, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		go func() { _, _ = s.Authenticate(context.Background(), "tm-a") }()
	}
}
func FuzzNewFromJSON(f *testing.F) {
	f.Add(testMap)
	f.Add(`{}`)
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = NewFromJSON(context.Background(), raw, testLookup(map[string]string{"EXECUTOR_API_KEY_A": "tm-a", "EXECUTOR_API_KEY_B": "tm-b"}))
	})
}
