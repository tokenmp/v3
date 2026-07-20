// Package adapter defines the data-driven, finite rule types used to adapt
// Executor requests to upstream providers. engine.go implements the
// stateless runtime that applies a compiled adapter's finite request DSL to a
// caller-supplied JSON object and maps classified upstream response metadata.
// It performs no I/O and is safe for concurrent use: Engine is a zero-width
// value, Apply mutates only locally-decoded data, and the compiled adapter is
// treated as read-only.
package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxEngineJSONBytes bounds both the request body input and the transformed
// output at 2 MiB, matching the hardened compiler's literal/size policy.
const maxEngineJSONBytes = 2 << 20

// Sentinel errors. Apply wraps input validation failures with ErrInvalidInput
// and rule execution failures with ErrRule (carried inside a RuleError). Error
// messages are deliberately generic: they never echo request body contents or
// rule literal values.
var (
	ErrInvalidInput = errors.New("invalid adapter input")
	ErrRule         = errors.New("adapter rule failed")
)

// RuleError identifies a failed compiled rule without exposing an arbitrary
// wrapped error. Its fields are intentionally private: callers can classify a
// failure with errors.Is/As and inspect RuleID, but cannot construct an error
// whose Error or Unwrap result leaks a secret-bearing cause.
type RuleError struct{ ruleID string }

func (e *RuleError) Error() string { return ErrRule.Error() }

// Unwrap supports errors.Is(err, ErrRule) for every RuleError.
func (e *RuleError) Unwrap() error { return ErrRule }

// RuleID returns the compiled rule identifier associated with the failure.
// It is metadata only; Error deliberately does not render it.
func (e *RuleError) RuleID() string {
	if e == nil {
		return ""
	}
	return e.ruleID
}

func newRuleError(ruleID string) *RuleError {
	// Rule IDs from compiled config are safe segments. A manually-constructed
	// adapter is untrusted, so do not preserve an arbitrary ID as externally
	// inspectable error metadata.
	if !safeSegment(ruleID) {
		ruleID = ""
	}
	return &RuleError{ruleID: ruleID}
}

// Engine is stateless and safe for concurrent use. It deliberately only
// evaluates the finite, compiler-validated adapter DSL; it performs no I/O.
type Engine struct{}

// ThinkingRequest is the caller-side reasoning request. Thinking is applied
// only when Enabled. A zero Effort then means "use the adapter default"; a nil
// BudgetTokens means "use the mapped budget".
type ThinkingRequest struct {
	Enabled      bool
	Effort       ThinkingEffort
	BudgetTokens *int
}

// EffectiveThinking is the sanitized, provider-bound result of mapping a
// ThinkingRequest through a compiled ThinkingPolicy.
type EffectiveThinking struct {
	RequestedEffort ThinkingEffort
	EffectiveEffort ThinkingEffort
	RequestedBudget int
	EffectiveBudget int
	Degraded        bool
}

// ApplyInput is the complete, stateless input to Engine.Apply. The Body is a
// raw JSON object; Headers/Query are never accepted as input — they are only
// ever produced by set_header/set_query rules into the InjectionPlan.
type ApplyInput struct {
	Adapter CompiledAdapter
	// ModelThinking is the selected compiled model's thinking bounds. It is
	// required whenever Thinking.Enabled so an adapter cannot apply its wider
	// policy to a more constrained selected model.
	ModelThinking ThinkingInput
	Body          json.RawMessage
	Thinking      ThinkingRequest
}

// InjectionPlan carries the header and query parameters produced by
// set_header/set_query rules. Values are JSON strings without control
// characters; names are compiler-allowlisted and runtime-defended.
type InjectionPlan struct {
	Headers map[string]string
	Query   map[string]string
}

// AppliedRequest is the atomic result of a successful Apply. On any failure
// Apply returns the zero AppliedRequest, so a caller never observes a partial
// transformation.
type AppliedRequest struct {
	Body          json.RawMessage
	Thinking      EffectiveThinking
	InjectionPlan InjectionPlan
}

// UpstreamResponse contains only classified upstream metadata. Callers must
// not pass arbitrary response bodies into mapping, preventing leakage.
type UpstreamResponse struct {
	HTTPStatus      int
	ErrorCode       string
	ErrorType       string
	Message         string
	FinishReason    string
	StreamEventType string
}

