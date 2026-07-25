# TokenMP v3 后台管理（Admin）设计

> 状态：方案设计 · 待评审
> 作者：pi · 日期：2026-07-25
> 参考：旧版 july `admin/`、executor `dashboard/` 的成熟实践

## 一、背景与目标

dev 服务器已部署完整后端（auth/api/billing/logging/notice/executor），用户侧 Panel
已对接真实 API。现需设计**面向运营/管理员的后台管理界面**（Admin），用于管理平台
配置、用户、套餐、请求日志、公告通知等。

### 设计原则

1. **与 Panel 独立**：Admin 是独立路由段 `/admin/*`，独立鉴权（`role=admin`），不复用
   Panel 的用户级组件（避免权限耦合）。
2. **契约先行**：所有 admin 端点先定义 OpenAPI（`packages/contracts/openapi/admin/v1.yaml`），
   Edge `services/api` 实现，前端类型从契约生成，不手写。
3. **数据域对齐后端**：Admin 的每个页面映射到已存在的后端数据域（auth/billing/logging/notice
   /executor config），不造无后端支撑的页面。
4. **移动端可降级**：Admin 以桌面为主（管理操作密集），但列表/详情保证移动端可读（复用 Panel
   的 mobile-first 表格→卡片降级模式）。
5. **安全边界**：admin 端点全部 `role=admin` 守卫，写操作审计日志（logging service 已就绪），
   敏感字段（credential/secret）永不回传前端。

## 二、旧版调研结论

### july `admin/`（最完整，22 个页面）

导航分组：主导航(控制台) / 域(用户·套餐·Provider·端点·上游Key·供应审核·路由组·路由策略·模型·兑换码·价格倍率)
/ 运营(请求日志) / 基建(公告·通知·版本·联系方式) / 系统(系统配置·迁移记录)

特点：
- 控制台有趋势图（recharts）+ 今日快照 + 模型用量 + Top 用户
- CRUD 统一用 `Modal + FormSection/Field` 表单组件
- 列表页统一用搜索 + FilterChip 筛选 + 分页
- 服务端鉴权：layout.tsx 里 `fetch /user/me`，`role !== 'admin'` redirect

### executor `dashboard/`（较简，6 个页面）

keys/codes/errors/models/plans/providers，recharts 用量图表。

### v3 的差异

- v3 有独立的 **Config Service**（`services/config`，已有 DB schema：providers/
  upstream_endpoints/upstream_credentials/models/adapters/route_mappings/
  route_groups/routing_policies/config_revisions/config_revision_snapshots/
  config_audit_log/global_config/price_multiplier_rules）。但当前只实现了**读路径**
  （`GET /v1/config/snapshots/latest`），**draft/publish 写路径是 TODO**（main.go 注释明说）。
  Executor 优先从 Config Service 拉，`EXECUTOR_CONFIG_SERVICE_URL` 为空时 fallback
  到 JSON 文件。Admin 的模型/路由/provider 管理应推动 Config Service 实现写路径，
  而非编辑文件。
- v3 的 credential 采用 `vault://` ref + Secret Store 分离设计：Config DB 的
  `upstream_credentials` 表只存 ref + key_prefix/suffix（展示用），明文在 Secret Store。
  Admin 可管理 credential 元数据，但明文不上 admin UI。
- v3 有独立的 Notice Service，公告/通知/changelog 可直接管理（已有 DB + API）。

## 三、Admin 路由与导航结构

```
/admin                          → 控制台（概览）
/admin/users                    → 用户管理
/admin/users/[id]               → 用户详情（密钥/套餐/用量/日志聚合）
/admin/plans                    → 套餐管理（CRUD）
/admin/user-plans               → 用户套餐绑定（分配/撤销）
/admin/api-keys                 → 全局 API 密钥（只读 + 禁用/撤销）
/admin/request-logs             → 请求日志（全局，跨用户）
/admin/request-logs/[id]        → 请求详情（attempts/events）
/admin/announcements            → 公告管理（CRUD + 发布）
/admin/changelogs               → 版本日志管理（CRUD）
/admin/notifications            → 通知管理（异步发送/查看）
/admin/providers                → Provider 与上游凭据（CRUD + 元数据）
/admin/models                   → 模型配置（经 Config Service）
/admin/routes                   → 路由配置（经 Config Service）
/admin/billing/usage            → 用量统计（全局聚合）
/admin/settings                 → 系统设置（平台级）
```

