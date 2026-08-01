package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestKeyDeriver_DeterministicAndUnique(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	d, err := NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("NewKeyDeriver: %v", err)
	}
	a := d.Derive("auth.login.ip", "203.0.113.5")
	b := d.Derive("auth.login.ip", "203.0.113.5")
	if a != b {
		t.Fatal("same dims must yield same key")
	}
	c := d.Derive("auth.login.ip", "203.0.113.6")
	if a == c {
		t.Fatal("different dims must yield different keys")
	}
	// Different scope, same dims must differ.
	if a == d.Derive("auth.register.ip", "203.0.113.5") {
		t.Fatal("different scope must yield different key")
	}
	// Key must never contain the raw IP.
	for _, needle := range []string{"203.0.113.5", "auth", "login"} {
		if contains(a, needle) {
			t.Fatalf("key leaked raw dim %q: %s", needle, a)
		}
	}
	if got := a[:len(KeyPrefix)]; got != KeyPrefix {
		t.Fatalf("key prefix = %q, want %q", got, KeyPrefix)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestKeyDeriver_SeparatorNoCollision(t *testing.T) {
	secret := make([]byte, 32)
	d, _ := NewKeyDeriver(secret)
	// ("a","b") and ("a\x00b") as a single dim must not collide.
	k1 := d.Derive("s", "a", "b")
	k2 := d.Derive("s", "a\x00b")
	if k1 == k2 {
		t.Fatal("dimension separator collision")
	}
}

func TestKeyDeriver_WeakSecret(t *testing.T) {
	if _, err := NewKeyDeriver(nil); err != ErrInvalidSecret {
		t.Fatalf("nil secret: got %v, want ErrInvalidSecret", err)
	}
	if _, err := NewKeyDeriver([]byte("short")); err != ErrInvalidSecret {
		t.Fatalf("short secret: got %v, want ErrInvalidSecret", err)
	}
}

func TestInMemory_BurstAndRefill(t *testing.T) {
	now := time.UnixMilli(0)
	clk := func() time.Time { return now }
	l := NewInMemory(clk)
	b := Bucket{Key: "k", Capacity: 3, RefillPerSecond: 1, TTLSeconds: 60}

	// Burst of 3 allowed, then denied.
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

	// Advance 1s → 1 token refilled.
	now = now.Add(time.Second)
	d, _ = l.Allow(context.Background(), b)
	if !d.Allowed {
		t.Fatal("after 1s refill, 1 token expected")
	}
	// Immediately denied again.
	d, _ = l.Allow(context.Background(), b)
	if d.Allowed {
		t.Fatal("must be denied again after consuming refill")
	}
}

func TestInMemory_IndependentBuckets(t *testing.T) {
	l := NewInMemory(time.Now)
	b1 := Bucket{Key: "k1", Capacity: 1, RefillPerSecond: 1, TTLSeconds: 60}
	b2 := Bucket{Key: "k2", Capacity: 1, RefillPerSecond: 1, TTLSeconds: 60}
	if d, _ := l.Allow(context.Background(), b1); !d.Allowed {
		t.Fatal("b1 first denied")
	}
	if d, _ := l.Allow(context.Background(), b1); d.Allowed {
		t.Fatal("b1 second allowed")
	}
	if d, _ := l.Allow(context.Background(), b2); !d.Allowed {
		t.Fatal("b2 must be independent")
	}
}

func TestInMemory_Concurrent(t *testing.T) {
	l := NewInMemory(time.Now)
	b := Bucket{Key: "shared", Capacity: 10, RefillPerSecond: 0, TTLSeconds: 60}
	var wg sync.WaitGroup
	var allowed int64
	var mu sync.Mutex
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, _ := l.Allow(context.Background(), b)
			if d.Allowed {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 10 {
		t.Fatalf("concurrent allowed = %d, want 10", allowed)
	}
}