// MappedResponse is the sanitized protocol-neutral result selected by a
// compiled response rule or the fail-closed default mapping.
type MappedResponse struct {
	HTTPStatus int
	ErrorCode  string
	ErrorType  string
	Message    string
	MatchedID  string
}

// Apply evaluates a compiled adapter's finite request DSL against a single
// JSON object. It is stateless: every input is decoded into a private tree,
// transformed in place, and re-encoded. Failure is atomic — the zero
// AppliedRequest is returned and no partial body escapes.
func (Engine) Apply(ctx context.Context, in ApplyInput) (AppliedRequest, error) {
	if ctx == nil {
		// Defensive: a caller that passes a nil context still gets a
		// deterministic, cancellation-free execution instead of a panic.
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	document, err := decodeEngineJSONContext(ctx, in.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return AppliedRequest{}, ctxErr
		}
		return AppliedRequest{}, fmt.Errorf("%w: request body: %v", ErrInvalidInput, err)
	}
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	object, ok := document.(map[string]any)
	if !ok {
		// The hardened contract requires exactly one top-level JSON object.
		return AppliedRequest{}, fmt.Errorf("%w: request body must be a JSON object", ErrInvalidInput)
	}
	thinking, err := effectiveThinking(in.Adapter.Thinking, in.ModelThinking, in.Thinking)
	if err != nil {
		return AppliedRequest{}, err
	}
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	out := AppliedRequest{
		Thinking:      thinking,
		InjectionPlan: InjectionPlan{Headers: map[string]string{}, Query: map[string]string{}},
	}
	// Build canonical allowlists once so every set_header/set_query rule is
	// defended identically, including against a manually-constructed or
	// otherwise untrusted CompiledAdapter that bypassed compilation.
	allowedHeaders := buildAllowedHeaders(in.Adapter.Request.AllowedHeaders)
	allowedQuery := buildAllowedQuery(in.Adapter.Request.AllowedQuery)
	for _, rule := range in.Adapter.Request.Rules {
		if err := ctx.Err(); err != nil {
			return AppliedRequest{}, err
		}
		if err := applyRule(object, rule, allowedHeaders, allowedQuery, out.InjectionPlan.Headers, out.InjectionPlan.Query); err != nil {
			// applyRule errors are intentionally not exposed: a manually-built
			// adapter must not turn an implementation error into caller-visible
			// configuration or payload disclosure.
			return AppliedRequest{}, newRuleError(rule.ID)
		}
		if err := ctx.Err(); err != nil {
			return AppliedRequest{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	body, err := json.Marshal(object)
	if err != nil {
		return AppliedRequest{}, fmt.Errorf("%w: encode output", ErrRule)
	}
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	// The hardened compiler bounds every JSON literal at 2 MiB / depth 64 /
	// nodes 10000, and decodeEngineJSON enforces the same triple cap on the
	// request body input. A transformation rule (copy/rename/append/set) can
	// duplicate or re-nest content so the transformed output exceeds those
	// bounds even when the input was valid: copying a 10000-node subtree into
	// a second location doubles the node count, and appending into a deeper
	// path can exceed depth 64. The marshaled output is therefore re-run
	// through the same decoder to enforce the byte, depth, and node caps on
	// the result. The original marshaled bytes are retained unchanged on
	// success so output lexemes (e.g. number formatting) are never rewritten
	// by a second encode pass.
	if _, err := decodeEngineJSONContext(ctx, body); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return AppliedRequest{}, ctxErr
		}
		return AppliedRequest{}, fmt.Errorf("%w: output exceeds structural cap", ErrRule)
	}
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	out.Body = json.RawMessage(body)
	if err := ctx.Err(); err != nil {
		return AppliedRequest{}, err
	}
	return out, nil
}

// MapResponse evaluates adapter.ResponseRules in their compiler-established
// order and returns the first match. It deliberately does not sort at runtime:
// CompiledAdapter.ResponseRules are pre-sorted by the compiler (priority,
// specificity, then ID), preserving a revision's deterministic policy.
func (Engine) MapResponse(adapter CompiledAdapter, upstream UpstreamResponse) MappedResponse {
	for _, rule := range adapter.ResponseRules {
		// CompiledAdapter values can be manually constructed outside Compile.
		// Never let an invalid rule turn arbitrary config into a public response;
		// skip it and use the sanitized default if no valid rule matches.
		if !safeSegment(rule.ID) || validateResponseMatch(rule.Match) != nil || validateResponseOutput(rule.Output) != nil {
			continue
		}
		if responseMatches(rule.Match, upstream) {
			return MappedResponse{HTTPStatus: rule.Output.HTTPStatus, ErrorCode: rule.Output.ErrorCode, ErrorType: rule.Output.ErrorType, Message: rule.Output.Message, MatchedID: rule.ID}
		}
	}
	return defaultResponse(upstream.HTTPStatus)
}

// responseMatches requires every populated dimension to match. Values within
// one dimension are alternatives (OR), while populated dimensions compose
// conjunctively (AND). A rule with no populated dimensions never matches, so
// an incomplete/manual CompiledAdapter cannot become a catch-all rule.
func responseMatches(match ResponseMatch, upstream UpstreamResponse) bool {
	populated := false
	if len(match.HTTPStatuses) != 0 {
		populated = true
		if !containsInt(match.HTTPStatuses, upstream.HTTPStatus) {
			return false
		}
	}
	if len(match.ErrorCodes) != 0 {
		populated = true
		if !containsString(match.ErrorCodes, upstream.ErrorCode) {
			return false
		}
	}
	if len(match.ErrorTypes) != 0 {
		populated = true
		if !containsString(match.ErrorTypes, upstream.ErrorType) {
			return false
		}
	}
	if len(match.MessageContains) != 0 {
		populated = true
		if !containsSubstring(match.MessageContains, upstream.Message) {
			return false
		}
	}
	if len(match.FinishReasons) != 0 {
		populated = true
		if !containsString(match.FinishReasons, upstream.FinishReason) {
			return false
		}
	}
	if len(match.StreamEventTypes) != 0 {
		populated = true
		if !containsString(match.StreamEventTypes, upstream.StreamEventType) {
			return false
		}
	}
	return populated
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, text string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func defaultResponse(status int) MappedResponse {
	switch {
	case status == 0:
		return MappedResponse{HTTPStatus: 504, ErrorCode: "UPSTREAM_TIMEOUT", ErrorType: "upstream_timeout", Message: "upstream timeout"}
	case status >= 400 && status < 500:
		return MappedResponse{HTTPStatus: 400, ErrorCode: "UPSTREAM_INVALID_REQUEST", ErrorType: "upstream_invalid_request", Message: "upstream rejected request"}
	case status >= 500 && status < 600:
		return MappedResponse{HTTPStatus: 502, ErrorCode: "UPSTREAM_ERROR", ErrorType: "upstream_error", Message: "upstream error"}
	default:
		return MappedResponse{HTTPStatus: 502, ErrorCode: "UPSTREAM_PROTOCOL_ERROR", ErrorType: "upstream_protocol_error", Message: "invalid upstream response"}
	}
}

func effectiveThinking(policy ThinkingPolicy, model ThinkingInput, requested ThinkingRequest) (EffectiveThinking, error) {
	if !requested.Enabled {
		// Do not silently honor thinking parameters when the capability is
		// disabled. This also preserves the old fail-closed behavior for an
		// unsupported adapter while making Enabled the unambiguous gate.
		if requested.Effort != "" || requested.BudgetTokens != nil {
			return EffectiveThinking{}, fmt.Errorf("%w: thinking is disabled", ErrInvalidInput)
		}
		return EffectiveThinking{}, nil
	}
	// ModelThinking is request-specific rather than adapter-specific: an
	// adapter may serve models with different bounds. Its zero value is not a
	// permissive default; it is an unsupported model and must fail closed.
	if !model.Supported || !model.DefaultEffort.Valid() || !model.MaxEffort.Valid() || rank(model.DefaultEffort) > rank(model.MaxEffort) || model.MinBudgetToken < 0 || model.MaxBudgetToken < model.MinBudgetToken {
		return EffectiveThinking{}, fmt.Errorf("%w: model thinking is unsupported or invalid", ErrInvalidInput)
	}
	if !policy.Supported {
		return EffectiveThinking{}, fmt.Errorf("%w: thinking is unsupported", ErrInvalidInput)
	}
	effort := requested.Effort
	if effort == "" {
		effort = policy.DefaultEffort
	}
	if !effort.Valid() {
		return EffectiveThinking{}, fmt.Errorf("%w: invalid thinking effort", ErrInvalidInput)
	}
	effective, ok := policy.EffortMapping[effort]
	if !ok || !effective.Valid() || rank(effective) > rank(model.MaxEffort) {
		return EffectiveThinking{}, fmt.Errorf("%w: unsupported thinking effort", ErrInvalidInput)
	}
	minBudget := max(policy.MinBudgetToken, model.MinBudgetToken)
	maxBudget := min(policy.MaxBudgetToken, model.MaxBudgetToken)
	if minBudget > maxBudget {
		return EffectiveThinking{}, fmt.Errorf("%w: thinking budget bounds do not intersect", ErrInvalidInput)
	}
	requestedBudget := 0
	if requested.BudgetTokens != nil {
		requestedBudget = *requested.BudgetTokens
		if requestedBudget < minBudget || requestedBudget > maxBudget {
			return EffectiveThinking{}, fmt.Errorf("%w: thinking budget outside bounds", ErrInvalidInput)
		}
	}
	effectiveBudget := policy.BudgetMapping[effective]
	if requested.BudgetTokens != nil {
		effectiveBudget = requestedBudget
	}
	if effectiveBudget < minBudget || effectiveBudget > maxBudget {
		return EffectiveThinking{}, fmt.Errorf("%w: effective thinking budget outside bounds", ErrInvalidInput)
	}
	return EffectiveThinking{RequestedEffort: effort, EffectiveEffort: effective, RequestedBudget: requestedBudget, EffectiveBudget: effectiveBudget, Degraded: rank(effective) < rank(effort)}, nil
}

func applyRule(document any, rule RequestRule, allowedHeaders, allowedQuery map[string]bool, headers, query map[string]string) error {
	// Defense in depth: the compiler rejects ValueRef and protected paths,
	// but a CompiledAdapter is not a trust boundary. Check every body-pointer
	// channel before reading or mutating it.
	if rule.ValueRef != "" {
		return fmt.Errorf("%w: value reference unsupported", ErrRule)
	}
	for _, path := range ruleBodyPaths(rule) {
		if protectedRuntimePath(path) {
			return fmt.Errorf("%w: protected request path", ErrRule)
		}
	}
	switch rule.Action {
	case RequestSet:
		value, err := decodeEngineJSON(rule.Value)
		if err != nil {
			return fmt.Errorf("%w: invalid set value", ErrRule)
		}
		// Guard an empty path before pointerTerminal, which would otherwise
		// slice out of range on a manually-constructed rule that bypassed
		// compilation. pointerSet/pointerAppend reject it through pointerParts,
		// but only after the terminal check below would already have panicked.
		if rule.Path == "" {
			return fmt.Errorf("%w: invalid set path", ErrRule)
		}
		if pointerTerminal(rule.Path) == "-" {
			return pointerAppend(document, rule.Path, value)
		}
		return pointerSet(document, rule.Path, value)
	case RequestCopy:
		value, err := pointerGet(document, rule.From)
		if err != nil {
			return err
		}
		value, err = deepCopyJSON(value)
		if err != nil {
			return fmt.Errorf("%w: copy deep copy", ErrRule)
		}
		// Guard an empty destination before pointerTerminal for the same
		// reason as RequestSet: a manually-constructed rule must fail closed
		// instead of slicing out of range.
		if rule.To == "" {
			return fmt.Errorf("%w: invalid copy destination", ErrRule)
		}
		if pointerTerminal(rule.To) == "-" {
			return pointerAppend(document, rule.To, value)
		}
		return pointerSet(document, rule.To, value)
	case RequestRename:
		value, err := pointerGet(document, rule.From)
		if err != nil {
			return err
		}
		value, err = deepCopyJSON(value)
		if err != nil {
			return fmt.Errorf("%w: rename deep copy", ErrRule)
		}
		return pointerRename(document, rule.From, rule.To, value)
	case RequestRemove:
		return pointerRemove(document, rule.Path)
	case RequestMapEnum:
		value, err := pointerGet(document, rule.Path)
		if err != nil {
			return err
		}
		stringValue, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: enum source is not a string", ErrRule)
		}
		mapped, ok := rule.EnumMap[stringValue]
		if !ok {
			return nil
		}
		return pointerSet(document, rule.Path, mapped)
	case RequestClampNumber:
		value, err := pointerGet(document, rule.Path)
		if err != nil {
			return err
		}
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%w: clamp source is not a number", ErrRule)
		}
		// Compare decimal values, never float64 conversions of the request
		// lexeme. In particular, 9007199254740993 must compare above a
		// 9007199254740992 bound rather than collapse to the same binary float.
		source, ok := new(big.Rat).SetString(number.String())
		if !ok {
			return fmt.Errorf("%w: clamp source is not a finite decimal", ErrRule)
		}
		// Defense in depth: the compiler rejects nil/non-finite/inverted bounds,
		// but manually-built compiled adapters must fail closed rather than panic.
		if rule.Min == nil || rule.Max == nil {
			return fmt.Errorf("%w: clamp bounds missing", ErrRule)
		}
		min, minLexeme, ok := clampBound(*rule.Min)
		if !ok {
			return fmt.Errorf("%w: clamp minimum is not finite", ErrRule)
		}
		max, maxLexeme, ok := clampBound(*rule.Max)
		if !ok || min.Cmp(max) > 0 {
			return fmt.Errorf("%w: invalid clamp bounds", ErrRule)
		}
		switch {
		case source.Cmp(min) < 0:
			return pointerSet(document, rule.Path, json.Number(minLexeme))
		case source.Cmp(max) > 0:
			return pointerSet(document, rule.Path, json.Number(maxLexeme))
		default:
			// Keep the original valid JSON number spelling (including exponent
			// form) when it already lies within the decimal interval.
			return nil
		}
	case RequestSetHeader:
		// Mirror the compiler's triple gate at runtime so an untrusted
		// CompiledAdapter cannot inject headers: the name must be a valid RFC
		// 7230 token, must not be a protected/credential name, must not be a
		// prototype-family name, and must be in the canonical allowlist.
		if !rfcToken(rule.Name) || deniedHeader(rule.Name) || forbiddenName(rule.Name) || !allowedHeaders[canonicalHeader(rule.Name)] {
			return fmt.Errorf("%w: protected header", ErrRule)
		}
		value, err := decodeHeaderQueryString(rule.Value)
		if err != nil {
			return err
		}
		headers[rule.Name] = value
		return nil
	case RequestSetQuery:
		// safeSegment permits prototype-family names (underscores and
		// letters), so forbiddenName is applied explicitly to keep rules from
		// introducing __proto__/prototype/constructor into any channel.
		if !safeSegment(rule.Name) || forbiddenName(rule.Name) || !allowedQuery[rule.Name] {
			return fmt.Errorf("%w: protected query name", ErrRule)
		}
		value, err := decodeHeaderQueryString(rule.Value)
		if err != nil {
			return err
		}
		query[rule.Name] = value
		return nil
	default:
		return fmt.Errorf("%w: unsupported request action", ErrRule)
	}
}

