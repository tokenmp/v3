package anthropicadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

const (
	maxAnthropicSSELineBytes  = sdk.MaxStreamEventDataBytes
	maxAnthropicSSEFrameBytes = sdk.MaxStreamEventDataBytes
	maxAnthropicSSEFrameLines = 4096
	maxAnthropicSSEDataLines  = 1024
	maxAnthropicJSONDepth     = 64
	maxAnthropicJSONNodes     = 10_000
)

var (
	errAnthropicStreamProtocol = errors.New("anthropicadapter: invalid stream")
	errConcurrentNext          = errors.New("anthropicadapter: concurrent stream next")

	// ErrStreamClosed is returned for every Next after Close has begun. It is
	// distinct from a provider's clean terminal event: a closed source is not
	// reusable, including for events that were already queued at Close time.
	ErrStreamClosed = errors.New("anthropicadapter: stream closed")
)

type observedAnthropicBody struct {
	io.ReadCloser
	observer *anthropicSSEObserver
}

func observingAnthropicBody(body io.ReadCloser, observer *anthropicSSEObserver) io.ReadCloser {
	return &observedAnthropicBody{ReadCloser: body, observer: observer}
}
func (b *observedAnthropicBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.observer != nil {
		if observed := b.observer.observe(p[:n]); observed != nil && err == nil {
			err = observed
		}
	}
	return n, err
}

// anthropicSSEObserver is an incremental strict SSE parser. It emits only
// canonical owned payloads and safe metadata. event/data state is local to one
// bounded frame; sends block while the queue is full, deliberately coupling
// provider reads to consumer progress.
type anthropicSSEObserver struct {
	ctx        context.Context
	events     chan sdk.StreamEvent
	done       chan struct{}
	finishOnce sync.Once
	abortOnce  sync.Once
	mu         sync.Mutex
	terminal   error
	line       []byte
	frameEvent string
	frameData  [][]byte
	frameBytes int
	frameLines int
	state      anthropicMessageState
}

func newAnthropicSSEObserver(ctx context.Context) *anthropicSSEObserver {
	return &anthropicSSEObserver{ctx: ctx, events: make(chan sdk.StreamEvent, streamQueueCapacity), done: make(chan struct{})}
}
func (o *anthropicSSEObserver) observe(input []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal != nil {
		return o.terminal
	}
	for _, b := range input {
		if b == '\n' {
			line := o.line
			o.line = nil
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if err := o.finishLine(line); err != nil {
				o.terminal = err
				return err
			}
			continue
		}
		o.line = append(o.line, b)
		if len(o.line) > maxAnthropicSSELineBytes {
			o.terminal = errAnthropicStreamProtocol
			return o.terminal
		}
	}
	return nil
}
func (o *anthropicSSEObserver) finishLine(line []byte) error {
	if len(line) == 0 {
		return o.finishFrame()
	}
	o.frameLines++
	if o.frameLines > maxAnthropicSSEFrameLines {
		return errAnthropicStreamProtocol
	}
	if line[0] == ':' {
		return nil
	}
	field, value, found := bytes.Cut(line, []byte(":"))
	if !found {
		value = nil
	}
	if len(field) == 0 || len(field) > 128 {
		return errAnthropicStreamProtocol
	}
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	switch string(field) {
	case "event":
		if o.frameEvent != "" || !knownAnthropicEvent(string(value)) {
			return errAnthropicStreamProtocol
		}
		o.frameEvent = string(value)
	case "data":
		if len(o.frameData) >= maxAnthropicSSEDataLines {
			return errAnthropicStreamProtocol
		}
		o.frameBytes += len(value)
		if o.frameBytes > maxAnthropicSSEFrameBytes {
			return errAnthropicStreamProtocol
		}
		o.frameData = append(o.frameData, append([]byte(nil), value...))
	default:
		return errAnthropicStreamProtocol
	}
	return nil
}
func (o *anthropicSSEObserver) finishFrame() error {
	defer func() { o.frameEvent = ""; o.frameData = nil; o.frameBytes = 0; o.frameLines = 0 }()
	if o.frameEvent == "" && len(o.frameData) == 0 {
		return nil
	}
	if o.frameEvent == "" || len(o.frameData) != 1 {
		return errAnthropicStreamProtocol
	}
	data := o.frameData[0]
	ev, err := o.state.parse(o.frameEvent, data)
	if err != nil {
		return err
	}
	if ev.Meta.Kind == streaming.EventNativeError {
		ev.Data = nil
	}
	select {
	case o.events <- ev:
		// Queue the event before publishing its terminal state. This preserves
		// the source contract: the consumer observes the authoritative native
		// error event before its next call observes the corresponding terminal
		// error. observe holds mu, so a concurrent Next cannot race between the
		// enqueue and this first-wins terminal publication.
		if ev.Meta.Kind == streaming.EventNativeError && o.terminal == nil {
			o.terminal = sdk.CloneClassifiedError(ev.Classified)
		}
		return nil
	case <-o.done:
		return context.Canceled
	case <-o.ctx.Done():
		return o.ctx.Err()
	}
}
func (o *anthropicSSEObserver) finish(err error) {
	o.finishOnce.Do(func() {
		o.mu.Lock()
		if o.terminal == nil {
			if err != nil {
				o.terminal = err
			} else if len(o.line) != 0 || o.frameEvent != "" || len(o.frameData) != 0 || !o.state.stopped {
				o.terminal = sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
			} else {
				o.terminal = streaming.ErrEndOfStream
			}
		}
		o.mu.Unlock()
		close(o.events)
	})
}

