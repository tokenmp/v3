# TokenMP v3

TokenMP v3 is an incremental, multi-language Monorepo: a Next.js web app, a shared contract package, a UI design-tokens package, and several Go backend services (auth, executor, config, logging, notice, billing, and the Edge/BFF api gateway). Infrastructure and repository-tool modules are intentionally minimal.

## Toolchain

- Node.js 26.4.0
- pnpm 11.15.0
- Turborepo 2.10.5
- TypeScript 6.0.3
- Go 1.26.5 (for Go services; workspace at `go.work`, modules `github.com/tokenmp/v3/services/{auth,executor,api,config,logging,notice,billing}`)

Install the pinned local toolchain with mise, then install dependencies:

```bash
mise install
pnpm install --frozen-lockfile
```

## Workspace

The workspace contains top-level logical partitions with scoped `AGENTS.md` guidance:

```text
apps/       # application modules; currently contains the Next.js web app
services/   # backend service modules: auth, executor, api, config, logging, notice, billing
packages/   # shared packages: ui-tokens and contracts
infra/      # infrastructure modules; currently contains db migrations only
tools/      # repository tools; currently empty
docs/       # shared project documentation and ADRs
```

Implemented modules: `apps/web` (Next.js admin + user panel), `packages/ui-tokens`, `packages/contracts`, `services/auth`, `services/executor`, `services/api` (Edge/BFF), `services/config`, `services/logging`, `services/notice`, and `services/billing`.

`packages/contracts/` is the language-neutral API contract package (`@tokenmp/contracts`) and the single source of truth for service OpenAPI contracts (Auth, Executor, Notice; Logging and Config are documented design-time contracts). Executor has seven runtime-actual generated routes: anonymous health; authenticated Chat/Messages non-stream or `stream:true` SSE; legacy Images completion-only non-stream execution; Models/Responses are runtime-enabled (Phase 13/14).

`services/auth/` implements registration, login, Ed25519/EdDSA access tokens, opaque refresh-token rotation/reuse detection, logout, `/me`, and Argon2id hashing with bcrypt upgrade. `services/executor/` is a Mock-first Go Foundation with strict config/snapshot/routing/adapter execution, per-attempt credential resolution, exact SDK registries, transport-neutral facades, one quota reservation plus frozen-policy retry in Runner, runtime composition before `net.Listen`, and hot-reload of compiled snapshots from the Config Service.

## Commands

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

These commands validate the root Node.js task graph and the two workspace
packages (UI Tokens and Contracts).
The Auth and Executor services are independent Go modules and are **not** part of the pnpm/Turborepo task graph. Validate them with `go` directly from their module directories (see their respective READMEs). CI currently has a dedicated Go job for Auth; Executor has no independent CI job of its own, but the existing `go-auth` job runs its generated-code freshness gate, generated transport/route conformance race tests, composition/config/app/process race tests, and pure-Go adapter/compiler/snapshot race tests.

## Continuous Integration

GitHub Actions runs a minimal CI baseline on every pull request and on pushes to `main`. The workflow lives at `.github/workflows/ci.yml` and is intentionally repository-scoped: no deployment, release, or publish job is included.

Implemented checks:

