# TokenMP v3 Panel App 方案

- 状态：draft — 待用户确认后实施
- 创建日期：2026-07-24
- 最后更新：2026-07-24
- 关联分支：none
- 负责人：Agent

## 目标

在 v3 monorepo 中创建首个前端应用 `apps/web`，实现用户认证流程（登录/注册/忘记密码）和用户 Panel 基础功能，为后续 Landing、Admin 等页面奠定技术基础。

## 范围

认证流程 + 用户 Panel 骨架 + 基础数据页面。

## 非目标

Landing 公开页、定价、文档、Admin Dashboard、模型可视化、Billing、Bot Keys。

---

## 1. 目标与边界

### 本次实施

- 注册页（email + password）
- 登录页（email + password）
- 忘记密码页（email → 验证码 → 重置密码）
- 用户 Panel 布局（侧边栏 + header + 移动端底部导航）
- Panel 首页 Overview（账户信息 + 最近请求摘要）
- Panel API Keys 管理
- Panel 请求日志列表
- Panel 设置（修改密码）

### 不在本次范围

- Landing 公开页（首页、定价、文档、工具接入指南）
- OneKey / 兑换码 / 套餐购买
- Admin Dashboard
- 模型列表/路由可视化
- Billing / 用量统计图表
- Bot Keys / CC Switch

---

## 2. 技术选型

| 层面 | 选型 | 理由 |
|------|------|------|
| 框架 | **Next.js 16** (App Router) | 旧版 executor/web 已验证；SSR/RSC 支持；与 v3 Node.js toolchain 兼容 |
| React | **React 19** | Next.js 16 要求 |
| 样式 | **Tailwind CSS v4** + `@tokenmp/ui-tokens` | v3 已有 token package + Tailwind integration |
| 组件库 | **shadcn/ui** (New York style) | 旧版已验证；与 Tailwind v4 + Radix 兼容；`@tokenmp/ui-tokens/shadcn` 已提供变量映射 |
| 状态管理 | **Zustand** (auth store) | 轻量、旧版已验证、persist middleware |
| 数据获取 | **TanStack React Query v5** | 旧版已验证、缓存/重试/乐观更新 |
| 表单 | **react-hook-form + zod v4** | 旧版已验证、类型安全 |
| 通知 | **sonner** | 轻量 toast |
| 图标 | **lucide-react** | 旧版已使用 |
| 包管理 | **pnpm** workspace | v3 标准 |
| 构建 | **Turborepo** task pipeline | v3 标准 |

### 不引入的依赖

- `next-themes`：首批不做暗色模式切换 UI，通过 `data-theme` 手动控制
- `recharts`：首批无图表需求
- `shiki`：首批无代码高亮需求
- `nuqs`：首批无 URL query state 需求

---

## 3. 目录结构

```
apps/web/
├── AGENTS.md                    # 模块级规范
├── package.json
├── tsconfig.json
├── next.config.ts
├── postcss.config.mjs
├── tailwind.config.ts           # v4 实际不需要此文件，用 CSS @theme
├── src/
│   ├── app/
│   │   ├── layout.tsx           # Root layout（字体、providers、全局 CSS）
│   │   ├── globals.css          # @import tokens + tailwind + shadcn
│   │   ├── (auth)/
│   │   │   ├── layout.tsx       # Auth shell 居中布局
│   │   │   ├── login/
│   │   │   │   └── page.tsx
│   │   │   ├── register/
│   │   │   │   └── page.tsx
│   │   │   └── forgot-password/
│   │   │       └── page.tsx
│   │   └── (panel)/
│   │       ├── layout.tsx       # Panel 布局（sidebar + header + bottom-nav）
│   │       ├── page.tsx         # Overview 首页
│   │       ├── keys/
│   │       │   └── page.tsx
│   │       ├── requests/
│   │       │   └── page.tsx
│   │       └── settings/
│   │           └── page.tsx
│   ├── components/
│   │   ├── ui/                  # shadcn 生成组件（button, input, label, card, ...）
│   │   ├── auth-shell.tsx       # 认证页面统一壳
│   │   ├── sidebar.tsx          # 桌面侧边栏
│   │   ├── header.tsx           # 顶部栏（用户头像、登出）
│   │   ├── bottom-nav.tsx       # 移动端底部导航
│   │   └── providers.tsx        # QueryProvider + ThemeProvider
│   ├── hooks/
│   │   └── use-auth-guard.ts    # 登录状态守卫 hook
│   ├── lib/
│   │   ├── api/
│   │   │   ├── core.ts          # HTTP client 基础（fetch wrapper、token 注入、refresh）
│   │   │   ├── auth.ts          # 认证 API mixin
│   │   │   └── user.ts          # 用户 API mixin
│   │   ├── auth.ts              # Zustand auth store
│   │   ├── api-error.ts         # 错误码 → 中文映射
│   │   └── validators.ts        # zod schema（email、password policy）
│   └── types/
│       └── index.ts             # User、TokenResponse 等类型
└── public/
    └── favicon.ico
```

