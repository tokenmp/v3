# ADR 0005: Auth Identity Flows

- Status: accepted
- Date: 2026-07-20
- Related: ADR 0004 (Auth Service Foundation)

## Context

ADR 0004 stood up the auth service foundation (HTTP server, config, DB, health
endpoints, migrations, Dockerfile, CI) without implementing registration,
login, JWT, refresh-token rotation or password hashing. This change implements
the auth identity flows on top of that foundation. The existing schema
(000001_create_users, 000002_create_auth_sessions) already carries every
column the flows need — rotation, revoke_reason allow-list, token_family_id,
replaced_by_session_id, token_version — so **no new migration is added**.

## Decision

### Endpoints

All auth routes live under `/api/v1/auth/`. The health probes `/healthz` and
`/readyz` are unchanged.

| Method | Path | Auth | Response |
|---|---|---|---|
| POST | `/api/v1/auth/register` | none | 201 `{id,email,role,status,created_at}` (no auto-login) |
| POST | `/api/v1/auth/login` | none | 200 `{access_token,refresh_token,token_type:"Bearer",expires_in}` |
| POST | `/api/v1/auth/refresh` | none | 200 token response |
| POST | `/api/v1/auth/logout` | none | 204 (idempotent) |
| POST | `/api/v1/auth/logout-all` | Bearer | 204 (revokes all + bumps token_version) |
| GET | `/api/v1/auth/me` | Bearer | 200 public user |
| PUT | `/api/v1/auth/password` | Bearer | 204 (bumps token_version, revokes all sessions) |

Responses are JSON in the body; tokens are **not** delivered via cookies. The
CLI/general consumer is the default and there is no ambient-cookie CSRF
surface. A future browser cookie-mode is a separate design and is not
implemented here.

### Access tokens — Ed25519 / EdDSA

Access tokens are signed with Ed25519 (`github.com/golang-jwt/jwt/v5`,
`EdDSA`). The advantage is that consumers that only need to validate tokens
hold the **public key** alone and never need the private key. Registered
claims: `iss`, `aud`, `sub`, `jti`, `iat`, `nbf`, `exp`. Custom claims: `role`
and `token_version`. Default access TTL is 15 minutes.

**Strict claim validation:** Verify requires `sub`, `jti`, `role`,
`token_version >= 1` and that all registered claims are present with correct
types. A custom struct with typed fields is used instead of MapClaims to
prevent zero-value silent pass-through. `WithValidMethods(["EdDSA"])` blocks
algorithm confusion attacks. Sign errors are never wrapped — only stable
sentinels (`ErrInvalidToken`, `ErrExpired`) are returned.

**Documented limitation:** if the private key is compromised, an attacker can
forge valid access tokens until the key is rotated. This is an accepted
trade-off of asymmetric signing; key rotation is a deployment concern and the
runtime image never contains keys (see Docker / deployment below).

### Configuration

JWT configuration is sourced from `AUTH_*` environment variables:

- `AUTH_JWT_PRIVATE_KEY_FILE`, `AUTH_JWT_PUBLIC_KEY_FILE` — file paths to
  PEM-encoded Ed25519 keys (PKCS8 private, PKIX public). **PEM content is not
  accepted via environment variables** to avoid multi-line secrets and log
  leakage. The service fails fast at startup if the files are missing or do
  not parse; errors are stable sentinels and never echo the key contents or
  the file paths.
- `AUTH_JWT_ISSUER` (default `tokenmp-auth`), `AUTH_JWT_AUDIENCE` (default
  `tokenmp-web`).
- `AUTH_JWT_ACCESS_TOKEN_TTL` (default `15m`), `AUTH_JWT_REFRESH_TOKEN_TTL`
  (default `30d`). Refresh TTL must be greater than access TTL; otherwise the
  service fails fast.

Tests generate an Ed25519 key pair **in-memory** per test process; no key file
is read from disk and no private key is ever written to the repository. The CI
does not need `openssl` or a pre-provisioned key.

