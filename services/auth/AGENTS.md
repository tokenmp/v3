# Auth Service

> 作用域：`services/auth/`。继承仓库根目录与 `services/AGENTS.md`。

## 模块职责

- 负责：TokenMP v3 认证服务的运行时与身份流——HTTP server、配置、PostgreSQL 连接、版本化 SQL migration、`/healthz` 与 `/readyz`、优雅关闭、Dockerfile、单元/集成测试、Go CI job；以及 Auth Identity Flows：注册、登录、Ed25519/EdDSA Access Token 签发、opaque Refresh Token 轮换与 reuse 检测、logout/logout-all、`/me`、Argon2id 密码哈希与 bcrypt 兼容升级。
- 不负责（本 PR 不实现）：`preferred_billing`/`fallback_enabled` 等业务字段；内存级速率限制（部署阻塞项，必须由未来 Gateway/Redis 共享策略落地）；浏览器 cookie 模式（另行设计）。
- 所有者：TokenMP 后端基础设施。

## 必读文档

- 模块说明：`README.md`
- 架构决策：`../../docs/adr/0004-auth-service-foundation.md`、`../../docs/adr/0005-auth-identity-flows.md`
- 接口契约：`api/openapi.yaml`
- Migration 文件：`migrations/000001_create_users.{up,down}.sql`、`migrations/000002_create_auth_sessions.{up,down}.sql`
- Go workspace：`../../go.work`
- 仓库 Docker 规范：`../../.agents/docker.md`

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/错误/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `GET /healthz` | 进程存活 | 200 `{status:"ok",service:"auth",timestamp}`；不依赖外部资源 | stable | `api/openapi.yaml`、`internal/handler/health.go` |
| `HEAD /healthz` | 同上 | 200；空 body | stable | 同上 |
| `GET /readyz` | 已注入 `Pinger` | 200 ok 或 503 `{status:"unready",...}`；**不泄露底层错误** | stable | 同上 |
| `HEAD /readyz` | 同上 | 200 或 503；空 body | stable | 同上 |
| `POST /api/v1/auth/register` | email+password JSON | 201 `{id,email,role,status,created_at}`；**不自动登录**；重复 409 `email_taken`；弱密码/非法 email 400 | stable | `api/openapi.yaml`、`internal/handler/auth.go` |
| `POST /api/v1/auth/login` | email+password JSON | 200 `{access_token,refresh_token,token_type:"Bearer",expires_in}`；不存在/错密码/disabled 统一 401 `invalid_credentials`（预生成 dummy Argon2id hash 执行 CompareDummy 防时序差，disabled 先完成 Compare 再返回） | stable | 同上 |
| `POST /api/v1/auth/refresh` | `{refresh_token}` JSON | 200 token response；命中已撤销旧 token 视为 reuse：事务内撤销整 family 为 `token_reuse` 并 COMMIT，返回固定 401（与 invalid_refresh 同形，不暴露 reuse 信号）；过期/disabled 同样 401 | stable | 同上 |
| `POST /api/v1/auth/logout` | `{refresh_token}` JSON | 204 幂等：无效/已撤销 token 也 204，避免探测 | stable | 同上 |
| `POST /api/v1/auth/logout-all` | Bearer | 204；撤销全部 active session 为 `logout_all` 并 bump `token_version`，立即失效所有 Access | stable | 同上 |
| `GET /api/v1/auth/me` | Bearer | 200 public user；中间件每次查当前 user 比较 status+token_version | stable | 同上 |
| `PUT /api/v1/auth/password` | Bearer + `{current_password,new_password}` | 204；Argon2id 更新、bump `token_version`、撤销所有 active session 为 `password_changed`（含当前）；调用方需重登 | stable | 同上 |

JWT 为 Ed25519/EdDSA（`github.com/golang-jwt/jwt/v5`）。claims：iss/aud/sub/jti/iat/nbf/exp + role + token_version；Access 默认 15m。消费者仅持公钥即可验证；文档必须明确私钥泄露仍可伪造，至轮换前有效。

Refresh 为 32-byte `crypto/rand` base64url；DB 只存 SHA-256 BYTEA；默认 30d。轮换方向：OLD row 的 `replaced_by_session_id` 指向 NEW session id，OLD 撤销为 `token_rotated`；NEW row 不携带 `replaced_by_session_id`。

