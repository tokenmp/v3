# TokenMP v3 Auth Service

The first Go service in the TokenMP v3 Monorepo. This PR is the **Auth
Foundation**: it stands up the service skeleton — Go module, HTTP server,
configuration, PostgreSQL connection, versioned SQL migrations, health and
readiness endpoints, graceful shutdown, Dockerfile, tests and CI — but does
**not** implement registration, login or JWT issuance. Those belong to a
follow-up change.

## Module status

- Module path: `github.com/tokenmp/v3/services/auth`
- Go version: 1.26.5 (pinned in root `mise.toml`; workspace `go.work` uses
  `./services/auth`)
- Runtime framework: Chi v5 for HTTP routing
- ORM: GORM with the PostgreSQL driver. **AutoMigrate is forbidden.** Schema
  is owned by versioned SQL migrations under `migrations/`, applied
  out-of-band by `golang-migrate` CLI.
- Logging: stdlib `log/slog`
- Configuration: stdlib `os`/`strconv`/`time` — no third-party config library
- Database: PostgreSQL, dedicated database named `tokenmp_auth`

## Scope of this foundation

Implemented:

- `cmd/auth` entrypoint: loads config, opens DB, starts HTTP server, performs
  graceful shutdown on SIGINT/SIGTERM
- `cmd/healthcheck`: standalone binary used as the Docker `HEALTHCHECK` so the
  runtime image does not depend on `curl`/`wget`
- `internal/config`: env-driven, fail-fast config with safe defaults
- `internal/database`: GORM Postgres open/close + Pinger for readiness
- `internal/database/models`: GORM entities mirroring the SQL schema
  (application-layer only; never migrated)
- `internal/handler`: `/healthz` and `/readyz` JSON handlers
- `internal/server`: Chi router with RequestID/RealIP/Recoverer middleware,
  configured `http.Server` timeouts
- `migrations/000001_create_users` and `migrations/000002_create_auth_sessions`
  (up/down)
- `api/openapi.yaml` contract for `/healthz` and `/readyz`
- Docker: multi-stage `services/auth/Dockerfile`; final stage is `alpine:3.22`,
  non-root (`tokenmp` UID/GID 10001), with `ca-certificates` and `tini` only.
- Unit tests (config + handler/server) and integration tests gated by the
  `integration` build tag
- GitHub Actions Go CI job (format/vet/test/build + migration cycle + integration)

Not implemented (intentionally deferred):

- Registration, login, refresh-token rotation, JWT issuance and Argon2id
- The `preferred_billing` and `fallback_enabled` fields are not part of Auth

## Security notes

- `AUTH_DATABASE_URL` is the single required secret-bearing env var. It is
  strictly validated: only `postgres`/`postgresql` URLs are accepted, and
  they must carry a host, a non-empty user and a path of exactly
  `/tokenmp_auth`. Any validation error reports only the failing rule — it
  never echoes the URL, the credentials, the host or the path. Defaults
  never include production credentials.
- Numeric and duration tunables (`AUTH_DB_MAX_*`, `AUTH_DB_CONN_MAX_LIFETIME`,
  `AUTH_SHUTDOWN_TIMEOUT`) fail fast on non-parseable or negative input; they
  never silently fall back to a default.
- Logs never print the connection string. `database.Open` returns stable
  classified errors (open failed / acquire *sql.DB failed / initial ping
  failed) that never wrap the driver's raw error, so the DSN cannot reach
  logs. `readyz` returns 503 on failure without leaking the underlying error
  text; HEAD responses write no body at all.
- `password_hash` stores bcrypt hashes today. The planned progressive upgrade
  to Argon2id is documented in `docs/adr/0004-auth-service-foundation.md` and
  will verify existing bcrypt hashes on next login, then re-hash with Argon2id.

## Environment variables

See `.env.example` for the full list and safe defaults. All variables use the
`AUTH_` prefix:

