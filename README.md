# TokenMP v3

TokenMP v3 is being developed as an incremental, multi-language Monorepo. This bootstrap establishes repository-level tooling only; no application, service, or shared package module is included yet.

## Toolchain

- Node.js 26.4.0
- pnpm 11.15.0
- Turborepo 2.10.5
- TypeScript 6.0.3

Install the pinned local toolchain with mise, then install dependencies:

```bash
mise install
pnpm install --frozen-lockfile
```

## Workspace

The workspace contains top-level logical partitions with scoped `AGENTS.md` guidance:

```text
apps/       # application modules; currently empty
services/   # backend service modules; currently empty
packages/   # shared packages; currently empty
infra/      # infrastructure modules; currently empty
tools/      # repository tools; currently empty
docs/       # shared project documentation and ADRs
```

The partition directories are repository structure, not implemented modules. No concrete app, service, package, infrastructure module, or tool is created until its scope, boundaries, dependencies, and module-level `AGENTS.md` are handled in a dedicated change.

## Commands

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

With no workspace modules, these commands validate the root task graph and complete without package tasks. Each module must add real scripts and tests when introduced.

## Agent guidance

Read `AGENTS.md`, then read each nested `AGENTS.md` from the repository root to the target module before making changes.

## Architecture decisions

- [ADR 0001: Monorepo Tooling](docs/adr/0001-monorepo-tooling.md)
