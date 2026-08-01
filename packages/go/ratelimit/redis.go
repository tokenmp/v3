package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketScript is a single atomic Lua token-bucket evaluation. It uses
// redis.call('TIME') so refill is computed against the Redis server clock,
// eliminating client-side drift across replicas. It is replicated-snapshot
// safe under Redis >= 5 (effects replication is the default), which CI and
// production both satisfy.
//
// KEYS[1]  = bucket hash key
// ARGV[1]  = capacity (float-as-string)
// ARGV[2]  = refill per second (float-as-string)
// ARGV[3]  = ttl seconds (integer-as-string)
//
// Returns: { allowed (0|1), retry_after_ms (int), remaining (int) }
const tokenBucketScript = `local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
local t = redis.call('TIME')
local now = tonumber(t[1]) + tonumber(t[2]) / 1000000.0
local data = redis.call('HMGET', key, 'tok', 'ts')
local tok = tonumber(data[1])
local ts = tonumber(data[2])
if tok == nil or ts == nil then
  tok = capacity
  ts = now
end
local elapsed = now - ts
if elapsed < 0 then elapsed = 0 end
tok = tok + elapsed * rate
if tok > capacity then tok = capacity end
local allowed = 0
local retry_ms = 0
if tok >= 1.0 then
  allowed = 1
  tok = tok - 1.0
else
  allowed = 0
  if rate > 0 then
    local need = 1.0 - tok
    retry_ms = math.ceil((need / rate) * 1000.0)
    if retry_ms < 1 then retry_ms = 1 end
  end
end
redis.call('HSET', key, 'tok', tok, 'ts', now)
redis.call('EXPIRE', key, ttl)
local remaining = math.floor(tok)
return {allowed, retry_ms, remaining}
`

// RedisLimiter evaluates token buckets atomically in Redis. It is safe for
// concurrent use. A nil client is invalid; construct with [NewRedisLimiter].
type RedisLimiter struct {
	rdb     redis.UniversalClient
	script  *redis.Script
	timeout time.Duration
}

// NewRedisLimiter wraps a go-redis UniversalClient. The client lifetime and
// connection pool are owned by the caller (the service wires its own client
// and closes it on shutdown). timeout bounds each Eval call so a hung Redis
// cannot block a request indefinitely; if it is <= 0 a sane default is used.
func NewRedisLimiter(rdb redis.UniversalClient, timeout time.Duration) *RedisLimiter {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &RedisLimiter{
		rdb:     rdb,
		script:  redis.NewScript(tokenBucketScript),
		timeout: timeout,
	}
}

// Allow evaluates Bucket b atomically in Redis. On any Redis error, nil result,
// or malformed reply it returns Decision{} and a non-wrapping [ErrUnavailable]
// so callers fail closed. The error never carries host, port, or credentials.
func (l *RedisLimiter) Allow(ctx context.Context, b Bucket) (Decision, error) {
	if b.Capacity <= 0 || b.TTLSeconds <= 0 {
		return Decision{}, fmt.Errorf("ratelimit: invalid bucket (capacity=%.0f ttl=%d)", b.Capacity, b.TTLSeconds)
	}
	cctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	res, err := l.script.Run(cctx, l.rdb,
		[]string{b.Key},
		formatFloat(b.Capacity),
		formatFloat(b.RefillPerSecond),
		b.TTLSeconds,
	).Result()
	if err != nil {
		// err may carry redis network details; collapse to a stable sentinel.
		return Decision{}, ErrUnavailable
	}
	arr, ok := res.([]interface{})
	if !ok || len(arr) < 3 {
		return Decision{}, ErrUnavailable
	}
	allowed, ok1 := arr[0].(int64)
	retryMS, ok2 := arr[1].(int64)
	remaining, ok3 := arr[2].(int64)
	if !ok1 || !ok2 || !ok3 {
		return Decision{}, ErrUnavailable
	}
	d := Decision{
		Allowed:   allowed == 1,
		Remaining: int(remaining),
	}
	if !d.Allowed && retryMS > 0 {
		d.RetryAfter = time.Duration(retryMS) * time.Millisecond
	}
	return d, nil
}

// formatFloat renders a float64 the way Lua's tonumber expects: a plain
// decimal string. strconv.FormatFloat with -1 precision round-trips.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
