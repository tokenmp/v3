# modelcatalogfacade

> Scope: `services/executor/internal/modelcatalogfacade/`; inherits `services/executor/AGENTS.md`.

## Responsibility

Transport-neutral composition root for the model catalog listing. Implements `modelcatalog.CatalogProvider` by composing per-request snapshot pinning, enabled-route filtering, quarantine state consultation, and model-to-entry mapping.

Owns no HTTP, database, env, main, or route registration. Imports no transport code. Every unsafe input (nil/typed-nil dependency, missing/invalid Principal, missing snapshot) fails closed to a safe sentinel that a transport renderer reduces to a protocol-native response.

## Public surface

| Export | Purpose |
|---|---|
| `Options` | Configuration: `Store` (required), `Quarantine` (required), `Clock` (optional, wall-time default) |
| `Facade` | Implements `modelcatalog.CatalogProvider` |
| `New(Options)` | Returns a `*Facade`; does not fail — dependencies revalidated per request |

## Behavior

1. Fail-closed on nil facade or nil/typed-nil Store/Quarantine → `ErrMisconfigured`.
2. Require defensively revalidated active service/admin Principal → `ErrUnauthenticated`.
3. Pin current snapshot → `ErrNoSnapshot` if none published.
4. Build set of model IDs with at least one enabled route; skip models with no enabled route.
5. Per model: check quarantine. Active quarantine excludes; read error (except `ErrNotFound`/context cancel) → `ErrQuarantineUnavailable` fail-closed; expired quarantine includes.
6. Map compiled model to `CatalogEntry` via `modelcatalog.MapCapabilities`/`MapThinking`.
7. Sort entries by ID for deterministic output.

## Safety rules

- Do not import HTTP, generated contract, chi, or transport packages.
- Principal validation mirrors `nonstreamfacade` bounds: printable 0x21..0x7e, Subject ≤ 256 bytes, KeyID ≤ 128 bytes.
- Nil/typed-nil Clock uses wall time; typed-nil injection fails closed.
- Catalog entries carry only safe public fields: no FallbackModelIDs, DisplayName, route, adapter, or credential detail.

## Verification

```bash
cd services/executor
go test -race -count=1 ./internal/modelcatalogfacade/...
```
