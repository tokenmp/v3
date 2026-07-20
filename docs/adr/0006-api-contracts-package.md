# ADR 0006: API Contracts Package

- Status: accepted
- Date: 2026-07-20
- Updated: 2026-07-20 (Contract-First Go Server Generation)
- Related: ADR 0001 (Monorepo Tooling), ADR 0004 (Auth Service Foundation), ADR 0005 (Auth Identity Flows)

## Context

The Auth Service's OpenAPI contract previously lived at `services/auth/api/openapi.yaml`,
co-located with the service implementation. This creates several problems:

1. Consumers must navigate into a service's private directory to discover the API contract,
   blurring the boundary between contract and implementation.
2. The contract naturally drifts toward describing implementation details (hash algorithms,
   database columns, transaction semantics, ORM concepts) that consumers should not need
   to understand.
3. Future services and consumers need a single, language-neutral location for all API
   contracts, independent of any specific service's source tree.
4. The monorepo rules in `.agents/monorepo.md` state that shared contracts should use
   language-neutral schemas as cross-language sources of truth, and that `packages/` is
   the correct home for cross-module shared code with stable boundaries.

No external application consumers are implemented yet (Web, Admin, Gateway are all future), but the
architectural boundary must be established before the first consumer arrives. Auth conformance test
(`services/auth/internal/server/contract_test.go`) is the current implemented direct consumer/verifier:
it loads the contract at test time (never at runtime), parses and validates it with kin-openapi,
walks the actual Chi router, and asserts bidirectional equality of all HTTP method+path pairs.

## Decision

### Create `packages/contracts` as the single source of truth

- Package name: `@tokenmp/contracts`
- Location: `packages/contracts/`
- Purpose: Language-neutral API protocol contracts for all TokenMP v3 services
- The Auth OpenAPI contract moves from `services/auth/api/openapi.yaml` to
  `packages/contracts/openapi/auth/v1.yaml`
- No second copy is retained in the Auth service directory

### Contract structure

```
packages/contracts/
  openapi/
    auth/
      v1.yaml    # Auth Service v1 API contract
  go/
    auth-v1-models.yaml  # oapi-codegen v2.8.0 models config
    auth-v1-server.yaml  # oapi-codegen v2.8.0 Chi/strict server config
    generate.sh          # Reproducible generation script
    check-generated.sh   # Freshness verification script
```

The path convention `openapi/<service>/v<version>.yaml` allows future services
to add their own contracts without collision.

### Contract-first Go generation

The Auth Service uses **oapi-codegen v2.8.0** from the authoritative OpenAPI
contract. The OpenAPI YAML remains the source of truth; the committed Go
boundary is derived deterministically.

**Generation pipeline:**

```
packages/contracts/openapi/auth/v1.yaml
  └─→ oapi-codegen v2.8.0 (via go run @v2.8.0)
      ├─→ services/auth/internal/contract/authv1/models.gen.go
      │     └─ models only (PublicUser, TokenResponse, Error, etc.)
      └─→ services/auth/internal/contract/authv1/server.gen.go
            └─ Chi handler, StrictServerInterface, and strict response types
```

`auth-v1-models.yaml` owns the required format type mapping and UUID import.
`auth-v1-server.yaml` contains only Chi/strict generation with the official
`skip-prune: true` option, so server code refers to models in the same
`authv1` package instead of generating duplicates. Both outputs carry a clear
source-contract and `DO NOT EDIT` header.

**Determinism and review governance:**

1. The generation script pins oapi-codegen v2.8.0 and generates both files in
   a fixed order. `check-generated.sh` regenerates into a temporary directory,
   byte-compares each output individually, and fails if obsolete `generated.go`
   exists. Lightweight Go freshness tests only inspect both markers and never
   invoke the generator.
2. Both `.gen.go` files are committed so normal builds need no generator.
   `.gitattributes` marks only `services/auth/internal/contract/authv1/*.gen.go`
   as `linguist-generated=true`, allowing GitHub to fold generated diffs by
   default. Review OpenAPI, adapters, tests, and freshness evidence first;
   expand generated output when needed.
3. Generator upgrades, API behavior changes, and whole-document OpenAPI
   formatting are separate PRs. `operationId` and schema names are stable
   generated identifiers: do not rename or reorder them without a semantic
   need, and prohibit meaningless generated/OpenAPI churn.
4. Generation uses `go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0`,
   avoiding tools-module pollution. The auth service's `go.mod` retains
   `github.com/google/uuid` as the generated-code runtime dependency;
   `oapi-codegen/runtime` is avoided by the models type mapping.

**Implementation boundary:**

```
models.gen.go + server.gen.go (contract-derived, DO NOT EDIT)
  ↓ implements
StrictServerInterface
  ↓ implemented by
transport/authv1api/adapter.go (StrictAdapter)
  ↓ delegates to
auth.Service (unchanged domain logic)
```

