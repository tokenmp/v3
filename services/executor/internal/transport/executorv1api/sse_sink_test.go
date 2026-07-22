package executorv1api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func sseEvent(sequence uint64, kind streaming.EventKind, eventType, data string) sdk.StreamEvent {
	return sdk.StreamEvent{Sequence: sequence, Meta: streaming.Event{Sequence: sequence, Kind: kind, EventType: eventType}, Data: []byte(data)}
}

func TestOpenAISSEProtocolSinkCommitFramingHeadersAndFinish(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	sink, err := NewOpenAISSEProtocolSink(recorder)
	if err != nil {
		t.Fatal(err)
	}
	first := sseEvent(1, streaming.EventSemantic, "chat.completion.chunk", `{"id":"one"}`)
	if err := sink.Commit(context.Background(), []sdk.StreamEvent{first}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !sink.Committed() {
		t.Fatal("Committed = false")
	}
	finish := sseEvent(2, streaming.EventFinish, "chat.completion.chunk", `{"id":"two","choices":[]}`)
	finish.Meta.FinishReason = "stop"
	if err := sink.WriteEvent(context.Background(), finish); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := recorder.Header().Get("Content-Type"), "text/event-stream"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := recorder.Header().Get("Cache-Control"), "no-cache, no-store"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := recorder.Header().Get("Connection"); got != "" {
		t.Errorf("Connection = %q, must not be set", got)
	}
	want := "data: {\"id\":\"one\"}\n\ndata: {\"id\":\"two\",\"choices\":[]}\n\ndata: [DONE]\n\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if err := sink.WriteEvent(context.Background(), sseEvent(3, streaming.EventSemantic, "chat.completion.chunk", `{}`)); !errors.Is(err, errSSESinkFinished) {
		t.Fatalf("post-finish WriteEvent = %v", err)
	}
}

