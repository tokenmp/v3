# streaming

> 作用域：`services/executor/internal/streaming`。本文件继承从仓库根目录到本目录之间的所有 `AGENTS.md`。本文件是 Manifest only（仅清单），详细语义见源码与测试。

## 模块职责

- 负责：transport-neutral streaming execution boundary —— streaming state machine、first-token commit gate、TTFT/stream-idle/stream-lifetime timer control、pre-commit lifecycle buffer、bounded safe Event metadata、monotonic bounded usage、safe terminal `Outcome`。
- 不负责：SSE framing/解析、protocol-aware semantic detection（OpenAI/Anthropic/Responses delta 解码）、downstream rendering、`internal/sdk` semantic stream、`internal/execution` attempt/retry/quota orchestration、`internal/transport`/`internal/composition` runtime 接入、credential/URL/routing detail。当前无模块内 runtime consumer，`stream:true` 仍不可用。任何 raw bytes / protocol field / downstream renderer interface / credential 均不进入本包。
- 所有者：`services/executor/internal/streaming/{types.go,state.go,clock.go,bridge.go,*_test.go}`。

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `Source` interface | `Next(ctx)`/`Close()` | `Next` honor ctx、返回 `ErrEndOfStream` 或 safe classified error；`Close` 幂等、可与 in-flight `Next` 并发、安全且 non-blocking/bounded，并尽可能 unblocks `Next` | experimental | `types.go` |
| `Sink` interface | `Commit(ctx,[]Event)`/`WriteEvent`/`Flush` | Commit 成功 = whole batch written+flushed 的**逻辑 Sink contract**；失败 = downstream uncertain，不重试；不声明 HTTP atomicity 或 wire proof | experimental | `types.go` |
| `Bridge.Run(ctx)` | 非 nil `Source`/`Sink`/valid `Timeouts`、`MaxTotal∈(0,MaxTotalHardCap]`、`MaxEvents∈(0,MaxEventsHardCap]` | `(Outcome, error)`；pre-commit failure 返 non-nil sentinel；post-commit failure 返 nil error + failed Outcome；success 返 nil + `StateCompleted` | experimental | `bridge.go` |
| `Outcome` | — | `State`/`Reason`/`Committed`/`Usage`/`Finish`/`UnresolvedCost`/`TTFT`；pre-commit failure 时 `Usage` 恒为零 | experimental | `bridge.go` |

## 模块边界

- 允许访问：仅 stdlib（`context`/`errors`/`reflect`/`sync`/`sync/atomic`/`time`）。
- 禁止访问：`internal/adapter`/`internal/sdk`/`internal/execution`/`internal/transport`/`internal/composition`/`internal/contract`/`runtime`。
- 数据所有权：只持有 sanitized `Event` metadata 与 bounded `Usage`；不持有 raw bytes、protocol field、credential。
- 配置和环境变量：无；`Timeouts`/`MaxTotal`/`MaxEvents` 由 caller 注入。
- 健康检查：无。

## 开发与验证

```bash
cd services/executor
go test -race -count=1 ./internal/streaming/...
go test -race -count=50 -run 'TimerPrecedence|CancelPrecedence|CloseConcurrent|TTFTFiredPostCommit' ./internal/streaming/
go test -run=^$ -fuzz=FuzzBridgeRun -fuzztime=10s ./internal/streaming/
gofmt -l internal/streaming/ && go vet ./internal/streaming/
```

## DO NOT

