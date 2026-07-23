package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/protocolconvert"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// convertingSink wraps a ProtocolSink and converts each stream event's Data
// payload from the target (upstream) protocol to the request (inbound)
// protocol. It is used only when the request protocol differs from the target
// route protocol. Same-protocol streams use the inner sink directly with zero
// overhead.
//
// A single upstream chunk may produce zero or more converted chunks. The sink
// buffers pending chunks and flushes them before the next real event.
//
// When one source event produces multiple output events, each output event
// must have a strictly increasing Sequence (the downstream SSE sink validates
// this). The convertingSink assigns its own monotonic Sequence counter to
// all output events: nextSeq starts at 1 and increments for each emitted
// event. The source Sequence is no longer preserved in output events because
// a one-to-many conversion can cause duplicate Sequences that downstream
// validators reject.
type convertingSink struct {
	inner         ProtocolSink
	fromProtocol  adapter.Protocol // target/upstream protocol
	toProtocol    adapter.Protocol // request/inbound protocol
	state         protocolconvert.StreamState
	convertFailed bool
	pending       []sdk.StreamEvent // buffered converted chunks awaiting delivery
	nextSeq       uint64            // monotonic output Sequence counter
}

// newConvertingSink returns a sink that converts stream event payloads from
// fromProtocol to toProtocol. When fromProtocol == toProtocol, it returns the
// inner sink directly (zero overhead).
func newConvertingSink(inner ProtocolSink, fromProtocol, toProtocol adapter.Protocol) ProtocolSink {
	if fromProtocol == toProtocol {
		return inner
	}
	return &convertingSink{
		inner:        inner,
		fromProtocol: fromProtocol,
		toProtocol:   toProtocol,
	}
}

// Commit converts each event payload and delegates to the inner sink.
func (s *convertingSink) Commit(ctx context.Context, events []sdk.StreamEvent) error {
	if s.convertFailed {
		return ErrProtocolConvert
	}
	if err := s.flushPending(ctx); err != nil {
		return err
	}
	converted, err := s.convertEvents(events)
	if err != nil {
		s.convertFailed = true
		return ErrProtocolConvert
	}
	return s.inner.Commit(ctx, converted)
}

// WriteEvent converts the event payload and delegates to the inner sink.
// Pending chunks from a prior multi-chunk conversion are flushed first.
func (s *convertingSink) WriteEvent(ctx context.Context, event sdk.StreamEvent) error {
	if s.convertFailed {
		return ErrProtocolConvert
	}
	if err := s.flushPending(ctx); err != nil {
		return err
	}
	converted, err := s.convertEvent(event)
	if err != nil {
		s.convertFailed = true
		return ErrProtocolConvert
	}
	// convertEvent may have produced pending chunks; flush them after the
	// primary event.
	if err := s.inner.WriteEvent(ctx, converted); err != nil {
		return err
	}
	return s.flushPending(ctx)
}

// Flush delegates to the inner sink after flushing any pending converted chunks.
func (s *convertingSink) Flush(ctx context.Context) error {
	if err := s.flushPending(ctx); err != nil {
		return err
	}
	return s.inner.Flush(ctx)
}

