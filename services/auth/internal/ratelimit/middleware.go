// Package ratelimit wires the shared Redis-backed token bucket into the Auth
// service. It provides a StrictMiddlewareFunc that gates the high-risk
// identity endpoints (login / register / refresh) BEFORE the expensive
// Argon2id / DB work runs, while preserving the strict raw-body boundary
// (the body is decoded once by the generated strict handler; this middleware
// reads the decoded request, never r.Body).
//
// Each operation is gated by TWO independent buckets with independent keys:
//
//   - a pure-IP bucket (scope "auth.<op>.ip", keyed on the trusted client IP);
//   - an account/token bucket (scope "auth.<op>.account", keyed on the
//     normalized email for login/register, or the opaque refresh token for
//     refresh).
//
// The IP bucket is checked first; only when it allows is the account/token
// bucket checked. Either bucket denying (429) or the backend being
// unavailable (503, fail closed) short-circuits before any Argon2id/DB work.
// The two buckets MAY share the same rate, but their keys are always
// independent so that rotating the email/token dimension cannot bypass the
// per-IP flood limit, and a single account crossing IPs is still bounded by
// the account bucket. The multi-bucket check is NOT a global transaction: a
// token consumed from the IP bucket is not rolled back if the account bucket
// later denies, but fail-closed semantics ensure no request proceeds when the
// backend is unavailable.
//
// Keys are HMAC-derived so raw dimensions never reach Redis or logs.
//
// On Redis unavailability the middleware fails CLOSED: protected endpoints
// return a stable, leak-free 503. On a denied request it returns 429 with
// Retry-After and Cache-Control: no-store. Health and read-only endpoints are
// not wrapped and remain anonymous/unlimited.
package ratelimit

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/tokenmp/v3/packages/go/ratelimit"
	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/auth/internal/contract/authv1"
)

// Policies holds the per-endpoint token-bucket policy for BOTH dimensions.
// Each operation is gated by two independent buckets: a pure-IP bucket and
// an account/token bucket. The two buckets MAY share the same rate, but their
// keys are always independent so that rotating the email/token dimension
// cannot bypass the per-IP flood limit, and a single account crossing IPs is
// still bounded by the account bucket.
//
// Capacity is the burst; RefillPerSecond is the steady-state rate. The IP
// dimension is checked first (before Argon2id/DB); only when it allows is the
// account/token dimension checked. Either dimension denying or the backend
// being unavailable short-circuits to 429/503 before any expensive work.
type Policies struct {
	// IP dimension (checked first).
	LoginIPCapacity    float64
	LoginIPRefill      float64
	RegisterIPCapacity float64
	RegisterIPRefill   float64
	RefreshIPCapacity  float64
	RefreshIPRefill    float64
	// Account/token dimension (checked second).
	LoginAccountCapacity    float64
	LoginAccountRefill      float64
	RegisterAccountCapacity float64
	RegisterAccountRefill   float64
	RefreshAccountCapacity  float64
	RefreshAccountRefill    float64
	TTL                     time.Duration
}

// Deps is the wired rate-limit dependencies. Limiter is the shared token-bucket
// backend (Redis in production, InMemory in unit tests); Deriver derives
// opaque keys; IPFromCtx returns the trusted client IP set by the trustedip
// net/http middleware. A nil Limiter means rate limiting is disabled — the
// middleware becomes a no-op pass-through so callers never accidentally
// half-wire it.
type Deps struct {
	Limiter   ratelimit.Limiter
	Deriver   *ratelimit.KeyDeriver
	Policies  Policies
	IPFromCtx func(context.Context) (net.IP, bool)
}

// noOpIP returns the trusted client IP from context via the shared trustedip
// package. It is the default resolver used when none is injected.
func noOpIP(ctx context.Context) (net.IP, bool) {
	ip := trustedip.FromContext(ctx)
	return ip, ip != nil
}

