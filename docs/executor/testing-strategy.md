# Executor Testing Strategy

- 状态：测试设计基线；Foundation 测试已实施，后续阶段测试尚未实施
- 适用范围：TokenMP v3 `services/executor`
- 架构来源：`architecture.md`
- API 契约：`packages/contracts/openapi/executor/v1.yaml`

Foundation 已实施的测试范围：运行时配置校验、`GET` / `HEAD /healthz`、优雅关闭、config、identity、quota、request log 和 runtime 端口的 Mock/InMemory contract 测试，以及 generated transport 的生成物标记检查（`freshness_test.go`）与路由契约一致性测试（`internal/server/contract_test.go`）；其中 quota 覆盖 reservation 从 `reserved` 到 `finalized` 或 `released` 的唯一终态、同终态幂等和相反终态冲突。本文其余 SDK、业务 adapter、公开模型运行时路由、流处理、集成、fuzz、性能、Docker 与 CI 测试仍是后续设计。

## 1. 测试目标

Executor 测试不仅验证“请求成功”，还必须证明：

1. OpenAI 与 Anthropic 公共协议兼容。
2. 配置规则能被安全编译、确定性匹配和解释。
3. Thinking 映射和降级正确且可审计。
4. 上游错误能按 Provider 配置映射，同时具有代码兜底。
5. 重试不会无限循环、重复输出、重复扣费或越过客户端 deadline。
6. TTFT、stream idle 和总时长超时可以真正终止卡住的上游。
7. 流提交后绝不透明拼接另一上游响应。
8. 客户端取消能够传播到 SDK 和上游。
9. Mock Repository 与未来持久化实现共享同一套契约测试。
10. 日志不泄露密钥、prompt、thinking 原文和原始错误正文。

## 2. 测试分层

```text
Contract tests
  ↓
Pure unit tests
  ↓
Component tests with mocks
  ↓
Protocol fixture replay
  ↓
SDK adapter tests using httptest upstreams
  ↓
Executor HTTP integration tests
  ↓
Race / fuzz / property / resilience tests
  ↓
Optional live-provider certification (manual/isolated, not baseline CI)
```

### 2.1 Contract tests

验证 `packages/contracts/openapi/executor/v1.yaml`：

- OpenAPI 可解析并通过 Validate；
- operationId 全仓唯一；
- 六个预期端点存在；
- OpenAI 与 Anthropic 错误 schema 不混用；
- `/v1/models` 包含 capability、max output 和 thinking 描述；
- 流式端点声明 `text/event-stream`；
- 生成代码与契约新鲜；
- 实际路由 method+path 与契约双向一致。

已实施：

```text
packages/contracts/tests/openapi-executor-v1.test.mjs   # 契约本身验证
services/executor/internal/contract/executorv1/freshness_test.go   # 生成物标记检查
services/executor/internal/server/contract_test.go   # generated Handler 路由与契约双向比较
```

注：`internal/server/contract_test.go` 以 kin-openapi 加载契约（对 `description`/`nullable` 与 `$ref` 的 OAS 3.0 同位词法使用 `AllowExtraSiblingFields` 宽容，与项目契约验证器一致），遍历 generated `Handler` 的 Chi 路由，与契约 method+path 双向比较（7 条路由）。该测试仅证明生成路由与契约一致，不代表运行时已注册业务路由——运行时 `main` 仍只服务 `/healthz`。

### 2.2 Pure unit tests

不启动 HTTP server，不使用真实 SDK transport，重点覆盖：

- selector parser；
- timeout/default/hard-limit 合并；
- config compiler；
- thinking map；
- rule matcher；
- retry decision；
- attempt budget；
- stream state transitions；
- protocol error renderer；
- quota state machine。

### 2.3 Component tests

以 Mock Repository 和 fake SDK adapter 运行完整 Pipeline：

```text
NormalizedRequest
→ identity
→ route
→ quota
→ adapt
→ fake SDK
→ response/retry
→ quota/log terminal
```

断言调用次数、顺序、终态和 explain trace。

