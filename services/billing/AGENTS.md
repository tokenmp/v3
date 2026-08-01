# Billing Service

> 作用域：`services/billing/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 职责

Billing Service 是 TokenMP V3 分层架构的**业务平面**计费服务：

- 管理套餐（plans）、用户套餐绑定（user_plans）、配额预留/结算/释放（quota_reservations）、用量账本（usage_ledger）。
- **持久化计费结算状态机**（reserve→finalize/release/pending→reconcile）：请求开始时 Edge/BFF 调 Reserve 预留（active hold 计入 hard quota 窗口，阻止并发穿透），成功且 usage confirmed 时 Finalize 结算实际值，失败/取消 Release，commit 后 usage unknown 或 Billing 暂不可用时 MarkPending（**禁止按 1 token 猜测**），reconciler 事后凭 confirmed evidence 补结算。
- **Billing 是唯一持久化 entitlement/ledger 所有者**；executor 只产出 secret-free normalized terminal usage evidence（经 Logging 持久化），不直连 Billing DB。DB 语义仅声称单写事务 + 幂等/唯一约束，不声称跨进程 exactly-once。
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
  - coding 窗口配额 enforcement（Reserve 内）：`billing_plan='coding'` 时以 `pg_advisory_xact_lock(hashtext(user_id))` 按用户串行化，幂等重复 Reserve 先短路（不重检），随后查活跃 coding user_plan+plan limits 并以 finalized `charge` ledger 行 **`+ confirmed `reconcile` 行**（`-SUM(request_delta)`，仅负 delta 计入消费，refund/sweep 逆转除外）计数消耗：`hourly_limit`→5 小时滚动、`weekly_limit`→周一 00:00 UTC→下周一 00:00 UTC（Go 侧计算 boundary，避免 DB 时区依赖）、`monthly_limit`→`activated_at`→`expires_at` 计划期；coding window limit `NULL` 或 `<=0` 均视为未设置/无限，不参与 hard limit 或降级比较。任一超限返回 `*QuotaExceededError`（对应 scope）；无活跃 coding 套餐 fail-closed（period, limit 0）。超限不写 reservation/ledger。`ListPendingReservations` 返回 `PendingReservation` 富投影（id/request_id/user_id/billing_plan/reserved_count/reserved_at）供 reconciler 无二次查询。
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
- `migrations/000004_settlement_state_machine.{up,down}.sql`：持久化结算状态机增量迁移。镜像 `infra/db/migrations/billing/0004_settlement_state_machine.sql`。变更（纯增量、对旧数据安全）：`quota_reservations.status` CHECK 新增 `pending_reconciliation`；新增 `usage_known`/`settlement_status`/`reconciled_at`/`idempotency_payload_hash` 列；新增 `reserved_*`/`final_*` amount 非负 CHECK；新增请求级部分唯一索引 `quota_reservations_request_active_uidx`（同一 `request_id` 在 active(reserved/pending_reconciliation) 期间最多一个 hold，防双扣）；新增 sweeper/reconciler 查询索引；`usage_ledger.ledger_type` CHECK 新增 `reconcile`/`sweep`。账本永不删除。**down 为 fail-closed**：存在任一 000004-only 数据（`pending_reconciliation` 行、`usage_ledger` ledger_type=`reconcile`/`sweep` 行、或 reservation 的 `settlement_status`/`usage_known=true`/`reconciled_at`/`idempotency_payload_hash` 非默认值）时 `RAISE EXCEPTION`（仅计数、无 secret），要求操作者先经 reconciler/runbook 清理/转换这些数据后再回退（见 Downgrade runbook preflight）。preflight 在任何 DDL 前执行；golang-migrate 逐文件包裹事务，失败回滚干净，raw pgx/psql Exec 场景因 preflight 先于任何 DROP 亦保证 schema 完整。

## 持久化结算状态机

`internal/repository`（`SettlementManager` 端口 + `GormRepository`）实现可审计的单写事务状态机：

