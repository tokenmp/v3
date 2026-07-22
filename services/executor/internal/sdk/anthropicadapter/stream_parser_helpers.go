package anthropicadapter

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func strictAnthropicJSON(raw []byte) (map[string]any, []byte, error) {
	if len(raw) == 0 || len(raw) > sdk.MaxStreamEventDataBytes || !utf8Valid(raw) {
		return nil, nil, errAnthropicStreamProtocol
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	nodes := 0
	value, err := parseAnthropicValue(dec, 1, &nodes)
	if err != nil {
		return nil, nil, errAnthropicStreamProtocol
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, errAnthropicStreamProtocol
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, nil, errAnthropicStreamProtocol
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > sdk.MaxStreamEventDataBytes {
		return nil, nil, errAnthropicStreamProtocol
	}
	return root, canonical, nil
}
func utf8Valid(v []byte) bool { return utf8.Valid(v) }
func parseAnthropicValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > maxAnthropicJSONDepth {
		return nil, errAnthropicStreamProtocol
	}
	*nodes++
	if *nodes > maxAnthropicJSONNodes {
		return nil, errAnthropicStreamProtocol
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			m := map[string]any{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := k.(string)
				if !ok || forbiddenKey(key) {
					return nil, errAnthropicStreamProtocol
				}
				if _, dup := m[key]; dup {
					return nil, errAnthropicStreamProtocol
				}
				v, err := parseAnthropicValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				m[key] = v
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return m, nil
		case '[':
			a := []any{}
			for d.More() {
				v, err := parseAnthropicValue(d, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return a, nil
		}
	case string, bool, nil, json.Number:
		return x, nil
	}
	return nil, errAnthropicStreamProtocol
}
func validOnly(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := m[n]; !ok {
			return false
		}
	}
	return true
}
func int64Value(v any) int64 {
	n, ok := v.(json.Number)
	if !ok {
		return -1
	}
	i, err := n.Int64()
	if err != nil {
		return -1
	}
	return i
}
func validMessageStart(m map[string]any) bool {
	if !validOnly(m, "type", "message") || m["type"] != "message_start" {
		return false
	}
	x, ok := m["message"].(map[string]any)
	if !ok || x["type"] != "message" || !stringField(x, "id") || !stringField(x, "model") {
		return false
	}
	usage, ok := x["usage"].(map[string]any)
	return ok && int64Value(usage["input_tokens"]) >= 0
}
func validBlockStart(m map[string]any, want int64) bool {
	if !validOnly(m, "type", "index", "content_block") || m["type"] != "content_block_start" || int64Value(m["index"]) != want {
		return false
	}
	b, ok := m["content_block"].(map[string]any)
	if !ok {
		return false
	}
	typ, _ := b["type"].(string)
	switch typ {
	case "text":
		return validOnly(b, "type", "text") && stringField(b, "text")
	case "thinking":
		return validOnly(b, "type", "thinking") && stringField(b, "thinking")
	case "tool_use":
		return validOnly(b, "type", "id", "name", "input") && stringField(b, "id") && stringField(b, "name")
	default:
		return false
	}
}
func validBlockDelta(m map[string]any, blocks map[int64]anthropicBlock) bool {
	if !validOnly(m, "type", "index", "delta") || m["type"] != "content_block_delta" {
		return false
	}
	b, ok := blocks[int64Value(m["index"])]
	if !ok {
		return false
	}
	d, ok := m["delta"].(map[string]any)
	if !ok {
		return false
	}
	typ, _ := d["type"].(string)
	switch typ {
	case "text_delta":
		return b.typ == "text" && validOnly(d, "type", "text") && stringField(d, "text")
	case "thinking_delta":
		return b.typ == "thinking" && validOnly(d, "type", "thinking") && stringField(d, "thinking")
	case "input_json_delta":
		return b.typ == "tool_use" && validOnly(d, "type", "partial_json") && stringField(d, "partial_json")
	case "signature_delta":
		return b.typ == "thinking" && !b.signature && validOnly(d, "type", "signature") && stringField(d, "signature")
	default:
		return false
	}
}
func nonEmptyDelta(d map[string]any) bool {
	for _, n := range []string{"text", "thinking", "partial_json"} {
		if v, ok := d[n].(string); ok && v != "" {
			return true
		}
	}
	return false
}
func validMessageDelta(m map[string]any) bool {
	if !validOnly(m, "type", "delta", "usage") || m["type"] != "message_delta" {
		return false
	}
	d, ok := m["delta"].(map[string]any)
	if !ok || !validOnly(d, "type", "stop_reason", "stop_sequence") || d["type"] != "message_delta" || !validFinishString(d["stop_reason"]) {
		return false
	}
	if stop := d["stop_sequence"]; stop != nil {
		if _, ok := stop.(string); !ok {
			return false
		}
	}
	u, ok := m["usage"].(map[string]any)
	return ok && validOnly(u, "output_tokens") && int64Value(u["output_tokens"]) >= 0
}
func validNativeError(m map[string]any) bool {
	if !validOnly(m, "type", "error") || m["type"] != "error" {
		return false
	}
	e, ok := m["error"].(map[string]any)
	if !ok {
		return false
	}
	typ, _ := e["type"].(string)
	return typ != ""
}
func classifyNativeError(m map[string]any) *sdk.ClassifiedError {
	e := m["error"].(map[string]any)
	typ, _ := e["type"].(string)
	switch typ {
	case "overloaded_error":
		return sdk.NewClassifiedError(sdk.ErrUnavailable, 0, "", "", typ)
	case "rate_limit_error":
		return sdk.NewClassifiedError(sdk.ErrRateLimited, 0, "", "", typ)
	default:
		return sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", typ)
	}
}
func validFinishString(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	return s == "end_turn" || s == "max_tokens" || s == "stop_sequence" || s == "tool_use" || s == "refusal"
}
func validFinish(v string) bool                   { return validFinishString(v) }
func stringField(m map[string]any, n string) bool { _, ok := m[n].(string); return ok }
func usageFrom(m map[string]any) streaming.Usage {
	return streaming.Usage{CompletionTokens: int64Value(m["output_tokens"])}
}
func mergeAnthropicUsage(cur, in streaming.Usage) streaming.Usage {
	if in.PromptTokens > cur.PromptTokens {
		cur.PromptTokens = in.PromptTokens
	}
	if in.CompletionTokens > cur.CompletionTokens {
		cur.CompletionTokens = in.CompletionTokens
	}
	if in.TotalTokens > cur.TotalTokens {
		cur.TotalTokens = in.TotalTokens
	}
	return cur
}
