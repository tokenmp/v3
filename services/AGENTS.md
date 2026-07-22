# Services 分区

> 作用域：`services/`。继承仓库根目录 `AGENTS.md`。

## 分区职责

`services/` 用于可独立开发、测试、构建和部署的后端服务。当前服务清单：

- `auth/`：TokenMP v3 认证服务，Go 1.26.5、Chi、GORM、PostgreSQL（库 `tokenmp_auth`）。已实现 Auth Identity Flows：注册/登录、Ed25519/EdDSA Access Token 签发、opaque Refresh Token 轮换与 reuse 检测、logout/logout-all、/me、Argon2id 密码哈希与 bcrypt 兼容升级；API 契约已抽离至 `packages/contracts/openapi/auth/v1.yaml`（见 ADR 0006），API 路由由 oapi-codegen 生成的 Chi strict handler 注册（contract-first），Auth conformance test（`internal/server/contract_test.go`）是当前唯一已实施的直接消费者/验证方。模块文档：`auth/AGENTS.md`。
- `executor/`：TokenMP v3 模型请求执行服务 Mock-first Foundation，Go 1.26.5 module `github.com/tokenmp/v3/services/executor`。已实施 HTTP health、运行时配置、优雅关闭、Mock/InMemory ports、quota reservation terminal、generated models/strict server、transport adapter（Foundation `New()` 501 + DI `NewNonStream` non-stream）、raw-body capture/normalizer/renderer/strict options、Config compiler、immutable generation-aware snapshot store、三份脱敏 config fixtures、C01–C27 compiler/snapshot 安全校验与真实编译测试、routing selector/resolver/Plan/quarantine，以及 route/HTTP conformance tests 与纯 Go Adapter Engine。routing 已实施 strict `model[:group][@provider]` selector（`auto` 无 group）、route group/provider selector/route-local credential/auto+model fallback 数据模型、legacy `CredentialRef` 的全长 SHA-256 合成 ID、revision-pinned immutable Resolver/Plan、deterministic candidates 与 model/provider/route/credential 四维 fail-closed quarantine；公开 Candidate/Plan 仅含安全 credential ID/priority，不含 credential ref 或 secret；`Plan.Next` 仅划定候选范围，不是 RetryDecider。已实施 `internal/quarantinebridge`：它把 runtime quarantine port/state 适配为 routing quarantine reader（每个 routing quarantine 维度映射到独立的带前缀 runtime target，使跨维度重复 ID 不冲突），并以 fail-closed 错误处理（not-found 保留为 `routing.ErrNotFound`、context 取消归一为裸 sentinel、其余读取失败（含 nil/typed-nil port）均以 `routing.ErrQuarantineUnavailable` 暴露）。它是 anti-corruption 层，经 `internal/composition` 接入 facade。Adapter Engine 已实施 strict JSON、全部有限 DSL actions、literal-only header/query、继续拒绝 `ValueRef`、model-bounded thinking 及 safe response mapping（维度间 AND/维度内 OR、compiled order/fixed default），并有 atomic/mutation/race/fuzz coverage；response mapping 已由内部 Runner 的 classified non-stream failure 路径消费，并经 Runner/composition 接入 runtime。Config compiler 在 `internal/snapshot.Compile` → `internal/adapter.Compile` 中执行校验与 normalization；BaseURL 校验现在拒绝 query/fragment/userinfo（`RawQuery`/`ForceQuery`/`Fragment`），错误仅命名公开 provider key，不泄露 URL 或内容；默认值为 `2m/45s/30s/10m/200ms/3/2/90s`（request/TTFT/idle/lifetime/backoff/total attempts/same-target attempts/total retry duration），结果可按 generation/revision 原子发布不可变 snapshot；经 `internal/composition` 接入 runtime，未实现热重载 loop。已实施 strict secret-free config 文件源 `internal/configsource`：`LoadFile` 以 1 MiB 上限、Lstat+post-open `SameFile` TOCTOU、`LimitReader`、严格 UTF-8、结构走查（重复 key/prototype-pollution/深度 256/节点 100000）、top-level object、`DisallowUnknownFields`、trailing-data 拒绝加载 regular 非 symlink 文件，并以 lexical+semantic 双通道 `ScanSecrets` 拒绝密钥材料；返回稳定 sentinel error，不泄露 path/content（non-wrapping）。`CompileAndPublishInitial` 经 `LoadFile` + 真实 `snapshot.Compile` 原子发布 initial generation=1 不可变 compiled snapshot，返回仅含 revision/generation/计数的 safe `InitialSnapshotMeta`；该包经 `internal/composition` 在启动时接入，未实现热重载 loop。credential env `internal/credentialenv` 只接受严格的 `vault://` credential ref → `EXECUTOR_CREDENTIAL_*` 环境变量名 JSON allowlist；每个 attempt 重新读取映射环境变量以观察 rotation，并仅以 opaque secret 交给调用方；`ValidateCompiled` 在启动预检中要求 enabled authenticated route 的 enabled credential refs 与可用 mapping 精确一致，并经 composition 接入。mapping JSON 与 secret environment variables 均不得提交。identity env `internal/identityenv` 严格验证非 secret `EXECUTOR_IDENTITY_MAP_JSON` entry ID → subject/key_id/role/status/`EXECUTOR_API_KEY_*`，每次认证重读 API-key 环境变量以支持 rotation；`executorv1api.AuthMiddleware` 在 `CaptureRawBody` 外层、body read/decode 前保护全部 `/v1`，以协议原生无泄漏 401 fail closed，并保留 `/healthz` 匿名。二者经 `internal/composition` 接入 `main`、app 与公开 runtime routes（未做 Auth JWT）。已实施 user-authorized runtime composition（`internal/composition`）：runtime `main` 在 `net.Listen` 之前经 `composition.Build` 组装 store、config source、credential/identity resolver、in-memory runtime/quarantine/quota/execution log、精确 OpenAI/Anthropic SDK registry、Runner、facade、generated strict handler 与 `AuthMiddleware(CaptureRawBody(...))`，启动拒绝不受支持的 enabled SDK/protocol route；生成的 7 条路由均为运行时实际路由（匿名 health、鉴权 501 的 models/responses/images、鉴权执行的 chat/messages）。`config.Load` 要求 `EXECUTOR_CONFIG_FILE`/`EXECUTOR_CREDENTIAL_REF_MAP_JSON`/`EXECUTOR_IDENTITY_MAP_JSON`，错误 redacted。已实施内部 shared `sdk` port 及官方 `github.com/openai/openai-go/v3` **v3.44.0** 的 OpenAI Chat Completions 非流式 adapter：每次调用独立校验 HTTPS target、上游 model 与 secret，SDK retry=0、禁止 redirect；对 Chat 请求执行严格契约验证（含 tools、vision 与 thinking 字段），安全注入与唯一 Bearer 鉴权，记录不含 URL/请求/响应/凭据的 attempt observer metadata；成功返回安全 request ID/status metadata，失败将 timeout、transport、protocol 与 HTTP 状态安全分类。TLS `httptest` 覆盖 target/header 隔离、retry/redirect、分类与无泄漏。与其并列，已实施官方 `github.com/anthropics/anthropic-sdk-go` **v1.58.0** 的 Anthropic Messages 非流式内部 adapter：每次调用独立校验 HTTPS target/path prefix、上游 model 与 opaque secret，使用 `WithoutEnvironmentDefaults`、SDK retry=0 且禁止 redirect；最终 transport 仅允许 per-call `x-api-key` 和固定 `anthropic-version`，并重建允许的 header/query。对 Messages 请求及成功响应执行严格 OpenAPI 形状验证，执行层的 target model 与 effective thinking 具有权威性，并覆盖 tools、vision 与 thinking；成功仅返回安全 status/request-ID metadata，失败安全分类为 timeout、transport、protocol 或 HTTP（含 Anthropic 529 overloaded→unavailable，并由 fixture 映射为 429）。TLS `httptest`、请求/响应 fuzz 覆盖 target/header/query/environment 隔离、retry/redirect、分类和无泄漏。内部 non-stream Runner 已实施（经 composition 接入 runtime）：它将 Resolver 与 Plan owner binding 固定，并在每次 attempt 纯 `Prepare` 后重新解析该 attempt 的 credential；随后执行 Adapter Engine、精确 SDK registry 查找及官方 SDK auth compatibility 检查。Runner 在首次成功 preflight 后一次性 Reserve quota，使用安全 Terminalizer 做单一终态；冻结首个 route 的 retry policy，按 attempt request timeout 调用 SDK，映射已分类失败、记录不含敏感信息的 execution events，并以 Mock/InMemory/fake 覆盖。它不提供 wire-attempt proof 或跨进程 exactly-once，且没有 durable idempotency/replay、remote quota/credential resolver、`Retry-After` header parsing、streaming、Responses/Images 执行（路由仍 501）、数据库、Docker 或独立 Executor CI job。已实施 non-stream HTTP transport 层：`internal/transport/executorv1api` 提供 OpenAI Chat 与 Anthropic Messages non-stream raw-body 捕获（2 MiB）、strict contract normalizer、protocol renderer、`SafeStrictOptions` 与 DI adapter（`NewNonStream`），并由 generated-handler component tests 覆盖；Foundation `New()` 的模型操作仍 501，但 runtime `main` 现经 composition 注册全部生成的 7 条路由。该内部 composition 不提供 wire-attempt proof 或跨进程 exactly-once。 已实施 transport-neutral non-stream facade 前置：`internal/nonstream` 定义 secret-free `Principal`、窄 `Executor` port 与 safe sentinel；`internal/nonstreamfacade` 在每个请求 pin 当前 snapshot、以 protocol filter 解析并保留 owner-bound Plan、防御性复验 authenticated principal、CSPRNG 生成并校验 reservation ID，且仅一次调用已实施的 Runner；`internal/authcontext` 提供私有 request identity 通道，`internal/requestid` 提供 `res_` URL-safe 语法和 crypto-random source。四包均已由独立 race 包覆盖，并经 `internal/composition` 接入 `main` 与公开路由。generated files 随变更提交，`check:generated:executor` 是现有 `go-auth` CI job 中的门禁步骤。 runtime 覆盖包含 composition-level route conformance test（枚举 OpenAPI 契约全部 operation，经全包装 `AuthMiddleware(CaptureRawBody(...))` handler 断言匿名/鉴权状态）与 `cmd/executor` process binary test（实际进程启动：health、unauth chat 401、鉴权空配置 chat 404 与 501 route、invalid 配置证明未 bind listener）。模块文档：`executor/AGENTS.md`。

