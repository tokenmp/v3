# ADR 0004: Auth Service Foundation

- Status: accepted
- Date: 2026-07-20
- Related: ADR 0001 (Monorepo Tooling), ADR 0003 (CI Baseline)

## Context

TokenMP v3 needs its first Go service. The auth service owns authentication
and JWT, and accesses only the `tokenmp_auth` database. This change is the
**foundation**: it must stand up the service skeleton (module, config, HTTP
server, DB connection, migrations, health endpoints, graceful shutdown,
Dockerfile, tests, CI) without implementing registration, login or JWT.
Business logic belongs to follow-up PRs.

A planner draft proposed several incompatible schema defaults that would
cause real production issues. This ADR records the corrected, authoritative
decisions that override those mistakes.

## Decision

### Go module and workspace

- Create the module `github.com/tokenmp/v3/services/auth` at Go 1.26.5.
- Introduce `go.work` at the repository root with `use ./services/auth`.
- Pin Go 1.26.5 in root `mise.toml` alongside Node/pnpm.
- Node.js workspace tooling (`pnpm-workspace.yaml`, `turbo.json`) must not
  own or manage Go module boundaries; the Go module is autonomous.

### Runtime and framework

- HTTP router: Chi v5 (`github.com/go-chi/chi/v5`) with RequestID, RealIP and
  Recoverer middleware.
- HTTP server: stdlib `net/http` with explicit read-header/read/write/idle
  timeouts.
- ORM: GORM (`gorm.io/gorm` + `gorm.io/driver/postgres`) for the connection
  pool and future application-layer queries. **AutoMigrate is forbidden.**
- Logging: stdlib `log/slog` (JSON default; text optional). Configuration
  uses stdlib only (no third-party config library).
- Graceful shutdown on SIGINT/SIGTERM within a configurable
  `AUTH_SHUTDOWN_TIMEOUT`; configuration errors fail fast.

### Configuration and secrets

- All runtime configuration is sourced from `AUTH_*` environment variables:
  `AUTH_DATABASE_URL` (required), `AUTH_HTTP_ADDR`, `AUTH_LOG_LEVEL`,
  `AUTH_LOG_FORMAT`, `AUTH_SHUTDOWN_TIMEOUT`, `AUTH_DB_MAX_OPEN_CONNS`,
  `AUTH_DB_MAX_IDLE_CONNS`, `AUTH_DB_CONN_MAX_LIFETIME`.
- `AUTH_DATABASE_URL` is the single connection URL. Composing the URL from
  multiple parts is avoided to prevent fragment leakage in logs/errors. It is
  strictly validated: only `postgres`/`postgresql` URLs are accepted, with a
  host, a non-empty user and a path of exactly `/tokenmp_auth`. Validation
  errors report only the failing rule and never echo the URL or credentials.
- Numeric and duration tunables (`AUTH_DB_MAX_*`, `AUTH_DB_CONN_MAX_LIFETIME`,
  `AUTH_SHUTDOWN_TIMEOUT`) fail fast on non-parseable or negative input; they
  never silently fall back to a default.
- Defaults are safe and never include production credentials.
- Logs never print the connection string. `database.Open` returns stable
  classified errors that do not wrap the driver's raw error, so the DSN cannot
  reach logs. The `/readyz` 503 response and the `/readyz` HEAD response never
  leak the underlying error message (HEAD writes no body).
- `.env.example` documents the surface; no real `.env` is committed.

### Schema (overrides planner errors)

The migration files are the source of truth. The corrected decisions are:

- The legacy production `users` table is confirmed to use
  `UUID` / `VARCHAR` / `TEXT` / `TIMESTAMPTZ` types. The migrations preserve
  those types and the stored values exactly so they remain compatible with
  the production table.
- `users.role` is `VARCHAR(16)` with `CHECK (role IN ('user','admin'))`,
  **default `'user'`**. The planner draft's "default admin" is rejected as
  insecure.
- `users.token_version` is `INTEGER`, **default `1`**, `CHECK (token_version >= 1)`.
- `users.email` is `VARCHAR(255)`; a CHECK requires
  `email <> '' AND email = LOWER(BTRIM(email))`, so every stored value is
  already the canonical lowercase-trimmed form (the application normalizes
  before insert; the CHECK is the backstop). Uniqueness is enforced on the
  expression index `LOWER(BTRIM(email))` (retained by contract even though the
  CHECK makes a plain unique index equivalent). **Citext is not used** —
  explicit lowercasing and trimming avoids extension types and collation drift.
- `users.password_hash` is **`TEXT`** (matches the legacy production column
  exactly, so there is no length cap on future hash formats) and stores
  **bcrypt** hashes today, with `CHECK (password_hash <> '')`.
- `users.status` is `VARCHAR(16)` with `CHECK (status IN ('active','disabled'))`,
  default `'active'`.