func TestAnthropicSSEProtocolSinkFramingAndSignaturePayload(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	sink, err := NewAnthropicSSEProtocolSink(recorder)
	if err != nil {
		t.Fatal(err)
	}
	first := sseEvent(1, streaming.EventLifecycle, "message_start", `{"type":"message_start"}`)
	if err := sink.Commit(context.Background(), []sdk.StreamEvent{first}); err != nil {
		t.Fatal(err)
	}
	signature := sseEvent(2, streaming.EventSemantic, "content_block_delta", `{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"opaque-signature"}}`)
	if err := sink.WriteEvent(context.Background(), signature); err != nil {
		t.Fatal(err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	finish := sseEvent(3, streaming.EventFinish, "message_stop", `{"type":"message_stop"}`)
	finish.Meta.FinishReason = "end_turn"
	if err := sink.WriteEvent(context.Background(), finish); err != nil {
		t.Fatal(err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := recorder.Body.String()
	want := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque-signature\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if strings.Contains(got, "[DONE]") {
		t.Error("Anthropic stream must not send OpenAI DONE")
	}
}

type flushRecorder struct {
	http.ResponseWriter
	flushes int
}

func (r *flushRecorder) Flush() { r.flushes++ }

func TestSSEProtocolSinkCommitRejectsDuplicateAndPostFinishBatchEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		events []sdk.StreamEvent
	}{
		{"duplicate sequence", []sdk.StreamEvent{sseEvent(1, streaming.EventSemantic, "chunk", `{}`), sseEvent(1, streaming.EventUsage, "chunk", `{}`)}},
		{"after finish", []sdk.StreamEvent{func() sdk.StreamEvent {
			e := sseEvent(1, streaming.EventFinish, "chunk", `{}`)
			e.Meta.FinishReason = "stop"
			return e
		}(), sseEvent(2, streaming.EventSemantic, "chunk", `{}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			sink, err := NewOpenAISSEProtocolSink(recorder)
			if err != nil {
				t.Fatal(err)
			}
			if err := sink.Commit(context.Background(), tc.events); !errors.Is(err, errSSESinkProtocol) {
				t.Fatalf("Commit = %v", err)
			}
			if sink.Committed() || recorder.Body.Len() != 0 {
				t.Fatalf("invalid batch started response: committed=%t body=%q", sink.Committed(), recorder.Body.String())
			}
		})
	}
}

func TestSSEProtocolSinkFlushLifecycle(t *testing.T) {
	t.Parallel()
	base := httptest.NewRecorder()
	writer := &flushRecorder{ResponseWriter: base}
	sink, err := NewOpenAISSEProtocolSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Commit(context.Background(), []sdk.StreamEvent{sseEvent(1, streaming.EventSemantic, "chunk", `{}`)}); err != nil {
		t.Fatal(err)
	}
	if writer.flushes != 1 {
		t.Fatalf("flushes after Commit = %d, want 1", writer.flushes)
	}
	if err := sink.WriteEvent(context.Background(), sseEvent(2, streaming.EventUsage, "chunk", `{}`)); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteEvent(context.Background(), sseEvent(3, streaming.EventSemantic, "chunk", `{}`)); !errors.Is(err, errSSESinkStaged) {
		t.Fatalf("second unflushed WriteEvent = %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writer.flushes != 2 {
		t.Fatalf("flushes after Flush = %d, want 2", writer.flushes)
	}
}

type noFlushWriter struct{ header http.Header }

func (w *noFlushWriter) Header() http.Header       { return w.header }
func (*noFlushWriter) Write(p []byte) (int, error) { return len(p), nil }
func (*noFlushWriter) WriteHeader(int)             {}

type typedNilWriter struct{ header http.Header }

func (w *typedNilWriter) Header() http.Header       { return w.header }
func (*typedNilWriter) Write(p []byte) (int, error) { return len(p), nil }
func (*typedNilWriter) WriteHeader(int)             {}
func (*typedNilWriter) Flush()                      {}

func TestSSEProtocolSinkFormattingIsRedacted(t *testing.T) {
	t.Parallel()
	sink, err := NewOpenAISSEProtocolSink(httptest.NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if got := fmt.Sprintf(format, sink); got != "executorv1api.SSEProtocolSink([REDACTED])" {
			t.Errorf("fmt.Sprintf(%q) = %q", format, got)
		}
	}
}

func TestSSEProtocolSinkConstructorFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := NewOpenAISSEProtocolSink(&noFlushWriter{header: make(http.Header)}); !errors.Is(err, errSSESinkMisconfigured) {
		t.Fatalf("no flusher error = %v", err)
	}
	var typedNil *typedNilWriter
	if _, err := NewOpenAISSEProtocolSink(typedNil); !errors.Is(err, errSSESinkMisconfigured) {
		t.Fatalf("typed nil error = %v", err)
	}
}

type failingWriter struct {
	header http.Header
	writes int
}

func (w *failingWriter) Header() http.Header         { return w.header }
func (w *failingWriter) Write(p []byte) (int, error) { w.writes++; return 1, errors.New("do-not-leak") }
func (*failingWriter) WriteHeader(int)               {}
func (*failingWriter) Flush()                        {}

func TestSSEProtocolSinkPartialWriteCommitsAndErrorsAreRedacted(t *testing.T) {
	t.Parallel()
	writer := &failingWriter{header: make(http.Header)}
	sink, err := NewOpenAISSEProtocolSink(writer)
	if err != nil {
		t.Fatal(err)
	}
	err = sink.Commit(context.Background(), []sdk.StreamEvent{sseEvent(1, streaming.EventSemantic, "chunk", `{}`)})
	if !errors.Is(err, errSSESinkWrite) || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("Commit error = %v", err)
	}
	if !sink.Committed() || writer.writes == 0 {
		t.Fatalf("committed = %t, writes = %d", sink.Committed(), writer.writes)
	}
}

func TestSSEProtocolSinkRejectsInvalidEventsAndCancelledCommitBeforeHeader(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	sink, err := NewAnthropicSSEProtocolSink(recorder)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sink.Commit(ctx, []sdk.StreamEvent{sseEvent(1, streaming.EventSemantic, "content_block_delta", `{}`)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Commit = %v", err)
	}
	if sink.Committed() || recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("preheader cancellation committed=%t code=%d body=%q", sink.Committed(), recorder.Code, recorder.Body.String())
	}
	bad := sseEvent(1, streaming.EventNativeError, "error", `{}`)
	if err := sink.Commit(context.Background(), []sdk.StreamEvent{bad}); !errors.Is(err, errSSESinkProtocol) {
		t.Fatalf("native error = %v", err)
	}
	bad = sseEvent(1, streaming.EventSemantic, "bad\nframe", `{}`)
	if err := sink.Commit(context.Background(), []sdk.StreamEvent{bad}); !errors.Is(err, errSSESinkProtocol) {
		t.Fatalf("newline event type = %v", err)
	}
	bad = sseEvent(1, streaming.EventSemantic, "unknown", `{}`)
	if err := sink.Commit(context.Background(), []sdk.StreamEvent{bad}); !errors.Is(err, errSSESinkProtocol) {
		t.Fatalf("unknown Anthropic event type = %v", err)
	}
}
