# Executor

Executor 是 TokenMP v3 的 Mock-first Foundation 模型请求执行服务。已提供 HTTP health、配置加载、优雅关闭、Mock/InMemory ports、配额 reservation 终态状态机，以及模块内 Config compiler、immutable generation-aware snapshot Store、revision-pinned routing Resolver/Plan、stateless pure-Go Adapter Engine 和 request-local、serial、pure-Go retry decision/attempt-budget State；并已实施 user-authorized 的 runtime composition（`internal/composition`）：runtime `main` 在 `net.Listen` 之前经 `composition.Build` 组装 immutable snapshot store、strict secret-free config source、credential/identity env resolver、in-memory runtime/quarantine/quota/execution log、精确 OpenAI/Anthropic SDK registry、Runner、facade、generated strict handler 与 `AuthMiddleware(CaptureRawBody(...))`，启动拒绝不受支持的 enabled SDK/protocol route。生成的 7 条路由均为运行时实际路由：匿名 `GET`/`HEAD /healthz`、鉴权 `/v1/models`·`/v1/responses`·`/v1/images/generations`（501）与鉴权 non-stream `/v1/chat/completions`·`/v1/messages` 执行。Phase 8.1 OpenAI internal stream SDK is implemented: shared `sdk.StreamClient`/`StreamSource`/`StreamEvent` provide monotonic sequence, safe metadata and adapter-owned canonical data, and the official OpenAI Chat `NewStreaming` adapter uses retry=0/no redirects/per-call auth/safe opening metadata, strict chunk parsing/classification, bounded no-raw exact-`[DONE]` observation and Close/cancel. It remains unconnected to `AttemptSession` scoped-secret integration, attempt/retry/quota, Bridge payload adapter, HTTP transport or composition; schema-valid `stream:true` stays 501. It does not claim public/provider E2E, HTTP atomicity or wire-attempt proof; stream-driver orchestration is next, then Anthropic streaming.

