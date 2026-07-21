# Executor Service

> 作用域：`services/executor/`。继承仓库根目录及 `services/AGENTS.md`。

## 模块职责

- 负责：TokenMP 模型请求执行面的 Foundation：health、优雅关闭、Mock/InMemory ports 与 quota reservation 终态状态机。已实施 generated transport 层及路由契约一致性测试，但运行时 `main` 仍未注册任何公开业务路由。已实施 Config compiler：`snapshot.Compile` → `adapter.Compile` 校验与 normalization 原始 snapshot，检查 identities/references、provider/adapter/protocol compatibility、HTTPS BaseURL（现在还拒绝 query/fragment/userinfo：`RawQuery`/`ForceQuery`/`Fragment`，错误仅命名公开 provider key，不泄露 URL 或内容）、capability/thinking、timeouts/retries、fallback cycle，以及有限 DSL 的路径、action、header/query allowlist 与规则 priority；产出 deterministic `CompiledConfig`。三份脱敏 fixture 均在严格解码、secret 扫描后实际编译并发布；C01–C27 相关安全、默认值、immutability 与确定性测试均已实施。已实施模块内 strict secret-free config 文件源 `internal/configsource`：`LoadFile(ctx, path)` 以 1 MiB（`MaxConfigBytes`）上限、`Lstat` 拒绝 symlink/非 regular、post-open `os.SameFile` TOCTOU 校验、`io.LimitReader` bound、严格 UTF-8、结构走查（重复 key、prototype-pollution key、深度 256、节点 100000）、top-level 必须为 object、`DisallowUnknownFields` 与 trailing-data 拒绝加载 regular 非 symlink 文件，加载后以 lexical + semantic 双通道 `ScanSecrets` 拒绝密钥材料（关闭 JSON-escape 与 URL percent-encoding 旁路）；返回稳定 sentinel error（`ErrConfigBlankPath`/`NotFound`/`NotRegular`/`TooLarge`/`Empty`/`Unreadable`/`Malformed`/`SecretDetected`/`CompileFailed`/`PublishFailed`），不泄露 path/content，且 non-wrapping（`errors.Unwrap` 返 nil）。`CompileAndPublishInitial(ctx, store, path)` 经 `LoadFile` + 真实 `snapshot.Compile` 原子发布 initial generation=1 不可变 compiled snapshot，返回仅含 revision/generation/模型/provider/route/adapter 计数的 safe `InitialSnapshotMeta`（unexported 字段，无 setter，不暴露 compiled config 指针）；nil store、compile失败、已 bootstrap store 与 context cancel 均 fail-closed。该包尚未接入 `main`/env/runtime routes/reload；phase 7.7 的 config source 前置已作为模块内能力完成，下一阶段为 credential env。已实施纯 Go routing：strict `model[:group][@provider]` selector（`auto` 不得带 group）、route group/provider selector/route-local non-secret credential/auto+model fallback；legacy adapter `CredentialRef` 合成 `legacy-route-sha256-<full SHA-256(route ID)>` ID。公开 Candidate/Plan 只含安全 credential ID/priority，不含 credential ref 或 secret；Resolver/Plan 固定 revision/generation，产出 deterministic candidates，并以 model/provider/route/credential 四维 quarantine read fail-closed；`Plan.Next` 仅提供候选 action scope，不是 RetryDecider。已实施 stateless、pure-Go、无 I/O 的 Adapter Engine：strict JSON object、全部有限 DSL actions、literal-only header/query、继续拒绝 `ValueRef`、model-bounded thinking，以及 response mapping（维度间 AND、维度内 OR、compiler fixed order 与固定安全 default）；Apply 失败原子化，测试覆盖 mutation isolation、race 和 fuzz。response mapping 已由内部 Runner 的 classified non-stream failure 路径消费，尚未接入 runtime HTTP pipeline。compiler 默认值为 request `2m`、TTFT `45s`、idle `30s`、lifetime `10m`、backoff `200ms`、total attempts `3`、same-target attempts `2`、total retry duration `90s`。
- 不负责：runtime config source wiring/reload loop（模块内 `internal/configsource` 已实施 strict secret-free `LoadFile` 与 `CompileAndPublishInitial` initial generation=1 bootstrap，但未接入 `main`/env/runtime routes/reload）、运行时公开模型 API 路由注册、runtime 公开业务路由、runtime HTTP normalizer/renderer/identity/facade/reservation composition（即把 identity、runtime config source、facade 与 reservation 接入公开 runtime server/路由）、durable idempotency/replay、remote quota/credential resolver、`Retry-After` header parsing、streaming、数据库、Docker、独立 Executor CI job、生产部署；retry State 仍是 request-local、serial、pure-Go、无 I/O 的 decision/budget 状态机，`BeginAttempt` 只计逻辑 reservation，不是 wire attempt gate。已实施 HTTP transport 层（模块内，未接 runtime）：`internal/transport/executorv1api` 在 Foundation 501 adapter 之外，新增 OpenAI Chat 与 Anthropic Messages non-stream 的 raw-body 捕获中间件（`CaptureRawBody`，上限 `MaxCapturedBodyBytes` = 2 MiB，超限/不可读 body 返回协议原生 400，不使用 `MaxBytesReader` 预写）、strict contract normalizer（`NormalizeOpenAIChat`/`NormalizeAnthropicMessages`，根字段 allow-list 严格镜像 OpenAPI 的 `additionalProperties:false`，并强制 JSON 深度/节点/selector 边界、Anthropic thinking 边界 `1024 <= budget_tokens < max_tokens` 且 disabled 时省略、bounded HTTPS/`data:image` 媒体 URL、base64 解码字节上限与 `media_type` 枚举，拒绝 streaming 请求且从不回显原始请求内容）、protocol renderer（渲染协议原生 OpenAI/Anthropic 成功与错误 body，对 completion/message 执行 bounded local contract check，Anthropic `request_id` 仅来自脱敏的 SDK metadata）、`SafeStrictOptions`（path-aware 的 StrictHTTPServerOptions，request/response error handler 渲染协议原生不泄漏错误，context cancel/deadline 不写任何响应）与 DI adapter（`NewNonStream(Options{Executor, RequestIDs})` 经注入的 `NonStreamExecutor` 端口执行 CreateChatCompletion/CreateMessage，nil/typed-nil executor fail-closed 为安全 internal 错误；`New()` Foundation 适配器不变，模型操作仍返回协议原生 501；ListModels/CreateResponse/CreateImage 仍 501）。该 transport 层由 generated-handler component tests 覆盖（`adapter_integration_test.go` 将 adapter 接入 generated `NewStrictHandler` 并经 `CaptureRawBody` 中间件，以 kin-openapi 校验响应，逐条 non-stream path 驱动 generated Chi 路由）。它仍未接入 identity、runtime config source、facade/reservation composition 或公开路由；`app.NewServer`/runtime `main` 仍只经 `internal/transport/healthz` 服务 `/healthz`，不构成公开业务能力。内部 non-stream Runner 已将 Resolver-owned Plan binding、每次 attempt 的纯 Prepare/Engine/registry preflight 和 credential resolve、冻结的 request-lifetime retry policy、per-attempt request timeout、一次 Reserve/安全 terminalizer、mapped failure 与 safe execution events 组合；它不提供 wire-attempt proof、跨进程 exactly-once 或 runtime 端到端业务能力。已实施内部 shared `sdk` port，以及并列的 official `github.com/openai/openai-go/v3` **v3.44.0** OpenAI Chat Completions non-stream adapter 和 official `github.com/anthropics/anthropic-sdk-go` **v1.58.0** Anthropic Messages non-stream internal adapter。Anthropic 每次调用校验 HTTPS target/path prefix、model/opaque secret，使用 `WithoutEnvironmentDefaults`、retry=0、no redirects，最终 transport 固定唯一 per-call `x-api-key`、`anthropic-version` 和允许的 header/query；strict OpenAPI request/response validation 覆盖 thinking authority、tools、vision，529 overloaded 分类为 unavailable 并由 fixture 映射为 429。TLS `httptest`、request/response fuzz 覆盖环境隔离、协议边界、分类和无泄漏。两种 SDK adapter 均未接入 execution pipeline 或 runtime 业务路由，故不是端到端业务能力；credential resolver/secret injector、Responses、Images 与 streaming 仍未实施。contracts 侧 Executor 生成配置/脚本已落地；已生成并提交 generated models/strict server（`services/executor/internal/contract/executorv1/{models,server}.gen.go`），由 `check:generated:executor` 新鲜度检查覆盖（该检查是现有 `go-auth` CI job 中必经的门禁步骤；无独立 Executor CI job）。
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
| generated Executor v1 transport（Chi + strict server） | oapi-codegen v2.8.0 从 `openapi/executor/v1.yaml` 生成 | `internal/contract/executorv1/{models,server}.gen.go`；`StrictServerInterface` 由 `internal/transport/executorv1api.Adapter` 实现：Foundation `New()` 的 `/healthz` 返回 `200`，模型操作返回协议原生 `501 NOT_IMPLEMENTED`，不启动任何 SSE 流；DI `NewNonStream(Options{Executor, RequestIDs})` 经注入的 `NonStreamExecutor` 执行 non-stream CreateChatCompletion/CreateMessage | experimental | `packages/contracts/go/executor-v1-*.yaml` + `openapi/executor/v1.yaml` |
| non-stream HTTP transport 层（模块内，未接 runtime） | 注入的 `NonStreamExecutor` 与可选 `RequestIDSource` | `CaptureRawBody`（2 MiB raw-body 捕获）、strict contract normalizer（镜像 `additionalProperties:false`、thinking/媒体/JSON 边界）、protocol renderer（协议原生成功/错误 body + bounded local contract check）、`SafeStrictOptions`（不泄漏 error handler）；generated-handler component tests 覆盖；identity/runtime config/facade/reservation 未接入，`app.NewServer` 仍只服务 `/healthz` | internal | `internal/transport/executorv1api/{adapter,body,normalizer,renderer,strictoptions,requestid,port}.go` 与 `*_test.go`/`adapter_integration_test.go` |
| Executor route conformance test | Go test 运行环境 | `internal/server/contract_test.go` 以 kin-openapi 加载契约，遍历 generated `Handler` 的 Chi 路由，与 OpenAPI method+path 双向严格比较（7 条路由） | experimental | `packages/contracts/openapi/executor/v1.yaml` |
| config compiler | non-empty revision；models/providers/adapters/routes 集合可为空 | `snapshot.Compile` → `adapter.Compile` 返回 normalized、deterministic `CompiledConfig` 或分类 validation error；空集合编译为无业务 route 的 config；C01–C27 相关有限 DSL、配置图、默认值、继承、priority、immutability 与 determinism 均由 compiler tests 覆盖 | experimental | `internal/snapshot/compiler.go`、`internal/adapter/compiler.go`、`*_test.go` |
| immutable generation-aware snapshot publication | non-empty matching revision, non-nil compiled config, non-zero monotonic generation | deep-copy freeze + atomic Store-owned publication; `Current` returns an independent same-generation view, so later publish cannot mutate an in-flight request's revision; invalid or stale publication preserves last known good | experimental | `internal/snapshot/store.go`、`internal/snapshot/store_test.go` |
| routing selector / Resolver / Plan | frozen compiled snapshot plus strict `model[:group][@provider]` selector | deterministic, revision/generation-pinned non-secret candidates; `auto` forbids group; model/provider/route/credential quarantine read failure fails closed; `Plan.Next` exposes scope only, not retry decisions | experimental | `internal/routing/{selector,resolver}.go` and tests |
| Adapter Engine `Apply` / `MapResponse` | compiled adapter、strict JSON object、selected model thinking bounds / classified upstream metadata | atomic `AppliedRequest` 或分类错误；literal-only injection plan；response rules follow compiler order with AND-across/OR-within dimensions and fixed safe default；no I/O | internal | `internal/adapter/engine.go` 与 tests |
| shared `sdk.Client.Complete` / provider adapters | call-local HTTPS target、upstream model、opaque secret 与已应用 request；仅 OpenAI Chat 或 Anthropic Messages non-stream | official `openai-go/v3` v3.44.0 / `anthropic-sdk-go` v1.58.0 execute exactly one provider call; safe completion metadata or classified error; Anthropic uses `WithoutEnvironmentDefaults`, fixed version and sole `x-api-key` | internal | `internal/sdk/{openaiadapter,anthropicadapter}/` 与 TLS/fuzz tests |
| internal non-stream `execution.Runner` | resolver-owned frozen Plan、request/reservation ID、JSON body、thinking、local credential resolver | per-attempt Prepare/Engine/registry/auth compatibility/credential resolution; frozen retry policy and request timeout; one Reserve then safe Finalize or Release; safe mapped failure or completion/events; no public HTTP behavior | internal | `internal/execution/{runner,registry,terminal}.go` 与 tests |
| three config fixtures | `fixtures/configs/{default,xfyun,anthropic}.json` | strict decode、secret scan、fixture-specific assertions、`Compile` 与 Store publish；每份均产生 store-ready compiled config | experimental | `internal/snapshot/{fixture,compiler}_test.go`、`fixtures/configs/*.json` |
| strict secret-free config file source `LoadFile` | regular 非 symlink 文件路径、`context.Context` | 1 MiB 上限、Lstat+post-open `SameFile` TOCTOU、`LimitReader`、严格 UTF-8、结构走查（重复 key/prototype-pollution/深度 256/节点 100000）、top-level object、`DisallowUnknownFields`、trailing-data 拒绝、lexical+semantic `ScanSecrets`；返回 `snapshot.ConfigSnapshot` 或稳定 sentinel error（不泄露 path/content，non-wrapping） | internal | `internal/configsource/load.go`、`scanner.go` 与 `configsource_test.go` |
| initial snapshot bootstrap `CompileAndPublishInitial` | non-nil `*snapshot.Store`、文件路径、`context.Context` | 经 `LoadFile`+真实 `snapshot.Compile` 原子发布 initial generation=1 不可变 compiled snapshot；返回 safe `InitialSnapshotMeta`（revision/generation/计数，不暴露 compiled config）；nil store/compile失败/已 bootstrap/cancel fail-closed | internal | `internal/configsource/load.go` 与 `configsource_test.go` |

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | `packages/contracts` | Executor HTTP 契约的设计/构建时事实来源；generated Go 代码由 oapi-codegen 从其生成 | OpenAPI 文件引用 + generated Go 代码；无 runtime import contracts package | `openapi/executor/v1.yaml` → `internal/contract/executorv1/{models,server}.gen.go` | 实施或变更公开路由时验证契约与路由 |
| 依赖 | `github.com/getkin/kin-openapi` | 路由契约一致性测试在 test 时解析并 Validate OpenAPI | test-only Go 依赖 | `internal/server/contract_test.go` | 修改契约或 generated Handler 后运行 `go test ./internal/server/...` |
| 依赖 | `github.com/go-chi/chi/v5`、`github.com/oapi-codegen/runtime` | generated server 与 adapter 的运行时依赖 | Go runtime import | `go.mod` | `go build ./...` |
| 依赖 | `github.com/openai/openai-go/v3` v3.44.0、`github.com/anthropics/anthropic-sdk-go` v1.58.0 | OpenAI Chat 与 Anthropic Messages non-stream provider calls | Go runtime imports behind internal shared `sdk` port | `internal/sdk/{openaiadapter,anthropicadapter}/` | `go test ./internal/sdk/...` |
| 依赖 | `internal/snapshot` | config 文件源加载 `ConfigSnapshot` 并经 `snapshot.Compile`/`Store` 编译发布 initial generation=1 | Go internal import | `internal/configsource/load.go` | `go test ./internal/configsource/...` |

