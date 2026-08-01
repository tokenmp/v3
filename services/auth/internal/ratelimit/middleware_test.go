package ratelimit

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tokenmp/v3/packages/go/ratelimit"
	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
)

func makeDeps(t *testing.T, l ratelimit.Limiter) Deps {
	t.Helper()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	d, err := ratelimit.NewKeyDeriver(secret)
	if err != nil {
		t.Fatalf("deriver: %v", err)
	}
	return Deps{
		Limiter: l,
		Deriver: d,
		Policies: Policies{
			LoginIPCapacity:         2,
			LoginIPRefill:           0,
			LoginAccountCapacity:    5,
			LoginAccountRefill:      0,
			RegisterIPCapacity:      1,
			RegisterIPRefill:        0,
			RegisterAccountCapacity: 5,
			RegisterAccountRefill:   0,
			RefreshIPCapacity:       5,
			RefreshIPRefill:         0,
			RefreshAccountCapacity:  1,
			RefreshAccountRefill:    0,
			TTL:                     time.Minute,
		},
	}
}

// runMiddleware builds the strict middleware and invokes it for the given
// operation with the decoded request object. It returns the response object
// (any) and error from the middleware, without calling the wrapped handler
// when the middleware short-circuits. When the middleware allows, it returns
// a sentinel "ALLOWED" via a fake handler.
func runMiddleware(t *testing.T, deps Deps, opID string, request any, ip net.IP) (any, error) {
	t.Helper()
	mw := NewStrictMiddleware(deps)
	allowed := false
	f := func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		allowed = true
		return "ALLOWED", nil
	}
	ctx := context.WithValue(context.Background(), ipKey{}, ip)
	// Use a nil ResponseWriter path: the middleware never writes directly; it
	// returns a response object. So a nil writer is safe here.
	resp, err := mw(f, opID)(ctx, nil, nil, request)
	_ = allowed
	return resp, err
}

// ipKey mirrors trustedip's private context key by storing via the public
// FromContext path. Since trustedip stores with its own private key, we
// inject IP via the Deps.IPFromCtx field instead.
type ipKey struct{}

func runWithIP(t *testing.T, deps Deps, opID string, request any, ip net.IP) any {
	t.Helper()
	deps.IPFromCtx = func(context.Context) (net.IP, bool) { return ip, ip != nil }
	mw := NewStrictMiddleware(deps)
	f := func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		return "ALLOWED", nil
	}
	resp, err := mw(f, opID)(context.Background(), nil, nil, request)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return resp
}

func TestMiddleware_DisabledIsPassthrough(t *testing.T) {
	// Nil limiter → pass-through for all ops including rate-limited ones.
	mw := NewStrictMiddleware(Deps{})
	called := false
	f := func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		called = true
		return nil, nil
	}
	_, _ = mw(f, "Login")(context.Background(), nil, nil, nil)
	if !called {
		t.Fatal("disabled middleware must pass through")
	}
}

func TestMiddleware_Login429AfterBurst(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	ip := net.ParseIP("203.0.113.10")
	req := authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "User@Example.COM", Password: "x"}}
	// Capacity 2 → two allowed, third denied.
	if r := runWithIP(t, deps, "Login", req, ip); r != "ALLOWED" {
		t.Fatalf("1st must be allowed, got %#v", r)
	}
	if r := runWithIP(t, deps, "Login", req, ip); r != "ALLOWED" {
		t.Fatalf("2nd must be allowed, got %#v", r)
	}
	r := runWithIP(t, deps, "Login", req, ip)
	resp, ok := r.(authv1.Login429JSONResponse)
	if !ok {
		t.Fatalf("3rd must be 429, got %T", r)
	}
	if resp.Headers.RetryAfter == nil || *resp.Headers.RetryAfter == "" {
		t.Fatal("429 must carry Retry-After")
	}
	if resp.Headers.CacheControl == nil || *resp.Headers.CacheControl != "no-store" {
		t.Fatal("429 must carry Cache-Control: no-store")
	}
}

func TestMiddleware_LoginEmailNormalization(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	// Give the IP bucket enough headroom so the account bucket is the
	// binding constraint, proving mixed-case emails share ONE account bucket.
	deps.Policies.LoginIPCapacity = 100
	deps.Policies.LoginAccountCapacity = 2
	ip := net.ParseIP("203.0.113.11")
	r1 := authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "A@B.com"}}
	r2 := authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "a@b.com"}}
	r3 := authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "a@b.com"}}
	if r := runWithIP(t, deps, "Login", r1, ip); r != "ALLOWED" {
		t.Fatalf("1st allowed expected, got %T", r)
	}
	if r := runWithIP(t, deps, "Login", r2, ip); r != "ALLOWED" {
		t.Fatalf("2nd (case-folded) must share bucket, got %T", r)
	}
	if r := runWithIP(t, deps, "Login", r3, ip); r == "ALLOWED" {
		t.Fatal("3rd must be denied (shared bucket by normalized email)")
	}
}

