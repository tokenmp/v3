# Logging Service

> 作用域：`services/logging/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 职责

Logging Service 是 TokenMP V3 分层架构的**业务平面**日志服务：

- 接收 executor / Edge 推送的请求生命周期事件，落库到 Log DB（`infra/db/migrations/log/`）。
- **不存客户端 request body 或成功 response body**；仅存摘要/计数/错误分类，以及 executor 提供的有界、脱敏、admin-only 上游错误响应片段（metadata）。
- Log DB 按天 RANGE 分区（PostgreSQL 原生，非旧版 2 小时分表 2000 张），自动清理旧分区。
- executor 不直连此库；Logging Service 是唯一读写方，异步落库不阻塞 executor 主路径。

## 当前实施状态（骨架）

- `cmd/logging/main.go`：入口，加载 `LOGGING_*` env、连 DB、建 server、graceful shutdown（SIGINT/SIGTERM）。
- `internal/config`：env 配置加载与严格校验（`LOGGING_DATABASE_URL` 限定 `postgres/postgresql` + 路径 `/tokenmp_logging`，支持 host 形式与 Unix socket 形式；连接串从不入日志/错误）。
- `internal/database`：GORM 连接，AutoMigrate 禁止，schema 由 `migrations/` 版本化 SQL 管理（golang-migrate）。`Open` 返回稳定 classified sentinel 错误，driver 错误（可能含 DSN）绝不经 `Error()`/`Unwrap()` 暴露。
- `internal/repository`：
  - 结构体 `RequestLog`/`Attempt`/`Event` 对齐 `request_logs`/`request_attempts`/`request_log_events` 表字段，**无客户端明文 body 字段**。`RequestLog.UserAgent` 仅保存 Edge 提供的 512-byte、UTF-8/control-char 双重清洗值，不采集其他请求头。
  - 端口 `Writer`（InsertRequestLog/InsertAttempt/InsertEvent）+ `Reader`（GetRequestLog/ListAttempts/ListEvents + ListRequestLogs/GetStats）+ `BatchIngestor`（IngestBatch 单事务批量插入，任一失败回滚）。
  - `GormRepository` 实现。`IngestBatch` 对同一 `request_id` 使用 transaction advisory lock，UPDATE 已有摘要、无记录时 INSERT；因此 Edge `received`、executor `quota_reserved/upstream_started(首 token + TTFT)/upstream_finished/terminal/completed` 可逐阶段 upsert 同一行。`final_status=processing` 表示未终态，最终事件更新为 success/error/client_cancelled 类别；迟到的异步 processing 事件不得覆盖已有终态，但仍可补 TTFT/stream；不同终态之间默认 first-writer-wins，但 `client_cancelled` 可覆盖非 success 终态以便列表可见，已成功完成的 executor 终态保持权威；`stream` 以 OR 语义只从 false 升为 true。`created_at` 零值时默认 `now()` 以路由到正确日分区；CHECK 约束的可空列（usage_status/retry_classified）用 `NULLIF` 映射。跨分区查询 by `request_id`。
  - sentinel：`ErrNotFound`/`ErrQueryFailed`/`ErrInsertFailed`，不泄漏 DSN/SQL。
- `internal/server`：HTTP（chi）。
  - `GET /healthz`（liveness）、`GET /readyz`（DB ping）。
  - `POST /v1/logs/ingest`：接收 `{log, attempts[], events[]}`，2 MiB body 限，单事务批量插入，返回 `{request_id, accepted}`。
  - `GET /v1/logs/{request_id}`：返回 `{log, attempts, events}`，不存在 404。
  - `GET /v1/logs`：分页列表（query：user_id/page/page_size/model/status/start_date/end_date），返回 `{logs, total, page, page_size}`；摘要包含协议/stream、Token/cache、latency/TTFT、thinking、provider/route 及有界 User-Agent；status 是逗号分隔的 final_status 枚举，未知值静默丢弃。
  - `GET /v1/logs/stats`：用量统计（query：user_id/days，默认 7，上限 90），返回 `{days, total_requests, total_input_tokens, total_output_tokens, by_model[]}`，by_model 按 requests 降序。
  - 协议原生 JSON 错误，不泄漏 DSN/SQL/凭据；所有响应 `Cache-Control: no-store`。
- `migrations/000001_init.{up,down}.sql`：Log DB schema（从 `infra/db/migrations/log/0001_init.sql` 转换为 golang-migrate up/down 格式，含 3 张分区表的日分区 + default 分区）。
- `migrations/000002_add_processing_status.{up,down}.sql`：为 `request_logs.final_status` 增加非终态 `processing`。
- `migrations/000003_add_user_agent.{up,down}.sql`：新增可空 `request_logs.user_agent`；仅存有界清洗后的 User-Agent。
- `migrations/000004_add_client_cancelled_status.{up,down}.sql`：新增终态 `client_cancelled`，用于 Edge 在客户端断开时关闭 processing 行。

## 验证

```bash
# 单元测试（无需 DB）
go test ./internal/config/... ./internal/database/... ./internal/server/...

# repository 集成测试（需临时 pg，应用 up/down migration）
LOGGING_REPO_TEST_DSN="postgres:///tokenmp_logging?host=/tmp&port=55434" go test -race ./internal/repository/...

# 全量
go test ./...

# 进程联调
LOGGING_DATABASE_URL=... LOGGING_HTTP_ADDR=127.0.0.1:18084 go run ./cmd/logging
```

- gofmt / vet / build 通过。
- repository 集成测试：插入+查回、批量、跨分区、not-found、no-plaintext grep guard。
- process smoke test：healthz/readyz 200、ingest 200（accepted:3）、get log 200（log+attempts+events）、404、400 全正确。

## 待实现（后续）

- 分区自动创建/清理（pg_partman 或 cron，记 `log_archive_runs`）。
- 批量/异步落库优化（当前 Edge received 为异步 HTTP；executor logsink 每事件同步 post 单事件 batch，Logging ingest 为同步事务）。
- 查询过滤（按 user/time/model/status 等）。

## 约束

- **DO NOT** 用 `AutoMigrate`——schema 由 `migrations/` 版本化 SQL 管。
- **DO NOT** 存客户端 request body 或成功 response body；上游错误 body 仅允许 executor 生成的有界、脱敏、admin-only metadata 片段。
- **DO NOT** 让 driver 错误经 `Error()`/`Unwrap()` 暴露 DSN。
- **DO NOT** 提交密钥/连接串/生产数据。
- DB 路径硬限 `/tokenmp_logging`，绝不连其他库。
- ingest 单事务：log + attempts + events 原子落库，任一失败回滚。

## 文档维护

读写路径、ingest 协议、分区策略变化时，同步维护本文件、`services/AGENTS.md` 与 `infra/db/AGENTS.md`。