// NewStrictMiddleware returns an authv1.StrictMiddlewareFunc that enforces
// the per-endpoint limits. Operations other than Login/Register/Refresh call
// through unchanged. When Deps.Limiter is nil the returned middleware is a
// pass-through (rate limiting disabled).
func NewStrictMiddleware(deps Deps) authv1.StrictMiddlewareFunc {
	if deps.Limiter == nil || deps.Deriver == nil {
		return func(f authv1.StrictHandlerFunc, _ string) authv1.StrictHandlerFunc { return f }
	}
	ipFrom := deps.IPFromCtx
	if ipFrom == nil {
		ipFrom = noOpIP
	}
	return func(f authv1.StrictHandlerFunc, operationID string) authv1.StrictHandlerFunc {
		switch operationID {
		case "Login", "Register", "Refresh":
		default:
			return f
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			ip, ok := ipFrom(ctx)
			if !ok || ip == nil || ip.To4() == nil && ip.To16() == nil {
				// No trusted client IP available — fail closed.
				return denied503(operationID), nil
			}
			ipStr := ip.String()
			ttl := int(deps.Policies.TTL.Seconds())
			if ttl <= 0 {
				ttl = 600
			}
			// Dimension 1: pure-IP bucket. Checked first; a deny/unavailable
			// here short-circuits before the account bucket is touched, so the
			// IP flood limit cannot be bypassed by rotating email/token.
			ipBucket := func(scope string, cap, refill float64) ratelimit.Bucket {
				return ratelimit.Bucket{
					Key:             deps.Deriver.Derive(scope+".ip", ipStr),
					Capacity:        cap,
					RefillPerSecond: refill,
					TTLSeconds:      ttl,
				}
			}
			// Dimension 2: account/token bucket. Independent key; checked only
			// after the IP bucket allows. Rotating email/token does NOT reset
			// the IP bucket, and a single account across IPs is bounded here.
			acctBucket := func(scope string, cap, refill float64, dim string) ratelimit.Bucket {
				return ratelimit.Bucket{
					Key:             deps.Deriver.Derive(scope+".account", dim),
					Capacity:        cap,
					RefillPerSecond: refill,
					TTLSeconds:      ttl,
				}
			}
			switch operationID {
			case "Login":
				if req, ok := request.(authv1.LoginRequestObject); ok && req.Body != nil {
					if resp := decide(ctx, deps.Limiter, ipBucket("auth.login", deps.Policies.LoginIPCapacity, deps.Policies.LoginIPRefill), operationID); resp != nil {
						return resp, nil
					}
					if resp := decide(ctx, deps.Limiter, acctBucket("auth.login", deps.Policies.LoginAccountCapacity, deps.Policies.LoginAccountRefill, normalizeEmail(req.Body.Email)), operationID); resp != nil {
						return resp, nil
					}
				}
			case "Register":
				if req, ok := request.(authv1.RegisterRequestObject); ok && req.Body != nil {
					if resp := decide(ctx, deps.Limiter, ipBucket("auth.register", deps.Policies.RegisterIPCapacity, deps.Policies.RegisterIPRefill), operationID); resp != nil {
						return resp, nil
					}
					if resp := decide(ctx, deps.Limiter, acctBucket("auth.register", deps.Policies.RegisterAccountCapacity, deps.Policies.RegisterAccountRefill, normalizeEmail(req.Body.Email)), operationID); resp != nil {
						return resp, nil
					}
				}
			case "Refresh":
				if req, ok := request.(authv1.RefreshRequestObject); ok && req.Body != nil {
					if resp := decide(ctx, deps.Limiter, ipBucket("auth.refresh", deps.Policies.RefreshIPCapacity, deps.Policies.RefreshIPRefill), operationID); resp != nil {
						return resp, nil
					}
					if resp := decide(ctx, deps.Limiter, acctBucket("auth.refresh", deps.Policies.RefreshAccountCapacity, deps.Policies.RefreshAccountRefill, req.Body.RefreshToken), operationID); resp != nil {
						return resp, nil
					}
				}
			}
			return f(ctx, w, r, request)
		}
	}
}

