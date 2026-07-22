package openaiadapter

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

const (
	maxObservedSSELineBytes  = 256 << 10
	maxObservedSSEFrameBytes = 1 << 20
	maxObservedSSEFrameLines = 4096
	maxObservedSSEDataLines  = 1024
	maxObservedSSEFieldBytes = 128
)

var (
	errObservedSSEProtocol = errors.New("openaiadapter: invalid sse stream")
	errConcurrentNext      = errors.New("openaiadapter: concurrent stream next")
)

// sseObserver is a passive bounded SSE framing observer. It never retains raw
// lines, frames, or data values: its byte-level state machine only recognizes
// an exact, single data: [DONE] frame.
type sseObserver struct {
	mu sync.Mutex

	lineBytes  int
	pendingCR  bool
	lineState  sseLineState
	fieldBytes int
	fieldMatch int
	dataMatch  int
	dataBytes  int

	frameLines         int
	frameDataLines     int
	frameDataBytes     int
	frameDoneCandidate bool
	sawDONE            bool
	terminal           error
}

type sseLineState uint8

const (
	sseLineStart sseLineState = iota
	sseLineComment
	sseLineField
	sseLineValueSpace
	sseLineDataValue
	sseLineOtherValue
)

const doneMarker = "[DONE]"

func observingBody(rc io.ReadCloser, observer *sseObserver) io.ReadCloser {
	return &observedBody{ReadCloser: rc, observer: observer}
}

type observedBody struct {
	io.ReadCloser
	observer *sseObserver
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if observeErr := b.observer.observe(p[:n]); observeErr != nil && err == nil {
			err = observeErr
		}
	}
	return n, err
}

func (o *sseObserver) observe(data []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal != nil {
		return o.terminal
	}
	for _, b := range data {
		if o.pendingCR {
			o.pendingCR = false
			if b == '\n' {
				o.finishLine()
				if o.terminal != nil {
					return o.terminal
				}
				continue
			}
			o.consumeByte('\r')
			if o.terminal != nil {
				return o.terminal
			}
		}
		if b == '\r' {
			// Defer CR so CRLF is one line ending while a standalone CR remains
			// ordinary data. No byte is retained in either case.
			o.pendingCR = true
			continue
		}
		if b == '\n' {
			o.finishLine()
			if o.terminal != nil {
				return o.terminal
			}
			continue
		}
		o.consumeByte(b)
		if o.terminal != nil {
			return o.terminal
		}
	}
	return nil
}

func (o *sseObserver) consumeByte(b byte) {
	o.lineBytes++
	if o.lineBytes > maxObservedSSELineBytes {
		o.terminal = errObservedSSEProtocol
		return
	}
	switch o.lineState {
	case sseLineStart:
		if b == ':' {
			o.lineState = sseLineComment
			return
		}
		if o.sawDONE {
			o.terminal = errObservedSSEProtocol
			return
		}
		o.lineState = sseLineField
		o.consumeFieldByte(b)
	case sseLineField:
		if b == ':' {
			o.beginValue()
			return
		}
		o.consumeFieldByte(b)
	case sseLineValueSpace:
		if b == ' ' {
			o.lineState = o.valueState()
			return
		}
		o.lineState = o.valueState()
		o.consumeValueByte(b)
	case sseLineDataValue, sseLineOtherValue:
		o.consumeValueByte(b)
	}
}

func (o *sseObserver) consumeFieldByte(b byte) {
	o.fieldBytes++
	if o.fieldBytes > maxObservedSSEFieldBytes {
		o.terminal = errObservedSSEProtocol
		return
	}
	if o.fieldMatch >= 0 && o.fieldMatch < len("data") && b == "data"[o.fieldMatch] {
		o.fieldMatch++
	} else {
		o.fieldMatch = -1
	}
}

func (o *sseObserver) isDataField() bool {
	return o.fieldBytes == len("data") && o.fieldMatch == len("data")
}

func (o *sseObserver) beginValue() {
	if o.isDataField() {
		if o.sawDONE {
			o.terminal = errObservedSSEProtocol
			return
		}
		o.frameDataLines++
		if o.frameDataLines > maxObservedSSEDataLines {
			o.terminal = errObservedSSEProtocol
			return
		}
		o.dataMatch = 0
		o.dataBytes = 0
		o.lineState = sseLineValueSpace
		return
	}
	o.lineState = sseLineOtherValue
}

func (o *sseObserver) valueState() sseLineState {
	if o.isDataField() {
		return sseLineDataValue
	}
	return sseLineOtherValue
}

func (o *sseObserver) consumeValueByte(b byte) {
	if o.lineState != sseLineDataValue {
		return
	}
	o.dataBytes++
	o.frameDataBytes++
	if o.frameDataBytes > maxObservedSSEFrameBytes {
		o.terminal = errObservedSSEProtocol
		return
	}
	if o.dataMatch >= 0 && o.dataMatch < len(doneMarker) && b == doneMarker[o.dataMatch] {
		o.dataMatch++
	} else {
		o.dataMatch = -1
	}
}

