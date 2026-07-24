// This file implements the streaming (SSE) conversions between OpenAI
// Responses and (OpenAI Chat | Anthropic Messages).
//
// Chat↔Responses is implemented directly with two state machines. The
// Anthropic↔Responses variants are composed through Chat: each Anthropic
// event is first converted to Chat chunk(s) by the existing
// Chat↔Anthropic machine (run on a dedicated sub-state), and those Chat
// chunks are then fed to the Chat↔Responses machine (another sub-state).
// This reuses the exhaustively tested Chat↔Anthropic streaming logic and
// keeps a single source of truth for that leg.
//
// The converter emits a faithful Responses SSE event sequence:
// response.created → response.in_progress → (output_item/content_part
// lifecycle + semantic deltas) → response.completed. Custom tools are
// wrapped/unwrapped via the same responsesCustomTool* helpers used for
// non-streaming, so the mapping is fully reversible without per-request
// state.

package protocolconvert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ── Chat → Responses (direct) ───────────────────────────────────────────────

func convertStreamChunkOpenAIToResponses(raw []byte, state *StreamState) ([][]byte, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("[DONE]")) {
		return finalizeOpenAIToResponses(state), nil
	}
	root, err := parseStrictJSON(raw)
	if err != nil {
		return nil, ErrInvalidStreamChunk
	}
	if !isString(root["id"]) || !isString(root["model"]) {
		return nil, ErrInvalidStreamChunk
	}

	// Accumulate usage from any chunk carrying it.
	if usage, ok := root["usage"].(map[string]any); ok {
		if pt, ok := usage["prompt_tokens"]; ok {
			state.RespUsage.PromptTokens = numToInt64(pt)
		}
		if ct, ok := usage["completion_tokens"]; ok {
			state.RespUsage.CompletionTokens = numToInt64(ct)
		}
	}

	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		// Usage-only terminal chunk (include_usage). If the message has
		// started, synthesize the Responses terminal now.
		if _, ok := root["usage"].(map[string]any); ok && state.RespStarted && !state.RespDone {
			return finalizeOpenAIToResponses(state), nil
		}
		return nil, nil
	}

	var results [][]byte
	if !state.RespStarted {
		state.RespStarted = true
		state.RespResponseID = responsesResponseID(stringVal(root["id"]))
		state.RespModel = stringVal(root["model"])
		state.RespSequence = 0
		state.RespOutputIndex = 0
		state.RespToolItemByID = map[string]string{}
		results = append(results,
			respEvent("response.created", state, map[string]any{
				"response": map[string]any{
					"id": state.RespResponseID, "object": "response",
					"status": "in_progress", "model": state.RespModel, "output": []any{},
				},
			}),
			respEvent("response.in_progress", state, map[string]any{
				"response": map[string]any{
					"id": state.RespResponseID, "object": "response",
					"status": "in_progress", "model": state.RespModel,
				},
			}),
		)
	}

	choice, ok := choices[0].(map[string]any)
	if !ok {
		return results, nil
	}
	delta, _ := choice["delta"].(map[string]any)
	finishReason := ""
	if fr, ok := choice["finish_reason"]; ok && fr != nil {
		finishReason = stringVal(fr)
	}

	// Role-only announcement: nothing to emit (response already started).
	if _, ok := delta["role"]; ok && len(delta) == 1 {
		return results, nil
	}

	// Reasoning must precede text in the Responses output order (mirrors
	// Anthropic thinking-before-text). Chat streams reasoning_content before
	// content, so this is the common order.
	if reasoning, ok := delta["reasoning_content"]; ok && isString(reasoning) && stringVal(reasoning) != "" {
		results = append(results, respReasoningDelta(state, stringVal(reasoning))...)
	}

	// Text content.
	if content, ok := delta["content"]; ok && isString(content) {
		text := stringVal(content)
		results = append(results, respTextDelta(state, text)...)
	}

	// Tool calls.
	if tcs, ok := delta["tool_calls"].([]any); ok {
		results = append(results, respToolCallDeltas(state, tcs)...)
	}

	if finishReason != "" {
		state.RespStatus = responsesStatusFromFinishReason(finishReason)
		results = append(results, finalizeOpenAIToResponses(state)...)
	}

	return results, nil
}

