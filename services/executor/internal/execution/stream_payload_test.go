package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

type payloadTestSource struct {
	mu      sync.Mutex
	events  []sdk.StreamEvent
	err     error
	gate    <-chan struct{}
	started chan<- struct{}
	closes  atomic.Int32
}

func (s *payloadTestSource) Next(context.Context) (sdk.StreamEvent, error) {
	if s.started != nil {
		s.started <- struct{}{}
	}
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return sdk.StreamEvent{}, s.err
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e, nil
}
func (s *payloadTestSource) Close() error { s.closes.Add(1); return nil }

type payloadTestSink struct {
	mu                            sync.Mutex
	commits                       [][]sdk.StreamEvent
	writes                        []sdk.StreamEvent
	commitErr, writeErr, flushErr error
	flushes                       int
}

func (s *payloadTestSink) Commit(_ context.Context, events []sdk.StreamEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits = append(s.commits, cloneEvents(events))
	return s.commitErr
}
func (s *payloadTestSink) WriteEvent(_ context.Context, event sdk.StreamEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, cloneStreamEvent(event))
	return s.writeErr
}
func (s *payloadTestSink) Flush(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
	return s.flushErr
}
func cloneEvents(in []sdk.StreamEvent) []sdk.StreamEvent {
	out := make([]sdk.StreamEvent, len(in))
	for i := range in {
		out[i] = cloneStreamEvent(in[i])
	}
	return out
}

func payloadEvent(sequence uint64, kind streaming.EventKind) sdk.StreamEvent {
	meta := streaming.Event{Sequence: sequence, Kind: kind}
	if kind == streaming.EventNativeError {
		return sdk.StreamEvent{Sequence: sequence, Meta: meta}
	}
	return sdk.StreamEvent{Sequence: sequence, Meta: meta, Data: []byte(fmt.Sprintf(`{"n":%d}`, sequence))}
}
func newPayloadAdapters(t *testing.T, events ...sdk.StreamEvent) (*sdkPayloadSource, *streamPayloadSink, *payloadTestSink) {
	t.Helper()
	source := &payloadTestSource{events: events, err: streaming.ErrEndOfStream}
	sink := &payloadTestSink{}
	payloads, adapter, err := newSDKPayloadSource(source, sink)
	if err != nil {
		t.Fatal(err)
	}
	return payloads, adapter, sink
}

func TestSDKPayloadSourceLifecycleBoundsAndCopyIsolation(t *testing.T) {
	events := make([]sdk.StreamEvent, maxPendingPayloadEvents+1)
	for i := range events {
		events[i] = payloadEvent(uint64(i+1), streaming.EventLifecycle)
	}
	source, sink, downstream := newPayloadAdapters(t, events...)
	var metas []streaming.Event
	for range events[:maxPendingPayloadEvents] {
		ev, err := source.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		metas = append(metas, ev)
	}
	if _, err := source.Next(context.Background()); !errors.Is(err, ErrStreamPayloadBuffer) {
		t.Fatalf("overflow = %v", err)
	}
	if err := sink.Commit(context.Background(), metas); err != nil {
		t.Fatal(err)
	}
	if len(downstream.commits) != 1 || len(downstream.commits[0]) != len(metas) {
		t.Fatal("payload batch missing")
	}
	// Retained copies survive source/caller mutation, while sink receives an
	// independent copy that cannot mutate adapter state.
	if got := string(downstream.commits[0][0].Data); got != `{"n":1}` {
		t.Fatalf("data=%q", got)
	}
}

