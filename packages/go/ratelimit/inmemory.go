package ratelimit

import (
	"context"
	"sync"
	"time"
)

// InMemory is a single-process token-bucket limiter intended only for unit
// tests and local development. It is NOT cross-replica consistent and MUST
// NOT be used in production. It uses the real wall clock for refill.
type InMemory struct {
	mu    sync.Mutex
	now   func() time.Time
	store map[string]*memBucket
}

type memBucket struct {
	tokens float64
	ts     time.Time
}

// NewInMemory returns a process-local limiter. now is optional; when nil the
// real clock is used. Pass a controlled clock in tests to assert refill.
func NewInMemory(now func() time.Time) *InMemory {
	if now == nil {
		now = time.Now
	}
	return &InMemory{now: now, store: make(map[string]*memBucket)}
}

// Allow evaluates Bucket b in-process. It mirrors [RedisLimiter] semantics:
// first request starts full, refill is computed against now, and a denied
// request returns a RetryAfter. Unlike [RedisLimiter] it never returns
// [ErrUnavailable].
func (l *InMemory) Allow(ctx context.Context, b Bucket) (Decision, error) {
	if b.Capacity <= 0 || b.TTLSeconds <= 0 {
		return Decision{}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bk, ok := l.store[b.Key]
	if !ok {
		bk = &memBucket{tokens: b.Capacity, ts: now}
		l.store[b.Key] = bk
	}
	elapsed := now.Sub(bk.ts)
	if elapsed < 0 {
		elapsed = 0
	}
	bk.tokens += elapsed.Seconds() * b.RefillPerSecond
	if bk.tokens > b.Capacity {
		bk.tokens = b.Capacity
	}
	bk.ts = now

	d := Decision{Remaining: int(bk.tokens)}
	if bk.tokens >= 1.0 {
		bk.tokens -= 1.0
		d.Remaining = int(bk.tokens)
		d.Allowed = true
		return d, nil
	}
	if b.RefillPerSecond > 0 {
		need := 1.0 - bk.tokens
		ms := int((need / b.RefillPerSecond) * 1000)
		if ms < 1 {
			ms = 1
		}
		d.RetryAfter = time.Duration(ms) * time.Millisecond
	}
	return d, nil
}

// Reset clears all buckets. Test helper.
func (l *InMemory) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = make(map[string]*memBucket)
}
