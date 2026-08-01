// Package ratelimit provides a cross-replica-consistent shared rate limiter
// backed by Redis. The limiting primitive is a token bucket evaluated
// atomically in a single Lua script using Redis server time, so there is no
// client-side clock drift across replicas.
//
// Production deployments MUST use [RedisLimiter] (no in-memory fallback).
// When Redis is unavailable, [RedisLimiter] returns [ErrUnavailable] so the
// caller can fail closed (503) on protected endpoints. [InMemory] exists only
// for unit tests and local development; it is NOT cross-replica consistent.
//
// Redis keys are derived with [KeyDeriver] using HMAC-SHA256 so that the
// bucket dimensions (IP, normalized email, subject, opaque token) are never
// stored in cleartext in Redis or in logs.
package ratelimit

import (
	"context"
	"errors"
	"time"
)

// ErrUnavailable is returned when the Redis backing store cannot be reached or
// returns an unexpected result. It is a stable, non-wrapping sentinel: callers
// must fail closed and never log a cause that could carry host or credential
// fragments. errors.Unwrap(ErrUnavailable) is nil.
var ErrUnavailable = errors.New("ratelimit: limiter backend unavailable")

// Bucket describes a single token-bucket limit to evaluate. Key is the already
// derived opaque bucket key (typically from [KeyDeriver.Derive]); the caller
// MUST NOT place raw dimensions (IP, email, token) in Key. Capacity is the
// maximum burst size in tokens; RefillPerSecond is the steady-state refill
// rate. TTLSeconds bounds the lifetime of the Redis key so idle buckets
// expire; it must be > 0.
type Bucket struct {
	Key             string
	Capacity        float64
	RefillPerSecond float64
	TTLSeconds      int
}

// Decision is the outcome of a limit check.
type Decision struct {
	// Allowed is true when the request consumed a token and may proceed.
	Allowed bool
	// RetryAfter is the minimum duration the caller should wait before
	// retrying when Allowed is false. It is 0 when Allowed is true.
	RetryAfter time.Duration
	// Remaining is the token count left in the bucket after this check
	// (floored to an integer). It is a best-effort hint, not a guarantee.
	Remaining int
}

// Limiter is the port implemented by [RedisLimiter] and [InMemory]. A nil
// limiter is never valid; callers that have rate limiting disabled must
// instead omit the middleware.
type Limiter interface {
	// Allow evaluates Bucket b atomically. On success it returns a Decision
	// (Allowed true or false with a RetryAfter). On a backend failure it
	// returns Decision{} and a non-nil error (typically [ErrUnavailable]).
	Allow(ctx context.Context, b Bucket) (Decision, error)
}
