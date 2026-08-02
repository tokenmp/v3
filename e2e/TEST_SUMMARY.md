# TokenMP v3 E2E coverage summary

This document describes the current test split, not a current pass count. Historical totals and results are not evidence that the present suite passes.

## Local smoke gate

`tests/smoke/` is the credential-free, deterministic subset used by the dedicated E2E GitHub Actions workflow. Without `BASE_URL`, Playwright starts an isolated loopback Next server with mock Auth and Notice APIs and runs this subset only through the `chromium-smoke` or `Mobile Chrome-smoke` projects. `pnpm --filter tokenmp-v3-e2e run test:smoke` builds the ignored `@tokenmp/ui-tokens` distribution first, making a fresh checkout self-contained; the workflow also builds it explicitly before browser installation for fail-fast feedback. Its server wrapper preserves and atomically restores the exact pre-run bytes (or absence) of `apps/web/next-env.d.ts` and `apps/web/tsconfig.json` on normal exit and termination.

Current local smoke coverage:

- home page identity and same-origin Login/Register navigation;
- Login and Register form accessibility and controls;
- loopback-only browser request assertion for the home page.

This is a UI availability gate, not proof of Admin CRUD, authenticated Panel behavior, or real service integration.

## Explicit live suite

The remaining Admin, Panel, mobile, and specialized specs require an explicitly supplied `BASE_URL` (or protected `E2E_BASE_URL`) and a controlled test environment with provisioned test data. Authenticated user specs receive `E2E_USER_EMAIL`/`E2E_USER_PASSWORD`; Admin specs receive `E2E_ADMIN_EMAIL`/`E2E_ADMIN_PASSWORD`. Optional fixture inputs are `E2E_API_KEY`, `E2E_USER_ID`, and `E2E_PLAN_ID`. Admin and user describes conditionally skip with a clear reason when their required pair is absent, so a credential-free live list/run does not attempt placeholder authentication. Values must remain protected secrets and must never be committed. Some specs exercise data-mutating flows, so they are manual/target-owner responsibilities and are not enabled in regular CI.

Use the current file list and Playwright list output to establish the actual coverage before selecting a live subset:

```bash
find e2e/tests -name '*.spec.ts' -print | sort
BASE_URL=https://your-controlled-test-target.example \
E2E_USER_EMAIL=… E2E_USER_PASSWORD=… \
E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… \
  pnpm --filter tokenmp-v3-e2e exec playwright test --list
```

Do not record credentials, API keys, or public server addresses in this summary. See [README.md](README.md) for safe execution modes and CI behavior.
