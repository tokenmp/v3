# Executor Architecture Design

- 状态：设计基线已确认；Foundation、Config compiler/snapshot、routing 与 Phase 5 pure-Go Adapter Engine 已实施，后续执行能力仍按本文设计逐阶段落地
- 适用范围：TokenMP v3 `services/executor`
- 契约来源：`packages/contracts/openapi/executor/v1.yaml`
- 关联测试方案：`testing-strategy.md`

## 1. 目标

Executor 是 TokenMP 的模型请求执行面，负责：

1. 接收 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Images 等公开协议请求。
2. 验证用户 API Key 并建立请求身份。
3. 解析 `model[:route_group][@provider]` 模型选择器。
4. 选择模型、Provider、Route 与上游 Credential。
5. 按配置将公共请求转换为上游可接受的请求。
6. 以官方 OpenAI / Anthropic Go SDK 为主要上游调用路径。
7. 识别并转换普通响应、流式事件和上游错误。
8. 按配置决定同 Credential 重试、切 Credential、切 Route、切 Provider或切模型。
9. 管理业务超时、流提交边界、取消、配额和请求日志。

Foundation 不连接数据库，已通过端口接口及 Mock/InMemory 实现提供身份、配置、配额、日志和运行时状态。generated models/strict server、adapter skeleton、route/HTTP conformance 与纯 Go Adapter Engine 已实施；公开模型业务路由的 runtime 注册、credential resolver/secret injector、业务执行、RetryDecider/attempt budget、SDK/provider、流处理及持久化仍未实施；未来持久化实现不得改变核心执行流程。

## 2. 设计原则

### 2.1 策略数据化，边界代码化

适合配置驱动的内容：

- Provider 与 SDK 类型；
- 认证 Header 方式；
- 模型及 Route 映射；
- Thinking effort 和 budget 映射；
- 有限的参数 set/copy/remove/rename/map/clamp；
- 响应错误匹配与映射；
- 重试目标范围；
- Provider、Route 级业务超时；
- 隔离动作与冷却时间。

必须由 Go 代码持有的安全边界：

- Context 取消传播；
- SDK 和 HTTP 生命周期；
- 流式协议解析和状态机；
- 下游响应提交边界；
- 全局超时硬上限；
- 全局 Attempt Budget；
- 配额终态 exactly-once；
- 密钥不泄露；
- 未命中规则的 fail-safe 默认行为；
- 配置 schema、语义和冲突校验。

数据库配置不是动态脚本执行平台。规则 DSL 必须有限、类型化、可验证和可解释。

### 2.2 Protocol-first 与内部规范模型

OpenAPI 生成类型只存在于 HTTP 边界。核心执行层使用内部 `NormalizedRequest`、`RouteCandidate`、`ExecutionError` 等类型，不直接依赖 OpenAI 或 Anthropic 生成模型。

### 2.3 官方 SDK 是主要调用路径

初期 SDK 注册表：

| SDK kind | 实现 | 用途 |
|---|---|---|
| `openai` | `github.com/openai/openai-go/v3` | OpenAI Chat / Responses 及可无损表达的兼容 Provider |
| `anthropic` | `github.com/anthropics/anthropic-sdk-go` | Anthropic Messages 及可无损表达的兼容 Provider |
| `generic_http` | 受控 HTTP adapter | 官方 SDK 无法表达的 Provider extension；不是默认路径 |

SDK 内部会使用 HTTP transport，但它不了解 TokenMP 的 TTFT、流空闲、Route fallback、配额和下游提交语义。这些业务控制由 Executor 通过 Context 和流状态机实现。

所有官方 SDK client 必须显式关闭 SDK 自带自动重试；受控 HTTP fallback 和代理层同样不得产生未计入 Attempt Budget 的隐藏重试。每一次实际上游调用都必须对应一个可观察的 attempt。

### 2.4 Mock-first Repository（Foundation 已落地）

Foundation 已落地核心端口及其 Mock/InMemory 实现，且不依赖数据库：

