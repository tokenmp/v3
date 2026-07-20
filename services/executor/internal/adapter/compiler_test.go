package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func intp(v int) *int           { return &v }
func floatp(v float64) *float64 { return &v }
func baseInput() ConfigInput {
	return ConfigInput{
		Revision:  "r1",
		Models:    map[string]ModelInput{"m": {ID: "m", Capabilities: []Capability{CapabilityChat}}},
		Providers: map[string]ProviderInput{"p": {ID: "p", Name: "provider", BaseURL: "https://provider.example/v1", SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat}},
		Adapters:  map[string]AdapterConfig{"a": {ID: "a", Name: "adapter", Version: 1, SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat, Auth: AuthRule{Kind: AuthNone}}},
		Routes:    []RouteInput{{ID: "r", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "upstream", Enabled: true, Protocol: ProtocolOpenAIChat}},
	}
}
func mustCompile(t *testing.T, in ConfigInput) CompiledConfig {
	t.Helper()
	got, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return got
}
func mustFail(t *testing.T, in ConfigInput, part string) {
	t.Helper()
	_, err := Compile(in)
	if err == nil || !strings.Contains(err.Error(), part) {
		t.Fatalf("Compile error = %v, want containing %q", err, part)
	}
}

func TestCompileAppliesDefaultsLimitsAndRelationships(t *testing.T) {
	got := mustCompile(t, baseInput())
	r := got.Routes[0]
	if r.Retry.MaxTotalAttempts != DefaultMaxTotalAttempts || r.Retry.MaxSameTargetAttempts != DefaultMaxSameTargetAttempts {
		t.Fatalf("retry defaults = %#v", r.Retry)
	}
	if r.Timeout.Request != DefaultRequestTimeout || r.Timeout.TTFT > r.Timeout.Request || r.Timeout.StreamIdle > r.Timeout.StreamMaxLifetime {
		t.Fatalf("timeout defaults/relationship = %#v", r.Timeout)
	}
	in := baseInput()
	in.Routes[0].Retry.MaxTotalAttempts = intp(0)
	got = mustCompile(t, in)
	if got.Routes[0].Retry.MaxTotalAttempts != 0 || got.Routes[0].Retry.MaxSameTargetAttempts != 0 {
		t.Fatal("zero total attempts must disable retries")
	}
	in = baseInput()
	in.Routes[0].Retry.MaxTotalAttempts = intp(HardMaxTotalAttempts + 1)
	mustFail(t, in, "attempts exceed bounds")
	in = baseInput()
	in.Routes[0].Timeout.TTFTTimeout = "121s"
	mustFail(t, in, "timeouts must satisfy")
	in = baseInput()
	in.Routes[0].Timeout.StreamIdleTimeout = "40s"
	in.Routes[0].Timeout.StreamMaxLifetime = "30s"
	mustFail(t, in, "timeouts must satisfy")
	in = baseInput()
	in.Routes[0].Timeout.RequestTimeout = "31m"
	mustFail(t, in, "hard limit")
}

func TestCompileRejectsIdentityReferencesAndFallbackCycles(t *testing.T) {
	in := baseInput()
	in.Revision = " "
	mustFail(t, in, "revision")
	in = baseInput()
	in.Adapters["a"] = AdapterConfig{ID: "other", Name: "adapter", Version: 1, SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat, Auth: AuthRule{Kind: AuthNone}}
	mustFail(t, in, "adapter")
	in = baseInput()
	in.Routes[0].ProviderID = "missing"
	mustFail(t, in, "unknown provider")
	in = baseInput()
	in.Routes = append(in.Routes, in.Routes[0])
	mustFail(t, in, "duplicate")
	in = baseInput()
	in.Routes = append(in.Routes, RouteInput{ID: "r2", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "two", Protocol: ProtocolOpenAIChat, FallbackRouteIDs: []string{"r"}})
	in.Routes[0].FallbackRouteIDs = []string{"r2"}
	mustFail(t, in, "fallback cycle")
}

func TestCompileThinkingAndFiniteRequestDSL(t *testing.T) {
	in := baseInput()
	a := in.Adapters["a"]
	a.Thinking = ThinkingPolicy{Supported: true, DefaultEffort: ThinkingLow, EffortMapping: map[ThinkingEffort]ThinkingEffort{ThinkingNone: ThinkingNone}}
	in.Adapters["a"] = a
	mustFail(t, in, "missing mapping")
	in = baseInput()
	a = in.Adapters["a"]
	a.Request = RequestPolicy{AllowedHeaders: []string{"X-Safe"}, Rules: []RequestRule{{ID: "h", Action: RequestSetHeader, Path: "/x", Name: "X-Unsafe"}}}
	in.Adapters["a"] = a
	mustFail(t, in, "not allowlisted")
	in = baseInput()
	a = in.Adapters["a"]
	a.Request = RequestPolicy{Rules: []RequestRule{{ID: "p", Action: RequestSet, Path: "/bad~2escape", Value: []byte(`true`)}}}
	in.Adapters["a"] = a
	mustFail(t, in, "invalid JSON pointer")
}

