package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func compiledEngineAdapter(t *testing.T, rules []RequestRule) CompiledAdapter {
	t.Helper()
	in := baseInput()
	raw := in.Adapters["a"]
	raw.Request.Rules = rules
	in.Adapters["a"] = raw
	return mustCompile(t, in).Adapters["a"]
}

func compiledEngineAdapterWith(t *testing.T, mutate func(*AdapterConfig)) CompiledAdapter {
	t.Helper()
	in := baseInput()
	raw := in.Adapters["a"]
	mutate(&raw)
	in.Adapters["a"] = raw
	return mustCompile(t, in).Adapters["a"]
}

func applyFull(t *testing.T, adapter CompiledAdapter, body string, thinking ThinkingRequest) AppliedRequest {
	t.Helper()
	got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: json.RawMessage(body), Thinking: thinking})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return got
}

func applyBody(t *testing.T, adapter CompiledAdapter, body string) map[string]any {
	t.Helper()
	got := applyFull(t, adapter, body, ThinkingRequest{})
	var out map[string]any
	if err := json.Unmarshal(got.Body, &out); err != nil {
		t.Fatalf("Unmarshal output: %v\n%s", err, got.Body)
	}
	return out
}

func applyErr(adapter CompiledAdapter, body string) error {
	_, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: json.RawMessage(body)})
	return err
}

func TestEngineApplyFiniteActionsAndAtomicity(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{
		{ID: "set", Action: RequestSet, Path: "/object/answer", Value: []byte(`42`)},
		{ID: "append", Action: RequestSet, Path: "/items/-", Value: []byte(`"new"`)},
		{ID: "map", Action: RequestMapEnum, Path: "/state", EnumMap: map[string]string{"cold": "warm"}},
		{ID: "clamp", Action: RequestClampNumber, Path: "/temperature", Min: floatp(0), Max: floatp(1)},
		{ID: "remove", Action: RequestRemove, Path: "/remove"},
	})
	out := applyBody(t, adapter, `{"object":{},"items":["old"],"state":"cold","temperature":2,"remove":true}`)
	if out["object"].(map[string]any)["answer"].(float64) != 42 || out["state"] != "warm" || out["temperature"].(float64) != 1 || len(out["items"].([]any)) != 2 {
		t.Fatalf("unexpected transformed body: %#v", out)
	}
	if _, exists := out["remove"]; exists {
		t.Fatalf("remove did not apply: %#v", out)
	}

	copyAdapter := compiledEngineAdapter(t, []RequestRule{{ID: "copy", Action: RequestCopy, From: "/object/answer", To: "/copy"}})
	copyOut := applyBody(t, copyAdapter, `{"object":{"answer":42}}`)
	if copyOut["copy"].(float64) != 42 {
		t.Fatalf("copy result = %#v", copyOut)
	}

	bad := compiledEngineAdapter(t, []RequestRule{{ID: "missing", Action: RequestRemove, Path: "/missing"}})
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: bad, Body: []byte(`{"keep":true}`)}); err == nil || !strings.Contains(err.Error(), "rule") {
		t.Fatalf("Apply error = %v, want rule error", err)
	}
}

func TestEngineAtomicityReturnsZeroResultOnAnyFailure(t *testing.T) {
	// A set that succeeds followed by a clamp on a missing path must fail
	// closed: the caller must never observe the partial /temperature set.
	adapter := compiledEngineAdapter(t, []RequestRule{
		{ID: "set", Action: RequestSet, Path: "/temperature", Value: []byte(`0.5`)},
		{ID: "clamp-missing", Action: RequestClampNumber, Path: "/missing/n", Min: floatp(0), Max: floatp(1)},
	})
	got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{"temperature":1}`)})
	if err == nil {
		t.Fatalf("Apply unexpectedly succeeded: %#v", got)
	}
	if got.Body != nil || got.Thinking != (EffectiveThinking{}) || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
		t.Fatalf("failure was not atomic: %#v", got)
	}
	var ruleErr *RuleError
	if !errors.As(err, &ruleErr) || ruleErr.RuleID() != "clamp-missing" {
		t.Fatalf("error = %v, want RuleError{clamp-missing}", err)
	}
}

func TestEngineRenameSameArrayUsesRemoveThenAdd(t *testing.T) {
	// Exercise the pointer primitive directly for move ordering: remove source
	// first, then insert at destination, so index shifts are deterministic.
	adapter := compiledEngineAdapter(t, nil)
	document, err := decodeEngineJSON([]byte(`{"items":["a","b","c"]}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := pointerGet(document, "/items/0")
	if err != nil {
		t.Fatal(err)
	}
	if err := pointerRename(document, "/items/0", "/items/2", value); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	out := applyBody(t, adapter, string(body))
	items := out["items"].([]any)
	if got, want := strings.Join([]string{items[0].(string), items[1].(string), items[2].(string)}, ","), "b,c,a"; got != want {
		t.Fatalf("rename items = %q, want %q", got, want)
	}
}