- **lint / typecheck / test / build** — installs dependencies with `pnpm install --frozen-lockfile` on Node.js 26.4.0, then runs the root `lint`, `typecheck`, `test`, and `build` scripts in order. The pinned pnpm 11.15.0 is installed via `pnpm/action-setup` before `actions/setup-node`, which then caches the pnpm store without any secret.
- **gitleaks** — scans the full history with the open-source Gitleaks CLI at a fixed version (v8.28.0). The runner downloads the official release tarball and its checksums file, verifies the tarball with `sha256sum`, installs the binary under `RUNNER_TEMP` (no system directories, no `sudo`), then runs `gitleaks git --redact --verbose --exit-code 1 .`. The workflow references no repository secret and no `GITHUB_TOKEN`, so pull requests from forks are scanned without any extra secret. The `gitleaks/gitleaks-action` wrapper is intentionally not used because it may require a `GITLEAKS_LICENSE` secret for organization repositories, which would break the baseline's no-extra-secret commitment.
- **go auth / format / vet / test / build** — the dedicated Go job for `services/auth`. It pins Go 1.26.5 via `actions/setup-go` and `checkout` at immutable SHAs, first runs Auth and Executor generated-code freshness gates (`check-generated.sh` and `check-generated-executor.sh`), then runs Executor generated transport/route conformance race tests and `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/execution/... ./internal/requestlog/... ./internal/quota/... ./internal/sdk/... ./internal/configsource/... ./internal/credentialenv/... ./internal/identityenv/... ./internal/quarantinebridge/... ./internal/nonstream/... ./internal/nonstreamfacade/... ./internal/authcontext/... ./internal/requestid/... ./internal/streaming/... ./internal/composition/... ./internal/config/... ./internal/app/... ./cmd/executor/...` from `services/executor`. The command is limited to module packages: it does not run a database, live provider, or remote request pipeline, but it now also covers runtime composition wiring — `./internal/composition/...` runs the contract-enumerated wrapped-handler route conformance (asserts anonymous/authenticated status for every OpenAPI operation through the full `AuthMiddleware(CaptureRawBody(...))` handler) and `./cmd/executor/...` runs the process binary test (actual process startup: health, unauthenticated chat 401, authenticated empty-config chat 404 and 501 routes, invalid config proving no listener bind) — alongside the internal non-stream Runner with Mock/InMemory/fake tests and the strict secret-free config file loader, while SDK HTTP tests use only local TLS `httptest` servers. `./internal/quarantinebridge/...` is listed explicitly because it is a separate package from `./internal/routing/...`; the routing race pattern does not automatically test it, so it must appear in the package list to be covered. The job then runs Auth `gofmt -l`, `go vet`, `go test -race`, and `go build`. It builds the auth Docker image on the GitHub Runner via `docker build -f services/auth/Dockerfile -t tokenmp-v3-auth:<sha> .` (build only — the image is neither run nor pushed nor published; the Ubuntu runner provides Docker, so no local Docker is required on developer machines), then runs the migration up/down/up cycle and the `integration`-tagged integration test against a PostgreSQL 17 service container (`postgres:17-alpine`). The `golang-migrate` CLI is installed at `v4.18.3` under `RUNNER_TEMP` (no `sudo`, no system directories). The job references no repository secret, so fork pull requests are covered. There is no independent Executor CI job in this job; runtime business routes are now registered and exercised by the composition route conformance and process binary tests, but the OpenAI Chat and Anthropic Messages non-stream SDK adapters still call only local TLS `httptest` servers (no live provider). Phase 8 `internal/streaming` foundation is race-tested but remains unwired: no SDK/provider stream, transport/composition integration, or real `stream:true` runtime behavior. The job is independent of the Node.js task graph and does not alter the existing verify/secrets-scan jobs.

The workflow requests the minimum permission `contents: read` and cancels superseded runs on the same ref. CI checks are the only implemented automation; continuous delivery and deployment are not implemented.

A dedicated [E2E smoke workflow](.github/workflows/e2e.yml) runs on pull requests and manual dispatch. It installs only Chromium and runs the credential-free local mock smoke project on an isolated loopback Next server; it does not accept a target URL, credentials, or API keys and never runs the live E2E suite. The normal `verify` job continues to exclude `tokenmp-v3-e2e`. See [`e2e/README.md`](e2e/README.md) for the explicit-`BASE_URL` live-suite procedure.

## Services & Ports

All Go services read their listen address and database DSN from environment variables; the defaults below come from each service's `internal/config`. Production/dev deployments override them via env (notably the dev box runs Notice on 8086 and Config on 8084 to avoid collisions).

