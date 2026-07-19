# ADR 0001: Monorepo Tooling

- Status: accepted
- Date: 2026-07-20

## Context

TokenMP v3 needs a multi-language Monorepo that can add applications, Node.js services, shared packages, and the Go executor incrementally. The first bootstrap must remain repository-scoped and must not pre-create unimplemented modules or select any module runtime framework.

## Decision

- Use pnpm workspaces for Node.js package management.
- Use Turborepo for repository task orchestration and caching.
- Use TypeScript for the initial Node.js workspace.
- Pin the local Node.js and pnpm toolchain in `mise.toml`.
- Create the top-level logical partitions and scoped `AGENTS.md` files as repository structure, without creating concrete modules.
- Do not include any application, service, or shared package module in the repository-tooling bootstrap.
- Introduce each concrete module in a dedicated change after confirming its scope and boundaries.
- Do not create `go.work` until the first Go module is introduced.
- Do not initialize a runtime framework during the workspace bootstrap.
- Require layered `AGENTS.md` files when each partition and module is actually created.

## Consequences

- The repository has one pnpm lockfile and one root task entry point.
- Each module remains responsible for its own scripts and runtime dependencies.
- Turborepo initially has no package tasks because partition directories are not workspace modules.
- Future Go modules remain autonomous and will not be managed by Node.js tooling.
- New apps, services, and packages are introduced in dedicated branches and PRs with accurate module documentation.

## Alternatives Considered

- npm or Yarn workspaces: viable, but pnpm provides stricter dependency boundaries and efficient workspace storage.
- Nx: capable but adds broader framework and generator conventions than this minimal bootstrap needs.
- Native workspace scripts only: simpler initially, but does not provide the intended task graph and caching foundation.
- Creating Auth or all planned modules now: rejected because module creation is separate from repository tooling and empty placeholders would imply boundaries and contracts that have not been designed.