func TestCompileSortsConflictsAndClones(t *testing.T) {
	in := baseInput()
	a := in.Adapters["a"]
	a.Response.Rules = []ResponseRule{{ID: "late", Priority: 20, Output: ResponseOutput{HTTPStatus: 500}}, {ID: "early", Priority: 10, Output: ResponseOutput{HTTPStatus: 429}}}
	a.Retry.Rules = []RetryRule{{ID: "late", Priority: 20, Action: RetryNextRoute}, {ID: "early", Priority: 10, Action: RetryNextCredential}}
	in.Adapters["a"] = a
	got := mustCompile(t, in)
	compiled := got.Adapters["a"]
	if compiled.ResponseRules[0].ID != "early" || compiled.Retry.Rules[0].ID != "early" {
		t.Fatal("rules were not priority sorted")
	}
	in.Adapters["a"] = AdapterConfig{}
	clone := CloneCompiledConfig(got)
	clone.Adapters["a"] = CompiledAdapter{}
	if got.Adapters["a"].ID == "" {
		t.Fatal("clone mutation leaked")
	}
}

func TestCompileRetriesRespectPrecedenceAndPositiveDurations(t *testing.T) {
	in := baseInput()
	in.Global.Retry = RetryPolicy{MaxTotalAttempts: intp(2), Backoff: "1s"}
	in.Providers["p"] = ProviderInput{ID: "p", Name: "provider", BaseURL: "https://provider.example/v1", SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat, Retry: RetryPolicy{MaxTotalAttempts: intp(3)}}
	got := mustCompile(t, in)
	if got.Routes[0].Retry.MaxTotalAttempts != 3 || got.Routes[0].Retry.Backoff != time.Second {
		t.Fatalf("precedence = %#v", got.Routes[0].Retry)
	}
	in = baseInput()
	in.Global.Retry.MaxTotalDuration = "0s"
	mustFail(t, in, "must be positive")
}

func TestCompileC02EmptyConfigProducesNoRoutes(t *testing.T) {
	got := mustCompile(t, ConfigInput{Revision: "empty"})
	if len(got.Routes) != 0 || len(got.Models) != 0 || len(got.Providers) != 0 || len(got.Adapters) != 0 {
		t.Fatalf("empty compilation = %#v", got)
	}
}

