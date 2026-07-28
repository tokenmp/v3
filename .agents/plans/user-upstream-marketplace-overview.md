# 用户上游市场（User Upstream Marketplace）

- 状态：deferred（待实现，优先级在调用链优化之后）
- 创建日期：2026-07-29
- 最后更新：2026-07-29
- 关联分支：none（分期开分支）
- 负责人：Agent / User
- Git tag：`design/user-upstream-marketplace`
- 备注：设计已定稿，待前期调用链优化完成后回头实施。实施时从 P1 文档开始。

## 目标

把上游配置能力从「平台管理员独占」下沉到「用户可自配置」，形成可共享、可记账的多租户上游市场。用户可以：

1. 在 Panel 配置自己的上游账号（provider、base_url、API key、模型清单）。
2. 选择自用、共享给其他用户、或两者兼有。
3. 通过 `model@provider_name` selector 显式选取（含他人共享的上游）。
4. 被他人调用时记录调用明细，用于后续收益结算。

## 核心决策（已确认）

| # | 决策 | 落地 |
|---|------|------|
| 1 | 共享选择机制 | 默认走平台池；用户注册全局唯一 `provider_name`，调用方用 `model@provider_name` 显式选取。复用现有 selector `model[:group][@provider]` 格式 |
| 2 | 定价权 | owner 自定价（`user_upstream_models.price_input/output_per_1k`，可空回退平台基础价）。平台 `price_multiplier_rules`/`models` 价格独立存在，互不干预 |
| 3 | 平台抽佣 | 一期不做抽佣，只记账（`gross_amount`，`commission`/`owner_revenue` 写 0） |
| 4 | owner 自用 quota | 默认不扣自己 quota；账号级开关 `owner_self_deduct_quota`（默认 false）、`owner_self_record_usage`（默认 true） |
| 5 | 结算货币 | 一期只做模拟提现：结算单 status 流转 `pending→confirmed→paid(模拟)`，不接真实支付 |
| 6 | 新建服务 | 新建 `services/upstream`，独立库 `tokenmp_upstream` |
| 7 | 状态约束 | 删除「上架后两个都关」场景。`published` 时 `sharing=public OR owner_self_use=true` 必须至少一个为真 |

## 有效状态矩阵

| status | sharing | owner_self_use | 含义 | 允许 |
|--------|---------|----------------|------|------|
| draft | — | — | 草稿，不进路由池 | ✓ |
| published | private | true | 仅自用 | ✓ |
| published | public | true | 自用+共享 | ✓ |
| published | public | false | 仅共享（自己不用） | ✓ |
| published | private | false | 两个都关 | ✗ 禁止 |

- `draft → published` 默认置为 `private + true`。
- 上架与后续修改都强制约束，违反返回 400。

## 路由优先级

```
请求 model@provider_name
  ├─ 带 @provider 且匹配某 user 的 published+public 上游 → 命中该用户上游
  ├─ 带 @provider 且是平台 provider → 走平台上游
  └─ 不带 @ → 平台默认池（现有逻辑）

caller==owner 且 owner_self_use=true 的自有上游：
  → 在 owner 用「不带 @」请求时优先命中（owner 自有优先于平台池）
```

## 数据模型总览

5 张表，位于新库 `tokenmp_upstream`：

- `user_upstream_accounts` — 用户上游账号（加密 key、provider_name、状态、共享/自用开关、限流）
- `user_upstream_models` — 账号暴露的模型清单（含 owner 定价）
- `user_upstream_call_records` — 共享调用记录（记账依据）
- `user_upstream_settlements` — 结算单（一期模拟提现）
- `user_upstream_balances` — owner 余额（pending/available）

完整 DDL 见各期文档。

## 服务改造清单

| 服务 | 改造 | 量级 |
|------|------|------|
| **新建 `services/upstream`** | 表 + CRUD + 加解密 + 上架/共享校验 + 记账 + 余额 | 大 |
| **Executor** | Resolver 候选来源 +用户 overlay；selector `@provider` 解析；owner 自有优先；catalog 合并；凭证端口；event 记 `upstream_owner_id`/`upstream_source`；quarantine 复用 | 大 |
| **Billing** | caller 扣 quota 复用现有；owner 自用按 `owner_self_deduct_quota` 控制 | 小 |
| **Logging** | events/attempts 增 `upstream_owner_id`、`upstream_source` | 中 |
| **API/Edge** | 基本无改（已有 `X-User-ID`） | 小 |
| **Notice** | 新增「密钥失效」「被调用」「结算」类型 | 小 |
| **Frontend** | Panel「我的上游」+「收益/记录」+ catalog 命名空间；Admin 监管视图 | 大 |

## 实施分期

| 阶段 | 文档 | 内容 |
|------|------|------|
| P1 | `user-upstream-marketplace-p1.md` | 新服务 + 表 + 加解密 + Panel CRUD + Executor owner 自有优先路由（仅 self_use，不共享）+ catalog 合并。不共享不计费 |
| P2 | `user-upstream-marketplace-p2.md` | sharing=public + `provider_name` selector + 限流 + quarantine + 非 owner 命中 |
| P3 | `user-upstream-marketplace-p3.md` | 记账（call_records + 余额）+ 模拟提现 + Notice 闭环 |
| P4 | `user-upstream-marketplace-p4.md` | admin 监管 + 密钥轮换通知 + 真实抽佣 |

每期文档独立完整，按顺序实施。

## 非目标（整体）

- 真实支付/提现对接（P4 也只做模拟，真实支付另行立项）
- 跨进程 exactly-once 调用保证（沿用 Executor 现有 trade-off）
- 用户上游的流式协议转换（沿用 Executor 现有 protocolconvert）

## 风险

- **密钥安全**：用户上游 key 是高敏数据，加密存储 + 运行时解密 + 审计日志，blast radius 隔离在新服务/新库。
- **Executor 改动面大**：Resolver 从单源变多源，需充分测试候选合并、优先级、quarantine 交互。
- **provider_name 冲突**：全局唯一约束 + 保留字检查（不能与平台 provider 重名）。