```go
type ConfigRepository interface {
    Snapshot(context.Context) (ConfigSnapshot, error)
}

type IdentityRepository interface {
    VerifyAPIKey(context.Context, string) (Identity, error)
}

type QuotaRepository interface {
    Reserve(context.Context, ReserveInput) (Reservation, error)
    Finalize(context.Context, FinalizeInput) error
    Release(context.Context, string) error
}

type RequestLogRepository interface {
    CreateRequest(context.Context, RequestLog) error
    CompleteRequest(context.Context, RequestResult) error
    AddAttempt(context.Context, RequestAttempt) error
}

type RuntimeStateRepository interface {
    GetQuarantine(context.Context, RuntimeTarget) (Quarantine, error)
    SetQuarantine(context.Context, QuarantineInput) error
}
```

Foundation 已实施：

- `MockConfigRepository`
- `MockIdentityRepository`
- `MockQuotaRepository`
- `MockRequestLogRepository`
- `InMemoryRuntimeStateRepository`

未来实现必须区分两类端口：

- **Executor 自有持久化端口**：配置快照、运行时隔离等由 Executor 明确拥有的数据，未来可以采用 PostgreSQL 或 Redis，并由 Executor 管理 schema、migration 和凭据。
- **远程服务客户端端口**：身份、业务配额或其他服务拥有的权威数据，通过版本化 HTTP/事件契约访问，不得以 Executor 的 PostgreSQL Repository 复制或直接读取其他服务数据库。

每个 Repository 在进入持久化阶段前必须明确数据所有者。Foundation 使用 Mock/InMemory 实现，不连接数据库。

## 3. 总体数据流

```text
Client
  ↓
HTTP Transport
  ├─ request/trace id
  ├─ API key extraction
  ├─ body limit
  └─ protocol-native error rendering
  ↓
Protocol Normalizer
  ↓ NormalizedRequest
Execution Pipeline
  ├─ identity
  ├─ capability validation
  ├─ route candidates
  ├─ quota reservation
  ├─ compiled request adaptation
  ├─ SDK invocation
  ├─ response/error mapping
  ├─ retry decision
  └─ quota/log finalization
  ↓
Protocol Renderer / StreamBridge
  ↓
Client
```

## 4. 目标目录

`services/executor/` 已作为 Foundation 模块存在。下列目录是完整设计目标；除 Foundation 已落地的 `cmd/executor`、`internal/app`、`internal/config`、端口实现、health transport、`internal/contract/executorv1` 和 `internal/transport/executorv1api` 外，Config model 阶段已落地 `internal/adapter`（原始配置类型与枚举）、`internal/snapshot`（原始 `ConfigSnapshot` 与不可变 `Store`/`NewCompiledSnapshot`/`CompiledSnapshot` 发布原语）、`internal/routing`（strict selector、revision-pinned Resolver/Plan）与 `fixtures/configs/{default,xfyun,anthropic}.json`；其余目录仅在对应阶段创建。

```text
services/executor/
├── AGENTS.md
├── README.md
├── go.mod
├── go.sum
├── Dockerfile
├── cmd/executor/main.go
├── internal/
│   ├── app/                    # composition root
│   ├── config/                 # 进程配置和代码默认值
│   ├── contract/executorv1/    # oapi-codegen 生成物
│   ├── transport/executorv1api/# HTTP 边界和协议错误 writer
│   ├── execution/              # pipeline、attempt budget、状态
│   ├── identity/               # identity port + mock
│   ├── model/                  # 内部稳定模型
│   ├── routing/                # selector、candidate resolver
│   ├── adapter/                # 配置编译和规则执行引擎
│   ├── snapshot/               # immutable compiled snapshot
│   ├── sdk/                    # OpenAI、Anthropic、受控 HTTP
│   ├── protocol/               # normalize/render per protocol
│   ├── streaming/              # SSE parser、bridge、状态机和 timer
│   ├── quota/                  # quota port + mock
│   ├── runtime/                # circuit/quarantine + in-memory state
│   ├── requestlog/             # request/attempt/event port + mock
│   └── testkit/                # upstream fixtures 和 assertions
├── fixtures/
│   ├── configs/
│   ├── responses/
│   └── streams/
└── test/integration/
```

模块只在对应实施阶段创建，不预建空目录。

## 5. Contract 与流式边界

