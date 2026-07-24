package protocolconvert

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
)

// ── Tool name sanitization constants ────────────────────────────────────────

const (
	// maxToolNameLen is the maximum allowed length for a sanitized tool name.
	maxToolNameLen = 64
	// sanitizedPrefix is the default prefix for tool names that require
	// sanitization. It ensures the name starts with an alpha character and
	// contains only [A-Za-z0-9_-].
	sanitizedPrefix = "tool_"
)

// ── ToolNameMap ─────────────────────────────────────────────────────────────

// ToolNameMap is an alias for map[string]string. It maps sanitized (safe) tool
// names back to their original names. A nil or empty map is a no-op: all tool
// names are already valid.
type ToolNameMap = map[string]string

// ── SanitizeToolNames ───────────────────────────────────────────────────────

// SanitizeToolNames traverses the relevant tools, tool_choice, and messages
// structures in an Anthropic→OpenAI request body and replaces tool names that
// do not strictly match [A-Za-z][A-Za-z0-9_-]{0,63} with sanitized
// alternatives. It returns the sanitized body and a nameMap for restoring
// original names in the response.
//
// Sanitization rules:
//   - Valid names (matching [A-Za-z][A-Za-z0-9_-]{0,63}) are preserved as-is.
//   - Invalid names are converted character-by-character:
//   - Valid chars [A-Za-z0-9_-] are kept.
//   - Invalid chars are replaced with '_'.
//   - If the result does not start with a letter, "tool_" is prepended.
//   - Truncated to 64 characters.
//   - Deterministic collision resolution: "_2", "_3", etc. are appended.
//   - Empty name → "tool_" + index.
//
// All three locations—tools, tool_choice, and messages—are always traversed
// so that invalid references are sanitized even when tools are absent or all
// tool definitions are already valid. The returned map only contains entries
// for names that were actually remapped.
//
// The returned body is a new JSON object; the original is not modified.
// If all tool names are already valid, the original body bytes are returned
// and the nameMap is nil (zero overhead).
func SanitizeToolNames(body []byte) ([]byte, map[string]string, error) {
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, nil, err
	}

	// ── Collect all tool name references from every location ──────────────
	type toolEntry struct {
		name  string
		index int // index in tools array for empty-name generation; -1 otherwise
	}
	var needsSanitize []toolEntry
	usedNames := make(map[string]bool)
	// Deduplicate invalid names so we only generate one sanitized form per
	// unique original name, regardless of how many locations reference it.
	seenInvalid := make(map[string]bool)

	// 1. tools[].name
	if tools, ok := root["tools"].([]any); ok {
		for i, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
			}
			name := stringVal(tool["name"])
			if name == "" || !isValidToolName(name) {
				if !seenInvalid[name] {
					seenInvalid[name] = true
					needsSanitize = append(needsSanitize, toolEntry{name, i})
				}
			} else {
				usedNames[name] = true
			}
		}
	}

	// 2. tool_choice (Anthropic shape: {"type": "tool", "name": "..."})
	if tc, ok := root["tool_choice"].(map[string]any); ok {
		if stringVal(tc["type"]) == "tool" {
			name := stringVal(tc["name"])
			if name != "" {
				if !isValidToolName(name) {
					if !seenInvalid[name] {
						seenInvalid[name] = true
						needsSanitize = append(needsSanitize, toolEntry{name, -1})
					}
				} else {
					usedNames[name] = true
				}
			}
		}
	}

	// 3. messages: assistant content blocks with type=tool_use
	if messages, ok := root["messages"].([]any); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if stringVal(msg["role"]) != "assistant" {
				continue
			}
			content, ok := msg["content"].([]any)
			if !ok {
				continue
			}
			for _, block := range content {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if stringVal(b["type"]) == "tool_use" {
					name := stringVal(b["name"])
					if name != "" {
						if !isValidToolName(name) {
							if !seenInvalid[name] {
								seenInvalid[name] = true
								needsSanitize = append(needsSanitize, toolEntry{name, -1})
							}
						} else {
							usedNames[name] = true
						}
					}
				}
			}
		}
	}

	if len(needsSanitize) == 0 {
		return body, nil, nil
	}

	// ── Build name map and resolve collisions ────────────────────────────
	nameMap := map[string]string{}
	for _, entry := range needsSanitize {
		index := entry.index
		if index < 0 {
			index = 0
		}
		sanitized := sanitizeSingleToolName(entry.name, index)
		sanitized = resolveCollision(sanitized, usedNames)
		usedNames[sanitized] = true
		nameMap[sanitized] = entry.name
	}

	// Apply the renaming to the parsed root.
	applyToolNameMap(root, nameMap)

	result, err := json.Marshal(root)
	if err != nil {
		return nil, nil, err
	}
	return result, nameMap, nil
}