公共错误统一 `{error:{code,message}}`，不泄露 PG/密码/token。`internal/repository` 返回稳定分类 sentinel（`ErrDuplicateEmail`/`ErrNotFound`/`ErrConstraint`/`ErrInternal`），不 wrap 驱动原始 error。

速率限制**未实现**：多副本不一致、RealIP 信任边界未定义。部署前必须由未来 Gateway/Redis 共享策略落地；不得描述为已保护。

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | PostgreSQL（库名 `tokenmp_auth`） | 用户/session 持久化 | GORM PG driver / SQL | `AUTH_DATABASE_URL`、`migrations/*.sql` | migration 周期 + 集成测试 |
| 依赖 | `github.com/go-chi/chi/v5` | HTTP 路由与中间件 | Go module import | chi v5 公开 API | `go build ./...` |
| 依赖 | `gorm.io/gorm` + `gorm.io/driver/postgres` | ORM 与连接池；事务 + `clause.Locking` SELECT FOR UPDATE | Go module import | GORM 公开 API | `go vet`、`go test -race` |
| 依赖 | `github.com/golang-jwt/jwt/v5` | Ed25519/EdDSA Access Token 签发与验证 | Go module import | `jwt/v5` 公开 API | `go test -race`、集成测试 |
| 依赖 | `github.com/alexedwards/argon2id` | Argon2id 密码哈希（PHC） | Go module import | 公开 API | `go test -race` |
| 依赖 | `golang.org/x/crypto`（bcrypt） | bcrypt 兼容验证 | Go module import | `bcrypt.CompareHashAndPassword` | `go test -race` |
| 依赖 | `github.com/jackc/pgx/v5`（`stdlib`） | 集成测试原生 SQL 校验 | build tag `integration` | pgx `database/sql` driver | `go test -tags=integration` |
| 依赖 | `golang-migrate` CLI | 应用 migration | 外部 CLI，非运行时依赖 | `migrate -path migrations` | CI migration 周期 |

当前**没有已实施的直接消费者**（Web/Admin/Gateway/其他服务均未实施）。不得把未来消费者写成已集成。

## 开发与验证

**本机默认只跑 unit tests。** 本机不得安装或启动 PostgreSQL，也不得运行
`docker run postgres`、`brew install postgresql` 或任何等价动作。本机也
**不需要** Docker：Dockerfile 由 `go-auth` CI job 在 GitHub Runner 上
`docker build` 验证（仅 build，不 run、不 push、不发布）。真实 migration
周期与集成测试**只**在 GitHub Actions 的 `go-auth` job 中运行，依托每次运行
临时创建的 `postgres:17-alpine` service container，运行后销毁（见
`.github/workflows/ci.yml`）。

```bash
cd services/auth

# 格式
gofmt -w .

# 静态检查（含集成 build tag 的静态检查，不需要真实 DB）
go vet ./...
go vet -tags=integration ./...

# 单元测试（不需要真实 DB；本机默认命令）
go test -race ./...

# 构建（默认 tag；集成 tag 的编译已由 go vet -tags=integration 覆盖）
go build ./...
```

`go test -race ./...`（不含 `integration` tag）是本机测试命令；`integration`
标签的集成套件由 CI 执行，不在本机运行。

Migration 周期与集成测试由 CI 执行：

```bash
# CI 中执行（本机不跑）：
migrate -path services/auth/migrations -database "$AUTH_DATABASE_URL" up
migrate -path services/auth/migrations -database "$AUTH_DATABASE_URL" down -all
migrate -path services/auth/migrations -database "$AUTH_DATABASE_URL" up
go test -tags=integration -race ./test/integration/...
```

### 连接共享开发/预览 PostgreSQL

若需将服务指向共享开发或预览 PostgreSQL，**必须**使用经确认的独立
`tokenmp_auth` 库，不得对生产库或其他服务共享库执行测试、migration 或临时
写入。连接信息通过仓库外渠道获取，不得提交，也不得写入服务器地址、SSH 路径
或凭据（见 `../../.agents/operations.md`、`../../.agents/docker.md`）。

## 模块边界