func (o *sseObserver) finishLine() {
	if o.lineBytes == 0 { // Empty line: the SSE frame boundary.
		o.finishFrame()
		o.resetLine()
		return
	}
	o.frameLines++
	if o.frameLines > maxObservedSSEFrameLines {
		o.terminal = errObservedSSEProtocol
		return
	}
	// A field without ':' has an empty value under the SSE standard.
	if o.lineState == sseLineField {
		o.beginValue()
		if o.terminal != nil {
			return
		}
	}
	if o.lineState == sseLineDataValue && o.dataBytes == len(doneMarker) && o.dataMatch == len(doneMarker) {
		o.frameDoneCandidate = true
	}
	o.resetLine()
}

func (o *sseObserver) finishFrame() {
	if o.frameDataLines == 1 && o.frameDoneCandidate {
		if o.sawDONE {
			o.terminal = errObservedSSEProtocol
		} else {
			o.sawDONE = true
		}
	}
	o.frameLines = 0
	o.frameDataLines = 0
	o.frameDataBytes = 0
	o.frameDoneCandidate = false
}

func (o *sseObserver) resetLine() {
	o.lineBytes = 0
	o.pendingCR = false
	o.lineState = sseLineStart
	o.fieldBytes = 0
	o.fieldMatch = 0
	o.dataMatch = 0
	o.dataBytes = 0
}

func (o *sseObserver) terminalErr() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.terminal
}

func (o *sseObserver) cleanEOF() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sawDONE && o.terminal == nil
}

// chunkSource owns both the SDK stream and the derived opening context. Next
// is serial; Close is idempotent, cancels the opening context, and closes the
// SDK stream to release an in-flight read.
type chunkSource struct {
	stream   *ssestream.Stream[openai.ChatCompletionChunk]
	cancel   context.CancelFunc
	observer *sseObserver

	mu       sync.Mutex
	closed   bool
	terminal error
	sequence uint64
	nextMu   sync.Mutex
}

func newChunkSource(stream *ssestream.Stream[openai.ChatCompletionChunk], options ...any) *chunkSource {
	var cancel context.CancelFunc
	var observer *sseObserver
	for _, option := range options {
		switch value := option.(type) {
		case context.CancelFunc:
			cancel = value
		case *sseObserver:
			observer = value
		}
	}
	return &chunkSource{stream: stream, cancel: cancel, observer: observer}
}

func (s *chunkSource) Next(ctx context.Context) (sdk.StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return sdk.StreamEvent{}, err
	}
	if !s.nextMu.TryLock() {
		return sdk.StreamEvent{}, errConcurrentNext
	}
	defer s.nextMu.Unlock()
	if err := s.getTerminal(); err != nil {
		return sdk.StreamEvent{}, err
	}
	if s.isClosed() {
		return sdk.StreamEvent{}, streaming.ErrEndOfStream
	}

	result := make(chan struct {
		ok  bool
		pan any
	}, 1)
	go func() {
		var r struct {
			ok  bool
			pan any
		}
		func() {
			defer func() { r.pan = recover() }()
			if s.stream != nil {
				r.ok = s.stream.Next()
			}
		}()
		result <- r
	}()
	select {
	case r := <-result:
		if r.pan != nil || !r.ok {
			err := s.classifyTerminal(ctx)
			s.setTerminal(err)
			return sdk.StreamEvent{}, err
		}
		ev, data, err := parseChunk([]byte(s.stream.Current().RawJSON()))
		if err != nil {
			safe := sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
			s.setTerminal(safe)
			return sdk.StreamEvent{}, safe
		}
		s.mu.Lock()
		s.sequence++
		sequence := s.sequence
		s.mu.Unlock()
		return sdk.StreamEvent{Sequence: sequence, Meta: ev, Data: data}, nil
	case <-ctx.Done():
		_ = s.Close()
		return sdk.StreamEvent{}, ctx.Err()
	}
}
func (s *chunkSource) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel, stream := s.cancel, s.stream
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stream != nil {
		_ = stream.Close()
	}
	return nil
}

func (s *chunkSource) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
func (s *chunkSource) getTerminal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal
}
func (s *chunkSource) setTerminal(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal == nil {
		s.terminal = err
	}
}

func (s *chunkSource) classifyTerminal(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.observer != nil && s.observer.terminalErr() != nil {
		return sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
	}
	if s.stream != nil && s.stream.Err() != nil {
		if errors.Is(s.stream.Err(), context.Canceled) {
			return context.Canceled
		}
		if errors.Is(s.stream.Err(), context.DeadlineExceeded) {
			return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
		}
		return sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	if s.observer == nil || s.observer.cleanEOF() {
		return streaming.ErrEndOfStream
	}
	return sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", "")
}
