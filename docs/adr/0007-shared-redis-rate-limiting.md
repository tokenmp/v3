# ADR 0007: Shared Redis Rate Limiting

## Status

Accepted — implemented (feat/shared-rate-limit branch).

## Context

The launch blocker "Redis 共享速率限制" required cross-replica-consistent
rate limiting for the high-risk identity endpoints (Auth login/register/refresh)
and the Edge metered model-execution endpoints (`/v1/*`). The prior state
documented rate limiting as unimplemented and trusted forwarding headers
unconditionally via chi `middleware.RealIP`, which is spoofable by any client.

Requirements (confirmed design, no pending decisions):

- Shared Redis token bucket, atomic per-evaluation, no client clock drift.
- No in-memory fallback: protected endpoints fail closed (stable, leak-free
  503) when Redis is unavailable; over-quota returns 429 + Retry-After +
  Cache-Control: no-store.
- Trusted client IP boundary: the well-formed `X-Forwarded-For` chain is
  honored only when the TCP peer belongs to an explicit trusted-proxy CIDR
  allowlist; otherwise the peer IP is used. `X-Real-IP` is NEVER used (it is
  a single-value header with no chain provenance and is trivially spoofable
  by any hop). When XFF is absent the TCP peer is used.
- HMAC-SHA256 key derivation — raw IP/email/token never placed in Redis keys
  or logs.
- Explicit, fail-fast configuration; secrets read from files, never echoed.
- Mature Go Redis client at a fixed version; Lua uses Redis server time.

## Decision

Introduce a new shared Go module `packages/go/ratelimit`
(`github.com/tokenmp/v3/packages/go/ratelimit`) consumed by both Auth and
the Edge. It owns:

- A `Limiter` port with a `Decision` (Allowed / RetryAfter / Remaining) and a
  stable non-wrapping `ErrUnavailable` sentinel (fail closed).
- `RedisLimiter`: a single atomic Lua token-bucket script using
  `redis.call('TIME')` so refill is computed against the Redis server clock
  (no cross-replica drift). Returns `ErrUnavailable` on any Redis error, nil
  result, or malformed reply. Effects-replication safe under Redis >= 5
  (default since 5.0).
- `InMemory`: process-local fake for unit tests only (not cross-replica, not
  production).
- `KeyDeriver`: length-prefixed HMAC-SHA256 → `rl:v1:<hex>` keys; minimum
  32-byte secret enforced.
- `trustedip` subpackage: `Resolver` + net/http `Middleware` that replaces
  unconditional chi `middleware.RealIP`. It sets the resolved IP in context
  and on `Request.RemoteAddr`. Rightmost-untrusted XFF semantics; `X-Real-IP`
  is deliberately ignored (no chain provenance). When the peer is untrusted
  or XFF is absent, the TCP peer is used.

Auth wiring (`services/auth/internal/ratelimit`): a `StrictMiddlewareFunc`
that gates `Login`/`Register`/`Refresh` BEFORE the Argon2id/DB work, reading
the already-decoded request body (never re-reading `r.Body`, preserving the
strict raw-body boundary). Each operation is gated by **two independent
buckets with independent keys**: a pure-IP bucket (scope
`auth.<op>.ip`, checked first) and an account/token bucket (scope
`auth.<op>.account`, checked second — normalized email for login/register,
opaque refresh token for refresh). The IP bucket is checked first; only when
it allows is the account/token bucket checked. Either bucket denying (429) or
the backend being unavailable (503, fail closed) short-circuits before any
Argon2id/DB work. The two buckets MAY share the same rate, but their keys are
always independent so rotating the email/token dimension cannot bypass the
per-IP flood limit, and a single account crossing IPs is still bounded by the
account bucket. The multi-bucket check is NOT a global transaction (a denied
account check does not roll back the consumed IP token), but fail-closed
semantics ensure no request proceeds when the backend is unavailable. It
returns the generated `…429JSONResponse` (Retry-After + Cache-Control) or
`…503JSONResponse` (fail closed). `middleware.RealIP` is replaced by the
trusted-IP middleware when rate limiting is enabled.

Edge wiring (`services/api/internal/ratelimit` + `internal/app`): two net/http
middlewares on the metered POST execution endpoints only — an IP bucket before
identity (bounds unauthenticated floods) and a subject bucket after identity,
before quota/proxy. Health and read-only endpoints (e.g. `GET /v1/models`) are
not limited. Rate limiting completes strictly before SSE commit, so no JSON
is injected after commit.

Contract: `packages/contracts/openapi/auth/v1.yaml` gains `429` (with
`Retry-After`) and `503` responses on login/register/refresh and two new
`Error.code` enum values `rate_limited` / `service_unavailable`. Generated
files regenerated; `check-generated.sh` freshness gate is green. Edge `/v1/*`
is a passthrough proxy not described by `api/v1.yaml`, so its rate-limit
responses are documented in `services/api/AGENTS.md` and tests, not the
contract.

Dependency: `github.com/redis/go-redis/v9 v9.21.0` (fixed). HMAC secret read
from a file path env var (`AUTH_RATE_LIMIT_HMAC_SECRET_FILE` /
`API_RATE_LIMIT_HMAC_SECRET_FILE`) into a short-lived `[]byte` on the config
object (`RateLimitHMACSecret`); main consumes it, builds the `KeyDeriver`, and
zeroes the buffer. The path is retained on the config as a non-secret
identifier; neither the path nor the content is ever echoed in errors.

CI: the `go-auth` job gains a `redis:7-alpine` service and steps for
`packages/go/ratelimit` gofmt/vet/build, unit race tests, and a
build-tagged real-Redis integration suite (burst/refill, two-instance sharing,
TTL, concurrency, Redis-down 503, HMAC key non-leak). The integration suite is
SKIP-only when `RATELIMIT_REQUIRE_REDIS_TESTS` is unset; CI sets
`RATELIMIT_REQUIRE_REDIS_TESTS=true` and explicitly waits for Redis to be
ready before running, so a URL-parse/ping/Lua failure is FATAL (never silently
skipped). Test isolation uses unique HMAC scopes per test plus a bounded
SCAN+DEL cleanup of `rl:v1:*` keys (no `FlushDB` on a shared database).

## Consequences

- Both services now depend on Redis when rate limiting is enabled; Redis is a
  required runtime dependency for production rate limiting (disabled by
  default, opt-in per environment). No memory fallback — operators must keep
  Redis available or accept 503 on protected endpoints.
- The `middleware.RealIP` unconditional behavior is gone wherever the
  trusted-IP resolver is wired; deployments behind a proxy MUST configure
  `*_RATE_LIMIT_TRUSTED_PROXIES` or client IP falls back to the peer.
- Known trade-off (Auth): a revoked-but-not-yet-expired JWT within the access
  TTL is unaffected by rate limiting; rate limiting is a defense-in-depth
  control, not an authz boundary.
- Local dev without Redis: unit/race tests pass without Redis; the
  build-tagged integration suite is SKIPPED (not failed) when Redis is
  unreachable, and runs in CI against the service container.