### 导航分组（侧边栏）

| 分组 | 页面 | 图标 | 数据源 |
|---|---|---|---|
| **主导航** | 控制台 | Shield | logging + billing 聚合 |
| **用户域** | 用户管理 | Users | auth.users |
| | API 密钥 | Key | auth.api_keys |
| | 套餐 | Package | billing.plans |
| | 用户套餐 | UserCheck | billing.user_plans |
| **运营** | 请求日志 | ScrollText | logging.request_logs |
| | 用量统计 | BarChart3 | logging.stats + billing.ledger |
| **内容** | 公告 | Megaphone | notice.announcements |
| | 版本日志 | Sparkles | notice.changelogs |
| | 通知 | Bell | notice.notifications |
| **执行** | Provider 与上游凭据 | Server | config.providers + upstream_credentials |
| | 模型配置 | Box | config.models（经 Config Service） |
| | 路由配置 | Route | config.route_mappings（经 Config Service） |
| **系统** | 系统设置 | Settings | platform config |

## 四、页面详细设计

### 4.1 控制台 `/admin`

**今日快照**（4 卡片）：
- 今日请求数 / 成功率 / 今日活跃用户 / 今日 Token 消耗

**趋势图**（recharts AreaChart，15 天）：
- 请求量 + 成功率双轴
- Token 输入/输出堆叠

**今日模型用量**（表格）：
- model / requests / success% / tokens / cost

**今日 Top 用户**（表格）：
- email / requests / tokens / cost

**后端需求**：`GET /api/v1/admin/stats?days=N`（聚合 logging + billing）

### 4.2 用户管理 `/admin/users`

**列表**（表格，搜索 + status 筛选 + 分页）：
- email / role / status / 创建时间 / 套餐 / 今日请求数

**操作**：
- 禁用/启用用户（`PATCH /api/v1/admin/users/{id}` status）
- 提升为管理员/降级（`PATCH role`）
- 查看详情（跳转 `/admin/users/[id]`）

**详情页**（聚合视图）：
- 用户基本信息
- 该用户的 API 密钥列表（只读）
- 该用户的套餐绑定
- 该用户的请求日志（分页）
- 该用户的用量统计

**后端需求**：
- `GET /api/v1/admin/users`（分页 + 搜索）
- `GET /api/v1/admin/users/{id}`（聚合）
- `PATCH /api/v1/admin/users/{id}`（status/role）

### 4.3 API 密钥 `/admin/api-keys`

**列表**（全局，跨用户）：
- user(email) / name / keyPrefix…keySuffix / status / 创建时间 / 最近使用

**操作**：禁用/撤销（不可创建——用户自己创建）

**后端需求**：`GET /api/v1/admin/keys`（代理 auth，跨用户）

### 4.4 套餐管理 `/admin/plans`

**列表**（Tab: coding/token）：
- name / planType / price / totalQuota / durationDays / status

**CRUD**（Modal 表单）：
- name / planType(coding|token|image|free) / price / category(monthly|quarterly|yearly)
  / monthly_limit / token_limit / allowed_models(多选) / status

**后端需求**：
- `GET/POST /api/v1/admin/plans`
- `GET/PATCH/DELETE /api/v1/admin/plans/{id}`

### 4.5 用户套餐 `/admin/user-plans`

**列表**（按用户）：
- user(email) / plan / planType / status / 激活时间 / 到期时间 / remaining

