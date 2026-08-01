# API Service (Edge/BFF)

> 作用域：`services/api/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 模块职责

TokenMP V3 分层架构的**入口层**。客户端所有请求先到 Edge/BFF，由它完成：
1. **客户端身份验证**（JWT EdDSA/Ed25519 本地验证，用 Auth 公钥）
2. **配额预留/结算**（调 Billing Service reserve→finalize/release/mark-pending/reconcile）
3. **转发执行**（反向代理到 Executor，注入 Edge 服务级 Bearer token）
4. **请求日志**（可选推 Logging Service；executor 侧 logsink 已覆盖执行内部事件）

Edge/BFF **不做**：模型路由、协议转换、上游转发（这些在 Executor）、记账落库（在 Billing Service）。

## 当前实施状态（骨架）

- `cmd/api/main.go`：入口，加载 `API_*` env、组装 deps、graceful shutdown。
- `internal/config`：env 配置（`API_EXECUTOR_URL`必填、`API_EXECUTOR_TOKEN`必填、`API_BILLING_URL`/`API_LOGGING_URL`/`API_AUTH_URL`/`API_CONFIG_SERVICE_URL`可选；Config Service 服务间 admin 授权共享密钥有两个互斥来源：生产源 `API_CONFIG_SERVICE_TOKEN_FILE`（从 regular 文件加载，`API_CONFIG_ADMIN_PROXY_ENABLED=true` 时必填，供 Compose 只读 secret mount）与 dev/test 直源 `API_CONFIG_SERVICE_TOKEN`（明文 env，仅供本地开发/no-database 单测，禁止进入生产 Compose env），二者同时设置 fail-fast；file 源采用项目既有 secret-file 安全模式：trim、Lstat 拒 symlink/non-regular、post-open `SameFile` TOCTOU 守卫、8 KiB 大小上限、`LimitReader`、严格 UTF-8、拒 NUL/换行、空值拒，失败返回不泄漏 path/content 的稳定 sentinel；`API_JWT_PUBLIC_KEY_FILE`必填；仅本地开发/测试显式设置 `API_ALLOW_NOOP_AUTH=true` 时可省略、`API_JWT_ISSUER`/`API_JWT_AUDIENCE`默认）。
- `internal/identity`：JWT 验证中间件（EdDSA/Ed25519，本地验，提取 subject/role 到 context；缺少公钥 fail-fast；仅 `NewNoopVerifier` 显式 opt-in 用于本地开发/测试；`NewVerifier` + `Middleware` + `FromContext`）。
- `internal/quota`：Billing Service 客户端（`Manager` 接口，`Reserve`/`Finalize(usageKnown)`/`Release`/`MarkPending`/`GetStatus`；`billingURL` 空时 noop；禁 redirect，10s timeout，`ErrQuotaUnavailable` 不泄漏 URL；Billing 429→`ErrQuotaExceeded`（Edge 返 429），409→`ErrConflict`（Edge 视为已结算不重试），404→`ErrNotFound`）。已移除旧的 token 1-token fallback。**信封解包**：所有 2xx 业务响应（Reserve、GetStatus，及 Finalize/Release/MarkPending 有 body 时）先经 `packages/go/httpresp.UnwrapData` 解 Billing 真实 `{code,data,message}` 信封再严格 decode 目标 DTO；malformed/nonzero code/shape 异常、body read error、意外空 body 一律归一 `ErrQuotaUnavailable`，不泄漏 body/URL；**仅** HTTP 204 No Content 允许空 body 直接成功，任何其他 2xx 必须读取成功、非空且 UnwrapData+decode 为合法 envelope（即使 dst nil 也校验 envelope），Reserve/GetStatus 空 200 不返回空 DTO 成功。
- `internal/ratelimit`：共享 Redis token-bucket 限流中间件（IP bucket + subject bucket），复用 `packages/go/ratelimit`。仅在计量执行 POST（chat/messages/responses/images）前限流；health 与只读端点（如 GET `/v1/models`）不限流。IP bucket 在身份验证前（未认证洪流也按 IP 限流）；subject bucket 在身份验证后、quota/proxy 前。Redis 异常 fail-closed 503；超额 429 `rate_limited` + `Retry-After` + `Cache-Control: no-store`。key 用 HMAC-SHA256，不把 IP/subject 明文放 Redis 或日志。限流严格在 SSE commit 前完成，不得在 commit 后注入 JSON。
- `internal/proxy`：反向代理转发到 executor（服务 token 模式注入 `Bearer <edge-token>`，JWT passthrough 保留客户端 JWT；仅服务 token 模式注入已验证 subject 的 `X-User-ID` delegated assertion，passthrough 一律剥离该 header；`ErrorHandter` 返回 502 JSON）。
- `internal/logging`：Logging Service HTTP 客户端（读取：`ListLogs`/`GetLog`/`GetStats`；写入：`Ingest` 用于 Edge 收到执行请求时创建 `processing` 日志与 `received` 事件，并仅附带 512-byte、UTF-8/control-char 清洗后的 `User-Agent`，不采集其他客户端头；`loggingURL` 空时 `ErrUnavailable`，404 区分为 `NotFound`，禁 redirect，不泄漏 URL）。
- `internal/billing`：Billing Service HTTP 客户端（用户只读 `ListPlans`/`ListUserPlans`/`GetBalance`/`GetUsageWindows`；admin `RenewUserPlan`/`SwitchUserPlan` 透传续费/切换，`CreateLimitOverride`/`ListLimitOverrides`/`RevokeLimitOverride` 透传 reset/bonus 覆盖）。`ListUserPlans` 调 Billing `/v1/billing/users/{user_id}/plans`，返回全部当前 active 套餐及 planName/category/hourly/weekly/monthly/token limit 元数据；`GetUsageWindows` 调 `/v1/billing/users/{user_id}/usage-windows` 获取 active coding plan 的 hour5/weekly/period 用量窗口；与 `internal/quota` 分离，后者负责 reserve/finalize/release 写入路径。下游 `Balance`/`Plan`/`UserPlan`/`UsageWindow`/`LimitOverride` 为 snake_case DTO，Edge facade 映射为契约 camelCase。
- `internal/config`（客户端）/`internal/admin/config_handlers`：Config Service 透传客户端。`config.Client` 转发 admin CRUD 与 publish 生命周期请求到 Config Service。**请求头 allowlist**：唯一从客户端转发的是经过校验的 `If-Match`（乐观并发版本，经 `RequestMeta` 显式传入，仅接受小正整数，拒绝 CRLF/非数字注入）；`X-Admin-Token`（`API_CONFIG_SERVICE_TOKEN`）由客户端从配置密钥独占注入，调用方无法覆盖或提供；`Authorization`/`Cookie`/任意客户端头一概不转发。**响应头 allowlist**：只回传 `ETag` 与 `Cache-Control`（no-store），其余上游头（含敏感/内部头）一律丢弃；`Content-Type` 由 handler 显式设置。`CheckRedirect` 拒绝所有 redirect（返回 `ErrUseLastResponse`）防止 X-Admin-Token 跨 origin 泄漏，固定 15s timeout，transport 错误映射为 `ErrConfigUnavailable` 不泄漏 URL/host/token/body；4xx（412/409/404）透传 body 与 allowlist 头，不被掩为 502。`WithHTTPClient` 选项用于测试 TLS 信任库并重应用 redirect 拒绝+timeout 策略。publish 生命周期路由（drafts/publish/archive/revert/revisions/audit）经 Edge `/api/v1/admin/config/*` 透传，鉴权由 Edge `identity.RequireAdmin` + Config Service `adminauth` 双重把关。`API_CONFIG_ADMIN_PROXY_ENABLED=true` 时启动 fail-fast 要求有效 token（`API_CONFIG_SERVICE_TOKEN_FILE` 或 dev/test 的 `API_CONFIG_SERVICE_TOKEN`），且注册 admin CRUD 代理路由；false（默认）仅注册只读 models catalog（无 token 可用）。
- `internal/settings`：用户设置进程内内存存储（`Get`/`Snapshot`，默认 preferredBilling="coding"/fallbackEnabled=false；`Snapshot` 用可选指针表达局部更新，支持把 bool 显式设为 false）。无持久化，生产化后可替换。
- `internal/panel`：Panel 业务查询 handler（`ListPlans`/`ListUserPlans`/`GetUserBalance`/`ListRequestLogs`/`GetRequestLog`/`GetRequestLogStats`/`GetUserSettings`/`UpdateUserSettings`）。聚合 logging+billing+settings，以 OpenAPI 契约形状返回；金额/配额用十进制字符串。防越权：`GetRequestLog` 按身份 subject 校验日志归属（admin 可放宽）。Plan 的 int64 id 经 `int64ToUUID` 确定性映射为契约 UUID，不暴露自增序号。
- `internal/app`：chi 路由组装（`/healthz` 匿名、`/api/v1/plans` 公开、`/api/v1/{user,request-logs,keys}` 身份、`/v1/*` 身份→配额→代理；`quotaMiddleware` reserve→forward→finalize/release）。
- `internal/transport/healthz`：health check handler。

## 请求流

```
# 模型执行请求
client → identity.Middleware (JWT verify)
  → quotaMiddleware（仅 metered 执行 POST：chat/messages/responses/images；/v1/models 等目录查询只鉴权转发，不预留、不写 processing 日志）
  → 对 metered 请求：分配并透传 X-Request-ID；清洗并限制 User-Agent；异步写 processing + received；按用户 settings.preferredBilling 选择 billing_plan（coding 默认；token 时 reserve 0 token 以避免 reserve+charge 双扣，成功后从 Logging total_tokens/input+output 查询真实 usage 再 finalize；查不到时最小 fallback 1 token）；reserve
  → proxy (forward to executor, inject Bearer token)
  → executor 复用同一 request_id 并逐阶段 upsert 日志
  → response → quotaMiddleware `settleReservation`（committed-aware 结算协调器，见下）：
    - pre-commit failure（未提交字节 / 客户端断开未 commit）→ Release + 异步写 `client_cancelled`。
    - 已提交错误响应（≥400）→ Release（失败请求不计费）。
    - 已提交成功（2xx/3xx）：coding 计划 Finalize(1,0,known=true)；token 计划调 Logging `GetLog` **单次有界查询**（不轮询、不猜测）：`usage_status=final` → Finalize 实际 total_tokens；未知 / not-terminal / Logging 不可达 → `MarkPending`（reconciler 事后凭 evidence port 补结算）。
    - Finalize 时 Billing 暂不可用 → MarkPending（不吞错、不双扣；sweeper/reconciler 事后结算）。
    - 所有结算用 **detached bounded context**（10s），客户端断开不丢结算。
    - 客户端已提交后断开（mid-response）→ MarkPending + 写 `client_cancelled`（绝不计为 success）。
    - 已移除 `isStreamRequest` 死参数（settlement 由 committed 标志与 HTTP 状态决定，无 stream 特殊逻辑）。

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
- identity：JWT valid/expired/wrong-issuer/empty、缺失公钥 fail-closed、显式 noop verifier、middleware allow/reject
- quota：noop、reserve→finalize→release、error、unreachable
- proxy：forward+token、502 on unreachable
- app：全链路（auth→quota→proxy→finalize）、auth reject 401、release on 502、healthz anonymous、quota unavailable 503、committed-aware settlement（token known finalize / not-terminal & outage→pending / pre-commit release / no-1-token-fallback / log-arrives-late→pending）
- panel：套餐列表过滤 image/free、余额降级返 0、用户套餐余额填充、请求日志分页+status 映射、日志详情越权拦截、stats 聚合、settings PATCH 局部更新持久化、未认证 401、下游不可用 503

## 待实现

- API Key 验证：V3 与 legacy prod key 均为 `sk-` 前缀，走 Auth Service `/api/v1/auth/verify-key`（`internal/identity/apikey_verifier.go`，`NewCompositeVerifier` 按前缀分发）。Legacy key 兼容需要 Auth 侧注入 `AUTH_LEGACY_API_KEY_PEPPER`（见 `services/auth/AGENTS.md`）。
- cancel-risk 评估

> committed-aware settlement coordinator 已实施（pre-commit release / known finalize / not-terminal & Logging outage→pending / Billing outage→pending / detached context 不丢结算）；1-token fallback 与 `isStreamRequest` 死参数已移除。Billing reconciler（evidence port）负责 pending 事后结算（见 `services/billing/AGENTS.md`）。

## 约束

- **DO NOT** 在 Edge 执行模型调用或协议转换——转发给 Executor。
- **DO NOT** 让 Edge 直连任何数据库——通过 Billing/Logging Service HTTP API。
- **DO NOT** 泄漏下游服务 URL 到错误响应。
- **DO NOT** 跳过身份验证（生产必须配置 `API_JWT_PUBLIC_KEY_FILE`；仅本地开发/测试可显式设置 `API_ALLOW_NOOP_AUTH=true` 使用 noop verifier）。
- Executor token 是服务级密钥——不得提交到仓库。

## 文档维护

请求流、中间件链、安全策略变化时，同步维护本文件与 `services/AGENTS.md`。

## Container image and Compose

- `Dockerfile` is built with the repository root as context and produces only the static
  `api` binary in a non-root Alpine runtime image. Its service-local module download runs
  with `GOWORK=off`; the shared `packages/go/httpresp` replace target is copied explicitly.
- The image health check probes `/healthz`, the HTTP liveness route.
- Root `compose.yaml` owns the service definition only; provide required database and key
  inputs at deploy time, and do not add shared PostgreSQL/Redis/proxy resources or secrets.

The repository-root build context means Dockerfile `COPY` sources are rooted at
`services/<service>` (with the shared `packages/go/httpresp` copied from the same root), not
at the Dockerfile directory. `tools/check-dockerfile-copy-sources.sh` statically guards this
before CI Docker builds.

Compose uses the actual `API_RATE_LIMIT_*` inputs when rate limiting is enabled:
enabled, external Redis address/DB, trusted-proxy CIDRs, read-only HMAC secret file, IP/subject
policy values and bucket TTL. Compose does not create Redis and has no speculative
`API_REDIS_URL`, `API_TRUSTED_PROXY_CIDRS`, or `API_HMAC_SECRET_FILE` aliases.

Compose also mounts the Config admin token as a read-only secret at
`API_CONFIG_SERVICE_TOKEN_FILE`, consumed by the API `internal/config` secret-file loader
(production source; `API_CONFIG_SERVICE_TOKEN` is a dev/test-only direct env alternative and
the two are mutually exclusive). Do not pass a secret-file path as the string
token, add an entrypoint wrapper, or place token content in environment/Compose render output.
