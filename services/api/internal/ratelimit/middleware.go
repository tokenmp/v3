// Package ratelimit wires the shared Redis token bucket into the Edge/BFF
// (API service). It provides two net/http middlewares for the metered model
// execution endpoints (/v1/chat/completions, /v1/messages, /v1/responses,
// /v1/images/generations):
//
//   - IP bucket: applied before identity verification so even unauthenticated
//     floods are bounded per trusted client IP.
//   - Subject bucket: applied after the identity middleware resolves the
//     authenticated subject, before the quota/proxy chain runs.
//
// Both buckets key on the resolved trusted client IP (and the subject for the
// subject bucket) using HMAC-SHA256 so raw dimensions never reach Redis or
// logs. Health and read-only endpoints (e.g. GET /v1/models) are not wrapped.
//
// On Redis unavailability the middlewares fail CLOSED with a stable, leak-free
// 503. On a denied request they return 429 with Retry-After and
// Cache-Control: no-store. Responses are written as raw JSON (no envelope) to
// match the Edge /v1/* execution error shape used by the quota middleware.
package ratelimit

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/tokenmp/v3/packages/go/ratelimit"
	"github.com/tokenmp/v3/packages/go/ratelimit/trustedip"
	"github.com/tokenmp/v3/services/api/internal/identity"
)

// Policies holds the per-dimension token-bucket policy for the Edge.
type Policies struct {
	IPCapacity   float64
	IPRefill     float64
	SubjCapacity float64
	SubjRefill   float64
	TTL          time.Duration
}

// Deps wires the shared limiter, key deriver and policy. When Limiter is nil
// the middlewares are pass-throughs (rate limiting disabled), so callers can
// never accidentally half-wire them.
type Deps struct {
	Limiter  ratelimit.Limiter
	Deriver  *ratelimit.KeyDeriver
	Policies Policies
}

// MeteredPath reports whether path is a metered model-execution POST that the
// rate limiters apply to. GET /v1/models and other read-only endpoints are
// excluded so they remain unlimited. It mirrors app.meteredExecutorRequest but
// is duplicated here to avoid an import cycle (app imports ratelimit).
func MeteredPath(method, path string) bool {
	if method != http.MethodPost {
		return false
	}
	switch path {
	case "/v1/chat/completions", "/v1/messages", "/v1/responses", "/v1/images/generations":
		return true
	default:
		return false
	}
}

// IPMiddleware returns a net/http middleware enforcing the per-IP bucket on
// metered execution endpoints. It reads the trusted client IP from context
// (set by the trustedip middleware). When the peer IP is unknown it fails
// closed (503). Non-metered requests pass through.
func IPMiddleware(deps Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if deps.Limiter == nil || deps.Deriver == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !MeteredPath(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ip := trustedip.FromContext(r.Context())
			if ip == nil || (ip.To4() == nil && ip.To16() == nil) {
				writeFailClosed(w)
				return
			}
			b := ratelimit.Bucket{
				Key:             deps.Deriver.Derive("edge.v1.ip", ip.String()),
				Capacity:        deps.Policies.IPCapacity,
				RefillPerSecond: deps.Policies.IPRefill,
				TTLSeconds:      ttlSeconds(deps.Policies.TTL),
			}
			if blocked(w, deps.Limiter, r.Context(), b) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SubjectMiddleware returns a net/http middleware enforcing the per-subject
// bucket on metered execution endpoints. It must run AFTER identity.Middleware
// so the subject is available; when no identity is present it passes through
// (the identity middleware already returned 401). On Redis failure it fails
// closed (503); the quota/proxy chain is never reached.
func SubjectMiddleware(deps Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if deps.Limiter == nil || deps.Deriver == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !MeteredPath(r.Method, r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			claims, ok := identity.FromContext(r.Context())
			if !ok || claims.Subject == "" {
				// No identity: the identity middleware already wrote 401.
				next.ServeHTTP(w, r)
				return
			}
			b := ratelimit.Bucket{
				Key:             deps.Deriver.Derive("edge.v1.subject", claims.Subject),
				Capacity:        deps.Policies.SubjCapacity,
				RefillPerSecond: deps.Policies.SubjRefill,
				TTLSeconds:      ttlSeconds(deps.Policies.TTL),
			}
			if blocked(w, deps.Limiter, r.Context(), b) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// blocked evaluates one bucket. It returns true (and writes a response) when
// the request must not proceed. On a backend error it writes 503; on a denial
// it writes 429 + Retry-After + Cache-Control: no-store.
func blocked(w http.ResponseWriter, l ratelimit.Limiter, ctx context.Context, b ratelimit.Bucket) bool {
	d, err := l.Allow(ctx, b)
	if err != nil {
		writeFailClosed(w)
		return true
	}
	if !d.Allowed {
		write429(w, d.RetryAfter)
		return true
	}
	return false
}

func ttlSeconds(d time.Duration) int {
	s := int(d.Seconds())
	if s <= 0 {
		s = 600
	}
	return s
}

// write429 writes the Edge 429 rate-limited response as raw JSON matching the
// /v1/* execution error shape. Retry-After is integer seconds (min 1).
func write429(w http.ResponseWriter, retryAfter time.Duration) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	ra := int(retryAfter.Seconds())
	if ra < 1 {
		ra = 1
	}
	h.Set("Retry-After", strconv.Itoa(ra))
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"Rate limit exceeded"}}`))
}

// writeFailClosed writes the Edge 503 service-unavailable response as raw
// JSON. It never leaks Redis topology or credentials.
func writeFailClosed(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":{"code":"service_unavailable","message":"Rate limit service unavailable"}}`))
}