Executor 契约已存在于 `packages/contracts/openapi/executor/v1.yaml`。已生成 Executor generated models/strict server，并随变更提交（`services/executor/internal/contract/executorv1/{models,server}.gen.go`）与 transport adapter skeleton（`internal/transport/executorv1api/adapter.go`），并新增路由契约一致性测试（`internal/server/contract_test.go`）以双向比较 generated Chi 路由与契约。但运行时 `main` 仍未注册任何公开业务路由，仍只经 `internal/transport/healthz` 服务 `/healthz`；strict server 生成的 SSE 能力仅为 generated capability，当前不被任何运行时代码调用。`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁（与 Auth 新鲜度步骤相邻），同一 job 还运行 generated contract、strict adapter skeleton 与 route/HTTP conformance 的 race tests；未新增独立 Executor CI job、运行时业务路由或执行 pipeline 测试，Docker 与集成验证仍待阶段 14。

由于公开生成接口同时支持 JSON 和长时间 SSE，实施前必须通过独立变更验证普通 `ServerInterface` 与 StrictServerInterface。普通接口更自然地控制：

- `http.ResponseWriter`
- `http.Flusher`
- 客户端断开 Context
- TTFT / idle timer
- downstream commit 状态

确认后必须同步修改生成配置、生成脚本、新鲜度检查和文档，不能保留两套事实。Auth 的 strict server 选择不因此改变。

## 6. 内部请求模型

```go
type NormalizedRequest struct {
    RequestID string
    TraceID   string
    Protocol  Protocol
    Operation Operation
    Identity  Identity
    Selector  ModelSelector
    Stream    bool
    Messages  []Message
    Input     []InputItem
    Tools     []Tool
    Sampling  SamplingConfig
    Thinking  ThinkingRequest
    OriginalBody []byte
    Extensions   map[string]any
}
```

`OriginalBody` 只用于经批准的同协议无损透传，受公开请求 body 上限约束，不得写入日志；请求结束后不再保留。核心路径优先使用规范化结构，避免同时长期持有多份大对象。

Thinking 请求和有效结果分离：

```go
type ThinkingRequest struct {
    Enabled         bool
    RequestedEffort string
    RequestedBudget int
    SummaryMode     string
    Source          string
}

