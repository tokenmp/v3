# Request Log

> 作用域：`services/executor/internal/requestlog/`

## 模块职责

- 定义 `ExecutionPort`：记录安全、无密钥的请求生命周期事件，并按过滤条件查询已记录事件。
- 提供 `InMemoryExecution`：并发安全的 FIFO 环形缓冲实现（默认容量 10000），满时淘汰最旧事件。
- 提供 `ExecutionMock`：可配置的测试替身，支持 `RecordFn`/`QueryFn` 注入与静态错误注入。

## Lifecycle Events

`ExecutionEvent.Kind` 标识请求生命周期阶段：

| Kind | 含义 |
|---|---|
| `attempt` | 单次上游调用尝试（含 latency、usage、status/code/type） |
| `reserved` | 配额预留成功 |
| `finalized` | 配额终态确认（含 `Settlement`） |
| `released` | 配额终态释放（含 `Settlement`） |
| `committed` | 流式提交边界（`Committed` 字段标记是否已 commit） |

事件按实际发生顺序记录；同一请求的 attempt 事件按 attempt number 递增。

## 安全边界

`ExecutionEvent`、`ExecutionCandidate`、`ExecutionUsage`、`ExecutionSettlement` 的全部字段均为安全公开元数据，不含：

- 请求 body、prompt、thinking 原文、图片 base64
- URL、header、credential reference 或 secret
- 上游响应正文

`TestExecutionEventSafeSurface` 通过反射检查字段名不含 `body`/`url`/`header`/`ref`/`secret`，并验证 `fmt` 渲染不泄露敏感标记。

## Ring Buffer

`InMemoryExecution` 使用固定容量环形缓冲：

- 默认容量 `defaultRingCapacity = 10000`
- `NewInMemoryExecutionWithCapacity(cap)` 创建自定义容量（cap 必须 > 0，否则 panic）
- 满时 FIFO 淘汰最旧事件
- `Events()` 与 `QueryEvents()` 返回防御性拷贝，调用方修改不影响内部存储

## QueryEvents

`ExecutionFilter` 支持可选字段组合过滤：

- `RequestID`：按请求 ID 过滤
- `ReservationID`：按预留 ID 过滤
- `Kind`：按事件类型过滤

零值 filter 返回全部事件；多字段 AND 语义。

## ExecutionMock

- `NewExecutionMockWith(opts...)` 支持 `WithExecutionRecordFn`/`WithExecutionRecordErr`/`WithExecutionQueryFn`
- `RecordFn` 在锁外调用；无 `RecordFn` 时事件追加内部存储并返回 `RecordErr`
- `QueryFn` 在锁外调用，结果经防御性拷贝返回
- 无 `QueryFn` 时使用内置过滤逻辑
- 并发安全：配置一次后可并发调用

## Fault Injection

`InMemoryExecution.SetFaultHook` 安装后置记录钩子：

- 钩子在事件已追加后、锁外调用
- 钩子返回错误时事件仍保留（模拟记录后故障）
- 传入 `nil` 清除钩子

## Contract Tests

`ExecutionContractTests(t, newPort)` 是共享契约套件，验证任意 `ExecutionPort` 实现：

- 初始空查询
- 单事件记录与查询
- 记录顺序保持
- 查询返回防御性拷贝
- Kind 过滤
- 并发记录安全

`InMemoryExecution` 与 `ExecutionMock` 均运行此契约套件。

## 测试覆盖

| 维度 | 已覆盖断言 |
|---|---|
| 安全表面 | 反射检查字段名、fmt 渲染无敏感标记 |
| 顺序与防御性拷贝 | 记录顺序、Events/QueryEvents 返回独立副本 |
| Fault injection | 钩子错误不删除已记录事件、清除后正常 |
| 并发 | 并发 Record+Events、并发 Record+QueryEvents |
| 环形缓冲 | FIFO 淘汰、精确容量、非正容量 panic |
| QueryEvents | 无 filter、按 RequestID/ReservationID/Kind、组合 filter、无匹配、防御性拷贝 |
| Mock | 契约套件、RecordFn/RecordErr/QueryFn/QueryFn 错误、QueryFn 结果防御性拷贝、并发 Record+Query |
| 契约 | InMemoryExecution 与 ExecutionMock 共享契约套件 |

## 不提供

- Durable storage、数据库或持久化
- 跨进程 exactly-once 或 idempotency
- 请求 body、上游响应或密钥的记录
- 热重载或动态容量调整