func TestMiddleware_DifferentIPsIndependent(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	req := authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "x@y.com"}}
	if r := runWithIP(t, deps, "Login", req, net.ParseIP("203.0.113.20")); r != "ALLOWED" {
		t.Fatalf("ip1 allowed expected, got %T", r)
	}
	if r := runWithIP(t, deps, "Login", req, net.ParseIP("203.0.113.21")); r != "ALLOWED" {
		t.Fatalf("ip2 must be independent, got %T", r)
	}
}

func TestMiddleware_RefreshTokenBucket(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	ip := net.ParseIP("203.0.113.30")
	req := authv1.RefreshRequestObject{Body: &authv1.RefreshRequest{RefreshToken: "opaque-token-abc"}}
	if r := runWithIP(t, deps, "Refresh", req, ip); r != "ALLOWED" {
		t.Fatalf("1st refresh allowed expected, got %T", r)
	}
	if r := runWithIP(t, deps, "Refresh", req, ip); r == "ALLOWED" {
		t.Fatal("2nd refresh must be denied (capacity 1)")
	}
}

func TestMiddleware_Register429(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	ip := net.ParseIP("203.0.113.40")
	req := authv1.RegisterRequestObject{Body: &authv1.RegisterRequest{Email: "r@example.com", Password: "verystrongpassword"}}
	if r := runWithIP(t, deps, "Register", req, ip); r != "ALLOWED" {
		t.Fatalf("1st register allowed expected, got %T", r)
	}
	if r := runWithIP(t, deps, "Register", req, ip); r == "ALLOWED" {
		t.Fatal("2nd register must be denied (capacity 1)")
	}
}

func TestMiddleware_RedisDownFailsClosed503(t *testing.T) {
	deps := makeDeps(t, &errorLimiter{})
	ip := net.ParseIP("203.0.113.50")
	req := authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "d@e.com"}}
	r := runWithIP(t, deps, "Login", req, ip)
	if _, ok := r.(authv1.Login503JSONResponse); !ok {
		t.Fatalf("redis down must fail closed 503, got %T", r)
	}
}

func TestMiddleware_NoIpfailsClosed503(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	deps.IPFromCtx = func(context.Context) (net.IP, bool) { return nil, false }
	mw := NewStrictMiddleware(deps)
	called := false
	f := func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		called = true
		return nil, nil
	}
	resp, _ := mw(f, "Login")(context.Background(), nil, nil, authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "x@y.com"}})
	if called {
		t.Fatal("must not call handler when IP is unknown")
	}
	if _, ok := resp.(authv1.Login503JSONResponse); !ok {
		t.Fatalf("unknown IP must fail closed 503, got %T", resp)
	}
}

func TestMiddleware_NonProtectedOpPassthrough(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	mw := NewStrictMiddleware(deps)
	called := false
	f := func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		called = true
		return nil, nil
	}
	_, _ = mw(f, "Me")(context.Background(), nil, nil, nil)
	if !called {
		t.Fatal("non-protected op must pass through")
	}
}

func TestMiddleware_IgnoresRealIPContext(t *testing.T) {
	// Verify the default IPFromCtx reads from trustedip context.
	resolver, err := trustedip.NewResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	// Default IPFromCtx (nil) → uses trustedip.FromContext.
	mw := NewStrictMiddleware(deps)
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.0.0.5:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	// Run the trustedip middleware to populate context.
	var ctx context.Context
	resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = r.Context()
	})).ServeHTTP(httptest.NewRecorder(), req)
	f := func(c context.Context, _ http.ResponseWriter, _ *http.Request, _ any) (any, error) {
		ip := trustedip.FromContext(c)
		if !ip.Equal(net.ParseIP("203.0.113.99")) {
			t.Fatalf("expected resolved IP in handler ctx, got %v", ip)
		}
		return "ALLOWED", nil
	}
	r, _ := mw(f, "Login")(ctx, nil, nil, authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "z@y.com"}})
	if r != "ALLOWED" {
		t.Fatalf("first allowed expected, got %T", r)
	}
}