type EffectiveThinking struct {
    Enabled         bool
    RequestedEffort string
    EffectiveEffort string
    RequestedBudget int
    EffectiveBudget int
    Degraded        bool
    RuleID          string
}
```

## 7. 配置快照

```go
type ConfigSnapshot struct {
    Revision  string
    CreatedAt time.Time
    Global    GlobalPolicy
    Models    map[string]ModelConfig
    Providers map[string]ProviderConfig
    Routes    []RouteConfig
    Adapters  map[string]AdapterConfig
}
```

Mock fixture 仍然必须经过：

```text
schema validation
→ semantic validation
→ conflict detection
→ compile
→ immutable CompiledSnapshot
→ atomic publish
```

当前实施状态：完整的模块内 compiler 已落地：`snapshot.Compile` 把 `ConfigSnapshot` 转为 `adapter.ConfigInput` 并调用 `adapter.Compile`，实施 C01–C27 相关的 identity/reference、provider/adapter/protocol compatibility、HTTPS 无 userinfo BaseURL、有限 DSL、capability/thinking、retry/timeout、priority/conflict、fallback、immutability 与 determinism 校验；它按 code defaults → global → adapter → provider → route 合并策略，并按 priority/route ID 确定性排序，输出 `CompiledConfig`。代码默认值为 request `2m`、TTFT `45s`、stream idle `30s`、stream max lifetime `10m`、retry backoff `200ms`、max total attempts `3`、max same-target attempts `2`、max total retry duration `90s`。随后 `NewCompiledSnapshot` 深拷贝冻结，专用 `Store` 以 `atomic.Pointer` 原子发布 Store-owned copy；每个 `Current()` 返回独立视图，因此 later publish 不改变已开始请求所持 revision，且 invalid 或 stale publication 保留 last known good。三份脱敏 fixture 均严格解码、secret 扫描、通过 compiler 并实际发布。它尚未连接 config repository/reload loop 或请求 pipeline。

同一请求和其全部 attempt 必须固定使用同一 revision，保证行为可复现。routing Resolver 已从 `CompiledSnapshot` 深拷贝并固定 revision/generation；其 Plan 与任何未来 request pipeline 的实际 attempt 之间尚未接线。

## 8. Adapter 配置模型

```go
type AdapterConfig struct {
    ID      string
    Name    string
    Version int
    SDKKind SDKKind
    Auth       AuthRule
    Capability CapabilityPolicy
    Thinking   ThinkingPolicy
    Request    RequestPolicy
    Response   ResponsePolicy
    Retry      RetryPolicy
    Timeout    TimeoutPolicy
}
```

### 8.1 有限 Request DSL

允许动作：

- `set(path, literal)`
- `copy(from, to)`
- `remove(path)`
- `rename(from, to)`
- `map_enum(path, map)`
- `clamp_number(path, min, max)`
- `set_header(name, literal)`
- `set_query(name, literal)`

Header 和 query 使用白名单，且 value 必须是 JSON string literal；`ValueRef` 在 compiler 和 Engine 均继续拒绝，直到未来独立 resolver integration。规则不得覆盖 `Host`、`Authorization`、代理/转发头、Content-Length、SDK 控制头或密钥引用；URL scheme、host 和基础 path 不属于 DSL。禁止任意脚本、SQL、网络、文件访问和自由模板执行。

### 8.2 Thinking 示例

讯飞 fixture 可以声明：

```json
{
  "default_effort": "medium",
  "effort_mapping": {
    "none": "low",
    "minimal": "low",
    "low": "low",
    "medium": "medium",
    "high": "high",
    "xhigh": "high",
    "max": "high"
  },
  "budget_mapping": {
    "low": 1024,
    "medium": 8000,
    "high": 16000
  }
}
```

请求 `xhigh` 时输出：

```text
requested=xhigh
effective=high
degraded=true
```

### 8.3 响应映射

模块内 `adapter.Engine.MapResponse` 已实施，输入只接受已分类的上游 metadata，绝不接受或返回任意上游正文。它按 compiler 已固定的规则顺序（priority、specificity、ID）返回首个命中；每个已填充 matcher 维度必须匹配（AND），同一维度的列表值互为替代（OR），没有任何 matcher 的规则 fail closed、不匹配。未命中时固定 default：4xx→400 `UPSTREAM_INVALID_REQUEST`、5xx→502 `UPSTREAM_ERROR`、status 0→504 `UPSTREAM_TIMEOUT`、未分类 2xx/3xx→502 `UPSTREAM_PROTOCOL_ERROR`。它尚未接入 request/execution pipeline。

响应规则可匹配：

- 上游 HTTP status
- provider error code/type
- 受限 message contains
- finish reason
- stream event type

例如讯飞 503 映射为面向客户端的 429：

```json
{
  "id": "xfyun-503-as-rate-limit",
  "priority": 200,
  "match": {"http_statuses": [503]},
  "output": {
    "http_status": 429,
    "error_code": "UPSTREAM_RATE_LIMITED",
    "error_type": "rate_limit_error",
    "message": "上游服务繁忙，请稍后重试。"
  }
}
```

未命中规则时，代码兜底：

| 输入 | 默认结果 |
|---|---|
| 未识别上游 4xx | `UPSTREAM_INVALID_REQUEST` |
| 未识别上游 5xx | `UPSTREAM_ERROR`, HTTP 502 |
| 网络/业务超时 | `UPSTREAM_TIMEOUT` |
| 非法 JSON/SSE | `UPSTREAM_PROTOCOL_ERROR` |
| Executor 内部错误 | `INTERNAL_ERROR`, HTTP 500 |

原始上游正文不得直接返回客户端。

## 9. 重试与候选范围

### 9.1 已实施 routing 边界

`internal/routing` 已实施为不访问网络、数据库或 Provider 的纯 Go 能力。它严格解析 `model[:group][@provider]`：空段、重复/颠倒分隔符、空白/控制字符和超长输入被拒绝；`auto` 可为 `auto` 或 `auto@provider`，但不能带 group。实际候选由冻结的 compiled snapshot 决定，数据模型已支持 route group、provider selector、route-local non-secret credential、configured auto model list 和 model fallback。

旧 adapter 级 `Auth.CredentialRef` 仍可被 compiler 接受；若 route 未声明 credentials，它被合成为 route-local credential，其 ID 为 `legacy-route-sha256-` 加 route ID 的**全长** SHA-256 hex。公开 Resolver Candidate/Plan 只带安全 credential ID/priority，绝不带 credential reference 或 secret material；credential resolution/secret injection 尚未实现。

Resolver/Plan 固定 source snapshot 的 revision/generation 并在该私有 universe 内产生 deterministic candidates。每个 candidate 在 model、provider、route、credential 四个独立 quarantine 维度检查；未找到 state 可用，active state 排除 candidate，除 context cancellation/deadline 外的读取失败 fail closed。`Plan.Next` 为 retry actions 给出已冻结、selector-scoped candidate scope（并排除 visited target）；它**不是** RetryDecider，尚不匹配 retry rules、不管理 attempt budget，且不发起任何上游调用。

```go
type RetryAction string

