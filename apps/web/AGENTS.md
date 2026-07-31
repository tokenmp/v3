# apps/web

> 作用域：`apps/web`。继承仓库根目录 `AGENTS.md` 与 `apps/AGENTS.md`。

## 模块职责

- 负责：TokenMP 用户前端（认证流程 + 用户 Panel）。
  - 认证：登录、注册、忘记密码（占位）。
  - Panel：概览、API 密钥管理、请求日志、公告/通知（占位）。
  - 账户操作整合到右上角用户下拉菜单：修改密码（弹窗）、登出所有设备、退出登录（均带确认）。
- 不负责：Landing 公开页、定价、文档、Admin Dashboard、模型可视化、Billing、Bot Keys。
- 所有者：前端。

## 必读文档

- 方案：`docs/plans/panel-app.md`
- 设计 token：`packages/ui-tokens/AGENTS.md`（CSS 变量 + tailwind + shadcn integration）
- Auth 契约：`packages/contracts/openapi/auth/v1.yaml`
- 旧版参考（非本仓库）：july `panel-sidebar`、executor `bottom-nav` / `header`。

## 技术栈

Next.js 16 (App Router) + React 19 + Tailwind CSS v4 + `@tokenmp/ui-tokens` + shadcn 风格组件（手写，非 radix CLI）+ Zustand (auth/sidebar store, persist) + TanStack React Query v5 + react-hook-form + zod v4 + sonner + lucide-react。

## 对外能力与返回契约

前端应用，无对外导出。页面路由：

| 路由 | 说明 |
|---|---|
| `/login` `/register` `/forgot-password` | 认证页（auth-shell 两栏布局） |
| `/panel` | 概览 |
| `/panel/keys` `/panel/requests` | API 密钥 / 请求日志 |
| `/panel/announcements` `/panel/notifications` `/panel/changelogs` | 公告 / 通知 / 版本日志 |

## 认证与数据层

- Auth store：`src/lib/auth.ts`（纯内存 Zustand；access token 与用户信息不持久化，启动时清理旧 `tokenmp-auth` localStorage）。refresh token 只由同源 `/api/auth/session/*` Route Handler 通过 `Secure; HttpOnly; SameSite=Strict` cookie 持有并轮换，绝不暴露给浏览器 JS。
- API client：`src/lib/api/core.ts`（fetch wrapper，自动注入内存 Bearer，401 通过同源 HttpOnly session cookie 自动 refresh 一次）。
- `src/lib/api/auth.ts` 默认走 **mock auth**（`mock-auth.ts`），无需后端即可登录。设 `NEXT_PUBLIC_USE_MOCK_AUTH=0` 切回真实 fetch 客户端。
- Mock 凭据（仅 mock 模式）：`demo@tokenmp.cn` / `demo1234`（user）；`admin@tokenmp.cn` / `admin1234`（admin）。任意邮箱 + 12 位以上密码可注册。
- 真实后端凭据（dev 部署）：`demo@tokenmp.cn` / `demo12345678`（user）；`admin@tokenmp.cn` / `admin12345678`（admin）。Auth 密码策略要求 12–128 runes。
- Panel 数据 API：`src/lib/api/user.ts` 已通过 Edge 对接 keys/requests/quota。`/panel` 概览必须把 Coding 请求额度与 Token 余额分开展示：Coding 单位为“次”，Token 单位为 `tokens`，套餐卡展示 planName、5小时/周/周期/token 总额、剩余、已用、进度和到期时间，不得把 Coding 套餐显示为 “tokens 流量”。当 `UserPlan.usageWindows` 存在时，Coding 套餐必须展示三个窗口进度：5小时滚动、周窗口（周一 08:00 北京时间 / Monday 00:00 UTC）、本周期总额度；缺失时降级为单个总进度。Admin 已移除独立“用户套餐”页面，套餐分配/续费/切换/撤销（开通/续费默认到期按 plan category：daily=1、weekly=7、monthly=30、quarterly=90、yearly=365 天）与 active coding user_plan 的 reset/bonus limit override 统一放在用户详情页：重置窗口起点或临时加额，不修改历史请求；同时可查看覆盖历史并软撤销生效中的 override。Provider/路由/账号后台支持供应商默认 context/max output/RPM/TPM、provider+model 路由覆盖、路由页按模型过滤并以 Provider 分组直接管理多个协议组合开关与多账号候选、账号级 RPM/TPM 与全局 softmax 选号策略配置（当前为配置入口，执行侧 enforcement 分后续批次接入）。请求日志列表按旧版信息顺序展示请求 ID 缩略、模型、协议（OpenAI/Responses/Anthropic + 流式/非流式）、状态、输入/输出/缓存 Token、TTFT、生成速度、总耗时、thinking 与时间；thinking 显示实际执行 effective effort，发生降级时显示“由 requested 降级”；管理日志额外展示 provider 与有界清洗后的 User-Agent，并由详情 trace 展示完整生命周期。
- Notice API：`src/lib/api/notice.ts`（`noticeApi`）对接 Notice Service（`packages/contracts/openapi/notice/v1.yaml`）。公告/changelog/通知，默认 mock（`NEXT_PUBLIC_USE_MOCK_NOTICE` 默认启用），`NEXT_PUBLIC_USE_MOCK_NOTICE=0` 切真实 fetch。通知 `action` 由通用 `NotificationAction` 组件数据驱动渲染，不写死跳转。
- 契约来源：Auth 使用 `packages/contracts/openapi/auth/v1.yaml`；Panel 请求日志使用 `packages/contracts/openapi/api/v1.yaml`（前端类型手动对齐，未生成 TS client）。

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 变更后验证 |
|---|---|---|---|---|
| 依赖 | `@tokenmp/ui-tokens` | design tokens + tailwind/shadcn integration | workspace import (CSS) | `pnpm build` |
| 依赖 | `@tokenmp/contracts` | auth OpenAPI 契约参考 | 类型对齐（非 runtime import） | typecheck |

