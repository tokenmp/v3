# API Service (Edge/BFF)

> 作用域：`services/api/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 模块职责

TokenMP V3 分层架构的**入口层**。客户端所有请求先到 Edge/BFF，由它完成：
1. **客户端身份验证**（JWT EdDSA/Ed25519 本地验证，用 Auth 公钥）
2. **配额预留/结算**（调 Billing Service reserve→finalize/release）
3. **转发执行**（反向代理到 Executor，注入 Edge 服务级 Bearer token）
4. **请求日志**（可选推 Logging Service；executor 侧 logsink 已覆盖执行内部事件）

Edge/BFF **不做**：模型路由、协议转换、上游转发（这些在 Executor）、记账落库（在 Billing Service）。

## 当前实施状态（骨架）

- `cmd/api/main.go`：入口，加载 `API_*` env、组装 deps、graceful shutdown。
- `internal/config`：env 配置（`API_EXECUTOR_URL`必填、`API_EXECUTOR_TOKEN`必填、`API_BILLING_URL`/`API_LOGGING_URL`可选、`API_JWT_PUBLIC_KEY_FILE`可选、`API_JWT_ISSUER`/`API_JWT_AUDIENCE`默认）。
- `internal/identity`：JWT 验证中间件（EdDSA/Ed25519，本地验，提取 subject/role 到 context；`API_JWT_PUBLIC_KEY_FILE` 空时 noop verifier dev-only；`NewVerifier` + `Middleware` + `FromContext`）。
- `internal/quota`：Billing Service 客户端（`Manager` 接口，`Reserve`/`Finalize`/`Release`；`billingURL` 空时 noop；禁 redirect，10s timeout，`ErrQuotaUnavailable` 不泄漏 URL）。
- `internal/proxy`：反向代理转发到 executor（注入 `Bearer <edge-token>`，`ErrorHandter` 返回 502 JSON）。
- `internal/logging`：Logging Service 只读 HTTP 客户端（`ListLogs`/`GetLog`/`GetStats`；`loggingURL` 空时 `ErrUnavailable`，404 区分为 `NotFound`，禁 redirect、1 MiB 响应体限、不泄漏 URL）。
- `internal/billing`：Billing Service 只读查询 HTTP 客户端（`ListPlans`/`ListUserPlans`/`GetBalance`；与 `internal/quota` 分离，后者负责 reserve/finalize/release 写入路径）。下游 `Balance`/`Plan`/`UserPlan` 为 snake_case DTO，Edge facade 映射为契约 camelCase。
- `internal/settings`：用户设置进程内内存存储（`Get`/`Snapshot`，默认 preferredBilling="coding"/fallbackEnabled=false；`Snapshot` 用可选指针表达局部更新，支持把 bool 显式设为 false）。无持久化，生产化后可替换。
- `internal/panel`：Panel 业务查询 handler（`ListPlans`/`ListUserPlans`/`GetUserBalance`/`ListRequestLogs`/`GetRequestLog`/`GetRequestLogStats`/`GetUserSettings`/`UpdateUserSettings`）。聚合 logging+billing+settings，以 OpenAPI 契约形状返回；金额/配额用十进制字符串。防越权：`GetRequestLog` 按身份 subject 校验日志归属（admin 可放宽）。Plan 的 int64 id 经 `int64ToUUID` 确定性映射为契约 UUID，不暴露自增序号。
- `internal/app`：chi 路由组装（`/healthz` 匿名、`/api/v1/plans` 公开、`/api/v1/{user,request-logs,keys}` 身份、`/v1/*` 身份→配额→代理；`quotaMiddleware` reserve→forward→finalize/release）。
- `internal/transport/healthz`：health check handler。

## 请求流

```
# 模型执行请求
client → identity.Middleware (JWT verify) → quotaMiddleware (reserve)
  → proxy (forward to executor, inject Bearer token)
  → response → quotaMiddleware (finalize if 2xx/3xx, release if error)

# Panel 业务查询请求（不经配额）
client → identity.Middleware (JWT verify) → panel handler
  → (logging.Client | billing.Client | settings.Store) → 契约 JSON
```

## 验证

```bash
cd services/api
go test ./...
go test -race ./...
```

- config：defaults、missing required、invalid URL、optional URLs
- identity：JWT valid/expired/wrong-issuer/empty、noop verifier、middleware allow/reject
- quota：noop、reserve→finalize→release、error、unreachable
- proxy：forward+token、502 on unreachable
- app：全链路（auth→quota→proxy→finalize）、auth reject 401、release on 502、healthz anonymous、quota unavailable 503
- panel：套餐列表过滤 image/free、余额降级返 0、用户套餐余额填充、请求日志分页+status 映射、日志详情越权拦截、stats 聚合、settings PATCH 局部更新持久化、未认证 401、下游不可用 503

## 待实现

- Logging 推送（Edge 侧日志 sink，当前 executor logsink 已覆盖执行事件）
- 客户端速率限制
- 流式响应的 quota finalize（当前在 response 完成时 finalize，流式可能需要 stream-aware）
- API Key 验证（当前仅 JWT；后续调 Auth Service `/api/v1/auth/verify-key`）
- cancel-risk 评估

## 约束

- **DO NOT** 在 Edge 执行模型调用或协议转换——转发给 Executor。
- **DO NOT** 让 Edge 直连任何数据库——通过 Billing/Logging Service HTTP API。
- **DO NOT** 泄漏下游服务 URL 到错误响应。
- **DO NOT** 跳过身份验证（dev-only noop verifier 仅在 `API_JWT_PUBLIC_KEY_FILE` 空时）。
- Executor token 是服务级密钥——不得提交到仓库。

## 文档维护

请求流、中间件链、安全策略变化时，同步维护本文件与 `services/AGENTS.md`。
