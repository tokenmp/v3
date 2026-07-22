package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// ProtocolSink renders owned canonical provider payloads. It is deliberately
// format-agnostic: protocol-native SSE framing belongs to a later transport
// adapter, not to the streaming lifecycle core.
type ProtocolSink interface {
	Commit(context.Context, []sdk.StreamEvent) error
	WriteEvent(context.Context, sdk.StreamEvent) error
	Flush(context.Context) error
}

// Safe, fixed errors for the payload boundary. None includes provider payload,
// provider metadata, URL, request content, or credentials.
var (
	ErrStreamPayloadMisconfigured = errors.New("execution: stream payload misconfigured")
	ErrStreamPayloadProtocol      = errors.New("execution: stream payload protocol")
	ErrStreamPayloadBuffer        = errors.New("execution: stream payload buffer overflow")
	ErrStreamPayloadStaged        = errors.New("execution: stream payload flush pending")
)

const (
	// A Bridge pre-commit buffer holds at most 32 lifecycle events, followed by
	// its committing semantic event; one post-commit event may be staged for
	// Flush. The payload side therefore retains at most 35 events.
	maxPendingPayloadEvents = streaming.MaxBufferedLifecycle + 3
	maxPendingPayloadBytes  = maxPendingPayloadEvents * sdk.MaxStreamEventDataBytes
)

// sdkPayloadSource adapts a semantic SDK stream to the metadata-only
// streaming.Source and retains a bounded, owned payload copy until its exact
// sequence has been durably handed to a ProtocolSink.
type sdkPayloadSource struct {
	source sdk.StreamSource

	mu             sync.Mutex
	closed         bool
	nextInProgress bool
	lastSequence   uint64
	pending        map[uint64]sdk.StreamEvent
	order          []uint64
	pendingBytes   int
	lastClassified *sdk.ClassifiedError
}

// newSDKPayloadSource returns a source and its matching sink adapter. They
// share the sequence-indexed owned payload store and must be used for one
// stream only.
func newSDKPayloadSource(source sdk.StreamSource, sink ProtocolSink) (*sdkPayloadSource, *streamPayloadSink, error) {
	if isNilStreamPayloadInterface(source) || isNilStreamPayloadInterface(sink) {
		return nil, nil, ErrStreamPayloadMisconfigured
	}
	payloads := &sdkPayloadSource{source: source, pending: make(map[uint64]sdk.StreamEvent)}
	return payloads, &streamPayloadSink{source: payloads, sink: sink}, nil
}

