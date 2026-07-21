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

The repository has four implemented modules: `packages/ui-tokens`, `packages/contracts`, `services/auth`, and `services/executor`. `packages/contracts/` is the language-neutral API contract package (`@tokenmp/contracts`) and the single source of truth for service OpenAPI contracts; it contains Auth and Executor contracts. The Executor contract describes its intended public interface, but Executor public business routes have not been implemented.

`services/auth/` implements the full auth identity flows (registration, login, Ed25519/EdDSA access-token issuance, opaque refresh-token rotation with reuse detection, logout, logout-all, `/me`, and Argon2id password hashing with bcrypt legacy upgrade). `services/executor/` is a Mock-first Go Foundation module that implements the `/healthz` endpoint, validated runtime configuration, graceful shutdown, Mock/InMemory ports, and the quota-reservation terminal state machine. The Config compiler/snapshot phase is implemented: `internal/snapshot.Compile` delegates to `internal/adapter.Compile` to validate and normalize config, then publishes a revisioned immutable compiled value through `NewCompiledSnapshot` and `Store`. The compiler rejects invalid identities/references, incompatible provider/adapter/protocol combinations, invalid HTTPS base URLs, invalid finite DSL policy, conflicting response/retry rules with the same priority, specificity, and scope, invalid thinking/capability policies, invalid durations/limits, and fallback cycles; it normalizes inherited policies and deterministic rule/route ordering. Sanitized `fixtures/configs/{default,xfyun,anthropic}.json` are strictly decoded, security-checked, compiled, and published in fixture tests. C01–C27 compiler validation and immutability/determinism coverage is implemented. Compiler defaults are request `2m`, TTFT `45s`, stream idle `30s`, stream lifetime `10m`, retry backoff `200ms`, total attempts `3`, same-target attempts `2`, and total retry duration `90s`. The compiler is not connected to a runtime config source or request pipeline. The routing stage is implemented as a pure, revision-pinned capability: strict `model[:group][@provider]` selectors (`auto` may have a provider but never a group), deterministic resolver candidates, route-local non-secret credential configuration (including full-SHA-256 legacy `CredentialRef` synthesis), and fail-closed model/provider/route/credential quarantine reads. Public routing Candidate/Plan values expose only safe credential ID/priority, never a credential reference or secret. `Plan.Next` scopes candidate advancement but is not a RetryDecider. Phase 5's stateless, pure-Go, no-I/O Adapter Engine is also implemented: it strictly decodes JSON objects, executes every finite DSL action, accepts literals only for header/query injection, continues to reject `ValueRef`, and bounds thinking by the selected model. Its safe response mapping uses AND across populated dimensions, OR within a dimension, compiler-established rule order, and a fixed default. Atomicity, mutation isolation, race, and fuzz coverage are implemented. The response mapping remains module-local and is not wired into a pipeline. 已实施内部 shared `sdk` port 及官方 `github.com/openai/openai-go/v3` **v3.44.0** 的 OpenAI Chat Completions 非流式 adapter：每次调用独立校验 HTTPS target、上游 model 与 secret，SDK retry=0、禁止 redirect；对 Chat 请求执行严格契约验证（含 tools、vision 与 thinking 字段），安全注入与唯一 Bearer 鉴权，记录不含 URL/请求/响应/凭据的 attempt observer metadata；成功返回安全 request ID/status metadata，失败将 timeout、transport、protocol 与 HTTP 状态安全分类。TLS `httptest` 覆盖 target/header 隔离、retry/redirect、分类与无泄漏。与其并列，已实施官方 `github.com/anthropics/anthropic-sdk-go` **v1.58.0** 的 Anthropic Messages 非流式内部 adapter：每次调用独立校验 HTTPS target/path prefix、上游 model 与 opaque secret，使用 `WithoutEnvironmentDefaults`、SDK retry=0 且禁止 redirect；最终 transport 仅允许 per-call `x-api-key` 和固定 `anthropic-version`，并重建允许的 header/query。对 Messages 请求及成功响应执行严格 OpenAPI 形状验证，执行层的 target model 与 effective thinking 具有权威性，并覆盖 tools、vision 与 thinking；成功仅返回安全 status/request-ID metadata，失败安全分类为 timeout、transport、protocol 或 HTTP（含 Anthropic 529 overloaded→unavailable，并由 fixture 映射为 429）。TLS `httptest`、请求/响应 fuzz 覆盖 target/header/query/environment 隔离、retry/redirect、分类和无泄漏。 两种 SDK adapter 均未接入 pipeline 或 runtime routing，因此不是端到端业务能力。仍未实施 credential resolver/secret injector、RetryDecider/attempt budget、pipeline/runtime 业务路由、Responses、Images、streaming、数据库、Docker 或独立 Executor CI job。 Executor generated models/strict server, adapter skeleton, and route/HTTP conformance tests are implemented and committed; runtime business routes remain unregistered, business execution is not implemented, and SSE is generated-only. `check:generated:executor` is a required freshness gate in the existing go-auth CI job; no independent Executor CI job exists yet. Other partition directories remain repository structure rather than implemented modules. No additional app, service, package, infrastructure module, or tool is created until its scope, boundaries, dependencies, and module-level `AGENTS.md` are handled in a dedicated change.

