# Executor Metrics

> Scope: `services/executor/internal/metrics/`; inherits `services/executor/AGENTS.md`.

## Responsibility

This package provides Prometheus HTTP observability for the Executor service:

- `Collector` owns all Executor Prometheus metrics: counter vec, histogram vec, and state gauges.
- `Middleware(next)` records `executor_http_requests_total{route,method,status_class}` and `executor_http_request_duration_seconds{route,method,status_class}` per request. It must be the outermost middleware (outside AuthMiddleware) so that 401 responses are also counted.
- `Handler(registry, disabled)` serves `GET /metrics` with `Cache-Control: no-store` and `Content-Type: text/plain; version=0.0.4`. POST → 405; subpath → 404; disabled → 404.
- State gauges read from existing in-memory state via injected functions:
  - `executor_config_generation` from `snapshot.Store.Generation()`
  - `executor_quota_reservations_total{state}` from `quota.DomainInMemory.CountByState()`
  - `executor_requestlog_events_total` from `requestlog.InMemoryExecution.EventCount()`

## Route labels

Low-cardinality stable labels (no user/model/credential/subject/key_id):

| Path | Label |
|---|---|
| `/healthz` | `healthz` |
| `/metrics` | `metrics` |
| `/v1/models` | `models` |
| `/v1/chat/completions` | `chat_completions` |
| `/v1/messages` | `messages` |
| `/v1/responses` | `responses` |
| `/v1/images/generations` | `images_generations` |
| Other | `other` |

## Safety rules

- Do not import `snapshot`, `quota`, `requestlog`, `execution`, HTTP transport, or runtime composition packages. State is injected via function callbacks.
- Do not add high-cardinality labels (user, model, credential, subject, key_id, request_id).
- `responseWriter` must forward `http.Flusher` so SSE streaming works through the middleware.
- Metrics endpoint is anonymous (same policy as `/healthz`), not part of the OpenAPI contract.
- Default enabled; `EXECUTOR_METRICS_ENABLED=false` disables.

## Verification

```bash
cd services/executor
gofmt -w internal/metrics
go test -race -count=1 ./internal/metrics/...
go test -race -count=5 ./internal/metrics/...
go build ./...
```
