# Logging Service

> 作用域：`services/logging/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 职责

Logging Service 是 TokenMP V3 分层架构的**业务平面**日志服务：

- 接收 executor / Edge 推送的请求生命周期事件，落库到 Log DB（`infra/db/migrations/log/`）。
- **不存明文 body**（只存摘要/计数/错误分类）——修正旧版存明文的隐私痛点。
- Log DB 按天 RANGE 分区（PostgreSQL 原生，非旧版 2 小时分表 2000 张），自动清理旧分区。
- executor 不直连此库；Logging Service 是唯一读写方，异步落库不阻塞 executor 主路径。

## 当前实施状态（骨架）

- `cmd/logging/main.go`：入口，加载 `LOGGING_*` env、连 DB、建 server、graceful shutdown（SIGINT/SIGTERM）。
- `internal/config`：env 配置加载与严格校验（`LOGGING_DATABASE_URL` 限定 `postgres/postgresql` + 路径 `/tokenmp_logging`，支持 host 形式与 Unix socket 形式；连接串从不入日志/错误）。
- `internal/database`：GORM 连接，AutoMigrate 禁止，schema 由 `migrations/` 版本化 SQL 管理（golang-migrate）。`Open` 返回稳定 classified sentinel 错误，driver 错误（可能含 DSN）绝不经 `Error()`/`Unwrap()` 暴露。
- `internal/repository`：
  - 结构体 `RequestLog`/`Attempt`/`Event` 对齐 `request_logs`/`request_attempts`/`request_log_events` 表字段，**无明文 body 字段**。
  - 端口 `Writer`（InsertRequestLog/InsertAttempt/InsertEvent）+ `Reader`（GetRequestLog/ListAttempts/ListEvents）+ `BatchIngestor`（IngestBatch 单事务批量插入，任一失败回滚）。
  - `GormRepository` 实现。写入用 Raw SQL `INSERT ... RETURNING id`；`created_at` 零值时默认 `now()` 以路由到正确日分区；CHECK 约束的可空列（usage_status/retry_classified）用 `NULLIF` 映射。跨分区查询 by `request_id`。
  - sentinel：`ErrNotFound`/`ErrQueryFailed`/`ErrInsertFailed`，不泄漏 DSN/SQL。
- `internal/server`：HTTP（chi）。
  - `GET /healthz`（liveness）、`GET /readyz`（DB ping）。
  - `POST /v1/logs/ingest`：接收 `{log, attempts[], events[]}`，2 MiB body 限，单事务批量插入，返回 `{request_id, accepted}`。
  - `GET /v1/logs/{request_id}`：返回 `{log, attempts, events}`，不存在 404。
  - 协议原生 JSON 错误，不泄漏 DSN/SQL/凭据；所有响应 `Cache-Control: no-store`。
- `migrations/000001_init.{up,down}.sql`：Log DB schema（从 `infra/db/migrations/log/0001_init.sql` 转换为 golang-migrate up/down 格式，含 3 张分区表的日分区 + default 分区）。

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
- executor 端日志 sink → 推送到 Logging Service 的 `/v1/logs/ingest`（HTTP/队列，异步）。
- 批量/异步落库优化（当前 ingest 同步事务）。
- 查询过滤（按 user/time/model/status 等）。

## 约束

- **DO NOT** 用 `AutoMigrate`——schema 由 `migrations/` 版本化 SQL 管。
- **DO NOT** 存明文 request_body/response_body——只摘要/计数/错误分类。
- **DO NOT** 让 driver 错误经 `Error()`/`Unwrap()` 暴露 DSN。
- **DO NOT** 提交密钥/连接串/生产数据。
- DB 路径硬限 `/tokenmp_logging`，绝不连其他库。
- ingest 单事务：log + attempts + events 原子落库，任一失败回滚。

## 文档维护

读写路径、ingest 协议、分区策略变化时，同步维护本文件、`services/AGENTS.md` 与 `infra/db/AGENTS.md`。