func TestEngineSetAcceptsObjectArrayAndScalarLiterals(t *testing.T) {
	cases := []struct {
		name string
		rule RequestRule
		body string
		want any
	}{
		{"scalar number", RequestRule{ID: "s", Action: RequestSet, Path: "/n", Value: []byte(`7`)}, `{}`, 7.0},
		{"scalar string", RequestRule{ID: "s", Action: RequestSet, Path: "/n", Value: []byte(`"x"`)}, `{}`, "x"},
		{"scalar bool", RequestRule{ID: "s", Action: RequestSet, Path: "/n", Value: []byte(`true`)}, `{}`, true},
		{"scalar null", RequestRule{ID: "s", Action: RequestSet, Path: "/n", Value: []byte(`null`)}, `{}`, nil},
		{"object literal", RequestRule{ID: "s", Action: RequestSet, Path: "/n", Value: []byte(`{"a":1,"b":[2,3]}`)}, `{}`, map[string]any{"a": 1.0, "b": []any{2.0, 3.0}}},
		{"array literal", RequestRule{ID: "s", Action: RequestSet, Path: "/n", Value: []byte(`[1,2,3]`)}, `{}`, []any{1.0, 2.0, 3.0}},
		{"nested index", RequestRule{ID: "s", Action: RequestSet, Path: "/items/1", Value: []byte(`"mid"`)}, `{"items":["a","b","c"]}`, "mid"},
		{"rfc6901 escapes", RequestRule{ID: "s", Action: RequestSet, Path: "/a~1b/~0c", Value: []byte(`true`)}, `{"a/b":{"~c":0}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := compiledEngineAdapter(t, []RequestRule{tc.rule})
			out := applyBody(t, adapter, tc.body)
			var got any
			if strings.Contains(tc.rule.Path, "/") && tc.rule.Path != "/n" {
				got = out
			} else {
				got = out["n"]
			}
			if tc.name == "nested index" {
				items := out["items"].([]any)
				if items[1] != tc.want {
					t.Fatalf("got %#v want %#v", items[1], tc.want)
				}
				return
			}
			if tc.name == "rfc6901 escapes" {
				outer := out["a/b"].(map[string]any)
				if outer["~c"] != tc.want {
					t.Fatalf("got %#v want %#v", outer["~c"], tc.want)
				}
				return
			}
			if !deepEqualJSON(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestEngineAppendOnlyTerminalWrite(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "append", Action: RequestSet, Path: "/items/-", Value: []byte(`"tail"`)}})
	out := applyBody(t, adapter, `{"items":["head"]}`)
	items := out["items"].([]any)
	if len(items) != 2 || items[0] != "head" || items[1] != "tail" {
		t.Fatalf("append = %#v", items)
	}
	// Append to a missing array is rejected: no missing intermediates.
	if err := applyErr(adapter, `{}`); err == nil {
		t.Fatal("append into missing array succeeded")
	}
}

func TestEngineNoMissingIntermediates(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "set", Action: RequestSet, Path: "/missing/nested/key", Value: []byte(`1`)}})
	if err := applyErr(adapter, `{}`); err == nil {
		t.Fatal("set through missing intermediate succeeded")
	}
	// Array index out of range is rejected, not silently coerced.
	idxAdapter := compiledEngineAdapter(t, []RequestRule{{ID: "set", Action: RequestSet, Path: "/items/5", Value: []byte(`1`)}})
	if err := applyErr(idxAdapter, `{"items":["a"]}`); err == nil {
		t.Fatal("out-of-range array index succeeded")
	}
}

func TestEngineCopyProducesIndependentDeepCopy(t *testing.T) {
	// The compiler forbids copy/read overlapping a later write to the source,
	// so exercise the engine's deep-copy primitive directly: copy a subtree,
	// mutate the source, and confirm the destination is unaffected.
	document, err := decodeEngineJSON([]byte(`{"src":{"inner":"original"}}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := pointerGet(document, "/src")
	if err != nil {
		t.Fatal(err)
	}
	value, err = deepCopyJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := pointerSet(document, "/dst", value); err != nil {
		t.Fatal(err)
	}
	changed, err := decodeEngineJSON([]byte(`"changed"`))
	if err != nil {
		t.Fatal(err)
	}
	if err := pointerSet(document, "/src/inner", changed); err != nil {
		t.Fatal(err)
	}
	src := (document.(map[string]any))["src"].(map[string]any)
	dst := (document.(map[string]any))["dst"].(map[string]any)
	if src["inner"] != "changed" {
		t.Fatalf("source not mutated: %#v", src)
	}
	if dst["inner"] != "original" {
		t.Fatalf("destination aliased source mutation: %#v", dst)
	}
}

func TestEngineCopyAppendDestination(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "copy", Action: RequestCopy, From: "/src", To: "/items/-"}})
	out := applyBody(t, adapter, `{"src":42,"items":[1]}`)
	items := out["items"].([]any)
	if len(items) != 2 || items[1] != 42.0 {
		t.Fatalf("copy-append = %#v", items)
	}
}

func TestEngineRenameRejectsAnyOverlapAtomically(t *testing.T) {
	for _, paths := range [][2]string{{"/same", "/same"}, {"/source", "/source/child"}, {"/source/child", "/source"}} {
		document, err := decodeEngineJSON([]byte(`{"same":1,"source":{"child":2,"keep":3}}`))
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(document)
		value, err := pointerGet(document, paths[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := pointerRename(document, paths[0], paths[1], value); err == nil {
			t.Fatalf("pointerRename(%q, %q) succeeded", paths[0], paths[1])
		}
		after, _ := json.Marshal(document)
		if string(after) != string(before) {
			t.Fatalf("overlap rename mutated document: before=%s after=%s", before, after)
		}
	}
}

func TestEngineRenameInvalidDestinationIsAtomic(t *testing.T) {
	document, err := decodeEngineJSON([]byte(`{"source":{"nested":1},"scalar":0}`))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(document)
	value, err := pointerGet(document, "/source")
	if err != nil {
		t.Fatal(err)
	}
	if err := pointerRename(document, "/source", "/scalar/child", value); err == nil {
		t.Fatal("pointerRename into scalar succeeded")
	}
	after, _ := json.Marshal(document)
	if string(after) != string(before) {
		t.Fatalf("failed rename mutated document: before=%s after=%s", before, after)
	}
}

func TestEngineRenameAcrossKeys(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "rename", Action: RequestRename, From: "/old", To: "/new"}})
	out := applyBody(t, adapter, `{"old":{"deep":1},"keep":true}`)
	if _, exists := out["old"]; exists {
		t.Fatal("rename left source behind")
	}
	if out["new"].(map[string]any)["deep"] != 1.0 {
		t.Fatalf("rename destination = %#v", out["new"])
	}
}

func TestEngineRemoveShiftsArrays(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "rm", Action: RequestRemove, Path: "/items/1"}})
	out := applyBody(t, adapter, `{"items":["a","b","c"]}`)
	items := out["items"].([]any)
	if len(items) != 2 || items[0] != "a" || items[1] != "c" {
		t.Fatalf("remove-shift = %#v", items)
	}
}

func TestEngineMapEnumNoOpForUnmapped(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "map", Action: RequestMapEnum, Path: "/state", EnumMap: map[string]string{"cold": "warm"}}})
	out := applyBody(t, adapter, `{"state":"hot"}`)
	if out["state"] != "hot" {
		t.Fatalf("unmapped enum was changed: %#v", out["state"])
	}
	// Non-string source fails closed.
	if err := applyErr(adapter, `{"state":1}`); err == nil {
		t.Fatal("map_enum accepted non-string source")
	}
}

func TestEngineClampNumberPreservesLexemeWithinRange(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "clamp", Action: RequestClampNumber, Path: "/n", Min: floatp(0), Max: floatp(100)}})
	got := applyFull(t, adapter, `{"n":42}`, ThinkingRequest{})
	// Exact integer lexeme preserved (not rewritten through float formatting).
	if !strings.Contains(string(got.Body), `"n":42`) {
		t.Fatalf("lexeme not preserved: %s", got.Body)
	}
}

func TestEngineClampNumberClampsAboveAndBelow(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "clamp", Action: RequestClampNumber, Path: "/n", Min: floatp(0), Max: floatp(1)}})
	out := applyBody(t, adapter, `{"n":5}`)
	if out["n"].(float64) != 1 {
		t.Fatalf("above max = %#v", out["n"])
	}
	out = applyBody(t, adapter, `{"n":-3}`)
	if out["n"].(float64) != 0 {
		t.Fatalf("below min = %#v", out["n"])
	}
	// Float clamp rewrites through strconv.FormatFloat.
	out = applyBody(t, adapter, `{"n":0.7}`)
	if out["n"].(float64) != 0.7 {
		t.Fatalf("float in range = %#v", out["n"])
	}
}