| Variable | Required | Default | Notes |
|---|---|---|---|
| `AUTH_DATABASE_URL` | yes | — | Single Postgres URL; never logged |
| `AUTH_HTTP_ADDR` | no | `:8080` | HTTP listen address |
| `AUTH_LOG_LEVEL` | no | `info` | `debug\|info\|warn\|error` |
| `AUTH_LOG_FORMAT` | no | `json` | `json\|text` |
| `AUTH_SHUTDOWN_TIMEOUT` | no | `30s` | Graceful shutdown budget |
| `AUTH_DB_MAX_OPEN_CONNS` | no | `25` | GORM/`sql.DB` pool |
| `AUTH_DB_MAX_IDLE_CONNS` | no | `5` | GORM/`sql.DB` pool |
| `AUTH_DB_CONN_MAX_LIFETIME` | no | `30m` | Connection max lifetime |
| `AUTH_HEALTHCHECK_ADDR` | no | `http://127.0.0.1:8080/healthz` | Used only by the in-image `healthcheck` binary |

## Schema

Migrations are the source of truth. Highlights and hard compatibility
constraints enforced by the schema:

- The legacy production `users` table is confirmed to use
  `UUID` / `VARCHAR` / `TEXT` / `TIMESTAMPTZ` types. These migrations
  preserve those types and the stored values exactly so they remain
  compatible with the production table.
- `users.email` is `VARCHAR(255)`; a CHECK requires `email <> '' AND
  email = LOWER(BTRIM(email))`, so every stored value is already the
  canonical lowercase-trimmed form (the application must normalize before
  insert; the CHECK is the backstop). Uniqueness is enforced on
  `LOWER(BTRIM(email))` via an expression unique index (retained by contract
  even though the CHECK makes a plain unique index equivalent). **Citext is
  not used.**
- `users.role` is `VARCHAR(16)` with `CHECK (role IN ('user','admin'))`,
  default `'user'`.
- `users.status` is `VARCHAR(16)` with `CHECK (status IN ('active','disabled'))`,
  default `'active'`.
- `users.token_version` is `INTEGER`, default `1`, `CHECK (token_version >= 1)`.
- `users.password_hash` is `TEXT` (matches the legacy production column;
  stores bcrypt) with `CHECK (password_hash <> '')`.
- `auth_sessions.refresh_token_hash` is `BYTEA` with a unique index and
  `CHECK (length(refresh_token_hash) > 0)`.
- `auth_sessions.token_family_id` is `UUID`, default `gen_random_uuid()`.
- `auth_sessions.ip` is `INET` (native Postgres inet; `auth_sessions` is a new
  Auth-owned table, not a legacy mirror).
- `auth_sessions.user_agent` is `TEXT` (unlimited; same new-table rationale as `ip`).
- `auth_sessions.revoke_reason` is `VARCHAR(64)` with an allow-list CHECK
  (`logout`, `logout_all`, `password_changed`, `admin_revoked`,
  `token_rotated`, `token_reuse`, `user_disabled`) and a consistency CHECK
  requiring `revoked_at` and `revoke_reason` to be both NULL or both set.
- `auth_sessions` carries `replaced_by_session_id` as a self-referential
  FK (`ON DELETE SET NULL`) preserved for future rotation semantics. The
  column name reads "this row was replaced BY session <id>": on rotation
  the OLD row is updated to set `replaced_by_session_id` to the NEW
  session's id and is revoked with `revoke_reason='token_rotated'`; new
  rows never carry this value. Also includes `expires_at`, timestamps,
  the `CHECK (expires_at > created_at)` and supporting indexes.
- `pgcrypto` is created by `000001` but is **not** dropped by the down
  migration (other schemas may depend on it).
- No seed data ships with migrations.

## Development and verification

**Local default is unit tests only.** The local machine must not install or
start PostgreSQL, and must not run `docker run postgres` / `brew install
postgresql` or anything similar. Local Docker is **not** required to verify
the Dockerfile — the auth image is built on the GitHub Runner as part of the
`go-auth` CI job (build only; not run, not pushed, not published). Real
migrations and integration tests also run **only** in GitHub Actions, against
an ephemeral `postgres:17-alpine` service container provisioned per run and
torn down afterwards (see the `go-auth` job in `.github/workflows/ci.yml`).