// buildAllowedHeaders normalizes the request allowlist into a canonical-key
// set, matching the compiler's validatedHeaders representation. A nil or
// empty allowlist yields an empty set, which denies every set_header rule at
// runtime — exactly the compiler's behavior for an adapter with no declared
// transport headers.
// ruleBodyPaths returns every JSON-pointer channel a request action can
// consume or mutate. Unknown/header/query actions have no body path.
func ruleBodyPaths(rule RequestRule) []string {
	switch rule.Action {
	case RequestSet, RequestRemove, RequestMapEnum, RequestClampNumber:
		return []string{rule.Path}
	case RequestCopy, RequestRename:
		return []string{rule.From, rule.To}
	default:
		return nil
	}
}

// protectedRuntimePath parses the pointer before comparing its decoded first
// token. This mirrors compiler protection even for manually-created rules and
// avoids bypasses through RFC 6901 escaping.
func protectedRuntimePath(path string) bool {
	parts, err := pointerParts(path)
	if err != nil || len(parts) == 0 {
		// Pointer operations will return a specific invalid-pointer error later.
		return false
	}
	switch parts[0] {
	case "model", "messages", "input", "prompt":
		return true
	default:
		return false
	}
}

// clampBound returns a finite float64 bound's stable, shortest exact decimal
// spelling and its rational decimal value. The spelling is also emitted for a
// clamped result, so boundary output is deterministic.
func clampBound(value float64) (*big.Rat, string, bool) {
	if !finite(value) {
		return nil, "", false
	}
	lexeme := strconv.FormatFloat(value, 'g', -1, 64)
	rat, ok := new(big.Rat).SetString(lexeme)
	return rat, lexeme, ok
}