**操作**：
- 分配套餐（Modal：选用户 + 选套餐 + 激活/到期）
- 撤销套餐（status→cancelled）

**后端需求**：
- `GET/POST /api/v1/admin/user-plans`
- `PATCH/DELETE /api/v1/admin/user-plans/{id}`

### 4.6 请求日志 `/admin/request-logs`

**列表**（全局，跨用户，比 Panel 多 user 列）：
- 时间 / user(email) / model / status / 耗时 / inputTokens / outputTokens / cost

**筛选**：user / model / status(success|error|all) / 日期范围

**详情**（抽屉或独立页）：
- 请求摘要 + attempts（provider/model/status/latency）+ events 时间线

**后端需求**：
- `GET /api/v1/admin/request-logs`（跨用户，复用 logging client）
- `GET /api/v1/admin/request-logs/{id}`

### 4.7 公告管理 `/admin/announcements`

**列表**：title / severity / status(draft|published) / 发布时间

**CRUD**（Modal + Markdown 编辑器）：
- title / content(Markdown) / severity(info|warning|success) / status / 发布时间

**后端需求**：扩展 Notice Service 加 admin 端点
- `GET/POST /api/v1/admin/announcements`
- `PATCH/DELETE /api/v1/admin/announcements/{id}`
- `POST /api/v1/admin/announcements/{id}/publish`

### 4.8 版本日志 `/admin/changelogs`

同公告结构（CRUD + Markdown），字段：version / title / content / published_at

### 4.9 通知管理 `/admin/notifications`

**发送通知**（表单）：
- 收件人（全体 / 指定用户）/ type / title / content / action(link:{label,href})

**列表**：已发送通知 + 已读率

**后端需求**：扩展 Notice Service
- `POST /api/v1/admin/notifications/send`
- `GET /api/v1/admin/notifications`（含已读统计）

### 4.10 模型配置 `/admin/models`

展示 executor config snapshot 中的 Models：
- model ID / displayName / capabilities(text|tools|vision|thinking|image) / thinking bounds

来源：`GET /api/v1/admin/executor-config`（Edge 代理 Config Service）

> **实现依赖**：需先推动 Config Service 实现写路径（draft/publish），否则模型配置
> 只能读不能改。当前 Config Service 只有 `GET /v1/config/snapshots/latest`。
> Admin 的 CRUD 会驱动 Config Service 的 `POST /v1/config/drafts` +
> `POST /v1/config/revisions/{id}/publish` 实现。

### 4.11 路由配置 `/admin/routes`

展示 Routes：
- route ID / model / provider / upstreamModel / protocol / priority / enabled / quarantine state

> 同上，依赖 Config Service 写路径。

### 4.12 用量统计 `/admin/billing/usage`

- 全局用量趋势（按天/按模型）
- 收入统计（usage_ledger 聚合）
- 配额消耗 Top 用户

**后端需求**：`GET /api/v1/admin/usage/stats?days=N&groupBy=model|user`

### 4.13 Provider 与上游凭据 `/admin/providers`

**Provider 列表**（CRUD）：
- id / name / displayLabel / selector / baseURL / sdkKind / protocol / status

**上游凭据**（元数据管理，不涉及明文）：
- provider / credentialRef(`vault://...`，只展示) / keyPrefix…keySuffix / priority / status

> 明文 secret 通过 Secret Store / 环境变量管理，**不进 admin UI**。
> Admin 只管理 `upstream_credentials` 表的元数据（ref/priority/status）。
> **前置依赖**：Config Service 写路径 + Secret Store 接入。

### 4.14 系统设置 `/admin/settings`

- 平台名称 / 默认套餐 / 注册开关 / 维护模式
- JWT 过期时间配置（只读展示）

**后端需求**：platform config（待定存储，可能 Config Service）

## 五、技术实现方案

### 5.1 前端

- **路由**：`apps/web/src/app/admin/`（与 `panel/` 平级）
- **布局**：`admin/layout.tsx` 做 `role=admin` 守卫（复用 Panel 的 sidebar/header 模式，但独立
  的 `admin-sidebar` 组件）
