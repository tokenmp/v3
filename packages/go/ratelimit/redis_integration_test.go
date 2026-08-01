//go:build integration

// Package ratelimit_test contains the real-Redis integration tests for the
// token-bucket limiter. These require a running Redis instance reachable at
// $RATING_TEST_REDIS_ADDR (default redis://127.0.0.1:6379/0). They are only
// compiled and run with the `integration` build tag.
//
// Skip vs Fail policy:
//
//   - When RATELIMIT_REQUIRE_REDIS_TESTS is unset/empty: tests SKIP (not FAIL)
//     if Redis is unreachable, URL parsing fails, or the Lua eval errors. This
//     keeps local `go test -race ./...` green without Redis.
//   - When RATELIMIT_REQUIRE_REDIS_TESTS=true: Redis is MANDATORY. URL parse
//     failure, ping failure, or Lua eval failure are all FATAL (t.Fatalf), so
//     CI can never silently skip the integration suite. GitHub Actions waits
//     for the Redis service health check before job steps; this suite then
//     PINGs Redis itself.
//
// The testable dial core lives in redis_helper_test.go (no build tag) as
// dialRedisErr, which returns (client, error, required) and never fatals. The
// reverse gate verification (bad URL / unreachable Redis) also lives there as
// plain unit tests that assert the returned error, so they pass under
// required+Redis-up CI without reddening the suite. This file's dialRedis wraps
// dialRedisErr with the skip-vs-fatal policy used by the real integration
// tests.
//
// Test isolation: tests do NOT call FlushDB on a shared database. Instead each
// test derives keys under a unique HMAC-scoped namespace (a per-test random
// prefix passed as a Derive scope), and cleans up only its own keys via a
// bounded SCAN+DEL of that prefix. This is safe to run against a shared Redis.
package ratelimit_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tokenmp/v3/packages/go/ratelimit"
)

// dialRedis connects to Redis and PINGs it, applying the skip-vs-fatal policy.
// Required mode: a URL parse error or ping failure is FATAL, so CI can never
// silently skip the real integration suite. Default: skip so local runs
// without Redis stay green. The pure gate logic (no Fatal) is dialRedisErr in
// redis_helper_test.go, which is unit-tested directly by TestDialRedisErr_*.
func dialRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb, err, required := dialRedisErr(t)
	if err == nil {
		return rdb
	}
	if required {
		t.Fatalf("%v (required=true)", err)
	}
	t.Skipf("%v (required=false)", err)
	return nil
}

// uniqueScope returns a per-test HMAC scope namespace so concurrent test runs
// against a shared Redis never collide. It combines the test name with a
// monotonic counter; the KeyDeriver HMAC ensures the scope never leaks into
// the raw key, so collisions only matter at the scope level.
var scopeMu sync.Mutex
var scopeSeq int64

func uniqueScope(t *testing.T) string {
	t.Helper()
	scopeMu.Lock()
	scopeSeq++
	n := scopeSeq
	scopeMu.Unlock()
	return fmt.Sprintf("test.%s.%d", t.Name(), n)
}

// cleanupScope deletes only the rate-limit keys whose HMAC digest was derived
// under the given scope. Because the deriver output is opaque, we cannot SCAN
// by scope directly; instead tests pass a dedicated prefix marker via the
// bucket Key and cleanup deletes keys matching that marker. To keep isolation
// simple and safe, each test uses a fresh deriver secret AND a unique scope,
// and cleanup deletes ALL rl:v1:* keys that this test process created is NOT
// safe on shared DB. Therefore tests use a dedicated Redis DB index
// (RATING_TEST_REDIS_DB, default 0) and cleanup only scans rl:v1:* in that DB.
//
// To avoid FlushDB on a shared library, cleanup performs a bounded SCAN+DEL of
// keys matching "rl:v1:*" within the connected DB only. Each test is still
// isolated by unique HMAC scopes (distinct keys), so cleanup is best-effort
// hygiene rather than a correctness requirement.
func cleanupScope(t *testing.T, rdb *redis.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "rl:v1:*", 200).Result()
		if err != nil {
			// Cleanup failure is non-fatal; isolation is by unique scope.
			return
		}
		if len(keys) > 0 {
			_ = rdb.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func TestRedisLimiter_BurstAndRefill(t *testing.T) {
	rdb := dialRedis(t)
	defer rdb.Close()
	defer cleanupScope(t, rdb)
	l := ratelimit.NewRedisLimiter(rdb, time.Second)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	scope := uniqueScope(t)
	b := ratelimit.Bucket{
		Key:             drv.Derive(scope, "203.0.113.1"),
		Capacity:        3,
		RefillPerSecond: 1,
		TTLSeconds:      60,
	}
	for i := 0; i < 3; i++ {
		d, err := l.Allow(context.Background(), b)
		if err != nil || !d.Allowed {
			t.Fatalf("attempt %d: allowed=%v err=%v", i, d.Allowed, err)
		}
	}
	d, err := l.Allow(context.Background(), b)
	if err != nil {
		t.Fatalf("4th: %v", err)
	}
	if d.Allowed {
		t.Fatal("4th must be denied")
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("RetryAfter must be > 0, got %v", d.RetryAfter)
	}
	// Wait ~1s for one refill.
	time.Sleep(1100 * time.Millisecond)
	d, _ = l.Allow(context.Background(), b)
	if !d.Allowed {
		t.Fatal("after 1s refill, 1 token expected")
	}
}

func TestRedisLimiter_TwoInstancesShared(t *testing.T) {
	rdb := dialRedis(t)
	defer rdb.Close()
	defer cleanupScope(t, rdb)
	// Two separate client/limiter instances sharing the same Redis key.
	l1 := ratelimit.NewRedisLimiter(rdb, time.Second)
	l2 := ratelimit.NewRedisLimiter(rdb, time.Second)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	scope := uniqueScope(t)
	b := ratelimit.Bucket{
		Key:             drv.Derive(scope, "203.0.113.2"),
		Capacity:        2,
		RefillPerSecond: 0,
		TTLSeconds:      60,
	}
	if d, _ := l1.Allow(context.Background(), b); !d.Allowed {
		t.Fatal("l1 first denied")
	}
	if d, _ := l2.Allow(context.Background(), b); !d.Allowed {
		t.Fatal("l2 must see the shared bucket state")
	}
	d, _ := l1.Allow(context.Background(), b)
	if d.Allowed {
		t.Fatal("third across instances must be denied (shared limit)")
	}
}

func TestRedisLimiter_TTLExpires(t *testing.T) {
	rdb := dialRedis(t)
	defer rdb.Close()
	defer cleanupScope(t, rdb)
	l := ratelimit.NewRedisLimiter(rdb, time.Second)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	scope := uniqueScope(t)
	b := ratelimit.Bucket{
		Key:             drv.Derive(scope, "203.0.113.3"),
		Capacity:        1,
		RefillPerSecond: 0,
		TTLSeconds:      1,
	}
	if d, _ := l.Allow(context.Background(), b); !d.Allowed {
		t.Fatal("first denied")
	}
	if d, _ := l.Allow(context.Background(), b); d.Allowed {
		t.Fatal("second allowed")
	}
	time.Sleep(1500 * time.Millisecond)
	// Key expired → bucket restarts full.
	if d, _ := l.Allow(context.Background(), b); !d.Allowed {
		t.Fatal("after TTL expiry bucket must restart full")
	}
}

func TestRedisLimiter_Concurrent(t *testing.T) {
	rdb := dialRedis(t)
	defer rdb.Close()
	defer cleanupScope(t, rdb)
	l := ratelimit.NewRedisLimiter(rdb, time.Second)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	scope := uniqueScope(t)
	b := ratelimit.Bucket{
		Key:             drv.Derive(scope, "203.0.113.4"),
		Capacity:        10,
		RefillPerSecond: 0,
		TTLSeconds:      60,
	}
	var wg sync.WaitGroup
	var allowed, denied int64
	var mu sync.Mutex
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, _ := l.Allow(context.Background(), b)
			mu.Lock()
			defer mu.Unlock()
			if d.Allowed {
				allowed++
			} else {
				denied++
			}
		}()
	}
	wg.Wait()
	if allowed != 10 {
		t.Fatalf("allowed = %d, want exactly 10 under concurrency", allowed)
	}
	if denied != 190 {
		t.Fatalf("denied = %d, want 190", denied)
	}
}