func TestCompileC03ToC12IdentityRulesAndDSLGuards(t *testing.T) {
	t.Run("unknown SDK", func(t *testing.T) {
		in := baseInput()
		p := in.Providers["p"]
		p.SDKKind = "unknown"
		in.Providers["p"] = p
		mustFail(t, in, "invalid provider")
	})
	t.Run("duplicate request ID", func(t *testing.T) {
		in := baseInput()
		a := in.Adapters["a"]
		a.Request.Rules = []RequestRule{{ID: "x", Action: RequestSet, Path: "/temperature", Value: []byte("1")}, {ID: "x", Action: RequestSet, Path: "/top_p", Value: []byte("1")}}
		in.Adapters["a"] = a
		mustFail(t, in, "duplicate")
	})
	t.Run("same path writes conflict", func(t *testing.T) {
		in := baseInput()
		a := in.Adapters["a"]
		a.Request.Rules = []RequestRule{{ID: "x", Action: RequestSet, Path: "/temperature", Value: []byte("1")}, {ID: "y", Action: RequestSet, Path: "/temperature", Value: []byte("2")}}
		in.Adapters["a"] = a
		mustFail(t, in, "write")
	})
	t.Run("rename source conflicts with other consumers", func(t *testing.T) {
		for _, other := range []RequestRule{
			{ID: "set", Action: RequestSet, Path: "/source", Value: []byte(`true`)},
			{ID: "remove", Action: RequestRemove, Path: "/source"},
			{ID: "map", Action: RequestMapEnum, Path: "/source", EnumMap: map[string]string{"cold": "warm"}},
			{ID: "clamp", Action: RequestClampNumber, Path: "/source", Min: floatp(0), Max: floatp(1)},
			{ID: "second-rename", Action: RequestRename, From: "/source", To: "/other"},
		} {
			in := baseInput()
			a := in.Adapters["a"]
			a.Request.Rules = []RequestRule{{ID: "first-rename", Action: RequestRename, From: "/source", To: "/destination"}, other}
			in.Adapters["a"] = a
			mustFail(t, in, "write")
		}
	})
	t.Run("rename permits distinct source and destination paths", func(t *testing.T) {
		in := baseInput()
		a := in.Adapters["a"]
		a.Request.Rules = []RequestRule{
			{ID: "first", Action: RequestRename, From: "/first-source", To: "/first-destination"},
			{ID: "second", Action: RequestRename, From: "/second-source", To: "/second-destination"},
			{ID: "set", Action: RequestSet, Path: "/set", Value: []byte(`true`)},
			{ID: "remove", Action: RequestRemove, Path: "/remove"},
			{ID: "map", Action: RequestMapEnum, Path: "/map", EnumMap: map[string]string{"cold": "warm"}},
			{ID: "clamp", Action: RequestClampNumber, Path: "/clamp", Min: floatp(0), Max: floatp(1)},
		}
		in.Adapters["a"] = a
		mustCompile(t, in)
	})
	t.Run("protected paths cannot mutate", func(t *testing.T) {
		for _, action := range []RequestAction{RequestRemove, RequestSet} {
			in := baseInput()
			a := in.Adapters["a"]
			a.Request.Rules = []RequestRule{{ID: "x", Action: action, Path: "/model/child", Value: []byte(`"x"`)}}
			in.Adapters["a"] = a
			mustFail(t, in, "protected")
		}
	})
	t.Run("copy and rename reject protected source and destination paths", func(t *testing.T) {
		for _, action := range []RequestAction{RequestCopy, RequestRename} {
			for _, paths := range [][2]string{{"/model/child", "/name"}, {"/name", "/messages/0"}} {
				in := baseInput()
				a := in.Adapters["a"]
				a.Request.Rules = []RequestRule{{ID: "x", Action: action, From: paths[0], To: paths[1]}}
				in.Adapters["a"] = a
				mustFail(t, in, "protected")
			}
		}
	})
	t.Run("value references fail closed", func(t *testing.T) {
		for _, action := range []RequestAction{RequestSet, RequestCopy, RequestRemove, RequestRename, RequestMapEnum, RequestClampNumber, RequestSetHeader, RequestSetQuery} {
			in := baseInput()
			a := in.Adapters["a"]
			a.Request.Rules = []RequestRule{{ID: "x", Action: action, ValueRef: "future-resolver"}}
			in.Adapters["a"] = a
			mustFail(t, in, "unsupported value reference")
		}
	})
	t.Run("denylist overrides allowlist after normalization", func(t *testing.T) {
		for _, name := range []string{
			"Host", "Authorization", "Content-Length", "Proxy-Authorization", "X-Forwarded-For", "X-SDK-Control",
			"X-ApiKey", "X_Api_Key", "X-AccessKey", "X.Access_Key", "X-PrivateKey", "X_private-key",
			"X-Secret", "X.Secret_Value", "X-Token", "X_token_value", "X-Credential", "X.Credential_Value",
		} {
			in := baseInput()
			a := in.Adapters["a"]
			a.Request.AllowedHeaders = []string{name}
			a.Request.Rules = []RequestRule{{ID: "x", Action: RequestSetHeader, Name: name}}
			in.Adapters["a"] = a
			mustFail(t, in, "allowlisted")
		}
	})
	t.Run("query remains case sensitive", func(t *testing.T) {
		in := baseInput()
		a := in.Adapters["a"]
		a.Request.AllowedQuery = []string{"APIKey"}
		a.Request.Rules = []RequestRule{{ID: "x", Action: RequestSetQuery, Name: "apikey"}}
		in.Adapters["a"] = a
		mustFail(t, in, "allowlisted")
	})
	t.Run("RFC6901 and bounded pointers", func(t *testing.T) {
		in := baseInput()
		a := in.Adapters["a"]
		a.Request.Rules = []RequestRule{{ID: "x", Action: RequestSet, Path: "/a~1b/~0c", Value: []byte("true")}}
		in.Adapters["a"] = a
		mustCompile(t, in)
		in = baseInput()
		a = in.Adapters["a"]
		a.Request.Rules = []RequestRule{{ID: "x", Action: RequestSet, Path: "/a~2", Value: []byte("true")}}
		in.Adapters["a"] = a
		mustFail(t, in, "JSON pointer")
	})
}

