# Infra DB

> 作用域：`infra/db/`。继承仓库根 `AGENTS.md` 与 `infra/AGENTS.md`，遵循 `.agents/docker.md`、`.agents/operations.md`、`.agents/monorepo.md`。

## 分区职责

`infra/db/` 是 TokenMP V3 三库（Config / Log / Billing）schema 迁移的中立基建层。仅保存可提交的 DDL 迁移、schema 定义与回滚说明，不绑定具体 service 实现、不含业务数据、不含敏感凭据。

三库物理分离，职责单一，可独立扩展/备份/迁移：

| 库 | 目录 | 职责 |
|----|------|------|
| Config DB | `migrations/config/` | provider/model/route/credential/adapter 配置，带版本（draft/published/archived），编译成 `ConfigSnapshot` 下发 executor；`0003_limits_and_routing_policy.sql` 增加 provider/route/credential/route-credential nullable context/max output/RPM/TPM 配置列；`0004_publish_hardening.sql` 加固写路径：draft 乐观并发 `version`、global singleton published（partial unique index）、revert 元数据（`source_revision_id`/`rollback_note`）、audit 扩展与收紧 action 枚举、secret 边界（`credential_ref` 恢复 NOT NULL + unique，`api_key` 列仅历史遗留，迁移不外迁/输出 secret，历史冲突 fail-closed，api_key COMMENT 用 DO block 守卫列存在），并修正 0001 遗留缺陷 `parent_revision_id`（bigserial→可选 bigint）。服务侧 `000004_publish_hardening.down.sql` 为 **fail-closed contract 回退**：在所有破坏性 DDL 之前的前置 preflight guard（`parent_revision_id IS NULL`、`version<>1`、非空 rollback provenance、`action='rollback_publish'`、非空 `actor_kind`/`request_id`）会在旧 schema 无法表达的数据存在时中止并（经 golang-migrate 文件级事务）整体回滚，绝不静默回填或丢历史；干净库上 down 成功回退 schema |
| Log DB | `migrations/log/` | 请求生命周期事件，不存明文，按天分区 + 自动清理 |
| Billing DB | `migrations/billing/` | 套餐/配额/记账，executor 不直连，由 Edge/BFF + Billing Service 操作；`0004_settlement_state_machine.sql` 增量加入持久化结算状态机（`pending_reconciliation` 状态、`usage_known`/`settlement_status`/`reconciled_at`/`idempotency_payload_hash`、amount 非负 CHECK、请求级 active-hold 部分唯一索引、sweeper/reconciler 查询索引、`usage_ledger` 新增 `reconcile`/`sweep` ledger_type）。账本永不删除。镜像 `services/billing/migrations/000004_*.down.sql` 为 **fail-closed**：存在任一 000004-only 数据（`pending_reconciliation` 行、`usage_ledger` ledger_type=`reconcile`/`sweep` 行、或 reservation 的 `settlement_status`/`usage_known=true`/`reconciled_at`/`idempotency_payload_hash` 非默认值）时 `RAISE EXCEPTION`（仅计数、无 secret），需先经 reconciler/runbook 清理/转换后再回退（见 services/billing/AGENTS.md Downgrade runbook）。preflight 在任何 DDL 前执行；golang-migrate 逐文件包裹事务，失败回滚干净，raw pgx/psql Exec 场景因 preflight 先于任何 DROP 亦保证 schema 完整。 |

设计依据：`docs/v3-db-schema-draft.md`（本地草案）+ `docs/v3-layered-architecture.md` + 旧版数据库调研（`docs/legacy-db-recon.md`，本地）+ V3 executor `ConfigSnapshot` 结构（`services/executor/internal/snapshot/types.go`、`internal/adapter/config.go`）。

## 迁移文件组织

- 按库分目录：`migrations/<db>/`。
- 命名约定：`NNNN_<short_desc>.sql`，`NNNN` 四位递增序号（`0001_init.sql` 为初始化）。
- 每个 `.sql` 文件用 `BEGIN;` ... `COMMIT;` 包裹事务，`-v ON_ERROR_STOP=1` 执行时遇错即停。服务侧权威迁移由 golang-migrate 管理（`services/<svc>/migrations/NNNNNN_*.up.sql` / `*.down.sql`），**golang-migrate 默认逐文件包裹单一事务**：每个迁移文件作为一个事务提交，失败则整个文件回滚，保证 schema 完整。fail-closed down（如 Billing 000004）的 preflight `DO $$ ... RAISE $$` 必须在任何 DDL 前执行，这样无论执行器是否事务包裹（golang-migrate 单事务、或 raw psql/pgx Exec 非事务逐语句），失败都不会留下半回退 schema。`infra/db/migrations/<db>/NNNN_*.sql` 是镜像 DDL（仅 up，不绑定 golang-migrate 的 up/down 对偶），服务侧的 `*.up/down.sql` 才是权威。
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