## Commands

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

These commands validate the root Node.js task graph and the two workspace
packages (UI Tokens and Contracts).
The Auth and Executor services are independent Go modules and are **not** part of the pnpm/Turborepo task graph. Validate them with `go` directly from their module directories (see their respective READMEs). CI currently has a dedicated Go job for Auth; Executor has no independent CI job of its own, but the existing `go-auth` job runs its generated-code freshness gate, generated transport/route conformance race tests, and pure-Go adapter/compiler/snapshot race tests.

## Continuous Integration

GitHub Actions runs a minimal CI baseline on every pull request and on pushes to `main`. The workflow lives at `.github/workflows/ci.yml` and is intentionally repository-scoped: no deployment, release, or publish job is included.

Implemented checks:

- **lint / typecheck / test / build** — installs dependencies with `pnpm install --frozen-lockfile` on Node.js 26.4.0, then runs the root `lint`, `typecheck`, `test`, and `build` scripts in order. The pinned pnpm 11.15.0 is installed via `pnpm/action-setup` before `actions/setup-node`, which then caches the pnpm store without any secret.
- **gitleaks** — scans the full history with the open-source Gitleaks CLI at a fixed version (v8.28.0). The runner downloads the official release tarball and its checksums file, verifies the tarball with `sha256sum`, installs the binary under `RUNNER_TEMP` (no system directories, no `sudo`), then runs `gitleaks git --redact --verbose --exit-code 1 .`. The workflow references no repository secret and no `GITHUB_TOKEN`, so pull requests from forks are scanned without any extra secret. The `gitleaks/gitleaks-action` wrapper is intentionally not used because it may require a `GITLEAKS_LICENSE` secret for organization repositories, which would break the baseline's no-extra-secret commitment.
- **go auth / format / vet / test / build** — the dedicated Go job for `services/auth`. It pins Go 1.26.5 via `actions/setup-go` and `checkout` at immutable SHAs, first runs Auth and Executor generated-code freshness gates (`check-generated.sh` and `check-generated-executor.sh`), then runs Executor generated transport/route conformance race tests and `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/sdk/...` from `services/executor`. The compiler/snapshot/routing/SDK boundary command is limited to module packages: it does not run a database, runtime config source, request pipeline, or live provider; SDK HTTP tests use only local TLS `httptest` servers. The job then runs Auth `gofmt -l`, `go vet`, `go test -race`, and `go build`. It builds the auth Docker image on the GitHub Runner via `docker build -f services/auth/Dockerfile -t tokenmp-v3-auth:<sha> .` (build only — the image is neither run nor pushed nor published; the Ubuntu runner provides Docker, so no local Docker is required on developer machines), then runs the migration up/down/up cycle and the `integration`-tagged integration test against a PostgreSQL 17 service container (`postgres:17-alpine`). The `golang-migrate` CLI is installed at `v4.18.3` under `RUNNER_TEMP` (no `sudo`, no system directories). The job references no repository secret, so fork pull requests are covered. There is no independent Executor CI job, runtime business-route registration, or execution-pipeline test in this job; the implemented OpenAI Chat and Anthropic Messages non-stream SDK adapters remain module-local. The job is independent of the Node.js task graph and does not alter the existing verify/secrets-scan jobs.

The workflow requests the minimum permission `contents: read` and cancels superseded runs on the same ref. CI checks are the only implemented automation; continuous delivery and deployment are not implemented.

## Agent guidance

Read `AGENTS.md`, then read each nested `AGENTS.md` from the repository root to the target module before making changes.

## Implemented modules