### 2.4 Protocol fixture replay

将已审查的 JSON/SSE fixture 作为协议回归事实：

```text
fixtures/responses/openai/*.json
fixtures/responses/anthropic/*.json
fixtures/streams/openai-chat/*.sse
fixtures/streams/openai-responses/*.sse
fixtures/streams/anthropic-messages/*.sse
fixtures/configs/*.json
```

fixture 必须脱敏，禁止保存生产请求、用户内容或真实密钥。

### 2.5 SDK adapter tests

使用 `httptest.Server` 作为模拟 Provider。官方 SDK 指向测试 base URL，验证：

- SDK 实际发送的 path、header 和 body；
- retry 由 Executor 控制，SDK 自身 retry 被关闭；
- Context timeout/cancel 可中断请求；
- stream event 正确到达 StreamBridge；
- upstream request id 被记录但不泄露密钥。

### 2.6 HTTP integration tests

启动完整 Executor HTTP handler，注入 Mock Repository 和测试 SDK registry，从客户端协议视角验证：

- API Key header；
- body decode；
- JSON / SSE response；
- OpenAI / Anthropic 错误形状；
- request id；
- 客户端取消；
- quota/log 调用。

不需要数据库。

## 3. Mock/Testkit 设计

### 3.1 Mock Config Repository

支持：

- 返回指定 revision；
- 返回编译错误；
- 模拟配置刷新；
- 验证请求中途替换 snapshot 不影响当前请求；
- 返回空配置和缺失引用。

### 3.2 Mock Identity Repository

固定 Key：

| Key | 结果 |
|---|---|
| `tm-test-valid` | active user |
| `tm-test-disabled` | disabled user |
| `tm-test-admin` | admin identity |
| `tm-test-invalid` | invalid key |
| 空值 | missing credentials |

### 3.3 Mock Quota Repository

记录：

- reserve/finalize/release 次数；
- reservation ID；
- terminal state；
- 重复 terminal 调用；
- 可注入 reserve/finalize/release 错误；
- Context cancel 后清理行为。

核心 invariant：

```text
对任一 reservation：有效 terminal state 只能是 finalized 或 released 之一。
相同 terminal 请求可用同一 reservation id 幂等重放；相反 terminal 请求必须稳定冲突。
```

Mock Repository 还必须模拟“服务端已应用 terminal transition，但调用方收到 timeout”，验证调用方以相同 reservation id 重试后不会重复结算。

### 3.4 Mock Request Log Repository

记录完整调用序列，并提供脱敏断言：

```go
AssertNoSecrets(t, records)
AssertSingleTerminalUpdate(t, requestID)
AssertAttemptOrder(t, requestID, expected)
```

### 3.5 Upstream Scenario Server

```go
type UpstreamScenario struct {
    StatusCode int
    Headers map[string]string
    Body string

    HeaderDelay time.Duration
    TTFTDelay   time.Duration
    EventDelay  time.Duration

    HeartbeatsBeforeFirstToken int
    DisconnectAfterEvents      int
    NeverSendHeaders           bool
    NeverSendSemanticEvent     bool
    BlockAfterEvents           int
}
```

支持原子计数：

- received requests；
- cancelled requests；
- active connections；
- request bodies；
- auth headers；
- attempt order。

## 4. Config Compiler 测试矩阵