func (s *convertingSink) flushPending(ctx context.Context) error {
	for len(s.pending) > 0 {
		event := s.pending[0]
		s.pending = s.pending[1:]
		if err := s.inner.WriteEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (s *convertingSink) convertEvents(events []sdk.StreamEvent) ([]sdk.StreamEvent, error) {
	var out []sdk.StreamEvent
	for _, event := range events {
		converted, err := s.convertEvent(event)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
		out = append(out, s.pending...)
		s.pending = nil
	}
	return out, nil
}

func (s *convertingSink) convertEvent(event sdk.StreamEvent) (sdk.StreamEvent, error) {
	// NativeError events have no Data payload; pass through with monotonic Sequence.
	if event.Classified != nil || len(event.Data) == 0 {
		s.nextSeq++
		event.Sequence = s.nextSeq
		event.Meta.Sequence = s.nextSeq
		return event, nil
	}
	chunks, err := protocolconvert.ConvertStreamChunk(event.Data, s.fromProtocol, s.toProtocol, &s.state)
	if err != nil {
		if errors.Is(err, protocolconvert.ErrUnsupportedConversion) || errors.Is(err, protocolconvert.ErrInvalidStreamChunk) {
			return sdk.StreamEvent{}, err
		}
		return sdk.StreamEvent{}, err
	}
	if len(chunks) == 0 {
		// No output for this input chunk (e.g., role announcement). Return a
		// zero-Data event that the inner sink can skip. Assign a monotonic
		// Sequence for consistency.
		s.nextSeq++
		event.Sequence = s.nextSeq
		event.Meta.Sequence = s.nextSeq
		event.Data = nil
		event.Meta.EventType = ""
		event.Meta.Kind = streaming.EventLifecycle
		return event, nil
	}
	// First chunk replaces the current event's Data and updates Meta.
	event.Data = json.RawMessage(chunks[0])
	event.Meta = updateMetaFromConverted(event.Meta, chunks[0], s.toProtocol)
	// Assign monotonic Sequence to the primary output event.
	s.nextSeq++
	event.Sequence = s.nextSeq
	event.Meta.Sequence = s.nextSeq
	// Remaining chunks are staged as pending events with updated Meta and
	// monotonic Sequences. Each pending event gets the next value from the
	// counter, ensuring all output events have unique, strictly increasing
	// Sequences.
	for _, chunk := range chunks[1:] {
		extra := event
		extra.Data = json.RawMessage(chunk)
		extra.Meta = updateMetaFromConverted(event.Meta, chunk, s.toProtocol)
		s.nextSeq++
		extra.Sequence = s.nextSeq
		extra.Meta.Sequence = s.nextSeq
		s.pending = append(s.pending, extra)
	}
	return event, nil
}

// maxMetaTypeBytes bounds the extracted type/object field length to prevent
// unbounded content from reaching downstream metadata.
const maxMetaTypeBytes = 128

// updateMetaFromConverted updates the Meta EventType and Kind from the
// converted JSON payload. The caller is responsible for setting the Sequence
// on both the StreamEvent and its Meta after this call.
//
// For Anthropic-bound events, it extracts the "type" field (e.g.
// "message_start", "content_block_delta", "message_stop").
// For OpenAI-bound events, it extracts the "object" field (e.g.
// "chat.completion.chunk").
//
// Terminal events (message_stop, or chat.completion.chunk with
// finish_reason) are classified as EventFinish; others retain their
// original Kind classification unless the event type demands a specific
// Kind (e.g. message_delta carries usage → EventUsage).
func updateMetaFromConverted(meta streaming.Event, converted []byte, toProtocol adapter.Protocol) streaming.Event {
	out := meta
	out.EventType = extractEventTypeFromJSON(converted, toProtocol)
	out.Kind = classifyConvertedKind(out.EventType, converted, toProtocol)
	if out.Kind == streaming.EventFinish {
		out.FinishReason = extractFinishReason(converted, toProtocol)
	} else {
		out.FinishReason = ""
	}
	return out
}

// extractEventTypeFromJSON parses the converted JSON to extract the event
// type identifier. For Anthropic-bound events this is the "type" field;
// for OpenAI-bound events this is the "object" field. The result is bounded
// and sanitized to the [A-Za-z0-9_.-] subset.
func extractEventTypeFromJSON(data []byte, toProtocol adapter.Protocol) string {
	if len(data) == 0 || len(data) > sdk.MaxStreamEventDataBytes {
		return ""
	}
	var raw struct {
		Type   string `json:"type"`
		Object string `json:"object"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}
	var v string
	if toProtocol == adapter.ProtocolAnthropic {
		v = raw.Type
	} else {
		v = raw.Object
	}
	if len(v) == 0 || len(v) > maxMetaTypeBytes {
		return ""
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			continue
		case r == '_' || r == '-' || r == '.':
			continue
		default:
			return ""
		}
	}
	return v
}

// classifyConvertedKind determines the correct EventKind for a converted
// event based on its EventType and the converted JSON content.
func classifyConvertedKind(eventType string, converted []byte, toProtocol adapter.Protocol) streaming.EventKind {
	if toProtocol == adapter.ProtocolAnthropic {
		switch eventType {
		case "message_stop":
			return streaming.EventFinish
		case "message_delta":
			return streaming.EventUsage
		case "content_block_delta":
			return streaming.EventSemantic
		case "message_start", "content_block_start", "content_block_stop", "ping":
			return streaming.EventLifecycle
		default:
			return streaming.EventLifecycle
		}
	}
	// OpenAI-bound: chat.completion.chunk
	if eventType == "chat.completion.chunk" {
		// Check for finish_reason in the converted chunk
		var raw struct {
			Choices []struct {
				FinishReason any `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal(converted, &raw) == nil && len(raw.Choices) > 0 {
			fr := raw.Choices[0].FinishReason
			if fr != nil {
				if s, ok := fr.(string); ok && s != "" {
					return streaming.EventFinish
				}
			}
		}
		// Check for usage-only chunk
		var rawUsage struct {
			Usage any `json:"usage"`
		}
		if json.Unmarshal(converted, &rawUsage) == nil && rawUsage.Usage != nil {
			// If it has usage but no choices with content, it's a usage event
			if len(raw.Choices) == 0 {
				return streaming.EventUsage
			}
		}
		return streaming.EventSemantic
	}
	return streaming.EventLifecycle
}

// extractFinishReason extracts the finish reason from a converted terminal
// event. For Anthropic message_stop it comes from the StreamState (already
// mapped); for OpenAI chat.completion.chunk it comes from choices[0].finish_reason.
func extractFinishReason(converted []byte, toProtocol adapter.Protocol) string {
	if toProtocol == adapter.ProtocolAnthropic {
		// Anthropic message_stop does not carry finish_reason in the JSON;
		// it was already mapped during conversion. The finish reason is
		// carried in the message_delta event, not message_stop. For message_stop
		// we use the stop_reason from the prior message_delta which the
		// protocolconvert already mapped. We extract it from the message_delta
		// if this is one, otherwise return empty (the Kind is already EventFinish).
		var raw struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
		}
		if json.Unmarshal(converted, &raw) == nil && raw.Delta.StopReason != "" {
			return sanitizeMetaToken(raw.Delta.StopReason)
		}
		return ""
	}
	// OpenAI
	var raw struct {
		Choices []struct {
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(converted, &raw) == nil && len(raw.Choices) > 0 && raw.Choices[0].FinishReason != nil {
		return sanitizeMetaToken(*raw.Choices[0].FinishReason)
	}
	return ""
}

// sanitizeMetaToken reduces a token to the safe [A-Za-z0-9_.-] subset,
// bounded in length. Mirrors streaming.sanitizeToken.
func sanitizeMetaToken(v string) string {
	if len(v) == 0 || len(v) > maxMetaTypeBytes {
		return ""
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			continue
		case r == '_' || r == '-' || r == '.':
			continue
		default:
			return ""
		}
	}
	return v
}
