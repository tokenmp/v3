# LogSink

> 作用域：`services/executor/internal/logsink/`。继承 `services/executor/AGENTS.md`。

## 模块职责

- 负责：将 executor 执行生命周期事件异步推送到 Logging Service `/v1/logs/ingest`。
- `RemoteSink` 包装内存 `ExecutionPort`（保留本地查询能力），在每次 `RecordExecution` 时同步 post 单事件 batch 到 Logging Service（用 `context.Background()`，吞错误不阻塞 executor 主路径）。
- 不负责：缓冲/批量聚合（当前每事件一次 post）、Logging Service 分区管理、Edge 日志推送。

## 对外能力

| 导出 | 输入 | 返回/错误 | 稳定性 |
|---|---|---|---|
| `NewRemoteSink(Options)` | Endpoint（http(s) base URL，无 path/query/fragment/userinfo）、Local（非 nil ExecutionPort）、HTTPClient（可选）、PostTimeout（可选，默认 10s） | `(*RemoteSink, error)`；URL 校验失败返回 `ErrSinkBlankURL`/`ErrSinkInvalidURL` | internal |
| `RemoteSink.RecordExecution` | ctx、ExecutionEvent | 先 inner.RecordExecution，再 post；post 错误吞掉（slog.Warn），**永不返回 post 错误** | internal |
| `RemoteSink.QueryEvents` | ctx、ExecutionFilter | 委托 inner 本地查询 | internal |
| `RemoteSink.post` | batch | `ErrSinkUnavailable`（HTTP 失败/非 2xx/redirect）、`ErrSinkOversized`（>2 MiB）；**不泄漏 URL/host/port/body** | internal（测试可见） |

## 安全边界

- HTTP client：`CheckRedirect: no redirect`，默认 10s timeout
- post 用 `context.Background()`（不继承请求 ctx，请求结束后仍送达）
- sentinel 错误不泄漏 endpoint URL、host、port、request body、response body
- `MaxIngestBodyBytes = 2<<20`（2 MiB），与 Logging Service 一致
- wire 类型（`requestLog`/`attempt`/`timelineEvent`/`batch`）json tag 与 Logging Service `repository.RequestLog/Attempt/Event` 精确对齐（设计/构建时对齐，无 Go runtime import）

## 事件映射

| ExecutionEvent 字段 | → batch 字段 |
|---|---|
| RequestID | Log.RequestID, Attempt.RequestID, Event.RequestID |
| Subject | Log.UserID |
| KeyID | Log.ClientKeyID |
| Candidate.ModelID | Log.ResolvedModel, Attempt.UpstreamModel |
| Candidate.{RouteID,ProviderID,CredentialID} | Log.{RouteID,ProviderID,CredentialID}, Attempt 同 |
| Protocol | Log.Protocol |
| ReservationID | Log.ReservationID |
| Usage (UsageKnown=true) | Log.{Input,Output,Total}Tokens + UsageStatus="final" |
| Latency | Log.LatencyMS, Attempt.LatencyMS, Event.DurationMS |
| Code | Log.ErrorCode, Attempt.ErrorCode, Attempt.HTTPStatus（3 位数时） |
| Type | Log.ErrorType, Attempt.ErrorType |
| Kind=attempt | 产生 1 行 Attempt |
| Kind=finalized/released | Log.CompletedAt = Timestamp |
| 所有 Kind | 产生 1 行 Event（Source="executor", Stage=Kind） |

## 验证

```bash
cd services/executor
go test ./internal/logsink/...
go test -race ./internal/logsink/...
```

- URL 校验（blank/query/fragment/userinfo/non-http/path/nil-local/negative-timeout）
- post + 本地查询保留
- 非 attempt kind 无 attempt 行
- 远端失败吞掉（return nil），本地记录保留
- 不跟随 redirect
- 超大 batch 拒绝（post 前）
- 不可达 host 无泄漏
- background context（取消请求 ctx 仍送达）
- QueryEvents 过滤委托
- 并发 record + query（race）
- wire shape 与 repository json tag 精确对齐

## 约束

- **DO NOT** 让 post 错误传播到 executor 主路径——吞掉。
- **DO NOT** 在错误中泄漏 endpoint URL/host/port/body。
- **DO NOT** 跟随 redirect。
- **DO NOT** 用请求 ctx 做 post——用 `context.Background()`。

## 文档维护

事件映射、wire 类型、安全策略变化时，同步维护本文件与 `services/executor/AGENTS.md`。
