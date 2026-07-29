# Billing Service

> 作用域：`services/billing/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 职责

Billing Service 是 TokenMP V3 分层架构的**业务平面**计费服务：

- 管理套餐（plans）、用户套餐绑定（user_plans）、配额预留/结算/释放（quota_reservations）、用量账本（usage_ledger）。
- **先预留后结算**（借鉴旧版 quota_reservations 模式）：请求开始时 Edge/BFF 调 Reserve 预留，结束时 Finalize 结算，失败/取消 Release 释放。
- executor **不直连**此库；由 Edge/BFF + Billing Service 操作。executor 只做本地 JWT/APIKey 验证。
- 用户主数据建议独立 Auth/Identity 库（旧版 api_keys/user_api_keys/bot_keys 三表重叠的教训），Billing 只引用 user_id。

## 当前实施状态（骨架）

- `cmd/billing/main.go`：入口，加载 `BILLING_*` env、连 DB、建 server、graceful shutdown（SIGINT/SIGTERM）。
- `internal/config`：env 配置加载与严格校验（`BILLING_DATABASE_URL` 限定 `postgres/postgresql` + 路径 `/tokenmp_billing`，支持 host 形式与 Unix socket 形式；连接串从不入日志/错误）。
- `internal/database`：GORM 连接，AutoMigrate 禁止，schema 由 `migrations/` 版本化 SQL 管理（golang-migrate）。classified sentinel 不泄漏 DSN。
- `internal/repository`：
  - 结构体 `Plan`/`User`/`UserPlan`/`QuotaReservation`/`UsageLedgerEntry` 对齐表字段。
  - 端口 `PlanReader`（GetPlan/ListPlans）、`UserPlanReader`（GetActiveUserPlan）、`QuotaManager`（Reserve/Finalize/Release，单事务 + ON CONFLICT DO NOTHING 幂等）、`LedgerReader`（ListLedger）、`BalanceReader`（GetBalance）、`UsageWindowsReader`（GetUsageWindows）。
  - `GormRepository` 实现。reserve/charge/refund 用 ledger delta 有符号方向（reserve/charge 负、refund 正）。idempotency_key UNIQUE 保证账本幂等。
  - sentinel：`ErrNotFound`/`ErrQueryFailed`/`ErrInsertFailed`/`ErrConflict`/`ErrQuotaExceeded`，不泄漏 DSN/SQL；`*QuotaExceededError` 携带 scope（hour5/weekly/period）与计费数，`Error()` 为安全常量，`Unwrap()` 返回 `ErrQuotaExceeded`。
  - coding 窗口配额 enforcement（Reserve 内）：`billing_plan='coding'` 时以 `pg_advisory_xact_lock(hashtext(user_id))` 按用户串行化，幂等重复 Reserve 先短路（不重检），随后查活跃 coding user_plan+plan limits 并以 finalized `charge` ledger 行（`-SUM(request_delta)`）计数消耗：`hourly_limit`→5 小时滚动、`weekly_limit`→周一 00:00 UTC→下周一 00:00 UTC（Go 侧计算 boundary，避免 DB 时区依赖）、`monthly_limit`→`activated_at`→`expires_at` 计划期；coding window limit `NULL` 或 `<=0` 均视为未设置/无限，不参与 hard limit 或降级比较。任一超限返回 `*QuotaExceededError`（对应 scope）；无活跃 coding 套餐 fail-closed（period, limit 0）。超限不写 reservation/ledger。
  - Phase 2 limit overrides（`internal/repository/overrides.go`）：`user_plan_limit_overrides` 表按 user_plan+scope 维度施加覆盖。`kind='reset'` 将该 scope 的 effective window start 前移至 `max(baseStart, latest active reset effective_from)`（原谅该时间点之前的消耗）；`kind='bonus'` 在生效区间（`now >= effective_from` 且 `effective_until IS NULL OR now < effective_until`）为 limit 追加 `bonus_requests`。revoke 为软失效（`effective_until = now()`，无需 status 列）。`overrideEffects`/`computeWindow` 被 Reserve enforcement、`GetUsageWindows`（返回 adjusted limit=base+active bonus、consumed since effective start、remaining）与 `GetBalance`（period scope 复用同一计算）共用。请求时间戳（usage_ledger.created_at 等）绝不修改，仅影响读取窗口与限额计算。`UserPlanLimitOverride` 结构体 + `CreateLimitOverride`/`ListLimitOverrides`/`RevokeLimitOverride` admin 方法（FK 违反→ErrInsertFailed，revoked 幂等，缺失→ErrNotFound）。
- `internal/server`：HTTP（chi）。
  - `GET /healthz`、`GET /readyz`。
  - `GET /v1/billing/plans`、`GET /v1/billing/plans/{id}`。
  - `GET /v1/billing/users/{user_id}/plan`（兼容：最新 active user_plan）与 `GET /v1/billing/users/{user_id}/plans`（Panel 概览使用：全部未过期 active user_plan，JOIN active plan 元数据，返回 plan_name/category/price/hourly/weekly/monthly/token limit）。
  - `POST /v1/billing/quota/reserve`、`/finalize`、`/release`（2 MiB body 限，幂等冲突映射 200）。Reserve 对 `*QuotaExceededError` 返回 429 + `quota_exceeded: <scope>`（scope=hour5/weekly/period），仅暴露 scope 不泄露 SQL/DSN。
  - `GET /v1/billing/users/{user_id}/ledger`。
  - `GET /v1/billing/users/{user_id}/balance`：返回 `{coding_remaining, token_remaining}` 十进制字符串。Coding=active coding 套餐月配额减本月已 charge 请求数；Token=active token 套餐 token_limit 加 net token_delta（全期），二者均钳到 >=0；无套餐/无账本返回 0，永不 ErrNotFound。
  - `GET /v1/billing/users/{user_id}/usage-windows`：返回活跃 coding 套餐的 hour5/weekly/period 窗口（limit/consumed/remaining/window_start/window_end），与 Reserve enforcement 同源计数；limit 为 override-adjusted（base+active bonus），window_start 为 effective start（max base start, latest active reset effective_from），无活跃 coding 套餐返回空数组。
  - Admin user_plan lifecycle endpoints：`POST /v1/billing/admin/user-plans/{id}/renew`（续费：按 `extend_days` 从当前未来到期日/now 延长，或显式设置 `expires_at`；不修改历史账本）、`POST /v1/billing/admin/user-plans/{id}/switch`（切换套餐：取消旧 user_plan，并为同 user 创建新 plan 绑定；目标必须同 plan_type 且最大总额度不低；coding 仅比较 `monthly_limit`/周期总额（hourly/weekly 是节流限制，不代表套餐等级），token 比较 `token_limit`；不以 price 判定等级（运营/反馈赠送套餐可 price=0 但额度更高）；历史请求继续归属于旧套餐周期，不改 usage_ledger；`/upgrade` 仅保留兼容）、`POST /v1/billing/admin/user-plans/{id}/cancel`（撤销 active 用户套餐，幂等）。
  - Phase 2 admin endpoints：`POST /v1/billing/admin/user-plans/{id}/limit-overrides`（create，校验 kind/scope/bonus_requests，effective_from 默认 now，支持 RFC3339 effective_from/effective_until）、`GET /v1/billing/admin/user-plans/{id}/limit-overrides`（list，newest-first）、`POST /v1/billing/admin/limit-overrides/{id}/revoke`（soft-revoke，幂等，not-found 404）。
  - 协议原生 JSON 错误，不泄漏 DSN/SQL/凭据；所有响应 `Cache-Control: no-store`。
- `migrations/000001_init.{up,down}.sql`：Billing DB schema（从 `infra/db/migrations/billing/0001_init.sql` 转换为 golang-migrate 格式；plan category 支持 `daily`/`weekly`/`monthly`/`quarterly`/`yearly`）。
- `migrations/000002_limit_overrides.{up,down}.sql`：Phase 2 `user_plan_limit_overrides` 表（kind reset/bonus、scope hour5/weekly/period、user_plan_id FK、effective_from、effective_until nullable、bonus_requests nullable、reason、created_by、created_at）。镜像 `infra/db/migrations/billing/0002_limit_overrides.sql`。
- `migrations/000003_plan_daily_weekly_categories.{up,down}.sql`：扩展 plan category CHECK 以支持天卡/周卡。镜像 `infra/db/migrations/billing/0003_plan_daily_weekly_categories.sql`。

## 验证

```bash
# 单元测试（无需 DB）
go test ./internal/config/... ./internal/database/... ./internal/server/...

