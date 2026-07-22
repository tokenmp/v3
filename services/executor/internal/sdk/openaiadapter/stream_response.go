package openaiadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// The parser accepts provider extensions but makes their resource use finite.
const (
	maxChunkBytes     = sdk.MaxStreamEventDataBytes
	maxChunkJSONDepth = 64
	maxChunkJSONNodes = 10000
	maxStringBytes    = 64 << 10
	maxArrayItems     = 256
)

var errChunkProtocol = errors.New("openaiadapter: invalid stream chunk")

// parseChunk strictly validates one OpenAI SSE JSON payload and returns an
// owned canonical JSON representation. Errors intentionally carry no provider
// data, including malformed payload fragments.
func parseChunk(raw []byte) (streaming.Event, json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxChunkBytes || !utf8.Valid(raw) {
		return streaming.Event{}, nil, errChunkProtocol
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	nodes := 0
	if err := walkJSON(dec, 0, &nodes); err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	if _, err := dec.Token(); err != io.EOF {
		return streaming.Event{}, nil, errChunkProtocol
	}
	var value any
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	root, ok := value.(map[string]any)
	if !ok {
		return streaming.Event{}, nil, errChunkProtocol
	}
	if _, inBandError := root["error"]; inBandError {
		// An in-band error is terminal upstream metadata only. Its provider body
		// (which can contain arbitrary text) must not reach a renderer or error.
		return streaming.Event{Kind: streaming.EventNativeError, EventType: "chat.completion.chunk"}, nil, nil
	}
	if err := validateRoot(root); err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	ev, err := classifyRoot(root)
	if err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return streaming.Event{}, nil, errChunkProtocol
	}
	return ev, ownedRaw(canonical), nil
}

func ownedRaw(b []byte) json.RawMessage { return append(json.RawMessage(nil), b...) }