---

## 4. 认证架构

### 4.1 Auth Store (Zustand + persist)

```ts
interface AuthState {
  accessToken: string | null;
  refreshToken: string | null;
  user: User | null;
  isAuthenticated: boolean;
  isHydrated: boolean;

  login: (tokens: TokenResponse, user: User) => void;
  logout: () => void;
  setTokens: (access: string, refresh: string) => void;
  updateUser: (user: User) => void;
}
```

- 持久化到 `localStorage` key `tokenmp-auth`
- `isHydrated` 标记防止 SSR/CSR 水合闪烁

### 4.2 API Client

- `fetch` wrapper，自动注入 `Authorization: Bearer <access_token>`
- 401 时自动尝试 `POST /api/v1/auth/refresh`（仅一次）
- refresh 失败 → 派发 `tokenmp:auth-expired` 事件 → auth store logout → redirect `/login?reason=session_expired`
- 所有 API 响应走统一 `Error` 结构解析

### 4.3 Token 存储策略

旧版用 localStorage，v3 首批保持一致。后续可评估 httpOnly cookie 方案。

---

## 5. Auth API 对接

基于 `packages/contracts/openapi/auth/v1.yaml` 已实现的端点：

| 功能 | 端点 | 前端页面 |
|------|------|---------|
| 注册 | `POST /api/v1/auth/register` | `/register` |
| 登录 | `POST /api/v1/auth/login` | `/login` |
| 刷新 token | `POST /api/v1/auth/refresh` | API client 自动 |
| 登出 | `POST /api/v1/auth/logout` | Header 登出按钮 |
| 当前用户 | `GET /api/v1/auth/me` | Panel 初始化时 |
| 修改密码 | `PUT /api/v1/auth/password` | `/panel/settings` |

**注意**：Auth 契约当前没有「忘记密码」端点。旧版 executor/web 有验证码流程（send-code → reset），但 v3 Auth 尚未实现。方案：

1. 首批在前端预留 `/forgot-password` 路由和 UI
2. 后端补齐 `POST /api/v1/auth/forgot-password/request-code` + `POST /api/v1/auth/forgot-password/reset` 后对接
3. 或先显示「请联系管理员重置」占位文案

---

## 6. 页面设计

### 6.1 登录页 `/login`

```
┌─────────────────────────────────┐
│         TokenMP Logo            │
│                                 │
│  ┌───────────────────────────┐  │
│  │ 邮箱                      │  │
│  │ [________________________]│  │
│  │                           │  │
│  │ 密码                 👁   │  │
│  │ [________________________]│  │
│  │                           │  │
│  │ □ 记住我        忘记密码？ │  │
│  │                           │  │
│  │ [     登录 TokenMP      ] │  │
│  │                           │  │
│  │   还没有账号？立即注册     │  │
│  └───────────────────────────┘  │
└─────────────────────────────────┘
```

- AuthShell 居中卡片
- 密码可见性切换
- 错误信息内联显示（error code → 中文）
- loading 状态禁用按钮

### 6.2 注册页 `/register`

```
┌─────────────────────────────────┐
│         TokenMP Logo            │
│                                 │
│  ┌───────────────────────────┐  │
│  │ 邮箱                      │  │
│  │ [________________________]│  │
│  │                           │  │
│  │ 密码                      │  │
│  │ [________________________]│  │
│  │ 密码强度指示条            │  │
│  │                           │  │
│  │ 确认密码                  │  │
│  │ [________________________]│  │
│  │                           │  │
│  │ [     创建账号          ] │  │
│  │                           │  │
│  │   已有账号？立即登录       │  │
│  └───────────────────────────┘  │
└─────────────────────────────────┘
```

- 密码策略：12-128 字符，显示强度指示
- 前端 zod 校验与后端一致
- 注册成功 → 自动跳转登录页（Auth 契约不自动登录）

### 6.3 忘记密码 `/forgot-password`

```
步骤 1：输入邮箱 → 发送验证码
步骤 2：输入验证码 + 新密码 → 重置
```

- 首批若后端未实现，显示「功能开发中，请联系管理员」占位

### 6.4 Panel 布局

```
┌────────┬────────────────────────────┐
│        │  Header  [用户 ▾] [登出]   │
│ Side-  ├────────────────────────────┤
│ bar    │                            │
│        │        Main Content        │
│ 首页   │                            │
│ 密钥   │                            │
│ 请求   │                            │
│ 设置   │                            │
│        │                            │
├────────┴────────────────────────────┤
│ [首页] [密钥] [请求] [设置]   ← 移动 │
└─────────────────────────────────────┘
```