Run from the repository root. The local toolchain is pinned via `mise`:

```bash
mise install
```

Format, vet, unit test and build — no database required:

```bash
cd services/auth
gofmt -w .
go vet ./...
go vet -tags=integration ./...
go test -race ./...
go build ./...
```

`go test -race ./...` (without the `integration` tag) is the local test
command. The `integration`-tagged suite is **not** run locally; it is executed
by CI only, where it runs the migration cycle (up → down → up), validates
schema defaults, CHECK constraints, the email normalization invariant,
`auth_sessions` FKs and unique indexes, the revoke_reason allow-list and
revoked consistency, the INET ip / TEXT user_agent columns, the
`replaced_by_session_id` self-FK, and confirms `/readyz` returns 200 against
a live database via the real `Pinger` driven through `httptest.NewServer`.

### Connecting to a shared dev/preview PostgreSQL

If you ever need to point the service at a shared development or preview
PostgreSQL instance, you **must** use a dedicated, confirmed `tokenmp_auth`
database on that server. Never run tests, migrations or ad-hoc inserts
against a production database or any database shared with other services.
Obtain the confirmed dev/preview connection details out-of-band; do not
commit them, and do not write server addresses, SSH paths or credentials into
this repository or its `.agents/` docs (per `.agents/operations.md` and
`.agents/docker.md`).

The `golang-migrate` CLI is not needed locally; it is installed by CI at
`v4.18.3` (built with the `-tags 'postgres'` build tag) under `RUNNER_TEMP`.

## Migrations

Migrations live at `services/auth/migrations/` and are applied by the
`golang-migrate` CLI. The runtime image does **not** run migrations and does
**not** ship the `migrate` binary; migrations are applied by CI/ops before
deploying a new version, never at container start. The migration cycle
(up → down → up) and the integration suite are exercised by the GitHub
Actions `go-auth` job against an ephemeral `postgres:17-alpine` service
container — not on developer machines.

```bash
migrate -path services/auth/migrations \
  -database "$AUTH_DATABASE_URL" up
```

## Docker

Per the module autonomy rule in `.agents/monorepo.md`, the Dockerfile lives
at `services/auth/Dockerfile` (not at the repository root). The build context
is still the repository root so `go.mod`/`go.sum`/`go.work` and the service
source resolve correctly:

```bash
docker build -f services/auth/Dockerfile -t tokenmp-v3-auth:local .
```

The builder stage uses `FROM --platform=$BUILDPLATFORM` so Go cross-compiles
to the requested target via `GOOS`/`GOARCH` (BuildKit injects
`TARGETOS`/`TARGETARCH`; no wrong `amd64` default — when unset, the build
falls back to Go's native host arch). The image is named
`tokenmp-v3-auth:<sha>` per `.agents/docker.md`. The final stage is
`alpine:3.22` and runs as the non-root `tokenmp` user (UID/GID 10001). The
`HEALTHCHECK` uses the in-image `/usr/local/bin/healthcheck` binary against
`/healthz` — it does not depend on `curl` or `wget`. Image digests are not
hard-coded; pinning is left to the deploy pipeline.

### CI image build

The Dockerfile is validated end-to-end by the `go-auth` CI job, which runs
`docker build -f services/auth/Dockerfile -t tokenmp-v3-auth:<sha> .` on the
GitHub Runner right after `go build` and before the migration cycle. This is a
**build-only** check: the image is not run, not pushed to any registry, not
published, and not cached as a long-lived artifact. The Ubuntu runner provides
Docker out of the box, so **no local Docker is required** on developer
machines to validate the Dockerfile.

## Architecture references

- `AGENTS.md` (module-level)
- `../../AGENTS.md` (repository root)
- `../../docs/adr/0004-auth-service-foundation.md`
- `api/openapi.yaml`