func TestCompileC13ToC22ThinkingTimeoutRetryAndPrecedence(t *testing.T) {
	t.Run("thinking output and budgets constrained by model", func(t *testing.T) {
		in := baseInput()
		m := in.Models["m"]
		m.Thinking = ThinkingInput{Supported: true, DefaultEffort: ThinkingLow, MaxEffort: ThinkingMedium, MinBudgetToken: 1, MaxBudgetToken: 10}
		in.Models["m"] = m
		a := in.Adapters["a"]
		a.Thinking = ThinkingPolicy{Supported: true, DefaultEffort: ThinkingLow, EffortMapping: fullEffortMap(ThinkingHigh), BudgetMapping: map[ThinkingEffort]int{ThinkingLow: 10}, MaxBudgetToken: 10}
		in.Adapters["a"] = a
		mustFail(t, in, "unsupported by model")
	})
	t.Run("durations reject negative zero and hard max", func(t *testing.T) {
		for _, d := range []RawDuration{"-1s", "0s"} {
			in := baseInput()
			in.Routes[0].Timeout.RequestTimeout = d
			mustFail(t, in, "positive")
		}
		in := baseInput()
		in.Routes[0].Timeout.RequestTimeout = "31m"
		mustFail(t, in, "hard limit")
	})
	t.Run("defaults match docs and explicit retry zero survives inheritance", func(t *testing.T) {
		in := baseInput()
		in.Global.Retry.MaxTotalAttempts = intp(4)
		a := in.Adapters["a"]
		a.Retry.MaxTotalAttempts = intp(0)
		in.Adapters["a"] = a
		got := mustCompile(t, in).Routes[0]
		if got.Retry.MaxTotalAttempts != 0 || got.Retry.MaxSameTargetAttempts != 0 {
			t.Fatalf("retry disable lost: %#v", got.Retry)
		}
		if got.Timeout.TTFT != 45*time.Second || got.Timeout.StreamMaxLifetime != 10*time.Minute || got.Retry.MaxTotalDuration != 90*time.Second || got.Retry.Backoff != 200*time.Millisecond {
			t.Fatalf("defaults=%#v %#v", got.Timeout, got.Retry)
		}
	})
	t.Run("code global adapter provider route precedence", func(t *testing.T) {
		in := baseInput()
		in.Global.Retry.MaxTotalAttempts = intp(4)
		a := in.Adapters["a"]
		a.Retry.MaxTotalAttempts = intp(5)
		in.Adapters["a"] = a
		p := in.Providers["p"]
		p.Retry.MaxTotalAttempts = intp(6)
		in.Providers["p"] = p
		in.Routes[0].Retry.MaxTotalAttempts = intp(7)
		if got := mustCompile(t, in).Routes[0].Retry.MaxTotalAttempts; got != 7 {
			t.Fatalf("got %d", got)
		}
	})
}

func fullEffortMap(to ThinkingEffort) map[ThinkingEffort]ThinkingEffort {
	return map[ThinkingEffort]ThinkingEffort{ThinkingNone: to, ThinkingMinimal: to, ThinkingLow: to, ThinkingMedium: to, ThinkingHigh: to, ThinkingXHigh: to, ThinkingMax: to}
}

func TestCompileC23ToC27FallbackDeterminismAndNoAliases(t *testing.T) {
	t.Run("deep fallback is iterative", func(t *testing.T) {
		in := baseInput()
		const n = 2000
		in.Routes = make([]RouteInput, n)
		for i := range in.Routes {
			id := fmt.Sprintf("r%d", i)
			in.Routes[i] = RouteInput{ID: id, ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "up", Protocol: ProtocolOpenAIChat}
			if i+1 < n {
				in.Routes[i].FallbackRouteIDs = []string{fmt.Sprintf("r%d", i+1)}
			}
		}
		mustCompile(t, in)
	})
	t.Run("response deterministic sort and true collision", func(t *testing.T) {
		in := baseInput()
		a := in.Adapters["a"]
		a.Response.Rules = []ResponseRule{{ID: "b", Priority: 1, Match: ResponseMatch{HTTPStatuses: []int{500}}, Output: ResponseOutput{HTTPStatus: 500}}, {ID: "a", Priority: 1, Match: ResponseMatch{ErrorCodes: []string{"x"}}, Output: ResponseOutput{HTTPStatus: 500}}}
		in.Adapters["a"] = a
		if got := mustCompile(t, in).Adapters["a"].ResponseRules; got[0].ID != "a" {
			t.Fatalf("sort=%#v", got)
		}
		a.Response.Rules = append(a.Response.Rules, ResponseRule{ID: "c", Priority: 1, Match: ResponseMatch{ErrorCodes: []string{"x"}}, Output: ResponseOutput{HTTPStatus: 500}})
		in.Adapters["a"] = a
		mustFail(t, in, "conflicting")
	})
	t.Run("raw mutation cannot alias output", func(t *testing.T) {
		in := baseInput()
		got := mustCompile(t, in)
		in.Models["m"].Capabilities[0] = CapabilityImages
		if got.Models["m"].Capabilities[0] != CapabilityChat {
			t.Fatal("raw alias leaked")
		}
	})
}