| Service | Module | Default listen addr (env) | PostgreSQL database (env) | Role |
|---|---|---|---|---|
| Auth | `services/auth` | `:8080` (`AUTH_HTTP_ADDR`) | `tokenmp_auth` (`AUTH_DATABASE_URL`) | User identity: register/login, Ed25519 JWT issuance, refresh-token rotation, `/me`, Argon2id hashing |
| Edge / BFF | `services/api` | `127.0.0.1:3002` (`API_HTTP_ADDR`) | none (pure gateway) | Verifies JWT locally, reserves quota, reverse-proxies `/v1/*` to Executor, aggregates admin calls to Auth/Logging/Config |
| Executor | `services/executor` | `127.0.0.1:8081` (`EXECUTOR_HTTP_ADDR`) | none | Calls upstream providers (OpenAI/Anthropic) via official SDKs; pulls compiled config from Config Service with hot-reload |
| Config | `services/config` | `:8082` (`CONFIG_HTTP_ADDR`) | `tokenmp_config` (`CONFIG_DATABASE_URL`) | CRUD for models/providers/routes/adapters/credentials/global_config; compiles and publishes immutable snapshots |
| Logging | `services/logging` | `:8083` (`LOGGING_HTTP_ADDR`) | `tokenmp_logging` (`LOGGING_DATABASE_URL`) | Ingests request logs/attempts/events from Executor; serves admin query/stats/dashboard |
| Notice | `services/notice` | `:8081` (`NOTICE_HTTP_ADDR`) | `tokenmp_biz` (`NOTICE_DATABASE_URL`) | Project announcements/changelogs/notifications; reuses Auth Ed25519 public key for local JWT verification |
| Billing | `services/billing` | `:8085` (`BILLING_HTTP_ADDR`) | `tokenmp_billing` (`BILLING_DATABASE_URL`) | Quota/balance; balances are computed values |
| Web | `apps/web` | `:3100` (`next dev/start -p 3100`) | none | Next.js admin panel + user panel; all API calls are same-origin (`/v1`, `/api/v1`) |

> **Port collision warning**: the `Executor` default (`127.0.0.1:8081`) and the `Notice` default (`:8081`) both bind 8081. The Edge BFF (`services/api`) expects Executor on 8081, so on hosts that also run Notice you must override `NOTICE_HTTP_ADDR` (dev uses 8086). Similarly, `Config` defaults to 8082 which the public openresty also used historically; dev overrides `CONFIG_HTTP_ADDR` to 8084.

### Edge routing

The dev box exposes a single public entry on `:80` via the system openresty (managed by one-panel). All API calls are same-origin from the browser's perspective; clients only see `/v1/*` (executor model requests) and `/api/v1/*` (business). openresty dispatches by path prefix to the backend services:

- `/v1/*` → Edge/BFF (`:3002`), which applies quota then proxies to the Executor
- `/api/v1/auth/*` → Auth (`:8080`)
- `/api/v1/notice/*` → Notice (`:8086`)
- `/api/v1/*` → Edge/BFF (`:3002`) for business routes (user/keys/admin/request-logs/plans)
- `/healthz` → Edge/BFF (`:3002`)
- `/*` → Web (`:3100`, Next.js)

The legacy `:8082` nginx container has been retired; `:80` is the sole public entry.

## Internal Communication Flows

### 1. Chat / Messages request (client → provider)

```text
browser (Bearer JWT)
  → openresty :80 /v1/chat/completions
  → services/api (Edge/BFF)
       identity.Middleware      : local Ed25519 JWT verify (iss/aud/exp/sub/role)
       quota.Middleware         : reserve quota against Billing Service
       proxy.ServeHTTP          : reverse-proxy to Executor
  → services/executor
       AuthMiddleware           : verify Bearer (JWT if EXECUTOR_JWT_PUBLIC_KEY_FILE set,
                                  else EXECUTOR_IDENTITY_MAP_JSON API-key map);
                                  X-User-ID header (set by Edge from verified
                                  claims.Subject) overrides the resolved subject
                                  so request logs record the real user.
       CaptureRawBody → nonstreamfacade / streamfacade
       Runner                  : per-attempt credential resolve → routing Plan →
                                  SDK adapter (OpenAI / Anthropic) → upstream
  → upstream provider (HTTPS, retry=0, no redirect)
```