const (
    RetryNone           RetryAction = "none"
    RetrySameCredential RetryAction = "same_credential"
    RetryNextCredential RetryAction = "next_credential"
    RetryNextRoute      RetryAction = "next_route"
    RetryNextProvider   RetryAction = "next_provider"
    RetryNextModel      RetryAction = "next_model"
)
```

RetryDecider 只输出决策，CandidateResolver 决定具体候选，Pipeline 执行尝试。

```go
type AttemptBudget struct {
    MaxTotalAttempts      int
    MaxTotalDuration      time.Duration
    MaxSameTargetAttempts int
}
```

首版建议值（实施时通过配置 schema/ADR 固化）：

```text
max total attempts       = 3
max total retry duration = 90s
max same target attempts = 2
```

配置只能在硬上限内收紧或覆盖。缺失值使用代码默认；显式 `0` 表示禁用重试。规则未命中时默认不重试、不隔离。

## 10. Timeout Policy

官方 SDK 负责调用，但业务超时由 Executor 通过 Context 和 StreamBridge 控制。

```go
type TimeoutPolicy struct {
    RequestTimeout    time.Duration
    TTFTTimeout       time.Duration
    StreamIdleTimeout time.Duration
    StreamMaxLifetime time.Duration
    RetryBackoff      time.Duration
}
```

覆盖顺序：

```text
code defaults
→ global config
→ adapter config
→ provider config
→ route config
```

首版建议默认值（实施时通过配置 schema/ADR 固化）：

```text
request timeout     2m
TTFT timeout        45s
stream idle timeout 30s
stream max lifetime 10m
retry backoff       200ms
```

代码硬上限：

```text
request timeout max     30m
TTFT timeout max        5m
stream idle timeout max 5m
stream lifetime max     60m
```

字段缺失使用默认值，显式 `0` 不表示无限。配置编译拒绝非正 duration、超过硬上限和不合理的相互关系；不静默 clamp。

> 实现对齐状态：compiler 已与本节默认值对齐：request `2m`、TTFT `45s`、idle `30s`、lifetime `10m`、backoff `200ms`、max total attempts `3`、max same-target attempts `2`、max total retry duration `90s`。它仍没有 runtime config source、reload loop 或 request-pipeline consumer。

## 11. 流式状态机

```text
init
→ connecting
→ waiting_first_semantic_event
→ committed
→ streaming
→ completed
  | failed_before_commit
  | failed_after_commit
  | client_cancelled
