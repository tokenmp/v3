package openaiadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func validChunk(extra string) []byte {
	return []byte(`{"id":"chat-1","object":"chat.completion.chunk","created":1,"model":"gpt-test","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}],"provider_extension":{"nested":[true,1]}` + extra + `}`)
}

func TestParseChunkClassifiesCanonicalOwnedPayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, raw string
		kind      streaming.EventKind
		finish    string
		usage     int64
	}{
		{"lifecycle role", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`, streaming.EventLifecycle, "", 0},
		{"content", string(validChunk("")), streaming.EventSemantic, "", 0},
		{"reasoning", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`, streaming.EventSemantic, "", 0},
		{"tool", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":null}]}`, streaming.EventSemantic, "", 0},
		{"usage only", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`, streaming.EventUsage, "", 3},
		{"finish", `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"function_call"}]}`, streaming.EventFinish, "function_call", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, data, err := parseChunk([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if ev.Kind != tc.kind || ev.FinishReason != tc.finish {
				t.Fatalf("event=%+v", ev)
			}
			if tc.kind == streaming.EventUsage && ev.Usage.TotalTokens != tc.usage {
				t.Fatalf("usage=%+v", ev.Usage)
			}
			if !bytes.Equal(data, []byte(tc.raw)) && !bytes.Contains(data, []byte(`"provider_extension"`)) && tc.name == "content" {
				t.Fatalf("lost extension: %s", data)
			}
			data[0] = 'X'
			if data2 := mustParse(t, []byte(tc.raw)); data2[0] == 'X' {
				t.Fatal("payload aliases caller or parser state")
			}
		})
	}
}
func mustParse(t *testing.T, raw []byte) []byte {
	t.Helper()
	_, d, err := parseChunk(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestParseChunkInBandErrorIsPayloadFreeNativeEvent(t *testing.T) {
	ev, data, err := parseChunk([]byte(`{"error":{"message":"secret payload","code":"ignored"}}`))
	if err != nil || ev.Kind != streaming.EventNativeError || data != nil {
		t.Fatalf("event/data/error = %#v/%q/%v", ev, data, err)
	}
}

func TestParseChunkStreamEventDataLimit(t *testing.T) {
	t.Parallel()
	// A bounded logprobs array controls total input size without exceeding the
	// parser's independent 64 KiB per-string cap.
	prefix := []byte(`{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null,"logprobs":[`)
	suffix := []byte(`]}]}`)
	const values = 4
	filler := sdk.MaxStreamEventDataBytes - len(prefix) - len(suffix) - (2*values + values - 1)
	if filler < 0 || filler > values*maxStringBytes {
		t.Fatalf("invalid bounded test filler = %d", filler)
	}
	atLimit := append([]byte(nil), prefix...)
	for i := 0; i < values; i++ {
		if i > 0 {
			atLimit = append(atLimit, ',')
		}
		length := filler / (values - i)
		filler -= length
		atLimit = append(atLimit, '"')
		atLimit = append(atLimit, bytes.Repeat([]byte("x"), length)...)
		atLimit = append(atLimit, '"')
	}
	atLimit = append(atLimit, suffix...)
	if len(atLimit) != sdk.MaxStreamEventDataBytes {
		t.Fatalf("at-limit size = %d", len(atLimit))
	}
	if _, data, err := parseChunk(atLimit); err != nil || len(data) > sdk.MaxStreamEventDataBytes {
		t.Fatalf("at-limit parse = (%d bytes, %v)", len(data), err)
	}
	if _, _, err := parseChunk(append(atLimit, ' ')); !errors.Is(err, errChunkProtocol) {
		t.Fatalf("over-limit parse = %v", err)
	}
}

func TestParseChunkRejectsInvalidWithoutLeakingPayload(t *testing.T) {
	t.Parallel()
	secret := "raw-provider-secret"
	cases := map[string]string{
		"blank": "", "trailing": string(validChunk("")) + `x`, "array": `[]`,
		"duplicate":       `{"id":"c","id":"` + secret + `","object":"chat.completion.chunk","created":1,"model":"m","choices":[]}`,
		"prototype":       `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"__proto__":{}}`,
		"bad required":    `{"id":"","object":"wrong","created":-1,"model":"","choices":[]}`,
		"multi":           `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}`,
		"index":           `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":1,"delta":{}}]}`,
		"finish semantic": `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"stop"}]}`,
		"negative usage":  `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"total_tokens":-1}}`,
		"bad tool":        `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"id":"x"}]},"finish_reason":null}]}`,
		"bad role":        `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"attacker"},"finish_reason":null}]}`,
		"bad delta":       `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":[],"finish_reason":null}]}`,
		"bad finish":      `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"unknown"}]}`,
		"partial usage":   `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"usage":{"total_tokens":1}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseChunk([]byte(raw))
			if !errors.Is(err, errChunkProtocol) || strings.Contains(err.Error(), secret) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func FuzzParseChunk(f *testing.F) {
	f.Add(validChunk(""))
	f.Add([]byte(`{"error":{"message":"secret"}}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, data, err := parseChunk(raw)
		if err == nil && len(data) != 0 && !jsonValid(data) {
			t.Fatal("accepted non-json output")
		}
	})
}
func jsonValid(b []byte) bool { var x any; return len(b) > 0 && json.Unmarshal(b, &x) == nil }

