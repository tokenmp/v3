# TokenMP v3

TokenMP v3 is an incremental, multi-language Monorepo. Repository tooling, two shared packages, and two Go service modules are implemented; application, infrastructure, and tool modules remain intentionally absent.

## Toolchain

- Node.js 26.4.0
- pnpm 11.15.0
- Turborepo 2.10.5
- TypeScript 6.0.3
- Go 1.26.5 (for Go services; workspace at `go.work`, modules `github.com/tokenmp/v3/services/auth` and `github.com/tokenmp/v3/services/executor`)

Install the pinned local toolchain with mise, then install dependencies:

```bash
mise install
pnpm install --frozen-lockfile
```

## Workspace

The workspace contains top-level logical partitions with scoped `AGENTS.md` guidance:

```text
apps/       # application modules; currently empty
services/   # backend service modules; currently contains auth and executor
packages/   # shared packages; currently contains ui-tokens and contracts
infra/      # infrastructure modules; currently empty
tools/      # repository tools; currently empty
docs/       # shared project documentation and ADRs
```

The repository has four implemented modules: `packages/ui-tokens`, `packages/contracts`, `services/auth`, and `services/executor`. `packages/contracts/` is the language-neutral API contract package (`@tokenmp/contracts`) and the single source of truth for service OpenAPI contracts. Executor has seven runtime-actual generated routes: anonymous health; authenticated Chat/Messages non-stream or `stream:true` SSE; legacy Images completion-only non-stream execution; and only Models/Responses remain `501`.

`services/auth/` implements registration, login, Ed25519/EdDSA access tokens, opaque refresh-token rotation/reuse detection, logout, `/me`, and Argon2id hashing with bcrypt upgrade. `services/executor/` is a Mock-first Go Foundation with strict config/snapshot/routing/adapter execution, per-attempt credential resolution, exact SDK registries, transport-neutral facades, one quota reservation plus frozen-policy retry in Runner, and runtime composition before `net.Listen`. Its OpenAI Chat, Anthropic Messages, and legacy OpenAI Images completion registry is runtime-enabled; Images is completion-only non-stream and uses shared `internal/imagecontract` validation (untrimmed nonempty 1 MiB prompt, 512-byte CTL-free user, default wire/renderer `url`, 16 MiB wire/10 MiB item/12 MiB aggregate, URL/streaming base64/usage/extensions/revised-prompt bounds). Every Images terminal response is `Cache-Control: no-store`; GPT Image-specific parameters and usage quota are unsupported. Chat/Messages `stream:true` is runtime SSE with auth-before-body-read, pre-commit native JSON errors and post-commit no fallback. Only `/v1/models` and `/v1/responses` remain authenticated `501`; non-goals include durable idempotency/replay, remote quota/credential resolver, `Retry-After`, public/provider E2E, database, Docker, hot reload, Auth JWT, and a standalone Executor CI job. The existing `go-auth` job performs generated freshness and explicit race coverage including the independent `./internal/imagecontract/...` package.

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

## Agent guidance

Read `AGENTS.md`, then read each nested `AGENTS.md` from the repository root to the target module before making changes.

## Implemented modules

- [`@tokenmp/ui-tokens`](packages/ui-tokens/README.md): framework-neutral Design Tokens with Tailwind CSS v4 and shadcn integration exports. No frontend app or component package is included yet.
- [`@tokenmp/contracts`](packages/contracts/README.md): language-neutral API contract package and single source of truth. Executor runtime registers all seven generated routes: anonymous health; authenticated Chat/Messages non-stream or SSE; legacy Images completion-only non-stream execution; only Models/Responses return `501`. Images uses the shared legacy contract/default `url`/bounded response semantics and always returns `Cache-Control: no-store`; GPT Image-specific parameters and usage quota are unsupported. Generated outputs are committed and checked by the existing `go-auth` CI job.
- [`services/auth`](services/auth/README.md): TokenMP v3 Auth Service — Go 1.26.5, Chi, GORM, PostgreSQL. Implements the auth identity flows: registration, login, Ed25519 (EdDSA) access-token issuance, opaque refresh-token rotation with reuse detection, logout, logout-all, `/me`, and Argon2id password hashing with bcrypt legacy upgrade.
- [`services/executor`](services/executor/README.md): TokenMP v3 Executor Foundation — Go 1.26.5. Runtime composition assembles strict config/snapshot/routing, exact completion and stream registries, Runner/facades, generated transport, and auth before listening. Chat/Messages execute non-stream or `stream:true` SSE; legacy Images is execution-registry/composition/transport enabled completion-only non-stream through `/v1/images/generations`, with facade→Runner one reservation plus frozen-policy retry. `internal/imagecontract` is the SDK/transport shared validator for the legacy default `url` and bounded image response; all Images terminal responses are no-store. Only Models/Responses retain `501`; CI explicitly race-tests `./internal/imagecontract/...` alongside the independent Executor packages.

## Architecture decisions

- [ADR 0001: Monorepo Tooling](docs/adr/0001-monorepo-tooling.md)
- [ADR 0002: UI Design Tokens](docs/adr/0002-ui-design-tokens.md)
- [ADR 0003: CI Baseline](docs/adr/0003-ci-baseline.md)
- [ADR 0004: Auth Service Foundation](docs/adr/0004-auth-service-foundation.md)
- [ADR 0005: Auth Identity Flows](docs/adr/0005-auth-identity-flows.md)
- [ADR 0006: API Contracts Package](docs/adr/0006-api-contracts-package.md)
- [UI Design System](docs/ui/design-system.md)
