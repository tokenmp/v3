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
	fromProtocol  adapter.Protocol  // target/upstream protocol
	toProtocol    adapter.Protocol  // request/inbound protocol
	toolNameMap   map[string]string // per-request tool name restoration map
	state         protocolconvert.StreamState
	convertFailed bool
	finalized     bool              // exactly-once: terminal already emitted (converted finish or Finalize)
	pending       []sdk.StreamEvent // buffered converted chunks awaiting delivery
	nextSeq       uint64            // monotonic output Sequence counter
}

// newConvertingSink returns a sink that converts stream event payloads from
// fromProtocol to toProtocol. When fromProtocol == toProtocol, it returns the
// inner sink directly (zero overhead).
//
// The optional nameMap carries sanitized→original tool name mappings for
// Anthropic→OpenAI conversion. It is per-request and scoped to the current
// attempt. When non-nil, converted stream chunks have their tool names
// restored to original values. A nil map is a no-op (zero overhead).
func newConvertingSink(inner ProtocolSink, fromProtocol, toProtocol adapter.Protocol, nameMap map[string]string) ProtocolSink {
	if fromProtocol == toProtocol {
		return inner
	}
	return &convertingSink{
		inner:        inner,
		fromProtocol: fromProtocol,
		toProtocol:   toProtocol,
		toolNameMap:  nameMap,
	}
}

// streamFinalizer is the optional terminal-synthesis capability a converting
// ProtocolSink may implement. Finalize is invoked exactly once by the Bridge
// on a committed clean EOF (via streamPayloadSink delegation) to synthesize
// and write any protocol-native terminal output directly to the downstream,
// then return sanitized terminal metadata. A plain same-protocol ProtocolSink
// does not implement it, so its absence leaves the Bridge Finalizer nil and
// preserves the legacy committed-EOF-is-truncated contract. It carries no
// raw bytes across the boundary: the returned metadata is sanitized.
type streamFinalizer interface {
	Finalize(ctx context.Context) (streaming.TerminalMeta, error)
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

// Finalize synthesizes the protocol-native terminal stream event(s) for a
// converted stream that ended cleanly without an explicit terminal, writes
// them directly to the inner sink, flushes, and returns sanitized terminal
// metadata. It is driven by the per-stream protocolconvert.StreamState
// accumulated across convertEvent calls. It never returns raw bytes across
// the boundary: the returned TerminalMeta carries only a bounded Finish token
// and optional bounded Usage counters.
//
// It is exactly-once: the finalized flag is set before synthesis so a second
// call (defensive or from a race) is a no-op returning empty metadata. A
// converted terminal chunk (message_stop / chat.completion.chunk with
// finish_reason) also sets the flag via convertEvent, so a Finalize after an
// explicit finish correctly synthesizes nothing.
//
// Flush of any remaining pending converted chunks happens first so the
// synthesized terminal is emitted after all converted content. A conversion
// failure (already recorded) or a pending-flush/inner-write error is returned
// as a non-context error; the streaming Bridge treats it as a post-commit
// sink failure (no retry).
func (s *convertingSink) Finalize(ctx context.Context) (streaming.TerminalMeta, error) {
	if s.convertFailed {
		return streaming.TerminalMeta{}, ErrProtocolConvert
	}
	if s.finalized {
		return streaming.TerminalMeta{}, nil
	}
	// Flush any buffered converted chunks before the terminal so it is emitted
	// after all converted content.
	if err := s.flushPending(ctx); err != nil {
		return streaming.TerminalMeta{}, err
	}
	payloads, err := protocolconvert.FinalizeStream(s.fromProtocol, s.toProtocol, &s.state)
	if err != nil {
		s.convertFailed = true
		return streaming.TerminalMeta{}, ErrProtocolConvert
	}
	// Set exactly-once before writing so a write failure does not allow a
	// retry to synthesize a second terminal. The synthesized output may be
	// partially written; the Bridge does not retry (downstream uncertain).
	s.finalized = true
	var finish string
	for _, payload := range payloads {
		event := s.buildFinalEvent(payload)
		if err := s.inner.WriteEvent(ctx, event); err != nil {
			return streaming.TerminalMeta{}, err
		}
		if err := s.inner.Flush(ctx); err != nil {
			return streaming.TerminalMeta{}, err
		}
		// The terminal payload carries the finish reason in the
		// request-protocol shape; extract it directly from the synthesized
		// bytes rather than re-deriving it from converter state, so the
		// sanitized Finish token matches what the downstream actually received.
		if fr := extractFinishReason(payload, s.toProtocol); fr != "" {
			finish = fr
		}
	}
	return s.terminalMetaFromState(finish), nil
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
	// Exactly-once terminal guard: once a terminal has been emitted (a
	// converted finish chunk or a prior Finalize), any subsequent chunk —
	// including a late usage chunk arriving after an explicit finish — must
	// do nothing and must NOT re-emit message_start or a second finish. It is
	// dropped as a no-op lifecycle event carrying a fresh monotonic Sequence.
	if s.finalized {
		return s.dropChunk(event), nil
	}
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
	restored := s.restoreChunkToolNames(chunks[0])
	event.Data = json.RawMessage(restored)
	event.Meta = updateMetaFromConverted(event.Meta, restored, s.toProtocol)
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
		restoredChunk := s.restoreChunkToolNames(chunk)
		extra.Data = json.RawMessage(restoredChunk)
		extra.Meta = updateMetaFromConverted(event.Meta, restoredChunk, s.toProtocol)
		s.nextSeq++
		extra.Sequence = s.nextSeq
		extra.Meta.Sequence = s.nextSeq
		s.pending = append(s.pending, extra)
	}
	// Detect a converted terminal (message_stop / chat.completion.chunk with
	// finish_reason): the converter closes the converted message, so the
	// per-stream state's started flag flips false. Set the exactly-once flag so
	// any later chunk does nothing.
	if s.terminalClosed() {
		s.finalized = true
	}
	return event, nil
}