| 编号 | 场景 | 期望 |
|---|---|---|
| C01 | 最小有效配置 | 编译成功 |
| C02 | 空配置 | 编译成功但无业务 route；readiness/调用按策略失败 |
| C03 | 重复 adapter id/name | 拒绝 |
| C04 | Route 引用未知 Provider | 拒绝 |
| C05 | Provider 引用未知 Adapter | 拒绝 |
| C06 | 未知 SDK kind | 拒绝 |
| C07 | 重复 rule id | 拒绝 |
| C08 | 同 scope/priority/specificity 的冲突 response rule | 拒绝 |
| C09 | 两个 request rule 写同一路径不同值 | 拒绝 |
| C10 | remove Provider 必填字段 | 拒绝 |
| C11 | 不支持的 DSL action | 拒绝 |
| C12 | 非法 JSON path | 拒绝 |
| C13 | Thinking mapping 缺少 Provider 声明支持的 level | 编译拒绝；不允许运行时猜测 |
| C14 | timeout 为负数 | 拒绝 |
| C15 | timeout 字段缺失 | 使用默认值 |
| C16 | timeout 显式为 0 | 拒绝；不解释为无限或缺失 |
| C17 | timeout 超硬上限 | 拒绝，不静默 clamp |
| C18 | TTFT > request timeout | 拒绝 |
| C19 | idle > stream lifetime | 拒绝 |
| C20 | retry attempts 字段缺失 | 使用代码默认 |
| C21 | retry attempts 显式为 0 | 禁用重试 |
| C22 | retry attempts 超硬上限 | 拒绝 |
| C23 | fallback 形成循环 | 拒绝 |
| C24 | 配置 map/slice 编译后被原始对象修改 | CompiledSnapshot 不受影响 |
| C25 | snapshot revision 缺失 | 拒绝 |
| C26 | rule priority 相同但 specificity 不同 | 更具体规则确定性优先 |
| C27 | 完全相同规则顺序随机化 | 编译结果和运行结果一致 |

## 5. Model Selector 与 Routing 测试

### 5.1 Selector

有效：

```text
gpt-4o
gpt-4o:fast
gpt-4o@openai
gpt-4o:fast@openai
auto
```

无效：

```text
空字符串
:model
model:
model@
model@@provider
model:group:extra
model@provider:group
含空白或控制字符的 selector
超过长度上限
```

### 5.2 Candidate Scope

构造至少：

```text
model A
  provider P1
    route R1 credential K1
    route R1 credential K2
    route R2 credential K3
  provider P2
    route R3 credential K4
model B
  provider P3 route R4 credential K5
```

分别验证：

- `same_credential` 只返回 K1；
- `next_credential` 返回同 Route 的 K2；
- `next_route` 返回同模型/组的 R2；
- `next_provider` 返回 P2；
- `next_model` 只在 auto/fallback policy 下返回 B；
- quarantined candidate 被过滤；
- 当前配置 revision 中不存在的 candidate 不出现；
- route priority 稳定且 tie-break 确定。

## 6. Thinking 测试矩阵

| 编号 | 用户请求 | Provider 配置 | 期望 |
|---|---|---|---|
| T01 | 不请求 thinking | 支持 | 不强制启用，除非配置明确默认启用 |
| T02 | `low` | 讯飞 low/medium/high | effective=low |
| T03 | `medium` | 讯飞 | effective=medium |
| T04 | `high` | 讯飞 | effective=high |
| T05 | `xhigh` | 讯飞 | effective=high, degraded=true |
| T06 | `max` | 讯飞 | effective=high, degraded=true |
| T07 | `minimal` | 讯飞 minimal→low | effective=low |
| T08 | OpenAI `reasoning_effort` | OpenAI SDK | 正确注入 |
| T09 | Responses `reasoning.effort` | OpenAI SDK | 正确注入 |
| T10 | Anthropic budget 16000 | Anthropic SDK | thinking enabled，budget 保留/映射 |
| T11 | budget < 1024 | Anthropic | 请求拒绝 |
| T12 | budget >= max_tokens | Anthropic | 请求拒绝 |
| T13 | 模型不支持 thinking | 用户请求 thinking | 能力错误，不静默删除 |
| T14 | Provider 无映射且无法同协议透传 | 请求 thinking | 明确拒绝 |
| T15 | 降级发生 | 日志 | 记录 requested/effective/rule id，不记录 thinking 原文 |
| T16 | 配置变更发生在请求中途 | 当前请求 | 使用启动时 revision 和映射 |
| T17 | source effort 未知 | 任意 | 400，不默认变 medium |
| T18 | thinking disabled | Provider 默认启用 | 尊重显式关闭，除非 Provider 不允许且应拒绝 |