### Refresh tokens — opaque, hashed, rotated

A refresh token is 32 bytes of `crypto/rand` encoded as base64url (no
padding). The database stores only the **SHA-256 hash** as `BYTEA` (the
`auth_sessions.refresh_token_hash` unique index); the raw token is never
persisted. Default refresh TTL is 30 days.

On presentation, the token is strictly validated: it must be valid
base64url and decode to exactly 32 bytes. Malformed or empty tokens are
rejected with a unified "invalid refresh" error **without** performing a
DB lookup — this prevents meaningless queries from hashing the literal
string bytes of a malformed token.

Rotation uses a transaction with `SELECT ... FOR UPDATE` on the session row
located by its hash:

1. If the row is not found → 401 invalid refresh (no revocation, rollback is
   a no-op).
2. If the row is already revoked → **token reuse**. All active sessions in the
   `token_family_id` are revoked with `revoke_reason='token_reuse'`, the
   transaction **commits**, and the service returns the same 401 shape as an
   invalid refresh token (reuse is not signalled to an attacker). The commit
   is guaranteed because the rotation callback returns `nil` after performing
   the revocation; the service decides the 401 result after the commit.
3. If `expires_at <= now` → 401 invalid (not reuse).
4. If the user is disabled → 401 invalid.
5. Otherwise rotate: create a new session in the **same token family**, set
   the old row's `replaced_by_session_id` to the new session id, revoke the
   old row with `revoke_reason='token_rotated'`, and issue new access +
   refresh tokens. **JWT issuance occurs inside the transaction**; if it
   fails the transaction rolls back and the new session is not persisted.

**Concurrent refresh semantics (high-security mode):** When two requests
simultaneously present the same valid refresh token, `SELECT FOR UPDATE`
serializes them. The first request rotates successfully and commits. The
second request, after acquiring the lock, sees the now-revoked row and
triggers the reuse path: the entire family (including the session just
created by the first request) is revoked and the transaction commits. This
means the first request's newly-issued tokens are immediately invalid even
though the first request returned success to its client. This is the
**high-security mode** — reuse detection takes priority over availability.
Clients must detect the subsequent 401 and re-authenticate. This trade-off
is accepted because the alternative (allowing concurrent rotation) would
permit token reuse, which is a stronger security failure.

The rotation direction is fixed: the **old** row points at the new session;
new rows never carry `replaced_by_session_id`.

### Logout, logout-all, password change

- **logout** locates the session by refresh token and revokes it with
  `revoke_reason='logout'`. It is idempotent: an invalid or already-revoked
  token returns 204 so the endpoint cannot be used to probe token existence.
- **logout-all** requires a Bearer. It revokes all active sessions for the
  user with `revoke_reason='logout_all'` and bumps `users.token_version`, so
  all outstanding access tokens are immediately invalid.
- **password change** requires a Bearer + `current_password` + `new_password`.
  On success it Argon2id-hashes the new password, updates the row, bumps
  `users.token_version`, and revokes all active sessions with
  `revoke_reason='password_changed'` (including the current one). Returns 204;
  the caller must re-login.

### Access auth middleware

`RequireUser` validates the Bearer token (EdDSA signature, `iss`, `aud`,
`exp`, `nbf`) and **loads the user from the DB on every request**, comparing
`status` (must be `active`) and the claim's `token_version` against
`users.token_version`. A mismatch → 401 with `invalid_token` code (never
`account_disabled`, which would leak account status). This is the
strong-revocation path: password change and logout-all bump `token_version`,
invalidating all access tokens immediately. The per-request DB lookup cost is
accepted as the price of correctness.

### IP attribution and X-Forwarded-For trust boundary

Session attribution records the client IP in the `auth_sessions.ip` INET
column. The IP is extracted from `r.RemoteAddr` with the port stripped via
`net.SplitHostPort` (handles both IPv4 `1.2.3.4:1234` and IPv6
`[::1]:1234`). Chi's `middleware.RealIP` rewrites `r.RemoteAddr` from the
last `X-Forwarded-For` entry when present.