func buildAllowedHeaders(raw []string) map[string]bool {
	out := make(map[string]bool, len(raw))
	for _, h := range raw {
		out[canonicalHeader(h)] = true
	}
	return out
}

// buildAllowedQuery normalizes the query allowlist into an exact-match set,
// matching the compiler's validatedQuery representation.
func buildAllowedQuery(raw []string) map[string]bool {
	out := make(map[string]bool, len(raw))
	for _, q := range raw {
		out[q] = true
	}
	return out
}

// decodeHeaderQueryString parses a JSON string literal without control
// characters. It is shared by set_header and set_query so both channels apply
// the same CTL defense in depth.
func decodeHeaderQueryString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > maxDSLLiteralBytes || !utf8.Valid(raw) {
		return "", fmt.Errorf("%w: invalid header or query value", ErrRule)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%w: invalid header or query value", ErrRule)
	}
	if hasCTL(value) {
		return "", fmt.Errorf("%w: header or query value contains control character", ErrRule)
	}
	return value, nil
}

// decodeEngineJSON parses exactly one JSON value using UseNumber semantics. It
// enforces UTF-8 validity, the 2 MiB input cap, the depth 64 / node 10000
// bounds, duplicate and prototype-family key rejection, and rejects trailing
// content. JSON decoder errors are mapped to generic messages so body content
// never leaks through error text.
func decodeEngineJSON(raw []byte) (any, error) {
	return decodeEngineJSONContext(context.Background(), raw)
}

