# Config Service

> 作用域：`services/config/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 职责

Config Service 是 TokenMP V3 分层架构的**控制平面**配置服务：

- 管理 Config DB（`infra/db/migrations/config/`）中的 provider/model/route/credential/adapter 配置。
- 发布版本化的配置快照（`config_revisions` draft/published/archived + `config_revision_snapshots.snapshot_json`）。
- 通过 HTTP `GET /v1/config/snapshots/latest` 把最新 published 的 raw `ConfigSnapshot` JSON 下发给 executor。

**编译边界**：Config Service **不编译**。它只下发 raw `ConfigSnapshot` JSON。编译成运行时 compiled snapshot 在 executor 端做（executor 的 `configsource.LoadFromConfigService` 拉取后本地 `snapshot.Compile`）。这样 Config Service 不依赖 executor 的 internal 包，executor 复用现有编译逻辑、热重载、runtime facade。

executor 不直连 Config DB；Config Service 是唯一读写方。

## 当前实施状态（骨架）

- `cmd/config/main.go`：入口，加载 `CONFIG_*` env、连 DB、建 server、graceful shutdown（SIGINT/SIGTERM）。
- `internal/config`：env 配置加载与严格校验（`CONFIG_DATABASE_URL` 限定 `postgres/postgresql` + 路径 `/tokenmp_config`，支持 host 形式与 Unix socket 形式；连接串从不入日志/错误）。
- `internal/database`：GORM 连接，AutoMigrate 禁止，schema 由 `migrations/` 版本化 SQL 管理（golang-migrate）。`Open` 返回稳定 classified sentinel 错误，driver 错误（可能含 DSN）绝不经 `Error()`/`Unwrap()` 暴露。
- `internal/repository`：读 published revision + snapshot。`LatestPublished` 选 `status='published'` 中 `published_at` 最新的一条，返回 raw JSON + revision + sha256。draft/archived 不暴露。失败映射为 `ErrNotFound`/`ErrQueryFailed`。
- `internal/server`：HTTP（chi）。`GET /healthz`（liveness）、`GET /readyz`（DB ping）、`GET /v1/config/snapshots/latest`（snapshot JSON，含 `X-Config-Revision`/`X-Config-SHA256` 头，`Cache-Control: no-store`）。错误为协议原生 JSON，不泄漏 DSN/SQL/凭据。
- `migrations/000001_init.{up,down}.sql`：Config DB schema（从 `infra/db/migrations/config/0001_init.sql` 转换为 golang-migrate up/down 格式）。

## 待实现（后续）

- 草稿/发布写路径（admin API：draft → publish → archive，写 `config_revisions`/`config_revision_snapshots`）。
- `config_audit_log` 写入。
- executor 端 `configsource.LoadFromConfigService`（HTTP pull snapshot_json → 本地 `snapshot.Compile` → 发布，对接现有热重载）。
- 配置热重载：Config Service push + executor SIGHUP（或 executor 轮询 latest revision）。

## 验证

```bash
# 单元测试（无需 DB）
go test ./internal/config/... ./internal/database/... ./internal/server/...

# repository 集成测试（需临时 pg，应用 up/down migration）
CONFIG_REPO_TEST_DSN="postgres:///tokenmp_config?host=/tmp&port=55433" go test ./internal/repository/...

# 全量
go test ./...

# 进程联调（启动临时 pg + apply migration + 跑 binary）
CONFIG_DATABASE_URL=... CONFIG_HTTP_ADDR=127.0.0.1:18082 go run ./cmd/config
```

- build + vet 通过。
- repository 集成测试验证：空库→`ErrNotFound`；多 published 选最新；draft 被忽略。
- process smoke test：healthz 200、readyz 200、snapshot 端点返回 revision/sha256/snapshot JSON。

## 约束

- **DO NOT** 用 `AutoMigrate`——schema 由 `migrations/` 版本化 SQL 管。
- **DO NOT** 在 Config Service 编译 snapshot——编译在 executor 端。
- **DO NOT** 让 driver 错误经 `Error()`/`Unwrap()` 暴露 DSN。
- **DO NOT** 提交密钥/连接串/生产数据——`upstream_credentials` 只存 `vault://` ref。
- DB 路径硬限 `/tokenmp_config`，绝不连其他库。

## 文档维护

读写路径、下发协议、编译边界变化时，同步维护本文件、`services/AGENTS.md` 与 `infra/db/AGENTS.md`。