func TestEngineClampUsesExactDecimalLexemes(t *testing.T) {
	// Decimal comparison must not collapse adjacent integers above 2^53 via a
	// float64 conversion. The lower one is retained verbatim; the larger one
	// is clamped to a stable json.Number boundary spelling.
	max := float64(9007199254740992)
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "clamp", Action: RequestClampNumber, Path: "/n", Min: floatp(0), Max: &max}})
	for _, tc := range []struct{ in, want string }{
		{`9007199254740992`, `9007199254740992`},
		{`9007199254740993`, `9.007199254740992e+15`},
		{`9.007199254740992e15`, `9.007199254740992e15`},
		{`9.007199254740993e15`, `9.007199254740992e+15`},
	} {
		got := applyFull(t, adapter, `{"n":`+tc.in+`}`, ThinkingRequest{})
		if want := `{"n":` + tc.want + `}`; string(got.Body) != want {
			t.Fatalf("clamp %s = %s, want %s", tc.in, got.Body, want)
		}
	}
	// An in-range exponent spelling remains exactly as supplied.
	unit := compiledEngineAdapter(t, []RequestRule{{ID: "clamp", Action: RequestClampNumber, Path: "/n", Min: floatp(0), Max: floatp(1)}})
	got := applyFull(t, unit, `{"n":7e-1}`, ThinkingRequest{})
	if string(got.Body) != `{"n":7e-1}` {
		t.Fatalf("in-range exponent lexeme rewritten: %s", got.Body)
	}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: unit, Body: []byte(`{"n":1e999}`)}); err == nil {
		t.Fatal("Apply accepted exponent overflow")
	}
	// Non-number source fails closed.
	if err := applyErr(unit, `{"n":"x"}`); err == nil {
		t.Fatal("clamp accepted non-number source")
	}
}

func TestEngineStrictJSONLimitsAndUnsafeKeys(t *testing.T) {
	adapter := compiledEngineAdapter(t, nil)
	for _, body := range [][]byte{
		[]byte(`{"a":{"__proto__":1}}`),
		[]byte(`{"a":{"prototype":1}}`),
		[]byte(`{"a":{"constructor":1}}`),
		append([]byte(`{"a":"`), append([]byte{0xff}, []byte(`"}`)...)...),
		[]byte(`{"n":1e999}`),
		[]byte(`{"a":1,"a":2}`),
	} {
		if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: body}); err == nil {
			t.Fatalf("Apply accepted unsafe body %q", body)
		}
	}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: make([]byte, maxEngineJSONBytes+1)}); err == nil {
		t.Fatal("Apply accepted oversized input")
	}

	// A valid near-limit input plus a bounded DSL literal must still respect
	// the independently enforced transformed-output cap.
	appendAdapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{{ID: "append", Action: RequestSet, Path: "/items/-", Value: []byte(`"` + strings.Repeat("x", maxDSLLiteralBytes-2) + `"`)}}}}
	nearLimit := `{"items":[],"payload":"` + strings.Repeat("x", maxEngineJSONBytes-32) + `"}`
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: appendAdapter, Body: []byte(nearLimit)}); err == nil {
		t.Fatal("Apply accepted oversized output")
	}
}

func TestEngineRejectsNonObjectTopLevel(t *testing.T) {
	adapter := compiledEngineAdapter(t, nil)
	for _, body := range []string{`[]`, `42`, `"text"`, `true`, `null`, ``, `  `, `{}{}`} {
		if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: json.RawMessage(body)}); err == nil {
			t.Fatalf("Apply accepted non-object body %q", body)
		}
	}
	// Empty object is valid.
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{}`)}); err != nil {
		t.Fatalf("Apply rejected empty object: %v", err)
	}
}

func TestEngineJSONDepthAndNodeBounds(t *testing.T) {
	adapter := compiledEngineAdapter(t, nil)
	// Depth maxDSLJSONDepth (64) is allowed; one deeper is rejected.
	deep := strings.Repeat("[", maxDSLJSONDepth+1) + "0" + strings.Repeat("]", maxDSLJSONDepth+1)
	body := `{"k":` + deep + `}`
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(body)}); err == nil {
		t.Fatal("Apply accepted over-depth body")
	}
	// Node count over maxDSLJSONNodes is rejected.
	many := "[" + strings.TrimSuffix(strings.Repeat("0,", maxDSLJSONNodes+1), ",") + "]"
	body = `{"k":` + many + `}`
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(body)}); err == nil {
		t.Fatal("Apply accepted over-node body")
	}
}

func TestEngineOutputRespectsStructuralCaps(t *testing.T) {
	// The compiler admits a set literal at exactly the node cap
	// (maxDSLJSONNodes, 10000) because the cap is exclusive of the limit.
	// Placing that literal into an object whose root already counts as one
	// node yields a transformed output of maxDSLJSONNodes+1 nodes, which must
	// be rejected by the independently enforced output structural cap. The
	// input itself is well under every cap, so only the transformation pushes
	// the result over.
	literal := []byte("[" + strings.TrimSuffix(strings.Repeat("0,", maxDSLJSONNodes-1), ",") + "]")
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "set", Action: RequestSet, Path: "/items", Value: literal}})
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{"items":[]}`)}); err == nil {
		t.Fatal("Apply accepted output exceeding node cap")
	}
	// A literal one node smaller stays under the cap and succeeds, proving
	// the rejection is structural rather than a blanket refusal.
	smaller := []byte("[" + strings.TrimSuffix(strings.Repeat("0,", maxDSLJSONNodes-2), ",") + "]")
	smallerAdapter := compiledEngineAdapter(t, []RequestRule{{ID: "set", Action: RequestSet, Path: "/items", Value: smaller}})
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: smallerAdapter, Body: []byte(`{"items":[]}`)}); err != nil {
		t.Fatalf("Apply rejected output within node cap: %v", err)
	}
}

func TestEngineTrailingContentRejected(t *testing.T) {
	adapter := compiledEngineAdapter(t, nil)
	for _, body := range []string{`{}{}`, `{} 1`, `{},null`} {
		if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: json.RawMessage(body)}); err == nil {
			t.Fatalf("Apply accepted trailing content %q", body)
		}
	}
}