func walkJSON(d *json.Decoder, depth int, nodes *int) error {
	if depth > maxChunkJSONDepth {
		return errChunkProtocol
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	*nodes++
	if *nodes > maxChunkJSONNodes {
		return errChunkProtocol
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			seen := map[string]struct{}{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				ks, ok := key.(string)
				if !ok || len(ks) > maxStringBytes || forbiddenKey(ks) {
					return errChunkProtocol
				}
				if _, exists := seen[ks]; exists {
					return errChunkProtocol
				}
				seen[ks] = struct{}{}
				if err := walkJSON(d, depth+1, nodes); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		case '[':
			count := 0
			for d.More() {
				count++
				if count > maxArrayItems {
					return errChunkProtocol
				}
				if err := walkJSON(d, depth+1, nodes); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		default:
			return errChunkProtocol
		}
	case string:
		if len(x) > maxStringBytes {
			return errChunkProtocol
		}
	}
	return nil
}
func forbiddenKey(k string) bool { return k == "__proto__" || k == "prototype" || k == "constructor" }

func validateRoot(r map[string]any) error {
	if !nonEmptyString(r["id"]) || !exactString(r["object"], "chat.completion.chunk") || !nonNegativeInt(r["created"]) || !nonEmptyString(r["model"]) {
		return errChunkProtocol
	}
	choices, ok := r["choices"].([]any)
	if !ok {
		return errChunkProtocol
	}
	usage, usagePresent, err := parseUsage(r["usage"])
	if err != nil {
		return errChunkProtocol
	}
	_ = usage
	if len(choices) == 0 {
		if !usagePresent {
			return errChunkProtocol
		}
		return nil
	}
	if len(choices) != 1 {
		return errChunkProtocol
	}
	choice, ok := choices[0].(map[string]any)
	if !ok || !exactInt(choice["index"], 0) {
		return errChunkProtocol
	}
	if delta, exists := choice["delta"]; !exists {
		return errChunkProtocol
	} else if err := validateDelta(delta); err != nil {
		return err
	}
	finish, exists := choice["finish_reason"]
	if !exists || (finish != nil && !supportedFinish(finish)) {
		return errChunkProtocol
	}
	if logprobs, exists := choice["logprobs"]; exists && logprobs != nil {
		if err := boundedStructure(logprobs, 0); err != nil {
			return err
		}
	}
	return nil
}
func validateDelta(v any) error {
	d, ok := v.(map[string]any)
	if !ok {
		return errChunkProtocol
	}
	if role, ok := d["role"]; ok && !oneOfString(role, "system", "user", "assistant", "tool", "function") {
		return errChunkProtocol
	}
	for _, key := range []string{"content", "reasoning_content", "refusal"} {
		if x, ok := d[key]; ok && x != nil && !boundedString(x) {
			return errChunkProtocol
		}
	}
	if fc, ok := d["function_call"]; ok && fc != nil {
		if err := validateFunction(fc); err != nil {
			return err
		}
	}
	if tc, ok := d["tool_calls"]; ok && tc != nil {
		a, ok := tc.([]any)
		if !ok || len(a) > maxArrayItems {
			return errChunkProtocol
		}
		for _, v := range a {
			m, ok := v.(map[string]any)
			if !ok || !nonNegativeInt(m["index"]) {
				return errChunkProtocol
			}
			for _, k := range []string{"id", "type"} {
				if x, yes := m[k]; yes && !boundedString(x) {
					return errChunkProtocol
				}
			}
			if f, yes := m["function"]; yes && f != nil {
				if err := validateFunction(f); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func validateFunction(v any) error {
	m, ok := v.(map[string]any)
	if !ok {
		return errChunkProtocol
	}
	for _, k := range []string{"name", "arguments"} {
		if x, yes := m[k]; yes && !boundedString(x) {
			return errChunkProtocol
		}
	}
	return nil
}
func parseUsage(v any) (streaming.Usage, bool, error) {
	if v == nil {
		return streaming.Usage{}, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return streaming.Usage{}, false, errChunkProtocol
	}
	u := streaming.Usage{}
	for _, x := range []struct {
		k string
		p *int64
	}{{"prompt_tokens", &u.PromptTokens}, {"completion_tokens", &u.CompletionTokens}, {"total_tokens", &u.TotalTokens}} {
		value, yes := m[x.k]
		if !yes {
			return u, false, errChunkProtocol
		}
		n, ok := integer(value)
		if !ok || n < 0 || n > streaming.MaxTotalHardCap {
			return u, false, errChunkProtocol
		}
		*x.p = n
	}
	return u, true, nil
}
func classifyRoot(r map[string]any) (streaming.Event, error) {
	usage, hasUsage, err := parseUsage(r["usage"])
	if err != nil {
		return streaming.Event{}, err
	}
	choices := r["choices"].([]any)
	if len(choices) == 0 {
		return streaming.Event{Kind: streaming.EventUsage, EventType: "chat.completion.chunk", Usage: &usage}, nil
	}
	c := choices[0].(map[string]any)
	d := c["delta"].(map[string]any)
	semantic := false
	for _, k := range []string{"content", "reasoning_content", "refusal"} {
		if x, ok := d[k]; ok && x != nil && x.(string) != "" {
			semantic = true
		}
	}
	if calls, ok := d["tool_calls"].([]any); ok {
		for _, x := range calls {
			m := x.(map[string]any)
			for _, k := range []string{"id", "type"} {
				if s, ok := m[k].(string); ok && s != "" {
					semantic = true
				}
			}
			if f, ok := m["function"].(map[string]any); ok {
				for _, k := range []string{"name", "arguments"} {
					if s, ok := f[k].(string); ok && s != "" {
						semantic = true
					}
				}
			}
		}
	}
	if f, ok := d["function_call"].(map[string]any); ok {
		for _, k := range []string{"name", "arguments"} {
			if s, ok := f[k].(string); ok && s != "" {
				semantic = true
			}
		}
	}
	finish := ""
	if x := c["finish_reason"]; x != nil {
		finish = x.(string)
	}
	if semantic && finish != "" {
		return streaming.Event{}, errChunkProtocol
	}
	if semantic {
		return streaming.Event{Kind: streaming.EventSemantic, EventType: "chat.completion.chunk"}, nil
	}
	if finish != "" {
		return streaming.Event{Kind: streaming.EventFinish, EventType: "chat.completion.chunk", FinishReason: finish}, nil
	}
	if hasUsage {
		return streaming.Event{Kind: streaming.EventUsage, EventType: "chat.completion.chunk", Usage: &usage}, nil
	}
	return streaming.Event{Kind: streaming.EventLifecycle, EventType: "chat.completion.chunk"}, nil
}
func boundedStructure(v any, depth int) error {
	if depth > maxChunkJSONDepth {
		return errChunkProtocol
	}
	switch x := v.(type) {
	case string:
		if len(x) > maxStringBytes {
			return errChunkProtocol
		}
	case []any:
		if len(x) > maxArrayItems {
			return errChunkProtocol
		}
		for _, e := range x {
			if err := boundedStructure(e, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if len(x) > maxArrayItems {
			return errChunkProtocol
		}
		for _, e := range x {
			if err := boundedStructure(e, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
func integer(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}
func nonNegativeInt(v any) bool       { n, ok := integer(v); return ok && n >= 0 }
func exactInt(v any, want int64) bool { n, ok := integer(v); return ok && n == want }
func boundedString(v any) bool        { s, ok := v.(string); return ok && len(s) <= maxStringBytes }
func nonEmptyString(v any) bool {
	s, ok := v.(string)
	return ok && s != "" && len(s) <= maxStringBytes
}
func exactString(v any, want string) bool { s, ok := v.(string); return ok && s == want }
func oneOfString(v any, values ...string) bool {
	s, ok := v.(string)
	if !ok || len(s) > maxStringBytes {
		return false
	}
	for _, x := range values {
		if s == x {
			return true
		}
	}
	return false
}
func supportedFinish(v any) bool {
	return oneOfString(v, "stop", "length", "tool_calls", "content_filter", "function_call")
}