Key header handling in `services/api/internal/proxy/proxy.go`:

- Rewrites scheme/host to the Executor URL and sets `Host`.
- When `serviceToken` (Edge identity) is configured: overwrites `Authorization: Bearer <serviceToken>`; otherwise passes the client JWT through.
- Deletes any client-supplied `X-User-ID`, then sets `X-User-ID: <verified claims.Subject>` from the identity context (defense against spoofing).
- Strips hop-by-hop headers.

Executor identity source selection lives in `services/executor/internal/composition/composition.go`: `EXECUTOR_JWT_PUBLIC_KEY_FILE` non-empty → JWT verifier primary; otherwise `EXECUTOR_IDENTITY_MAP_JSON` API-key map fallback. There is no JWT passthrough from Edge to Executor for chat traffic — Edge is the only trusted caller and its `X-User-ID` is authoritative.

### 2. Executor config pull & hot-reload

```text
services/executor (cmd/executor main)
  → composition.Build
       EXECUTOR_CONFIG_SERVICE_URL set?
         yes → configsource.CompileAndPublishInitialFromConfigService
                 GET <url>/v1/config/snapshots/latest   (10s, no redirect, ≤2 MiB)
                 DisallowUnknownFields + ScanSecrets → snapshot.Compile → store.Publish(gen=1)
         no  → configsource.CompileAndPublishInitial (EXECUTOR_CONFIG_FILE on disk)
  → store.Current() drives resolvers, SDK registry, route registration
  → hot-reload loop:
       SIGHUP                        → Reloader.Reload(ctx)
       EXECUTOR_CONFIG_RELOAD_INTERVAL (e.g. 10s, default 0=off)
                                    → stat mtime+size poll → Reloader.Reload(ctx)
         Reloader: load → if revision unchanged no-op → snapshot.Compile →
                   validate (rejectUnsupportedEnabledRoutes,
                             credentialenv.ValidateCompiled) → store.Publish(gen+1)
         logs: "config reload: success gen N→N+1 rev=..."
```

Admins publish a new revision through the Config Service admin API (`POST /v1/config/admin/compile`); the Executor picks it up on the next SIGHUP or poll.

### 3. Retry policy compilation (global → adapter → provider → route)

Config Service (`services/config/internal/server/compile.go`) reads `global_config` JSONB keys (`default_retry`, `default_timeout`, `auto_model_ids`) plus the `models`/`providers`/`routes`/`adapters`/`upstream_credentials` tables and emits a `wireConfigSnapshot`. `resolveGlobalRetry` falls back to code defaults when the DB value is missing or invalid. Auto-generated adapters (`autoGenerateAdapter`) intentionally leave `Retry`/`Timeout` empty so they inherit the global policy.

Executor (`services/executor/internal/adapter/compiler.go`) compiles retry with a four-layer override chain — each layer's non-nil `Rules` **replace** (not merge) the parent's:

```text
code defaults (MaxTotalAttempts=3, MaxSameTargetAttempts=2, 45s, 500ms)
  → global  (from snapshot Global.Retry)
    → adapter
      → provider
        → route  (final policy used by Runner)
```

An explicit opt-out (`MaxTotalAttempts: 0`) at the adapter or provider layer disables retries for all routes under it and cannot be re-enabled by a route. Hard caps: `MaxTotalAttempts ≤ 10`, `MaxSameTargetAttempts ≤ 5`, `MaxTotalDuration ≤ 90s`, `Backoff ≤ 1m`. Retry actions: `same_credential` (same route+credential, suited to 503 transient overload, bounded by `MaxSameTargetAttempts`), `next_credential` (429 rate limit), `next_route` (5xx upstream fault), `next_provider`, `next_model`, `none`. Admins edit the global policy on the `/admin/retry` page and click "编译并发布" to publish a new snapshot the Executor hot-reloads.