- 桌面：左侧固定 sidebar (w-60) + 顶部 header + 主内容区可滚动
- 移动：隐藏 sidebar，底部 BottomNav 固定
- 响应式断点：`md` (768px)

### 6.5 Panel 首页 Overview `/panel`

```
┌──────────────────────────────────────┐
│  概览                                │
│                                      │
│  ┌──────────┐ ┌──────────┐ ┌──────┐ │
│  │ 账户     │ │ 配额     │ │ 状态 │ │
│  │ user@... │ │ 余额: .. │ │ 正常 │ │
│  │ 注册: .. │ │ 已用: .. │ │      │ │
│  └──────────┘ └──────────┘ └──────┘ │
│                                      │
│  最近请求                            │
│  ┌──────────────────────────────────┐│
│  │ #req-abc  gpt-4  200  1.2s      ││
│  │ #req-def  claude  200  0.8s     ││
│  │ ...                              ││
│  └──────────────────────────────────┘│
└──────────────────────────────────────┘
```

- 账户卡片：邮箱、角色、注册时间
- 配额卡片：当前余额（如有）、已用 token
- 最近请求：最近 5 条请求摘要（需后端 API 支持）
- 若后端 API 未就绪，使用 mock data 占位

### 6.6 API Keys `/panel/keys`

```
┌──────────────────────────────────────┐
│  API 密钥                [+ 创建密钥] │
│                                      │
│  ┌──────────────────────────────────┐│
│  │ sk-***abc  创建于 2026-07-20    ││
│  │           [复制] [撤销]          ││
│  ├──────────────────────────────────┤│
│  │ sk-***def  创建于 2026-07-18    ││
│  │           [复制] [撤销]          ││
│  └──────────────────────────────────┘│
└──────────────────────────────────────┘
```

- 密钥脱敏显示（前缀 + 后 4 位）
- 创建后一次性显示完整密钥（toast 复制）
- 撤销需确认对话框

### 6.7 请求日志 `/panel/requests`

```
┌──────────────────────────────────────┐
│  请求日志              [刷新] [筛选]  │
│                                      │
│  ┌──────────────────────────────────┐│
│  │ 时间       模型     状态  耗时   ││
│  │ 07-24 14:00 gpt-4   200   1.2s  ││
│  │ 07-24 13:55 claude  200   0.8s  ││
│  │ ...                              ││
│  └──────────────────────────────────┘│
│                                      │
│  [1] [2] [3] ... [下一页]           │
└──────────────────────────────────────┘
```

- 分页列表
- 状态颜色编码（2xx 绿、4xx 黄、5xx 红）
- 点击可查看详情（后续迭代）

### 6.8 设置 `/panel/settings`

```
┌──────────────────────────────────────┐
│  设置                                │
│                                      │
│  修改密码                            │
│  ┌──────────────────────────────────┐│
│  │ 当前密码 [____________________]  ││
│  │ 新密码   [____________________]  ││
│  │ 确认密码 [____________________]  ││
│  │ [保存修改]                       ││
│  └──────────────────────────────────┘│
│                                      │
│  登出所有设备                         │
│  [登出全部会话]                      │
└──────────────────────────────────────┘
```

---

## 7. 依赖图

```
apps/web
  ├── @tokenmp/ui-tokens          (design tokens + tailwind + shadcn integration)
  ├── @tokenmp/contracts           (OpenAPI 契约，用于类型生成或参考)
  ├── next, react, react-dom
  ├── tailwindcss, @tailwindcss/postcss
  ├── zustand
  ├── @tanstack/react-query
  ├── react-hook-form, @hookform/resolvers, zod
  ├── sonner
  ├── lucide-react
  ├── clsx, tailwind-merge, class-variance-authority
  └── shadcn/ui components (via CLI init)
```

依赖方向合规：`apps/web → packages/*`，不反向。

---

## 8. 实施步骤

### Phase 1: 骨架搭建

1. 创建 `apps/web/` 目录 + `package.json` + `tsconfig.json`
2. 初始化 Next.js 16（App Router, TypeScript, Tailwind CSS v4）
3. 配置 pnpm workspace + Turborepo task
4. 导入 `@tokenmp/ui-tokens` 的 CSS（index.css + tailwind + shadcn integration）
5. 运行 `shadcn init` 生成基础组件（New York style, CSS variables mode）
6. 创建 root layout + globals.css
7. 创建 `AGENTS.md`
8. 验证 `pnpm --filter @tokenmp/web dev` 可启动

### Phase 2: 认证基础设施

