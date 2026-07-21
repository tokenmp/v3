package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// fixtureNames lists every raw ConfigSnapshot fixture that must compile.
var fixtureNames = []string{"default", "xfyun", "anthropic"}

// fixtureDir resolves the repository-local fixtures/configs directory from the
// location of this test file, so it is independent of the process CWD.
func fixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile: services/executor/internal/snapshot/fixture_test.go
	executorDir := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	return filepath.Join(executorDir, "fixtures", "configs")
}

// loadRawConfig reads a fixture and decodes it strictly into a ConfigSnapshot.
// Strict decoding turns typo'd field names into compile-time-style failures:
// a fixture that does not match the struct surface cannot "compile".
func loadRawConfig(t *testing.T, name string) (raw []byte, cfg ConfigSnapshot) {
	t.Helper()
	path := filepath.Join(fixtureDir(t), name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("decode %s as ConfigSnapshot: %v", name, err)
	}
	return data, cfg
}

// compileAndFreeze compiles a raw ConfigSnapshot into a CompiledConfig and
// freezes it into a *CompiledSnapshot ready for Store.Publish. Uses the real
// adapter compilation path and adapter.CloneCompiledConfig for deep copies.
func compileAndFreeze(t *testing.T, cfg ConfigSnapshot, generation uint64) *CompiledSnapshot {
	t.Helper()
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile(%q): %v", cfg.Revision, err)
	}
	frozen, err := NewCompiledSnapshot(cfg.Revision, &compiled, generation)
	if err != nil {
		t.Fatalf("NewCompiledSnapshot(%q, gen=%d): %v", cfg.Revision, generation, err)
	}
	return frozen
}

// forbiddenSecretKey matches a JSON object key that must never appear in a
// sanitized fixture. It anchors on the opening quote and the trailing ":
// so it never matches substrings inside longer field names such as
// "MaxBudgetToken" or enum values such as "api_key_header".
var forbiddenSecretKey = regexp.MustCompile(`(?i)"(secret|password|credential|api[_-]?key|apikey|access[_-]?key|private[_-]?key|secret[_-]?key|client[_-]?secret|auth[_-]?token|token)"\s*:`)

var secretJWT = regexp.MustCompile(`(?i)(?:eyJ[a-z0-9_-]+)\.(?:eyJ[a-z0-9_-]+)\.[a-z0-9_-]+`)

var secretURLQuery = regexp.MustCompile(`(?i)https?://[^\s"']*[?&](?:credential|api[_-]?key|apikey|token|secret|password)=[^&#\s"']+`)

// secretValueMarkers are literal secret-bearing value prefixes that must
// never appear in a sanitized raw fixture. None collide with allowed enum
// values or field names.
var secretValueMarkers = []string{
	"sk-",
	"Bearer ",
	"BEGIN PRIVATE KEY",
	"AKIA",
	"xox-",
	"ghp_",
	"gho_",
	"glpat-",
}

func secretFindings(raw []byte) []string {
	var findings []string
	if loc := forbiddenSecretKey.FindIndex(raw); loc != nil {
		findings = append(findings, "forbidden secret key "+string(raw[loc[0]:loc[1]]))
	}
	for _, marker := range secretValueMarkers {
		if bytes.Contains(raw, []byte(marker)) {
			findings = append(findings, "secret value marker "+marker)
		}
	}
	if secretJWT.Match(raw) {
		findings = append(findings, "JWT-shaped secret")
	}
	if secretURLQuery.Match(raw) {
		findings = append(findings, "URL query credential")
	}
	return findings
}

func assertNoSecrets(t *testing.T, name string, raw []byte) {
	t.Helper()
	if findings := secretFindings(raw); len(findings) != 0 {
		t.Errorf("%s: %s", name, strings.Join(findings, "; "))
	}
}