| 操作 | 语义 |
|---|---|
| `Reserve` | 创建 `reserved`+`held` 行与 `reserve` 账本行；active reserved/pending holds **计入** coding hard quota 窗口（`computeWindow` 合并 finalized charge + confirmed reconcile + active holds），per-user `pg_advisory_xact_lock` 串行化读检插入，阻止并发穿透；按 reservationID 幂等。 |
| `Finalize(id, reqs, tokens, usageKnown)` | `usageKnown=true` 时结算实际值为 `finalized`+`settled` 并追加 `charge` 行；重复同 payload 幂等 nil，不同 payload 或对立终态返回稳定 `ErrConflict`（→409）；`usageKnown=false` 被拒绝（禁止猜测）。 |
| `Release` | `reserved`→`released`+`released`，追加 `refund` 行；幂等；finalized/expired/pending→`ErrConflict`。 |
| `MarkPending` | `reserved`→`pending_reconciliation`+`pending`，hold 保留待 reconciler；幂等；其他终态→`ErrConflict`。 |
| `Reconcile(id, reqs, tokens, usageKnown)` | `usageKnown=true`→Finalize 实际值（`settled` + `reconcile` 账本行，**负 request_delta 计入 coding 窗口**）；`usageKnown=false`→Release held 金额（`released`，**under-charge，绝不伪造 token 计数**）；幂等。 |
| `Expire` | sweeper 对 `expires_at` 已过期的 `reserved` 行置 `expired`+`expired` 并追加 `sweep` refund 行；幂等；未到期/其他终态→`ErrConflict`。 |
| `ListExpiredReservations`/`ListPendingReservations` | sweeper/reconciler 批量查询，不依赖请求 context。 |

所有 amount 非负有界；sentinel `ErrNotFound`/`ErrConflict`/`ErrQuotaExceeded`/`ErrQueryFailed`/`ErrInsertFailed` 不泄漏 SQL/DSN；`*QuotaExceededError` 仅暴露 scope。`Finalize` 的 idempotency payload hash 为公开结算计数的 SHA-256 摘要（非 secret）。

`internal/server` 新增：`GET /v1/billing/quota/reservations/{reservation_id}`（安全结算状态投影）、`POST /v1/billing/quota/mark-pending`、`POST /v1/billing/quota/reconcile`。`POST /finalize` 新增 `usage_known` 字段。`finalize`/`release` 的 `ErrConflict` 现映射为稳定 **409**（同 payload 幂等仍 200）。Reserve 仍 429 + scope。所有响应 `Cache-Control: no-store`，错误协议原生 JSON 不泄漏。

`internal/evidence`：Billing→Logging 的窄、secret-free `Lookup` port，解析 pending reservation 的 terminal usage evidence（Billing **不直读 Logging DB**）。`LoggingClient` 拉 `GET /v1/logs/{request_id}`，严格 timeout（默认 10s）、禁 redirect、响应 ≤1 MiB、不泄漏 URL/host/body、可选服务 token（`Authorization: Bearer`），`usage_status=final`→Known，其余→`ErrNotTerminal`，404→`ErrNotFound`，不可达→`ErrUnavailable`；空 URL 时返回 `NilLookup`（全部保留 pending）。`Evidence`/sentinel 仅供 reconciler 使用。
`internal/sweeper`：后台 reconciler/expiry loop（`Sweeper.Run`，detached bounded context，graceful shutdown 观察进程 ctx）。每 tick 先 `ListExpiredReservations`→`Expire`，再对超过 `PendingGrace` 的 pending 行经 evidence port 查询 terminal evidence 后结算。reconciler **绝不猜测**、**绝不盲释放**：
- Known（Logging `usage_status=final`）→ `Reconcile(id, counts, true)` confirmed（coding 用 reserved request 计数、token 用 evidence token 计数）。
- NotFound（无 log 行）/ NotTerminal（usage 非 final）/ Logging 不可用 → **保留 pending，下一 tick 重试**，绝不 release。
- 仅当 pending 年龄超过显式 `RetentionDeadline`（默认 30m，必须 > grace）且配置 `UnknownPolicy=release_unknown` 时才释放（under-charge、审计 reason `retention-expired-unknown`）；默认 `keep_pending` 保留 pending 并告警计数。
Billing **不直读 Logging DB**：`internal/evidence` 的 `Lookup` port（`LoggingClient`）拉 Logging `GET /v1/logs/{request_id}`，严格 timeout、禁 redirect、不泄漏 URL/host/body，可选服务 token（见下 token 解析），空 URL 时 `NilLookup`（全部保留 pending）。`ListPendingReservations` 返回富投影（id/request_id/user_id/billing_plan/reserved_count）避免每行二次查询。单行失败不中止整批；`ErrConflict` 视为并发已解决不计错误；返回安全计数器（expired/reconciledKnown/reconciledUnknown/retentionAlerts/expiryErrors/reconcileErrors）供指标。配置：`BILLING_SWEEPER_ENABLED`(默认 true)/`BILLING_SWEEPER_INTERVAL`(30s)/`BILLING_SWEEPER_PENDING_GRACE`(2m)/`BILLING_SWEEPER_EXPIRY_BATCH`/`BILLING_SWEEPER_PENDING_BATCH`/`BILLING_SWEEPER_RETENTION_DEADLINE`(30m)/`BILLING_SWEEPER_UNKNOWN_POLICY`(keep_pending|release_unknown)/`BILLING_LOGGING_URL`/`BILLING_LOGGING_TIMEOUT`(10s)；非法值 fail-fast 不静默 fallback。sweeper 不删除账本，不声称跨进程 exactly-once。