### 4. Request log ingestion (Executor → Logging → admin)

```text
services/executor Runner (r.Logger.RecordExecution per lifecycle event)
  → logsink.RemoteSink (wraps an in-memory ExecutionPort)
       buildBatch(event) → POST <LoggingServiceURL>/v1/logs/ingest
         (background ctx, 10s, no redirect, ≤4096 B response discard,
          errors swallowed + slog.Warn, never blocks the request path)
  → services/logging
       handleIngest: MaxBytesReader ≤2 MiB, DisallowUnknownFields
       repository.IngestBatch (single tx):
         upsert request_logs    ON CONFLICT (request_id) DO UPDATE
         insert request_attempts (one row per attempt_index)
         insert request_log_events (timeline)

admin query path:
  browser → services/api (Edge, RequireAdmin)
    → logging.Client.ListLogs / GetLog / GetStats / GetDashboard
    → services/logging GET /v1/logs, /v1/logs/{id}, /v1/logs/stats, /v1/logs/dashboard
```

`X-User-ID` set by Edge becomes the `user_id` recorded on `request_logs`, so admin views show the real end user rather than the Edge service identity.

### 5. Notice auth (no token issuance)

`services/notice` reuses the Auth Ed25519 public key (`NOTICE_JWT_PUBLIC_KEY_FILE`) for **local verification only** — it never issues tokens. The JWT `sub` is treated as a loose reference to `auth.users.id` (cross-database, no FK). Notifications carry a generic data-driven `action` affordance (`{type:"link",label,href}`) that clients render themselves; unknown types are ignored gracefully.

## Agent guidance

Read `AGENTS.md`, then read each nested `AGENTS.md` from the repository root to the target module before making changes.

## Implemented modules

- [`apps/web`](apps/web): Next.js web app — admin panel (users/keys/request-logs/retry-policy/billing/usage/notice/settings) and user panel (keys/usage). All API calls are same-origin (`/v1` for executor model requests, `/api/v1` for business); openresty on `:80` dispatches by prefix to the backend services.
- [`@tokenmp/ui-tokens`](packages/ui-tokens/README.md): framework-neutral Design Tokens with Tailwind CSS v4 and shadcn integration exports.
- [`@tokenmp/contracts`](packages/contracts/README.md): language-neutral API contract package and single source of truth. Auth, Executor, and Notice OpenAPI contracts are generated into Go strict servers; Logging and Config are documented design-time contracts. Generated outputs are committed and checked by the existing `go-auth` CI job.
- [`services/auth`](services/auth/README.md): Auth Service — Go 1.26.5, Chi, GORM, PostgreSQL (`tokenmp_auth`). Registration, login, Ed25519 (EdDSA) access-token issuance, opaque refresh-token rotation with reuse detection, logout, `/me`, Argon2id hashing with bcrypt legacy upgrade.
- [`services/executor`](services/executor/README.md): Executor Service — Go 1.26.5. Runtime composition assembles strict config/snapshot/routing, exact completion and stream registries, Runner/facades, generated transport, auth, and hot-reload before listening. Chat/Messages execute non-stream or `stream:true` SSE; legacy Images is completion-only non-stream via `/v1/images/generations`; Models/Responses are runtime-enabled. Pulls compiled snapshots from the Config Service and hot-reloads on SIGHUP or poll.
- [`services/api`](services/api/AGENTS.md): Edge/BFF — Go 1.26.5, Chi. Verifies JWT locally (Ed25519), reserves quota against Billing, reverse-proxies `/v1/*` to the Executor (injecting `X-User-ID` from verified `claims.Subject`), and aggregates admin calls to Auth/Logging/Config.
- [`services/config`](services/config/AGENTS.md): Config Service — Go 1.26.5, GORM, PostgreSQL (`tokenmp_config`). CRUD for models/providers/routes/adapters/credentials/global_config; compiles and publishes immutable snapshots consumed by the Executor.
- [`services/logging`](services/logging/AGENTS.md): Logging Service — Go 1.26.5, GORM, PostgreSQL (`tokenmp_logging`). Ingests request logs/attempts/events from the Executor via `POST /v1/logs/ingest`; serves admin query/stats/dashboard.
- [`services/notice`](services/notice/AGENTS.md): Notice Service — Go 1.26.5, GORM, PostgreSQL (`tokenmp_biz`). Project announcements/changelogs/notifications; reuses the Auth Ed25519 public key for local JWT verification (no token issuance).
- [`services/billing`](services/billing/AGENTS.md): Billing Service — Go 1.26.5, GORM, PostgreSQL (`tokenmp_billing`). Quota/balance management for the Edge reserve/finalize flow.

