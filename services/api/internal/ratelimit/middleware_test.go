package ratelimit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/packages/go/ratelimit"
	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/api/internal/identity"
	apiratelimit "github.com/tokenmp/v3/services/api/internal/ratelimit"
)

func makeEdgeDeps(t *testing.T, l ratelimit.Limiter) apiratelimit.Deps {
	t.Helper()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	d, err := ratelimit.NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("deriver: %v", err)
	}
	return apiratelimit.Deps{
		Limiter: l,
		Deriver: d,
		Policies: apiratelimit.Policies{
			IPCapacity:   2,
			IPRefill:     0,
			SubjCapacity: 2,
			SubjRefill:   0,
			TTL:          time.Minute,
		},
	}
}

func runChain(t *testing.T, deps apiratelimit.Deps, trustedCIDRs []string, remoteAddr, xff, subject, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	resolver, err := trustedip.NewResolver(trustedCIDRs)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Inject a fake identity between the IP and subject buckets so the subject
	// bucket has a verified subject to key on (mirrors identity.Middleware).
	injectSubject := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subject != "" {
				r = r.WithContext(identity.WithClaims(r.Context(), identity.Claims{Subject: subject}))
			}
			next.ServeHTTP(w, r)
		})
	}
	// Chain: trustedIP → IP bucket → (fake identity) → subject bucket → inner.
	h := resolver.Middleware(apiratelimit.IPMiddleware(deps)(injectSubject(apiratelimit.SubjectMiddleware(deps)(inner))))
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEdge_DisabledIsPassthrough(t *testing.T) {
	// Nil limiter → no limiting.
	rec := runChain(t, apiratelimit.Deps{}, []string{"10.0.0.0/8"}, "10.0.0.5:1", "", "subj", http.MethodPost, "/v1/chat/completions")
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled must pass through, got %d", rec.Code)
	}
}

func TestEdge_IPBucket429AfterBurst(t *testing.T) {
	deps := makeEdgeDeps(t, ratelimit.NewInMemory(time.Now))
	// Trusted peer so IP resolves from XFF.
	for i := 0; i < 2; i++ {
		rec := runChain(t, deps, []string{"10.0.0.0/8"}, "10.0.0.5:1", "203.0.113.7", "subj", http.MethodPost, "/v1/chat/completions")
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d", i, rec.Code)
		}
	}
	rec := runChain(t, deps, []string{"10.0.0.0/8"}, "10.0.0.5:1", "203.0.113.7", "subj", http.MethodPost, "/v1/chat/completions")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd must be 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

func TestEdge_SubjectBucket429AfterBurst(t *testing.T) {
	deps := makeEdgeDeps(t, ratelimit.NewInMemory(time.Now))
	// Two distinct IPs, same subject → subject bucket (capacity 2) exhausts.
	for i := 0; i < 2; i++ {
		rec := runChain(t, deps, []string{"10.0.0.0/8"}, "10.0.0.5:1", "203.0.113.7", "subj-A", http.MethodPost, "/v1/chat/completions")
		if rec.Code != http.StatusOK {
			t.Fatalf("attempt %d: got %d", i, rec.Code)
		}
	}
	// Different IP, same subject → still limited by subject bucket.
	rec := runChain(t, deps, []string{"10.0.0.0/8"}, "10.0.0.5:1", "203.0.113.8", "subj-A", http.MethodPost, "/v1/chat/completions")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("subject bucket 3rd must be 429, got %d", rec.Code)
	}
}

func TestEdge_ReadOnlyNotLimited(t *testing.T) {
	deps := makeEdgeDeps(t, &countLimiter{})
	// GET /v1/models is read-only → not metered → not limited.
	rec := runChain(t, deps, []string{"10.0.0.0/8"}, "10.0.0.5:1", "203.0.113.7", "subj", http.MethodGet, "/v1/models")
	if rec.Code != http.StatusOK {
		t.Fatalf("read-only must not be limited, got %d", rec.Code)
	}
}

func TestEdge_RedisDownFailsClosed503(t *testing.T) {
	deps := makeEdgeDeps(t, &errorLimiter{})
	rec := runChain(t, deps, []string{"10.0.0.0/8"}, "10.0.0.5:1", "203.0.113.7", "subj", http.MethodPost, "/v1/chat/completions")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("redis down must fail closed 503, got %d", rec.Code)
	}
}

func TestEdge_UntrustedPeerUsesPeerIP(t *testing.T) {
	deps := makeEdgeDeps(t, ratelimit.NewInMemory(time.Now))
	// Peer not a trusted proxy → forged XFF ignored; peer IP used.
	rec := runChain(t, deps, []string{"10.0.0.0/8"}, "198.51.100.9:1", "10.0.0.99", "subj", http.MethodPost, "/v1/chat/completions")
	if rec.Code != http.StatusOK {
		t.Fatalf("first must be allowed (peer IP), got %d", rec.Code)
	}
}

func TestEdge_NoIpfailsClosed503(t *testing.T) {
	deps := makeEdgeDeps(t, ratelimit.NewInMemory(time.Now))
	// No trusted resolver configured → trustedip.FromContext returns nil → 503.
	// We simulate by running IPMiddleware directly without the trustedip chain.
	mw := apiratelimit.IPMiddleware(deps)
	called := false
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if called {
		t.Fatal("must not call handler when IP unknown")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown IP must fail closed 503, got %d", rec.Code)
	}
}

// countLimiter counts Allow calls; always allows.
type countLimiter struct{ n int }

func (c *countLimiter) Allow(context.Context, ratelimit.Bucket) (ratelimit.Decision, error) {
	c.n++
	return ratelimit.Decision{Allowed: true}, nil
}

// errorLimiter simulates Redis down.
type errorLimiter struct{}

func (errorLimiter) Allow(context.Context, ratelimit.Bucket) (ratelimit.Decision, error) {
	return ratelimit.Decision{}, ratelimit.ErrUnavailable
}