// respTextDelta emits the message output_item/content_part lifecycle (once)
// and the output_text.delta. It closes an open reasoning item first.
func respTextDelta(state *StreamState, text string) [][]byte {
	var out [][]byte
	if state.RespReasoningOpen {
		out = append(out, respReasoningClose(state)...)
	}
	if !state.RespTextBlockOpen {
		state.RespMessageItemID = "msg_converted"
		out = append(out,
			respEvent("response.output_item.added", state, map[string]any{
				"output_index": state.RespOutputIndex,
				"item": map[string]any{
					"id": state.RespMessageItemID, "type": "message",
					"role": "assistant", "status": "in_progress", "content": []any{},
				},
			}),
			respEvent("response.content_part.added", state, map[string]any{
				"output_index":  state.RespOutputIndex,
				"content_index": 0,
				"part":          map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
			}),
		)
		state.RespTextBlockOpen = true
		state.RespContentIndex = 0
	}
	state.RespTextAccum += text
	if text != "" {
		out = append(out, respEvent("response.output_text.delta", state, map[string]any{
			"output_index":  state.RespOutputIndex,
			"content_index": state.RespContentIndex,
			"delta":         text,
		}))
	}
	return out
}

// respReasoningDelta emits the reasoning output_item lifecycle (once) and a
// reasoning_summary_text.delta carrying the reasoning text.
func respReasoningDelta(state *StreamState, text string) [][]byte {
	var out [][]byte
	if !state.RespReasoningOpen {
		state.RespReasoningItemID = "rs_converted"
		out = append(out, respEvent("response.output_item.added", state, map[string]any{
			"output_index": state.RespOutputIndex,
			"item": map[string]any{
				"id": state.RespReasoningItemID, "type": "reasoning",
				"status": "in_progress", "summary": []any{},
			},
		}))
		state.RespReasoningOpen = true
	}
	state.RespReasoningText += text
	if text != "" {
		out = append(out, respEvent("response.reasoning_summary_text.delta", state, map[string]any{
			"output_index":  state.RespOutputIndex,
			"summary_index": 0,
			"delta":         text,
		}))
	}
	return out
}

// respReasoningClose closes an open reasoning output item. Returns the
// events to emit.
func respReasoningClose(state *StreamState) [][]byte {
	out := [][]byte{respEvent("response.reasoning_summary_text.done", state, map[string]any{
		"output_index":  state.RespOutputIndex,
		"summary_index": 0,
		"text":          state.RespReasoningText,
	})}
	// emit output_item.done with the accumulated summary, then advance.
	out = append(out, respEvent("response.output_item.done", state, map[string]any{
		"output_index": state.RespOutputIndex,
		"item": map[string]any{
			"id": state.RespReasoningItemID, "type": "reasoning",
			"status":  "completed",
			"summary": []any{map[string]any{"type": "summary_text", "text": state.RespReasoningText}},
		},
	}))
	state.RespReasoningOpen = false
	state.RespOutputIndex++
	return out
}

// respToolCallDeltas emits output_item.added for a new tool call (with the
// custom-tool wrapper applied where applicable) and the arguments/input delta.
// Tool-call identity is keyed by the OpenAI tool_call index (delta chunks carry
// index, not id); a new call is announced when its index has not been seen.
func respToolCallDeltas(state *StreamState, tcs []any) [][]byte {
	var out [][]byte
	if state.RespReasoningOpen {
		out = append(out, respReasoningClose(state)...)
	}
	for _, tc := range tcs {
		call, ok := tc.(map[string]any)
		if !ok {
			continue
		}
		idx := int(numToInt64(call["index"]))
		fn, _ := call["function"].(map[string]any)
		name := ""
		if fn != nil {
			name = stringVal(fn["name"])
		}
		args := ""
		if fn != nil {
			args = stringVal(fn["arguments"])
		}
		callID := stringVal(call["id"])
		itemID := state.RespToolItemByID[fmt.Sprintf("%d", idx)]

		// New tool call: announce output_item.added.
		if callID != "" && name != "" && itemID == "" {
			if state.RespTextBlockOpen {
				out = append(out, respTextClose(state)...)
			}
			itemID = "fc_" + callID
			state.RespToolItemByID[fmt.Sprintf("%d", idx)] = itemID
			state.RespToolItemByID[callID] = itemID
			state.RespToolCallByItem = mapStringSet(state.RespToolCallByItem)
			state.RespToolCallByItem[itemID] = append(state.RespToolCallByItem[itemID], name)
			item := map[string]any{
				"id": itemID, "status": "in_progress", "call_id": callID,
			}
			if _, ok := responsesCustomToolOriginalName(name); ok {
				item["type"] = "custom_tool_call"
				item["name"] = name
			} else {
				item["type"] = "function_call"
				item["name"] = name
				item["arguments"] = ""
			}
			out = append(out, respEvent("response.output_item.added", state, map[string]any{
				"output_index": state.RespOutputIndex,
				"item":         item,
			}))
		}

		// Arguments delta (keyed by index so delta-only chunks are handled).
		if args != "" {
			// Determine isCustom from the announced name for this item id.
			isCustom := false
			if names := state.RespToolCallByItem[itemID]; len(names) > 0 {
				if _, ok := responsesCustomToolOriginalName(names[len(names)-1]); ok {
					isCustom = true
				}
			}
			if isCustom {
				out = append(out, respEvent("response.custom_tool_call_input.delta", state, map[string]any{
					"output_index": state.RespOutputIndex,
					"item_id":      itemID,
					"delta":        args,
				}))
			} else {
				out = append(out, respEvent("response.function_call_arguments.delta", state, map[string]any{
					"output_index": state.RespOutputIndex,
					"item_id":      itemID,
					"delta":        args,
				}))
			}
		}
	}
	return out
}