func TestEngineThinkingMappingDefaultBudgetAndDegradation(t *testing.T) {
	in := baseInput()
	model := in.Models["m"]
	model.Thinking = ThinkingInput{Supported: true, DefaultEffort: ThinkingLow, MaxEffort: ThinkingMax, MaxBudgetToken: 10}
	in.Models["m"] = model
	raw := in.Adapters["a"]
	raw.Thinking = ThinkingPolicy{Supported: true, DefaultEffort: ThinkingLow, EffortMapping: fullEffortMap(ThinkingHigh), BudgetMapping: map[ThinkingEffort]int{ThinkingHigh: 4}, MaxBudgetToken: 10}
	raw.Request = RequestPolicy{AllowedHeaders: []string{"X-Safe"}, AllowedQuery: []string{"mode"}, Rules: []RequestRule{{ID: "header", Action: RequestSetHeader, Name: "X-Safe", Value: []byte(`"ok"`)}, {ID: "query", Action: RequestSetQuery, Name: "mode", Value: []byte(`"fast"`)}}}
	in.Adapters["a"] = raw
	adapter := mustCompile(t, in).Adapters["a"]

	// Requested xhigh maps to high (degraded), budget honored from request.
	budget := 8
	got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, ModelThinking: model.Thinking, Body: []byte(`{}`), Thinking: ThinkingRequest{Enabled: true, Effort: ThinkingXHigh, BudgetTokens: &budget}})
	if err != nil {
		t.Fatal(err)
	}
	if got.InjectionPlan.Headers["X-Safe"] != "ok" || got.InjectionPlan.Query["mode"] != "fast" {
		t.Fatalf("injection plan = %#v", got.InjectionPlan)
	}
	if got.Thinking.RequestedEffort != ThinkingXHigh || got.Thinking.EffectiveEffort != ThinkingHigh || !got.Thinking.Degraded || got.Thinking.EffectiveBudget != 8 || got.Thinking.RequestedBudget != 8 {
		t.Fatalf("thinking = %#v", got.Thinking)
	}

	// Empty effort uses adapter default (low → high, still degraded); nil
	// budget falls back to the mapped budget for the effective effort.
	got, err = (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, ModelThinking: model.Thinking, Body: []byte(`{}`), Thinking: ThinkingRequest{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Thinking.RequestedEffort != ThinkingLow || got.Thinking.EffectiveEffort != ThinkingHigh || got.Thinking.EffectiveBudget != 4 {
		t.Fatalf("default thinking = %#v", got.Thinking)
	}

	// Budget above MaxBudgetToken is rejected.
	tooBig := 11
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, ModelThinking: model.Thinking, Body: []byte(`{}`), Thinking: ThinkingRequest{Enabled: true, BudgetTokens: &tooBig}}); err == nil {
		t.Fatal("Apply accepted out-of-bounds budget")
	}
}

func TestEngineUnsupportedThinkingRejectsRequest(t *testing.T) {
	adapter := compiledEngineAdapter(t, nil) // Thinking.Supported == false
	budget := 4
	for _, req := range []ThinkingRequest{
		{Effort: ThinkingLow},
		{BudgetTokens: &budget},
	} {
		if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{}`), Thinking: req}); err == nil {
			t.Fatalf("unsupported thinking accepted %#v", req)
		}
	}
	// Empty thinking on an unsupported adapter yields zero effective thinking.
	got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{}`)})
	if err != nil || got.Thinking != (EffectiveThinking{}) {
		t.Fatalf("unsupported empty thinking = %#v, %v", got.Thinking, err)
	}
}