// sanitizeSingleToolName converts a single tool name to a valid form.
func sanitizeSingleToolName(name string, index int) string {
	if name == "" {
		return fmt.Sprintf("%s%d", sanitizedPrefix, index)
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	result := b.String()
	// Must start with a letter.
	if len(result) == 0 || !isAlpha(rune(result[0])) {
		result = sanitizedPrefix + result
	}
	// Truncate to maxToolNameLen.
	if len(result) > maxToolNameLen {
		result = result[:maxToolNameLen]
	}
	return result
}

// resolveCollision appends _2, _3, etc. until the name is unique.
// It dynamically adjusts the base-name length so that the full candidate
// never exceeds maxToolNameLen and the entire "_N" suffix is preserved
// (no silent truncation that could collapse e.g. _100 into _10).
func resolveCollision(name string, used map[string]bool) string {
	if !used[name] {
		return name
	}
	for i := 2; ; i++ {
		suffix := fmt.Sprintf("_%d", i)
		baseLen := maxToolNameLen - len(suffix)
		if baseLen < 1 {
			baseLen = 1
		}
		candidate := name[:min(len(name), baseLen)] + suffix
		if !used[candidate] {
			return candidate
		}
	}
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isValidToolName reports whether a tool name strictly matches the OpenAI
// requirement: [A-Za-z][A-Za-z0-9_-]{0,63}.
func isValidToolName(name string) bool {
	if len(name) == 0 || len(name) > maxToolNameLen {
		return false
	}
	if !utf8.ValidString(name) {
		return false
	}
	r, size := utf8.DecodeRuneInString(name)
	if !isAlpha(r) {
		return false
	}
	for _, r := range name[size:] {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// applyToolNameMap renames tools and references in the parsed JSON root.
func applyToolNameMap(root map[string]any, nameMap map[string]string) {
	if len(nameMap) == 0 {
		return
	}
	// Build reverse map: original → sanitized.
	revMap := make(map[string]string, len(nameMap))
	for safe, orig := range nameMap {
		revMap[orig] = safe
	}

	// tools[].name
	if tools, ok := root["tools"].([]any); ok {
		for _, t := range tools {
			tool, ok := t.(map[string]any)
			if !ok {
				continue
			}
			name := stringVal(tool["name"])
			if safe, ok := revMap[name]; ok {
				tool["name"] = safe
			}
		}
	}

	// tool_choice: if type=tool, rename function.name
	if tc, ok := root["tool_choice"].(map[string]any); ok {
		if stringVal(tc["type"]) == "tool" {
			if fn, ok := tc["name"]; ok {
				name := stringVal(fn)
				if safe, ok := revMap[name]; ok {
					tc["name"] = safe
				}
			}
		}
	}

	// messages: assistant tool_use blocks have "name" field
	if messages, ok := root["messages"].([]any); ok {
		for _, m := range messages {
			msg, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if stringVal(msg["role"]) != "assistant" {
				continue
			}
			content, ok := msg["content"].([]any)
			if !ok {
				continue
			}
			for _, block := range content {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if stringVal(b["type"]) == "tool_use" {
					name := stringVal(b["name"])
					if safe, ok := revMap[name]; ok {
						b["name"] = safe
					}
				}
			}
		}
	}
}

// ── RestoreToolNamesResponse ────────────────────────────────────────────────

// RestoreToolNamesResponse restores original tool names in a non-streaming
// response body. It auto-detects the response shape and handles:
//
//   - OpenAI: choices[].message.tool_calls[].function.name
//   - Anthropic: message.content[].type=tool_use.name (and top-level content[])
//   - Responses: output[].name where type is function_call or custom_tool_call
//
// The nameMap maps sanitized→original tool names. If nameMap is nil or empty,
// it returns body unchanged (zero overhead).
func RestoreToolNamesResponse(body []byte, nameMap map[string]string) ([]byte, error) {
	if len(nameMap) == 0 {
		return body, nil
	}
	root, err := parseStrictJSON(body)
	if err != nil {
		return nil, ErrInvalidResponse
	}
	// OpenAI: choices[].message.tool_calls[].function.name
	if choices, ok := root["choices"].([]any); ok {
		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if msg, ok := choice["message"].(map[string]any); ok {
				restoreToolNamesInMessage(msg, nameMap)
			}
		}
	}
	// Anthropic: message.content[].type=tool_use.name (top-level message object)
	if msg, ok := root["message"].(map[string]any); ok {
		restoreToolNamesInMessage(msg, nameMap)
	}
	// Also check top-level content[] (direct Anthropic response without wrapper)
	if _, ok := root["content"].([]any); ok {
		restoreContentToolUse(root, nameMap)
	}
	// Responses: output[].name for function_call / custom_tool_call items.
	if output, ok := root["output"].([]any); ok {
		for _, item := range output {
			it, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ := stringVal(it["type"])
			if typ != "function_call" && typ != "custom_tool_call" {
				continue
			}
			name := stringVal(it["name"])
			if orig, ok := nameMap[name]; ok {
				it["name"] = orig
			}
		}
	}
	return json.Marshal(root)
}

// restoreToolNamesInMessage restores tool names in a message-like map.
// It handles both OpenAI tool_calls[].function.name and Anthropic
// content[].type=tool_use.name in a single pass.
func restoreToolNamesInMessage(message map[string]any, nameMap map[string]string) {
	// OpenAI: tool_calls[].function.name
	if tcs, ok := message["tool_calls"].([]any); ok {
		for _, tc := range tcs {
			call, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			if fn, ok := call["function"].(map[string]any); ok {
				name := stringVal(fn["name"])
				if orig, ok := nameMap[name]; ok {
					fn["name"] = orig
				}
			}
		}
	}
	// Anthropic: content[].type=tool_use.name
	restoreContentToolUse(message, nameMap)
}

// restoreContentToolUse restores tool names in content[] blocks where
// type=tool_use. This covers Anthropic response and stream shapes.
func restoreContentToolUse(container map[string]any, nameMap map[string]string) {
	content, ok := container["content"].([]any)
	if !ok {
		return
	}
	for _, block := range content {
		b, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if stringVal(b["type"]) == "tool_use" {
			name := stringVal(b["name"])
			if orig, ok := nameMap[name]; ok {
				b["name"] = orig
			}
		}
	}
}

// ── RestoreToolNamesStreamChunk ─────────────────────────────────────────────

// RestoreToolNamesStreamChunk restores original tool names in a single stream
// chunk. It auto-detects the chunk format and handles:
//
//   - OpenAI: choices[].delta.tool_calls[].function.name
//   - Anthropic: content_block_start[type=tool_use].name
//   - Responses: output_item.added item.name (function_call / custom_tool_call)
//
// No protocol selector is needed. The nameMap maps sanitized→original tool
// names. If nameMap is nil or empty, it returns chunk unchanged (zero overhead).
func RestoreToolNamesStreamChunk(chunk []byte, nameMap map[string]string) ([]byte, error) {
	if len(nameMap) == 0 {
		return chunk, nil
	}
	if len(chunk) == 0 {
		return chunk, nil
	}
	root, err := parseStrictJSON(chunk)
	if err != nil {
		return nil, ErrInvalidStreamChunk
	}
	// OpenAI: choices[].delta.tool_calls[].function.name
	if choices, ok := root["choices"].([]any); ok {
		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			if delta, ok := choice["delta"].(map[string]any); ok {
				restoreToolNamesInMessage(delta, nameMap)
			}
		}
		return json.Marshal(root)
	}
	// Anthropic: content_block_start[type=tool_use].name
	if stringVal(root["type"]) == "content_block_start" {
		if block, ok := root["content_block"].(map[string]any); ok {
			if stringVal(block["type"]) == "tool_use" {
				name := stringVal(block["name"])
				if orig, ok := nameMap[name]; ok {
					block["name"] = orig
				}
			}
		}
	}
	// Responses: output_item.added with item.name (function_call / custom_tool_call)
	if stringVal(root["type"]) == "response.output_item.added" {
		if item, ok := root["item"].(map[string]any); ok {
			typ := stringVal(item["type"])
			if typ == "function_call" || typ == "custom_tool_call" {
				name := stringVal(item["name"])
				if orig, ok := nameMap[name]; ok {
					item["name"] = orig
				}
			}
		}
	}
	return json.Marshal(root)
}

// ── ConvertRequestWithToolMap ───────────────────────────────────────────────

// ConvertRequestWithToolMap performs the same conversion as ConvertRequest but
// first sanitizes tool names for Anthropic→OpenAI and Anthropic→Responses
// conversions. It returns the converted body and a nameMap for response
// restoration.
//
// For directions other than Anthropic→OpenAI/Responses, it delegates directly
// to ConvertRequest and returns a nil map (zero overhead).
//
// The nameMap is per-request and must be scoped to the current attempt. It
// should be threaded through to RestoreToolNamesResponse /
// RestoreToolNamesStreamChunk for the corresponding response/stream.
func ConvertRequestWithToolMap(reqBody []byte, fromProtocol, toProtocol adapter.Protocol) ([]byte, map[string]string, error) {
	if fromProtocol == adapter.ProtocolAnthropic && (toProtocol == adapter.ProtocolOpenAIChat || toProtocol == adapter.ProtocolOpenAIResponses) {
		sanitized, nameMap, err := SanitizeToolNames(reqBody)
		if err != nil {
			return nil, nil, ErrInvalidRequest
		}
		converted, err := ConvertRequest(sanitized, fromProtocol, toProtocol)
		if err != nil {
			return nil, nil, err
		}
		return converted, nameMap, nil
	}
	converted, err := ConvertRequest(reqBody, fromProtocol, toProtocol)
	return converted, nil, err
}