## 开发与验证

```bash
pnpm --filter @tokenmp/web dev      # http://localhost:3100
pnpm --filter @tokenmp/web typecheck
pnpm --filter @tokenmp/web lint
pnpm --filter @tokenmp/web build    # output: standalone
```

- 最小验证：typecheck + lint + build 全过。
- 视觉验证：Playwright（桌面 1440 + 移动 390），确认表格表头、布局不错乱、键盘可访问性。

## 部署

`Dockerfile` 基于预构建 standalone 产物打包（不在镜像内 build）。dev 服务器以 Docker 容器运行（`node:22-alpine`），映射端口到宿主。

## 模块边界

- 允许访问：`packages/*` 公开入口。
- 禁止访问：service 私有源码、服务数据库。
- 配置和环境变量：`NEXT_PUBLIC_API_BASE`（浏览器 auth service base，默认空=同源）、`AUTH_API_BASE`（仅服务端 session BFF 的 auth service base；未设置时兼容回退 `NEXT_PUBLIC_API_BASE`）、`NEXT_PUBLIC_NOTICE_API_BASE`（notice service base，默认空=同源）、`NEXT_PUBLIC_USE_MOCK_AUTH`（默认启用 mock）、`NEXT_PUBLIC_USE_MOCK_NOTICE`（默认启用 mock）。真实部署经 nginx 反代同源：`API_BASE=/auth-api`、`NOTICE_API_BASE=/notice-api`。CSP `connect-src` 从公开 API/Biz/Notice base 构建 allowlist，并允许对应 SSE/WebSocket origin。
- 设计 token：CSS 变量统一来自 `@tokenmp/ui-tokens`，组件内不内联颜色 hex（`--brand-solid: #111827` 除外，与 logo 一致）。

## 焦点样式体系

全局 `*:focus-visible` 用双层 box-shadow ring（`--tmp-color-focus-ring` + offset）。组件按需附加：
- `focus-inset` / `focus-nav`：导航/菜单类，深背景 + 内描边（区别于 hover 浅、active 蓝）。

## DO NOT

- **DO NOT** 直接在组件里硬编码数据——经 `api.*` 层（mock 或真实）。
- **DO NOT** 用 `hsl(var(--primary))`——token 是完整颜色值（oklch/hex），用 `color-mix(in oklch, ...)`。
- **DO NOT** 在 route group `(panel)` 放需要 `/panel/*` URL 的页面——route group 不产生 URL 段，用真实 `panel/` 目录。
- **DO NOT** 在 client component 用 `useSearchParams()` 而不包 Suspense（build prerender 报错）。