// terminalClosed reports whether the converter state has just closed the
// converted message (a converted message_stop or a chat.completion.chunk with
// finish_reason). After such a conversion, both protocolconvert's per-stream
// StreamState started flags are false. A non-terminal conversion leaves them
// true (or already-false-from-prior-terminal, covered by the finalized guard).
func (s *convertingSink) terminalClosed() bool {
	if s.toProtocol == adapter.ProtocolAnthropic {
		return !s.state.OAIStarted
	}
	return !s.state.AntStarted
}

// dropChunk emits a no-op lifecycle placeholder for a chunk that arrives after
// the terminal has already been emitted. It carries a fresh monotonic
// Sequence and zero Data so the inner sink can skip it without affecting the
// downstream protocol framing. It never re-runs the converter, so no second
// terminal can be synthesized and accumulated usage cannot be mutated.
func (s *convertingSink) dropChunk(event sdk.StreamEvent) sdk.StreamEvent {
	s.nextSeq++
	event.Sequence = s.nextSeq
	event.Meta.Sequence = s.nextSeq
	event.Meta.Kind = streaming.EventLifecycle
	event.Meta.EventType = ""
	event.Meta.FinishReason = ""
	event.Meta.Progress = nil
	event.Meta.Usage = nil
	event.Data = nil
	event.Classified = nil
	return event
}

// restoreChunkToolNames applies the per-request tool name restoration map to
// a converted stream chunk. It auto-detects the chunk format (OpenAI or
// Anthropic) and restores original tool names in the appropriate fields.
//
// If s.toolNameMap is nil/empty, it returns chunk unchanged (zero overhead).
func (s *convertingSink) restoreChunkToolNames(chunk []byte) []byte {
	if len(s.toolNameMap) == 0 {
		return chunk
	}
	restored, err := protocolconvert.RestoreToolNamesStreamChunk(chunk, s.toolNameMap)
	if err != nil {
		// Restoration failure is non-fatal; return the original chunk.
		// The tool name will remain sanitized.
		return chunk
	}
	return restored
}

// buildFinalEvent constructs the owned canonical StreamEvent for one
// synthesized terminal payload, assigning a fresh monotonic Sequence and
// deriving sanitized Meta (Kind/EventType/FinishReason) from the converted
// JSON so the downstream framing is protocol-correct. It mirrors
// updateMetaFromConverted but for synthesized (not converted-input) events.
func (s *convertingSink) buildFinalEvent(payload []byte) sdk.StreamEvent {
	meta := streaming.Event{}
	meta = updateMetaFromConverted(meta, payload, s.toProtocol)
	s.nextSeq++
	meta.Sequence = s.nextSeq
	return sdk.StreamEvent{
		Sequence: s.nextSeq,
		Meta:     meta,
		Data:     append(json.RawMessage(nil), payload...),
	}
}

// terminalMetaFromState returns the sanitized TerminalMeta for the last
// synthesized terminal. The Finish token is the request-protocol finish reason
// extracted from the synthesized bytes (passed in, already sanitized by
// extractFinishReason's sanitizeMetaToken). The optional bounded Usage is
// derived from the per-stream converter state's accumulated counters; the
// Bridge merges it monotonically and clamps to MaxTotal at intake.
func (s *convertingSink) terminalMetaFromState(finish string) streaming.TerminalMeta {
	var usage *streaming.Usage
	if s.toProtocol == adapter.ProtocolAnthropic {
		usage = &streaming.Usage{
			PromptTokens:     s.state.OAIUsage.PromptTokens,
			CompletionTokens: s.state.OAIUsage.CompletionTokens,
			TotalTokens:      s.state.OAIUsage.PromptTokens + s.state.OAIUsage.CompletionTokens,
		}
	} else {
		usage = &streaming.Usage{
			PromptTokens:     s.state.AntUsage.PromptTokens,
			CompletionTokens: s.state.AntUsage.CompletionTokens,
			TotalTokens:      s.state.AntUsage.PromptTokens + s.state.AntUsage.CompletionTokens,
		}
	}
	return streaming.TerminalMeta{Finish: finish, Usage: usage}
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