尚未实现 durable idempotency/replay、remote quota/credential resolver、`Retry-After` header parsing、public/provider streaming driver、streaming transport/composition 接线、Responses、Images 执行、数据库、Docker 或独立 Executor CI job。已实施 HTTP transport 层（经 composition 接入 runtime）：OpenAI Chat 与 Anthropic Messages non-stream 的 raw-body 捕获（2 MiB）、strict contract normalizer、protocol renderer、`SafeStrictOptions` 与 DI adapter（`NewNonStream`），并由 generated-handler component tests 覆盖；runtime `main` 经 composition 注册全部生成的 7 条路由。已实施内部 shared `sdk` port 及官方 `github.com/openai/openai-go/v3` **v3.44.0** 的 OpenAI Chat Completions 非流式 adapter：每次调用独立校验 HTTPS target、上游 model 与 secret，SDK retry=0、禁止 redirect；对 Chat 请求执行严格契约验证（含 tools、vision 与 thinking 字段），安全注入与唯一 Bearer 鉴权，记录不含 URL/请求/响应/凭据的 attempt observer metadata；成功返回安全 request ID/status metadata，失败将 timeout、transport、protocol 与 HTTP 状态安全分类。TLS `httptest` 覆盖 target/header 隔离、retry/redirect、分类与无泄漏。与其并列，已实施官方 `github.com/anthropics/anthropic-sdk-go` **v1.58.0** 的 Anthropic Messages 非流式内部 adapter：每次调用独立校验 HTTPS target/path prefix、上游 model 与 opaque secret，使用 `WithoutEnvironmentDefaults`、SDK retry=0 且禁止 redirect；最终 transport 仅允许 per-call `x-api-key` 和固定 `anthropic-version`，并重建允许的 header/query。对 Messages 请求及成功响应执行严格 OpenAPI 形状验证，执行层的 target model 与 effective thinking 具有权威性，并覆盖 tools、vision 与 thinking；成功仅返回安全 status/request-ID metadata，失败安全分类为 timeout、transport、protocol 或 HTTP（含 Anthropic 529 overloaded→unavailable，并由 fixture 映射为 429）。TLS `httptest`、请求/响应 fuzz 覆盖 target/header/query/environment 隔离、retry/redirect、分类和无泄漏。内部 non-stream Runner 已实施（经 composition 接入 runtime）：Resolver/Plan owner binding、纯 `Prepare`、per-attempt credential resolve、Adapter Engine、精确 SDK registry 和 official SDK auth compatibility 均在 attempt 路径中组合；它冻结 request-lifetime retry policy、对每次 SDK 调用设置 request timeout、只 Reserve quota 一次并用安全 Terminalizer 选择单一终态，映射已分类失败并记录不含敏感信息的 execution events。Mock/InMemory/fake 测试覆盖该路径。Runner 不提供 wire-attempt proof、跨进程 exactly-once、durable idempotency/replay 或 remote quota/credential resolver；`Retry-After` header parsing、public/provider streaming driver 与 transport/composition stream 接线、Responses、Images 执行、数据库、Docker 与独立 Executor CI job 仍未实现。已实施并提交 Executor v1 generated transport（`internal/contract/executorv1/{models,server}.gen.go`）与 `internal/transport/executorv1api` transport adapter（Foundation `New()` health+501 与 DI `NewNonStream` non-stream 执行，含 raw-body capture/normalizer/renderer/strict options），并新增路由契约一致性测试（`internal/server/contract_test.go`）与 generated-handler component tests（`adapter_integration_test.go`）；但运行时 `main` 现已注册全部生成的 7 条路由（经 runtime composition 接入），不再只经 `internal/transport/healthz` 服务 `/healthz`。`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁；同一 job 还运行 generated contract、transport adapter/route/HTTP conformance 的 race tests，以及 composition/config/app/process race 测试，但仍无独立 Executor CI job。

## 已实施能力

- `GET /healthz` 返回 `200` 和 `{"status":"ok"}`；`HEAD /healthz` 返回相同状态与 headers 但无响应体。两者不访问外部资源。经 runtime composition 由 generated strict handler 提供（与其他 6 条路由同源），不再由独立的 `internal/transport/healthz` 注册。
- generated Executor v1 transport（`internal/contract/executorv1`）：oapi-codegen v2.8.0 从 `openapi/executor/v1.yaml` 生成的 Chi handler 与 StrictServerInterface；存在于已提交版本且带 `DO NOT EDIT` 头。
- `internal/transport/executorv1api.Adapter` 实现 generated StrictServerInterface：Foundation `New()` 适配器的 `/healthz` 返回 `200`，模型操作（`/v1/models`、`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、`/v1/images/generations`）返回协议原生 `501` 错误，绝不启动任何 SSE 流。DI `NewNonStream(Options{Executor, RequestIDs})` 适配器经注入的 `NonStreamExecutor` 端口执行 non-stream CreateChatCompletion/CreateMessage，nil/typed-nil executor fail-closed 为安全 internal 错误；ListModels/CreateResponse/CreateImage 仍 501。该 adapter 经 runtime composition（`internal/composition`）接入公开 runtime server/路由，同时仍由路由一致性测试与 generated-handler component tests 驱动。
- identity env 与 outer HTTP auth（经 composition 接入 runtime）：`internal/identityenv` 严格加载非 secret `EXECUTOR_IDENTITY_MAP_JSON`（entry ID → subject/key_id/role/status/`EXECUTOR_API_KEY_*`），每个认证请求重读 opaque API-key 环境变量以支持 rotation；`executorv1api.AuthMiddleware` 在 `CaptureRawBody` 外层保护所有 `/v1` 路径、保留 `/healthz` 匿名、在读取 body 前以协议原生 `401` fail closed，并将仅含安全字段的 identity 写入私有 context。它经 `internal/composition` 接入 `main`、app 与公开 runtime routes。
- non-stream HTTP transport 层（经 composition 接入 runtime）：`internal/transport/executorv1api` 实现 `CaptureRawBody` 中间件（OpenAI Chat 与 Anthropic Messages POST body 上限 `MaxCapturedBodyBytes` = 2 MiB，超限/不可读返回协议原生 400，不使用 `MaxBytesReader` 预写）、strict contract normalizer（`NormalizeOpenAIChat`/`NormalizeAnthropicMessages`，根字段 allow-list 严格镜像 OpenAPI `additionalProperties:false`，强制 JSON 深度/节点/selector 边界、Anthropic thinking 边界 `1024 <= budget_tokens < max_tokens` 且 disabled 时省略、bounded HTTPS/`data:image` 媒体 URL、base64 解码字节上限与 `media_type` 枚举，拒绝 streaming 请求（Phase 8 foundation 尚未接入；不启动 SSE/不作 provider stream）且从不回显原始请求内容）、protocol renderer（渲染协议原生 OpenAI/Anthropic 成功与错误 body，对 completion/message 执行 bounded local contract check，Anthropic `request_id` 仅来自脱敏 SDK metadata）与 `SafeStrictOptions`（path-aware StrictHTTPServerOptions，request/response error handler 渲染协议原生不泄漏错误，context cancel/deadline 不写响应）。`adapter_integration_test.go` 将 adapter 接入 generated `NewStrictHandler` 并经 `CaptureRawBody`，以 kin-openapi 校验响应，逐条 non-stream path 驱动 generated Chi 路由。该层经 `internal/composition` 接入 identity、runtime config source、facade/reservation composition 与公开路由。
- transport-neutral non-stream 边界与 composition（四个模块内包，经 composition 接入 runtime 公开路由）：`internal/nonstream` 是一次 non-stream 请求的 transport-neutral 边界，拥有 `Request`/`Result` 形状、窄 `Executor` 端口、经认证的 secret-free 调用方 `Principal`，以及 transport renderer 归约为协议原生响应的安全 sentinel 错误（`ErrModelNotFound`/404、`ErrInvalidRequest`/400、`ErrUnauthorized`/401、`ErrMisconfigured`/internal）；它不导入任何 HTTP/chi/generated contract/transport 代码，仅依赖 adapter（`Protocol`/`ThinkingRequest`）与 execution（`Result`），且只有 transport-facing facade 可构造携带 trusted `Principal` 的 `Request`。`internal/nonstreamfacade` 实现 `nonstream.Executor` 的 transport-neutral composition root：每请求 pin 当前 compiled snapshot、构造保留 resolver-owned Plan 的 protocol-filtered routing Resolver、要求并防御性 revalidate trusted `Principal`、经 shared `requestid` 发出 CSPRNG reservation 标识符，并精确委托一次 `execution.Runner.Run`；它不拥有 HTTP/数据库/env/main/路由注册，不导入 transport 代码，任何不安全输入（nil/typed-nil 依赖、缺失或非法 `Principal`、缺失 snapshot、未知 model）均 fail-closed 为安全 sentinel。`internal/authcontext` 拥有贯穿请求管道的 request-scoped 经认证调用方 identity，集中私有 context key 与公开只读 accessor，使 transport auth boundary（`executorv1api.AuthMiddleware`）与 transport normalizer 共享单一 canonical、secret-free identity 通道而不在 transport surface 暴露可伪造 writer；`WithIdentity` 是仅供内部 composition 的窄 writer（`AuthMiddleware` 为唯一预期调用方），`IdentityFromContext` 返回不含 key material 的副本。shared `internal/requestid` 定义被 non-stream facade 与 execution Runner 共用的 reservation 标识符语法与来源：reservation id 服务端生成、绝不接受客户端输入，语法为 `res_` + 16–128 个 URL-safe unreserved 字符（base64 `RawURLEncoding` 字母表）；默认 `Random` source 取 16 字节 crypto-random 并以 `RawURLEncoding` 编码为 22 字符后缀，nil/typed-nil source 回退到 cryptographic default，短读返回空串 fail-closed。Runner 现以 `requestid.ValidReservationID` 校验 `ReservationID`（取代原先 trim-only 空检查），在任何 preflight/quota/upstream call 前拒绝非法 reservation id；`Selector` 新增非 canonical 的 programmatic `Protocol` filter（由 facade 等调用方设置而非从 selector 字符串解析），当非空时 `Resolve` 只放行 compiled protocol 匹配的 route，故 chat completion 请求不会解析到 anthropic_messages route，反之亦然；transport `NonStreamExecutor`/`NonStreamRequest`/`NonStreamResult` 现为 transport-neutral `nonstream.*` 的类型别名并携带 `Principal`，renderer 现将 `nonstream.ErrUnauthorized`(401) 与 `nonstream.ErrModelNotFound`(404) 渲染为协议原生响应。四包均由独立 race 包覆盖，并经 `internal/composition` 接入 identity、runtime config source、facade/reservation composition 与公开路由。
- 路由契约一致性测试（`internal/server/contract_test.go`）：以 kin-openapi 加载 `openapi/executor/v1.yaml`，遍历 generated `Handler` 的 Chi 路由，与契约 method+path 双向严格比较（7 条路由）。
- `cmd/executor` 读取配置、在 `net.Listen` 之前组装 runtime composition、监听 HTTP，并在 `SIGINT` 或 `SIGTERM` 后以配置的超时优雅关闭；HTTP server 使用 header 读取和 keep-alive 空闲连接边界。`composition.Build` 在启动时组装 immutable snapshot store、strict secret-free config source、credential/identity env resolver、in-memory runtime/quarantine/quota/execution log、精确 OpenAI/Anthropic SDK registry、Runner、facade、generated strict handler 与 `AuthMiddleware(CaptureRawBody(...))`；启动拒绝不受支持的 enabled SDK/protocol route（仅 OpenAIChat 与 Anthropic Messages）。
- `internal/config` 验证运行时配置，并要求 `EXECUTOR_CONFIG_FILE`、`EXECUTOR_CREDENTIAL_REF_MAP_JSON` 与 `EXECUTOR_IDENTITY_MAP_JSON`；错误为固定/redacted，不泄露 JSON、路径或密钥。
- `internal/app` 以注入的 handler 创建 HTTP server；nil/typed-nil handler fail-closed 返回错误。
- `internal/{configrepo,identity,quota,requestlog,runtime}` 提供端口，以及 Mock/InMemory 实现。
- quota reservation 只能从 `reserved` 迁移到 `finalized` 或 `released`：相同终态幂等，相反终态返回稳定冲突。
- `internal/snapshot.Compile` 将原始 `ConfigSnapshot` 转换为 `internal/adapter.Compile` 的输入；compiler 校验 C01–C27 所覆盖的 identity/reference、provider/adapter/protocol compatibility、HTTPS Base URL（现在还拒绝 query/fragment/userinfo：`RawQuery`/`ForceQuery`/`Fragment`，错误仅命名公开 provider key，不泄露 URL 或内容）、有限 DSL、thinking/capability、timeout/retry、priority/conflict、fallback 与确定性/无别名约束，并按继承优先级 normalization。
- `NewCompiledSnapshot` 和 `Store` 深拷贝冻结并按单调 generation 原子发布；`Current` 返回独立的不可变视图，拒绝无效或陈旧发布且保留 last known good。
- `internal/routing` 是纯 Go 的 revision-pinned Resolver/Plan：解析严格的 `model[:group][@provider]` selector；特殊 `auto` 只允许裸用或 `@provider`，不允许 route group。Resolver 在其冻结的 compiled snapshot 中产生确定性 candidate，支持 route group/provider selector、route-local credential、`auto` 列表及 model fallback。
- route-local credential 的 compiled config 可保存非 secret reference；旧 adapter `Auth.CredentialRef` 会合成为稳定的 `legacy-route-sha256-<full SHA-256(route ID)>` credential ID。公开 Candidate/Plan 只含安全 credential ID/priority，不包含 credential ref、secret 或 credential material。
- Resolver 对 model、provider、route、credential 四个独立 quarantine 维度读取失败时 fail closed；active quarantine 过滤候选。已实施 `internal/quarantinebridge`：它把 runtime quarantine port/state 适配为 routing quarantine reader（每个 routing quarantine 维度映射到独立的带前缀 runtime target，使跨维度重复 ID 不冲突），并以 fail-closed 错误处理（not-found 保留为 `routing.ErrNotFound`、context 取消归一为裸 sentinel、其余读取失败（含 nil/typed-nil port）均以 `routing.ErrQuarantineUnavailable` 暴露）。它是 anti-corruption 层，经 `internal/composition` 接入 facade。`Plan.Next` 只在已冻结、selector-scoped universe 内为 retry action 返回候选范围，**不是** RetryDecider，也不执行重试或 attempt budget。
- `internal/adapter.Engine` 是零状态、纯 Go、无 I/O 的运行时：`Apply` 对严格 JSON object 执行全部有限 DSL actions（set/copy/remove/rename/map_enum/clamp_number/set_header/set_query），以 selected model bounds 约束 thinking；header/query 只从 JSON string literal 注入，`ValueRef` 继续拒绝。失败返回零 `AppliedRequest`，不泄露部分 mutation。`MapResponse` 按 compiler 固定的顺序匹配：填充维度之间为 AND、同一维度内为 OR；无匹配使用固定安全 default。response mapping 已由内部 Runner 用于 classified non-stream failure，并经 composition/facade 接入 runtime。
- `internal/execution/retry.State` 是 request-local、serial、pure-Go、无 I/O 的 retry decision/attempt-budget 状态机；`BeginAttempt` 仍只计逻辑 reservation，不证明 wire attempt。内部 non-stream `execution.Runner` 已在每个 attempt 将 Resolver/Plan owner binding、pure `Prepare`、Engine、SDK registry/auth compatibility、credential resolve、冻结 retry policy 与 per-attempt timeout 组合，并以一次 Reserve 和安全 Terminalizer 处理 mapped failure、completion 与安全 execution events。Runner 经 composition/facade 接入 runtime，但不提供 wire proof、跨进程 exactly-once 或 durable idempotency/replay。
- 三份脱敏 fixture（`fixtures/configs/{default,xfyun,anthropic}.json`）在严格解码、secret 扫描和 fixture-specific assertions 后实际编译并发布。Config 必须保持 secret-free：编译与 fixture 路径仅接受非密钥引用与 opaque credential ID，不得内联任何 credential material、token 或 secret。fixture 文件随 VCS 提交，故其文件权限**刻意不强制**收紧（不对 `fixtures/configs/` 或编译产物施加 `0600`/`0400` 之类的权限校验）；secret-free 约束取代文件权限作为安全边界。Compiler 默认值：request `2m`、TTFT `45s`、stream idle `30s`、stream lifetime `10m`、retry backoff `200ms`、total attempts `3`、same-target attempts `2`、total retry duration `90s`。
- `internal/sdk` 提供仅有 `Complete` 的 shared non-stream port、opaque/redacted `CredentialSecret`、安全 `ClassifiedError` 与 attempt observer。并列的 `internal/sdk/openaiadapter` 使用 official `github.com/openai/openai-go/v3` v3.44.0 完成 OpenAI Chat Completions；`internal/sdk/anthropicadapter` 使用 official `github.com/anthropics/anthropic-sdk-go` v1.58.0 完成 Anthropic Messages。两者均以 per-call target/model/secret 调用且 retry=0、禁止 redirect。Anthropic adapter 使用 `WithoutEnvironmentDefaults`，校验 HTTPS target/path prefix，并在最终 transport 固定 `anthropic-version`、唯一 `x-api-key` 和允许的 header/query；其严格 OpenAPI request/response validator 覆盖 thinking authority、tools、vision，529 overloaded 分类为 unavailable（fixture 再映射为 429）。两者均只保留安全 completion/observer metadata；Anthropic TLS `httptest` 和 request/response fuzz 覆盖环境隔离、协议边界、分类和无泄漏。
- 模块内 strict secret-free config 文件源 `internal/configsource`（经 composition 启动接入）：`LoadFile(ctx, path)` 以 1 MiB（`MaxConfigBytes`）上限、`Lstat` 拒绝 symlink/非 regular、post-open `os.SameFile` TOCTOU 校验、`io.LimitReader` bound、严格 UTF-8、结构走查（重复 key、prototype-pollution key、深度 256、节点 100000）、top-level 必须为 object、`DisallowUnknownFields` 与 trailing-data 拒绝加载 regular 非 symlink 文件，加载后以 lexical + semantic 双通道 `ScanSecrets` 拒绝密钥材料（关闭 JSON-escape 与 URL percent-encoding 旁路）；返回稳定 sentinel error，不泄露 path/content，且 non-wrapping（`errors.Unwrap` 返 nil）。`CompileAndPublishInitial(ctx, store, path)` 经 `LoadFile` + 真实 `snapshot.Compile` 原子发布 initial generation=1 不可变 compiled snapshot，返回仅含 revision/generation/计数的 safe `InitialSnapshotMeta`（不暴露 compiled config 指针）；nil store、compile失败、已 bootstrap store 与 context cancel 均 fail-closed。该包现经 `internal/composition` 在启动时接入（`CompileAndPublishInitial` 发布 initial generation=1）；未实现热重载 loop。credential env `internal/credentialenv` 只接受严格的 `vault://` credential ref → `EXECUTOR_CREDENTIAL_*` 环境变量名 JSON allowlist；每个 attempt 重新读取映射环境变量以观察 rotation，并仅以 opaque secret 交给调用方；`ValidateCompiled` 在启动预检中要求 enabled authenticated route 的 enabled credential refs 与可用 mapping 精确一致，并经 composition 接入。mapping JSON 与 secret environment variables 均不得提交。identity env `internal/identityenv` 严格验证非 secret `EXECUTOR_IDENTITY_MAP_JSON` entry ID → subject/key_id/role/status/`EXECUTOR_API_KEY_*`，每次认证重读 API-key 环境变量以支持 rotation；`executorv1api.AuthMiddleware` 在 `CaptureRawBody` 外层、body read/decode 前保护全部 `/v1`，以协议原生无泄漏 401 fail closed，并保留 `/healthz` 匿名。二者经 `internal/composition` 接入 `main`、app 与公开 runtime routes；未做 Auth JWT。