func assertEnumsValid(t *testing.T, cfg ConfigSnapshot) {
	t.Helper()
	for _, a := range cfg.Adapters {
		if !a.SDKKind.Valid() {
			t.Errorf("adapter %s: invalid SDKKind %q", a.ID, a.SDKKind)
		}
		if !a.Protocol.Valid() {
			t.Errorf("adapter %s: invalid Protocol %q", a.ID, a.Protocol)
		}
		if !a.Auth.Kind.Valid() {
			t.Errorf("adapter %s: invalid AuthKind %q", a.ID, a.Auth.Kind)
		}
		for _, c := range a.Capability.Require {
			if !c.Valid() {
				t.Errorf("adapter %s: invalid required capability %q", a.ID, c)
			}
		}
		for _, c := range a.Capability.Deny {
			if !c.Valid() {
				t.Errorf("adapter %s: invalid denied capability %q", a.ID, c)
			}
		}
		if !a.Thinking.DefaultEffort.Valid() {
			t.Errorf("adapter %s: invalid DefaultEffort %q", a.ID, a.Thinking.DefaultEffort)
		}
		for in, out := range a.Thinking.EffortMapping {
			if !in.Valid() {
				t.Errorf("adapter %s: invalid EffortMapping key %q", a.ID, in)
			}
			if !out.Valid() {
				t.Errorf("adapter %s: invalid EffortMapping value %q", a.ID, out)
			}
		}
		for _, r := range a.Response.Rules {
			if r.Output.HTTPStatus < 100 || r.Output.HTTPStatus > 599 {
				t.Errorf("adapter %s: invalid output status %d", a.ID, r.Output.HTTPStatus)
			}
		}
		for _, r := range a.Retry.Rules {
			if !r.Action.Valid() {
				t.Errorf("adapter %s: invalid RetryAction %q", a.ID, r.Action)
			}
		}
	}
	for _, m := range cfg.Models {
		for _, c := range m.Capabilities {
			if !c.Valid() {
				t.Errorf("model %s: invalid capability %q", m.ID, c)
			}
		}
		if !m.Thinking.DefaultEffort.Valid() {
			t.Errorf("model %s: invalid DefaultEffort %q", m.ID, m.Thinking.DefaultEffort)
		}
		if !m.Thinking.MaxEffort.Valid() {
			t.Errorf("model %s: invalid MaxEffort %q", m.ID, m.Thinking.MaxEffort)
		}
	}
	for _, p := range cfg.Providers {
		if !p.SDKKind.Valid() {
			t.Errorf("provider %s: invalid SDKKind %q", p.ID, p.SDKKind)
		}
		if !p.Protocol.Valid() {
			t.Errorf("provider %s: invalid Protocol %q", p.ID, p.Protocol)
		}
	}
	for _, r := range cfg.Routes {
		if !r.Protocol.Valid() {
			t.Errorf("route %s: invalid Protocol %q", r.ID, r.Protocol)
		}
	}
}

// effortRank orders the normalized ThinkingEffort scale so tests can assert that
// an adapter degrades (maps a higher input effort to a strictly lower output).
func effortRank(e adapter.ThinkingEffort) int {
	switch e {
	case adapter.ThinkingNone:
		return 0
	case adapter.ThinkingMinimal:
		return 1
	case adapter.ThinkingLow:
		return 2
	case adapter.ThinkingMedium:
		return 3
	case adapter.ThinkingHigh:
		return 4
	case adapter.ThinkingXHigh:
		return 5
	case adapter.ThinkingMax:
		return 6
	default:
		return -1
	}
}

func assertDurationsParse(t *testing.T, label string, d adapter.TimeoutPolicy) {
	t.Helper()
	for name, raw := range map[string]adapter.RawDuration{
		"RequestTimeout":    d.RequestTimeout,
		"TTFTTimeout":       d.TTFTTimeout,
		"StreamIdleTimeout": d.StreamIdleTimeout,
		"StreamMaxLifetime": d.StreamMaxLifetime,
		"RetryBackoff":      d.RetryBackoff,
	} {
		if raw == "" {
			continue
		}
		if _, err := time.ParseDuration(string(raw)); err != nil {
			t.Errorf("%s: invalid %s duration %q: %v", label, name, raw, err)
		}
	}
}