**Logging service token 解析（`internal/config`）**：可选 Billing→Logging bearer token 经 `resolveLoggingToken` 从至多一个源解析，`main` 只拿最终 opaque token 交给 `evidence.NewClient`（从不持 path）。
- `BILLING_LOGGING_SERVICE_TOKEN_FILE`（生产）：只读 Docker secret mount 路径，按项目 secret-file 铁律读取——`Lstat` 拒 symlink/non-regular、post-open `Stat`+`os.SameFile` 闭 Lstat→Open TOCTOU、8 KiB `LimitReader` 上限、严格 UTF-8、拒 NUL/CR/LF（先 trim 周围空白含 Docker secret 常见尾换行）、trim。失败为稳定非 wrapping sentinel（`ErrLoggingTokenFile*`），不泄漏 path/content。
- `BILLING_LOGGING_SERVICE_TOKEN`（dev/test 内联值）：同样内容校验（UTF-8、拒 NUL/CR/LF、trim），失败 `ErrLoggingTokenInvalid`。
- 双源同时存在→`ErrLoggingTokenConflict` fail-fast。
- token 非 empty 但 `BILLING_LOGGING_URL` 为空→`ErrLoggingTokenWithoutURL` fail-fast，避免误配置静默携带无用 secret；URL 启用无 token 合法（Logging 允许匿名）。

## 内部 HTTP 契约

`packages/contracts/openapi/billing/v1.yaml` 是 Billing 内部 HTTP 唯一事实源（reserve/finalize/release/get-status/mark-pending/reconcile + healthz/readyz）。未接入 oapi-codegen 生成（contract conformance）。禁止泄漏账本/DB/上游 body；`forbiddenTerms` lint 已覆盖（不得出现 transaction/commit/postgres 等）。Durability 声明仅限于单写事务幂等/唯一约束；**不声称跨进程 exactly-once**。

## Pending evidence/retry/retention 策略

- Edge 请求 context cancel 后仍用 **bounded detached context** MarkPending（10s），不恢复 1-token fallback；token 计划成功后单次有界查询 Logging evidence，known→Finalize，未知→MarkPending。
- pending reservation 持久关联 `request_id`/`reservation_id`；reconciler 经 evidence port 在 grace 内/到期时查询 terminal evidence：known→confirmed reconcile，NotFound/NotTerminal/不可用→保留 pending 重试至 `RetentionDeadline`。
- Logging 不可用 **绝不释放**（ErrUnavailable→保留 pending）。
- 仅超过显式较长 `RetentionDeadline` 且 `UnknownPolicy=release_unknown` 才可 release/sweep，审计 reason；默认 `keep_pending` 保留 pending 并告警。


```bash
## 验证

```bash
# 单元测试（无需 DB）
go test ./internal/config/... ./internal/database/... ./internal/server/... ./internal/sweeper/... ./internal/evidence/...

# repository 集成测试（需临时 pg；含 settlement 状态机/幂等冲突/expiry/reconcile/请求级唯一约束/active-hold 窗口）
BILLING_REPO_TEST_DSN="postgres:///tokenmp_billing?host=/tmp&port=55435" go test -race ./internal/repository/...

# 进程联调
BILLING_DATABASE_URL=... BILLING_HTTP_ADDR=127.0.0.1:18085 go run ./cmd/billing
```

- gofmt/vet/build 通过。
- repository 集成测试：reserve→finalize→release 完整流、幂等、not-found、ledger 查询、plan/user_plan 查询、Phase 2 limit overrides（bonus 提升 enforcement/window、reset 前移 window start、revoke 软失效与幂等、expired bonus 不生效）。
- process smoke test：healthz/readyz 200、list plans/get user plan 200、reserve/finalize/release 200（幂等）、ledger 2 条（重复调用未产生重复）、missing field 400。
- settlement 单元测试覆盖：known evidence reconcile confirmed、not-found/non-terminal/Logging outage 保留 pending、retention keep_pending/release_unknown、duplicate reconcile 幂等、coding/token confirmed-count 映射、nil evidence 保留 pending。
- migration down fail-closed PG 测试：up→插 reconcile/sweep ledger + pending + settled 列→down 预期失败（schema/index/columns/check 完整保留）→清理/转换为旧 schema 可表达状态→down 成功（三类 preflight 并列）。
- repository 集成测试隔离约定：每个测试经 `resetSchema`（`DROP SCHEMA public CASCADE`+`CREATE SCHEMA`）+ 全量 up 取得干净完整 schema，cleanup 同样 reset；**绝不用 down migration 做测试清理**（000004 down 是 fail-closed，旧 down-loop 在 down4 RAISE 后忽略错误继续 down3..1 删表会污染下一测试）。`dsn()` 强制 DSN 含 `tokenmp_billing` 以防 reset 误伤非测试库；`applyUpMigrations` 事后校验 6 张核心表存在以拦截半应用 schema。测试不得依赖执行顺序。

## Downgrade runbook（preflight）

000004 down 在存在任一 000004-only 数据时 `RAISE EXCEPTION`（仅计数、无 secret）。这些数据旧 schema 无法表达，回退会孤儿化在途 hold / 违反账本永不删除 / 丢失已结算证据，导致稳定少收费。preflight 在任何 DDL 前执行（见上）。回退前必须先清零这些数据：

```sql
-- preflight: 回退前检查所有 000004-only 数据
SELECT
  (SELECT count(*) FROM quota_reservations WHERE status = 'pending_reconciliation') AS pending_count,
  (SELECT count(*) FROM usage_ledger WHERE ledger_type IN ('reconcile', 'sweep')) AS reconcile_sweep_count,
  (SELECT count(*) FROM quota_reservations
     WHERE settlement_status IS NOT NULL
        OR usage_known = true
        OR reconciled_at IS NOT NULL
        OR idempotency_payload_hash IS NOT NULL) AS settled_count;
