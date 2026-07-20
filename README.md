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

`services/auth/` implements the full auth identity flows (registration, login, Ed25519/EdDSA access-token issuance, opaque refresh-token rotation with reuse detection, logout, logout-all, `/me`, and Argon2id password hashing with bcrypt legacy upgrade). `services/executor/` is a Mock-first Go Foundation module that implements the `/healthz` endpoint, validated runtime configuration, graceful shutdown, Mock/InMemory ports, and the quota-reservation terminal state machine. Executor generation configuration/scripts are pre-provisioned and experimental in contracts; `services/executor` has not generated, committed, or registered generated models/server, and `check:generated:executor` is not a current gate. Other partition directories remain repository structure rather than implemented modules. No additional app, service, package, infrastructure module, or tool is created until its scope, boundaries, dependencies, and module-level `AGENTS.md` are handled in a dedicated change.

## Commands

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

These commands validate the root Node.js task graph and the two workspace
packages (UI Tokens and Contracts).
The Auth and Executor services are independent Go modules and are **not** part of the pnpm/Turborepo task graph. Validate them with `go` directly from their module directories (see their respective READMEs). CI currently has a dedicated Go job for Auth; Executor has no CI job yet.

## Continuous Integration

GitHub Actions runs a minimal CI baseline on every pull request and on pushes to `main`. The workflow lives at `.github/workflows/ci.yml` and is intentionally repository-scoped: no deployment, release, or publish job is included.

Implemented checks:

- **lint / typecheck / test / build** — installs dependencies with `pnpm install --frozen-lockfile` on Node.js 26.4.0, then runs the root `lint`, `typecheck`, `test`, and `build` scripts in order. The pinned pnpm 11.15.0 is installed via `pnpm/action-setup` before `actions/setup-node`, which then caches the pnpm store without any secret.
- **gitleaks** — scans the full history with the open-source Gitleaks CLI at a fixed version (v8.28.0). The runner downloads the official release tarball and its checksums file, verifies the tarball with `sha256sum`, installs the binary under `RUNNER_TEMP` (no system directories, no `sudo`), then runs `gitleaks git --redact --verbose --exit-code 1 .`. The workflow references no repository secret and no `GITHUB_TOKEN`, so pull requests from forks are scanned without any extra secret. The `gitleaks/gitleaks-action` wrapper is intentionally not used because it may require a `GITLEAKS_LICENSE` secret for organization repositories, which would break the baseline's no-extra-secret commitment.
- **go auth / format / vet / test / build** — the dedicated Go job for `services/auth`. It pins Go 1.26.5 via `actions/setup-go` and `checkout` at immutable SHAs, runs `gofmt -l`, `go vet`, `go test -race`, `go build`, then builds the auth Docker image on the GitHub Runner via `docker build -f services/auth/Dockerfile -t tokenmp-v3-auth:<sha> .` (build only — the image is neither run nor pushed nor published; the Ubuntu runner provides Docker, so no local Docker is required on developer machines), then runs the migration up/down/up cycle and the `integration`-tagged integration test against a PostgreSQL 17 service container (`postgres:17-alpine`). The `golang-migrate` CLI is installed at `v4.18.3` under `RUNNER_TEMP` (no `sudo`, no system directories). The job references no repository secret, so fork pull requests are covered. The job is independent of the Node.js task graph and does not alter the existing verify/secrets-scan jobs.

The workflow requests the minimum permission `contents: read` and cancels superseded runs on the same ref. CI checks are the only implemented automation; continuous delivery and deployment are not implemented.

## Agent guidance

Read `AGENTS.md`, then read each nested `AGENTS.md` from the repository root to the target module before making changes.

## Implemented modules

- [`@tokenmp/ui-tokens`](packages/ui-tokens/README.md): framework-neutral Design Tokens with Tailwind CSS v4 and shadcn integration exports. No frontend app or component package is included yet.
- [`@tokenmp/contracts`](packages/contracts/README.md): language-neutral API contract package. Single source of truth for all service OpenAPI contracts; contains Auth Service v1 and the Executor contract. Services conform to contracts at design/build time; the package has zero runtime dependencies. Executor public business routes described by its contract are not implemented. Executor generation configuration/scripts are pre-provisioned and experimental in contracts; `services/executor` has not generated, committed, or registered generated models/server, and `check:generated:executor` is not a current gate.
- [`services/auth`](services/auth/README.md): TokenMP v3 Auth Service — Go 1.26.5, Chi, GORM, PostgreSQL. Implements the auth identity flows: registration, login, Ed25519 (EdDSA) access-token issuance, opaque refresh-token rotation with reuse detection, logout, logout-all, `/me`, and Argon2id password hashing with bcrypt legacy upgrade.
- [`services/executor`](services/executor/README.md): TokenMP v3 Executor Foundation — Go 1.26.5. Implements `/healthz`, runtime configuration, graceful shutdown, Mock/InMemory ports, and quota-reservation terminal transitions. It has no database, SDK, Docker image, CI job, or public model business routes yet. Executor generation configuration/scripts are pre-provisioned and experimental in contracts; `services/executor` has not generated, committed, or registered generated models/server, and `check:generated:executor` is not a current gate.

## Architecture decisions

- [ADR 0001: Monorepo Tooling](docs/adr/0001-monorepo-tooling.md)
- [ADR 0002: UI Design Tokens](docs/adr/0002-ui-design-tokens.md)
- [ADR 0003: CI Baseline](docs/adr/0003-ci-baseline.md)
- [ADR 0004: Auth Service Foundation](docs/adr/0004-auth-service-foundation.md)
- [ADR 0005: Auth Identity Flows](docs/adr/0005-auth-identity-flows.md)
- [ADR 0006: API Contracts Package](docs/adr/0006-api-contracts-package.md)
- [UI Design System](docs/ui/design-system.md)