## 新增模块准入

新增 `services/<name>/` 前必须确认：

- 服务职责、非职责范围和所有者。
- 公开及内部契约、调用方和返回/错误语义。
- 数据库、schema、migration 和外部资源所有权。
- 上下游依赖、鉴权、超时、重试和幂等要求。
- 测试、健康检查、镜像和部署边界。
- 模块 README 和模块级 `AGENTS.md`。

## 依赖边界

- 服务可以依赖边界明确的 `packages/*` 公开入口。
- 服务间通过已确认且可版本化的网络或事件契约通信。
- 服务不得导入其他服务的私有源码。
- 服务不得直接访问其他服务拥有的数据库或运行其 migration。

## 开发与验证

具体语言、框架和命令由每个服务定义。Node.js 服务应接入根 pnpm/Turborepo 任务；Go 服务保持独立 module 边界。仓库已引入 `go.work`（`use ./services/auth`、`./services/executor`）；Go modules 为 `github.com/tokenmp/v3/services/auth` 和 `github.com/tokenmp/v3/services/executor`；后续 Go 服务通过独立变更新增 `go.work` 条目与模块级 `AGENTS.md`。

## DO NOT

- **DO NOT** 为尚未实施的服务建立空目录或虚假接口——通过独立变更渐进添加。
- **DO NOT** 使用共享数据库或源码 import 替代服务契约——保持数据与部署自治。
- **DO NOT** 在服务中提交密钥和环境专属配置——只提交模板与验证规则。

## 文档维护

创建、移动或删除服务时，同步更新当前服务清单、根 `AGENTS.md` 及提供方和消费者的依赖记录。