## 运行时配置

| 变量 | 默认值 | 说明 |
|---|---|---|
| `EXECUTOR_HTTP_ADDR` | `127.0.0.1:8081` | HTTP 监听地址；空值使用默认值。 |
| `EXECUTOR_SHUTDOWN_TIMEOUT` | `10s` | 收到终止信号后等待优雅关闭完成的最长正 duration；显式空值、无效值或非正值会使启动失败。 |
| `EXECUTOR_READ_HEADER_TIMEOUT` | `10s` | 读取请求 headers 的最长正 duration，限制慢速 headers；缺失时使用默认，显式空值、无效值或非正值会使启动失败。 |
| `EXECUTOR_IDLE_TIMEOUT` | `60s` | keep-alive 空闲连接的最长正 duration；缺失时使用默认，显式空值、无效值或非正值会使启动失败。 |
| `EXECUTOR_CONFIG_FILE` | （必填） | strict secret-free 配置文件路径；缺失或空白会使启动失败，错误不泄露路径或内容。 |
| `EXECUTOR_CREDENTIAL_REF_MAP_JSON` | （必填） | 非 secret `vault://` credential ref → `EXECUTOR_CREDENTIAL_*` 环境变量名 JSON 映射；可较长，缺失或空白会使启动失败，错误不泄露 JSON 内容。 |
| `EXECUTOR_IDENTITY_MAP_JSON` | （必填） | 非 secret entry ID → identity 映射 JSON；可较长，缺失或空白会使启动失败，错误不泄露 JSON 内容。 |

