# Executor

Executor 是 TokenMP v3 的 Mock-first Foundation 模型请求执行服务。已提供 HTTP health、配置加载、优雅关闭、Mock/InMemory ports 与配额 reservation 终态状态机；尚未实现公开模型业务路由的运行时注册、数据库、SDK、Docker 或独立 Executor CI job。已实施并提交 Executor v1 generated transport（`internal/contract/executorv1/{models,server}.gen.go`）与 `internal/transport/executorv1api` adapter skeleton，并新增路由契约一致性测试（`internal/server/contract_test.go`）；但运行时 `main` 仍未注册任何公开业务路由，仍只经 `internal/transport/healthz` 服务 `/healthz`。`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁；同一 job 还运行 generated contract、strict adapter skeleton 与 route/HTTP conformance 的 race tests，但仍无独立 Executor CI job、运行时业务路由或执行 pipeline 测试。

## 已实施能力

- `GET /healthz` 返回 `200` 和 `{"status":"ok"}`；`HEAD /healthz` 返回相同状态与 headers 但无响应体。两者不访问外部资源。仅由运行时 `main` 经 `internal/transport/healthz` 注册。
- generated Executor v1 transport（`internal/contract/executorv1`）：oapi-codegen v2.8.0 从 `openapi/executor/v1.yaml` 生成的 Chi handler 与 StrictServerInterface；存在于已提交版本且带 `DO NOT EDIT` 头。
- `internal/transport/executorv1api.Adapter` 实现 generated StrictServerInterface：`/healthz` 返回 `200`，模型操作（`/v1/models`、`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、`/v1/images/generations`）返回协议原生 `501` 错误，绝不启动任何 SSE 流。适配器仅被路由一致性测试消费，未接入运行时 server。
- 路由契约一致性测试（`internal/server/contract_test.go`）：以 kin-openapi 加载 `openapi/executor/v1.yaml`，遍历 generated `Handler` 的 Chi 路由，与契约 method+path 双向严格比较（7 条路由）。
- `cmd/executor` 读取配置、监听 HTTP，并在 `SIGINT` 或 `SIGTERM` 后以配置的超时优雅关闭；HTTP server 使用 header 读取和 keep-alive 空闲连接边界。
- `internal/config` 验证运行时配置。
- `internal/{configrepo,identity,quota,requestlog,runtime}` 提供端口，以及 Mock/InMemory 实现。
- quota reservation 只能从 `reserved` 迁移到 `finalized` 或 `released`：相同终态幂等，相反终态返回稳定冲突。

## 运行时配置

| 变量 | 默认值 | 说明 |
|---|---|---|
| `EXECUTOR_HTTP_ADDR` | `127.0.0.1:8081` | HTTP 监听地址；空值使用默认值。 |
| `EXECUTOR_SHUTDOWN_TIMEOUT` | `10s` | 收到终止信号后等待优雅关闭完成的最长正 duration；显式空值、无效值或非正值会使启动失败。 |
| `EXECUTOR_READ_HEADER_TIMEOUT` | `10s` | 读取请求 headers 的最长正 duration，限制慢速 headers；缺失时使用默认，显式空值、无效值或非正值会使启动失败。 |
| `EXECUTOR_IDLE_TIMEOUT` | `60s` | keep-alive 空闲连接的最长正 duration；缺失时使用默认，显式空值、无效值或非正值会使启动失败。 |

服务不设置全局 `WriteTimeout`，以便未来 SSE 响应不被截断；也不设置总 `ReadTimeout`，避免截断流式请求。

## 开发与验证

在 `services/executor/` 目录执行：

```bash
gofmt -w .
go mod edit -json
go test ./...
go build ./...
```

生成物新鲜度：

```bash
pnpm --filter @tokenmp/contracts check:generated:executor
```

路由契约一致性：

```bash
go test ./internal/server/...
```

测试不需要数据库，也不需要 Docker。没有独立 Executor CI job；`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁。

## 契约与边界

- `packages/contracts/openapi/executor/v1.yaml` 是 Executor HTTP 契约的唯一事实来源。运行时 `main` 只注册 `/healthz`（经 `internal/transport/healthz`）；generated `Handler`/`StrictHandler` 与 `executorv1api.Adapter` 仅被路由一致性测试驱动，未接入运行时 server。契约中的公开模型业务路由尚未在运行时实现，strict SSE 为 generated capability，当前不被任何运行时代码调用。generated models/strict server 随变更提交，位于 `internal/contract/executorv1/` 并供 adapter 与测试使用；`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁；同一 job 还运行 generated contract、strict adapter skeleton 与 route/HTTP conformance 的 race tests，但仍无独立 Executor CI job、运行时业务路由或执行 pipeline 测试。Docker、运行时路由与集成验证仍待后续独立阶段（见 `docs/executor/architecture.md` 阶段 14）。
- Foundation 不拥有数据库、schema 或 migration，并使用 Mock/InMemory ports。未来 Executor 如需持久化，可拥有自己的数据库及其 schema 和 migration；不得访问其他服务的数据库、schema、migration 或私有源码。
- 服务间集成必须使用明确、可版本化的契约，不能以源码 import 或共享数据库替代。