## 7. Request Rule 测试

每种 DSL 动作至少覆盖成功与失败：

- set literal；
- set from effective thinking；
- copy；
- remove；
- rename；
- map enum；
- clamp number；
- set header；
- set query。

安全测试：

- 禁止修改认证密钥占位；
- 禁止写入未允许 header；
- 禁止移除 model；
- 禁止递归或超深 path；
- 输入 body 超限；
- 规则执行不得修改原始 `NormalizedRequest`；
- 同一 compiled adapter 可被并发请求安全复用。

## 8. Response Mapping 测试

### 8.1 HTTP 错误

| 场景 | 期望 |
|---|---|
| 讯飞 503 + 专属规则 | 客户端 429 `UPSTREAM_RATE_LIMITED` |
| 通用 429 | 429 rate limit |
| 401 | `UPSTREAM_AUTH_INVALID` |
| 403 | `UPSTREAM_PERMISSION_DENIED` |
| 500 未配置 | 502 `UPSTREAM_ERROR` |
| 418 未配置 | 安全的 `UPSTREAM_INVALID_REQUEST` 或固定兜底，不能透传正文 |
| HTML 502 页面 | 502，正文被脱敏截断，仅内部记录 |
| 空 body 5xx | 使用代码默认 message |
| 非法 UTF-8 | 不 panic；返回协议错误 |

### 8.2 HTTP 200 中的错误

- OpenAI `{error:{...}}`；
- Anthropic `{type:"error",error:{...}}`；
- Responses `status=failed`；
- 200 + HTML；
- 200 + malformed JSON；
- content-type 错误。

在下游未提交时，这些必须转换成正常 HTTP 错误，而不是当作成功。

### 8.3 多规则命中

- Provider scope 优于 global；
- 高 priority 优于低 priority；
- exact code 优于 message contains；
- 完全冲突在编译阶段拒绝；
- selected rule id 出现在 explain trace。

## 9. Retry 与 Attempt Budget 测试

| 编号 | 场景 | 期望 |
|---|---|---|
| R01 | 未命中 retry rule | 不重试 |
| R02 | same credential transient failure | 同目标最多达到上限 |
| R03 | next credential | K1 失败后 K2 |
| R04 | next route | R1 后 R2 |
| R05 | next provider | P1 后 P2 |
| R06 | next model | 仅 auto/fallback policy 生效 |
| R07 | max total attempts=3 | 第 4 次永不执行 |
| R08 | max total duration 耗尽 | 停止重试 |
| R09 | 客户端 deadline 不足下一次尝试 | 停止 |
| R10 | 客户端取消 | 不重试 |
| R11 | 下游已 commit | 不重试 |
| R12 | 参数错误/模型不存在 | 不重试 |
| R13 | 401 同 key | 不重试同 key；按规则可 next credential |
| R14 | 429 + Retry-After 在预算内 | 等待或切换，按策略确定 |
| R15 | Retry-After 超剩余 deadline | 不等待 |
| R16 | retry rule 形成 candidate 循环 | visited target 防循环 |
| R17 | 配额 reserve 后 attempt 失败 | reservation 正确 release/finalize |
| R18 | 同一 request 跨 route | config revision 不变 |
| R19 | SDK 误启用自身 retry | 测试应检测请求次数并失败 |
| R20 | 并发请求共享 RetryDecider | 无数据竞争 |

## 10. Timeout 测试

必须使用较短测试时长或可注入 fake clock，避免测试本身运行分钟级。

### 10.1 Request timeout

- 上游永不返回 header；
- deadline 后 Context 被取消；
- SDK 调用退出；
- upstream server 观察到连接取消；
- quota/log 正确终态；
- goroutine 不泄漏。

### 10.2 TTFT timeout

- header 立即返回但首 token 延迟超过阈值；
- 只发空行不刷新 TTFT；
- 只发代理 keepalive 不刷新 TTFT；
- Anthropic ping 可证明连接存活，但不满足首语义事件，因此不取消 TTFT；首 token 后 ping 可刷新 transport activity，但不能绕过 stream max lifetime；
- commit 前 TTFT timeout 可按规则切候选；
- 尝试预算耗尽后返回超时。

