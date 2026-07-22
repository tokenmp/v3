package executorv1api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// SSEProtocolSink renders owned canonical stream payloads into the native SSE
// wire format for exactly one OpenAI Chat or Anthropic Messages response. It
// deliberately owns no handler, hybrid dispatch, or composition wiring.
type SSEProtocolSink struct {
	mu        sync.Mutex
	writer    http.ResponseWriter
	flusher   http.Flusher
	protocol  adapter.Protocol
	committed bool
	staged    bool
	finished  bool
	last      uint64
}

var (
	errSSESinkMisconfigured = errors.New("executorv1api: sse sink misconfigured")
	errSSESinkProtocol      = errors.New("executorv1api: sse sink protocol")
	errSSESinkStaged        = errors.New("executorv1api: sse sink flush pending")
	errSSESinkFinished      = errors.New("executorv1api: sse sink finished")
	errSSESinkWrite         = errors.New("executorv1api: sse sink write failed")
)

// NewOpenAISSEProtocolSink creates an OpenAI Chat Completions SSE sink.
func NewOpenAISSEProtocolSink(w http.ResponseWriter) (*SSEProtocolSink, error) {
	return NewSSEProtocolSink(w, adapter.ProtocolOpenAIChat)
}

// NewOpenAIResponsesSSEProtocolSink creates an OpenAI Responses SSE sink. It
// reuses the OpenAI Chat framing (data: {JSON}\n\n with a trailing [DONE])
// since both OpenAI protocols share that SSE shape.
func NewOpenAIResponsesSSEProtocolSink(w http.ResponseWriter) (*SSEProtocolSink, error) {
	return NewSSEProtocolSink(w, adapter.ProtocolOpenAIChat)
}

// NewAnthropicSSEProtocolSink creates an Anthropic Messages SSE sink.
func NewAnthropicSSEProtocolSink(w http.ResponseWriter) (*SSEProtocolSink, error) {
	return NewSSEProtocolSink(w, adapter.ProtocolAnthropic)
}

// NewSSEProtocolSink validates that the response writer can flush before any
// headers are sent. ResponseController cannot preflight this capability, so a
// concrete http.Flusher is required; typed-nil writers/flushers fail closed.
func NewSSEProtocolSink(w http.ResponseWriter, protocol adapter.Protocol) (*SSEProtocolSink, error) {
	if isNilSSEValue(w) || (protocol != adapter.ProtocolOpenAIChat && protocol != adapter.ProtocolAnthropic) {
		return nil, errSSESinkMisconfigured
	}
	flusher, ok := w.(http.Flusher)
	if !ok || isNilSSEValue(flusher) {
		return nil, errSSESinkMisconfigured
	}
	return &SSEProtocolSink{writer: w, flusher: flusher, protocol: protocol}, nil
}

var _ execution.ProtocolSink = (*SSEProtocolSink)(nil)

// String, GoString, and Format intentionally omit the writer and all stream
// state; either can carry request or response data that is unsafe for logs.
func (*SSEProtocolSink) String() string     { return "executorv1api.SSEProtocolSink([REDACTED])" }
func (s *SSEProtocolSink) GoString() string { return s.String() }
func (s *SSEProtocolSink) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte(s.String()))
}

// Committed reports whether this sink may have started the HTTP response. It
// becomes true before WriteHeader, so even a partial write cannot permit an
// unsafe JSON fallback.
func (s *SSEProtocolSink) Committed() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed
}