- 允许访问：自身 module、`tokenmp_auth` 数据库、公开依赖（chi、gorm、pgx 驱动）。
- 禁止访问：其他服务的私有源码或私有数据库；其他服务的 migration。
- 数据所有权：`tokenmp_auth` 数据库中的 `users` 与 `auth_sessions` 表，由本服务独占。
- 配置和环境变量：仅 `AUTH_*`（见 README 表格）；`AUTH_DATABASE_URL` 必填，严格解析
  （仅 postgres/postgresql、必有 host、非空 user、path 精确 `tokenmp_auth`）；
  所有 `AUTH_DB_MAX_*` / lifetime / shutdown timeout 非法值 fail-fast，不静默 fallback；
  错误不回显 URL/凭据。JWT 密钥走 `AUTH_JWT_PRIVATE_KEY_FILE`/`AUTH_JWT_PUBLIC_KEY_FILE` 文件路径（不接受 PEM 环境变量），
  启动 fail-fast 解析 Ed25519 PEM，错误不回显 key/path；`AUTH_JWT_ACCESS_TOKEN_TTL`/`AUTH_JWT_REFRESH_TOKEN_TTL` 非法或 refresh <= access 时 fail-fast。
- Docker 镜像/部署单元：`tokenmp-v3-auth:<sha>`，构建上下文仓库根目录，Dockerfile 位于 `services/auth/Dockerfile`（按模块自治规范放于模块目录，而非根 `Dockerfile.auth`）；最终镜像非 root，不含 migrate CLI，不在启动时执行 migration；运行时镜像**不含 JWT 密钥**，部署必须挂载 key files（见 ADR 0005）。
- 健康检查：`/healthz`（liveness，无外部依赖）；`/readyz`（readiness，DB ping，503 不泄露底层错误）；镜像 `HEALTHCHECK` 用内置 `/usr/local/bin/healthcheck` 二进制打 `/healthz`。

## DO NOT

- **DO NOT** 在本机安装或启动 PostgreSQL，也不得运行 `docker run postgres`、
  `brew install postgresql` 或任何等价动作 —— 真实 migration 与集成测试只由
  GitHub Actions 的 `postgres:17-alpine` service container 运行。本机默认只跑
  `gofmt`、`go vet`、`go test -race`（不含 `integration` tag）、`go build`。
- **DO NOT** 对生产数据库或任何非本服务独享的共享库执行测试、migration 或临时
  写入 —— 连接共享开发/预览 PostgreSQL 时必须使用经确认的独立 `tokenmp_auth` 库。
- **DO NOT** 在仓库或 `.agents/` 文档写入服务器地址、SSH 路径或凭据 —— 私有
  部署信息只放可选的 `.agents/local.md`（不提交）。
- **DO NOT** 调用 GORM `AutoMigrate` —— schema 由 `migrations/*.sql` 管理；
  AutoMigrate 会绕过 CHECK/索引/默认值并造成与 migration 不一致。正确做法：新增 migration。
- **DO NOT** 在 `/readyz` 503 响应中包含底层错误文本 —— 会泄露连接串或内部拓扑。
  正确做法：只返回 `{status:"unready"}`，错误进服务日志（不含连接串）。
- **DO NOT** 用 `citext` 存储 email —— 显式 `LOWER(BTRIM(email))` 唯一索引保持类型简单，
  避免扩展依赖与排序规则漂移。正确做法：VARCHAR(255) + CHECK
  (`email <> '' AND email = LOWER(BTRIM(email))`) + 表达式唯一索引；应用层在写入前归一化，
  CHECK 作为后闸确保存储值统一小写 trim。
- **DO NOT** 在日志或错误里输出 `AUTH_DATABASE_URL` 或其片段 —— 凭证泄露风险。
  正确做法：只输出验证错误，不带值。
- **DO NOT** 在容器启动时跑 migration 或把 `migrate` 二进制塞进运行时镜像 ——
  部署期 migration 由 CI/ops 在发布前执行；镜像不应隐式改库。