服务不设置全局 `WriteTimeout`，以便未来 SSE 响应不被截断；也不设置总 `ReadTimeout`，避免截断流式请求。

### 运行时实际路由行为与中间件顺序

runtime `main` 在 `net.Listen` **之前**经 `composition.Build` 组装全部依赖并 fail-closed 校验，故任何配置错误（缺失/空白必填 env、配置文件不存在/含 secret/编译失败、credential/identity 映射无效、不受支持的 enabled SDK/protocol route）都使进程在未绑定监听器时退出。组装出的 HTTP handler 层次自外向内为：

```text
AuthMiddleware(identitySource)   # 外层：/healthz 匿名；保护全部 /v1（含未知路径，它们后续 404）；body 读取前以协议原生 401 fail closed
  └─ CaptureRawBody(generated)   # 内层：2 MiB raw-body 捕获、strict normalizer、SafeStrictOptions、generated Chi strict handler
```

中间件顺序的语义约束：`AuthMiddleware` 必须在 `CaptureRawBody` 外层，使被拒绝的鉴权请求绝不读取或解析 body；`AuthMiddleware` 保留 `/healthz` 匿名，并对 `/v1` 与 `/v1/*` 全部要求 `Authorization: Bearer <opaque API key>`，未知 `/v1` 路径同样被鉴权拦截（匿名 401、鉴权后 404）。