func TestSDKPayloadSourceRejectsSequenceAndPayloadViolations(t *testing.T) {
	cases := []sdk.StreamEvent{
		{Sequence: 0, Meta: streaming.Event{Sequence: 0, Kind: streaming.EventSemantic}, Data: []byte(`{}`)},
		{Sequence: 1, Meta: streaming.Event{Sequence: 2, Kind: streaming.EventSemantic}, Data: []byte(`{}`)},
		{Sequence: 1, Meta: streaming.Event{Sequence: 1, Kind: streaming.EventSemantic}, Data: []byte(`not-json`)},
		{Sequence: 1, Meta: streaming.Event{Sequence: 1, Kind: streaming.EventNativeError}, Data: []byte(`{}`)},
	}
	for _, event := range cases {
		source, _, _ := newPayloadAdapters(t, event)
		if _, err := source.Next(context.Background()); !errors.Is(err, ErrStreamPayloadProtocol) {
			t.Fatalf("event=%v error=%v", event.Sequence, err)
		}
	}
	// Duplicates are rejected only once the first event has been accepted.
	source, _, _ := newPayloadAdapters(t, payloadEvent(2, streaming.EventSemantic), payloadEvent(2, streaming.EventFinish))
	if _, err := source.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Next(context.Background()); !errors.Is(err, ErrStreamPayloadProtocol) {
		t.Fatalf("duplicate=%v", err)
	}
}

func TestSDKPayloadSinkCommitOrderExactLookupAndFailureRetention(t *testing.T) {
	source, sink, downstream := newPayloadAdapters(t, payloadEvent(4, streaming.EventLifecycle), payloadEvent(9, streaming.EventSemantic))
	first, _ := source.Next(context.Background())
	second, _ := source.Next(context.Background())
	if err := sink.Commit(context.Background(), []streaming.Event{second, first}); err != nil {
		t.Fatal(err)
	}
	if got := []uint64{downstream.commits[0][0].Sequence, downstream.commits[0][1].Sequence}; got[0] != 9 || got[1] != 4 {
		t.Fatalf("order=%v", got)
	}
	if err := sink.Commit(context.Background(), []streaming.Event{first}); !errors.Is(err, ErrStreamPayloadProtocol) {
		t.Fatalf("deleted lookup=%v", err)
	}

	source, sink, downstream = newPayloadAdapters(t, payloadEvent(1, streaming.EventSemantic))
	event, _ := source.Next(context.Background())
	downstream.commitErr = errors.New("downstream")
	if err := sink.Commit(context.Background(), []streaming.Event{event}); err == nil {
		t.Fatal("missing commit failure")
	}
	if _, ok := source.pending[event.Sequence]; !ok {
		t.Fatal("uncertain commit deleted retained payload")
	}
	sink.Discard()
	if len(source.pending) != 0 {
		t.Fatal("Discard did not release uncertain payload")
	}
}