Foundation 尚无已验证的直接消费者。内部 non-stream Runner 仅由模块内 Mock/InMemory/fake 测试消费，未接入 runtime 公开业务路由、runtime HTTP normalizer/renderer composition 或 identity；non-stream HTTP transport 层（normalizer/renderer/strict options/raw-body capture/DI adapter）仅由 generated-handler component tests 消费，未接入 runtime server。routing Resolver/Plan、Adapter Engine 与 shared SDK port/OpenAI Chat、Anthropic Messages adapters 也仍无 runtime consumer。模块内 `internal/configsource`（`LoadFile`/`CompileAndPublishInitial`）仅由其包内测试消费，未接入 `main`/env/runtime routes/reload。Runner 只使用 call-local credential resolution，未提供 durable idempotency/replay、remote resolver、wire proof 或跨进程 exactly-once；公开 Candidate/Plan 不暴露 credential ref，Engine 不解析 credential ref 或注入 secret。两种 adapter 均只消费调用方提供的 opaque per-call secret，不实现 credential resolution/secret injection。generated transport 与 adapter 仅被路由一致性测试消费，未被运行时 `main` 注册。compiler 和 snapshot store 是模块内纯 Go 库，无 runtime consumer；三份 fixture 仅被 compiler/snapshot tests 消费。

