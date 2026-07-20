# Executor Service

> 作用域：`services/executor/`。继承仓库根目录及 `services/AGENTS.md`。

## 模块职责

- 负责：TokenMP 模型请求执行面的 Foundation：health、优雅关闭、Mock/InMemory ports 与 quota reservation 终态状态机。已实施 generated transport 层及路由契约一致性测试，但运行时 `main` 仍未注册任何公开业务路由。已实施 Config compiler：`snapshot.Compile` → `adapter.Compile` 校验与 normalization 原始 snapshot，检查 identities/references、provider/adapter/protocol compatibility、HTTPS BaseURL、capability/thinking、timeouts/retries、fallback cycle，以及有限 DSL 的路径、action、header/query allowlist 与规则 priority；产出 deterministic `CompiledConfig`。三份脱敏 fixture 均在严格解码、secret 扫描后实际编译并发布；C01–C27 相关安全、默认值、immutability 与确定性测试均已实施。已实施纯 Go routing：strict `model[:group][@provider]` selector（`auto` 不得带 group）、route group/provider selector/route-local non-secret credential/auto+model fallback；legacy adapter `CredentialRef` 合成 `legacy-route-sha256-<full SHA-256(route ID)>` ID。公开 Candidate/Plan 只含安全 credential ID/priority，不含 credential ref 或 secret；Resolver/Plan 固定 revision/generation，产出 deterministic candidates，并以 model/provider/route/credential 四维 quarantine read fail-closed；`Plan.Next` 仅提供候选 action scope，不是 RetryDecider。已实施 stateless、pure-Go、无 I/O 的 Adapter Engine：strict JSON object、全部有限 DSL actions、literal-only header/query、继续拒绝 `ValueRef`、model-bounded thinking，以及 response mapping（维度间 AND、维度内 OR、compiler fixed order 与固定安全 default）；Apply 失败原子化，测试覆盖 mutation isolation、race 和 fuzz。response mapping 尚未接入 pipeline。compiler 默认值为 request `2m`、TTFT `45s`、idle `30s`、lifetime `10m`、backoff `200ms`、total attempts `3`、same-target attempts `2`、total retry duration `90s`。
- 不负责：runtime config source/reload loop、运行时公开模型 API 路由注册、credential resolver/secret injector、RetryDecider/attempt budget、execution pipeline、数据库、SDK/provider、Docker、独立 Executor CI job、Provider 调用或生产部署。compiled config、routing Resolver/Plan 与 Adapter Engine 均尚未被 request pipeline 消费。contracts 侧 Executor 生成配置/脚本已落地；已生成并提交 generated models/strict server（`services/executor/internal/contract/executorv1/{models,server}.gen.go`），由 `check:generated:executor` 新鲜度检查覆盖（该检查是现有 `go-auth` CI job 中必经的门禁步骤；无独立 Executor CI job）。
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
| generated Executor v1 transport（Chi + strict server） | oapi-codegen v2.8.0 从 `openapi/executor/v1.yaml` 生成 | `internal/contract/executorv1/{models,server}.gen.go`；`StrictServerInterface` 由 `internal/transport/executorv1api.Adapter` 实现：`/healthz` 返回 `200`，模型操作返回协议原生 `501 NOT_IMPLEMENTED`，不启动任何 SSE 流 | experimental | `packages/contracts/go/executor-v1-*.yaml` + `openapi/executor/v1.yaml` |
| Executor route conformance test | Go test 运行环境 | `internal/server/contract_test.go` 以 kin-openapi 加载契约，遍历 generated `Handler` 的 Chi 路由，与 OpenAPI method+path 双向严格比较（7 条路由） | experimental | `packages/contracts/openapi/executor/v1.yaml` |
| config compiler | non-empty revision；models/providers/adapters/routes 集合可为空 | `snapshot.Compile` → `adapter.Compile` 返回 normalized、deterministic `CompiledConfig` 或分类 validation error；空集合编译为无业务 route 的 config；C01–C27 相关有限 DSL、配置图、默认值、继承、priority、immutability 与 determinism 均由 compiler tests 覆盖 | experimental | `internal/snapshot/compiler.go`、`internal/adapter/compiler.go`、`*_test.go` |
| immutable generation-aware snapshot publication | non-empty matching revision, non-nil compiled config, non-zero monotonic generation | deep-copy freeze + atomic Store-owned publication; `Current` returns an independent same-generation view, so later publish cannot mutate an in-flight request's revision; invalid or stale publication preserves last known good | experimental | `internal/snapshot/store.go`、`internal/snapshot/store_test.go` |
| routing selector / Resolver / Plan | frozen compiled snapshot plus strict `model[:group][@provider]` selector | deterministic, revision/generation-pinned non-secret candidates; `auto` forbids group; model/provider/route/credential quarantine read failure fails closed; `Plan.Next` exposes scope only, not retry decisions | experimental | `internal/routing/{selector,resolver}.go` and tests |
| Adapter Engine `Apply` / `MapResponse` | compiled adapter、strict JSON object、selected model thinking bounds / classified upstream metadata | atomic `AppliedRequest` 或分类错误；literal-only injection plan；response rules follow compiler order with AND-across/OR-within dimensions and fixed safe default；no I/O | internal | `internal/adapter/engine.go` 与 tests |
| three config fixtures | `fixtures/configs/{default,xfyun,anthropic}.json` | strict decode、secret scan、fixture-specific assertions、`Compile` 与 Store publish；每份均产生 store-ready compiled config | experimental | `internal/snapshot/{fixture,compiler}_test.go`、`fixtures/configs/*.json` |

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | `packages/contracts` | Executor HTTP 契约的设计/构建时事实来源；generated Go 代码由 oapi-codegen 从其生成 | OpenAPI 文件引用 + generated Go 代码；无 runtime import contracts package | `openapi/executor/v1.yaml` → `internal/contract/executorv1/{models,server}.gen.go` | 实施或变更公开路由时验证契约与路由 |
| 依赖 | `github.com/getkin/kin-openapi` | 路由契约一致性测试在 test 时解析并 Validate OpenAPI | test-only Go 依赖 | `internal/server/contract_test.go` | 修改契约或 generated Handler 后运行 `go test ./internal/server/...` |
| 依赖 | `github.com/go-chi/chi/v5`、`github.com/oapi-codegen/runtime` | generated server 与 adapter 的运行时依赖 | Go runtime import | `go.mod` | `go build ./...` |