- **鉴权**：复用现有 Zustand auth store + JWT，`role !== 'admin'` 时 redirect `/panel`
- **组件**：复用 Panel 的 Card/Table/Badge/Dialog，新增 `FormSection`/`Field` 表单组件族
  （参考 july）
- **图表**：新增 `recharts` 依赖（趋势图/用量图）
- **API 层**：`src/lib/api/admin.ts`，mock 默认 + `NEXT_PUBLIC_USE_MOCK_ADMIN` 切换

### 5.2 后端

- **契约**：新建 `packages/contracts/openapi/admin/v1.yaml`，定义全部 admin 端点
- **实现位置**：`services/api`（Edge/BFF）新增 `internal/admin/handlers.go`，聚合下游服务
- **鉴权中间件**：`identity.Middleware` + 新增 `RequireAdmin`（校验 `role=admin`）
- **跨服务调用**：
  - 用户/密钥 → Auth Service（已有 HTTP API，Edge 代理）
  - 套餐/用量 → Billing Service（需扩展 admin 端点）
  - 日志 → Logging Service（已有 list/stats，需加跨用户查询）
  - 公告/通知 → Notice Service（需加 admin 端点，**通知发送走异步队列**）
  - 模型/路由/provider/credential → Config Service（需实现写路径 draft/publish）
- **鉴权中间件**：`identity.Middleware` + `RequireAdmin`（校验 `role=admin`）
- **审计**：所有写操作记录到 logging（`POST /v1/logs/ingest` event_type=admin_action）

### 5.3 下游服务扩展需求

| 服务 | 需新增端点 | 优先级 |
|---|---|---|
| Auth | `GET /api/v1/admin/users` 分页 + `PATCH /users/{id}` status/role + `GET /admin/keys` 跨用户 | 高 |
| Billing | admin plans CRUD + user-plans CRUD + 跨用户 usage stats | 高 |
| Logging | 跨用户 request-logs 查询（已有，去掉 user_id 过滤即可）+ admin stats 聚合 | 中 |
| Notice | admin announcements/changelogs CRUD + notifications **异步发送**（队列/后台 worker） | 中 |
| Config | **写路径 draft/publish**（当前只读）+ admin providers/credentials/models/routes CRUD | 低（但 Phase 4 前置） |

## 六、实施分期建议

### Phase 1（MVP，可独立交付）
- Admin 布局 + 鉴权守卫 + 侧边栏
- 控制台（今日快照 + 趋势图）
- 用户管理（列表 + 禁用/启用 + 详情聚合）
- 请求日志（全局列表 + 详情）

### Phase 2（内容运营）
- 公告管理 CRUD
- 版本日志 CRUD
- 通知发送

### Phase 3（套餐计费）
- 套餐 CRUD
- 用户套餐分配
- 用量统计

### Phase 4（执行配置 + 凭据）
- **前置**：Config Service 实现写路径（draft/publish），这是本阶段一切 CRUD 的基础
- Provider CRUD + 上游凭据元数据管理（不含明文）
- 模型配置 CRUD（经 Config Service）
- 路由配置 CRUD（经 Config Service）
- 系统设置

## 七、已决策

1. **Config 管理**：不编辑文件，推动 Config Service 实现写路径（draft/publish），
   admin CRUD 操作 DB，编译后的 snapshot 下发 executor。当前 Config Service 只读
   是临时状态，admin 会驱动补齐写路径。
2. **Credential 展示**：admin 管理上游凭据元数据（ref/prefix/suffix/priority/status），
   明文 secret 由 Secret Store / 环境变量管理，不进 admin UI。需新增 Provider 与
   上游凭据管理页。
3. **通知发送**：异步，走队列/后台 worker，不同步写 DB。
4. **Admin 端点归属**：全部放 Edge `services/api` 聚合，统一鉴权 + 不暴露下游给前端。