- **DO NOT** 让本包解析 SSE bytes 或做 protocol-aware semantic detection —— 原因：违反 protocol-neutral 边界并让 protocol detail 泄漏到 core；正确做法：由 caller（future protocol-specific Source adapter）将上游事件分类为 `EventKind` 后供给 bounded safe token。
- **DO NOT** 在 pre-commit failure 的 `Outcome.Usage` 中返回累计 usage —— 原因：nothing committed downstream，按计费契约 reservation release、不可计费，旧实现会误计费未 commit 的失败流；正确做法：pre-commit failure 恒返回零 `Usage`。
- **DO NOT** 在 `Source.Close` 后省略等待 `pumpDone` —— 原因：会泄漏上游 read goroutine；正确做法：`cancelPump()` 后调用 concurrent-safe/non-blocking `Close()` 以尽可能 unblock `Next`，再等待 `pumpDone`。
- **DO NOT** 在 post-commit 事件就绪时跳过已就绪的 lifetime/idle/cancel recheck —— 原因：Go `select` 随机选择就绪 channel，post-commit semantic/progress/finish 会被先选中并 WriteEvent/Flush/重置 idle/完成，绕过 lifetime hard cap 与 idle max-gap；正确做法：每次从 `events` 取出事件后先做 precedence recheck（ctx/lifetime/ttft/idle）。
- **DO NOT** 在 commit 后让 TTFT timer 仍能终止流 —— 原因：commit 时 TTFT budget 已消费，fired-but-not-selected 的 TTFT 残留值在 `Stop()` 后仍可选，会误报 post-commit TTFT timeout；正确做法：commit 时 `ttft.Stop()` 并 `ttftChan=nil` 使残留值不可达。

## 已知陷阱与历史教训

### TTFT fired-but-not-selected post-commit 误终止

- 症状：commit 成功后流被 `ReasonTTFTTimeout` 误终止。
- 根因：`ttft.Done()` 的值在 `Stop()` 后仍在 channel 中，post-commit select 仍可选。
- DO：commit 时 `ttft.Stop()` + `ttftChan=nil`（nil channel 永不被 select）。
- DO NOT：仅 `Stop()` 不置 nil。
- 验证：`TestBridgeTTFTFiredPostCommitNeverFails`（`stopFiresTimer` 模拟 `Stop()` 推送 stale firing）。
- 证据：`bridge.go` commit 块；prior review 阻塞项 #2。

### Close|Next 并发

- 症状：退出时上游 read goroutine 泄漏，或 adapter 无法从阻塞 read 中恢复。
- 根因：未定义并发 Close 契约，或 Close 后未等待 pump 返回。
- DO：Source 的 `Close` 必须 concurrent-safe/non-blocking 并尽可能 unblock `Next`；Bridge 执行 `cancelPump()` → `Close()` → `<-pumpDone`。
- DO NOT：在 Close 后直接返回或假定取消一定能中断底层 read。
- 验证：`TestBridgeCloseConcurrentWithNext`（`closeProbeSource` 在 in-flight `Next` 时由 Close unblocks）。
- 证据：`bridge.go` exit ordering；prior review 阻塞项 #3。

### Pre-commit usage 误计费

- 症状：pre-commit failure 的 `Outcome.Usage` 带累计 usage，与 doc「pre-commit failure 为零」矛盾。
- 根因：旧 `preFail` 原样返回 `usage`。
- DO：`preFail` 返回零 `Usage`，`UnresolvedCost=false`。
- DO NOT：`preFail` 返回累计 usage。
- 验证：`TestBridgePreCommitFailureReportsZeroUsage`。
- 证据：`bridge.go` `preFail`；`types.go` `EventUsage`/`Outcome.Usage` doc；prior review 阻塞项 #1。

### Post-commit timer/事件竞争无优先级

- 症状：lifetime/idle 已就绪但 post-commit 事件被先处理，绕过 hard cap。
- 根因：Go `select` 随机选就绪 channel，post-commit 无 precedence recheck。
- DO：以 `Clock.Now()` 和 `started`/lifetime deadline/`lastProgress`+idle deadline 做 authoritative recheck；timer 仅作 wakeup。
- DO NOT：仅依赖 select 顺序或消费 timer channel 来决定超时。
- 验证：`TestBridgePostCommitTimerPrecedenceOverReadyEvent`（fake clock 将 lifetime 推至 deadline，`firingSink` 同时 wake timer）。
- 证据：`bridge.go` recheck 块；prior review 阻塞项 #2。

## 文档维护触发器

- 公开导出（`Source`/`Sink`/`Bridge`/`Outcome`/`Event`/`State`/`Reason`/`Timeouts`）、返回结构、错误语义或副作用变化。
- 依赖或消费者功能变化（本包目前无 module 内消费者；attempt/retry/quota、SDK semantic stream、transport 与 composition 接入均为 future PR）。
- 验证命令（race/fuzz）变化。
