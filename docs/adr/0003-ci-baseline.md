# ADR 0003: Continuous Integration Baseline

- Status: accepted
- Date: 2026-07-20

## Context

The repository reached a point where a shared UI Token package exists and the root task graph (`lint`, `typecheck`, `test`, `build`) is runnable. Without automated checks, pull requests and pushes to `main` could introduce regressions or leaked secrets. A minimal, repository-scoped CI baseline is needed before more modules are added. The baseline must not pre-implement deployment, release, or delivery automation.

## Decision

- Use GitHub Actions as the CI platform, triggered on every pull request and on pushes to `main`.
- Request the minimum workflow permission `contents: read`; no extra tokens are requested.
- Cancel superseded runs on the same ref using a `concurrency` group keyed by workflow and ref.
- Pin Node.js 26.4.0 and install the pinned pnpm 11.15.0 via `pnpm/action-setup` before `actions/setup-node`, then install with `pnpm install --frozen-lockfile`. Node 26.4.0 does not ship Corepack, so Corepack is not used.
- Cache the pnpm store through `actions/setup-node`'s built-in `pnpm` cache; no repository secret is required.
- Run the root scripts in order: `lint`, `typecheck`, `test`, `build`.
- Scan the full history by downloading the open-source Gitleaks CLI at a fixed version (v8.28.0) directly on the runner, verifying its release tarball against the official checksums file with `sha256sum`, installing it under `RUNNER_TEMP` (no `sudo`, no system directories), and running `gitleaks git --redact --verbose --exit-code 1 .`. The workflow references no repository secret and no `GITHUB_TOKEN`, so fork pull requests are scanned without any extra secret.
- Do not include deployment, release, publish, or delivery jobs in this baseline.

## Consequences

- Every pull request and `main` push is validated against the same root task graph and secret scan.
- Fork pull requests are covered by CI without needing a personal access token or repository secret.
- The baseline is intentionally limited to verification; continuous delivery and deployment remain unimplemented until a dedicated change defines their scope, environment, and security boundaries.
- Adding modules requires that their scripts and tests are wired into the existing root task graph so they remain covered by CI.

## Alternatives Considered

- Broad CI matrix and deployment stages: rejected for this change because delivery scope and environment are not yet decided, and the task boundary is repository verification only.
- `gitleaks/gitleaks-action` wrapper: rejected because for organization repositories it may require a `GITLEAKS_LICENSE` secret, which would break the baseline's no-extra-secret commitment and exclude fork pull requests. The fixed-version open-source CLI with official checksum verification avoids both issues.
- Running checks locally only: rejected because it does not protect `main` or enforce checks on external contributions.