- [`@tokenmp/ui-tokens`](packages/ui-tokens/README.md): framework-neutral Design Tokens with Tailwind CSS v4 and shadcn integration exports. No frontend app or component package is included yet.
- [`@tokenmp/contracts`](packages/contracts/README.md): language-neutral API contract package. Single source of truth for all service OpenAPI contracts; contains Auth Service v1 and the Executor contract. Services conform to contracts at design/build time; the package has zero runtime dependencies. Executor public business routes described by its contract are not implemented. Executor generated models/strict server, adapter skeleton, and route/HTTP conformance tests are implemented and committed; runtime business routes remain unregistered, business execution is not implemented, and SSE is generated-only. `check:generated:executor` is a required freshness gate in the existing go-auth CI job; no independent Executor CI job exists yet. Generated outputs are committed and validated by the CI freshness gate.
- [`services/auth`](services/auth/README.md): TokenMP v3 Auth Service — Go 1.26.5, Chi, GORM, PostgreSQL. Implements the auth identity flows: registration, login, Ed25519 (EdDSA) access-token issuance, opaque refresh-token rotation with reuse detection, logout, logout-all, `/me`, and Argon2id password hashing with bcrypt legacy upgrade.
- [`services/executor`](services/executor/README.md): TokenMP v3 Executor Foundation — Go 1.26.5. Implements `/healthz`, runtime configuration, graceful shutdown, Mock/InMemory ports, and quota-reservation terminal transitions. The Config compiler/snapshot phase is implemented: `internal/snapshot.Compile` → `internal/adapter.Compile` validates and normalizes config, and revisioned compiled values are atomically published through immutable `NewCompiledSnapshot`/`Store` views. The three sanitized fixtures (`default`, `xfyun`, `anthropic`) are strictly decoded, security-checked, compiled, and published under tests; C01–C27 validation/immutability/determinism coverage is implemented. Compiler defaults are `2m/45s/30s/10m/200ms/3/2/90s` (request/TTFT/idle/lifetime/backoff/total attempts/same-target attempts/total retry duration). The compiler is not wired to a runtime config source or request pipeline. The pure-Go routing stage is implemented: strict `model[:group][@provider]` parsing (`auto` has no group), route group/provider selector and route-local non-secret credential configuration, auto/model fallback, full-SHA-256 legacy `CredentialRef` synthesis, revision-pinned immutable Resolver/Plan, deterministic ordering, and fail-closed model/provider/route/credential quarantine. Public Candidate/Plan values contain only safe credential ID/priority, not credential references or secrets. `Plan.Next` scopes candidates only; it is not a RetryDecider. Phase 5's stateless pure-Go, no-I/O Adapter Engine is implemented with strict JSON, every finite DSL action, literal-only header/query injection, continued `ValueRef` rejection, model-bounded thinking, and safe response mapping (AND across dimensions, OR within a dimension, compiler order, fixed default); it has atomic/mutation/race/fuzz coverage, but response mapping is not wired into a pipeline. 已实施内部 shared `sdk` port 及官方 `github.com/openai/openai-go/v3` **v3.44.0** 的 OpenAI Chat Completions 非流式 adapter：每次调用独立校验 HTTPS target、上游 model 与 secret，SDK retry=0、禁止 redirect；对 Chat 请求执行严格契约验证（含 tools、vision 与 thinking 字段），安全注入与唯一 Bearer 鉴权，记录不含 URL/请求/响应/凭据的 attempt observer metadata；成功返回安全 request ID/status metadata，失败将 timeout、transport、protocol 与 HTTP 状态安全分类。TLS `httptest` 覆盖 target/header 隔离、retry/redirect、分类与无泄漏。与其并列，已实施官方 `github.com/anthropics/anthropic-sdk-go` **v1.58.0** 的 Anthropic Messages 非流式内部 adapter：每次调用独立校验 HTTPS target/path prefix、上游 model 与 opaque secret，使用 `WithoutEnvironmentDefaults`、SDK retry=0 且禁止 redirect；最终 transport 仅允许 per-call `x-api-key` 和固定 `anthropic-version`，并重建允许的 header/query。对 Messages 请求及成功响应执行严格 OpenAPI 形状验证，执行层的 target model 与 effective thinking 具有权威性，并覆盖 tools、vision 与 thinking；成功仅返回安全 status/request-ID metadata，失败安全分类为 timeout、transport、protocol 或 HTTP（含 Anthropic 529 overloaded→unavailable，并由 fixture 映射为 429）。TLS `httptest`、请求/响应 fuzz 覆盖 target/header/query/environment 隔离、retry/redirect、分类和无泄漏。 两种 SDK adapter 均未接入 pipeline 或 runtime routing，因此不是端到端业务能力。仍未实施 credential resolver/secret injector、RetryDecider/attempt budget、pipeline/runtime 业务路由、Responses、Images、streaming、数据库、Docker 或独立 Executor CI job。 Executor generated models/strict server, adapter skeleton, and route/HTTP conformance tests are implemented and committed; runtime business routes remain unregistered, business execution is not implemented, and SSE is generated-only. `check:generated:executor` is a required freshness gate in the existing go-auth CI job; no independent Executor CI job exists yet. Generated outputs are committed and validated by the CI freshness gate.

## Architecture decisions

- [ADR 0001: Monorepo Tooling](docs/adr/0001-monorepo-tooling.md)
- [ADR 0002: UI Design Tokens](docs/adr/0002-ui-design-tokens.md)
- [ADR 0003: CI Baseline](docs/adr/0003-ci-baseline.md)
- [ADR 0004: Auth Service Foundation](docs/adr/0004-auth-service-foundation.md)
- [ADR 0005: Auth Identity Flows](docs/adr/0005-auth-identity-flows.md)
- [ADR 0006: API Contracts Package](docs/adr/0006-api-contracts-package.md)
- [UI Design System](docs/ui/design-system.md)