// respTextClose closes an open message output item (text part + item).
func respTextClose(state *StreamState) [][]byte {
	out := [][]byte{
		respEvent("response.output_text.done", state, map[string]any{
			"output_index":  state.RespOutputIndex,
			"content_index": state.RespContentIndex,
			"text":          state.RespTextAccum,
		}),
		respEvent("response.content_part.done", state, map[string]any{
			"output_index":  state.RespOutputIndex,
			"content_index": state.RespContentIndex,
			"part":          map[string]any{"type": "output_text", "text": state.RespTextAccum, "annotations": []any{}},
		}),
		respEvent("response.output_item.done", state, map[string]any{
			"output_index": state.RespOutputIndex,
			"item": map[string]any{
				"id": state.RespMessageItemID, "type": "message", "role": "assistant",
				"status":  "completed",
				"content": []any{map[string]any{"type": "output_text", "text": state.RespTextAccum, "annotations": []any{}}},
			},
		}),
	}
	state.RespTextBlockOpen = false
	state.RespOutputIndex++
	return out
}

// finalizeOpenAIToResponses closes any open items and emits response.completed.
// It is idempotent: a second call is a no-op.
func finalizeOpenAIToResponses(state *StreamState) [][]byte {
	if !state.RespStarted || state.RespDone {
		return nil
	}
	var out [][]byte
	if state.RespReasoningOpen {
		out = append(out, respReasoningClose(state)...)
	}
	if state.RespTextBlockOpen {
		out = append(out, respTextClose(state)...)
	}
	// Close any open tool items (deduplicate item IDs: the map keys both
	// index and callID strings to the same item id).
	closed := map[string]bool{}
	for _, itemID := range state.RespToolItemByID {
		if closed[itemID] {
			continue
		}
		closed[itemID] = true
		out = append(out, respToolItemClose(state, itemID)...)
		state.RespOutputIndex++
	}
	state.RespToolItemByID = map[string]string{}
	state.RespToolCallByItem = nil

	if state.RespStatus == "" {
		state.RespStatus = "completed"
	}
	output := state.respCompletedOutput()
	out = append(out, respEvent("response.completed", state, map[string]any{
		"response": map[string]any{
			"id": state.RespResponseID, "object": "response",
			"status": state.RespStatus, "model": state.RespModel,
			"output": output,
			"usage": map[string]any{
				"input_tokens":  json.Number(fmt.Sprintf("%d", state.RespUsage.PromptTokens)),
				"output_tokens": json.Number(fmt.Sprintf("%d", state.RespUsage.CompletionTokens)),
				"total_tokens":  json.Number(fmt.Sprintf("%d", state.RespUsage.PromptTokens+state.RespUsage.CompletionTokens)),
			},
		},
	}))
	state.RespDone = true
	return out
}

// respToolItemClose emits the arguments/input .done + output_item.done for a
// tool call. The accumulated arguments are unknown at this streaming layer
// (only deltas were forwarded), so an empty arguments string is emitted, which
// is consistent with the upstream having already streamed the real deltas.
func respToolItemClose(state *StreamState, itemID string) [][]byte {
	evType := "response.function_call_arguments.done"
	out := [][]byte{respEvent(evType, state, map[string]any{
		"output_index": state.RespOutputIndex,
		"item_id":      itemID,
		"arguments":    "",
	})}
	out = append(out, respEvent("response.output_item.done", state, map[string]any{
		"output_index": state.RespOutputIndex,
		"item": map[string]any{
			"id": itemID, "status": "completed", "type": "function_call",
			"call_id": strings.TrimPrefix(itemID, "fc_"),
			"name":    "", "arguments": "",
		},
	}))
	return out
}