The `StrictAdapter` converts generated HTTP types to `auth.Service` inputs and
outputs, preserves security semantics, uses `StrictMiddlewareFunc` for Bearer
authentication, and retains the pre-decode body validation middleware. Route
registration remains generated through `authv1.HandlerWithOptions()`; no
manual duplicate route registration exists.

### Consumer-facing contracts only

Contracts describe only what consumers can observe over HTTP:

- Request/response shapes, headers, status codes
- Authentication mechanism and token semantics
- Idempotency guarantees and error envelopes
- Security semantics (e.g., reuse detection invalidates the token family)

Contracts MUST NOT expose:

- Hash algorithms (Argon2id, bcrypt), storage formats (PHC, BYTEA, SHA-256)
- Database column names, table names, CHECK constraints, indexes
- Transaction semantics (SELECT FOR UPDATE, commit/rollback)
- ORM concepts (GORM, AutoMigrate, TxRunner)
- Internal error classification (sentinel names, driver errors)
- Migration structure or schema details

An automated `forbiddenTerms` list in `scripts/contract-helpers.mjs` enforces
this at lint/test time.

### Dependency direction

```text
packages/contracts/openapi/auth/v1.yaml
  │ (oapi-codegen at build time)
  ↓
services/auth/internal/contract/authv1/{models,server}.gen.go
  │ (implements at compile time)
  ↓
services/auth/internal/transport/authv1api/adapter.go
  │ (delegates at runtime)
  ↓
services/auth/internal/auth/service.go (unchanged domain logic)
```

Consumers MUST NOT read Auth service source code, GORM models, migrations or
database structure to discover the API. The contract is the sole authority.

### Build and validation

The package provides real lint, typecheck, test and build scripts. The
validation script uses the `yaml` npm package (a devDependency) to truly
parse each YAML document rather than relying on regex pseudo-parsing.
Runtime consumers have zero third-party dependencies — only the `dist/`
YAML files are published.

- **lint**: YAML parsing + structural validation (OpenAPI 3.x, info.title/version,
  paths non-empty, valid HTTP methods with operationId), trailing whitespace,
  final newline, forbidden internal implementation terms
- **typecheck**: Cross-file operationId uniqueness, $ref resolution via JSON Pointer
- **test**: Node test runner contract tests (required endpoints, forbidden terms,
  uniform error envelope, Cache-Control headers, build output fidelity)
- **build**: Copy contracts to `dist/openapi/` for reproducible consumption
- **generate:auth:go**: Generate Go server from Auth v1 OpenAPI contract
- **check:generated**: Verify committed generated file matches clean regeneration

All scripts integrate with the existing pnpm/Turborepo task graph.

### No client generation (yet)

This package does not generate client code. Code generation is a consumer
responsibility. TypeScript client generation for the Web app is planned but
not yet implemented.

## Consequences

- The Auth OpenAPI contract has a single authoritative location at
  `packages/contracts/openapi/auth/v1.yaml`; the old path
  `services/auth/api/openapi.yaml` is removed.
- Consumers have a clear, stable entry point that does not require navigating
  service internals.
- The contract is automatically validated against internal implementation term
  leakage.
- The Auth service's API routes are derived from the contract via oapi-codegen,
  with compile-time interface conformance and CI freshness checks.
- The old `internal/handler` package has been deleted. Its HTTP tests and
  helpers migrated to `internal/transport/authv1api`, the active generated
  contract adapter. `internal/server` remains only as a compatibility facade
  used by integration tests; production wiring imports `authv1api` directly.
- Future services (api, quota, executor) will add their contracts under
  `openapi/<service>/v1.yaml` following the same convention.
- The Auth service's AGENTS.md, README and ADRs are updated to reference the
  new contract location, the generation pipeline, and to state that consumers
  must not read Auth source code to discover the API.
- No HTTP behavior changes; this is a structural refactoring only.

## Alternatives Considered

- **Keep contract in service directory:** rejected. Co-location encourages
  implementation-detail leakage and forces consumers to navigate service
  internals. It also makes cross-service contract discovery inconsistent.
- **Copy contract to both locations:** rejected. Two copies will drift;
  a single source of truth is required.
- **Use a third-party OpenAPI validator (e.g., spectral):** rejected as a
  full linter, but the `yaml` npm package is now used as a devDependency for
  true YAML parsing. Regex pseudo-parsing was insufficient for reliable
  structural validation. A full OpenAPI linter can still be added later if needed.
- **Generate typed clients in this package:** rejected. Code generation is a
  consumer concern; the package should remain a pure contract source.
- **Use a tools/go.mod for oapi-codegen:** rejected. The `go run @version`
  approach pins the version in the script without polluting any module's
  dependency graph. `oapi-codegen/runtime` is avoided via `type-mapping`
  overrides; `github.com/google/uuid` is the only generated-code runtime
  dependency.
- **Use the non-strict server interface:** rejected. The strict interface
  provides type-safe per-status response objects and ensures compile-time
  conformance with the contract's status codes.
