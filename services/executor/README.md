# Executor

Executor 是 TokenMP v3 的 Mock-first Foundation 模型请求执行服务。已提供 HTTP health、配置加载、优雅关闭、Mock/InMemory ports、配额 reservation 终态状态机，以及模块内 Config compiler、immutable generation-aware snapshot Store、revision-pinned routing Resolver/Plan、stateless pure-Go Adapter Engine 和 request-local、serial、pure-Go retry decision/attempt-budget State；尚未实现公开模型业务路由的运行时注册、credential resolver/secret injector、执行 pipeline、数据库、Docker 或独立 Executor CI job。已实施内部 shared `sdk` port 及官方 `github.com/openai/openai-go/v3` **v3.44.0** 的 OpenAI Chat Completions 非流式 adapter：每次调用独立校验 HTTPS target、上游 model 与 secret，SDK retry=0、禁止 redirect；对 Chat 请求执行严格契约验证（含 tools、vision 与 thinking 字段），安全注入与唯一 Bearer 鉴权，记录不含 URL/请求/响应/凭据的 attempt observer metadata；成功返回安全 request ID/status metadata，失败将 timeout、transport、protocol 与 HTTP 状态安全分类。TLS `httptest` 覆盖 target/header 隔离、retry/redirect、分类与无泄漏。与其并列，已实施官方 `github.com/anthropics/anthropic-sdk-go` **v1.58.0** 的 Anthropic Messages 非流式内部 adapter：每次调用独立校验 HTTPS target/path prefix、上游 model 与 opaque secret，使用 `WithoutEnvironmentDefaults`、SDK retry=0 且禁止 redirect；最终 transport 仅允许 per-call `x-api-key` 和固定 `anthropic-version`，并重建允许的 header/query。对 Messages 请求及成功响应执行严格 OpenAPI 形状验证，执行层的 target model 与 effective thinking 具有权威性，并覆盖 tools、vision 与 thinking；成功仅返回安全 status/request-ID metadata，失败安全分类为 timeout、transport、protocol 或 HTTP（含 Anthropic 529 overloaded→unavailable，并由 fixture 映射为 429）。TLS `httptest`、请求/响应 fuzz 覆盖 target/header/query/environment 隔离、retry/redirect、分类和无泄漏。两种 SDK adapter 均未接入 pipeline 或 runtime routing，因此不是端到端业务能力。仍未实施 credential resolver/secret injector、pipeline/runtime 业务路由、Responses、Images、streaming、数据库、Docker 或独立 Executor CI job。已实施并提交 Executor v1 generated transport（`internal/contract/executorv1/{models,server}.gen.go`）与 `internal/transport/executorv1api` adapter skeleton，并新增路由契约一致性测试（`internal/server/contract_test.go`）；但运行时 `main` 仍未注册任何公开业务路由，仍只经 `internal/transport/healthz` 服务 `/healthz`。`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁；同一 job 还运行 generated contract、strict adapter skeleton 与 route/HTTP conformance 的 race tests，但仍无独立 Executor CI job、运行时业务路由或执行 pipeline 测试。

## 已实施能力

- `GET /healthz` 返回 `200` 和 `{"status":"ok"}`；`HEAD /healthz` 返回相同状态与 headers 但无响应体。两者不访问外部资源。仅由运行时 `main` 经 `internal/transport/healthz` 注册。
- generated Executor v1 transport（`internal/contract/executorv1`）：oapi-codegen v2.8.0 从 `openapi/executor/v1.yaml` 生成的 Chi handler 与 StrictServerInterface；存在于已提交版本且带 `DO NOT EDIT` 头。
- `internal/transport/executorv1api.Adapter` 实现 generated StrictServerInterface：`/healthz` 返回 `200`，模型操作（`/v1/models`、`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、`/v1/images/generations`）返回协议原生 `501` 错误，绝不启动任何 SSE 流。适配器仅被路由一致性测试消费，未接入运行时 server。
- 路由契约一致性测试（`internal/server/contract_test.go`）：以 kin-openapi 加载 `openapi/executor/v1.yaml`，遍历 generated `Handler` 的 Chi 路由，与契约 method+path 双向严格比较（7 条路由）。
- `cmd/executor` 读取配置、监听 HTTP，并在 `SIGINT` 或 `SIGTERM` 后以配置的超时优雅关闭；HTTP server 使用 header 读取和 keep-alive 空闲连接边界。
- `internal/config` 验证运行时配置。
- `internal/{configrepo,identity,quota,requestlog,runtime}` 提供端口，以及 Mock/InMemory 实现。
- quota reservation 只能从 `reserved` 迁移到 `finalized` 或 `released`：相同终态幂等，相反终态返回稳定冲突。
- `internal/snapshot.Compile` 将原始 `ConfigSnapshot` 转换为 `internal/adapter.Compile` 的输入；compiler 校验 C01–C27 所覆盖的 identity/reference、provider/adapter/protocol compatibility、HTTPS Base URL、有限 DSL、thinking/capability、timeout/retry、priority/conflict、fallback 与确定性/无别名约束，并按继承优先级 normalization。
- `NewCompiledSnapshot` 和 `Store` 深拷贝冻结并按单调 generation 原子发布；`Current` 返回独立的不可变视图，拒绝无效或陈旧发布且保留 last known good。
- `internal/routing` 是纯 Go 的 revision-pinned Resolver/Plan：解析严格的 `model[:group][@provider]` selector；特殊 `auto` 只允许裸用或 `@provider`，不允许 route group。Resolver 在其冻结的 compiled snapshot 中产生确定性 candidate，支持 route group/provider selector、route-local credential、`auto` 列表及 model fallback。
- route-local credential 的 compiled config 可保存非 secret reference；旧 adapter `Auth.CredentialRef` 会合成为稳定的 `legacy-route-sha256-<full SHA-256(route ID)>` credential ID。公开 Candidate/Plan 只含安全 credential ID/priority，不包含 credential ref、secret 或 credential material。
- Resolver 对 model、provider、route、credential 四个独立 quarantine 维度读取失败时 fail closed；active quarantine 过滤候选。`Plan.Next` 只在已冻结、selector-scoped universe 内为 retry action 返回候选范围，**不是** RetryDecider，也不执行重试或 attempt budget。
- `internal/adapter.Engine` 是零状态、纯 Go、无 I/O 的运行时：`Apply` 对严格 JSON object 执行全部有限 DSL actions（set/copy/remove/rename/map_enum/clamp_number/set_header/set_query），以 selected model bounds 约束 thinking；header/query 只从 JSON string literal 注入，`ValueRef` 继续拒绝。失败返回零 `AppliedRequest`，不泄露部分 mutation。`MapResponse` 按 compiler 固定的顺序匹配：填充维度之间为 AND、同一维度内为 OR；无匹配使用固定安全 default。response mapping 已在模块内实现，尚未接入 pipeline。
- `internal/execution/retry.State` 是 request-local、serial、pure-Go、无 I/O 的 retry decision/attempt-budget 状态机：它固定 policy/Plan，管理 rule matching、candidate scope、delay、总量/同 target/总时长预算和 commit/cancel gate，但不等待、不调用 SDK，也未接 execution pipeline/runtime。`BeginAttempt` 计入的是逻辑 reservation；由于尚无 pipeline，它与 SDK preflight/`RoundTrip` 没有已接线的顺序或一一对应关系，因而不能证明已发起 transport 或 wire attempt。SDK observer 仅在每个 `RoundTrip` 调用前记录 transport-attempt observation，同样不证明网络写入。
- 三份脱敏 fixture（`fixtures/configs/{default,xfyun,anthropic}.json`）在严格解码、secret 扫描和 fixture-specific assertions 后实际编译并发布。Compiler 默认值：request `2m`、TTFT `45s`、stream idle `30s`、stream lifetime `10m`、retry backoff `200ms`、total attempts `3`、same-target attempts `2`、total retry duration `90s`。
- `internal/sdk` 提供仅有 `Complete` 的 shared non-stream port、opaque/redacted `CredentialSecret`、安全 `ClassifiedError` 与 attempt observer。并列的 `internal/sdk/openaiadapter` 使用 official `github.com/openai/openai-go/v3` v3.44.0 完成 OpenAI Chat Completions；`internal/sdk/anthropicadapter` 使用 official `github.com/anthropics/anthropic-sdk-go` v1.58.0 完成 Anthropic Messages。两者均以 per-call target/model/secret 调用且 retry=0、禁止 redirect。Anthropic adapter 使用 `WithoutEnvironmentDefaults`，校验 HTTPS target/path prefix，并在最终 transport 固定 `anthropic-version`、唯一 `x-api-key` 和允许的 header/query；其严格 OpenAPI request/response validator 覆盖 thinking authority、tools、vision，529 overloaded 分类为 unavailable（fixture 再映射为 429）。两者均只保留安全 completion/observer metadata；Anthropic TLS `httptest` 和 request/response fuzz 覆盖环境隔离、协议边界、分类和无泄漏。

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