// respCompletedOutput builds a best-effort output array for response.completed.
func (s *StreamState) respCompletedOutput() []any {
	var output []any
	if s.RespReasoningText != "" {
		output = append(output, map[string]any{
			"id": s.RespReasoningItemID, "type": "reasoning", "status": "completed",
			"summary": []any{map[string]any{"type": "summary_text", "text": s.RespReasoningText}},
		})
	}
	if s.RespTextBlockOpen || s.RespTextAccum != "" || s.RespMessageItemID == "" {
		// message item may have been closed already; include only if not closed.
	}
	if s.RespMessageItemID != "" && s.RespTextAccum != "" && !s.RespTextBlockOpen {
		// Already emitted via output_item.done; do not duplicate.
	}
	// Tool calls are emitted via output_item.done during streaming; do not
	// duplicate them here. The output array is best-effort.
	return output
}

// responsesStatusFromFinishReason maps a Chat finish_reason to a Responses
// status.
func responsesStatusFromFinishReason(reason string) string {
	switch reason {
	case "length":
		return "incomplete"
	default:
		return "completed"
	}
}

// ── Responses → Chat (direct) ───────────────────────────────────────────────

func convertStreamChunkResponsesToOpenAI(raw []byte, state *StreamState) ([][]byte, error) {
	root, err := parseStrictJSON(raw)
	if err != nil {
		return nil, ErrInvalidStreamChunk
	}
	eventType := stringVal(root["type"])
	if eventType == "" {
		return nil, ErrInvalidStreamChunk
	}
	if state.RespInDone {
		return nil, ErrInvalidStreamChunk
	}

	var results [][]byte

	switch eventType {
	case "response.created":
		if state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}
		resp, ok := root["response"].(map[string]any)
		if !ok || stringVal(resp["id"]) == "" || stringVal(resp["model"]) == "" || stringVal(resp["status"]) == "" {
			return nil, ErrInvalidStreamChunk
		}
		state.RespInCreated = true
		state.RespInMessageID = stringVal(resp["id"])
		state.RespInModel = stringVal(resp["model"])

	case "response.in_progress", "response.queued":
		if !state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}

	case "response.output_item.added":
		if !state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}
		results = append(results, respInOutputItemAdded(root, state)...)

	case "response.output_text.delta":
		if !state.RespInCreated || !isString(root["delta"]) {
			return nil, ErrInvalidStreamChunk
		}
		delta := stringVal(root["delta"])
		results = append(results, respInEnsureRole(state)...)
		if delta != "" {
			state.RespInTextAccum += delta
			results = append(results, buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{"content": delta}, "", nil))
		}

	case "response.output_text.done", "response.content_part.added",
		"response.content_part.done", "response.output_item.done",
		"response.reasoning_summary_text.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done",
		"response.output_text.annotation.added":
		// Lifecycle: no Chat delta.

	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		if !state.RespInCreated || !isString(root["delta"]) {
			return nil, ErrInvalidStreamChunk
		}
		delta := stringVal(root["delta"])
		results = append(results, respInEnsureRole(state)...)
		if delta != "" {
			state.RespInReasoning += delta
			results = append(results, buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{"reasoning_content": delta}, "", nil))
		}

	case "response.function_call_arguments.delta":
		if !state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}
		results = append(results, respInArgsDelta(root, state, false)...)

	case "response.custom_tool_call_input.delta":
		if !state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}
		results = append(results, respInArgsDelta(root, state, true)...)

	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		// Lifecycle: arguments already streamed.

	case "response.completed":
		if !state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}
		results = append(results, respInFinish(root, state)...)

	case "response.incomplete", "response.failed", "error":
		if !state.RespInCreated {
			return nil, ErrInvalidStreamChunk
		}
		results = append(results, respInFinish(root, state)...)

	case "ping":
		// Ignore.

	default:
		// Unknown but structurally valid event: ignore (forward-compatible).
	}

	return results, nil
}

// respInEnsureRole emits the role-announcement chunk once per stream.
func respInEnsureRole(state *StreamState) [][]byte {
	if state.RespInStarted {
		return nil
	}
	if state.RespInMessageID == "" {
		state.RespInMessageID = "chatcmpl_converted"
	}
	state.RespInStarted = true
	return [][]byte{buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{"role": "assistant"}, "", nil)}
}