### 10.3 Stream idle timeout

- 已发第一个 token，然后无语义事件；
- idle timer 触发；
- downstream committed 后不得切 Provider；
- 输出协议级失败或中断，不发成功终止；
- 有确认 partial usage 时 finalize；没有确认 usage 时 release 并记录 `unresolved_upstream_cost`。

### 10.4 Max lifetime

- 上游持续合法地发 token，但超过最大 lifetime；
- 流被终止；
- 不能因持续 event 无限绕过硬上限。

### 10.5 Timer race

- token 到达和 TTFT timer 同时触发；
- client cancel 和 idle timer 同时触发；
- upstream completion 和 total deadline 同时触发；
- 每个请求只执行一次 terminal cleanup。

## 11. Streaming 测试矩阵

### 11.1 OpenAI Chat

- 正常多个 delta + `[DONE]`；
- usage 在最后 chunk；
- 第一个 chunk 无 content、第二个才有 content；
- tool call delta；
- error event before commit；
- error event after commit；
- EOF before `[DONE]`；
- 上游在完整 event 之间 TCP EOF/reset；
- 上游写出半个 SSE frame 后断开；
- 部分 token 后断开；
- 已 commit 后断开不得切 Provider、不得发送 `[DONE]`，body/connection 最终关闭；
- 超大 SSE event；
- CRLF 与 LF；
- 分片发生在 JSON token 中间。

### 11.2 OpenAI Responses

- `response.created` → output events → `response.completed`；
- `response.failed`；
- `response.incomplete`；
- reasoning summary events；
- output text、reasoning summary 或 function-call arguments delta 均可成为首语义事件；
- `response.created`、空 lifecycle event 或 heartbeat 不取消 TTFT；
- `response.created` 后只发 heartbeat，最终触发 TTFT；
- 已有语义事件后长期静默触发 stream idle；
- 持续 lifecycle/heartbeat 仍受 stream max lifetime；
- completed 前 EOF；
- failed/incomplete after output commit；
- 以上场景分别断言 partial usage 与 quota terminal 行为。

### 11.3 Anthropic Messages

- `message_start`；
- `content_block_start/delta/stop`；
- `message_delta`；
- `message_stop`；
- ping；
- thinking block 和 signature；
- tool_use block；
- error before/after commit；
- 缺少 message_stop。

### 11.4 Backpressure/客户端断开

- 客户端读得非常慢；
- 客户端首 token 前断开；
- 客户端已读部分 token 后断开；
- 上游 Context 立即取消；
- 不继续消耗上游 token；
- active connection 最终归零。

## 12. Protocol Error Rendering 测试

### 12.1 OpenAI endpoints

验证字段：

```json
{
  "error": {
    "message": "...",
    "type": "...",
    "code": "...",
    "param": null
  },
  "status": 429
}
```

HTTP status 与 body `status` 必须一致。

### 12.2 Anthropic Messages

验证字段：

```json
{
  "type": "error",
  "error": {
    "type": "rate_limit_error",
    "message": "..."
  },
  "request_id": "req_xxx"
}
```

禁止混入 OpenAI `code`/`param`，除非未来协议明确允许 extension。

### 12.3 方法和 Content-Type

- 405；
- 非 JSON Content-Type；
- malformed JSON；
- 多个 JSON values；
- body 超限；
- unknown fields 的契约策略；
- HEAD `/healthz` 空 body。

## 13. SDK Adapter 测试

### 13.1 OpenAI SDK

- 架构不变量：SDK 自动 retry 显式为 0，所有实际上游调用必须计入 Executor attempt；
- Chat request path/body/header；
- Responses request path/body/header；
- custom base URL；
- 非流 5xx/429、流建连及读取错误均不触发 SDK 隐式 retry；
- Executor retry 每次消耗 attempt 和总时间预算；
- request body extension 保留；
- Context cancel；
- non-stream usage；
- stream close；
- SDK error 转内部 error。

