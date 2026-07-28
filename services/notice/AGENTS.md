# Notice Service

> 作用域：`services/notice/`。继承仓库根目录与 `services/AGENTS.md`。

## 模块职责

- 负责：TokenMP v3 公告/通知/版本日志服务的运行时与 HTTP 接口。
  - 公告（announcements）：项目级公告列表与详情，按发布时间倒序。
  - 版本日志（changelogs）：产品版本更新记录列表与详情。
  - 通知（notifications）：per-user 通知列表、未读计数、标记已读/全部已读；通知可携带**通用、数据驱动的 action**（type/label/href），客户端按数据渲染，不写死任何跳转目标。
- 不负责：token 签发（由 Auth Service 负责）、用户管理、套餐/计费业务逻辑、跨程序通用通知平台、推送通道（邮件/WebSocket）。
- 所有者：TokenMP 后端基础设施。

## 必读文档

- 接口契约：`../../packages/contracts/openapi/notice/v1.yaml`（唯一权威副本）
- Migration 文件：`migrations/000001_create_announcements.{up,down}.sql`、`migrations/000002_create_changelogs.{up,down}.sql`、`migrations/000003_create_notifications.{up,down}.sql`
- Go workspace：`../../go.work`
- 鉴权参考：Auth Service JWT（Ed25519/EdDSA），本服务仅验证不签发

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `GET /healthz` | 进程存活 | 200 `{status:"ok",service:"notice",timestamp}` | stable | `notice/v1.yaml` |
| `GET /readyz` | 已注入 Pinger | 200 或 503（不泄露底层错误） | stable | 同上 |
| `GET /api/v1/announcements` | Bearer | 200 `{items,total}`；401 | stable | 同上 |
| `GET /api/v1/announcements/{id}` | Bearer | 200 announcement；404 | stable | 同上 |
| `GET /api/v1/changelogs` | Bearer | 200 `{items,total}` | stable | 同上 |
| `GET /api/v1/changelogs/{id}` | Bearer | 200 changelog；404 | stable | 同上 |
| `GET /api/v1/notifications` | Bearer | 200 当前用户通知 `{items,total}` | stable | 同上 |
| `GET /api/v1/notifications/unread-count` | Bearer | 200 `{count}` | stable | 同上 |
| `POST /api/v1/notifications/{id}/read` | Bearer | 204 幂等；404 | stable | 同上 |
| `POST /api/v1/notifications/read-all` | Bearer | 204 幂等 | stable | 同上 |

通知 `action` 为 nullable JSONB：`null` 表示纯信息性；非空时为 `{type:"link",label,href}`。客户端必须按数据渲染，未知 `type` 应优雅忽略。广播通知使用 `models.BroadcastUserID` sentinel row 表示，用户侧列表/未读/标记已读会把该 sentinel 与当前用户 ID 一并纳入；当前广播已读状态是共享的，未来如需 per-user broadcast read state 应改为异步展开或独立读状态表。响应统一 `{error:{code,message}}`，不泄露 PG/DSN。所有响应 `Cache-Control: no-store`。

## 鉴权

Ed25519 (EdDSA) JWT 本地验证。`internal/jwtverifier` 从 PKIX PEM 公钥文件加载 Auth 公钥，校验 EdDSA 签名、iss/aud/exp；`sub` 为 Auth users.id。已知 trade-off：15min TTL 窗口内 revoked token 仍可用（与 Executor jwtverifier 一致）。中间件对未通过验证的请求返回协议原生 401，不泄露具体失败项。

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 变更后验证 |
|---|---|---|---|---|
| 依赖 | `@tokenmp/contracts` | notice OpenAPI 契约唯一事实来源 | 设计时契约文档（本服务未接入 oapi-codegen 生成；前端类型手动对齐） | `pnpm --filter @tokenmp/contracts lint/test` |
| 依赖 | Auth Service | JWT 公钥（共享 PEM 文件） | 部署时文件注入，非源码 import、非数据库共享 | `go test ./internal/jwtverifier/...` |
| 依赖 | PostgreSQL（库 `tokenmp_biz`） | 公告/通知/changelog 持久化 | GORM + SQL | migration 周期 |
| 被依赖 | `apps/web` | 公告/通知/changelog API | HTTP + Bearer | 前端 typecheck + Playwright |

## 开发与验证

```bash
cd services/notice

# 格式
go vet ./...

# 单元测试（无需数据库）
go test ./...

# 构建
go build ./...
```

本机默认只跑单元测试（config/server/jwtverifier，使用 fake store + 生成密钥，不依赖 DB）。真实 migration 周期与集成测试在 CI 中依托临时 `postgres:17-alpine` 运行（与 Auth 一致）。

## 模块边界

- 允许访问：`packages/*` 公开入口（契约文档）。
- 禁止访问：其他 service 私有源码、其他 service 的数据库（`tokenmp_auth`）。
- `notifications.user_id` 是对 Auth users.id 的**松引用**（跨库无 FK）。
- 配置和环境变量：`NOTICE_DATABASE_URL`（必需，path 必须为 `/tokenmp_biz`）、`NOTICE_JWT_PUBLIC_KEY_FILE`（必需）、`NOTICE_HTTP_ADDR`、`NOTICE_JWT_ISSUER`、`NOTICE_JWT_AUDIENCE`、`NOTICE_LOG_LEVEL/FORMAT`、`NOTICE_SHUTDOWN_TIMEOUT`、`NOTICE_DB_*`。
- 数据库：`tokenmp_biz`（独立于 `tokenmp_auth`）。

## DO NOT

- **DO NOT** AutoMigrate —— schema 由版本化 SQL migration 管理。
- **DO NOT** 在客户端硬编码通知 action 跳转目标 —— action 由数据驱动（type/label/href）。
- **DO NOT** 把 notice 做成跨程序通用通知平台 —— 只服务当前项目。
- **DO NOT** 共享或直接访问 Auth 数据库 —— 仅通过 JWT 公钥验证身份。