# repository 集成测试（需临时 pg）
BILLING_REPO_TEST_DSN="postgres:///tokenmp_billing?host=/tmp&port=55435" go test -race ./internal/repository/...

# 进程联调
BILLING_DATABASE_URL=... BILLING_HTTP_ADDR=127.0.0.1:18085 go run ./cmd/billing
```

- gofmt/vet/build 通过。
- repository 集成测试：reserve→finalize→release 完整流、幂等、not-found、ledger 查询、plan/user_plan 查询、Phase 2 limit overrides（bonus 提升 enforcement/window、reset 前移 window start、revoke 软失效与幂等、expired bonus 不生效）。
- process smoke test：healthz/readyz 200、list plans/get user plan 200、reserve/finalize/release 200（幂等）、ledger 2 条（重复调用未产生重复）、missing field 400。

## 待实现（后续）

- 余额聚合/对账（reserve hold 与 charge final 的余额计算）——当前 repository 只机械持久化 delta。
- marketplace_*（可选独立模块，schema 占位）。
- Edge/BFF 接入（请求开始调 Reserve、结束调 Finalize）。
- 套餐过期/续费逻辑。

## 约束

- **DO NOT** 用 `AutoMigrate`——schema 由 `migrations/` 版本化 SQL 管。
- **DO NOT** 让 executor 直连此库——由 Edge/BFF + Billing Service 操作。
- **DO NOT** 让 driver 错误经 `Error()`/`Unwrap()` 暴露 DSN。
- **DO NOT** 提交密钥/连接串/生产数据。
- DB 路径硬限 `/tokenmp_billing`，绝不连其他库。
- Reserve/Finalize/Release 单事务 + idempotency_key 幂等。

## 文档维护

计费模型、幂等策略、预留结算流程变化时，同步维护本文件、`services/AGENTS.md` 与 `infra/db/AGENTS.md`。