### 13.2 Anthropic SDK

- 架构不变量：SDK 自动 retry 显式为 0；
- `/v1/messages` path；
- x-api-key / version header；
- max_tokens；
- thinking budget；
- thinking signature；
- stream event；
- 5xx/429/stream error 不触发 SDK 隐式 retry；
- Context cancel。

### 13.3 Generic HTTP escape hatch

- 只有 Adapter 明确配置才允许；
- endpoint 必须通过允许策略；
- auth header 正确；
- request/response size 限制；
- 不允许访问 loopback/metadata 地址的 SSRF 测试（如果 endpoint 可配置）。

## 14. Quota 与日志测试

### 14.1 Quota lifecycle

| 场景 | 终态 |
|---|---|
| 非流成功 | finalize |
| commit 前所有 attempt 失败 | release |
| commit 后部分输出失败且 usage 已确认 | finalize |
| commit 后部分输出失败且 usage 未确认 | release，并记录 `unresolved_upstream_cost` |
| 客户端首 token 前取消 | release |
| 客户端部分输出后取消且 usage 已确认 | finalize |
| 客户端部分输出后取消且 usage 未确认 | release，并记录 `unresolved_upstream_cost` |
| finalize 临时失败 | 幂等重试，不允许再 release |
| cleanup 重复执行 | 无重复终态 |

### 14.2 Request log lifecycle

- 每个请求只有一个 terminal update；
- 每个上游调用有独立 attempt；
- retry action 与 selected rule id 记录；
- timeout phase 记录；
- config revision 记录；
- stream committed 状态记录；
- 原始上游 body 受限、脱敏；
- 密钥、Authorization、prompt、thinking 原文不存在。

## 15. Concurrency 与 Race 测试

使用：

```bash
go test -race ./...
```

覆盖：

- 并发读取同一 CompiledSnapshot；
- 后台发布新 snapshot；
- 当前请求继续使用旧 revision；
- 并发熔断状态更新；
- 同 reservation 多路径 finalize/release；
- timer/cancel/completion 竞争；
- StreamBridge 并发关闭；
- Mock Repository 自身 race-free。

稳定实现后的扩展测试（不作为 Foundation 阶段阻塞项）：

- 100–1000 个并发非流请求；
- 100 个并发流请求；
- 配置切换期间持续请求；
- 上游 429 风暴下 attempt budget 不失控。

基线 CI 不要求严格性能门槛，但不得发生 race、deadlock、panic 或 goroutine 无界增长。

## 16. Fuzz / Property Tests

### 16.1 Fuzz targets

```text
FuzzParseModelSelector
FuzzCompileAdapterConfig
FuzzApplyRequestRules
FuzzParseOpenAIChatSSE
FuzzParseOpenAIResponsesSSE
FuzzParseAnthropicSSE
FuzzMapUpstreamError
FuzzRenderProtocolError
```

不变量：

- 不 panic；
- 不越界；
- 不生成非法 UTF-8；
- 不泄露输入中的 secret marker；
- 有界内存；
- 非法输入返回稳定分类错误。

### 16.2 Property tests

- 同配置输入顺序随机化，编译结果一致；
- map effort 输出一定属于 Provider 支持集合；
- Attempt count 永不超过 budget；
- downstream committed 后 action 永不为 retry；
- reservation terminal operation 最多一次；
- Error response HTTP status 与协议 body 一致；
- 编译后的 snapshot 不被源配置后续修改影响。

## 17. Resilience 与泄漏测试

建议对关键测试使用：

- `go test -race -count=20` 重复执行竞争场景；
- goroutine baseline 对比；
- 活跃 upstream connection 最终为 0；
- response body 全部关闭；
- timer/ticker 全部停止；
- Context cancel 后固定时间内 pipeline 退出。

可引入但不必第一阶段引入的工具：

- `go.uber.org/goleak`；
- 自定义 connection counter transport；
- fake clock。

引入第三方测试依赖前需评估维护价值。

