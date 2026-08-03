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

The remaining Admin, Panel, mobile, and specialized specs require an explicitly supplied `BASE_URL` (or protected `E2E_BASE_URL`) and a controlled test environment. `E2E_BASE_URL` opts into live global setup: it registers a run-scoped pool of ordinary disposable Auth users, logs each in separately (registration does not auto-login), and assigns a stable identity by Playwright parallel worker. This isolates the high-volume user/browser logins from the protected Admin account and makes the temporary user ID and access token available to tests. The pool state is held only in ignored `test-results/` with owner-only permissions and removed in teardown. Auth has no delete endpoint, so the clearly prefixed disposable users remain in the target after a run. `E2E_USER_EMAIL`/`E2E_USER_PASSWORD` remain a provisioned fallback when only `BASE_URL` is supplied; Admin specs still require `E2E_ADMIN_EMAIL`/`E2E_ADMIN_PASSWORD`. Optional fixture inputs are `E2E_API_KEY`, `E2E_USER_ID`, and `E2E_PLAN_ID`. Values must remain protected secrets and must never be committed.

The live suite distinguishes isolated user/Notice mutation from shared-control-plane mutation. Disposable coverage now exercises Admin role/status round trips against a pool user, targeted notification delivery (including recipient inbox verification) and cleanup, and announcement/changelog create-update-delete with cleanup. Billing plan changes plus Config/Provider/model/route/credential/retry/auto-model/snapshot publication remain explicit skips because their shared configuration cannot be made isolated by a disposable user; skipped cases are not silently treated as passes.

Use the current file list and Playwright list output to establish the actual coverage before selecting a live subset:

```bash
find e2e/tests -name '*.spec.ts' -print | sort
BASE_URL=https://your-controlled-test-target.example \
E2E_USER_EMAIL=… E2E_USER_PASSWORD=… \
E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… \
  pnpm --filter tokenmp-v3-e2e exec playwright test --list
```

Do not record credentials, API keys, or public server addresses in this summary. See [README.md](README.md) for safe execution modes and CI behavior.