1. 创建 `src/lib/auth.ts` — Zustand auth store
2. 创建 `src/lib/api/core.ts` — fetch wrapper + token 注入 + refresh
3. 创建 `src/lib/api/auth.ts` — 登录/注册/me/登出 API
4. 创建 `src/lib/api-error.ts` — 错误码映射
5. 创建 `src/lib/validators.ts` — zod schema
6. 创建 `src/types/index.ts` — User, TokenResponse 类型
7. 创建 `src/components/providers.tsx` — QueryProvider
8. 创建 `src/hooks/use-auth-guard.ts`

### Phase 3: 认证页面

1. 创建 `src/components/auth-shell.tsx`
2. 创建 `/login` 页面
3. 创建 `/register` 页面
4. 创建 `/forgot-password` 页面（占位或对接后端）
5. shadcn 组件按需添加：button, input, label, checkbox, card

### Phase 4: Panel 布局

1. 创建 `src/components/sidebar.tsx`
2. 创建 `src/components/header.tsx`
3. 创建 `src/components/bottom-nav.tsx`
4. 创建 `(panel)/layout.tsx` — 组装 sidebar + header + bottom-nav
5. 添加 auth guard（未登录跳转 /login）

### Phase 5: Panel 页面

1. Overview 首页 — 账户卡片 + 请求摘要
2. API Keys 页 — 列表 + 创建 + 撤销
3. 请求日志页 — 分页列表
4. 设置页 — 修改密码

### Phase 6: 收尾

1. 更新根 `AGENTS.md` apps 清单
2. 更新 `apps/AGENTS.md` 应用清单
3. `pnpm lint / typecheck / test / build` 全通过
4. Turborepo pipeline 配置
5. （可选）Dockerfile

---

## 9. 需要确认的事项

1. **忘记密码**：v3 Auth 尚未实现此端点。首批是预留 UI + 占位文案，还是先不建此页面？
2. **请求日志 API**：v3 Auth 契约没有请求日志端点。Panel 请求列表需要新的 API（可能在未来的 API 服务中）。首批是否用 mock data 占位？
3. **API Keys 管理**：v3 Auth 契约没有 API Key CRUD 端点。同上，首批是否用 mock data 占位？
4. **配额/余额**：需要 Quota 服务 API。首批是否用 mock data 占位？
5. **暗色模式**：首批是否需要 theme toggle，还是固定 light？
6. **验证码（Captcha）**：旧版有阿里云验证码。v3 是否需要？如需要，需配置。
7. **App 名称**：`apps/web` 还是 `apps/panel`？

---

## 10. 前置条件

- pnpm workspace 和 Turborepo 已配置
- `@tokenmp/ui-tokens` package 可用
- `@tokenmp/contracts` Auth OpenAPI 契约可用
- `services/auth` 可本地运行或有远程开发环境

## 11. 验证

1. `pnpm --filter @tokenmp/web dev` 启动无错误
2. 可访问 `/login`、`/register`、`/forgot-password`
3. 登录后跳转 `/panel`，显示 overview
4. 未登录访问 `/panel` 跳转 `/login`
5. 侧边栏/底部导航路由切换正常
6. `pnpm lint / typecheck / build` 全通过

## 12. 风险与回滚

| 风险 | 缓解 |
|------|------|
| Auth 契约无 forgot-password 端点 | 前端预留 UI + 占位文案 |
| Panel 数据 API 未实现 | mock data 占位 |
| shadcn init 可能覆盖已有 CSS | 备份 globals.css 后执行 |
| Next.js 16 与 pnpm workspace 兼容性 | 验证 turbo task 正常运行 |

回滚：删除 `apps/web/` 目录和 workspace 配置即可，不影响其他模块。

## 13. 决策与阻塞项

- [ ] 忘记密码：占位 vs 等后端？
- [ ] 请求日志 / API Keys / 配额：mock data vs 等 API？
- [ ] 暗色模式 toggle：首批是否需要？
- [ ] 验证码（Captcha）：首批是否集成？
- [ ] App 目录名：`web` vs `panel`？

## 14. 完成后的文档同步

- 更新根 `AGENTS.md` apps 清单
- 更新 `apps/AGENTS.md` 应用清单
- 创建 `apps/web/AGENTS.md`
- 更新 `README.md` workspace 模块列表

---

## 15. 与旧版差异

| 方面 | 旧版 executor/web | v3 apps/web |
|------|-------------------|-------------|
| 包管理 | pnpm (独立) | pnpm workspace (monorepo) |
| 设计 Token | 内联 CSS 变量 | `@tokenmp/ui-tokens` package |
| Auth API | 自定义 REST | OpenAPI 契约驱动 |
| 状态管理 | zustand | zustand (保持一致) |
| 后端 | 单体 Node.js | Go 微服务 (auth + executor) |
| 路由组 | (panel)/ + /admin/ | (auth)/ + (panel)/ |
| 测试 | Node test runner | 待定 (Vitest 或 Playwright) |