- Migration `000001_create_users` creates `pgcrypto` (`CREATE EXTENSION IF
  NOT EXISTS`) but the down migration **does not drop it** — future schemas
  may depend on it; dropping would cascade unpredictably.
- Migration `000002_create_auth_sessions` carries the refresh-token rotation
  columns required by future login work: `refresh_token_hash BYTEA` with a
  unique index and `CHECK (length(refresh_token_hash) > 0)`;
  `token_family_id UUID NOT NULL DEFAULT gen_random_uuid()`;
  `replaced_by_session_id UUID` (self-FK, `ON DELETE SET NULL`, preserved for
  future rotation semantics; the column reads "this row was replaced BY
  session <id>" so on rotation the OLD row is updated to point at the NEW
  session id and revoked with `revoke_reason='token_rotated'` — new rows
  never carry this value); `expires_at`; `revoked_at`; `revoke_reason`;
  `ip` **INET**; `user_agent` **TEXT**; timestamps. CHECK constraints:
  `expires_at > created_at`; `revoke_reason` allow-list
  (`logout`/`logout_all`/`password_changed`/`admin_revoked`/`token_rotated`/`token_reuse`/`user_disabled`);
  and `revoked_at`/`revoke_reason` consistency (both NULL or both set).
- `auth_sessions` migration applies and reverses cleanly; down drops tables
  in reverse order.
- No seed data ships.
- `preferred_billing` and `fallback_enabled` are **not** in Auth's data
  ownership and are not present in this schema.

### Local development policy (no local PostgreSQL)

- The local machine must **not** install or start PostgreSQL, and must not
  run `docker run postgres`, `brew install postgresql` or any equivalent.
  Local Docker is **not** required to validate the Dockerfile; the image is
  built on the GitHub Runner as part of the `go-auth` CI job (build only —
  not run, not pushed, not published). Local verification is limited to
  `gofmt`, `go vet` (with and without the `integration` tag),
  `go test -race ./...` (without the `integration` tag), `go build`, the
  four pnpm scripts, YAML parse checks and `git diff --check`.
- Real migrations and the integration suite run **only** in the GitHub
  Actions `go-auth` job, against an ephemeral `postgres:17-alpine` service
  container provisioned per run and torn down afterwards. The migration
  up/down/up cycle and the integration tests are reliable because they run
  against a fresh, dedicated `tokenmp_auth` database every run.
- If a developer must point the service at a shared dev/preview PostgreSQL,
  they **must** use a dedicated, confirmed `tokenmp_auth` database on that
  server. Tests, migrations and ad-hoc writes are **never** run against a
  production database or any database shared with other services.
- Server addresses, SSH paths and credentials are never committed and never
  written into the repository or `.agents/` docs; private deployment
  context lives only in the optional, uncommitted `.agents/local.md`.

### Hashing strategy (forward-looking)

- This PR does **not** implement login, so no hashing is performed at runtime.
- `password_hash` stores bcrypt hashes today.
- The planned progressive upgrade to Argon2id will:
  1. Verify the stored bcrypt hash on the next successful login.
  2. Re-hash with Argon2id.
  3. Update the row and bump `token_version` as needed.
- Argon2id is the long-term target; bcrypt is the compatibility baseline.
- This strategy is documented here and in the module `AGENTS.md` so future
  PRs do not redesign the schema.

### Migrations and tooling

- Migrations are versioned SQL files under `services/auth/migrations`, applied
  by `golang-migrate` CLI at version `v4.18.3` (pinned in CI; the actual
  resolved version is verified via `go list`). The CLI must be built with the
  `-tags 'postgres'` build tag because the default `go install` only ships
  the `stub` database driver.
- The `migrate` library is **not** a runtime dependency of the service; only
  the CLI is used by CI/ops. The runtime image does **not** ship `migrate`
  and does **not** run migrations at startup.
- AutoMigrate is forbidden — schema must change via migration files only.

### Tests and CI

- Unit tests: `internal/config` (env validation, strict `AUTH_DATABASE_URL`
  parsing that never echoes the URL/credentials in errors, fail-fast on
  invalid numeric/duration values), `internal/database` (classified errors
  whose public `Error()` never renders the driver/DSN text), `internal/handler`
  (`/healthz` and `/readyz` GET + HEAD with an injected fake Pinger; HEAD
  writes no body; 503 does not leak error text), `internal/server` (Chi routes,
  middleware, shutdown).
- Integration tests gated by the `integration` build tag require a real
  PostgreSQL instance and the `migrate` CLI on PATH. **They are not run on
  developer machines**; they run only in the GitHub Actions `go-auth` job
  against a PostgreSQL 17 service container, where they run the migration
  cycle (up → down → up) and assert schema defaults, CHECK constraints, the
  email normalization invariant, `auth_sessions` FKs and unique indexes, the
  `revoke_reason` allow-list and revoked consistency, the INET `ip` / TEXT
  `user_agent` columns, the `replaced_by_session_id` self-FK, and `/readyz`
  returning 200 against a live DB via `httptest.NewServer(server.Router())`
  (and 503 with no leak after the underlying pool is closed). Test isolation
  is provided by the migration down→up reset at the start of each test — no
  manual `DROP TABLE` is performed.