生成的 7 条路由均为运行时实际路由（见 `packages/contracts/openapi/executor/v1.yaml`）：

| 路由 | 匿名 | 鉴权 | 说明 |
|---|---|---|---|
| `GET /healthz` | `200` `{"status":"ok"}` | `200` | 不访问外部资源；`Cache-Control: no-store`。 |
| `HEAD /healthz` | `200` 无 body | `200` | 同上，无响应体。 |
| `GET /v1/models` | `401` | `501` | 未实现，协议原生 OpenAI 错误。 |
| `POST /v1/chat/completions` | `401` | 执行 / `404` | non-stream 执行；空配置下 model 未解析 → `404`。 |
| `POST /v1/messages` | `401` | 执行 / `404` | non-stream 执行；空配置下 model 未解析 → `404`。 |
| `POST /v1/responses` | `401` | `501` | 未实现。 |
| `POST /v1/images/generations` | `401` | `501` | 未实现。 |
| 未知 `/v1/*` | `401` | `404` | 鉴权拦截；鉴权后 generated router 返回 `404`。 |

该行为由 `internal/composition` 的 composition-level route conformance test（枚举 OpenAPI 契约全部 operation）与 `cmd/executor` 的 process binary test（实际进程启动、health/unauth chat/鉴权空配置 chat 404 与 501 route、invalid 配置证明未 bind）验证。