## Architecture decisions

- [ADR 0001: Monorepo Tooling](docs/adr/0001-monorepo-tooling.md)
- [ADR 0002: UI Design Tokens](docs/adr/0002-ui-design-tokens.md)
- [ADR 0003: CI Baseline](docs/adr/0003-ci-baseline.md)
- [ADR 0004: Auth Service Foundation](docs/adr/0004-auth-service-foundation.md)
- [ADR 0005: Auth Identity Flows](docs/adr/0005-auth-identity-flows.md)
- [ADR 0006: API Contracts Package](docs/adr/0006-api-contracts-package.md)
- [UI Design System](docs/ui/design-system.md)

### Container Compose

The repository-root [`compose.yaml`](compose.yaml) builds and orchestrates the seven Go
services and Web app under the fixed `tokenmp-v3` Compose project. It deliberately does
**not** create or manage PostgreSQL, Redis, OpenResty, or any other shared infrastructure.
Database DSNs, credential mappings, and JWT key-file paths are required deployment inputs;
key files are injected read-only and no `.env` file is committed. Containers use the
internal `tokenmp-v3-backend` network; only Edge/BFF (`3002`, configurable with
`TOKENMP_V3_API_HOST_PORT`) and Web (`3100`, configurable with
`TOKENMP_V3_WEB_HOST_PORT`) are published by default.

Use a temporary, environment-owned env file to render the configuration before an
operator starts it:

```bash
docker compose --env-file /tmp/tokenmp-v3.compose.env -p tokenmp-v3 config
docker compose -p tokenmp-v3 build
```

For portable external-infrastructure access, supply DNS names reachable from both Linux
Docker Engine and Docker Desktop in the DSN/URL inputs. Do not commit host-gateway,
private-address, or environment-specific Compose overrides. CI statically verifies the four
new service Dockerfile `COPY` sources against the repository-root build context, renders
Compose with disposable placeholder files/values, and builds all seven Go service images
without running or pushing them.

### Runtime contract

Auth and API use the exact `AUTH_RATE_LIMIT_*` / `API_RATE_LIMIT_*` names when rate
limiting is enabled: enabled flag, external Redis address/DB, trusted-proxy CIDRs,
read-only HMAC-secret file, policy capacity/refill values and bucket TTL. Redis remains
external; Compose neither creates it nor uses obsolete `*_REDIS_URL`/`*_HMAC_SECRET_FILE`
aliases.

The Config admin secret is mounted read-only as `CONFIG_ADMIN_TOKEN_FILE` (consumed by the
Config Service loader). API and Billing are also mounted with read-only
`API_CONFIG_SERVICE_TOKEN_FILE` and `BILLING_LOGGING_SERVICE_TOKEN_FILE` paths, consumed by
the API and Billing `internal/config` secret-file loaders respectively, while Billing is
wired to `BILLING_LOGGING_URL=http://logging:8083` with explicit strict sweeper defaults.
Never pass a file path as a token value, use a secret-reading entrypoint wrapper, or
interpolate a token into Compose environment/rendered configuration.

Deployment source files are mounted read-only under `/run/secrets`; neither their contents nor
external Redis/PostgreSQL resources are committed or created. CI checks this contract with
`tools/check-compose-env-contract.sh`, renders using temporary sentinel secret files, and fails
if the sentinel appears in rendered Compose output.
