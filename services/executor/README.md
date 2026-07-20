# Executor

Executor 是 TokenMP v3 的 Mock-first Foundation 模型请求执行服务。已提供 HTTP health、配置加载、优雅关闭、Mock/InMemory ports 与配额 reservation 终态状态机；尚未实现公开模型业务路由、数据库、SDK、Docker 或 CI。contracts 侧 Executor 生成配置/脚本已预置且为 experimental；`services/executor` 尚未生成、提交或注册 generated models/server，`check:generated:executor` 尚非现行门禁。

## 已实施能力

- `GET /healthz` 返回 `200` 和 `{"status":"ok"}`；`HEAD /healthz` 返回相同状态与 headers 但无响应体。两者不访问外部资源。
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

测试不需要数据库，也不需要 Docker。此分支没有 Executor CI job。

## 契约与边界

- `packages/contracts/openapi/executor/v1.yaml` 是 Executor HTTP 契约的唯一事实来源。Foundation 只实现 `/healthz`；该契约中的公开模型业务路由尚未实现。contracts 侧 Executor 生成配置/脚本已预置且为 experimental；`services/executor` 尚未生成、提交或注册 generated models/server，`check:generated:executor` 尚非现行门禁。
- Foundation 不拥有数据库、schema 或 migration，并使用 Mock/InMemory ports。未来 Executor 如需持久化，可拥有自己的数据库及其 schema 和 migration；不得访问其他服务的数据库、schema、migration 或私有源码。
- 服务间集成必须使用明确、可版本化的契约，不能以源码 import 或共享数据库替代。
