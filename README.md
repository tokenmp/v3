# TokenMP v3

TokenMP v3 is being developed as an incremental, multi-language Monorepo. Repository tooling and the shared UI Design Token package are implemented; application and service modules remain intentionally absent.

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
packages/   # shared packages; currently contains ui-tokens
infra/      # infrastructure modules; currently empty
tools/      # repository tools; currently empty
docs/       # shared project documentation and ADRs
```

`packages/ui-tokens/` is the first implemented module. Other partition directories remain repository structure rather than implemented modules. No additional app, service, package, infrastructure module, or tool is created until its scope, boundaries, dependencies, and module-level `AGENTS.md` are handled in a dedicated change.

## Commands

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

These commands validate the root task graph and the UI Token package. Each additional module must add real scripts and tests when introduced.

## Agent guidance

Read `AGENTS.md`, then read each nested `AGENTS.md` from the repository root to the target module before making changes.

## Implemented modules

- [`@tokenmp/ui-tokens`](packages/ui-tokens/README.md): framework-neutral Design Tokens with Tailwind CSS v4 and shadcn integration exports. No frontend app or component package is included yet.

## Architecture decisions

- [ADR 0001: Monorepo Tooling](docs/adr/0001-monorepo-tooling.md)
- [ADR 0002: UI Design Tokens](docs/adr/0002-ui-design-tokens.md)
- [UI Design System](docs/ui/design-system.md)