测试不需要数据库，也不需要 Docker。`go test ./...` 包含 compiler、Store、fixture、Adapter Engine 与 C01–C27 覆盖。Engine 测试覆盖 strict JSON、全部有限 DSL actions、literal-only header/query、继续拒绝 `ValueRef`、model-bounded thinking、safe response mapping、atomicity/mutation isolation、race 与 fuzz。OpenAI Chat 与 Anthropic Messages non-stream adapters 的 TLS `httptest` 覆盖 strict contract validator、per-call target/model/secret、安全注入/鉴权、retry=0、no redirects、attempt observer、success metadata 及 timeout/transport/protocol/HTTP 分类；Anthropic 另覆盖 `WithoutEnvironmentDefaults`、HTTPS target/path prefix、唯一 `x-api-key`/固定 version、thinking authority、tools/vision、529 mapping 和 request/response fuzz。没有独立 Executor CI job；现有 `go-auth` job 在 generated freshness、generated transport/route conformance race tests 之外，还从本目录运行 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/sdk/...`。该门禁不运行数据库、runtime config source、request pipeline 或 live provider；SDK HTTP tests 只使用本地 TLS `httptest` server。

## 契约与边界

- `packages/contracts/openapi/executor/v1.yaml` 是 Executor HTTP 契约的唯一事实来源。运行时 `main` 只注册 `/healthz`（经 `internal/transport/healthz`）；generated `Handler`/`StrictHandler` 与 `executorv1api.Adapter` 仅被路由一致性测试驱动，未接入运行时 server。契约中的公开模型业务路由尚未在运行时实现，strict SSE 为 generated capability，当前不被任何运行时代码调用。generated models/strict server 随变更提交，位于 `internal/contract/executorv1/` 并供 adapter 与测试使用；现有 `go-auth` job 运行 generated freshness、generated transport/route conformance race tests，以及 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/sdk/...` 的 compiler/snapshot race 门禁。该门禁不运行数据库、runtime config source、request pipeline 或 live provider；SDK HTTP tests 只使用本地 TLS `httptest` server；仍无独立 Executor CI job、运行时业务路由或执行 pipeline 测试。Docker、运行时路由与集成验证仍待后续独立阶段（见 `docs/executor/architecture.md` 阶段 14）。
- Config compiler、Store、routing Resolver/Plan、Adapter Engine 与 retry State 尚未由 runtime config source、reload loop 或 request pipeline 消费；它们是模块内已实施的纯 Go 能力。retry State 不是 wire attempt gate：其预算记录逻辑 reservation，而 SDK observer 仅在 `RoundTrip` 前记录 transport-attempt observation。OpenAI Chat 与 Anthropic Messages non-stream adapters 同样尚未接入 pipeline/runtime routing，不能构成端到端业务能力。二者只安全使用调用方提供的 opaque per-call secret；不解析 credential ref，也不负责 credential resolution/secret injection，该职责仍留给后续阶段。Responses、Images 与 streaming 均未实施。
- Foundation 不拥有数据库、schema 或 migration，并使用 Mock/InMemory ports。未来 Executor 如需持久化，可拥有自己的数据库及其 schema 和 migration；不得访问其他服务的数据库、schema、migration 或私有源码。
- 服务间集成必须使用明确、可版本化的契约，不能以源码 import 或共享数据库替代。
