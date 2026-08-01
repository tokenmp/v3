# TokenMP v3 E2E coverage summary

This document describes the current test split, not a current pass count. Historical totals and results are not evidence that the present suite passes.

## Local smoke gate

`tests/smoke/` is the credential-free, deterministic subset used by the dedicated E2E GitHub Actions workflow. Without `BASE_URL`, Playwright starts an isolated loopback Next server with mock Auth and Notice APIs and runs this subset only through the `chromium-smoke` or `Mobile Chrome-smoke` projects. Its server wrapper preserves and atomically restores the exact pre-run bytes (or absence) of `apps/web/next-env.d.ts` and `apps/web/tsconfig.json` on normal exit and termination.

Current local smoke coverage:

- home page identity and same-origin Login/Register navigation;
- Login and Register form accessibility and controls;
- loopback-only browser request assertion for the home page.

This is a UI availability gate, not proof of Admin CRUD, authenticated Panel behavior, or real service integration.

## Explicit live suite

The remaining Admin, Panel, mobile, and specialized specs require an explicitly supplied `BASE_URL` and a controlled test environment with provisioned test data. Some exercise authenticated and data-mutating flows, so they are manual/target-owner responsibilities and are not enabled in regular CI.

Use the current file list and Playwright list output to establish the actual coverage before selecting a live subset:

```bash
find e2e/tests -name '*.spec.ts' -print | sort
BASE_URL=https://your-controlled-test-target.example \
  pnpm --filter tokenmp-v3-e2e exec playwright test --list
```

Do not record credentials, API keys, or public server addresses in this summary. See [README.md](README.md) for safe execution modes and CI behavior.