func TestEngineHeaderQueryRejectsInvalidUTF8Atomically(t *testing.T) {
	const marker = "do-not-leak-invalid-utf8"
	for _, tc := range []struct {
		name    string
		action  RequestAction
		allowed RequestPolicy
	}{
		{"set header", RequestSetHeader, RequestPolicy{AllowedHeaders: []string{"X-Safe"}}},
		{"set query", RequestSetQuery, RequestPolicy{AllowedQuery: []string{"mode"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := RequestRule{ID: "invalid-utf8", Action: tc.action, Value: []byte{'"', 0xff, '"'}}
			if tc.action == RequestSetHeader {
				rule.Name = "X-Safe"
			} else {
				rule.Name = "mode"
			}
			adapter := CompiledAdapter{Request: tc.allowed}
			adapter.Request.Rules = []RequestRule{rule}

			got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{"marker":"` + marker + `"}`)})
			if err == nil || !errors.Is(err, ErrRule) {
				t.Fatalf("Apply error = %v, want ErrRule", err)
			}
			if got.Body != nil || got.Thinking != (EffectiveThinking{}) || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
				t.Fatalf("failure was not atomic: %#v", got)
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatalf("error leaked request marker: %v", err)
			}
		})
	}
}

func TestEngineHeaderQueryRejectControlCharsAndNonStrings(t *testing.T) {
	// Runtime CTL defense: even though the compiler validates literals, the
	// engine rejects raw values containing control characters. Names are
	// explicitly allowlisted so the CTL/non-string path is the only failure.
	ctlAdapter := CompiledAdapter{Request: RequestPolicy{AllowedHeaders: []string{"X-Safe"}, Rules: []RequestRule{
		{ID: "h", Action: RequestSetHeader, Name: "X-Safe", Value: []byte(`"a` + "\x00" + `b"`)},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: ctlAdapter, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply accepted CTL header value")
	}
	nonStringAdapter := CompiledAdapter{Request: RequestPolicy{AllowedQuery: []string{"mode"}, Rules: []RequestRule{
		{ID: "q", Action: RequestSetQuery, Name: "mode", Value: []byte(`1`)},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: nonStringAdapter, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply accepted non-string query value")
	}
}

func TestEngineRuntimeDefendsProtectedBodyPaths(t *testing.T) {
	// Rules constructed without Compile must still be unable to read or write
	// protected request channels, including escaped path tokens.
	for _, rule := range []RequestRule{
		{ID: "set", Action: RequestSet, Path: "/model", Value: []byte(`"x"`)},
		{ID: "remove", Action: RequestRemove, Path: "/messages/0"},
		{ID: "map", Action: RequestMapEnum, Path: "/input", EnumMap: map[string]string{"a": "b"}},
		{ID: "clamp", Action: RequestClampNumber, Path: "/prompt", Min: floatp(0), Max: floatp(1)},
		{ID: "copy", Action: RequestCopy, From: "/model", To: "/x"},
		{ID: "rename", Action: RequestRename, From: "/x", To: "/messages/0"},
	} {
		got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{rule}}}, Body: []byte(`{"model":"m","messages":["a"],"input":"a","prompt":1,"x":1}`)})
		if err == nil || !errors.Is(err, ErrRule) || got.Body != nil {
			t.Fatalf("protected rule %#v: got %#v, %v", rule, got, err)
		}
	}
}

func TestEngineDefendsProtectedHeadersQueryAndValueRef(t *testing.T) {
	// Defense in depth: an unvalidated CompiledAdapter attempting to write a
	// protected header, unsafe query name, or ValueRef is rejected at runtime.
	protected := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{
		{ID: "h", Action: RequestSetHeader, Name: "Authorization", Value: []byte(`"x"`)},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: protected, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply wrote protected header")
	}
	badQuery := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{
		{ID: "q", Action: RequestSetQuery, Name: "bad name", Value: []byte(`"x"`)},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: badQuery, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply wrote unsafe query name")
	}
	for _, action := range []RequestAction{RequestSet, RequestCopy, RequestRemove, RequestRename, RequestMapEnum, RequestClampNumber, RequestSetHeader, RequestSetQuery} {
		rule := RequestRule{ID: "v", Action: action, ValueRef: "future-resolver"}
		switch action {
		case RequestSet, RequestMapEnum, RequestClampNumber:
			rule.Path = "/n"
			if action == RequestSet {
				rule.Value = []byte(`1`)
			}
			if action == RequestMapEnum {
				rule.EnumMap = map[string]string{"a": "b"}
			}
			if action == RequestClampNumber {
				rule.Min, rule.Max = floatp(0), floatp(1)
			}
		case RequestCopy, RequestRename:
			rule.From, rule.To = "/a", "/b"
		case RequestRemove:
			rule.Path = "/n"
		case RequestSetHeader, RequestSetQuery:
			rule.Name, rule.Value = "X-Safe", []byte(`"x"`)
		}
		valueRefAdapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{rule}}}
		if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: valueRefAdapter, Body: []byte(`{"n":1,"a":1}`)}); err == nil {
			t.Fatalf("Apply accepted ValueRef for action %q", action)
		}
	}
}

func TestEngineRuntimeDefendsUntrustedAllowlistAndCanonicalHeader(t *testing.T) {
	// An untrusted CompiledAdapter bypasses compilation. set_header must
	// validate the full chain: canonical name match against the allowlist,
	// RFC 7230 token form, protected/credential denylist, and prototype-family
	// rejection. Each case below uses a name in a different canonical/lexical
	// form to exercise the canonical compare path.
	cases := []struct {
		name    string
		rule    RequestRule
		allowed []string
	}{
		// Allowlist contains X-Safe but rule writes a non-allowlisted name.
		{"non-allowlisted header", RequestRule{ID: "h", Action: RequestSetHeader, Name: "X-Other", Value: []byte(`"x"`)}, []string{"X-Safe"}},
		// Allowlist contains X-Safe; rule writes "x-safe" which canonicalizes
		// to the same key and must be accepted — sanity check for canonical match.
		{"canonical-allowlisted header accepts", RequestRule{ID: "h", Action: RequestSetHeader, Name: "x-safe", Value: []byte(`"ok"`)}, []string{"X-Safe"}},
		// Non-RFC token (space) rejected even when allowlist is empty.
		{"invalid rfc token", RequestRule{ID: "h", Action: RequestSetHeader, Name: "Bad Name", Value: []byte(`"x"`)}, nil},
		// Prototype-family header rejected even if listed.
		{"__proto__ header", RequestRule{ID: "h", Action: RequestSetHeader, Name: "__proto__", Value: []byte(`"x"`)}, []string{"__proto__"}},
		{"prototype header", RequestRule{ID: "h", Action: RequestSetHeader, Name: "prototype", Value: []byte(`"x"`)}, []string{"prototype"}},
		{"constructor header", RequestRule{ID: "h", Action: RequestSetHeader, Name: "constructor", Value: []byte(`"x"`)}, []string{"constructor"}},
		// Credential-adjacent name rejected even if listed (runtime mirror).
		{"authorization header", RequestRule{ID: "h", Action: RequestSetHeader, Name: "Authorization", Value: []byte(`"x"`)}, []string{"Authorization"}},
		{"x-api-key header", RequestRule{ID: "h", Action: RequestSetHeader, Name: "x-api-key", Value: []byte(`"x"`)}, []string{"x-api-key"}},
		// Query: allowlisted name accepted.
		{"allowlisted query accepts", RequestRule{ID: "q", Action: RequestSetQuery, Name: "mode", Value: []byte(`"fast"`)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ca := CompiledAdapter{}
			if tc.rule.Action == RequestSetQuery {
				ca.Request.AllowedQuery = []string{"mode"}
			} else {
				ca.Request.AllowedHeaders = tc.allowed
			}
			ca.Request.Rules = []RequestRule{tc.rule}
			got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: ca, Body: []byte(`{}`)})
			accept := strings.Contains(tc.name, "accepts")
			if accept && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if !accept && err == nil {
				t.Fatalf("expected rejection, got %#v", got)
			}
			if accept && tc.rule.Action == RequestSetHeader {
				if got.InjectionPlan.Headers["X-Safe"] != "ok" && got.InjectionPlan.Headers["x-safe"] != "ok" {
					t.Fatalf("header not set: %#v", got.InjectionPlan.Headers)
				}
			}
			if accept && tc.rule.Action == RequestSetQuery {
				if got.InjectionPlan.Query["mode"] != "fast" {
					t.Fatalf("query not set: %#v", got.InjectionPlan.Query)
				}
			}
		})
	}
	// Query allowlist gate: allowlist with mode does NOT accept version.
	queryNotListed := CompiledAdapter{Request: RequestPolicy{AllowedQuery: []string{"mode"}, Rules: []RequestRule{
		{ID: "q", Action: RequestSetQuery, Name: "version", Value: []byte(`"1"`)},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: queryNotListed, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply wrote non-allowlisted query name")
	}
	// Prototype-family query name rejected even when allowlisted.
	protoQuery := CompiledAdapter{Request: RequestPolicy{AllowedQuery: []string{"__proto__"}, Rules: []RequestRule{
		{ID: "q", Action: RequestSetQuery, Name: "__proto__", Value: []byte(`"x"`)},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: protoQuery, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply wrote __proto__ query name")
	}
}

func TestPointerPartsRuntimeLimitsBypassCompiler(t *testing.T) {
	cases := []string{
		"/" + strings.Repeat("a", maxJSONPointerLength),
		"/" + strings.Repeat("a/", maxJSONPointerDepth) + "z",
		string(append([]byte("/"), 0xff)),
	}
	for _, pointer := range cases {
		if _, err := pointerParts(pointer); err == nil {
			t.Fatalf("pointerParts accepted invalid runtime pointer %q", pointer)
		}
	}
}

func TestEnginePointerRejectsPrototypeFamilyTokens(t *testing.T) {
	// Even if a rule's path slips through with a prototype-family terminal,
	// pointerParts rejects the token so it can never become a JSON object key.
	for _, p := range []string{"/__proto__", "/a/prototype", "/x/constructor"} {
		adapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{
			{ID: "set", Action: RequestSet, Path: p, Value: []byte(`1`)},
		}}}
		if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{}`)}); err == nil {
			t.Fatalf("Apply accepted prototype-family pointer %q", p)
		}
	}
	// Source pointer with prototype-family token rejected at parse time, so
	// the body never needs to contain such a key.
	copyAdapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{
		{ID: "copy", Action: RequestCopy, From: "/__proto__", To: "/x"},
	}}}
	if _, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: copyAdapter, Body: []byte(`{}`)}); err == nil {
		t.Fatal("Apply accepted prototype-family source pointer")
	}
}

func TestEngineApplyToleratesNilContext(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "set", Action: RequestSet, Path: "/n", Value: []byte(`1`)}})
	got, err := (Engine{}).Apply(nil, ApplyInput{Adapter: adapter, Body: []byte(`{}`)})
	if err != nil {
		t.Fatalf("Apply with nil context failed: %v", err)
	}
	if len(got.Body) == 0 {
		t.Fatal("Apply produced no body")
	}
}