func assertRetryDurationsParse(t *testing.T, label string, r adapter.RetryPolicy) {
	t.Helper()
	for name, raw := range map[string]adapter.RawDuration{
		"MaxTotalDuration": r.MaxTotalDuration,
		"Backoff":          r.Backoff,
	} {
		if raw == "" {
			continue
		}
		if _, err := time.ParseDuration(string(raw)); err != nil {
			t.Errorf("%s: invalid %s duration %q: %v", label, name, raw, err)
		}
	}
}

func TestFixturesCompileIntoCompiledSnapshot(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			raw, cfg := loadRawConfig(t, name)
			if cfg.Revision == "" {
				t.Fatalf("%s: empty Revision", name)
			}
			if cfg.CreatedAt.IsZero() {
				t.Fatalf("%s: zero CreatedAt", name)
			}
			if len(cfg.Adapters) == 0 || len(cfg.Providers) == 0 || len(cfg.Models) == 0 || len(cfg.Routes) == 0 {
				t.Fatalf("%s: incomplete ConfigSnapshot (need Models/Providers/Routes/Adapters)", name)
			}

			// Compile + freeze + publish through the Store, verifying immutable views.
			assertEnumsValid(t, cfg)
			compiled := compileAndFreeze(t, cfg, 1)
			var store Store
			if err := store.Publish(compiled); err != nil {
				t.Fatalf("Publish(%s): %v", name, err)
			}
			view, err := store.Current()
			if err != nil {
				t.Fatalf("Current(%s): %v", name, err)
			}
			if got := view.Revision(); got != cfg.Revision {
				t.Errorf("%s: Revision = %q, want %q", name, got, cfg.Revision)
			}
			viewValue := view.Value()
			assertEnumsValid(t, cfg) // validate raw cfg still valid (not mutated)

			// Mutate raw input after publication; the published view
			// must be unaffected (deep clone contract).
			cfg.Adapters["x"] = adapter.AdapterConfig{ID: "injected"}
			for k := range cfg.Models {
				delete(cfg.Models, k)
			}
			if _, ok := view.Value().Adapters["x"]; ok {
				t.Errorf("%s: published view reflects post-publish source mutation", name)
			}
			if len(view.Value().Models) == 0 {
				t.Errorf("%s: published view reflects post-publish source deletion", name)
			}

			// A caller-local mutation of the returned value must not leak back.
			viewValue.Adapters["caller-mutation"] = adapter.CompiledAdapter{ID: "leak"}
			again, err := store.Current()
			if err != nil {
				t.Fatalf("Current(%s) second: %v", name, err)
			}
			if _, ok := again.Value().Adapters["caller-mutation"]; ok {
				t.Errorf("%s: caller-local value mutation leaked into Store", name)
			}

			assertNoSecrets(t, name, raw)
		})
	}
}

func TestFixturesContainNoSecrets(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			raw, _ := loadRawConfig(t, name)
			assertNoSecrets(t, name, raw)
		})
	}
}