// Commit emits and flushes the initial batch. A write or flush failure leaves
// the response committed/uncertain and is intentionally not retryable.
func (s *SSEProtocolSink) Commit(ctx context.Context, events []sdk.StreamEvent) error {
	if s == nil || ctx == nil {
		return errSSESinkMisconfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed || s.staged || s.finished || len(events) == 0 {
		return errSSESinkProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	last, finished := s.last, s.finished
	for _, event := range events {
		if err := s.validateAt(event, last, finished); err != nil {
			return err
		}
		last = event.Sequence
		finished = finished || event.Meta.Kind == streaming.EventFinish
	}
	// Recheck immediately before opening the response. A cancelled request must
	// remain available to its handler's native JSON error path.
	if err := ctx.Err(); err != nil {
		return err
	}
	s.start()
	for _, event := range events {
		if err := s.writeEvent(event); err != nil {
			return err
		}
		s.last = event.Sequence
	}
	s.flusher.Flush()
	return nil
}

// WriteEvent stages exactly one post-commit event. Flush completes its logical
// delivery; callers must not write another event while one is staged.
func (s *SSEProtocolSink) WriteEvent(ctx context.Context, event sdk.StreamEvent) error {
	if s == nil || ctx == nil {
		return errSSESinkMisconfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.committed {
		return errSSESinkProtocol
	}
	if s.finished {
		return errSSESinkFinished
	}
	if s.staged {
		return errSSESinkStaged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.validateAt(event, s.last, s.finished); err != nil {
		return err
	}
	if err := s.writeEvent(event); err != nil {
		return err
	}
	s.last = event.Sequence
	s.staged = true
	return nil
}

// Flush flushes one staged post-commit event.
func (s *SSEProtocolSink) Flush(ctx context.Context) error {
	if s == nil || ctx == nil {
		return errSSESinkMisconfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.committed || !s.staged {
		return errSSESinkProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.flusher.Flush()
	s.staged = false
	return nil
}

func (s *SSEProtocolSink) start() {
	h := s.writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// Do not set Connection: it is invalid/meaningless for HTTP/2.
	s.committed = true
	s.writer.WriteHeader(http.StatusOK)
}

func (s *SSEProtocolSink) validateAt(event sdk.StreamEvent, last uint64, finished bool) error {
	if finished || event.Sequence == 0 || event.Sequence <= last || event.Meta.Sequence != event.Sequence || event.Classified != nil || event.Meta.Kind == streaming.EventNativeError || len(event.Data) == 0 || len(event.Data) > sdk.MaxStreamEventDataBytes || !canonicalSSEJSON(event.Data) {
		return errSSESinkProtocol
	}
	if !validSSEKind(event.Meta.Kind) || !validSSEToken(event.Meta.EventType) {
		return errSSESinkProtocol
	}
	if event.Meta.Kind == streaming.EventFinish {
		if finished || !validSSEToken(event.Meta.FinishReason) {
			return errSSESinkProtocol
		}
	} else if event.Meta.FinishReason != "" {
		return errSSESinkProtocol
	}
	if s.protocol == adapter.ProtocolAnthropic && !validAnthropicSSEEvent(event.Meta.EventType) {
		return errSSESinkProtocol
	}
	return nil
}

func canonicalSSEJSON(data []byte) bool {
	if !json.Valid(data) {
		return false
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return false
	}
	return bytes.Equal(compact.Bytes(), data)
}

func validSSEKind(kind streaming.EventKind) bool {
	switch kind {
	case streaming.EventLifecycle, streaming.EventSemantic, streaming.EventUsage, streaming.EventFinish:
		return true
	default:
		return false
	}
}

func validSSEToken(v string) bool {
	if v == "" {
		return false
	}
	if len(v) > 128 {
		return false
	}
	for _, b := range []byte(v) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func validAnthropicSSEEvent(v string) bool {
	switch v {
	case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "ping":
		return true
	default:
		return false
	}
}

func (s *SSEProtocolSink) writeEvent(event sdk.StreamEvent) error {
	var frame []byte
	if s.protocol == adapter.ProtocolAnthropic {
		frame = append(frame, "event: "...)
		frame = append(frame, event.Meta.EventType...)
		frame = append(frame, '\n')
	}
	frame = append(frame, "data: "...)
	frame = append(frame, event.Data...)
	frame = append(frame, '\n', '\n')
	if event.Meta.Kind == streaming.EventFinish && s.protocol == adapter.ProtocolOpenAIChat {
		frame = append(frame, "data: [DONE]\n\n"...)
	}
	if err := writeAll(s.writer, frame); err != nil {
		return errSSESinkWrite
	}
	if event.Meta.Kind == streaming.EventFinish {
		s.finished = true
	}
	return nil
}

func writeAll(w http.ResponseWriter, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil || n <= 0 {
			return errSSESinkWrite
		}
		data = data[n:]
	}
	return nil
}

func isNilSSEValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
