package anthropicadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func nativeFrames() string {
	return strings.Join([]string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"m","type":"message","model":"claude","usage":{"input_tokens":2}}}` + "\n\n",
		`event: ping` + "\n" + `data: {"type":"ping"}` + "\n\n",
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n",
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}` + "\n\n",
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","thinking":""}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta","thinking":"thought"}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"sig"}}` + "\n\n",
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}` + "\n\n",
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool","name":"f","input":{}}}` + "\n\n",
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{}"}}` + "\n\n",
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":2}` + "\n\n",
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"type":"message_delta","stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":3}}` + "\n\n",
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n",
	}, "")
}

func TestAnthropicStreamNativeSequence(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, nativeFrames())
	}))
	defer ts.Close()
	open, err := newTestClient(t, ts, nil).Stream(context.Background(), streamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Source.Close()
	var events []sdk.StreamEvent
	for {
		ev, err := open.Source.Next(context.Background())
		if err != nil {
			if !errors.Is(err, streaming.ErrEndOfStream) {
				t.Fatalf("Next: %v", err)
			}
			break
		}
		events = append(events, ev)
	}
	if len(events) != 14 {
		t.Fatalf("events = %d", len(events))
	}
	for i, e := range events {
		if e.Sequence != uint64(i+1) || e.Meta.Sequence != e.Sequence {
			t.Fatalf("sequence[%d]=%d/%d", i, e.Sequence, e.Meta.Sequence)
		}
		if len(e.Data) > sdk.MaxStreamEventDataBytes {
			t.Fatal("unbounded data")
		}
	}
	if events[3].Meta.Kind != streaming.EventSemantic || events[6].Meta.Kind != streaming.EventSemantic || events[10].Meta.Kind != streaming.EventSemantic {
		t.Fatalf("semantic kinds: %#v", events)
	}
	if events[7].Meta.Kind != streaming.EventLifecycle || !bytes.Contains(events[7].Data, []byte(`"signature":"sig"`)) {
		t.Fatalf("signature lifecycle/data = %#v", events[7])
	}
	if events[12].Meta.Kind != streaming.EventUsage || events[12].Meta.Usage.CompletionTokens != 3 {
		t.Fatalf("usage = %#v", events[12])
	}
	if events[13].Meta.Kind != streaming.EventFinish || events[13].Meta.FinishReason != "end_turn" {
		t.Fatalf("finish = %#v", events[13])
	}
}

func TestAnthropicParserRejectsMalformedOrderAndTerminal(t *testing.T) {
	cases := map[string]string{
		"index": `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","model":"x","usage":{"input_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

`,
		"signature": `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","model":"x","usage":{"input_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"s"}}

`,
		"poststop": nativeFrames() + `event: ping
data: {"type":"ping"}

`,
		"truncated": `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","model":"x","usage":{"input_tokens":0}}}`,
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			o := newAnthropicSSEObserver(context.Background())
			_ = o.observe([]byte(wire))
			o.finish(nil)
			for range o.events {
			}
			if !errors.Is(o.terminalError(), sdk.ErrProtocol) {
				t.Fatalf("error = %v", o.terminalError())
			}
		})
	}
}

func TestAnthropicParserNativeErrorIsPayloadFree(t *testing.T) {
	o := newAnthropicSSEObserver(context.Background())
	if err := o.observe([]byte(`event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"do-not-leak"}}

`)); err != nil {
		t.Fatal(err)
	}
	ev := <-o.events
	if ev.Meta.Kind != streaming.EventNativeError || ev.Data != nil || !errors.Is(ev.Classified, sdk.ErrUnavailable) || strings.Contains(fmt.Sprint(ev.Classified), "do-not-leak") {
		t.Fatalf("native error = %#v", ev)
	}
}

func TestAnthropicStreamNativeTerminalBeatsSDKDecoderError(t *testing.T) {
	cases := []struct {
		name string
		typ  string
		want error
	}{
		{name: "overloaded", typ: "overloaded_error", want: sdk.ErrUnavailable},
		{name: "rate limit", typ: "rate_limit_error", want: sdk.ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const secret = "provider-native-message-must-not-leak"
			wire := fmt.Sprintf("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":%q,\"message\":%q}}\n\n", tc.typ, secret)
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, wire)
			}))
			defer ts.Close()

			open, err := newTestClient(t, ts, nil).Stream(context.Background(), streamCall(ts.URL, "key"))
			if err != nil {
				t.Fatal(err)
			}
			defer open.Source.Close()

			event, err := open.Source.Next(context.Background())
			if err != nil {
				t.Fatalf("native event Next: %v", err)
			}
			if event.Meta.Kind != streaming.EventNativeError || event.Data != nil || !errors.Is(event.Classified, tc.want) || strings.Contains(fmt.Sprint(event), secret) || strings.Contains(fmt.Sprint(event.Classified), secret) {
				t.Fatalf("native event = %#v classified=%v", event, event.Classified)
			}

			_, err = open.Source.Next(context.Background())
			if !errors.Is(err, tc.want) || errors.Is(err, sdk.ErrProtocol) || strings.Contains(fmt.Sprint(err), secret) {
				t.Fatalf("terminal Next = %v, want native %v without protocol/message", err, tc.want)
			}
		})
	}
}