### Phase 8.1 OpenAI internal stream SDK（未接 runtime）

已实现内部 shared `sdk.StreamClient`、`StreamSource` 与 `StreamEvent`：事件含 monotonic `Sequence`、safe Meta 与 adapter-owned canonical `Data`。official OpenAI Chat `openai-go` `NewStreaming` adapter 每次仅开一个 stream，retry=0、禁止 redirect、per-call Bearer auth，并只返回安全 opening 2xx status/request-ID metadata；它严格 parse/classify chunk，以 bounded no-raw SSE observer 证明精确一个 `[DONE]`，`Close`/cancel 会释放 in-flight read。

此能力尚未接入 `AttemptSession` scoped-secret integration、attempt/retry/quota、Bridge payload adapter、HTTP transport 或 composition；因此 schema-valid `stream:true` 仍返回 501。它不表示 public/provider E2E、HTTP atomicity 或 wire-attempt proof；下一阶段是 stream-driver orchestration。

### 空配置启动

一份无 models/providers/routes 的 secret-free 配置会编译为无业务 route 的 compiled snapshot（initial generation=1）。该配置下进程可健康启动并提供 `/healthz`，但所有 `POST /v1/chat/completions` 与 `POST /v1/messages` 鉴权请求会因 model 未解析而返回 `404`；`/v1/models`、`/v1/responses`、`/v1/images/generations` 仍为 `501`。credential ref 映射可为空 JSON `{}`（无 enabled route 则无 credential 需求），但 identity 映射必须含至少一个 active service/admin identity 以使鉴权请求可成功。

### 安全本地示例（占位符，无 secret）

以下示例仅用于本地运行与测试；所有占位值均为非 secret。生产部署、真实 credential 与 API key 不得提交。