// Next obtains one SDK event, validates the stream-to-core invariants before
// it can reach Bridge, and stores an owned payload copy keyed by its sequence.
func (s *sdkPayloadSource) Next(ctx context.Context) (streaming.Event, error) {
	if s == nil || ctx == nil {
		return streaming.Event{}, ErrStreamPayloadMisconfigured
	}
	if err := ctx.Err(); err != nil {
		return streaming.Event{}, err
	}
	s.mu.Lock()
	if s.closed || s.nextInProgress {
		s.mu.Unlock()
		return streaming.Event{}, ErrStreamPayloadMisconfigured
	}
	s.nextInProgress = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.nextInProgress = false
		s.mu.Unlock()
	}()

	event, err := s.source.Next(ctx)
	if err != nil {
		return streaming.Event{}, s.safeNextError(err)
	}
	if err := ctx.Err(); err != nil {
		return streaming.Event{}, err
	}
	owned, err := validateAndCopyStreamEvent(event)
	if err != nil {
		return streaming.Event{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || owned.Sequence == 0 || owned.Sequence <= s.lastSequence {
		return streaming.Event{}, ErrStreamPayloadProtocol
	}
	if len(s.pending) >= maxPendingPayloadEvents || s.pendingBytes+len(owned.Data) > maxPendingPayloadBytes {
		return streaming.Event{}, ErrStreamPayloadBuffer
	}
	s.lastSequence = owned.Sequence
	// Native errors are safe Bridge-only terminal metadata. They have no
	// renderer payload and Bridge never asks the paired payload sink to resolve
	// them, so do not consume pending-store capacity for this sequence.
	if owned.Meta.Kind == streaming.EventNativeError {
		s.lastClassified = sdk.CloneClassifiedError(owned.Classified)
		return cloneStreamMeta(owned.Meta), nil
	}
	s.pending[owned.Sequence] = owned
	s.order = append(s.order, owned.Sequence)
	s.pendingBytes += len(owned.Data)
	return cloneStreamMeta(owned.Meta), nil
}

// Close is idempotent and delegates exactly once. It deliberately does not
// discard pending bytes: a driver defers the paired sink's Discard after
// Bridge.Run has completed its final downstream operation.
func (s *sdkPayloadSource) Close() error {
	if s == nil {
		return ErrStreamPayloadMisconfigured
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.source.Close()
}

// LastClassified returns an independent safe copy of the latest classified
// SDK Next failure. It never retains or returns arbitrary upstream errors.
func (s *sdkPayloadSource) LastClassified() *sdk.ClassifiedError {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneClassified(s.lastClassified)
}

func (s *sdkPayloadSource) safeNextError(err error) error {
	if errors.Is(err, streaming.ErrEndOfStream) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var classified *sdk.ClassifiedError
	if errors.As(err, &classified) && classified != nil {
		s.mu.Lock()
		s.lastClassified = cloneClassified(classified)
		s.mu.Unlock()
		return cloneClassified(classified)
	}
	return ErrStreamPayloadProtocol
}

// streamPayloadSink resolves Bridge metadata back to the exact sequence-owned
// SDK payload. A failed downstream call leaves payloads retained, because the
// downstream outcome is uncertain and this adapter never retries it.
type streamPayloadSink struct {
	source *sdkPayloadSource
	sink   ProtocolSink

	mu     sync.Mutex
	staged uint64
}

func (s *streamPayloadSink) Commit(ctx context.Context, events []streaming.Event) error {
	if s == nil || ctx == nil || s.source == nil || isNilStreamPayloadInterface(s.sink) {
		return ErrStreamPayloadMisconfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staged != 0 {
		return ErrStreamPayloadStaged
	}
	payloads, err := s.source.resolve(events)
	if err != nil {
		return err
	}
	if err := s.sink.Commit(ctx, payloads); err != nil {
		return err
	}
	s.source.delete(events)
	return nil
}

func (s *streamPayloadSink) WriteEvent(ctx context.Context, event streaming.Event) error {
	if s == nil || ctx == nil || s.source == nil || isNilStreamPayloadInterface(s.sink) {
		return ErrStreamPayloadMisconfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staged != 0 {
		return ErrStreamPayloadStaged
	}
	payloads, err := s.source.resolve([]streaming.Event{event})
	if err != nil {
		return err
	}
	if err := s.sink.WriteEvent(ctx, payloads[0]); err != nil {
		return err
	}
	s.staged = event.Sequence
	return nil
}

func (s *streamPayloadSink) Flush(ctx context.Context) error {
	if s == nil || ctx == nil || s.source == nil || isNilStreamPayloadInterface(s.sink) {
		return ErrStreamPayloadMisconfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staged == 0 {
		return ErrStreamPayloadMisconfigured
	}
	if err := s.sink.Flush(ctx); err != nil {
		return err
	}
	s.source.delete([]streaming.Event{{Sequence: s.staged}})
	s.staged = 0
	return nil
}

// Discard releases all retained canonical payload bytes. It is idempotent and
// must be deferred by the future StreamDriver after Bridge.Run returns.
func (s *streamPayloadSink) Discard() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.staged = 0
	s.mu.Unlock()
	s.source.discard()
}

func (s *sdkPayloadSource) resolve(events []streaming.Event) ([]sdk.StreamEvent, error) {
	if len(events) == 0 {
		return nil, ErrStreamPayloadProtocol
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payloads := make([]sdk.StreamEvent, 0, len(events))
	for _, event := range events {
		payload, ok := s.pending[event.Sequence]
		if !ok || !sameStreamMeta(payload.Meta, event) {
			return nil, ErrStreamPayloadProtocol
		}
		payloads = append(payloads, cloneStreamEvent(payload))
	}
	return payloads, nil
}

func (s *sdkPayloadSource) delete(events []streaming.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range events {
		if payload, ok := s.pending[event.Sequence]; ok {
			clear(payload.Data)
			s.pendingBytes -= len(payload.Data)
			delete(s.pending, event.Sequence)
		}
	}
	if len(s.order) > 0 {
		kept := s.order[:0]
		for _, sequence := range s.order {
			if _, ok := s.pending[sequence]; ok {
				kept = append(kept, sequence)
			}
		}
		s.order = kept
	}
}

func (s *sdkPayloadSource) discard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sequence, event := range s.pending {
		clear(event.Data)
		delete(s.pending, sequence)
	}
	s.order = nil
	s.pendingBytes = 0
}

func validateAndCopyStreamEvent(event sdk.StreamEvent) (sdk.StreamEvent, error) {
	if event.Sequence == 0 || event.Meta.Sequence != event.Sequence || !validPayloadKind(event.Meta.Kind) {
		return sdk.StreamEvent{}, ErrStreamPayloadProtocol
	}
	if event.Meta.Kind == streaming.EventNativeError {
		if event.Data != nil || event.Classified == nil {
			return sdk.StreamEvent{}, ErrStreamPayloadProtocol
		}
	} else if event.Classified != nil || len(event.Data) == 0 || len(event.Data) > sdk.MaxStreamEventDataBytes || !json.Valid(event.Data) {
		return sdk.StreamEvent{}, ErrStreamPayloadProtocol
	}
	if len(event.Data) > sdk.MaxStreamEventDataBytes {
		return sdk.StreamEvent{}, ErrStreamPayloadProtocol
	}
	if event.Data != nil {
		var compact bytes.Buffer
		if err := json.Compact(&compact, event.Data); err != nil || !bytes.Equal(compact.Bytes(), event.Data) {
			return sdk.StreamEvent{}, ErrStreamPayloadProtocol
		}
	}
	return cloneStreamEvent(event), nil
}

func validPayloadKind(kind streaming.EventKind) bool {
	switch kind {
	case streaming.EventLifecycle, streaming.EventSemantic, streaming.EventUsage, streaming.EventFinish, streaming.EventNativeError:
		return true
	default:
		return false
	}
}

func cloneStreamEvent(event sdk.StreamEvent) sdk.StreamEvent { return sdk.CloneStreamEvent(event) }

func cloneStreamMeta(event streaming.Event) streaming.Event {
	if event.Progress != nil {
		value := *event.Progress
		event.Progress = &value
	}
	if event.Usage != nil {
		value := *event.Usage
		event.Usage = &value
	}
	return event
}

func sameStreamMeta(left, right streaming.Event) bool {
	return left.Sequence == right.Sequence && left.Kind == right.Kind && left.EventType == right.EventType &&
		left.FinishReason == right.FinishReason && sameProgress(left.Progress, right.Progress) && sameUsage(left.Usage, right.Usage)
}
func sameProgress(left, right *streaming.Progress) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && left.Processed == right.Processed)
}
func sameUsage(left, right *streaming.Usage) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func cloneClassified(value *sdk.ClassifiedError) *sdk.ClassifiedError {
	return sdk.CloneClassifiedError(value)
}

func isNilStreamPayloadInterface(value any) bool {
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
