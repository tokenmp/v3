# packages/go/ratelimit

> 作用域：`packages/go/ratelimit`。继承仓库根 `AGENTS.md` 与 `packages/AGENTS.md`。

## 模块职责

跨副本一致的共享 token-bucket 速率限制基础设施。提供：

- `Limiter` 端口 + `Decision`（Allowed / RetryAfter / Remaining）+ 稳定非 wrapping
  `ErrUnavailable` sentinel（fail-closed）。
- `RedisLimiter`：单次原子 Lua token-bucket 脚本，用 `redis.call('TIME')` 以 Redis
  服务器时钟计算 refill，消除跨副本客户端时钟漂移。Redis 错误/空结果/畸形回复一律
  返回 `ErrUnavailable`。Redis >= 5 effects-replication 安全。
- `InMemory`：进程内 fake，仅用于单元测试，**不**跨副本、**不**用于生产。
- `KeyDeriver`：length-prefixed HMAC-SHA256 → `rl:v1:<hex>`；最少 32 字节 secret；
  原始维度（IP/email/token）绝不进 key。
- `trustedip` 子包：`Resolver` + net/http `Middleware`，取代无条件 chi
  `middleware.RealIP`。仅当 TCP peer 属显式 CIDR 时接受转发头；否则用 peer IP。
  `X-Real-IP` **绝不使用**（单值头无 chain provenance，任意 hop 可伪造）；规范
  `X-Forwarded-For` chain 是唯一 forwarded 来源，XFF 缺失时用 TCP peer。

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `Limiter.Allow` | `Bucket{Key,Capacity,RefillPerSecond,TTLSeconds}` | `Decision` 或 `ErrUnavailable`（fail-closed） | stable | 本包 `ratelimit.go` |
| `NewRedisLimiter` | `redis.UniversalClient` + timeout | `*RedisLimiter`；客户端生命周期归调用方 | stable | 本包 `redis.go` |
| `KeyDeriver.Derive` | scope + dims | `rl:v1:<hex>`；不泄露原始维度 | stable | 本包 `key.go` |
| `trustedip.Resolver.Middleware` | 配置的 CIDR | 设置 ctx IP + `RemoteAddr` | stable | 本包 `trustedip/trustedip.go` |

## 依赖关系与消费者

| 方向 | 模块 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | `github.com/redis/go-redis/v9 v9.21.0` | Redis 客户端 + `redis.Script` | Go module import | go-redis 公开 API | go test/build |
| 被依赖 | `services/auth` | login/register/refresh StrictMiddlewareFunc | Go module import（replace） | `internal/ratelimit` | Auth go test + 契约门禁 |
| 被依赖 | `services/api` | `/v1/*` IP+subject 中间件 | Go module import（replace） | `internal/ratelimit` | Edge go test |

## 开发与验证

```bash
cd packages/go/ratelimit
gofmt -l .          # 必须空输出
go vet ./...
go vet -tags=integration ./...
go test -race ./...
# 真实 Redis 集成（build tag integration）：
# 默认（未设 RATELIMIT_REQUIRE_REDIS_TESTS）：Redis 不可达时 SKIP。
# 必需模式（CI 设 RATELIMIT_REQUIRE_REDIS_TESTS=true）：URL parse/ping/Lua 失败
# 均 FATAL，绝不静默 skip。测试隔离用唯一 HMAC scope + 有界 SCAN+DEL 清理
# rl:v1:* 键，不在共享库上 FlushDB。
RATELIMIT_REQUIRE_REDIS_TESTS=true \
  RATING_TEST_REDIS_ADDR=redis://127.0.0.1:6379/0 \
  go test -tags=integration -race ./...
```

集成测试在 Redis 不可达且未开启 required 模式时自动 SKIP（不 FAIL）；CI 在
`redis:7-alpine` service container 上以 required 模式运行。GitHub Actions 在 job
steps 前等待 service health check；随后 required suite 自身 PING Redis，URL parse、
ping 或 Lua 失败均 FATAL，因此不会静默 SKIP。

## 测试门禁拆分（required-mode gate）

反向验证（坏 URL / Redis 不可达）必须**不**让整个 suite 变红。实现：

- `redis_helper_test.go`（无 build tag）暴露 `dialRedisErr(t) (*redis.Client, error, required bool)`，纯函数、**绝不**调用 `t.Fatal`/`t.Skip`，返回错误与 `required` 标志。
- `TestDialRedisErr_BadURL` / `TestDialRedisErr_PingFails` 直接断言 `dialRedisErr` 返回非 nil 错误，因此在 required+Redis-up CI 下也 PASS，不会 fatal 任何测试。
- `redis_integration_test.go`（build tag `integration`）的 `dialRedis(t)` 包 `dialRedisErr` 并施加 skip-vs-fatal 策略：required 时连接失败 FATAL（CI 不静默 skip），否则 SKIP（本地无 Redis 绿）。真实集成测试用 `dialRedis`。
- 历史教训：早期反向测试直接调用 `dialRedis(t)` 并依赖 `t.Fatalf` 证明 gate，导致 required+Redis-up CI 整包必红。禁止让反向验证走 Fatal 路径。

## 模块边界

- 允许：自身 module、公开依赖（go-redis）、被 Auth/Edge 以 import 方式复用。
- 禁止：反向依赖具体 service；任何业务逻辑；数据库/repository；内存 fallback 用于生产。

## DO NOT

- **DO NOT** 在生产用 `InMemory`——它不跨副本一致；生产必须用 `RedisLimiter`，Redis
  异常必须 fail-closed（`ErrUnavailable` → 调用方返回 503）。
- **DO NOT** 把原始 IP/email/token 放进 `Bucket.Key` 或日志——必须经 `KeyDeriver.Derive`。
- **DO NOT** 用客户端时钟计算 refill——Lua 脚本必须用 `redis.call('TIME')`。
- **DO NOT** 让 HMAC secret 少于 32 字节——`NewKeyDeriver` 会 fail-fast。
- **DO NOT** 把 HMAC secret 内容、Redis URL/密码写进错误或日志。

## 已知陷阱与历史教训

### 命名组件触发 oapi-codegen 枚举前缀

- 症状：在 auth 契约新增 `components/responses/RateLimited` 后重生成，oapi-codegen
  为全部 `ErrorErrorCode` 枚举常量加上 `ErrorErrorCode` 前缀，破坏现有短名引用。
- 根因：响应组件名 `RateLimited` 与枚举值 `rate_limited` 生成的常量 `RateLimited`
  冲突，oapi-codegen 为整个枚举加类型前缀消歧。
- DO：429/503 响应直接 inline 到各 operation（与 500 风格一致），避免命名响应组件。
- DO NOT：新增与枚举值同名的 `components/responses` 组件。
- 验证：`check-generated.sh` 绿 + `go build ./...` 保持短名。
- 适用范围：使用 oapi-codegen 生成枚举的所有 Go 服务。
