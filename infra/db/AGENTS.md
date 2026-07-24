# Infra DB

> 作用域：`infra/db/`。继承仓库根 `AGENTS.md` 与 `infra/AGENTS.md`，遵循 `.agents/docker.md`、`.agents/operations.md`、`.agents/monorepo.md`。

## 分区职责

`infra/db/` 是 TokenMP V3 三库（Config / Log / Billing）schema 迁移的中立基建层。仅保存可提交的 DDL 迁移、schema 定义与回滚说明，不绑定具体 service 实现、不含业务数据、不含敏感凭据。

三库物理分离，职责单一，可独立扩展/备份/迁移：

| 库 | 目录 | 职责 |
|----|------|------|
| Config DB | `migrations/config/` | provider/model/route/credential/adapter 配置，带版本（draft/published/archived），编译成 `ConfigSnapshot` 下发 executor |
| Log DB | `migrations/log/` | 请求生命周期事件，不存明文，按天分区 + 自动清理 |
| Billing DB | `migrations/billing/` | 套餐/配额/记账，executor 不直连，由 Edge/BFF + Billing Service 操作 |

设计依据：`docs/v3-db-schema-draft.md`（本地草案）+ `docs/v3-layered-architecture.md` + 旧版数据库调研（`docs/legacy-db-recon.md`，本地）+ V3 executor `ConfigSnapshot` 结构（`services/executor/internal/snapshot/types.go`、`internal/adapter/config.go`）。

## 迁移文件组织

- 按库分目录：`migrations/<db>/`。
- 命名约定：`NNNN_<short_desc>.sql`，`NNNN` 四位递增序号（`0001_init.sql` 为初始化）。
- 每个 `.sql` 文件用 `BEGIN;` ... `COMMIT;` 包裹事务，`-v ON_ERROR_STOP=1` 执行时遇错即停。
- 初始化迁移 `0001_init.sql` 一次建全部表；后续 alter 用 `0002_*.sql` 递增，不修改已发布文件。
- 搁置表（如 `model_fallbacks`、`route_fallbacks`）在 `0001_init.sql` 内以注释占位，待系统路由层需要再启用。

## 验证

每个迁移必须可复现地通过 PostgreSQL 17/18 语法与约束校验：

```bash
# 启动临时实例（不依赖线上）
initdb -D /tmp/pg-test -A trust --no-locale
pg_ctl -D /tmp/pg-test -o "-p 55432 -k /tmp" start
psql -p 55432 -h /tmp -d postgres -v ON_ERROR_STOP=1 -f infra/db/migrations/config/0001_init.sql
```

关键校验项：
- 全部 `CREATE TABLE`/`CREATE INDEX`/`COMMENT` 无报错。
- `FK VALID`（默认 VALID，不用 NOT VALID）。
- `CHECK` 约束枚举值生效（status 等）。
- 部分唯一索引行为正确（如 `route_mappings` 的 `routes_model_default_uidx`：同一 model 下 active default route 最多一条）。
- `touch_updated_at` 触发器对带 `updated_at` 的表生效。

## 约束

- **DO NOT** 提交 IP、密码、令牌、连接串或生产数据——凭据只存 `vault://` ref，明文在 Secret Store。
- **DO NOT** 用 `NOT VALID` FK——统一 VALID，引用完整。
- **DO NOT** 物理删除——软删除统一用 `status`（active/disabled/deleted），部分唯一索引用 `WHERE status <> 'deleted'`。
- **DO NOT** 在迁移里存明文 body 或请求/响应内容（Log DB 尤其）。
- 字段名与 V3 `ConfigSnapshot` 编译目标对齐（model_id/provider_id/adapter_id/protocol/sdk_kind/effort_mapping 等），让 Config Service 编译时映射简单。
- 不在此目录实现 service 代码或 DB 连接逻辑——那是 `services/` 的职责。

## 与分层架构关系

- **Config Service** 从 `config_revisions(status=published)` → `config_revision_snapshots.snapshot_json` 读取，调 V3 现有 `snapshot.Compile` 编译，下发给 executor（HTTP pull / push + SIGHUP）。executor 代码不变，只把 `configsource.LoadFile` 换成 `LoadFromConfigService`，编译/热重载/runtime facade 全部复用。
- **executor 不直连任何数据库**。Config 由 Config Service 编译后下发；日志通过 HTTP/队列推给 Logging Service；计费由 Edge/BFF 调 Billing Service。
- Log DB 的日志由 Logging Service 异步落库，executor 主路径不阻塞。
- Billing DB 的 quota 预留/校验放在 Edge/BFF → Billing Service，executor 只做本地 JWT/APIKey 验证。

## 边界

- 公开仓库只保存 schema 定义与迁移模板；私有服务器、SSH、部署路径、实时拓扑从可选 `.agents/local.md` 获取。
- 三库不混存；`price_multiplier_rules` 放 Config DB 因属定价配置，实际计费在 Billing DB 结算。
- 用户主数据建议独立 Auth/Identity 库（旧版 `api_keys`/`user_api_keys`/`bot_keys` 三表重叠的教训），Billing DB 只引用 `user_id`。

## 文档维护

schema、迁移、命名、回滚方式变化时，同步维护本文件、相关迁移文件头注释与 `docs/v3-db-schema-draft.md`（本地草案）。