func TestSecretScannerRejectsSecretBearingFixtureContent(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "credential field", raw: `{"credential":"not-a-reference"}`},
		{name: "apiKey field", raw: `{"apiKey":"not-a-reference"}`},
		{name: "token field", raw: `{"token":"not-a-reference"}`},
		{name: "sk value", raw: `{"note":"sk-live-secret"}`},
		{name: "JWT value", raw: `{"note":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature"}`},
		{name: "URL query credential", raw: `{"BaseURL":"https://api.example/v1?credential=not-a-reference"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if findings := secretFindings([]byte(tc.raw)); len(findings) == 0 {
				t.Fatalf("secret scanner accepted %q", tc.raw)
			}
		})
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "credential reference field", raw: `{"CredentialRef":"resolver://provider/default"}`},
		{name: "safe enum text", raw: `{"AuthKind":"api_key_header","note":"tokenized request metadata"}`},
		{name: "longer field names", raw: `{"MaxBudgetToken":10,"AccessTokenMode":"reference"}`},
		{name: "url without credential query", raw: `{"BaseURL":"https://api.example/v1?mode=tokenized"}`},
	} {
		t.Run("safe "+tc.name, func(t *testing.T) {
			if findings := secretFindings([]byte(tc.raw)); len(findings) != 0 {
				t.Fatalf("secret scanner false positive for %q: %v", tc.raw, findings)
			}
		})
	}
	for _, name := range fixtureNames {
		name := name
		t.Run("clean "+name, func(t *testing.T) {
			raw, _ := loadRawConfig(t, name)
			if findings := secretFindings(raw); len(findings) != 0 {
				t.Fatalf("clean fixture findings = %v", findings)
			}
		})
	}
}

func TestFixturesCompileKeyFields(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			_, raw := loadRawConfig(t, name)
			compiled, err := Compile(raw)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if compiled.Revision != raw.Revision || len(compiled.Models) != 1 || len(compiled.Providers) != 1 || len(compiled.Adapters) != 1 || len(compiled.Routes) != 1 {
				t.Fatalf("compiled key fields = %#v", compiled)
			}
			route := compiled.Routes[0]
			if route.ID == "" || route.ModelID == "" || route.ProviderID == "" || route.AdapterID == "" || route.UpstreamModel == "" || !route.Enabled {
				t.Fatalf("compiled route is incomplete: %#v", route)
			}
			if _, ok := compiled.Models[route.ModelID]; !ok {
				t.Errorf("route model %q is absent from compiled models", route.ModelID)
			}
			provider, ok := compiled.Providers[route.ProviderID]
			if !ok {
				t.Errorf("route provider %q is absent from compiled providers", route.ProviderID)
			} else if provider.Selector == "" {
				t.Errorf("route provider %q has empty selector", route.ProviderID)
			}
			if len(raw.Routes[0].Credentials) != 0 && len(route.Credentials) != len(raw.Routes[0].Credentials) {
				t.Errorf("explicit route credentials were not preserved: %#v", route.Credentials)
			}
			if len(raw.Routes[0].Credentials) == 0 && (len(route.Credentials) != 1 || route.Credentials[0].CredentialRef != raw.Adapters[route.AdapterID].Auth.CredentialRef) {
				t.Errorf("legacy adapter credential was not bridged: %#v", route.Credentials)
			}
			if _, ok := compiled.Adapters[route.AdapterID]; !ok {
				t.Errorf("route adapter %q is absent from compiled adapters", route.AdapterID)
			}
			assertCompiledRulesSorted(t, name, compiled.Adapters[route.AdapterID])
		})
	}
}

func assertCompiledRulesSorted(t *testing.T, name string, a adapter.CompiledAdapter) {
	t.Helper()
	for i := 1; i < len(a.ResponseRules); i++ {
		if a.ResponseRules[i-1].Priority > a.ResponseRules[i].Priority {
			t.Errorf("%s: compiled response rules are not priority sorted: %q before %q", name, a.ResponseRules[i-1].ID, a.ResponseRules[i].ID)
		}
	}
	for i := 1; i < len(a.Retry.Rules); i++ {
		if a.Retry.Rules[i-1].Priority > a.Retry.Rules[i].Priority {
			t.Errorf("%s: compiled retry rules are not priority sorted: %q before %q", name, a.Retry.Rules[i-1].ID, a.Retry.Rules[i].ID)
		}
	}
}

func TestFixtureAdaptersHaveRetryAndTimeout(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			_, cfg := loadRawConfig(t, name)
			if cfg.Adapters == nil {
				t.Fatalf("%s: nil Adapters", name)
			}
			for id, a := range cfg.Adapters {
				if a.Retry.MaxTotalAttempts == nil {
					t.Errorf("%s/%s: MaxTotalAttempts unset", name, id)
				} else if *a.Retry.MaxTotalAttempts < 1 {
					t.Errorf("%s/%s: MaxTotalAttempts = %d, want >=1", name, id, *a.Retry.MaxTotalAttempts)
				}
				if len(a.Retry.Rules) == 0 {
					t.Errorf("%s/%s: no RetryRules", name, id)
				}
				if a.Timeout.RequestTimeout == "" {
					t.Errorf("%s/%s: RequestTimeout unset", name, id)
				}
				if a.Timeout.TTFTTimeout == "" {
					t.Errorf("%s/%s: TTFTTimeout unset", name, id)
				}
				assertRetryDurationsParse(t, name+"/"+id, a.Retry)
				assertDurationsParse(t, name+"/"+id, a.Timeout)
			}
		})
	}
}

func TestXfyunResponseMaps503To429(t *testing.T) {
	_, cfg := loadRawConfig(t, "xfyun")
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	a := compiled.Adapters["adapter-xfyun-default"]

	var matched *adapter.ResponseRule
	for i := range a.ResponseRules {
		r := a.ResponseRules[i]
		for _, st := range r.Match.HTTPStatuses {
			if st == 503 {
				matched = &r
				break
			}
		}
		if matched != nil {
			break
		}
	}
	if matched == nil {
		t.Fatalf("xfyun: no ResponseRule matches upstream 503")
	}
	if matched.Output.HTTPStatus != 429 {
		t.Errorf("xfyun 503 -> HTTPStatus = %d, want 429", matched.Output.HTTPStatus)
	}
	if matched.Output.ErrorCode != "rate_limited" || matched.Output.ErrorType != "rate_limited" {
		t.Errorf("xfyun 503 compiled mapping = %#v, want rate_limited code and type", matched.Output)
	}
	if len(matched.Match.ErrorCodes) != 2 || matched.Match.ErrorCodes[0] != "service_unavailable" || matched.Match.ErrorCodes[1] != "traffic_overflow" {
		t.Errorf("xfyun 503 compiled match codes = %v, want [service_unavailable traffic_overflow]", matched.Match.ErrorCodes)
	}

	// The 503 path must also be retried (degraded service treated as retryable).
	var retryRule *adapter.RetryRule
	for i := range a.Retry.Rules {
		r := a.Retry.Rules[i]
		for _, st := range r.HTTPStatuses {
			if st == 503 {
				retryRule = &r
				break
			}
		}
		if retryRule != nil {
			break
		}
	}
	if retryRule == nil {
		t.Fatal("xfyun: no RetryRule matches upstream 503")
	}
	if !retryRule.Action.Valid() || retryRule.Action == adapter.RetryNone {
		t.Errorf("xfyun 503 retry action = %q, want a non-none candidate move", retryRule.Action)
	}
}

func TestXfyunThinkingDegradation(t *testing.T) {
	_, cfg := loadRawConfig(t, "xfyun")
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	a := compiled.Adapters["adapter-xfyun-default"]

	mapping := a.Thinking.EffortMapping
	if len(mapping) == 0 {
		t.Fatal("xfyun: empty EffortMapping")
	}

	// The model advertises MaxEffort high (see T-matrix T02–T06: xfyun supports
	// low/medium/high). The adapter must degrade any requested effort that
	// exceeds the model's MaxEffort down to high or below, with a strictly
	// lower rank than the input. Efforts within MaxEffort (e.g. high itself)
	// are passed through and are not considered degradations.
	model := cfg.Models["chat-xfyun"]
	if model.Thinking.MaxEffort != adapter.ThinkingHigh {
		t.Errorf("xfyun model MaxEffort = %q, want high", model.Thinking.MaxEffort)
	}
	maxRank := effortRank(model.Thinking.MaxEffort)

	cases := []adapter.ThinkingEffort{adapter.ThinkingXHigh, adapter.ThinkingMax}
	for _, in := range cases {
		out, ok := mapping[in]
		if !ok {
			t.Errorf("xfyun: EffortMapping missing %q", in)
			continue
		}
		if out != adapter.ThinkingHigh {
			t.Errorf("xfyun: %q -> %q, want high", in, out)
		}
		if effortRank(out) > maxRank {
			t.Errorf("xfyun: %q -> %q exceeds model MaxEffort %q", in, out, model.Thinking.MaxEffort)
		}
		if effortRank(out) >= effortRank(in) {
			t.Errorf("xfyun: %q -> %q is not a degradation (rank not strictly lower)", in, out)
		}
	}
}

func TestAnthropicCompiledDeclarations(t *testing.T) {
	_, cfg := loadRawConfig(t, "anthropic")
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	model := compiled.Models["chat-anthropic"]
	if !model.Thinking.Supported || model.Thinking.DefaultEffort != adapter.ThinkingMedium || model.Thinking.MaxEffort != adapter.ThinkingMax || model.Thinking.MaxBudgetToken != 32000 {
		t.Errorf("anthropic compiled model thinking = %#v", model.Thinking)
	}
	a := compiled.Adapters["adapter-anthropic-default"]
	if a.Protocol != adapter.ProtocolAnthropic || a.SDKKind != adapter.SDKKindAnthropic || a.Auth.CredentialRef != "vault://anthropic-default/credential/default" {
		t.Errorf("anthropic compiled adapter identity = %#v", a)
	}
	if a.Thinking.DefaultEffort != adapter.ThinkingMedium || a.Thinking.EffortMapping[adapter.ThinkingXHigh] != adapter.ThinkingHigh || a.Thinking.EffortMapping[adapter.ThinkingMax] != adapter.ThinkingHigh || a.Thinking.BudgetMapping[adapter.ThinkingHigh] != 16000 || a.Thinking.BudgetMapping[adapter.ThinkingMax] != 32000 {
		t.Errorf("anthropic compiled adapter thinking = %#v", a.Thinking)
	}
	route := compiled.Routes[0]
	if route.UpstreamModel != "claude-default" || route.Protocol != adapter.ProtocolAnthropic {
		t.Errorf("anthropic compiled route = %#v", route)
	}
}

func TestAnthropicResponseMapsOverloadedTo429(t *testing.T) {
	_, cfg := loadRawConfig(t, "anthropic")
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	a := compiled.Adapters["adapter-anthropic-default"]

	var overloaded, rateLimited *adapter.ResponseRule
	for i := range a.ResponseRules {
		r := a.ResponseRules[i]
		switch r.ID {
		case "resp-529-to-429":
			overloaded = &r
		case "resp-429":
			rateLimited = &r
		}
	}
	if overloaded == nil {
		t.Fatal("anthropic: missing resp-529-to-429 ResponseRule")
	}
	if overloaded.Output.HTTPStatus != 429 || overloaded.Output.ErrorCode != "rate_limited" || overloaded.Output.ErrorType != "rate_limited" {
		t.Errorf("anthropic overloaded compiled mapping = %#v, want 429/rate_limited/rate_limited", overloaded.Output)
	}
	if got := overloaded.Match.HTTPStatuses; len(got) != 1 || got[0] != 529 {
		t.Errorf("anthropic overloaded compiled match statuses = %v, want [529]", got)
	}
	if got := overloaded.Match.ErrorTypes; len(got) != 1 || got[0] != "overloaded_error" {
		t.Errorf("anthropic overloaded compiled match types = %v, want [overloaded_error]", got)
	}
	if rateLimited == nil {
		t.Fatal("anthropic: missing resp-429 ResponseRule")
	}
	if got := rateLimited.Match.HTTPStatuses; len(got) != 1 || got[0] != 429 {
		t.Errorf("anthropic rate limit compiled match statuses = %v, want [429]", got)
	}
	if got := rateLimited.Match.ErrorTypes; len(got) != 0 {
		t.Errorf("anthropic rate limit compiled match types = %v, want status-only rule", got)
	}
	if rateLimited.Output.HTTPStatus != 429 || rateLimited.Output.ErrorCode != "rate_limited" || rateLimited.Output.ErrorType != "rate_limited" {
		t.Errorf("anthropic rate limit compiled mapping = %#v, want 429/rate_limited/rate_limited", rateLimited.Output)
	}
}

func TestCompiledSnapshotRejectsInvalidInputs(t *testing.T) {
	_, cfg := loadRawConfig(t, "default")
	compiled, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Empty external revision
	if _, err := NewCompiledSnapshot("", &compiled, 1); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("empty revision: err = %v, want ErrInvalidSnapshot", err)
	}

	// Whitespace-only external revision
	if _, err := NewCompiledSnapshot("   ", &compiled, 1); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("whitespace revision: err = %v, want ErrInvalidSnapshot", err)
	}

	// Zero generation
	if _, err := NewCompiledSnapshot(compiled.Revision, &compiled, 0); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("zero generation: err = %v, want ErrInvalidSnapshot", err)
	}

	// Nil config
	if _, err := NewCompiledSnapshot(compiled.Revision, nil, 1); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("nil config: err = %v, want ErrInvalidSnapshot", err)
	}

	// Blank config.Revision
	blankRev := compiled
	blankRev.Revision = ""
	if _, err := NewCompiledSnapshot("rev", &blankRev, 1); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("blank config.Revision: err = %v, want ErrInvalidSnapshot", err)
	}

	// Mismatched external revision vs config.Revision
	if _, err := NewCompiledSnapshot("different", &compiled, 1); !errors.Is(err, ErrInvalidSnapshot) {
		t.Errorf("mismatched revision: err = %v, want ErrInvalidSnapshot", err)
	}
}

func TestFixturesRoundTripStableJSON(t *testing.T) {
	// Re-marshalling a decoded snapshot and decoding again must be idempotent:
	// no field is lost or type-coerced by the ConfigSnapshot surface. This
	// guards against accidental json tag drift once a real compiler lands.
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			_, first := loadRawConfig(t, name)
			encoded, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var second ConfigSnapshot
			dec := json.NewDecoder(bytes.NewReader(encoded))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&second); err != nil {
				t.Fatalf("re-decode: %v", err)
			}
			// Sanity: revisions and adapter counts survive a round trip.
			if first.Revision != second.Revision {
				t.Errorf("Revision drift: %q vs %q", first.Revision, second.Revision)
			}
			if len(first.Adapters) != len(second.Adapters) {
				t.Errorf("Adapters count drift: %d vs %d", len(first.Adapters), len(second.Adapters))
			}
		})
	}
}

func TestFixtureBaseURLsContainNoCredentials(t *testing.T) {
	for _, name := range fixtureNames {
		name := name
		t.Run(name, func(t *testing.T) {
			_, cfg := loadRawConfig(t, name)
			for id, p := range cfg.Providers {
				if p.BaseURL == "" {
					t.Errorf("%s/%s: empty BaseURL", name, id)
				}
				if strings.Contains(p.BaseURL, "@") {
					t.Errorf("%s/%s: BaseURL %q contains userinfo", name, id, p.BaseURL)
				}
				lower := strings.ToLower(p.BaseURL)
				if strings.HasPrefix(lower, "http://") {
					t.Errorf("%s/%s: BaseURL %q must use https", name, id, p.BaseURL)
				}
			}
		})
	}
}