## 开发与验证

```bash
go mod edit -json
go test ./...
go build ./...
```

- 格式化：`gofmt -w .`
- 最小验证：`go mod edit -json`、`go test ./...`、`go build ./...`（包括 `go test -race -count=1 ./internal/execution/...` 的 Runner/registry/terminal tests，`go test ./internal/sdk/...` 的 OpenAI Chat 与 Anthropic Messages TLS/fuzz tests，以及 `go test ./internal/transport/executorv1api/...` 的 raw-body capture、normalizer、renderer、strict options、request-id 与 generated-handler component tests）
- CI compiler/snapshot race 门禁：现有 `go-auth` job 在本目录运行 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/execution/... ./internal/requestlog/... ./internal/quota/... ./internal/sdk/... ./internal/configsource/...`；覆盖 compiler/snapshot/routing/internal Runner/requestlog/quota/SDK boundary/config source packages（包括 Engine、OpenAI 与 Anthropic adapters 与 strict secret-free 文件源）；不运行数据库、runtime config source wiring、公开 runtime pipeline 或 live provider，SDK HTTP tests 只使用本地 TLS `httptest` server，且不代表独立 Executor CI job。
- 生成物新鲜度：`pnpm --filter @tokenmp/contracts check:generated:executor`（临时目录重生成 + 字节比较；generated models/strict server 随变更提交，纳入新鲜度检查）。
- 路由契约一致性：`go test ./internal/server/...`（以 kin-openapi 加载 `openapi/executor/v1.yaml`，与 generated Handler 的 Chi 路由双向比较）。
- 契约测试：contracts package 侧 `pnpm --filter @tokenmp/contracts lint|typecheck|test` 验证契约本身。
- 集成测试：Foundation 使用 Mock/InMemory ports，不启动数据库。

## 模块边界

- 允许访问：模块自身 Go 源码、Mock/InMemory ports，以及作为设计/构建时事实来源的 Executor OpenAPI 契约。generated models/strict server 随变更提交，存在于 `internal/contract/executorv1/`，供 adapter 与一致性测试使用。Config compiler、immutable generation-aware snapshot store、routing、Adapter Engine、内部 non-stream Runner 与模块内 strict secret-free config 文件源 `internal/configsource`（`LoadFile`/`CompileAndPublishInitial`，未接 runtime）已落地；Runner 仅作模块内 composition，未接 runtime HTTP/identity。Adapter Engine 不进行 credential resolution 或 secret injection；Runner 每 attempt 通过 resolver 私有地解析 call-local secret。
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
- **DO NOT** 引入 Docker 或独立 Executor CI job/运行时路由 — generated models/strict server 已生成、提交并供 adapter 与测试使用；现有 `go-auth` job 仅复用其 Go toolchain 执行 generated freshness、generated transport/route conformance race tests，以及 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/execution/... ./internal/requestlog/... ./internal/quota/... ./internal/sdk/... ./internal/configsource/...` compiler/snapshot/internal Runner/config source race 门禁。该门禁不运行数据库、runtime config source wiring、request pipeline 或 live provider；SDK HTTP tests 只使用本地 TLS `httptest` server；Docker、运行时业务路由与集成验证仍待后续独立阶段（见 `docs/executor/architecture.md` 阶段 14）。
- **DO NOT** 将有限 DSL 扩展为脚本执行平台，或让其写 Host、Authorization、代理/转发、Content-Length、SDK 控制头、密钥引用、URL scheme/host/base path；header/query 必须是 JSON string literal，`ValueRef` 继续拒绝。禁止任意脚本、SQL、网络、文件访问或自由模板。compiler/Engine 仅接受有限 action、受限 path 及 allowlisted header/query。
- **DO NOT** 把 compiler/store/routing/Adapter Engine/retry State、SDK adapters、内部 non-stream Runner、non-stream HTTP transport 层或模块内 config 文件源 `internal/configsource` 误写为 runtime 公开业务执行：Runner 仅模块内组合，transport 层仅由 generated-handler component tests 驱动，config 文件源仅由包内测试消费；三者均未接入 identity、runtime config source wiring、facade/reservation composition 或公开路由，也无 durable idempotency/replay、remote quota/credential resolver；`app.NewServer`/runtime `main` 仍只服务 `/healthz`。response mapping 只由 Runner 的 classified non-stream failure 路径消费。retry State 只管理逻辑 reservation，Runner 也不提供 wire-attempt proof 或跨进程 exactly-once；Responses/Images/streaming 仍未实施。
- **DO NOT** 把 `Plan.Next` 当作 RetryDecider，或在 resolver 外扩大 selector/revision-pinned candidate universe：它只对已冻结候选定义 action scope；retry rule matching 与 attempt budget 由 retry State 管理，但公开 runtime execution pipeline 仍未实施。
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