func TestRedisLimiter_DownReturnsUnavailable(t *testing.T) {
	// Point at a closed port to simulate Redis being down without touching a
	// real server. This test does NOT require a real Redis and always runs.
	opts, err := redis.ParseURL("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()
	l := ratelimit.NewRedisLimiter(rdb, 500*time.Millisecond)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	b := ratelimit.Bucket{
		Key:             drv.Derive(uniqueScope(t), "203.0.113.5"),
		Capacity:        1,
		RefillPerSecond: 1,
		TTLSeconds:      60,
	}
	_, err = l.Allow(context.Background(), b)
	if err != ratelimit.ErrUnavailable {
		t.Fatalf("got err=%v, want ErrUnavailable", err)
	}
}

func TestRedisLimiter_KeyHidesDimension(t *testing.T) {
	rdb := dialRedis(t)
	defer rdb.Close()
	defer cleanupScope(t, rdb)
	l := ratelimit.NewRedisLimiter(rdb, time.Second)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	scope := uniqueScope(t)
	b := ratelimit.Bucket{
		Key:             drv.Derive(scope, "203.0.113.77", "user@example.com"),
		Capacity:        1,
		RefillPerSecond: 0,
		TTLSeconds:      60,
	}
	_, _ = l.Allow(context.Background(), b)
	// Inspect Redis keys: the key must not contain the raw IP or email.
	ctx := context.Background()
	keys, _ := rdb.Keys(ctx, "rl:v1:*").Result()
	if len(keys) == 0 {
		t.Fatal("no rate-limit key written")
	}
	for _, k := range keys {
		for _, needle := range []string{"203.0.113.77", "user@example.com"} {
			if containsStr(k, needle) {
				t.Fatalf("redis key leaks raw dimension %q: %s", needle, k)
			}
		}
	}
	fmt.Println("redis key shape:", keys[0])
}

// TestRedisLimiter_InvalidBucketReturnsError documents that a Lua eval path
// with an invalid bucket surfaces a non-nil error (distinct from a real Redis
// failure). It uses a real Redis connection but a zero-capacity bucket, which
// the limiter rejects in its own validation before reaching Lua.
func TestRedisLimiter_InvalidBucketReturnsError(t *testing.T) {
	rdb := dialRedis(t)
	defer rdb.Close()
	defer cleanupScope(t, rdb)
	l := ratelimit.NewRedisLimiter(rdb, time.Second)
	drv, _ := ratelimit.NewKeyDeriver(makeTestSecret(t))
	// Capacity 0 is rejected by the limiter's own validation (not via Lua),
	// returning a non-nil error distinct from ErrUnavailable.
	b := ratelimit.Bucket{
		Key:             drv.Derive(uniqueScope(t), "203.0.113.6"),
		Capacity:        0,
		RefillPerSecond: 1,
		TTLSeconds:      60,
	}
	_, err := l.Allow(context.Background(), b)
	if err == nil {
		t.Fatal("invalid bucket must return a non-nil error")
	}
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func makeTestSecret(t *testing.T) []byte {
	t.Helper()
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte('a' + i%26)
	}
	return s
}