## 18. Benchmark 与性能测试

建议 benchmark：

```text
BenchmarkSnapshotRead
BenchmarkSelectorParse
BenchmarkCompileAdapterConfig
BenchmarkApplyThinkingRule
BenchmarkApplyRequestRules
BenchmarkMatchResponseRules
BenchmarkRetryDecision
BenchmarkOpenAIChatSSEParse
BenchmarkResponsesSSEParse
BenchmarkAnthropicSSEParse
```

性能原则：

- 请求热路径不读取数据库；
- 不动态编译正则；
- 不完整缓冲流；
- 不重复 marshal/unmarshal 大 body；
- compiled rule lookup 保持有界；
- 配置刷新不阻塞请求。

性能门槛在有实现和基线测量后确定，不在设计阶段虚构具体数字。

## 19. CI 测试分组

建议后续 CI 拆分：

### `go-executor-unit`

```bash
gofmt -l services/executor
go vet ./...
go test -race -count=1 ./...
go build ./...
```

### `executor-contract`

```bash
pnpm --filter @tokenmp/contracts lint
pnpm --filter @tokenmp/contracts typecheck
pnpm --filter @tokenmp/contracts test
# generated models/strict server 随变更提交，纳入新鲜度检查；
# 该检查已作为最小新鲜度步骤接入现有 go-auth CI job：
pnpm --filter @tokenmp/contracts check:generated:executor
# 路由契约一致性（kin-openapi 加载契约与 generated Handler 双向比较）：
cd services/executor && go test ./internal/server/...
```

`check:generated:executor` 是现有 `go-auth` CI job 中必经的新鲜度门禁；`go test ./internal/server/...`（路由一致性）与 Docker/集成验证仍待后续独立阶段（见阶段 14）。

### `executor-integration`

无需数据库，使用 Mock Repository + `httptest.Server`：

```bash
go test -race -count=1 ./test/integration/...
```

### `executor-fuzz-smoke`

在 Config/Protocol parser 实现稳定后，对指定 fuzz target 执行短时 smoke fuzz。完整长时 fuzz 放定时任务或手工执行，不阻塞 Foundation 阶段。

### `executor-docker`

构建镜像，不运行、不推送；镜像内不得包含 fixtures、测试密钥或源码缓存。

## 20. 每阶段最低测试门槛

| 阶段 | 最低门槛 |
|---|---|
| Foundation | **已实施**：运行时 config、health、graceful shutdown，以及 Mock/InMemory ports 和 quota terminal contract/unit tests |
| Codegen | **已实施**：freshness + route/HTTP conformance（不表示 runtime 路由注册或业务执行） |
| Config model | C01–C27 + compiler fuzz smoke |
| Routing | selector + candidate scope + deterministic ordering |
| Adapter engine | thinking + DSL + response mapping |
| OpenAI SDK | request/response + retry=0 + context cancel |
| Retry | R01–R20 适用项 + property budget invariant |
| Streaming | TTFT/idle/lifetime + partial disconnect + cancel |
| Anthropic | native JSON/error/SSE + thinking signature |
| Responses | lifecycle events + failed/incomplete + reasoning summary |
| Quota | exactly-once terminal property + race tests |
| Logging | single terminal + redaction tests |
| CI/Docker | full local-equivalent suite + image build |

## 21. Definition of Done

Executor 功能只有满足以下条件才算完成：

1. 对应契约、正常路径、错误路径和流路径均有测试。
2. 所有新规则有编译测试、运行测试和未命中兜底测试。
3. 所有重试功能有 Attempt Budget、commit boundary 和 quota 测试。
4. 所有业务超时都有可控、快速且无泄漏的测试。
5. `go test -race ./...` 通过。
6. 对外错误格式和 SDK 客户端兼容测试通过。
7. 没有密钥、prompt、thinking 原文或原始上游正文泄露。
8. 文档、fixtures 和实现所声明的行为一致。
9. 任何未运行的测试、平台限制或已知风险在 PR 中明确说明。