func TestEngineErrorsAreTypedAndDoNotEchoBody(t *testing.T) {
	const secret = "super-secret-value"
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "missing", Action: RequestRemove, Path: "/missing"}})
	_, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{"k":"` + secret + `"}`)})
	if err == nil {
		t.Fatal("Apply succeeded")
	}
	if !errors.Is(err, ErrRule) {
		t.Fatalf("error is not ErrRule: %v", err)
	}
	var ruleErr *RuleError
	if !errors.As(err, &ruleErr) || ruleErr.RuleID() != "missing" {
		t.Fatalf("error is not RuleError{missing}: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error echoed body content: %v", err)
	}
	// Even a manually-constructed rule with a secret-bearing ID cannot expose
	// that ID through Error or RuleID.
	const ruleIDSecret = "rule-secret\nvalue"
	_, err = (Engine{}).Apply(context.Background(), ApplyInput{Adapter: CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{{ID: ruleIDSecret, Action: RequestRemove, Path: "/missing"}}}}, Body: []byte(`{}`)})
	if !errors.Is(err, ErrRule) || strings.Contains(err.Error(), ruleIDSecret) {
		t.Fatalf("manual rule ID leaked through error: %v", err)
	}
	if !errors.As(err, &ruleErr) || ruleErr.RuleID() != "" {
		t.Fatalf("manual rule ID leaked through typed error: %#v", ruleErr)
	}
	// Invalid input classification.
	_, err = (Engine{}).Apply(context.Background(), ApplyInput{Adapter: compiledEngineAdapter(t, nil), Body: []byte(`not json`)})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid body error = %v, want ErrInvalidInput", err)
	}
}

func TestEngineContextCancellationStopsApply(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{{ID: "set", Action: RequestSet, Path: "/n", Value: []byte(`1`)}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Engine{}).Apply(ctx, ApplyInput{Adapter: adapter, Body: []byte(`{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply error = %v, want context.Canceled", err)
	}
}

type cancelAfterContext struct {
	context.Context
	remaining int
}

func (c *cancelAfterContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestEngineContextCancellationDuringDecodeIsAtomic(t *testing.T) {
	// The custom context deterministically cancels during node-by-node decode;
	// no scheduler timing or large allocation race is required.
	body := `{"items":[` + strings.TrimRight(strings.Repeat(`0,`, 512), ",") + `]}`
	ctx := &cancelAfterContext{Context: context.Background(), remaining: 32}
	got, err := (Engine{}).Apply(ctx, ApplyInput{Adapter: CompiledAdapter{}, Body: []byte(body)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply error = %v, want context.Canceled", err)
	}
	if got.Body != nil || got.Thinking != (EffectiveThinking{}) || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
		t.Fatalf("cancelled Apply leaked result: %#v", got)
	}
}

func TestEngineInputAdapterOutputIsolation(t *testing.T) {
	adapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{{ID: "set", Action: RequestSet, Path: "/nested/value", Value: []byte(`"changed"`)}}}}
	input := []byte(`{"nested":{"value":"original"}}`)
	inputBefore := append([]byte(nil), input...)
	adapterBefore := append([]byte(nil), adapter.Request.Rules[0].Value...)
	got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: input})
	if err != nil {
		t.Fatal(err)
	}
	got.Body[0] = '['
	if string(input) != string(inputBefore) {
		t.Fatalf("Apply mutated input: %s", input)
	}
	if string(adapter.Request.Rules[0].Value) != string(adapterBefore) {
		t.Fatalf("Apply mutated adapter: %s", adapter.Request.Rules[0].Value)
	}
	again, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: input})
	if err != nil || string(again.Body) != `{"nested":{"value":"changed"}}` {
		t.Fatalf("output mutation aliased later Apply: body=%s err=%v", again.Body, err)
	}
	if got.InjectionPlan.Headers == nil || got.InjectionPlan.Query == nil {
		t.Fatalf("missing independently owned output maps: %#v", got)
	}
}

func TestEngineFailureErrorAndZeroResultMatrix(t *testing.T) {
	valid := CompiledAdapter{}
	ruleFailure := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{{ID: "missing", Action: RequestRemove, Path: "/missing"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		adapter CompiledAdapter
		body    []byte
		want    error
	}{
		{"invalid input", context.Background(), valid, []byte(`not-json`), ErrInvalidInput},
		{"rule failure", context.Background(), ruleFailure, []byte(`{}`), ErrRule},
		{"cancelled", ctx, valid, []byte(`{}`), context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (Engine{}).Apply(tc.ctx, ApplyInput{Adapter: tc.adapter, Body: tc.body})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tc.want)
			}
			if got.Body != nil || got.Thinking != (EffectiveThinking{}) || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
				t.Fatalf("failure was not atomic: %#v", got)
			}
		})
	}
}

func TestEngineConcurrentSafe(t *testing.T) {
	adapter := compiledEngineAdapter(t, []RequestRule{
		{ID: "set", Action: RequestSet, Path: "/n", Value: []byte(`1`)},
		{ID: "append", Action: RequestSet, Path: "/items/-", Value: []byte(`"x"`)},
		{ID: "clamp", Action: RequestClampNumber, Path: "/t", Min: floatp(0), Max: floatp(1)},
	})
	body := []byte(`{"n":0,"items":[],"t":5}`)
	const n = 64
	var wg sync.WaitGroup
	type result struct {
		body string
		err  error
	}
	results := make([]result, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: body})
			results[i] = result{body: string(got.Body), err: err}
		}(i)
	}
	wg.Wait()
	const golden = `{"items":["x"],"n":1,"t":1}`
	for i, got := range results {
		if got.err != nil {
			t.Fatalf("goroutine %d failed: %v", i, got.err)
		}
		if got.body != golden {
			t.Fatalf("goroutine %d output = %s, want %s", i, got.body, golden)
		}
	}
}

func TestResponseMatchesTruthTable(t *testing.T) {
	match := ResponseMatch{HTTPStatuses: []int{429}, ErrorCodes: []string{"busy"}}
	for _, tc := range []struct {
		name     string
		upstream UpstreamResponse
		want     bool
	}{
		{name: "all populated dimensions match", upstream: UpstreamResponse{HTTPStatus: 429, ErrorCode: "busy"}, want: true},
		{name: "status misses", upstream: UpstreamResponse{HTTPStatus: 503, ErrorCode: "busy"}},
		{name: "code misses", upstream: UpstreamResponse{HTTPStatus: 429, ErrorCode: "other"}},
		{name: "both miss", upstream: UpstreamResponse{HTTPStatus: 503, ErrorCode: "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseMatches(match, tc.upstream); got != tc.want {
				t.Fatalf("responseMatches(%#v) = %t, want %t", tc.upstream, got, tc.want)
			}
		})
	}
	if responseMatches(ResponseMatch{}, UpstreamResponse{HTTPStatus: 200, ErrorCode: "anything"}) {
		t.Fatal("empty match must not be a catch-all")
	}
}