// TestMiddleware_SameIPDifferentEmailStillIPLimited proves the pure-IP
// bucket is independent of the account bucket: rotating the email dimension
// does NOT reset the per-IP flood limit. The IP bucket is the binding
// constraint and blocks regardless of which email is presented.
func TestMiddleware_SameIPDifferentEmailStillIPLimited(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	// Tight IP bucket, generous account bucket.
	deps.Policies.LoginIPCapacity = 2
	deps.Policies.LoginIPRefill = 0
	deps.Policies.LoginAccountCapacity = 100
	deps.Policies.LoginAccountRefill = 0
	ip := net.ParseIP("203.0.113.60")
	// Two different emails from the same IP exhaust the IP bucket.
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "a@example.com"}}, ip); r != "ALLOWED" {
		t.Fatalf("1st (email a) must be allowed, got %T", r)
	}
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "b@example.com"}}, ip); r != "ALLOWED" {
		t.Fatalf("2nd (email b) must be allowed, got %T", r)
	}
	// Third request with a THIRD different email — must still be denied by
	// the IP bucket, proving email rotation cannot bypass IP flood control.
	r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "c@example.com"}}, ip)
	if _, ok := r.(authv1.Login429JSONResponse); !ok {
		t.Fatalf("3rd (email c) must be 429 via IP bucket, got %T", r)
	}
}

// TestMiddleware_SingleAccountAcrossIPsAccountLimited proves the account
// bucket is independent of the IP bucket: a single account rotating across
// IPs is still bounded by the account bucket.
func TestMiddleware_SingleAccountAcrossIPsAccountLimited(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	// Generous IP bucket, tight account bucket.
	deps.Policies.LoginIPCapacity = 100
	deps.Policies.LoginIPRefill = 0
	deps.Policies.LoginAccountCapacity = 2
	deps.Policies.LoginAccountRefill = 0
	email := "shared@example.com"
	// Two different IPs, same account → account bucket exhausts.
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: email}}, net.ParseIP("203.0.113.70")); r != "ALLOWED" {
		t.Fatalf("1st (ip1) must be allowed, got %T", r)
	}
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: email}}, net.ParseIP("203.0.113.71")); r != "ALLOWED" {
		t.Fatalf("2nd (ip2) must be allowed, got %T", r)
	}
	// Third request from a THIRD IP, same account → account bucket denies.
	r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: email}}, net.ParseIP("203.0.113.72"))
	if _, ok := r.(authv1.Login429JSONResponse); !ok {
		t.Fatalf("3rd (ip3, same account) must be 429 via account bucket, got %T", r)
	}
}

// TestMiddleware_IPBucketCheckedBeforeAccount proves the IP bucket is checked
// first: when the IP bucket is exhausted, the account bucket is NOT consulted
// (no token consumed from it), so a later request from a fresh IP but the same
// account still has its full account budget. This documents the
// non-transactional multi-bucket semantics: a denied IP check does not consume
// an account token, and vice-versa is impossible because IP runs first.
func TestMiddleware_IPBucketCheckedBeforeAccount(t *testing.T) {
	deps := makeDeps(t, ratelimit.NewInMemory(time.Now))
	deps.Policies.LoginIPCapacity = 1
	deps.Policies.LoginIPRefill = 0
	deps.Policies.LoginAccountCapacity = 2
	deps.Policies.LoginAccountRefill = 0
	ip := net.ParseIP("203.0.113.80")
	// 1st: IP allows, account allows.
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "x@y.com"}}, ip); r != "ALLOWED" {
		t.Fatalf("1st must be allowed, got %T", r)
	}
	// 2nd: same IP → IP bucket denies; account bucket must NOT be consumed.
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "x@y.com"}}, ip); r == "ALLOWED" {
		t.Fatal("2nd must be denied by IP bucket")
	}
	// 3rd: fresh IP, SAME account → account bucket should still have its 1
	// token (IP denial did not consume it), so this must be allowed.
	if r := runWithIP(t, deps, "Login", authv1.LoginRequestObject{Body: &authv1.LoginRequest{Email: "x@y.com"}}, net.ParseIP("203.0.113.81")); r != "ALLOWED" {
		t.Fatalf("3rd (fresh IP, same account) must be allowed — IP denial must not consume account token, got %T", r)
	}
}

// errorLimiter is a fake limiter that always fails, simulating Redis down.
type errorLimiter struct{}

func (errorLimiter) Allow(context.Context, ratelimit.Bucket) (ratelimit.Decision, error) {
	return ratelimit.Decision{}, ratelimit.ErrUnavailable
}