```

首个语义 token/event 前可以在 Attempt Budget 内重试或切换候选。HTTP ping、空 SSE 行和代理 keepalive 不算语义事件。首个语义事件定义为：

- OpenAI Chat：非空 content、reasoning 或 tool-call delta；
- OpenAI Responses：output text、reasoning summary 或 function-call arguments delta；
- Anthropic Messages：text、thinking 或 input-json delta。

Transport 在首个语义事件前缓冲必须的生命周期事件，因此 commit 与首个可输出的语义进展同时发生。

提交下游后默认不透明重试、不拼接另一 Provider 输出、不伪造成功终止：

| 协议 | 上游原生失败事件 | Executor 本地 timeout/断流 |
|---|---|---|
| OpenAI Chat | 若 SDK 原样提供错误事件则按兼容测试处理 | 关闭流，不发送 `[DONE]` |
| OpenAI Responses | 转发合法 `response.failed` / `response.incomplete` | 仅在官方 error event 经 SDK 兼容测试认证后发送；否则关闭流 |
| Anthropic Messages | 转发合法 `error` event | 发送协议合法 error event 后关闭 |

无论采用事件还是关闭连接，都记录 `failed_after_commit`、partial usage 和唯一终态。

## 12. 配额与日志

Reservation 状态：

```text
reserved → finalized
reserved → released
```

`reservation_id` 是跨 Repository/服务边界的幂等键。相同 reservation 的同一终态重复请求返回当前成功终态；相反终态竞争返回稳定冲突错误，不执行第二次结算。调用成功但响应丢失时允许使用相同 reservation id 重试。Mock 和未来持久化实现必须通过同一 Repository contract suite。

结算默认语义：commit 前失败或取消时 release；commit 后有确认 usage 时 finalize；commit 后没有确认 usage 时 release，同时记录 `unresolved_upstream_cost` 供未来对账，不猜测用户扣费。任何并发取消、超时、重试和清理路径都必须保证唯一 terminal transition。

Request/Attempt/Event 日志至少记录：

- request_id / trace_id
- model selector
- route/provider/credential 内部 ID
- attempt number
- config revision
- timeout phase
- stream state
- requested/effective thinking effort
- selected rule IDs
- retry action
- usage 与 quota reservation ID

不得记录 API Key、上游密钥、完整 prompt、thinking 原文、图片 base64 或未脱敏上游正文。

## 13. 开发阶段

| 阶段 | 分支建议 | 内容 |
|---|---|---|
| 1 | `feat/executor-foundation` | **已实施**：模块骨架、health、运行时配置、优雅关闭、Mock/InMemory ports、最小 quota terminal 状态机 |
| 2 | `refactor/executor-codegen` | **已实施**：generated models/strict server、adapter skeleton、生成物新鲜度门禁与 route/HTTP conformance；运行时尚未注册业务路由，业务执行与流处理未实现 |
| 3 | `feat/executor-config-model` | **已实施（模块内，未接 runtime）**：`snapshot.Compile` → `adapter.Compile`、C01–C27 相关有限 DSL 安全校验、继承/normalization、deterministic ordering、immutable generation-aware Store 与三份脱敏 fixture 的严格解码/真实编译/发布测试；默认值已与本设计基线对齐。 |
| 4 | `feat/executor-routing` | **已实施（纯 Go，未接 runtime/pipeline）**：strict `model[:group][@provider]` selector（`auto` 禁止 group）、route group/provider selector/route-local non-secret credential/auto+model fallback、legacy `CredentialRef` 全长 SHA-256 合成、revision-pinned immutable Resolver/Plan、deterministic candidates 与 model/provider/route/credential 四维 fail-closed quarantine；公开 Candidate/Plan 仅含安全 credential ID/priority，不含 credential ref 或 secret；`Plan.Next` 仅定义 candidate scope，不是 RetryDecider。 |
| 5 | `feat/executor-adapter-engine` | **已实施（模块内，未接 pipeline）**：stateless pure-Go/no-I/O Engine 严格解码 JSON object、执行全部有限 DSL actions、literal-only header/query、继续拒绝 `ValueRef`、按 selected model bounds 映射 thinking，并安全 map response（维度间 AND、维度内 OR、compiler established order、fixed default）；atomic/mutation/race/fuzz 覆盖已实施。 |
| 6 | `feat/executor-sdk-openai` | OpenAI SDK 非流式、SDK retry=0、Context cancel、quota cleanup |
| 7 | `feat/executor-retry-policy` | retry decision、attempt budget、quarantine |
| 8 | `feat/executor-streaming` | StreamBridge、TTFT/idle/lifetime |
| 9 | `feat/executor-sdk-anthropic` | Anthropic SDK 与原生流/error |
| 10 | `feat/executor-responses` | Responses 事件状态机 |
| 11 | `feat/executor-images` | Images 协议 |
| 12 | `feat/executor-quota` | 完整配额策略和 Repository contract suite |
| 13 | `feat/executor-request-logging` | Request/Attempt/Event 日志 |
| 14 | `build/executor-ci-docker` | Docker、CI 和集成验证 |
| 15 | 独立决策 | PostgreSQL/Redis/internal HTTP repositories |

每一阶段从最新 `main` 创建单一职责分支，通过 PR 合并。提交遵循 Conventional Commits，例如：

```text
feat(executor): add in-memory configuration repository
feat(executor): compile immutable adapter snapshots
test(executor): reject conflicting response rules
feat(executor): enforce TTFT and stream idle timeouts
```

## 14. 非目标

首轮实现不包含：

- PostgreSQL schema 或 migration；
- Admin 配置 UI；
- 任意脚本化 transform；
- 动态加载 Go plugin；
- 全局多实例隔离协调；
- 生产部署；
- 未经 fixture 和契约验证的 Provider native 协议。
