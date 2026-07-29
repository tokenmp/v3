package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

type commitObserverTestSink struct {
	commitErr error
	commits   atomic.Int32
}

func (s *commitObserverTestSink) Commit(context.Context, []streaming.Event) error {
	s.commits.Add(1)
	return s.commitErr
}
func (*commitObserverTestSink) WriteEvent(context.Context, streaming.Event) error { return nil }
func (*commitObserverTestSink) Flush(context.Context) error                       { return nil }

func TestCommitObserverSinkObservesFirstSuccessfulCommitOnce(t *testing.T) {
	inner := &commitObserverTestSink{}
	var observed atomic.Int32
	sink := &commitObserverSink{inner: inner, onCommit: func() { observed.Add(1) }}

	if err := sink.Commit(context.Background(), nil); err != nil {
		t.Fatalf("Commit(first) error = %v", err)
	}
	if err := sink.Commit(context.Background(), nil); err != nil {
		t.Fatalf("Commit(second) error = %v", err)
	}
	if got := observed.Load(); got != 1 {
		t.Fatalf("observed = %d, want 1", got)
	}
}

func TestCommitObserverSinkDoesNotObserveFailedCommit(t *testing.T) {
	inner := &commitObserverTestSink{commitErr: errors.New("commit failed")}
	var observed atomic.Int32
	sink := &commitObserverSink{inner: inner, onCommit: func() { observed.Add(1) }}

	if err := sink.Commit(context.Background(), nil); err == nil {
		t.Fatal("Commit() error = nil")
	}
	if got := observed.Load(); got != 0 {
		t.Fatalf("observed = %d, want 0", got)
	}
}
