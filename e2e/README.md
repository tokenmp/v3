# TokenMP v3 E2E

Playwright tests for the TokenMP web app. The suite has two deliberately separate modes so a default command or pull request never targets a remote environment.

## Safe local smoke (default)

With no `BASE_URL`, Playwright starts an isolated loopback Next development server with mock Auth and Notice APIs enabled. It runs only `tests/smoke/` in Chromium and Mobile Chrome; these tests use no login credentials and cover public, same-origin UI only.

```bash
pnpm --filter tokenmp-v3-e2e run test:smoke
pnpm --filter tokenmp-v3-e2e run test:smoke:mobile
```

`test:smoke` automatically builds `@tokenmp/ui-tokens` first, so it works from a fresh checkout where the ignored `packages/ui-tokens/dist/` output does not yet exist. The GitHub Actions smoke gate runs the same script and also performs that build explicitly before browser installation to fail fast. Other Playwright list, UI, debug, and explicit live-target commands do not build UI tokens automatically; build them first when their target starts the local web app.

The server listens on `127.0.0.1:3101` by default. Set `E2E_PORT` to an unused local port when needed. Playwright owns this server and never reuses an existing process, preventing a local invocation from silently testing another application. The `scripts/run-local-smoke-server.mjs` wrapper snapshots the exact bytes (or absence) of `apps/web/next-env.d.ts` and `apps/web/tsconfig.json` before Next starts, then atomically restores them on normal exit or termination signals. This protects existing local edits without using Git checkout.

The smoke spec records browser requests and asserts they are loopback-only. It does not prove that a future application build has no server-side external dependency; keep local smoke page coverage limited to assets and APIs available in mock mode.

## Explicit live target

All existing Admin, Panel, mobile, and data-mutating specs remain live-target tests. They are **not** selected unless `BASE_URL` is explicitly supplied. In this mode Playwright never starts a server and targets exactly the supplied URL:

```bash
BASE_URL=https://your-controlled-test-target.example \
  pnpm --filter tokenmp-v3-e2e exec playwright test --project=chromium
```

Use a controlled, disposable environment and its provisioned test identities. Do not put a target URL containing credentials, API keys, or production data in a workflow, source file, or command history. A private target URL belongs in a protected repository variable or secret when a future manually dispatched live workflow is added; browser credentials must remain secrets and must not be passed as workflow inputs.

The full suite is intentionally excluded from `.github/workflows/ci.yml`'s normal `verify` job. `.github/workflows/e2e.yml` is a separate PR/manual local-smoke gate only; it does not run the live suite.

## Structure

```text
e2e/
├── tests/
│   ├── smoke/       # credential-free, local mock gate
│   ├── admin/       # live Admin CRUD coverage
│   ├── panel/       # live user-panel coverage
│   └── *.spec.ts    # live specialized coverage
├── utils/test-utils.ts
├── playwright.config.ts
└── run-tests.sh
```

Inspect the current suite rather than relying on this overview:

```bash
find e2e/tests -name '*.spec.ts' -print | sort
pnpm --filter tokenmp-v3-e2e exec playwright test --list
```

## Installation and artifacts

```bash
pnpm install --frozen-lockfile
pnpm --filter tokenmp-v3-e2e exec playwright install chromium
pnpm --filter tokenmp-v3-e2e run test:smoke    # builds UI tokens, then runs Chromium smoke
pnpm --filter tokenmp-v3-e2e run test:wrapper  # wrapper normal-exit and TERM restoration
```

Failure-only screenshots, videos, and reports are written to `e2e/test-results/` and `e2e/playwright-report/`; both are ignored by Git. Review artifacts before sharing because browser output can contain test-environment data.