func TestCompileC24NestedAliasesAreFullyDetached(t *testing.T) {
	min, max := 0.1, 0.9
	in := baseInput()
	model := in.Models["m"]
	model.Capabilities = []Capability{CapabilityChat, CapabilityTools}
	in.Models["m"] = model
	provider := in.Providers["p"]
	provider.Retry.Rules = []RetryRule{{ID: "provider", Priority: 1, HTTPStatuses: []int{503}, ErrorCodes: []string{"busy"}, ErrorTypes: []string{"temporary"}, Action: RetryNextRoute}}
	in.Providers["p"] = provider
	a := in.Adapters["a"]
	a.Capability = CapabilityPolicy{Require: []Capability{CapabilityChat}, Deny: []Capability{CapabilityImages}}
	a.Thinking = ThinkingPolicy{EffortMapping: map[ThinkingEffort]ThinkingEffort{ThinkingLow: ThinkingMinimal}, BudgetMapping: map[ThinkingEffort]int{ThinkingLow: 7}}
	a.Request = RequestPolicy{AllowedHeaders: []string{"X-Safe"}, AllowedQuery: []string{"mode"}, Rules: []RequestRule{{ID: "set", Action: RequestSet, Path: "/temperature", Value: []byte("1"), EnumMap: map[string]string{"warm": "hot"}, Min: &min, Max: &max}}}
	a.Response.Rules = []ResponseRule{{ID: "response", Priority: 1, Match: ResponseMatch{HTTPStatuses: []int{503}, ErrorCodes: []string{"busy"}, ErrorTypes: []string{"temporary"}, MessageContains: []string{"retry"}, FinishReasons: []string{"length"}, StreamEventTypes: []string{"error"}}, Output: ResponseOutput{HTTPStatus: 503}}}
	a.Retry.Rules = []RetryRule{{ID: "adapter", Priority: 1, HTTPStatuses: []int{429}, ErrorCodes: []string{"rate"}, ErrorTypes: []string{"limited"}, Action: RetryNextCredential}}
	in.Adapters["a"] = a
	in.Routes[0].FallbackRouteIDs = []string{"r2"}
	in.Routes = append(in.Routes, RouteInput{ID: "r2", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "fallback", Protocol: ProtocolOpenAIChat, Retry: RetryPolicy{Rules: []RetryRule{{ID: "route", Priority: 1, HTTPStatuses: []int{500}, ErrorCodes: []string{"retry"}, ErrorTypes: []string{"temporary"}, Action: RetryNextProvider}}}})

	compiled := mustCompile(t, in)
	in.Models["m"] = ModelInput{ID: "m", Capabilities: []Capability{CapabilityImages}}
	provider.Retry.Rules[0].HTTPStatuses[0] = 418
	in.Providers["p"] = provider
	a.Capability.Require[0] = CapabilityImages
	a.Thinking.EffortMapping[ThinkingLow] = ThinkingMax
	a.Thinking.BudgetMapping[ThinkingLow] = 99
	a.Request.AllowedHeaders[0] = "X-Mutated"
	a.Request.AllowedQuery[0] = "mutated"
	a.Request.Rules[0].Value[0] = '9'
	a.Request.Rules[0].EnumMap["warm"] = "cold"
	*a.Request.Rules[0].Min = 0.2
	*a.Request.Rules[0].Max = 0.8
	a.Response.Rules[0].Match.HTTPStatuses[0] = 418
	a.Response.Rules[0].Match.ErrorCodes[0] = "mutated"
	a.Response.Rules[0].Match.ErrorTypes[0] = "mutated"
	a.Response.Rules[0].Match.MessageContains[0] = "mutated"
	a.Response.Rules[0].Match.FinishReasons[0] = "mutated"
	a.Response.Rules[0].Match.StreamEventTypes[0] = "mutated"
	a.Retry.Rules[0].HTTPStatuses[0] = 418
	a.Retry.Rules[0].ErrorCodes[0] = "mutated"
	a.Retry.Rules[0].ErrorTypes[0] = "mutated"
	in.Adapters["a"] = a
	in.Routes[0].FallbackRouteIDs[0] = "mutated"
	in.Routes[1].Retry.Rules[0].HTTPStatuses[0] = 418

	got := compiled.Adapters["a"]
	if compiled.Models["m"].Capabilities[0] != CapabilityChat || compiled.Providers["p"].Retry.Rules[0].HTTPStatuses[0] != 503 || got.Capability.Require[0] != CapabilityChat || got.Thinking.EffortMapping[ThinkingLow] != ThinkingMinimal || got.Thinking.BudgetMapping[ThinkingLow] != 7 || got.Request.AllowedHeaders[0] != "X-Safe" || got.Request.AllowedQuery[0] != "mode" || string(got.Request.Rules[0].Value) != "1" || got.Request.Rules[0].EnumMap["warm"] != "hot" || *got.Request.Rules[0].Min != 0.1 || *got.Request.Rules[0].Max != 0.9 || got.ResponseRules[0].Match.HTTPStatuses[0] != 503 || got.ResponseRules[0].Match.ErrorCodes[0] != "busy" || got.ResponseRules[0].Match.ErrorTypes[0] != "temporary" || got.ResponseRules[0].Match.MessageContains[0] != "retry" || got.ResponseRules[0].Match.FinishReasons[0] != "length" || got.ResponseRules[0].Match.StreamEventTypes[0] != "error" || got.Retry.Rules[0].HTTPStatuses[0] != 429 || compiled.Routes[0].FallbackRouteIDs[0] != "r2" || compiled.Routes[1].Retry.Rules[0].HTTPStatuses[0] != 500 {
		t.Fatalf("raw mutation leaked into compiled config: %#v", compiled)
	}

	clone := CloneCompiledConfig(compiled)
	clone.Adapters["a"].Request.Rules[0].EnumMap["warm"] = "clone-mutation"
	clone.Routes[0].FallbackRouteIDs[0] = "clone-mutation"
	if compiled.Adapters["a"].Request.Rules[0].EnumMap["warm"] != "hot" || compiled.Routes[0].FallbackRouteIDs[0] != "r2" {
		t.Fatal("CloneCompiledConfig retained a nested alias")
	}
}