```sh
# 1) 最小空配置（secret-free）
cat > /tmp/executor-config.json <<'JSON'
{
  "Revision": "local-empty",
  "CreatedAt": "2026-07-22T00:00:00Z",
  "Models": {},
  "Providers": {},
  "Routes": [],
  "Adapters": {}
}
JSON

# 2) 必填 env（占位符，不含真实 secret）
export EXECUTOR_HTTP_ADDR=127.0.0.1:8081
export EXECUTOR_SHUTDOWN_TIMEOUT=10s
export EXECUTOR_CONFIG_FILE=/tmp/executor-config.json
# 无业务 route 时可为空映射；仅当有 enabled route 才需 vault:// ref → EXECUTOR_CREDENTIAL_* 映射
export EXECUTOR_CREDENTIAL_REF_MAP_JSON='{}'
# entry ID → identity；api_key_env 指向下面设置的 opaque key 环境变量
export EXECUTOR_IDENTITY_MAP_JSON='{"local":{"subject":"local","key_id":"kid-local","role":"service","status":"active","api_key_env":"EXECUTOR_API_KEY_LOCAL"}}'
# opaque API key（占位符，勿提交真实值）
export EXECUTOR_API_KEY_LOCAL='tm-local-placeholder-not-a-real-secret'

# 3) 启动
./executor

# 4) 验证：匿名 health、鉴权 501 route、空配置 chat 404
curl -s http://127.0.0.1:8081/healthz
# {"status":"ok"}
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8081/v1/models   # 匿名 → 401
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $EXECUTOR_API_KEY_LOCAL" http://127.0.0.1:8081/v1/models   # 鉴权 → 501
curl -s -o /dev/null -w '%{http_code}\n' -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $EXECUTOR_API_KEY_LOCAL" \
  -d '{"model":"missing","messages":[{"role":"user","content":"hi"}],"stream":false}' \
  http://127.0.0.1:8081/v1/chat/completions   # 鉴权空配置 → 404
```

`EXECUTOR_CREDENTIAL_REF_MAP_JSON`、`EXECUTOR_IDENTITY_MAP_JSON` 与各 `EXECUTOR_API_KEY_*`/`EXECUTOR_CREDENTIAL_*` 环境变量的实际值均不得提交到版本控制。

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

runtime composition route conformance（枚举 OpenAPI 契约全部 operation，经全包装 handler 断言匿名/鉴权状态）：

```bash
go test ./internal/composition/...
```

process binary test（实际进程启动：health、unauth chat、鉴权空配置 chat 404 与 501 route、invalid 配置证明未 bind）：

```bash
go test ./cmd/executor/...
```