func TestParseChunkRelaxedProviderCompatibility(t *testing.T) {
	t.Parallel()
	// MiniMax intermediate chunk: no finish_reason.
	minimaxIntermediate := `{"id":"chatcmpl-m1","object":"chat.completion.chunk","created":1234,"model":"minimax-model","choices":[{"index":0,"delta":{"content":"hello"}}]}`
	ev, data, err := parseChunk([]byte(minimaxIntermediate))
	if err != nil {
		t.Fatalf("MiniMax intermediate: %v", err)
	}
	if ev.Kind != streaming.EventSemantic {
		t.Fatalf("MiniMax intermediate kind = %v, want semantic", ev.Kind)
	}
	if len(data) == 0 {
		t.Fatal("MiniMax intermediate: no data")
	}

	// MiniMax terminal chunk: has finish_reason + usage.
	minimaxTerminal := `{"id":"chatcmpl-m1","object":"chat.completion.chunk","created":1235,"model":"minimax-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
	ev, data, err = parseChunk([]byte(minimaxTerminal))
	if err != nil {
		t.Fatalf("MiniMax terminal: %v", err)
	}
	if ev.Kind != streaming.EventFinish || ev.FinishReason != "stop" {
		t.Fatalf("MiniMax terminal: kind=%v finish=%s", ev.Kind, ev.FinishReason)
	}
	if ev.Usage == nil || ev.Usage.TotalTokens != 15 {
		t.Fatalf("MiniMax terminal usage: %+v", ev.Usage)
	}

	// astron/GLM intermediate chunk: no created, no finish_reason, no usage.
	astronIntermediate := `{"id":"chatcmpl-a1","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{"content":"world"}}]}`
	ev, data, err = parseChunk([]byte(astronIntermediate))
	if err != nil {
		t.Fatalf("astron intermediate: %v", err)
	}
	if ev.Kind != streaming.EventSemantic {
		t.Fatalf("astron intermediate kind = %v, want semantic", ev.Kind)
	}
	if len(data) == 0 {
		t.Fatal("astron intermediate: no data")
	}

	// astron/GLM terminal chunk: has finish_reason + usage, no created.
	astronTerminal := `{"id":"chatcmpl-a1","object":"chat.completion.chunk","model":"glm-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`
	ev, data, err = parseChunk([]byte(astronTerminal))
	if err != nil {
		t.Fatalf("astron terminal: %v", err)
	}
	if ev.Kind != streaming.EventFinish || ev.FinishReason != "stop" {
		t.Fatalf("astron terminal: kind=%v finish=%s", ev.Kind, ev.FinishReason)
	}
	if ev.Usage == nil || ev.Usage.TotalTokens != 11 {
		t.Fatalf("astron terminal usage: %+v", ev.Usage)
	}

	// Standard OpenAI chunk (regression check): full fields.
	standardChunk := string(validChunk(""))
	ev, data, err = parseChunk([]byte(standardChunk))
	if err != nil {
		t.Fatalf("standard OpenAI: %v", err)
	}
	if ev.Kind != streaming.EventSemantic {
		t.Fatalf("standard OpenAI kind = %v, want semantic", ev.Kind)
	}
	if len(data) == 0 {
		t.Fatal("standard OpenAI: no data")
	}

	// Empty choices without usage (lifecycle event, previously rejected).
	emptyNoUsage := `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[]}`
	ev, _, err = parseChunk([]byte(emptyNoUsage))
	if err != nil {
		t.Fatalf("empty choices no usage: %v", err)
	}
	if ev.Kind != streaming.EventLifecycle {
		t.Fatalf("empty choices no usage kind = %v, want lifecycle", ev.Kind)
	}

	// Missing finish_reason on non-semantic delta (lifecycle event, previously rejected).
	missingFinish := `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{}}]}`
	ev, _, err = parseChunk([]byte(missingFinish))
	if err != nil {
		t.Fatalf("missing finish: %v", err)
	}
	if ev.Kind != streaming.EventLifecycle {
		t.Fatalf("missing finish kind = %v, want lifecycle", ev.Kind)
	}

	// Invalid created value (present but wrong type) still rejected.
	badCreated := `{"id":"c","object":"chat.completion.chunk","created":"not-a-number","model":"m","choices":[]}`
	_, _, err = parseChunk([]byte(badCreated))
	if !errors.Is(err, errChunkProtocol) {
		t.Fatalf("bad created should be rejected: %v", err)
	}

	// Invalid finish_reason value (present but unsupported) still rejected.
	badFinish := `{"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"unknown"}]}`
	_, _, err = parseChunk([]byte(badFinish))
	if !errors.Is(err, errChunkProtocol) {
		t.Fatalf("bad finish should be rejected: %v", err)
	}
}

func TestParseResponseChunkResponsesStreaming(t *testing.T) {
	t.Parallel()

	// response.created
	created := `{"type":"response.created","response":{"id":"resp_x","object":"response","status":"in_progress","output":[]},"sequence_number":0}`
	ev, data, err := parseResponseChunk([]byte(created))
	if err != nil {
		t.Fatalf("response.created: %v", err)
	}
	if ev.Kind != streaming.EventLifecycle || ev.EventType != "response.created" {
		t.Fatalf("created: kind=%v type=%s", ev.Kind, ev.EventType)
	}
	if len(data) == 0 {
		t.Fatal("created: no data")
	}

	// response.in_progress
	inProgress := `{"type":"response.in_progress","response":{"id":"resp_x","object":"response","status":"in_progress","output":[]},"sequence_number":1}`
	ev, _, err = parseResponseChunk([]byte(inProgress))
	if err != nil {
		t.Fatalf("response.in_progress: %v", err)
	}
	if ev.Kind != streaming.EventLifecycle || ev.EventType != "response.in_progress" {
		t.Fatalf("in_progress: kind=%v type=%s", ev.Kind, ev.EventType)
	}

	// response.output_item.added
	itemAdded := `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]},"sequence_number":2}`
	ev, _, err = parseResponseChunk([]byte(itemAdded))
	if err != nil {
		t.Fatalf("response.output_item.added: %v", err)
	}
	if ev.Kind != streaming.EventLifecycle || ev.EventType != "response.output_item.added" {
		t.Fatalf("output_item.added: kind=%v type=%s", ev.Kind, ev.EventType)
	}

	// response.output_text.delta (semantic)
	textDelta := `{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hello","sequence_number":3}`
	ev, data, err = parseResponseChunk([]byte(textDelta))
	if err != nil {
		t.Fatalf("response.output_text.delta: %v", err)
	}
	if ev.Kind != streaming.EventSemantic || ev.EventType != "response.output_text.delta" {
		t.Fatalf("text delta: kind=%v type=%s", ev.Kind, ev.EventType)
	}
	if len(data) == 0 {
		t.Fatal("text delta: no data")
	}

	// response.output_text.done (lifecycle)
	textDone := `{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"Hello world","sequence_number":4}`
	ev, _, err = parseResponseChunk([]byte(textDone))
	if err != nil {
		t.Fatalf("response.output_text.done: %v", err)
	}
	if ev.Kind != streaming.EventLifecycle || ev.EventType != "response.output_text.done" {
		t.Fatalf("text done: kind=%v type=%s", ev.Kind, ev.EventType)
	}

	// response.completed (finish + usage)
	completed := `{"type":"response.completed","response":{"id":"resp_x","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello world"}]}],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15},"model":"gpt-4o"},"sequence_number":5}`
	ev, data, err = parseResponseChunk([]byte(completed))
	if err != nil {
		t.Fatalf("response.completed: %v", err)
	}
	if ev.Kind != streaming.EventFinish || ev.EventType != "response.completed" {
		t.Fatalf("completed: kind=%v type=%s", ev.Kind, ev.EventType)
	}
	if ev.FinishReason != "completed" {
		t.Fatalf("completed finish reason = %q, want completed", ev.FinishReason)
	}
	if ev.Usage == nil || ev.Usage.TotalTokens != 15 {
		t.Fatalf("completed usage: %+v", ev.Usage)
	}
	if len(data) == 0 {
		t.Fatal("completed: no data")
	}

	// response.failed (native error)
	failed := `{"type":"response.failed","response":{"id":"resp_y","object":"response","status":"failed","output":[]},"sequence_number":6}`
	ev, _, err = parseResponseChunk([]byte(failed))
	if err != nil {
		t.Fatalf("response.failed: %v", err)
	}
	if ev.Kind != streaming.EventNativeError || ev.EventType != "response.failed" {
		t.Fatalf("failed: kind=%v type=%s", ev.Kind, ev.EventType)
	}

	// error event (native error)
	errorEvent := `{"type":"error","code":"server_error","message":"internal error"}`
	ev, _, err = parseResponseChunk([]byte(errorEvent))
	if err != nil {
		t.Fatalf("error event: %v", err)
	}
	if ev.Kind != streaming.EventNativeError || ev.EventType != "error" {
		t.Fatalf("error: kind=%v type=%s", ev.Kind, ev.EventType)
	}

	// response.completed without usage (finish, no usage)
	completedNoUsage := `{"type":"response.completed","response":{"id":"resp_z","object":"response","status":"completed","output":[]},"sequence_number":7}`
	ev, _, err = parseResponseChunk([]byte(completedNoUsage))
	if err != nil {
		t.Fatalf("completed no usage: %v", err)
	}
	if ev.Kind != streaming.EventFinish {
		t.Fatalf("completed no usage: kind=%v", ev.Kind)
	}
	if ev.Usage != nil {
		t.Fatalf("completed no usage: unexpected usage %+v", ev.Usage)
	}
}

func TestParseResponseChunkRejectsInvalid(t *testing.T) {
	t.Parallel()
	secret := "raw-provider-secret"
	cases := map[string]string{
		"blank":        "",
		"not object":   `[]`,
		"no type":      `{"response":{"id":"r"}}`,
		"empty type":   `{"type":""}`,
		"unknown type": `{"type":"chat.completion.chunk"}`,
		"bad type":     `{"type":123}`,
		"trailing":     `{"type":"response.created"}x`,
		"duplicate":    `{"type":"` + secret + `","type":"response.created"}`,
		"prototype":    `{"type":"response.created","__proto__":{}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := parseResponseChunk([]byte(raw))
			if !errors.Is(err, errChunkProtocol) || strings.Contains(err.Error(), secret) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestParseResponseChunkInBandErrorIsPayloadFreeNativeEvent(t *testing.T) {
	// Top-level "error" key (not type) is also treated as in-band error.
	ev, data, err := parseResponseChunk([]byte(`{"error":{"message":"secret payload","code":"ignored"}}`))
	if err != nil || ev.Kind != streaming.EventNativeError || data != nil {
		t.Fatalf("event/data/error = %#v/%q/%v", ev, data, err)
	}
}

func FuzzParseResponseChunk(f *testing.F) {
	f.Add([]byte(`{"type":"response.created","response":{"id":"r","status":"in_progress"}}`))
	f.Add([]byte(`{"type":"response.output_text.delta","delta":"hi"}`))
	f.Add([]byte(`{"type":"response.completed","response":{"id":"r","status":"completed"}}`))
	f.Add([]byte(`{"error":{"message":"secret"}}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, data, err := parseResponseChunk(raw)
		if err == nil && len(data) != 0 && !jsonValid(data) {
			t.Fatal("accepted non-json output")
		}
	})
}