func TestCompileC27InputPermutationsAreDeepEqual(t *testing.T) {
	in := baseInput()
	in.Models["z"] = ModelInput{ID: "z", Capabilities: []Capability{CapabilityChat}}
	in.Providers["q"] = ProviderInput{ID: "q", Name: "q-provider", BaseURL: "https://q.example/v1", SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat}
	in.Adapters["b"] = AdapterConfig{ID: "b", Name: "b-adapter", Version: 1, SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat, Auth: AuthRule{Kind: AuthNone}}
	in.Routes = append(in.Routes, RouteInput{ID: "z-route", ModelID: "z", ProviderID: "q", AdapterID: "b", UpstreamModel: "z", Priority: 1, Protocol: ProtocolOpenAIChat})
	want := mustCompile(t, in)
	for i := 0; i < 50; i++ {
		permuted := baseInput()
		permuted.Models = map[string]ModelInput{"z": in.Models["z"], "m": in.Models["m"]}
		permuted.Providers = map[string]ProviderInput{"q": in.Providers["q"], "p": in.Providers["p"]}
		permuted.Adapters = map[string]AdapterConfig{"b": in.Adapters["b"], "a": in.Adapters["a"]}
		permuted.Routes = []RouteInput{in.Routes[1], in.Routes[0]}
		if got := mustCompile(t, permuted); !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d compiled non-deterministically\n got: %#v\nwant: %#v", i, got, want)
		}
	}
}

func TestCompileRetryZeroOverridesAtEveryLayer(t *testing.T) {
	for _, layer := range []string{"global", "adapter", "provider", "route"} {
		t.Run(layer, func(t *testing.T) {
			in := baseInput()
			switch layer {
			case "global":
				in.Global.Retry = RetryPolicy{MaxTotalAttempts: intp(0), MaxSameTargetAttempts: intp(4)}
			case "adapter":
				in.Global.Retry = RetryPolicy{MaxTotalAttempts: intp(4), MaxSameTargetAttempts: intp(3)}
				a := in.Adapters["a"]
				a.Retry = RetryPolicy{MaxTotalAttempts: intp(0), MaxSameTargetAttempts: intp(4)}
				in.Adapters["a"] = a
			case "provider":
				in.Global.Retry = RetryPolicy{MaxTotalAttempts: intp(4), MaxSameTargetAttempts: intp(3)}
				a := in.Adapters["a"]
				a.Retry = RetryPolicy{MaxTotalAttempts: intp(5), MaxSameTargetAttempts: intp(4)}
				in.Adapters["a"] = a
				p := in.Providers["p"]
				p.Retry = RetryPolicy{MaxTotalAttempts: intp(0), MaxSameTargetAttempts: intp(5)}
				in.Providers["p"] = p
			case "route":
				in.Global.Retry = RetryPolicy{MaxTotalAttempts: intp(4), MaxSameTargetAttempts: intp(3)}
				a := in.Adapters["a"]
				a.Retry = RetryPolicy{MaxTotalAttempts: intp(5), MaxSameTargetAttempts: intp(4)}
				in.Adapters["a"] = a
				p := in.Providers["p"]
				p.Retry = RetryPolicy{MaxTotalAttempts: intp(6), MaxSameTargetAttempts: intp(5)}
				in.Providers["p"] = p
				in.Routes[0].Retry = RetryPolicy{MaxTotalAttempts: intp(0), MaxSameTargetAttempts: intp(5)}
			}
			got := mustCompile(t, in).Routes[0].Retry
			if got.MaxTotalAttempts != 0 || got.MaxSameTargetAttempts != 0 {
				t.Fatalf("%s retry zero did not disable retries and reset same-target attempts: %#v", layer, got)
			}
		})
	}
}