测试不需要数据库，也不需要 Docker。`go test ./...` 包含 compiler、Store、fixture、Adapter Engine 与 C01–C27 覆盖。Engine 测试覆盖 strict JSON、全部有限 DSL actions、literal-only header/query、继续拒绝 `ValueRef`、model-bounded thinking、safe response mapping、atomicity/mutation isolation、race 与 fuzz。OpenAI Chat 与 Anthropic Messages non-stream adapters 的 TLS `httptest` 覆盖 strict contract validator、per-call target/model/secret、安全注入/鉴权、retry=0、no redirects、attempt observer、success metadata 及 timeout/transport/protocol/HTTP 分类；Anthropic 另覆盖 `WithoutEnvironmentDefaults`、HTTPS target/path prefix、唯一 `x-api-key`/固定 version、thinking authority、tools/vision、529 mapping 和 request/response fuzz。没有独立 Executor CI job；现有 `go-auth` job 在 generated freshness、generated transport/route conformance race tests 之外，还从本目录运行 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/execution/... ./internal/requestlog/... ./internal/quota/... ./internal/sdk/... ./internal/configsource/... ./internal/credentialenv/... ./internal/identityenv/... ./internal/quarantinebridge/... ./internal/nonstream/... ./internal/nonstreamfacade/... ./internal/authcontext/... ./internal/requestid/... ./internal/streaming/...`。该门禁不运行数据库、live provider 或远端 request pipeline；composition route conformance 与 process binary test 覆盖 runtime wiring（全包装 handler 的匿名/鉴权路由行为与实际进程启动/失败）。`./internal/execution/...` 覆盖 internal Runner、registry、terminal 与 request-local retry State，`./internal/requestlog/...` 与 `./internal/quota/...` 覆盖 Runner 使用的日志和 quota ports，`./internal/configsource/...` 覆盖 strict secret-free 文件源与 initial generation=1 bootstrap；SDK HTTP tests 只使用本地 TLS `httptest` server。`./internal/quarantinebridge/...` 被显式列出是因为它与 `./internal/routing/...` 是独立 package，routing 的 race pattern 不会自动测试它，必须单独加入包列表才会被覆盖。

## 契约与边界

- `packages/contracts/openapi/executor/v1.yaml` 是 Executor HTTP 契约的唯一事实来源。运行时 `main` 现经 `internal/composition` 注册全部生成的 7 条路由；generated `Handler`/`StrictHandler` 与 `executorv1api.Adapter`（含 Foundation `New()` 与 DI `NewNonStream`）由 runtime composition 接入，并由路由一致性测试与 generated-handler component tests 驱动。契约中的 OpenAI Chat 与 Anthropic Messages non-stream 已在运行时执行（鉴权），`/v1/models`·`/v1/responses`·`/v1/images/generations` 仍为 501，strict SSE 为 generated capability，当前不被任何运行时代码调用。契约媒体安全边界：有限 request 对象关闭（`additionalProperties:false`）；`ChatTool.function.parameters`、`AnthropicTool.input_schema`、`ResponseTool.parameters`、`AnthropicContentBlock.input`、`CreateResponseRequest.text.format.schema` 等 free-form JSON/JSON Schema 字段有意开放（无 `additionalProperties` 约束）以透传任意合法 schema；成功响应的 `ChatMessage`、`ChatContentPart`、`ChatToolCall` 保持可扩展（无 `additionalProperties:false`）以校验 provider extension；image URL 仅接受 bounded HTTPS（scheme https、非空 host、无 userinfo）或带 image MIME 与 `;base64` 的 `data:` URL，HTTP 及其他 scheme 被拒绝，Anthropic `source.media_type` 枚举为 image/jpeg|png|gif|webp，base64 须为标准 padded 且解码后非空、在解码字节上限内。generated models/strict server 随变更提交，位于 `internal/contract/executorv1/` 并供 adapter 与测试使用；现有 `go-auth` job 运行 generated freshness、generated transport/route conformance race tests，以及 `go test -race -count=1 ./internal/adapter/... ./internal/snapshot/... ./internal/routing/... ./internal/execution/... ./internal/requestlog/... ./internal/quota/... ./internal/sdk/... ./internal/configsource/... ./internal/credentialenv/... ./internal/identityenv/... ./internal/quarantinebridge/... ./internal/nonstream/... ./internal/nonstreamfacade/... ./internal/authcontext/... ./internal/requestid/... ./internal/streaming/... ./internal/composition/... ./internal/config/... ./internal/app/... ./cmd/executor/...` 的 compiler/snapshot/internal Runner/config source/composition race 门禁。该门禁不运行数据库、live provider 或 request pipeline 的远端调用；SDK HTTP tests 只使用本地 TLS `httptest` server；仍无独立 Executor CI job。`./internal/quarantinebridge/...` 被显式列出是因为它与 `./internal/routing/...` 是独立 package，routing 的 race pattern 不会自动测试它。Docker、独立 Executor CI job 与远端/真实 provider 集成仍待后续独立阶段（见 `docs/executor/architecture.md` 阶段表；runtime 路由 wiring 已由 composition route conformance 与 process binary test 验证）。
- Config compiler、Store、routing Resolver/Plan、Adapter Engine 与 retry State 经 runtime composition（`internal/composition`）间接消费；模块内 strict secret-free config 文件源 `internal/configsource`（`LoadFile`/`CompileAndPublishInitial`）经 composition 启动接入，但未实现热重载 loop。retry State 不是 wire attempt gate：其预算记录逻辑 reservation，而 SDK observer 仅在 `RoundTrip` 前记录 transport-attempt observation。OpenAI Chat 与 Anthropic Messages non-stream adapters 经内部 Runner 与 composition 接入 runtime routing（chat/messages 执行）；二者只安全使用调用方提供的 opaque per-call secret，不解析 credential ref，也不负责 credential resolution/secret injection（该职责由 `credentialenv` + composition 承担）。Responses、Images 与 public/provider streaming driver、transport/composition stream 接线均未实施（models/responses/images 路由仍 501）。
- Config 安全边界：所有 config（含 `fixtures/configs/*.json` fixture 与任何 runtime config source 产出）必须 secret-free，只能携带 non-secret credential reference 或 opaque credential ID，绝不内联 secret/credential material/token。因 fixture 随 VCS 提交且经 secret 扫描，文件系统权限（如 `0600`/`0400`、owner-only 读）被**有意不强制**：secret-free 约束是唯一安全边界，文件权限不作为额外防线，也不得依赖其作为安全控制。
- Foundation 不拥有数据库、schema 或 migration，并使用 Mock/InMemory ports。未来 Executor 如需持久化，可拥有自己的数据库及其 schema 和 migration；不得访问其他服务的数据库、schema、migration 或私有源码。
- 服务间集成必须使用明确、可版本化的契约，不能以源码 import 或共享数据库替代。
