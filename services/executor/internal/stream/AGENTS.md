# stream

> Scope: `services/executor/internal/stream/`.

## Responsibility

- Owns the transport-neutral public-internal streaming boundary: normalized `Request`, secret-free `Principal`, narrow `Executor`, `execution.ProtocolSink`, `execution.StreamResult`, and canonical safe-error aliases shared with `internal/nonstream`.
- Does not own HTTP/SSE framing, transport handlers, composition/runtime wiring, routing, quota, or provider execution. Phase 10 consumes this boundary through the transport adapter; this package remains transport-neutral.

## Rules

- Keep this package free of HTTP, generated contracts, Chi, and transport imports.
- Reuse canonical `internal/nonstream` Principal/status/role/safe errors instead of duplicating classifications.
- `Request.Body` is validated, owned JSON; never add credentials, raw provider payloads, or route details.

## Verification

```bash
cd services/executor
go test -race -count=1 ./internal/stream/...
```