// hasTerminal reports whether parsing has selected a terminal result. In-band
// native errors are terminal as soon as their event has been queued, so an
// official SDK decoder error observed afterwards is non-authoritative.
func (o *anthropicSSEObserver) hasTerminal() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.terminal != nil
}

func (o *anthropicSSEObserver) abort() {
	o.abortOnce.Do(func() {
		// Close first, without taking mu: observe can be blocked sending to the
		// bounded queue while it holds mu. The closed channel releases that send.
		close(o.done)
		o.mu.Lock()
		if o.terminal == nil {
			o.terminal = streaming.ErrEndOfStream
		}
		o.mu.Unlock()
	})
}
func (o *anthropicSSEObserver) terminalError() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if errors.Is(o.terminal, streaming.ErrEndOfStream) {
		return streaming.ErrEndOfStream
	}
	var classified *sdk.ClassifiedError
	if errors.As(o.terminal, &classified) {
		return sdk.CloneClassifiedError(classified)
	}
	// Parser internals never escape this adapter boundary.
	return sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
}
func knownAnthropicEvent(v string) bool {
	switch v {
	case "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "ping", "error":
		return true
	}
	return false
}

type anthropicMessageState struct {
	started, stopped bool
	blocks           map[int64]anthropicBlock
	next             int64
	finish           string
	usage            streaming.Usage
}
type anthropicBlock struct {
	typ       string
	signature bool
}

func (s *anthropicMessageState) parse(name string, raw []byte) (sdk.StreamEvent, error) {
	root, canonical, err := strictAnthropicJSON(raw)
	if err != nil {
		return sdk.StreamEvent{}, errAnthropicStreamProtocol
	}
	if s.stopped {
		return sdk.StreamEvent{}, errAnthropicStreamProtocol
	}
	meta := streaming.Event{Kind: streaming.EventLifecycle, EventType: name}
	switch name {
	case "ping":
		if len(root) != 1 || root["type"] != "ping" {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
	case "error":
		if s.stopped || !validNativeError(root) {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		s.stopped = true
		meta.Kind = streaming.EventNativeError
		return sdk.StreamEvent{Meta: meta, Classified: classifyNativeError(root)}, nil
	case "message_start":
		if s.started || !validMessageStart(root) {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		s.started = true
		s.blocks = map[int64]anthropicBlock{}
		message := root["message"].(map[string]any)
		s.usage.PromptTokens = int64Value(message["usage"].(map[string]any)["input_tokens"])
		s.usage.TotalTokens = s.usage.PromptTokens
	case "content_block_start":
		if !s.started || len(s.blocks) != 0 || !validBlockStart(root, s.next) {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		b := root["content_block"].(map[string]any)
		s.blocks[s.next] = anthropicBlock{typ: b["type"].(string)}
		s.next++
	case "content_block_delta":
		if !s.started || !validBlockDelta(root, s.blocks) {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		d := root["delta"].(map[string]any)
		if nonEmptyDelta(d) {
			meta.Kind = streaming.EventSemantic
		}
		if d["type"] == "signature_delta" {
			index := int64Value(root["index"])
			b := s.blocks[index]
			b.signature = true
			s.blocks[index] = b
		}
	case "content_block_stop":
		index := int64Value(root["index"])
		if !s.started || !validOnly(root, "type", "index") || root["type"] != "content_block_stop" || index < 0 || index != s.next-1 {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		if _, active := s.blocks[index]; !active {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		delete(s.blocks, index)
	case "message_delta":
		if !s.started || len(s.blocks) != 0 || !validMessageDelta(root) {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		d := root["delta"].(map[string]any)
		s.finish = d["stop_reason"].(string)
		u := root["usage"].(map[string]any)
		s.usage = mergeAnthropicUsage(s.usage, usageFrom(u))
		s.usage.TotalTokens = s.usage.PromptTokens + s.usage.CompletionTokens
		meta.Kind = streaming.EventUsage
		meta.Usage = &s.usage
	case "message_stop":
		if !s.started || !validOnly(root, "type") || root["type"] != "message_stop" || !validFinish(s.finish) {
			return sdk.StreamEvent{}, errAnthropicStreamProtocol
		}
		s.stopped = true
		meta.Kind = streaming.EventFinish
		meta.FinishReason = s.finish
	default:
		return sdk.StreamEvent{}, errAnthropicStreamProtocol
	}
	return sdk.StreamEvent{Meta: meta, Data: append(json.RawMessage(nil), canonical...)}, nil
}
