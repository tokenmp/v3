// Package ratelimit_test contains shared helpers for the Redis integration
// tests of the token-bucket limiter.
//
// This file has NO build tag so the dial gate helper and its reverse unit
// tests compile under plain `go test ./...`. The dial helper never calls
// t.Fatal/t.Skip: it returns the error plus a `required` flag so unit tests
// can assert the gate behavior directly without reddening the suite. The
// real-Redis integration tests (build tag `integration`) wrap this helper in
// dialRedis, which applies the skip-vs-fatal policy.
package ratelimit_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// requireRedis reports whether Redis integration tests are mandatory.
func requireRedis() bool {
	v := strings.TrimSpace(os.Getenv("RATELIMIT_REQUIRE_REDIS_TESTS"))
	return v == "true" || v == "1"
}

func redisAddr() string {
	if v := os.Getenv("RATING_TEST_REDIS_ADDR"); v != "" {
		return v
	}
	return "redis://127.0.0.1:6379/0"
}

// dialRedisErr parses the configured Redis URL, connects, and PINGs. It
// returns the connected client on success, or a non-nil error describing a URL
// parse failure or ping failure. It also returns the `required` flag
// (RATELIMIT_REQUIRE_REDIS_TESTS). It NEVER calls t.Fatal/t.Skip, so unit tests
// can assert the gate outcome directly without affecting the whole-suite
// result. The caller owns closing the returned client on success.
//
// Splitting the testable core out of dialRedis is what lets the reverse
// verification (bad URL / unreachable Redis) run under required+Redis-up CI
// without forcing the suite to fail: previously the reverse tests called
// dialRedis and relied on t.Fatalf, which reddened CI even when Redis was
// healthy.
func dialRedisErr(t *testing.T) (*redis.Client, error, bool) {
	t.Helper()
	required := requireRedis()
	opts, err := redis.ParseURL(redisAddr())
	if err != nil {
		return nil, fmt.Errorf("invalid RATING_TEST_REDIS_ADDR: %w", err), required
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis unreachable: %w", err), required
	}
	return rdb, nil, required
}

// TestDialRedisErr_BadURL asserts the gate helper returns a non-nil error for
// an unparseable URL, without calling t.Fatalf. It passes in both required and
// non-required modes (no real Redis needed), proving the reverse verification
// can never redden the CI suite. The actual required-mode fatal behavior is
// exercised by dialRedis inside the real integration tests.
func TestDialRedisErr_BadURL(t *testing.T) {
	t.Setenv("RATING_TEST_REDIS_ADDR", "ht!tp://@@bad:xyz")
	rdb, err, required := dialRedisErr(t)
	if err == nil {
		t.Fatal("expected parse error for malformed URL, got nil")
	}
	if rdb != nil {
		t.Fatal("expected nil client on parse error")
	}
	if required != requireRedis() {
		t.Fatalf("required flag = %v, want %v", required, requireRedis())
	}
}

// TestDialRedisErr_PingFails asserts the gate helper returns a non-nil error
// when the address parses but Redis is unreachable. It points at a closed
// port, so no real Redis is required. As with TestDialRedisErr_BadURL, it never
// calls t.Fatalf and thus cannot redden the suite.
func TestDialRedisErr_PingFails(t *testing.T) {
	t.Setenv("RATING_TEST_REDIS_ADDR", "redis://127.0.0.1:1/0")
	rdb, err, required := dialRedisErr(t)
	if err == nil {
		t.Fatal("expected ping error for unreachable Redis, got nil")
	}
	if rdb != nil {
		t.Fatal("expected nil client on ping error")
	}
	if required != requireRedis() {
		t.Fatalf("required flag = %v, want %v", required, requireRedis())
	}
}