// decodeEngineJSONContext checks context at every decoded node, keeping
// cancellation latency bounded for the two potentially large decode phases.
func decodeEngineJSONContext(ctx context.Context, raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > maxEngineJSONBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON input")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	nodes := 0
	value, err := decodeJSONValueContext(ctx, dec, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing JSON input")
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("trailing JSON input")
	} else if err != io.EOF {
		return nil, fmt.Errorf("invalid JSON input")
	}
	return value, nil
}

func decodeJSONValue(dec *json.Decoder, depth int, nodes *int) (any, error) {
	return decodeJSONValueContext(context.Background(), dec, depth, nodes)
}

func decodeJSONValueContext(ctx context.Context, dec *json.Decoder, depth int, nodes *int) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > maxDSLJSONDepth {
		return nil, fmt.Errorf("JSON depth limit exceeded")
	}
	*nodes++
	if *nodes > maxDSLJSONNodes {
		return nil, fmt.Errorf("JSON node limit exceeded")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid JSON input")
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			out := make(map[string]any)
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("invalid JSON input")
				}
				name, ok := key.(string)
				if !ok || forbiddenName(name) {
					return nil, fmt.Errorf("unsafe JSON object key")
				}
				if _, exists := out[name]; exists {
					return nil, fmt.Errorf("duplicate JSON object key")
				}
				value, err := decodeJSONValueContext(ctx, dec, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				out[name] = value
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid JSON object")
			}
			return out, nil
		case '[':
			out := []any{}
			for dec.More() {
				value, err := decodeJSONValueContext(ctx, dec, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid JSON array")
			}
			return out, nil
		}
		return nil, fmt.Errorf("invalid JSON input")
	}
	if number, ok := token.(json.Number); ok {
		// Reject exponent/magnitude overflow such as 1e999 early so it can never
		// reach clamp or marshal as a non-finite value.
		value, err := number.Float64()
		if err != nil || !finite(value) {
			return nil, fmt.Errorf("JSON number is not a finite float64")
		}
	}
	return token, nil
}

// forbiddenName reports whether a key or pointer token is a prototype-family
// name. Such names are rejected at every introduction point — JSON object
// keys during decoding, JSON pointer tokens during traversal, and header/query
// rule names — so a compiled adapter can never inject __proto__, prototype,
// or constructor into a request body, an InjectionPlan, or an upstream URL.
func forbiddenName(key string) bool {
	return key == "__proto__" || key == "prototype" || key == "constructor"
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// deepCopyJSON round-trips a value through encode/decode so the destination
// owns an independent tree. This prevents later rule mutations from aliasing
// a copied source.
func deepCopyJSON(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return decodeEngineJSON(raw)
}