func FuzzCompile(f *testing.F) {
	f.Add("https://provider.example/v1", "/temperature")
	f.Fuzz(func(t *testing.T, baseURL, path string) {
		in := baseInput()
		p := in.Providers["p"]
		p.BaseURL = baseURL
		in.Providers["p"] = p
		a := in.Adapters["a"]
		a.Request.Rules = []RequestRule{{ID: "f", Action: RequestSet, Path: path, Value: []byte("true")}}
		in.Adapters["a"] = a
		_, _ = Compile(in)
	})
}

func TestCompileRoutingCandidates(t *testing.T) {
	t.Run("normalizes selectors credentials and candidate order", func(t *testing.T) {
		in := baseInput()
		in.Global.AutoModelIDs = []string{"m", "z"}
		in.Models["m"] = ModelInput{ID: "m", Capabilities: []Capability{CapabilityChat}, FallbackModelIDs: []string{"z"}}
		in.Models["z"] = ModelInput{ID: "z", Capabilities: []Capability{CapabilityChat}}
		in.Providers["p"] = ProviderInput{ID: "p", Name: "provider", BaseURL: "https://provider.example/v1", SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat}
		in.Routes[0].RouteGroup = "primary"
		in.Routes[0].Credentials = []CredentialInput{{ID: "secondary", CredentialRef: "vault://provider/secondary", Priority: 2, Enabled: true}, {ID: "primary", CredentialRef: "vault://provider/primary", Priority: 1, Enabled: true}}
		in.Adapters["a"] = AdapterConfig{ID: "a", Name: "adapter", Version: 1, SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat, Auth: AuthRule{Kind: AuthBearerHeader, Header: "Authorization"}}
		got := mustCompile(t, in)
		if !reflect.DeepEqual(got.AutoModelIDs, []string{"m", "z"}) || got.Providers["p"].Selector != "p" || !reflect.DeepEqual(got.Models["m"].FallbackModelIDs, []string{"z"}) || got.Routes[0].Credentials[0].ID != "primary" {
			t.Fatalf("routing candidates = %#v", got)
		}
		clone := CloneCompiledConfig(got)
		clone.AutoModelIDs[0] = "mutated"
		clone.Models["m"].FallbackModelIDs[0] = "mutated"
		clone.Routes[0].Credentials[0].ID = "mutated"
		if got.AutoModelIDs[0] != "m" || got.Models["m"].FallbackModelIDs[0] != "z" || got.Routes[0].Credentials[0].ID != "primary" {
			t.Fatal("clone retained routing candidate aliases")
		}
	})

	for _, tc := range []struct {
		name, want string
		mutate     func(*ConfigInput)
	}{
		{"duplicate selector", "selector", func(in *ConfigInput) {
			in.Providers["q"] = ProviderInput{ID: "q", Name: "other", Selector: "p", BaseURL: "https://other.example/v1", SDKKind: SDKKindOpenAI, Protocol: ProtocolOpenAIChat}
		}},
		{"unsafe selector", "selector", func(in *ConfigInput) { p := in.Providers["p"]; p.Selector = "../p"; in.Providers["p"] = p }},
		{"auto actual model", "reserved", func(in *ConfigInput) {
			in.Models = map[string]ModelInput{"auto": {ID: "auto", Capabilities: []Capability{CapabilityChat}}}
			in.Routes[0].ModelID = "auto"
		}},
		{"unknown auto", "unknown auto", func(in *ConfigInput) { in.Global.AutoModelIDs = []string{"missing"} }},
		{"duplicate auto", "duplicate auto", func(in *ConfigInput) { in.Global.AutoModelIDs = []string{"m", "m"} }},
		{"fallback model cycle", "fallback model cycle", func(in *ConfigInput) {
			in.Models["z"] = ModelInput{ID: "z", Capabilities: []Capability{CapabilityChat}, FallbackModelIDs: []string{"m"}}
			m := in.Models["m"]
			m.FallbackModelIDs = []string{"z"}
			in.Models["m"] = m
		}},
		{"legacy explicit conflict", "conflict", func(in *ConfigInput) {
			a := in.Adapters["a"]
			a.Auth = AuthRule{Kind: AuthBearerHeader, Header: "Authorization", CredentialRef: "vault://provider/legacy"}
			in.Adapters["a"] = a
			in.Routes[0].Credentials = []CredentialInput{{ID: "explicit", CredentialRef: "vault://provider/explicit", Enabled: true}}
		}},
		{"auth credential required", "enabled credential", func(in *ConfigInput) {
			a := in.Adapters["a"]
			a.Auth = AuthRule{Kind: AuthBearerHeader, Header: "Authorization"}
			in.Adapters["a"] = a
		}},
		{"auth none rejects credentials", "auth none", func(in *ConfigInput) {
			in.Routes[0].Credentials = []CredentialInput{{ID: "c", CredentialRef: "vault://provider/c", Enabled: true}}
		}},
		{"credential ids global", "duplicate credential ID", func(in *ConfigInput) {
			in.Routes = append(in.Routes, RouteInput{ID: "r2", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "two", Protocol: ProtocolOpenAIChat, Credentials: []CredentialInput{{ID: "c", CredentialRef: "vault://provider/c", Enabled: true}}})
			a := in.Adapters["a"]
			a.Auth = AuthRule{Kind: AuthBearerHeader, Header: "Authorization"}
			in.Adapters["a"] = a
			in.Routes[0].Credentials = []CredentialInput{{ID: "c", CredentialRef: "vault://provider/first", Enabled: true}}
		}},
		{"fallback group mismatch", "route group", func(in *ConfigInput) {
			in.Routes[0].RouteGroup = "one"
			in.Routes = append(in.Routes, RouteInput{ID: "r2", ModelID: "m", ProviderID: "p", AdapterID: "a", UpstreamModel: "two", Protocol: ProtocolOpenAIChat, RouteGroup: "two"})
			in.Routes[0].FallbackRouteIDs = []string{"r2"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { in := baseInput(); tc.mutate(&in); mustFail(t, in, tc.want) })
	}
}

func TestCompileCredentialErrorsDoNotExposeReferences(t *testing.T) {
	const secretMarker = "secret-marker-must-not-escape"
	for _, tc := range []struct {
		name        string
		credentials []CredentialInput
		credential  string
		want        string
	}{
		{
			name:        "invalid reference",
			credentials: []CredentialInput{{ID: "primary", CredentialRef: "vault://provider/" + secretMarker + "?query=forbidden", Enabled: true}},
			credential:  "primary",
			want:        "invalid credential reference",
		},
		{
			name: "duplicate reference",
			credentials: []CredentialInput{
				{ID: "primary", CredentialRef: "vault://provider/" + secretMarker, Enabled: true},
				{ID: "secondary", CredentialRef: "vault://provider/" + secretMarker, Enabled: true},
			},
			credential: "secondary",
			want:       "duplicate credential reference",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			a := in.Adapters["a"]
			a.Auth = AuthRule{Kind: AuthBearerHeader, Header: "Authorization"}
			in.Adapters["a"] = a
			in.Routes[0].Credentials = tc.credentials

			_, err := Compile(in)
			if err == nil {
				t.Fatal("Compile succeeded")
			}
			message := err.Error()
			for _, want := range []string{`route "r"`, `credential "` + tc.credential + `"`, tc.want} {
				if !strings.Contains(message, want) {
					t.Errorf("error = %q, want %q", message, want)
				}
			}
			if strings.Contains(message, secretMarker) {
				t.Errorf("error exposed credential reference marker: %q", message)
			}
		})
	}
}

func TestCompileSynthesizesStableLegacyCredential(t *testing.T) {
	in := baseInput()
	a := in.Adapters["a"]
	a.Auth = AuthRule{Kind: AuthBearerHeader, Header: "Authorization", CredentialRef: "vault://provider/default"}
	in.Adapters["a"] = a
	got := mustCompile(t, in)

	sum := sha256.Sum256([]byte("r"))
	wantID := legacyCredentialIDPrefix + hex.EncodeToString(sum[:])
	if len(wantID) != len(legacyCredentialIDPrefix)+sha256.Size*2 || got.Routes[0].Credentials[0] != (CompiledCredential{ID: wantID, CredentialRef: "vault://provider/default", Enabled: true}) {
		t.Fatalf("legacy credential = %#v, want full SHA-256 ID %q", got.Routes[0].Credentials, wantID)
	}
	if legacyCredentialID("r") != wantID || legacyCredentialID("r") != legacyCredentialID("r") {
		t.Fatalf("legacy credential ID is not stable: %q", legacyCredentialID("r"))
	}
}

func TestCompileRejectsExplicitLegacyCredentialNamespace(t *testing.T) {
	in := baseInput()
	a := in.Adapters["a"]
	a.Auth = AuthRule{Kind: AuthBearerHeader, Header: "Authorization"}
	in.Adapters["a"] = a
	in.Routes[0].Credentials = []CredentialInput{{ID: legacyCredentialIDPrefix + strings.Repeat("0", sha256.Size*2), CredentialRef: "vault://provider/explicit", Enabled: true}}
	mustFail(t, in, "legacy credential ID namespace is reserved")
}