func TestAnthropicStreamUnknownNativeErrorIsSafeProtocol(t *testing.T) {
	const secret = "unknown-provider-native-message-must-not-leak"
	wire := fmt.Sprintf("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"future_error\",\"message\":%q}}\n\n", secret)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, wire)
	}))
	defer ts.Close()

	open, err := newTestClient(t, ts, nil).Stream(context.Background(), streamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Source.Close()
	event, err := open.Source.Next(context.Background())
	if err != nil || event.Meta.Kind != streaming.EventNativeError || !errors.Is(event.Classified, sdk.ErrProtocol) || strings.Contains(fmt.Sprint(event.Classified), secret) {
		t.Fatalf("native event = %#v, %v", event, err)
	}
	_, err = open.Source.Next(context.Background())
	if !errors.Is(err, sdk.ErrProtocol) || strings.Contains(fmt.Sprint(err), secret) {
		t.Fatalf("terminal Next = %v", err)
	}
}

func TestAnthropicParserLimitsAndFuzzSafety(t *testing.T) {
	o := newAnthropicSSEObserver(context.Background())
	if err := o.observe([]byte("event: ping\ndata: " + strings.Repeat("x", sdk.MaxStreamEventDataBytes+1))); !errors.Is(err, errAnthropicStreamProtocol) {
		t.Fatalf("limit = %v", err)
	}
}
func FuzzAnthropicSSEParser(f *testing.F) {
	f.Add([]byte(nativeFrames()))
	f.Add([]byte(`event: error\ndata: {"type":"error","error":{"type":"api_error","message":"secret"}}\n\n`))
	f.Fuzz(func(t *testing.T, wire []byte) {
		o := newAnthropicSSEObserver(context.Background())
		_ = o.observe(wire)
		o.finish(nil)
		for range o.events {
		}
	})
}

func TestAnthropicSourceCloseWakesBlockedNextAndConvergesPump(t *testing.T) {
	started := make(chan struct{})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer ts.Close()
	open, err := newTestClient(t, ts, nil).Stream(context.Background(), streamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	source, ok := open.Source.(*messageSource)
	if !ok {
		t.Fatalf("source = %T", open.Source)
	}

	nextDone := make(chan error, 1)
	go func() {
		_, err := source.Next(context.Background())
		nextDone <- err
	}()
	// Give Next an opportunity to enter its select before Close signals it.
	time.Sleep(10 * time.Millisecond)

	// Closing a normal HTTP-backed official stream is bounded by its response
	// body Close contract. It must also wake the source's blocked consumer.
	startedClose := time.Now()
	if err := source.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if elapsed := time.Since(startedClose); elapsed > time.Second {
		t.Fatalf("Close blocked for %v", elapsed)
	}
	select {
	case err := <-nextDone:
		if !errors.Is(err, ErrStreamClosed) {
			t.Fatalf("blocked Next error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not wake blocked Next")
	}
	select {
	case <-source.pumpDone:
	case <-time.After(time.Second):
		t.Fatal("pump did not converge after official SDK close")
	}
	if _, err := source.Next(context.Background()); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("post-close Next = %v", err)
	}
}

// TestAnthropicStreamHasNoDetachedCloseGoroutine prevents a Close path from
// hiding an unbounded SDK/body Close behind a goroutine. The source contract
// instead relies on the official response-body Close being bounded.
func TestAnthropicStreamHasNoDetachedCloseGoroutine(t *testing.T) {
	source, err := os.ReadFile("stream.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "stream.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		goStmt, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		foundClose := false
		ast.Inspect(goStmt.Call, func(child ast.Node) bool {
			call, ok := child.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Close" {
				foundClose = true
			}
			return true
		})
		if foundClose {
			t.Error("stream.go starts a goroutine that calls Close")
		}
		return true
	})
}

func TestAnthropicSourceCloseIsConcurrentAndIdempotent(t *testing.T) {
	started := make(chan struct{})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))
	defer ts.Close()
	open, err := newTestClient(t, ts, nil).Stream(context.Background(), streamCall(ts.URL, "key"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	source := open.Source.(*messageSource)

	const closers = 32
	start := make(chan struct{})
	done := make(chan error, closers)
	for range closers {
		go func() {
			<-start
			done <- source.Close()
		}()
	}
	close(start)
	for range closers {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent Close() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent Close blocked")
		}
	}
	select {
	case <-source.pumpDone:
	case <-time.After(time.Second):
		t.Fatal("pump did not converge")
	}
}
