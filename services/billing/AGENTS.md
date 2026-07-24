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
  - 端口 `PlanReader`（GetPlan/ListPlans）、`UserPlanReader`（GetActiveUserPlan）、`QuotaManager`（Reserve/Finalize/Release，单事务 + ON CONFLICT DO NOTHING 幂等）、`LedgerReader`（ListLedger）、`BalanceReader`（GetBalance）。
  - `GormRepository` 实现。reserve/charge/refund 用 ledger delta 有符号方向（reserve/charge 负、refund 正）。idempotency_key UNIQUE 保证账本幂等。
  - sentinel：`ErrNotFound`/`ErrQueryFailed`/`ErrInsertFailed`/`ErrConflict`，不泄漏 DSN/SQL。
- `internal/server`：HTTP（chi）。
  - `GET /healthz`、`GET /readyz`。
  - `GET /v1/billing/plans`、`GET /v1/billing/plans/{id}`。
  - `GET /v1/billing/users/{user_id}/plan`。
  - `POST /v1/billing/quota/reserve`、`/finalize`、`/release`（2 MiB body 限，幂等冲突映射 200）。
  - `GET /v1/billing/users/{user_id}/ledger`。
  - `GET /v1/billing/users/{user_id}/balance`：返回 `{coding_remaining, token_remaining}` 十进制字符串。Coding=active coding 套餐月配额减本月已 charge 请求数；Token=active token 套餐 token_limit 加 net token_delta（全期），二者均钳到 >=0；无套餐/无账本返回 0，永不 ErrNotFound。
  - 协议原生 JSON 错误，不泄漏 DSN/SQL/凭据；所有响应 `Cache-Control: no-store`。
- `migrations/000001_init.{up,down}.sql`：Billing DB schema（从 `infra/db/migrations/billing/0001_init.sql` 转换为 golang-migrate 格式）。

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
- repository 集成测试：reserve→finalize→release 完整流、幂等、not-found、ledger 查询、plan/user_plan 查询。
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