**Security boundary:** Deployments MUST ensure that only trusted reverse
proxies can set `X-Forwarded-For` (typically by stripping/overwriting it at
the edge load balancer). An untrusted X-FF allows IP spoofing, which affects
session attribution and any future rate limiting. This service does not
validate X-FF provenance itself — that is a deployment responsibility.

### Password policy and hashing

- New passwords are hashed with **Argon2id** using OWASP-recommended
  parameters: memory 64 MiB (65536 KiB), iterations 3, parallelism 2, salt
  16 bytes, key 32 bytes, PHC string format (`github.com/alexedwards/argon2id`).
- Legacy bcrypt hashes (`$2a`/`$2b`/`$2y`) are accepted for verification. On a
  successful bcrypt login the password is re-hashed with Argon2id **in the same
  transaction** (progressive upgrade, per ADR 0004). The upgrade does **not**
  bump `token_version` (login does not invalidate sessions).
- Passwords are treated as raw UTF-8 byte sequences: they are **not trimmed**
  and **not NFKC-normalized**. Length is measured in Unicode code points and
  must be 12..128. Invalid UTF-8, NUL and C0/C1 control characters are
  rejected.
- `email` is normalized with `LOWER(BTRIM)` before storage (matching the DB
  CHECK backstop); exactly one `@`, local and domain parts non-empty, no
  whitespace or control characters, and `<= 255` are enforced. A dot in the
  domain is NOT required — `user@localhost` and `user@tld` are accepted.

### Register / login error model

- register duplicate email → 409 `email_taken`. Register success returns 201
  with the public user view and **does not auto-login**.
- login (user not found / wrong password / disabled) → the same 401
  `invalid_credentials`. A **pre-generated dummy Argon2id hash** is used for
  `CompareDummy` on the not-found / mismatch path to flatten the timing side
  channel. Per-request dummy hash generation is forbidden. Disabled accounts
  complete the password comparison before returning the uniform error.
  **Documented limitation:** Argon2id and bcrypt have inherently different
  latency profiles; complete uniformity is not achievable. The pre-generated
  dummy ensures the not-found path always performs exactly one Argon2id
  Compare, matching the wrong-password path for Argon2id users.
- All public errors use the uniform body `{error:{code,message}}`. Raw
  Postgres / driver errors, password hashes and tokens are never surfaced.
  The repository layer returns stable classified sentinels
  (`ErrDuplicateEmail`, `ErrNotFound`, `ErrConstraint`, `ErrInternal`); the
  driver error text is never wrapped into `Error()`/`Unwrap()`.

### Layering

```
cmd/auth            — wiring (config, keys, DB, repos, service, server)
internal/security
  jwt               — Ed25519 issue/verify, key loading (no echo)
  password          — Argon2id + bcrypt compat + policy validation
  refresh           — 32-byte token gen + SHA-256 hash
internal/repository — GORM user/session repos + TxRunner; classified errors
internal/auth       — service: Register/Login/Refresh/Logout/LogoutAll/Me/ChangePassword
internal/handler    — HTTP handlers + RequireUser middleware + uniform errors
internal/server     — Chi router wiring
```

### Testing

- **Unit tests** (no DB, run locally with `go test -race ./...`) cover:
  - `security/jwt` (issue/verify, expiry, aud/iss mismatch, tampered
    signature, wrong key pair, key loading, mismatched pair)
  - `security/password` (policy, Argon2id + bcrypt match/mismatch, $2b
    compatibility, upgrade flag, dummy hash)
  - `security/refresh` (entropy, deterministic hash, malformed input)
  - `auth` service with in-memory fake repos: register (duplicate, weak,
    invalid email), login (Argon2id, bcrypt upgrade, disabled, uniform
    invalid credentials), refresh rotation, **reuse commit semantics**
    (family revoked and persisted despite the 401), expired/unknown refresh,
    logout idempotent, logout-all version bump, password change (success,
    wrong current, weak new), me
  - `handler`/`middleware`: HTTP contracts for every endpoint, error body
    shape, no-credential-leak, stale-access-after-version-bump,
    disabled-account 401, tampered token 401, unknown-field rejection
  - `config`: JWT env validation (negative TTL, refresh <= access)
  - `server`: routes + shutdown (unchanged from foundation)