// respInOutputItemAdded records the tool item and emits its tool_call start.
func respInOutputItemAdded(root map[string]any, state *StreamState) [][]byte {
	item, ok := root["item"].(map[string]any)
	if !ok {
		return nil
	}
	itemID := stringVal(item["id"])
	callID := stringVal(item["call_id"])
	if callID == "" {
		callID = itemID
	}
	switch stringVal(item["type"]) {
	case "function_call", "custom_tool_call":
		if itemID == "" {
			return nil
		}
		results := respInEnsureRole(state)
		name := stringVal(item["name"])
		if stringVal(item["type"]) == "custom_tool_call" {
			name = responsesCustomToolWrapperName(name)
		}
		if state.RespInToolItems == nil {
			state.RespInToolItems = map[string]int{}
		}
		if _, exists := state.RespInToolItems[itemID]; !exists {
			idx := len(state.RespInToolCalls)
			state.RespInToolItems[itemID] = idx
			state.RespInToolCalls = append(state.RespInToolCalls, map[string]any{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": "",
				},
			})
			results = append(results, buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{
				"tool_calls": []any{map[string]any{
					"index":    json.Number(fmt.Sprintf("%d", idx)),
					"id":       callID,
					"type":     "function",
					"function": map[string]any{"name": name, "arguments": ""},
				}},
			}, "", nil))
		}
		return results
	}
	return nil
}

// respInArgsDelta emits an arguments delta for the tool item referenced by the
// event. isCustom selects the input- vs arguments-shaped Chat delta (both map
// to function.arguments on the Chat side).
func respInArgsDelta(root map[string]any, state *StreamState, isCustom bool) [][]byte {
	_ = isCustom
	itemID := stringVal(root["item_id"])
	delta := stringVal(root["delta"])
	if delta == "" || state.RespInToolItems == nil {
		return nil
	}
	idx, ok := state.RespInToolItems[itemID]
	if !ok {
		return nil
	}
	return [][]byte{buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{
		"tool_calls": []any{map[string]any{
			"index":    json.Number(fmt.Sprintf("%d", idx)),
			"function": map[string]any{"arguments": delta},
		}},
	}, "", nil)}
}

// respInFinish emits the terminal Chat chunk with finish_reason + usage.
func respInFinish(root map[string]any, state *StreamState) [][]byte {
	if state.RespInDone {
		return nil
	}
	results := respInEnsureRole(state)
	finishReason := "stop"
	if len(state.RespInToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	usage := respInUsage(root, state)
	if status := respStatusFromEvent(root); status == "incomplete" {
		finishReason = "length"
	}
	results = append(results, buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{}, finishReason, usage))
	state.RespInDone = true
	return results
}

func respStatusFromEvent(root map[string]any) string {
	if resp, ok := root["response"].(map[string]any); ok {
		return stringVal(resp["status"])
	}
	if t := stringVal(root["type"]); t == "response.incomplete" {
		return "incomplete"
	}
	return ""
}

func respInUsage(root map[string]any, state *StreamState) map[string]any {
	if resp, ok := root["response"].(map[string]any); ok {
		if u, ok := resp["usage"].(map[string]any); ok {
			state.RespInUsage.PromptTokens = numToInt64(u["input_tokens"])
			state.RespInUsage.CompletionTokens = numToInt64(u["output_tokens"])
		}
	}
	return map[string]any{
		"prompt_tokens":     json.Number(fmt.Sprintf("%d", state.RespInUsage.PromptTokens)),
		"completion_tokens": json.Number(fmt.Sprintf("%d", state.RespInUsage.CompletionTokens)),
		"total_tokens":      json.Number(fmt.Sprintf("%d", state.RespInUsage.PromptTokens+state.RespInUsage.CompletionTokens)),
	}
}

