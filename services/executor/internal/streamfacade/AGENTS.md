# streamfacade

> Scope: `services/executor/internal/streamfacade/`.

## Responsibility

- Composes one transport-neutral stream request: pin current snapshot, resolve a protocol-filtered owner-bound plan, defensively validate a trusted active Principal, issue a CSPRNG reservation ID, and call the injected StreamDriver exactly once.
- Does not implement an HTTP/SSE sink, hybrid adapter, composition/runtime registration, provider execution, quota lifecycle, or routing policy. Phase 10 composition injects this facade into the HTTP adapter without changing that boundary.

## Rules

- Required Store, Driver, Quarantine, and request Sink must reject nil and typed-nil values fail-closed before side effects.
- Preserve safe sentinel mapping: malformed selector→400, missing model→404, invalid Principal→401; snapshot/routing/dependency failures stay opaque; cancellation/deadline propagate unchanged.
- Retain resolver-owned Plan identity and copy no credential or provider details into errors.

## Verification

```bash
cd services/executor
go test -race -count=1 ./internal/streamfacade/...
```