func TestResponseMatchesDimensionORAndCombinedSignals(t *testing.T) {
	match := ResponseMatch{
		HTTPStatuses:     []int{429, 503},
		ErrorCodes:       []string{"busy", "overloaded"},
		ErrorTypes:       []string{"temporary", "capacity"},
		MessageContains:  []string{"retry later", "try again"},
		FinishReasons:    []string{"length", "content_filter"},
		StreamEventTypes: []string{"error", "response.failed"},
	}
	if !responseMatches(match, UpstreamResponse{HTTPStatus: 503, ErrorCode: "overloaded", ErrorType: "capacity", Message: "please try again", FinishReason: "content_filter", StreamEventType: "response.failed"}) {
		t.Fatal("alternatives within each dimension must match")
	}
	for _, tc := range []struct {
		name     string
		upstream UpstreamResponse
	}{
		{name: "message misses", upstream: UpstreamResponse{HTTPStatus: 429, ErrorCode: "busy", ErrorType: "temporary", Message: "upstream secret", FinishReason: "length", StreamEventType: "error"}},
		{name: "finish reason misses", upstream: UpstreamResponse{HTTPStatus: 429, ErrorCode: "busy", ErrorType: "temporary", Message: "retry later", FinishReason: "stop", StreamEventType: "error"}},
		{name: "stream event misses", upstream: UpstreamResponse{HTTPStatus: 429, ErrorCode: "busy", ErrorType: "temporary", Message: "retry later", FinishReason: "length", StreamEventType: "delta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if responseMatches(match, tc.upstream) {
				t.Fatalf("responseMatches(%#v) matched despite missing populated dimension", tc.upstream)
			}
		})
	}
}

func TestEngineMapResponseUsesCompiledOrderDefaultsAndSanitizes(t *testing.T) {
	// ResponseRules have already been compiler-sorted by priority before the
	// runtime receives this immutable adapter. MapResponse must keep that order
	// rather than re-sort, so the earlier rule wins even though both match.
	adapter := CompiledAdapter{ResponseRules: []ResponseRule{
		{ID: "first", Priority: 10, Match: ResponseMatch{HTTPStatuses: []int{503}}, Output: ResponseOutput{HTTPStatus: 429, ErrorCode: "RETRY", ErrorType: "retry", Message: "try again"}},
		{ID: "later", Priority: 20, Match: ResponseMatch{HTTPStatuses: []int{503}}, Output: ResponseOutput{HTTPStatus: 500, ErrorCode: "LATER", ErrorType: "later", Message: "must not win"}},
	}}
	upstream := UpstreamResponse{HTTPStatus: 503, Message: "upstream secret"}
	got := (Engine{}).MapResponse(adapter, upstream)
	if got.MatchedID != "first" || got.HTTPStatus != 429 || got.ErrorCode != "RETRY" || strings.Contains(got.Message, "secret") {
		t.Fatalf("MapResponse = %#v", got)
	}

	for _, tc := range []struct {
		name     string
		upstream UpstreamResponse
		status   int
		code     string
	}{
		{name: "legacy timeout", upstream: UpstreamResponse{HTTPStatus: 0, Message: "upstream secret"}, status: 504, code: "UPSTREAM_TIMEOUT"},
		{name: "classified timeout", upstream: UpstreamResponse{HTTPStatus: 0, ErrorType: "timeout", Message: "upstream secret"}, status: 504, code: "UPSTREAM_TIMEOUT"},
		{name: "classified transport", upstream: UpstreamResponse{HTTPStatus: 0, ErrorType: "transport", Message: "upstream secret"}, status: 502, code: "UPSTREAM_TRANSPORT_ERROR"},
		{name: "classified protocol", upstream: UpstreamResponse{HTTPStatus: 0, ErrorType: "protocol", Message: "upstream secret"}, status: 502, code: "UPSTREAM_PROTOCOL_ERROR"},
		{name: "upstream client error", upstream: UpstreamResponse{HTTPStatus: 404, Message: "upstream secret"}, status: 400, code: "UPSTREAM_INVALID_REQUEST"},
		{name: "upstream server error", upstream: UpstreamResponse{HTTPStatus: 503, ErrorCode: "not matched", Message: "upstream secret"}, status: 502, code: "UPSTREAM_ERROR"},
		{name: "protocol error", upstream: UpstreamResponse{HTTPStatus: 200, Message: "upstream secret"}, status: 502, code: "UPSTREAM_PROTOCOL_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (Engine{}).MapResponse(CompiledAdapter{}, tc.upstream)
			if got.MatchedID != "" || got.HTTPStatus != tc.status || got.ErrorCode != tc.code || strings.Contains(got.Message, "secret") {
				t.Fatalf("fallback = %#v", got)
			}
		})
	}
}

func TestEngineMapResponseRejectsManualUnsafeOutput(t *testing.T) {
	upstream := UpstreamResponse{HTTPStatus: 503, Message: "upstream secret"}
	for _, rule := range []ResponseRule{
		{ID: "unsafe id\nsecret", Match: ResponseMatch{HTTPStatuses: []int{503}}, Output: ResponseOutput{HTTPStatus: 429, ErrorCode: "RETRY", ErrorType: "retry", Message: "safe"}},
		{ID: "bad-status", Match: ResponseMatch{HTTPStatuses: []int{503}}, Output: ResponseOutput{HTTPStatus: 200, ErrorCode: "RETRY", ErrorType: "retry", Message: "safe"}},
		{ID: "bad-message", Match: ResponseMatch{HTTPStatuses: []int{503}}, Output: ResponseOutput{HTTPStatus: 429, ErrorCode: "RETRY", ErrorType: "retry", Message: "configured secret\nvalue"}},
		{ID: "empty-match", Output: ResponseOutput{HTTPStatus: 429, ErrorCode: "RETRY", ErrorType: "retry", Message: "safe"}},
	} {
		got := (Engine{}).MapResponse(CompiledAdapter{ResponseRules: []ResponseRule{rule}}, upstream)
		if got.MatchedID != "" || got.HTTPStatus != 502 || got.ErrorCode != "UPSTREAM_ERROR" || strings.Contains(got.Message, "secret") {
			t.Fatalf("unsafe manual rule produced public response: %#v", got)
		}
	}
}

func FuzzEngineMapResponse(f *testing.F) {
	f.Add(503, "busy", "temporary", "retry later", "length", "error")
	adapter := CompiledAdapter{ResponseRules: []ResponseRule{{
		ID: "mapped",
		Match: ResponseMatch{
			HTTPStatuses:     []int{503},
			ErrorCodes:       []string{"busy"},
			ErrorTypes:       []string{"temporary"},
			MessageContains:  []string{"retry"},
			FinishReasons:    []string{"length"},
			StreamEventTypes: []string{"error"},
		},
		Output: ResponseOutput{HTTPStatus: 429, ErrorCode: "RETRY", ErrorType: "retry", Message: "try again"},
	}}}
	f.Fuzz(func(t *testing.T, status int, code, kind, message, finish, stream string) {
		got := (Engine{}).MapResponse(adapter, UpstreamResponse{HTTPStatus: status, ErrorCode: code, ErrorType: kind, Message: message, FinishReason: finish, StreamEventType: stream})
		if got.MatchedID == "mapped" && (got.HTTPStatus != 429 || got.ErrorCode != "RETRY" || got.ErrorType != "retry" || got.Message != "try again") {
			t.Fatalf("mapped response = %#v", got)
		}
	})
}

