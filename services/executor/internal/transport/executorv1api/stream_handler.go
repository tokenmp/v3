package executorv1api

import "net/http"

// CreateChatCompletion first performs only strict structural mode detection.
// Non-stream requests then stay in the generated strict wrapper, whose Adapter
// performs the sole full normalization and owns its request ID. A true stream
// request gets exactly one Hybrid ID before its single full normalization.
func (h *Hybrid) CreateChatCompletion(w http.ResponseWriter, r *http.Request) {
	stream, err := DetectOpenAIChatStream(r.Context())
	if lifecycleErr := contextLifecycleError(r.Context(), err); lifecycleErr != nil {
		return
	}
	if err != nil {
		writeChatResponse(w, RenderChatError(err))
		return
	}
	if !stream {
		h.strictServer().CreateChatCompletion(w, r)
		return
	}

	requestID := h.requestID(r.Context())
	normalized, err := NormalizeOpenAIChatRequest(r.Context(), requestID)
	if lifecycleErr := contextLifecycleError(r.Context(), err); lifecycleErr != nil {
		return
	}
	if err != nil {
		writeChatResponse(w, RenderChatError(err))
		return
	}
	// DetectOpenAIChatStream saw true; preserve this defensive invariant should
	// the full normalizer's stream extraction ever diverge.
	if !normalized.Stream {
		writeChatResponse(w, RenderChatError(ErrInvalidRequest))
		return
	}

	sink, err := NewOpenAISSEProtocolSink(w)
	if err != nil || isNilStreamExecutor(h.streamExecutor) {
		writeChatResponse(w, RenderChatError(errFailClosed))
		return
	}
	result, err := h.streamExecutor.Execute(r.Context(), normalized.StreamRequest(sink))
	// A committed sink is authoritative: JSON would corrupt its SSE response.
	// Context lifecycle termination is similarly a deliberate no-write result.
	if sink.Committed() || contextLifecycleError(r.Context(), err) != nil {
		return
	}
	if err != nil {
		writeChatResponse(w, RenderChatError(err))
		return
	}
	writeChatResponse(w, RenderChatStreamResult(result))
}

// CreateMessage is the Anthropic-native counterpart of CreateChatCompletion.
// Structural errors are native 400s without a request ID; stream:false is
// delegated unchanged so the strict Adapter owns the only full normalization
// and ID allocation. True streams allocate one Hybrid ID before normalization.
func (h *Hybrid) CreateMessage(w http.ResponseWriter, r *http.Request) {
	stream, err := DetectAnthropicMessagesStream(r.Context())
	if lifecycleErr := contextLifecycleError(r.Context(), err); lifecycleErr != nil {
		return
	}
	if err != nil {
		writeMessageResponse(w, RenderMessageError(err))
		return
	}
	if !stream {
		h.strictServer().CreateMessage(w, r)
		return
	}

	requestID := h.requestID(r.Context())
	normalized, err := NormalizeAnthropicMessagesRequest(r.Context(), requestID)
	if lifecycleErr := contextLifecycleError(r.Context(), err); lifecycleErr != nil {
		return
	}
	if err != nil {
		writeMessageResponse(w, RenderMessageErrorWithRequestID(err, requestID))
		return
	}
	if !normalized.Stream {
		writeMessageResponse(w, RenderMessageErrorWithRequestID(ErrInvalidRequest, requestID))
		return
	}

	sink, err := NewAnthropicSSEProtocolSink(w)
	if err != nil || isNilStreamExecutor(h.streamExecutor) {
		writeMessageResponse(w, RenderMessageErrorWithRequestID(errFailClosed, requestID))
		return
	}
	result, err := h.streamExecutor.Execute(r.Context(), normalized.StreamRequest(sink))
	if sink.Committed() || contextLifecycleError(r.Context(), err) != nil {
		return
	}
	if err != nil {
		writeMessageResponse(w, RenderMessageErrorWithRequestID(err, requestID))
		return
	}
	writeMessageResponse(w, RenderMessageStreamResult(result, requestID))
}