Foundation 尚无已验证的直接消费者。routing Resolver/Plan 与 Adapter Engine 均仅由模块内测试消费，未接入 runtime routing 或 request pipeline；公开 Candidate/Plan 不暴露 credential ref，Engine 不解析 credential ref 或注入 secret。generated transport 与 adapter 仅被路由一致性测试消费，未被运行时 `main` 注册。compiler 和 snapshot store 是模块内纯 Go 库，无 runtime consumer；三份 fixture 仅被 compiler/snapshot tests 消费。

## 开发与验证

```bash
go mod edit -json
go test ./...
go build ./...
```

- 格式化：`gofmt -w .`
- 最小验证：`go mod edit -json`、`go test ./...`、`go build ./...`
- CI compiler/snapshot race 门禁：现有 `go-auth` job 在本目录运行 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/...`；覆盖纯 Go adapter/snapshot packages（包括 Engine），不运行数据库、SDK、runtime config source 或 request pipeline，且不代表独立 Executor CI job。
- 生成物新鲜度：`pnpm --filter @tokenmp/contracts check:generated:executor`（临时目录重生成 + 字节比较；generated models/strict server 随变更提交，纳入新鲜度检查）。
- 路由契约一致性：`go test ./internal/server/...`（以 kin-openapi 加载 `openapi/executor/v1.yaml`，与 generated Handler 的 Chi 路由双向比较）。
- 契约测试：contracts package 侧 `pnpm --filter @tokenmp/contracts lint|typecheck|test` 验证契约本身。
- 集成测试：Foundation 使用 Mock/InMemory ports，不启动数据库。

## 模块边界

- 允许访问：模块自身 Go 源码、Mock/InMemory ports，以及作为设计/构建时事实来源的 Executor OpenAPI 契约。generated models/strict server 随变更提交，存在于 `internal/contract/executorv1/`，供 adapter 与一致性测试使用。Config compiler、immutable generation-aware snapshot store、routing 与 Adapter Engine 已落地并仅供模块内测试/后续 composition 使用。Adapter Engine 不进行 credential resolution 或 secret injection。
- 禁止访问：其他服务的数据库、私有源码、schema 或 migration。
- 数据所有权：Foundation 不拥有数据库、schema 或 migration；未来 Executor 如需持久化，只能拥有自己的数据库及其 schema 和 migration，且仍不得访问其他服务的库。
- 配置和环境变量：`EXECUTOR_HTTP_ADDR`、`EXECUTOR_SHUTDOWN_TIMEOUT`、`EXECUTOR_READ_HEADER_TIMEOUT`（默认 `10s`）和 `EXECUTOR_IDLE_TIMEOUT`（默认 `60s`）；duration 缺失使用默认，显式空、无效或非正值拒绝。定义见 `README.md`。HTTP server 不设置总 `ReadTimeout` 或全局 `WriteTimeout`，以免截断未来流式请求/SSE。
- Docker 镜像/部署单元：Foundation 未实施。
- 健康检查：Foundation health endpoint；具体路由在实现中定义并测试。

## DO NOT

- **DO NOT** 在 `services/executor` runtime `main` 注册公开业务路由 — 运行时服务器仍只通过 `internal/transport/healthz` 服务 `/healthz`；generated `Handler`/`StrictHandler` 仅被路由一致性测试驱动，不得接入运行时 server。正确做法：后续实现阶段验证流式接口后，再独立变更将生成路由接入 composition root。
- **DO NOT** 连接数据库或引入 schema/migration — 正确做法：通过端口使用 Mock/InMemory 实现；未来跨服务数据通过版本化契约访问，持久化仅可使用 Executor 自有数据库。
- **DO NOT** 访问其他服务的数据库、schema、migration 或私有源码 — 服务边界必须通过版本化契约保持。
- **DO NOT** 在运行时启用 SSE 流式响应 — strict server 生成的 SSE 能力仅为 generated capability，当前不被任何运行时代码调用；流式实现待后续流式阶段。
- **DO NOT** 引入 Docker 或独立 Executor CI job/运行时路由 — generated models/strict server 已生成、提交并供 adapter 与测试使用；现有 `go-auth` job 仅复用其 Go toolchain 执行 generated freshness、generated transport/route conformance race tests，以及 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/...` compiler/snapshot race 门禁。该门禁不运行数据库、SDK、runtime config source 或 request pipeline；Docker、运行时业务路由与集成验证仍待后续独立阶段（见 `docs/executor/architecture.md` 阶段 14）。
- **DO NOT** 将有限 DSL 扩展为脚本执行平台，或让其写 Host、Authorization、代理/转发、Content-Length、SDK 控制头、密钥引用、URL scheme/host/base path；header/query 必须是 JSON string literal，`ValueRef` 继续拒绝。禁止任意脚本、SQL、网络、文件访问或自由模板。compiler/Engine 仅接受有限 action、受限 path 及 allowlisted header/query。
- **DO NOT** 把 compiler/store/routing/Adapter Engine 已落地误写为 runtime reload 或业务执行：它们尚无 runtime config source、reload loop、credential resolver/secret injector、Provider 调用、RetryDecider/attempt budget 或 request-pipeline consumer；response mapping 尤其尚未接入 pipeline。
- **DO NOT** 把 `Plan.Next` 当作 RetryDecider，或在 resolver 外扩大 selector/revision-pinned candidate universe：它只对已冻结候选定义 action scope；retry rule matching、attempt budget 与执行均未实施。
- **DO NOT** 修改 compiler 默认值或 policy 继承顺序而不更新 C01–C27 tests 与文档：当前实值为 request `2m`、TTFT `45s`、idle `30s`、lifetime `10m`、backoff `200ms`、total attempts `3`、same-target attempts `2`、total retry duration `90s`。
- **DO NOT** 手动编辑 `internal/contract/executorv1/{models,server}.gen.go` — 通过 `packages/contracts/go/generate-executor.sh` 重生成。
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
