# services/auth/internal/ratelimit

> 作用域：`services/auth/internal/ratelimit`。继承 `services/auth/AGENTS.md`。

## 模块职责

将 `packages/go/ratelimit` 接入 Auth 的 `StrictMiddlewareFunc`，对
`Login`/`Register`/`Refresh` 在 Argon2id/DB 操作前限流。读取已解码的
请求体（不重读 `r.Body`，保留严格 raw-body 边界）。fail-closed 503 /
429 `rate_limited` + `Retry-After` + `Cache-Control: no-store`。

每个操作由**两个独立 key 的 bucket** 门控：

- 纯 IP bucket（scope `auth.<op>.ip`，先检查）；
- account/token bucket（scope `auth.<op>.account`，后检查；login/register
  用 normalized email，refresh 用 opaque refresh token）。

IP bucket 先检查，仅当它允许时才检查 account/token bucket。任一 bucket
deny（429）或后端不可用（503 fail-closed）即在 Argon2id/DB 前短路。两个
bucket 可共享速率，但 key 始终独立：轮换 email/token 不能绕过纯 IP 防洪，
单账号跨 IP 仍受 account bucket 约束。多 bucket 检查**不是全局事务**（account
deny 不回滚已消费的 IP token），但 fail-closed 保证后端不可用时无请求通过。

## 对外能力

| 能力 | 输入 | 返回/副作用 | 稳定性 |
|---|---|---|---|
| `NewStrictMiddleware(Deps)` | `Limiter`/`Deriver`/`Policies`/`IPFromCtx` | `authv1.StrictMiddlewareFunc`；nil Limiter → passthrough | stable |

## 模块边界

- 仅读 `trustedip` 注入的 ctx IP + 已解码 `authv1.*RequestObject`。
- 不直连 Redis（用 `packages/go/ratelimit.Limiter` 端口）。
- HMAC secret 以短生命周期 `[]byte` 由 config 传入 main，main 构建 deriver 后
  置零；config 的 `RateLimitHMACSecretFile` 仅保留为非机密路径标识，不存 secret 内容。

## DO NOT

- DO NOT 在 `Deps.Limiter==nil` 时仍写入限流响应——nil 是"禁用"信号。
- DO NOT 用 `RetryAfter==0` 区分 429/503——必须用 limiter 返回的 error。