```

任一计数 > 0 均阻止回退：
1. `pending_count > 0`：确保 sweeper 配置 `BILLING_LOGGING_URL` 指向可用 Logging，等待 reconciler 自动结算 confirmed 或（`release_unknown` 策略下）释放超 retention 的行；或经 Billing API 手动 reconcile/release 各 pending reservation（`POST /v1/billing/quota/reserve/reconcile`）。
2. `reconcile_sweep_count > 0`：账本永不删除。一旦存在 `reconcile`/`sweep` 账本行即说明已发生过结算/扫描，旧 schema 的 CHECK 不接受这些类型；这种情况下**不应回退**到 000004 之前，应保留当前 schema（如需回退需专门的迁移转换/归档，超出常规 runbook）。
3. `settled_count > 0`：reservation 已被结算/对账（非默认列值），旧 schema 无处记录。同样不应回退；若为测试/迁移中间态可显式将其清回旧 schema 可表达状态（`settlement_status=NULL`、`usage_known=false`、`reconciled_at=NULL`、`idempotency_payload_hash=NULL`）后再重试。
4. 重跑 preflight 直到三项计数均为 0，再执行 `migrate down`。

## 待实现（后续）

- 余额聚合/对账（reserve hold 与 charge final 的余额计算）——当前 repository 只机械持久化 delta。
- marketplace_*（可选独立模块，schema 占位）。
- 套餐过期/续费逻辑。

> Edge/BFF 接入与持久化结算闭环已实施（见 `services/api/AGENTS.md` 的 committed-aware settlement coordinator）；pending 事后结算由 Billing reconciler 经 evidence port 负责（见上 Pending evidence/retry/retention 策略）。


## 约束

- **DO NOT** 用 `AutoMigrate`——schema 由 `migrations/` 版本化 SQL 管。
- **DO NOT** 让 executor 直连此库——由 Edge/BFF + Billing Service 操作。
- **DO NOT** 让 driver 错误经 `Error()`/`Unwrap()` 暴露 DSN。
- **DO NOT** 提交密钥/连接串/生产数据。
- DB 路径硬限 `/tokenmp_billing`，绝不连其他库。
- Reserve/Finalize/Release 单事务 + idempotency_key 幂等。

## 文档维护

计费模型、幂等策略、预留结算流程变化时，同步维护本文件、`services/AGENTS.md` 与 `infra/db/AGENTS.md`。

## Container image and Compose

- `Dockerfile` is built with the repository root as context and produces only the static
  `billing` binary in a non-root Alpine runtime image. Its service-local module download runs
  with `GOWORK=off`; the shared `packages/go/httpresp` replace target is copied explicitly.
- The image health check probes `/readyz`, the HTTP and database readiness route.
- Root `compose.yaml` owns the service definition only; provide required database and key
  inputs at deploy time, and do not add shared PostgreSQL/Redis/proxy resources or secrets.

The repository-root build context means Dockerfile `COPY` sources are rooted at
`services/<service>` (with the shared `packages/go/httpresp` copied from the same root), not
at the Dockerfile directory. `tools/check-dockerfile-copy-sources.sh` statically guards this
before CI Docker builds.

Compose supplies `BILLING_LOGGING_URL=http://logging:8083`, explicit sweeper defaults,
and a read-only `BILLING_LOGGING_SERVICE_TOKEN_FILE` Docker secret, consumed by the Billing
`internal/config` secret-file loader (production source; `BILLING_LOGGING_SERVICE_TOKEN` is a
dev/test-only direct env alternative and the two are mutually exclusive). Do not pass the path
as the string token or add a token-reading entrypoint wrapper.