- **DO NOT** 在本 PR 实现内存级速率限制——多副本不一致、RealIP 信任边界未定义；速率限制必须由未来 Gateway/Redis 共享策略落地，部署前是阻塞项。不要把服务描述为已保护。
- **DO NOT** 通过环境变量传入 JWT PEM 密钥内容——只用 `AUTH_JWT_PRIVATE_KEY_FILE`/`AUTH_JWT_PUBLIC_KEY_FILE` 文件路径，避免多行 secret 与日志泄露。启动 fail-fast 解析 Ed25519 PEM，错误不回显 key/path。
- **DO NOT** 在仓库或镜像中提交/打包 JWT 私钥——测试进程内生成 key，运行时镜像不含 key，部署必须挂载 key files。
- **DO NOT** 把密码 trim 或 NFKC 归一——按原始 UTF-8 字节处理，按 rune 计数 12..128，拒绝无效 UTF8/NUL/控制字符。
- **DO NOT** 把 reuse 撤销随 error rollback——事务内撤销 family active sessions 为 `token_reuse` 后必须 COMMIT 再返回 401（service 层让 fn 返回 nil，commit 后再决定结果错误）。
- **DO NOT** 在 `users` 表加入 `preferred_billing`/`fallback_enabled` 等业务字段——
  不属于 Auth 数据所有权。

## 已知陷阱与历史教训

### 本机装 PostgreSQL 导致环境漂移与误连生产库

- 症状：开发者本机起了 PostgreSQL，跑 migration/集成测试后残留数据；或为“图方便”
  指向共享库甚至生产库做验证。
- 根因：没有明确“本机只跑 unit tests”的边界，迁移与集成测试在本机执行后产生
  状态漂移，且连接共享/生产库存在数据安全风险。
- DO：本机默认只跑 `gofmt`、`go vet`、`go test -race`（不含 `integration` tag）、
  `go build`；真实 migration 周期与集成测试交给 GitHub Actions 的
  `postgres:17-alpine` service container。连接共享开发/预览 PostgreSQL 时必须
  使用经确认的独立 `tokenmp_auth` 库。
- DO NOT：本机安装或启动 PostgreSQL、`docker run postgres`、`brew install
  postgresql`；对生产库或非本服务独享的共享库执行测试/migration/临时写入。
- 验证：本机 `go test -race ./...` 不依赖数据库；CI `go-auth` job 包含
  migration up/down/up 与 `go test -tags=integration`，并在 `go build` 后、
  migration 前于 GitHub Runner 上 `docker build` 验证 Dockerfile（仅 build，
  不 run/不 push/不发布），本机无需 Docker 或数据库。
- 适用范围：所有 TokenMP v3 服务。

### readyz 503 泄露数据库错误

- 症状：readiness 失败时响应体包含 `pq: ... password=...` 等内部信息。
- 根因：直接返回 `err.Error()` 把底层连接错误透出。
- DO：只返回固定 `{status:"unready",service:"auth",timestamp}`；底层错误进日志且不含连接串。
- DO NOT：把 `pinger.Ping` 的 `err` 写入 HTTP 响应。
- 验证：`internal/handler/health_test.go` 的 `TestReadyz_Unready503NoLeak` 断言响应不含 `secret`/`password`。
- 适用范围：所有 readiness 健康端点。

### GORM AutoMigrate 与版本化 migration 冲突

- 症状：AutoMigrate 生成的表缺少 CHECK 约束、表达式唯一索引与默认值，与 migration 不一致。
- 根因：AutoMigrate 只能描述 struct tag 可表达的内容，无法表达 `LOWER(BTRIM(email))` 唯一索引、`CHECK (role IN ...)`、`pgcrypto`。
- DO：schema 变更走 `migrations/NNNNN_*.up.sql` + 对应 down；GORM 模型只用于应用层读写。
- DO NOT：调用 `db.AutoMigrate(&User{},&AuthSession{})`。
- 验证：CI migration up/down/up 周期 + 集成测试断言 CHECK/索引行为。
- 适用范围：本服务及任何使用 GORM 的 TokenMP v3 服务。

## 文档维护触发器

出现以下变化时同步更新本文件、README、`api/openapi.yaml` 与 ADR 0004 / 0005：

- 新增/移除对外端点或修改返回结构。
- 引入/移除速率限制、Redis 共享策略、浏览器 cookie 模式或密钥轮换流程。
- 新增/删除直接依赖或首批真实消费者。
- migration、CHECK 约束、索引、默认值或库名变化。
- CI 步骤、Go 版本、`go.work` 或 Dockerfile 变化。
- 发现新的非显然陷阱，或上述陷阱不再适用时。