// decide calls the limiter and returns the generated response object to send
// when the request must be blocked: 503 on backend failure (fail closed),
// 429 with Retry-After on a legitimate denial. It returns nil to allow the
// request through.
func decide(ctx context.Context, l ratelimit.Limiter, b ratelimit.Bucket, operationID string) any {
	d, err := l.Allow(ctx, b)
	if err != nil {
		return denied503(operationID)
	}
	if !d.Allowed {
		return limitResponse(operationID, d)
	}
	return nil
}

// limitResponse returns the generated 429 response object for the operation
// with Retry-After + Cache-Control: no-store headers and the rate_limited
// error body.
func limitResponse(operationID string, d ratelimit.Decision) any {
	body := errResp(authv1.RateLimited, "rate limit exceeded")
	retryAfter := formatRetryAfter(d.RetryAfter)
	switch operationID {
	case "Login":
		return authv1.Login429JSONResponse{Body: body, Headers: authv1.Login429ResponseHeaders{
			CacheControl: strPtr("no-store"),
			ContentType:  strPtr("application/json; charset=utf-8"),
			RetryAfter:   &retryAfter,
		}}
	case "Register":
		return authv1.Register429JSONResponse{Body: body, Headers: authv1.Register429ResponseHeaders{
			CacheControl: strPtr("no-store"),
			ContentType:  strPtr("application/json; charset=utf-8"),
			RetryAfter:   &retryAfter,
		}}
	case "Refresh":
		return authv1.Refresh429JSONResponse{Body: body, Headers: authv1.Refresh429ResponseHeaders{
			CacheControl: strPtr("no-store"),
			ContentType:  strPtr("application/json; charset=utf-8"),
			RetryAfter:   &retryAfter,
		}}
	}
	return denied503(operationID)
}

// denied503 returns the generated 503 response object for the operation with
// the service_unavailable error body and Cache-Control: no-store. It is the
// fail-closed path used when the limiter backend is unavailable.
func denied503(operationID string) any {
	body := errResp(authv1.ServiceUnavailable, "service unavailable")
	switch operationID {
	case "Login":
		return authv1.Login503JSONResponse{Body: body, Headers: authv1.Login503ResponseHeaders{
			CacheControl: strPtr("no-store"),
			ContentType:  strPtr("application/json; charset=utf-8"),
		}}
	case "Register":
		return authv1.Register503JSONResponse{Body: body, Headers: authv1.Register503ResponseHeaders{
			CacheControl: strPtr("no-store"),
			ContentType:  strPtr("application/json; charset=utf-8"),
		}}
	case "Refresh":
		return authv1.Refresh503JSONResponse{Body: body, Headers: authv1.Refresh503ResponseHeaders{
			CacheControl: strPtr("no-store"),
			ContentType:  strPtr("application/json; charset=utf-8"),
		}}
	}
	return nil
}

func errResp(code authv1.ErrorErrorCode, msg string) authv1.Error {
	return authv1.Error{Error: struct {
		Code    authv1.ErrorErrorCode `json:"code"`
		Message string                `json:"message"`
	}{Code: code, Message: msg}}
}

func strPtr(s string) *string { return &s }

// formatRetryAfter renders a duration as integer seconds (Retry-After is an
// integer count of seconds per RFC 9110). Minimum 1.
func formatRetryAfter(d time.Duration) string {
	s := int(d.Seconds())
	if s < 1 {
		s = 1
	}
	return strconv.Itoa(s)
}

// normalizeEmail mirrors auth.Service email normalization so the bucket key
// is stable regardless of case/whitespace in the request. It never returns an
// error — a malformed email simply hashes as-is, which still rate-limits by
// IP+raw string consistently.
func normalizeEmail(raw string) string {
	s := raw
	// Trim and lowercase ASCII; non-ASCII is left intact but still trimmed.
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
