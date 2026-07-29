package execution

import (
	"context"
	"sync"

	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// commitObserverSink delegates all downstream writes and invokes onCommit
// exactly once after the first-token Commit succeeds. The observer runs after
// the downstream flush, so it never reports a first token that was not
// committed to the client. onCommit must return quickly; the StreamDriver uses
// it only to enqueue best-effort logging work.
type commitObserverSink struct {
	inner    streaming.Sink
	onCommit func()
	once     sync.Once
}

func (s *commitObserverSink) Commit(ctx context.Context, events []streaming.Event) error {
	if err := s.inner.Commit(ctx, events); err != nil {
		return err
	}
	s.once.Do(func() {
		if s.onCommit != nil {
			s.onCommit()
		}
	})
	return nil
}

func (s *commitObserverSink) WriteEvent(ctx context.Context, event streaming.Event) error {
	return s.inner.WriteEvent(ctx, event)
}

func (s *commitObserverSink) Flush(ctx context.Context) error {
	return s.inner.Flush(ctx)
}
