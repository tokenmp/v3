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

All existing Admin, Panel, mobile, and data-mutating specs remain live-target tests. They are **not** selected unless `BASE_URL` (or protected `E2E_BASE_URL`) is explicitly supplied. In this mode Playwright never starts a server and targets exactly the supplied URL:

```bash
BASE_URL=https://your-controlled-test-target.example \
E2E_USER_EMAIL=… E2E_USER_PASSWORD=… \
E2E_ADMIN_EMAIL=… E2E_ADMIN_PASSWORD=… \
  pnpm --filter tokenmp-v3-e2e exec playwright test --project=chromium
```

Live identities and optional test data are injected only through protected environment variables:

| Variable | Purpose |
| --- | --- |
| `E2E_BASE_URL` | Optional alternative to `BASE_URL` for the explicit target. |
| `E2E_USER_EMAIL`, `E2E_USER_PASSWORD` | Required by authenticated Panel and user mobile specs. |
| `E2E_ADMIN_EMAIL`, `E2E_ADMIN_PASSWORD` | Required by Admin CRUD and admin mobile specs. |
| `E2E_API_KEY`, `E2E_USER_ID`, `E2E_PLAN_ID` | Optional provisioned fixture data for specs that need it. |

Admin describes are skipped with a clear reason when the admin pair is absent; user describes do the same for the user pair. This makes `--list` and an accidental credential-free live invocation safe, but it does not validate a supplied credential: an invalid supplied value still fails the relevant login test. Mock/local fallbacks are deliberately invalid placeholders and cannot authenticate to a live service.

Live Admin coverage intentionally separates safe UI/read-only checks from mutations. Specs assert current dialogs, controls, routes, list wiring, and responsive navigation against the supplied target. Create/update/delete, snapshot publication, and role/status mutation cases are explicitly skipped with a reason unless a dedicated disposable fixture is supplied: they must not alter shared development data.

Use a controlled, disposable environment and its provisioned test identities. Do not put a target URL containing credentials, API keys, passwords, or production data in a workflow, source file, command history, or tracked `.env` file. Keep all `E2E_*` values in protected secrets; never commit their values.

The full suite is intentionally excluded from `.github/workflows/ci.yml`'s normal `verify` job. `.github/workflows/e2e.yml` is a separate PR/manual local-smoke gate only; it does not run the live suite.

## Structure

```text
e2e/
├── tests/
│   ├── smoke/       # credential-free, local mock gate
│   ├── admin/       # live Admin CRUD coverage
│   ├── panel/       # live user-panel coverage
│   └── *.spec.ts    # live specialized coverage
├── utils/credentials.ts  # protected E2E_* credential resolution and suite gates
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