func TestSDKPayloadSinkWriteFlushStagesAndRetainsOnFailure(t *testing.T) {
	source, sink, downstream := newPayloadAdapters(t, payloadEvent(1, streaming.EventSemantic), payloadEvent(2, streaming.EventFinish))
	first, _ := source.Next(context.Background())
	second, _ := source.Next(context.Background())
	if err := sink.WriteEvent(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteEvent(context.Background(), second); !errors.Is(err, ErrStreamPayloadStaged) {
		t.Fatalf("staged write=%v", err)
	}
	downstream.flushErr = errors.New("flush uncertain")
	if err := sink.Flush(context.Background()); err == nil {
		t.Fatal("missing flush failure")
	}
	if _, ok := source.pending[first.Sequence]; !ok {
		t.Fatal("uncertain flush deleted staged payload")
	}
	if err := sink.WriteEvent(context.Background(), second); !errors.Is(err, ErrStreamPayloadStaged) {
		t.Fatalf("write after uncertain flush=%v", err)
	}
	sink.Discard()
	if len(source.pending) != 0 {
		t.Fatal("Discard did not release staged payload")
	}
}

func TestSDKPayloadSourceClassifiedCacheCloseAndDiscard(t *testing.T) {
	secret := "provider-raw-secret"
	upstream := &payloadTestSource{err: fmt.Errorf("wrapped %s: %w", secret, sdk.NewClassifiedError(sdk.ErrUnavailable, 503, "request-safe", "code", "type"))}
	downstream := &payloadTestSink{}
	source, sink, err := newSDKPayloadSource(upstream, downstream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Next(context.Background()); !errors.Is(err, sdk.ErrUnavailable) || errors.Is(err, errors.New(secret)) {
		t.Fatalf("Next=%v", err)
	}
	cached := source.LastClassified()
	if cached == nil || !errors.Is(cached, sdk.ErrUnavailable) || fmt.Sprint(cached) == secret {
		t.Fatalf("cache=%v", cached)
	}
	if cached == source.LastClassified() {
		t.Fatal("classified cache was not cloned")
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if upstream.closes.Load() != 1 {
		t.Fatalf("closes=%d", upstream.closes.Load())
	}
	sink.Discard()
}

func TestSDKPayloadAdaptersConcurrentMisuseFailsSafe(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	upstream := &payloadTestSource{events: []sdk.StreamEvent{payloadEvent(1, streaming.EventSemantic)}, err: streaming.ErrEndOfStream, gate: gate, started: started}
	downstream := &payloadTestSink{}
	source, sink, err := newSDKPayloadSource(upstream, downstream)
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { _, err := source.Next(context.Background()); first <- err }()
	<-started
	if _, err := source.Next(context.Background()); !errors.Is(err, ErrStreamPayloadMisconfigured) {
		t.Fatalf("concurrent Next=%v", err)
	}
	close(gate)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := sink.Commit(context.Background(), []streaming.Event{{Sequence: 1, Kind: streaming.EventSemantic}}); err != nil {
		t.Fatal(err)
	}
}

func TestSDKPayloadSourcePayloadLimitAndPendingPeak(t *testing.T) {
	if maxPendingPayloadEvents != 35 || maxPendingPayloadBytes != 35*sdk.MaxStreamEventDataBytes {
		t.Fatalf("pending bounds = %d events / %d bytes", maxPendingPayloadEvents, maxPendingPayloadBytes)
	}
	atLimit := payloadEvent(1, streaming.EventSemantic)
	atLimit.Data = append([]byte(`"`), bytes.Repeat([]byte("x"), sdk.MaxStreamEventDataBytes-2)...)
	atLimit.Data = append(atLimit.Data, '"')
	source, _, _ := newPayloadAdapters(t, atLimit)
	if _, err := source.Next(context.Background()); err != nil {
		t.Fatalf("at-limit payload: %v", err)
	}

	overLimit := payloadEvent(1, streaming.EventSemantic)
	overLimit.Data = append([]byte(`"`), bytes.Repeat([]byte("x"), sdk.MaxStreamEventDataBytes-1)...)
	overLimit.Data = append(overLimit.Data, '"')
	source, _, _ = newPayloadAdapters(t, overLimit)
	if _, err := source.Next(context.Background()); !errors.Is(err, ErrStreamPayloadProtocol) {
		t.Fatalf("oversized payload error=%v", err)
	}
}

func TestSDKPayloadSourceNativeErrorDoesNotConsumePendingPayload(t *testing.T) {
	event := payloadEvent(1, streaming.EventNativeError)
	event.Data = nil
	event.Classified = sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "stream_error", "protocol")
	source, _, _ := newPayloadAdapters(t, event)
	meta, err := source.Next(context.Background())
	if err != nil || meta.Kind != streaming.EventNativeError {
		t.Fatalf("Next = (%v, %v)", meta, err)
	}
	if len(source.pending) != 0 || source.pendingBytes != 0 {
		t.Fatalf("native error retained payload: count=%d bytes=%d", len(source.pending), source.pendingBytes)
	}
	classified := source.LastClassified()
	if classified == nil || !errors.Is(classified, sdk.ErrProtocol) || classified == event.Classified {
		t.Fatalf("native classification = %#v, want owned event classification", classified)
	}
}