// finalizeResponsesToOpenAI synthesizes a terminal Chat chunk when the upstream
// Responses stream ended cleanly without a response.completed event. Idempotent.
func finalizeResponsesToOpenAI(state *StreamState) [][]byte {
	if !state.RespInStarted || state.RespInDone {
		return nil
	}
	finishReason := "stop"
	if len(state.RespInToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	usage := map[string]any{
		"prompt_tokens":     json.Number(fmt.Sprintf("%d", state.RespInUsage.PromptTokens)),
		"completion_tokens": json.Number(fmt.Sprintf("%d", state.RespInUsage.CompletionTokens)),
		"total_tokens":      json.Number(fmt.Sprintf("%d", state.RespInUsage.PromptTokens+state.RespInUsage.CompletionTokens)),
	}
	state.RespInDone = true
	return [][]byte{buildOpenAIChunk(state.RespInMessageID, state.RespInModel, map[string]any{}, finishReason, usage)}
}

// ── Composite: Anthropic → Responses (through Chat) ─────────────────────────

func convertStreamChunkAnthropicToResponses(raw []byte, state *StreamState) ([][]byte, error) {
	if state.SubAntToChat == nil {
		state.SubAntToChat = &StreamState{}
	}
	if state.SubChatToResp == nil {
		state.SubChatToResp = &StreamState{}
	}
	chatChunks, err := convertStreamChunkAnthropicToOpenAI(raw, state.SubAntToChat)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, chunk := range chatChunks {
		conv, err := convertStreamChunkOpenAIToResponses(chunk, state.SubChatToResp)
		if err != nil {
			return nil, err
		}
		out = append(out, conv...)
	}
	return out, nil
}

func finalizeAnthropicToResponses(state *StreamState) [][]byte {
	if state.SubAntToChat == nil {
		state.SubAntToChat = &StreamState{}
	}
	if state.SubChatToResp == nil {
		state.SubChatToResp = &StreamState{}
	}
	chatChunks := finalizeAnthropicToOpenAI(state.SubAntToChat)
	var out [][]byte
	for _, chunk := range chatChunks {
		conv, err := convertStreamChunkOpenAIToResponses(chunk, state.SubChatToResp)
		if err != nil {
			return out
		}
		out = append(out, conv...)
	}
	out = append(out, finalizeOpenAIToResponses(state.SubChatToResp)...)
	return out
}

// ── Composite: Responses → Anthropic (through Chat) ─────────────────────────

func convertStreamChunkResponsesToAnthropic(raw []byte, state *StreamState) ([][]byte, error) {
	if state.SubRespToChat == nil {
		state.SubRespToChat = &StreamState{}
	}
	if state.SubChatToAnt == nil {
		state.SubChatToAnt = &StreamState{}
	}
	chatChunks, err := convertStreamChunkResponsesToOpenAI(raw, state.SubRespToChat)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for _, chunk := range chatChunks {
		conv, err := convertStreamChunkOpenAIToAnthropic(chunk, state.SubChatToAnt)
		if err != nil {
			return nil, err
		}
		out = append(out, conv...)
	}
	return out, nil
}

func finalizeResponsesToAnthropic(state *StreamState) [][]byte {
	if state.SubRespToChat == nil {
		state.SubRespToChat = &StreamState{}
	}
	if state.SubChatToAnt == nil {
		state.SubChatToAnt = &StreamState{}
	}
	chatChunks := finalizeResponsesToOpenAI(state.SubRespToChat)
	var out [][]byte
	for _, chunk := range chatChunks {
		conv, err := convertStreamChunkOpenAIToAnthropic(chunk, state.SubChatToAnt)
		if err != nil {
			return out
		}
		out = append(out, conv...)
	}
	out = append(out, finalizeOpenAIToAnthropic(state.SubChatToAnt)...)
	return out
}

// ── Responses event helpers ─────────────────────────────────────────────────

// respEvent builds one Responses SSE event payload (without the SSE framing)
// and assigns the next monotonic sequence_number.
func respEvent(eventType string, state *StreamState, extra map[string]any) []byte {
	state.RespSequence++
	ev := map[string]any{"type": eventType, "sequence_number": json.Number(fmt.Sprintf("%d", state.RespSequence))}
	for k, v := range extra {
		ev[k] = v
	}
	// response.created/in_progress/completed carry a top-level "response";
	// deltas carry item_id/output_index when relevant. extra supplies them.
	b, _ := json.Marshal(ev)
	return b
}

// responsesResponseID derives a Responses response id from a Chat chunk id.
func responsesResponseID(chatID string) string {
	if chatID == "" {
		return "resp_converted"
	}
	if strings.HasPrefix(chatID, "resp_") {
		return chatID
	}
	return "resp_" + chatID
}

// nowUnix is a small helper kept for symmetry with the non-stream builders.
func nowUnix() int64 { return time.Now().Unix() }

// mapStringSet returns m if non-nil, else a fresh map.
func mapStringSet(m map[string][]string) map[string][]string {
	if m == nil {
		return map[string][]string{}
	}
	return m
}