func TestEngineRuleErrorIsOpaqueAndClassifiable(t *testing.T) {
	e := newRuleError("r1")
	if !errors.Is(e, ErrRule) {
		t.Fatal("RuleError does not unwrap to ErrRule")
	}
	if e.RuleID() != "r1" {
		t.Fatalf("RuleError RuleID = %q, want r1", e.RuleID())
	}
	if strings.Contains(e.Error(), "r1") {
		t.Fatalf("RuleError exposed rule ID in message: %q", e.Error())
	}
	// External packages can form only RuleError{}, whose error and unwrap
	// remain the fixed sentinel; no arbitrary cause can be injected or leaked.
	if got := (&RuleError{}).Error(); got != ErrRule.Error() || !errors.Is(&RuleError{}, ErrRule) {
		t.Fatalf("zero RuleError = %q, does not safely classify", got)
	}
}

func FuzzEngineApply(f *testing.F) {
	f.Add(`{"items":[],"n":1}`)
	f.Add(`{"object":{},"state":"cold","temperature":2}`)
	f.Add(`{"src":{"inner":"x"},"items":["a"]}`)
	adapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{
		{ID: "set", Action: RequestSet, Path: "/n", Value: []byte(`1`)},
		{ID: "append", Action: RequestSet, Path: "/items/-", Value: []byte(`"z"`)},
		{ID: "clamp", Action: RequestClampNumber, Path: "/t", Min: floatp(0), Max: floatp(1)},
	}}}
	f.Fuzz(func(t *testing.T, body string) {
		got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(body)})
		if err != nil {
			if got.Body != nil || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
				t.Fatalf("failure was not atomic: %#v", got)
			}
			return
		}
		if len(got.Body) > maxEngineJSONBytes {
			t.Fatalf("output exceeds 2 MiB")
		}
	})
}

// TestEngineMalformedManualRulesFailClosedNotPanic exercises rule shapes that
// the compiler rejects but a manually-constructed CompiledAdapter can still
// carry. Every case must fail closed as a RuleError (errors.Is ErrRule) without
// panicking, and the returned AppliedRequest must be the zero value so no
// partial transformation escapes.
func TestEngineMalformedManualRulesFailClosedNotPanic(t *testing.T) {
	cases := []struct {
		name   string
		rule   RequestRule
		body   string
		ruleID string
	}{
		{"clamp nil min", RequestRule{ID: "c", Action: RequestClampNumber, Path: "/n", Max: floatp(1)}, `{"n":1}`, "c"},
		{"clamp nil max", RequestRule{ID: "c", Action: RequestClampNumber, Path: "/n", Min: floatp(0)}, `{"n":1}`, "c"},
		{"clamp nil bounds", RequestRule{ID: "c", Action: RequestClampNumber, Path: "/n"}, `{"n":1}`, "c"},
		{"clamp bounds missing non-number source", RequestRule{ID: "c", Action: RequestClampNumber, Path: "/n"}, `{"n":"x"}`, "c"},
		{"set empty path", RequestRule{ID: "s", Action: RequestSet, Path: "", Value: []byte(`1`)}, `{}`, "s"},
		{"copy empty destination", RequestRule{ID: "cp", Action: RequestCopy, From: "/a", To: ""}, `{"a":1}`, "cp"},
		{"rename empty destination", RequestRule{ID: "rn", Action: RequestRename, From: "/a", To: ""}, `{"a":1}`, "rn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := CompiledAdapter{Request: RequestPolicy{Rules: []RequestRule{tc.rule}}}
			got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(tc.body)})
			if err == nil {
				t.Fatalf("Apply succeeded for malformed rule: %#v", got)
			}
			if !errors.Is(err, ErrRule) {
				t.Fatalf("error is not ErrRule: %v", err)
			}
			var ruleErr *RuleError
			if !errors.As(err, &ruleErr) || ruleErr.RuleID() != tc.ruleID {
				t.Fatalf("error = %v, want RuleError{%s}", err, tc.ruleID)
			}
			if got.Body != nil || got.Thinking != (EffectiveThinking{}) || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
				t.Fatalf("failure was not atomic: %#v", got)
			}
		})
	}
}

// FuzzEngineMalformedRuleShapes fuzzes combinations of action and
// missing/empty fields to guarantee the engine never panics on a
// manually-constructed CompiledAdapter: every input must either succeed or
// fail closed as a typed error, never panic.
func FuzzEngineMalformedRuleShapes(f *testing.F) {
	// Seed corpus: a byte selects the action; a second byte is a bitmask that
	// drops/empties fields (path, from, to, min, max). Each seed pairs a
	// representative action with a representative malformation so the fuzzer
	// has a concrete starting point.
	type seed struct{ action, mask byte }
	actions := []RequestAction{RequestSet, RequestCopy, RequestRename, RequestRemove, RequestMapEnum, RequestClampNumber, RequestSetHeader, RequestSetQuery}
	for _, a := range actions {
		for _, m := range []byte{0, 1, 2, 4, 8, 16, 31} {
			f.Add(byte(len(a)), m)
		}
	}
	f.Fuzz(func(t *testing.T, actionIdx, mask byte) {
		idx := int(actionIdx) % len(actions)
		action := actions[idx]
		rule := RequestRule{ID: "m", Action: action}
		if mask&1 == 0 {
			rule.Path = "/n"
		}
		if mask&2 == 0 {
			rule.From = "/a"
		}
		if mask&4 == 0 {
			rule.To = "/b"
		}
		if mask&8 == 0 {
			rule.Min = floatp(0)
		}
		if mask&16 == 0 {
			rule.Max = floatp(1)
		}
		// Always provide a valid value and enum map so the only malformations
		// exercised are the structural field drops above.
		rule.Value = []byte(`1`)
		rule.EnumMap = map[string]string{"a": "b"}
		rule.Name = "X-Safe"
		adapter := CompiledAdapter{
			Request: RequestPolicy{
				AllowedHeaders: []string{"X-Safe"},
				AllowedQuery:   []string{"mode"},
				Rules:          []RequestRule{rule},
			},
		}
		got, err := (Engine{}).Apply(context.Background(), ApplyInput{Adapter: adapter, Body: []byte(`{"n":1,"a":1}`)})
		if err != nil {
			if got.Body != nil || got.InjectionPlan.Headers != nil || got.InjectionPlan.Query != nil {
				t.Fatalf("failure was not atomic: %#v", got)
			}
			return
		}
		if len(got.Body) > maxEngineJSONBytes {
			t.Fatalf("output exceeds 2 MiB")
		}
	})
}

// deepEqualJSON compares two decoded JSON values for structural equality,
// tolerating the float64-vs-json.Number distinction in expected literals.
func deepEqualJSON(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			if !deepEqualJSON(v, bv[k]) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualJSON(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