- **Integration tests** (`-tags=integration`, CI only, real PostgreSQL 17):
  full register → login → /me → refresh rotation → reuse (family revoked) →
  bcrypt legacy upgrade (asserts stored hash → Argon2id, no version bump) →
  password change invalidates access and revokes refresh → logout-all
  invalidates access and revokes all sessions → disabled-login rejected →
  no raw DB error leakage. A **concurrent refresh** test fires 8 goroutines
  against the same refresh token and asserts exactly one succeeds (the row
  lock serializes; the others hit the reuse path).

### Rate limiting — explicitly NOT implemented

An in-memory rate limiter is **not** shipped in this PR. The reasons are
recorded here so a future change does not assume protection exists:

- A per-instance in-memory limiter is inconsistent across replicas and
  cannot protect a multi-replica deployment.
- The `RealIP` trust boundary (which headers are trusted, which proxies are
  in front) is not defined for this service yet, so a naive limiter could be
  bypassed or poisoned.

**Rate limiting is a deployment blocker.** It must be provided by a future
gateway or a shared Redis-backed policy before production deployment. This ADR
does not claim the service is protected; consumers and operators must not
assume it is.

### Docker / deployment

The Dockerfile is unchanged in shape: it builds the `auth` and `healthcheck`
binaries and ships a minimal non-root alpine image. The runtime image does
**not** contain JWT keys. Deployments MUST mount the Ed25519 key files at
runtime at the paths configured by `AUTH_JWT_PRIVATE_KEY_FILE` /
`AUTH_JWT_PUBLIC_KEY_FILE`; the service fails fast if they are missing or
invalid. The image does not run migrations and does not ship `migrate`.

### No schema change

No new migration is added. The existing `users` and `auth_sessions` tables
already provide every column and constraint the flows require (rotation
columns, revoke_reason allow-list, token_version, expression email unique
index). AutoMigrate remains forbidden.

## Consequences

- The auth service now issues Ed25519 access tokens and rotates opaque
  refresh tokens with reuse detection. Registration, login, refresh, logout,
  logout-all, /me and password change are implemented end-to-end.
- All outstanding access tokens can be invalidated immediately via
  `token_version` bump (password change, logout-all).
- The README, module AGENTS.md, OpenAPI and docs index are updated; the
  foundation "not implemented" status is replaced by the implemented flow
  status.
- Rate limiting remains a documented deployment blocker.
- Key management is now a deployment concern: operators must provision and
  rotate the Ed25519 key pair out-of-band; the runtime image does not contain
  keys.

## Alternatives Considered

- **HMAC (HS256) access tokens:** rejected. Ed25519 lets consumers verify
  with the public key alone, narrowing key distribution. The compromise
  trade-off is documented above.
- **Refresh token in a cookie:** rejected for this PR. The default consumer
  is a CLI/general client; cookie-mode CSRF and browser storage are a
  separate future design.
- **In-memory rate limiter:** rejected (see above). A half-correct limiter
  gives false confidence; a correct limiter needs a shared store and a
  defined trust boundary.
- **Storing refresh tokens in plaintext:** rejected. Only the SHA-256 hash is
  stored, so a DB read cannot yield a usable refresh token.
- **Bumping token_version on bcrypt upgrade:** rejected. Login does not
  invalidate sessions; the upgrade is transparent to the user.
- **NFKC normalization of passwords:** rejected. Passwords are treated as raw
  UTF-8 to avoid silent mutation of user-supplied bytes; the policy is
  enforced by rune count and control-character rejection.
