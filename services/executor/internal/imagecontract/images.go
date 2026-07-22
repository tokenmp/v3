// Package imagecontract owns the provider-neutral semantic boundary for the
// legacy OpenAI Images request and success response shapes. Parsing, provider
// calls, routing and HTTP rendering remain outside this package.
package imagecontract

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	MaxPromptBytes           = 1 << 20
	MaxUserBytes             = 512
	MaxWireResponseBytes     = 16 << 20
	MaxURLBytes              = 16 << 10
	MaxRevisedPromptBytes    = 64 << 10
	MaxExtensionValueBytes   = 64 << 10
	MaxDecodedDataBytes      = 10 << 20
	MaxDecodedAggregateBytes = 12 << 20
)

// ValidateRequest validates an already strictly parsed JSON object and returns
// the observable response-format default. It intentionally does not trim
// prompt/model: the SDK's legacy Images contract treats any nonempty string as
// supplied, while routing separately owns selector grammar.
func ValidateRequest(r map[string]any) (string, bool) {
	if !onlyFields(r, fieldSet("model", "prompt", "n", "size", "quality", "response_format", "style", "user")) {
		return "", false
	}
	model, mok := r["model"].(string)
	prompt, pok := r["prompt"].(string)
	if !mok || model == "" || !pok || prompt == "" || len(prompt) > MaxPromptBytes ||
		!optionalIntegerRange(r, "n", 1, 10) || !optionalEnum(r, "size", "256x256", "512x512", "1024x1024", "1792x1024", "1024x1792") ||
		!optionalEnum(r, "quality", "standard", "hd") || !optionalEnum(r, "response_format", "url", "b64_json") || !optionalEnum(r, "style", "vivid", "natural") {
		return "", false
	}
	if user, exists := r["user"]; exists {
		s, ok := user.(string)
		if !ok || len(s) > MaxUserBytes || hasCTL(s) {
			return "", false
		}
	}
	if format, exists := r["response_format"].(string); exists {
		return format, true
	}
	return "url", true
}

// ValidateResponse validates an already strictly parsed success response. It
// accepts bounded provider extensions but requires every data item to use the
// requested format consistently.
func ValidateResponse(r map[string]any, wanted string) bool {
	if len(r) > 32 || !boundedExtensions(r, fieldSet("created", "data", "usage")) || !nonnegativeInteger(r["created"]) {
		return false
	}
	data, ok := r["data"].([]any)
	if !ok || len(data) < 1 || len(data) > 10 {
		return false
	}
	if usage, exists := r["usage"]; exists && !validUsage(usage) {
		return false
	}
	kind := ""
	total := int64(0)
	for _, value := range data {
		item, ok := value.(map[string]any)
		if !ok || !onlyFields(item, fieldSet("url", "b64_json", "revised_prompt")) {
			return false
		}
		u, hasURL := item["url"].(string)
		b, hasB64 := item["b64_json"].(string)
		if hasURL == hasB64 || (hasURL && !validURL(u)) {
			return false
		}
		thisKind := "url"
		if hasB64 {
			thisKind = "b64_json"
			n, ok := decodedBytes(b, MaxDecodedDataBytes)
			if !ok || total > MaxDecodedAggregateBytes-n {
				return false
			}
			total += n
		}
		if kind == "" {
			kind = thisKind
		} else if kind != thisKind {
			return false
		}
		if revised, exists := item["revised_prompt"]; exists {
			s, ok := revised.(string)
			if !ok || len(s) > MaxRevisedPromptBytes || hasCTL(s) {
				return false
			}
		}
	}
	return wanted == "" || wanted == kind
}

func fieldSet(names ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(names))
	for _, n := range names {
		s[n] = struct{}{}
	}
	return s
}
func onlyFields(o map[string]any, allowed map[string]struct{}) bool {
	for k := range o {
		if _, ok := allowed[k]; !ok {
			return false
		}
	}
	return true
}
func optionalEnum(o map[string]any, name string, values ...string) bool {
	v, ok := o[name]
	if !ok {
		return true
	}
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, want := range values {
		if s == want {
			return true
		}
	}
	return false
}
func optionalIntegerRange(o map[string]any, name string, low, high int64) bool {
	v, ok := o[name]
	if !ok {
		return true
	}
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i >= low && i <= high
}
func hasCTL(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
func nonnegativeInteger(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && i >= 0
}
func validURL(s string) bool {
	if len(s) == 0 || len(s) > MaxURLBytes {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}
func decodedBytes(s string, max int64) (int64, bool) {
	n, err := io.CopyN(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(s)), max+1)
	return n, (err == nil || err == io.EOF) && n <= max
}
func validUsage(v any) bool {
	o, ok := v.(map[string]any)
	if !ok || len(o) > 16 || !boundedExtensions(o, fieldSet("input_tokens", "output_tokens", "total_tokens", "input_tokens_details", "output_tokens_details")) {
		return false
	}
	for _, k := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if x, exists := o[k]; exists && !nonnegativeInteger(x) {
			return false
		}
	}
	for _, k := range []string{"input_tokens_details", "output_tokens_details"} {
		if x, exists := o[k]; exists {
			d, ok := x.(map[string]any)
			if !ok || len(d) > 8 || !boundedExtensions(d, fieldSet("image_tokens", "text_tokens")) {
				return false
			}
			for name, val := range d {
				if (name == "image_tokens" || name == "text_tokens") && !nonnegativeInteger(val) {
					return false
				}
			}
		}
	}
	return true
}
func boundedExtensions(o map[string]any, known map[string]struct{}) bool {
	for key, value := range o {
		if _, ok := known[key]; !ok && jsonValueSize(value, MaxExtensionValueBytes) > MaxExtensionValueBytes {
			return false
		}
	}
	return true
}
func jsonValueSize(value any, limit int) int {
	var size func(any, int) int
	size = func(v any, used int) int {
		if used > limit {
			return used
		}
		switch x := v.(type) {
		case nil:
			return used + 4
		case bool:
			if x {
				return used + 4
			}
			return used + 5
		case json.Number:
			return used + len(x)
		case string:
			used += 2
			for _, r := range x {
				if r < 0x20 || r == '"' || r == '\\' || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029 {
					used += 6
				} else {
					used += utf8.RuneLen(r)
				}
				if used > limit {
					return used
				}
			}
			return used
		case []any:
			used++
			for i, item := range x {
				if i > 0 {
					used++
				}
				used = size(item, used)
				if used > limit {
					return used
				}
			}
			return used + 1
		case map[string]any:
			used++
			for key, item := range x {
				if used > limit {
					return used
				}
				if used > 1 {
					used++
				}
				used = size(key, used) + 1
				used = size(item, used)
			}
			return used + 1
		default:
			return limit + 1
		}
	}
	return size(value, 0)
}
