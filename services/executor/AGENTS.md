# Executor Service

> 作用域：`services/executor/`。继承仓库根目录及 `services/AGENTS.md`。

## 模块职责

- 负责：TokenMP 模型请求执行面的 Foundation：health、优雅关闭、Mock/InMemory ports 与 quota reservation 终态状态机。
- 不负责：公开模型 API、数据库、SDK、Docker、CI、Provider 调用或生产部署。contracts 侧 Executor 生成配置/脚本已预置且为 experimental；`services/executor` 尚未生成、提交或注册 generated models/server，`check:generated:executor` 尚非现行门禁。
- 所有者：TokenMP。

## 必读文档

- 模块说明：`README.md`
- API 契约：`../../packages/contracts/openapi/executor/v1.yaml`
- 源码与测试：`cmd/executor/`、`internal/`

模块文档仅引用仓库中实际存在的文件。

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `GET` / `HEAD /healthz` | HTTP health 请求 | `200`；GET 返回 `{"status":"ok"}`，HEAD 无响应体；不访问外部资源 | experimental | `internal/transport/healthz/handler.go` 与测试 |
| graceful shutdown | 进程终止信号 | 在 `EXECUTOR_SHUTDOWN_TIMEOUT` 内停止接受新请求并等待已有工作结束 | experimental | `internal/app/server.go` 与测试 |
| HTTP connection boundaries | HTTP 连接 | `ReadHeaderTimeout` 限制慢速 request headers，`IdleTimeout` 限制 keep-alive 空闲；不设置总 `ReadTimeout` 或全局 `WriteTimeout`，保留未来流式请求/SSE | experimental | `internal/config/config.go`、`internal/app/server.go` 与测试 |
| quota reservation terminal | 已 `reserved` 的 reservation ID | `finalized` 或 `released`；同终态幂等、相反终态返回 `ErrConflict` | internal | `internal/quota/port.go` 与 contract tests |

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | `packages/contracts` | Executor HTTP 契约的设计/构建时事实来源 | OpenAPI 文件引用；无 Go runtime import | `openapi/executor/v1.yaml` | 实施或变更公开路由时验证契约与路由 |

Foundation 尚无已验证的直接消费者。

## 开发与验证

```bash
go mod edit -json
go test ./...
go build ./...
```

- 格式化：`gofmt -w .`
- 最小验证：`go mod edit -json`、`go test ./...`、`go build ./...`
- 契约测试：Foundation 尚未实施；后续以 `packages/contracts/openapi/executor/v1.yaml` 为准。
- 集成测试：Foundation 使用 Mock/InMemory ports，不启动数据库。

## 模块边界

- 允许访问：模块自身 Go 源码、Mock/InMemory ports，以及作为设计/构建时事实来源的 Executor OpenAPI 契约。
- 禁止访问：其他服务的数据库、私有源码、schema 或 migration。
- 数据所有权：Foundation 不拥有数据库、schema 或 migration；未来 Executor 如需持久化，只能拥有自己的数据库及其 schema 和 migration，且仍不得访问其他服务的库。
- 配置和环境变量：`EXECUTOR_HTTP_ADDR`、`EXECUTOR_SHUTDOWN_TIMEOUT`、`EXECUTOR_READ_HEADER_TIMEOUT`（默认 `10s`）和 `EXECUTOR_IDLE_TIMEOUT`（默认 `60s`）；duration 缺失使用默认，显式空、无效或非正值拒绝。定义见 `README.md`。HTTP server 不设置总 `ReadTimeout` 或全局 `WriteTimeout`，以免截断未来流式请求/SSE。
- Docker 镜像/部署单元：Foundation 未实施。
- 健康检查：Foundation health endpoint；具体路由在实现中定义并测试。

## DO NOT

- **DO NOT** 在 Foundation 中连接数据库或引入 schema/migration — 正确做法：通过端口使用 Mock/InMemory 实现；未来跨服务数据通过版本化契约访问，持久化仅可使用 Executor 自有数据库。
- **DO NOT** 访问其他服务的数据库、schema、migration 或私有源码 — 服务边界必须通过版本化契约保持。
- **DO NOT** 在 `services/executor` 引入 SDK、generated models/server、Docker 或 CI — 它们不属于 Foundation 范围；正确做法：仅在后续独立阶段生成、提交并注册 generated models/server。contracts 侧预置的 Executor 生成配置/脚本保持 experimental，`check:generated:executor` 尚非现行门禁。
- **DO NOT** 对同一 reservation 执行相反终态 — 会造成重复结算或错误释放；正确做法：同终态幂等重放，相反终态返回稳定冲突。

## 已知陷阱与历史教训

### Reservation 终态唯一性

- 症状：取消、超时和完成路径竞争时可能重复结算或释放。
- 根因：多个清理路径未共享唯一终态约束。
- DO：只允许 `reserved → finalized` 或 `reserved → released`，并使相同终态幂等。
- DO NOT：在已终态 reservation 上执行相反终态。
- 验证：Foundation quota terminal 状态机单元测试。
- 证据：`internal/quota/port.go`、`internal/quota/contract_test.go`。
- 适用范围：所有 quota port 实现及调用方清理路径。

## 文档维护触发器

出现公开能力、端口契约、直接依赖、运行变量、测试命令、数据所有权或部署边界变化时，同步更新本文件及 `README.md`，并维护 `services/AGENTS.md` 和根 `AGENTS.md` 索引。
