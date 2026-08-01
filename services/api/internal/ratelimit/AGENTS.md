# services/api/internal/ratelimit

> 作用域：`services/api/internal/ratelimit`。继承 `services/api/AGENTS.md`。

## 模块职责

将 `packages/go/ratelimit` 接入 Edge。两个 net/http 中间件，仅作用于计量执行
POST（chat/messages/responses/images）：

- `IPMiddleware`：身份验证**前**的 per-IP bucket（未认证洪流也按 IP 限流）。
- `SubjectMiddleware`：身份验证**后**、quota/proxy **前**的 per-subject bucket。

health 与只读端点（如 GET `/v1/models`）不限流。限流严格在 SSE commit 前完成。
fail-closed 503 / 429 `rate_limited` + `Retry-After` + `Cache-Control: no-store`，
原始 JSON（与 Edge `/v1/*` 错误形状一致，不经 envelope）。

## 对外能力

| 能力 | 输入 | 返回/副作用 | 稳定性 |
|---|---|---|---|
| `IPMiddleware(Deps)` | shared limiter + deriver + policies | `func(http.Handler) http.Handler`；nil Limiter → passthrough | stable |
| `SubjectMiddleware(Deps)` | 同上 | 同上 | stable |
| `MeteredPath(method,path)` | method+path | bool | stable |

## DO NOT

- DO NOT 在 SSE commit 后写 JSON——中间件必须在 quota/proxy 前。
- DO NOT 限制 GET `/v1/models` 等只读端点。
- DO NOT 在 `SubjectMiddleware` 无 identity 时返回限流响应——identity 中间件已 401。