- GitHub Actions: a dedicated Go job (named `go auth / format / vet / test /
  build`) is added to the existing CI workflow. It runs `gofmt -l`, `go vet`
  (with and without the `integration` tag), `go test -race`, `go build`, then
  builds the auth Docker image on the GitHub Runner via
  `docker build -f services/auth/Dockerfile -t tokenmp-v3-auth:<sha> .` (build
  only — the image is not run, not pushed to any registry, not published, not
  cached as a long-lived artifact; the Ubuntu runner provides Docker, so no
  local Docker is required on developer machines), then runs the migration
  up/down/up cycle, and the integration suite against a PostgreSQL 17 service
  container. The `go build` step does **not** use `-tags=integration` because
  the integration-tagged packages are already compiled by `go vet
  -tags=integration` and by the integration test run. `setup-go` and
  `checkout` are pinned to immutable SHAs; the postgres image is pinned to
  `17-alpine`. The job references no repository secrets, so fork PRs are
  covered. Existing Node/gitleaks jobs are preserved unchanged.

### Docker

- A multi-stage `services/auth/Dockerfile` builds the `auth` and `healthcheck`
  binaries from the root context (`docker build -f services/auth/Dockerfile .`).
  Per the module autonomy rule in `.agents/monorepo.md`, the Dockerfile lives
  inside the module directory, not at the repository root. The builder stage
  uses `FROM --platform=$BUILDPLATFORM` so Go cross-compiles to the target via
  `GOOS`/`GOARCH`; `TARGETOS`/`TARGETARCH` are not given a wrong `amd64`
  default — when a plain build does not inject them, the shell falls back to
  Go's native host arch. The final stage is `alpine:3.22`, non-root
  (`tokenmp` UID/GID 10001), with `ca-certificates` and `tini` only. The image
  does **not** include `migrate` and does **not** run migrations. Image
  digests are not hard-coded in the Dockerfile; pinning is left to the deploy
  pipeline to verify.
- The `HEALTHCHECK` uses the in-image `/usr/local/bin/healthcheck` binary
  against `/healthz`, so the runtime image does not depend on `curl` or
  `wget`.
- Image name: `tokenmp-v3-auth:<sha>` per `.agents/docker.md`.
- Root `.dockerignore` excludes secrets, VCS, Node.js outputs, caches and
  documentation, but **not** `go.mod`, `go.sum`, `go.work`, the auth service
  source or the migrations.

### Contract

- `api/openapi.yaml` declares only `/healthz` and `/readyz` with a uniform
  `HealthResponse`, including explicit HEAD operations that carry the same
  status and `Cache-Control: no-store` header but no body, plus the
  `Cache-Control` header on the GET responses. No speculative endpoints. No
  consumers exist yet; none are described as integrated.

## Consequences

- The repository now has a Go workspace and its first Go service. ADR 0001's
  "Do not create `go.work` until the first Go module is introduced" is
  satisfied; `monorepo.md` "Go workspace尚未创建" and related pending states
  are updated.
- Future Go services follow the same module-per-service pattern, added by
  dedicated changes with their own `go.work` entries and module `AGENTS.md`.
- Adding registration/login/JWT/Argon2 in future PRs does not require schema
  changes — the rotation columns and CHECK constraints already exist.
- CI grows from two jobs (Node verify + gitleaks) to three by adding the
  independent Go job; the Node/gitleaks jobs are preserved.
- The runtime image stays minimal and non-root; production deploy/migration
  remains out of scope and is not implemented.

## Alternatives Considered

- **citext for email:** rejected. Explicit `LOWER(BTRIM(email))` keeps types
  plain, avoids extension/collation drift, and is reason-about-able by any
  driver.
- **bcrypt-only, no Argon2id plan:** rejected. Argon2id is the OWASP-recommended
  long-term target; documenting the progressive upgrade avoids a later schema
  redesign.
- **Running migrations at container start:** rejected. Migrations are a
  deployment-time concern; running them in every replica risks races and
  violates the "image has no migration side effects" boundary. CI/ops apply
  them out-of-band.
- **Including `migrate` in the runtime image:** rejected. The image should
  not ship migration tooling; doing so widens the attack surface and tempts
  startup-time migrations.
- **Hand-rolled HTTP router over Chi:** rejected. Chi is a small, stdlib-
  compatible router with mature middleware; re-implementing it adds risk for
  no benefit.
- **AutoMigrate:** rejected. GORM AutoMigrate cannot express CHECK
  constraints, expression indexes or `pgcrypto`, and would silently diverge
  from the authoritative migrations.
