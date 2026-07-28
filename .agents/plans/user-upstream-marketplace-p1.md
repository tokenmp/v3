# 用户上游市场 P1：用户私有上游 + 自有优先路由

- 状态：draft
- 创建日期：2026-07-29
- 最后更新：2026-07-29
- 关联分支：none（待开 `feat/user-upstream-p1`）
- 负责人：Agent / User
- 依赖总览：`.agents/plans/user-upstream-marketplace-overview.md`

## 目标

实现最小可用闭环：用户能在 Panel 配置自己的上游账号，Executor 路由时 owner 自有上游优先于平台池。本阶段**不做共享、不做计费、不做收益**。

P1 结束后用户可以：
1. 在 Panel 创建上游账号（provider、base_url、API key 加密存储）。
2. 为账号配置模型清单。
3. 上架为 `published`（默认 private+self_use）。
4. 调用 Executor 时，owner 用「不带 @」请求 → 优先命中自己的 published+self_use 上游，其次平台池。
5. `GET /v1/models` catalog 合并 owner 自有 model。

## 非目标（P1 排除）

- 共享给其他用户（`sharing=public` 的**调用侧**，P1 可设 public 但 Executor 不解析非 owner 命中）→ P2
- `model@provider_name` 显式选取非自己的上游 → P2
- per-caller 限流 → P2
- quarantine 接入用户上游 → P2
- 调用记账 / 余额 / 结算 → P3
- Admin 监管视图 → P4
- 密钥失效通知 → P3/P4

> 说明：P1 允许用户把 sharing 设为 public，但 Executor 在 P1 **不实现**非 owner 调用 public 上游的路由解析。即 public 标记先存着但不生效。这样 P2 只改 Executor 路由，不改数据模型。

## 范围

### 1. 新建 `services/upstream`

Go 1.26.5 module `github.com/tokenmp/v3/services/upstream`，加入 `go.work`。

目录结构（对齐 notice 服务模式）：

```
services/upstream/
  cmd/upstream/main.go          # 入口：env、DB、server、graceful shutdown
  cmd/healthcheck/main.go
  internal/
    config/                     # env 加载校验（UPSTREAM_*，DSN 限定 /tokenmp_upstream）
    database/                  # GORM + classified sentinel（不泄漏 DSN）
    crypto/                    # AES-256-GCM 加解密（v1: 格式，复用 prod 语义）
    models/                    # GORM 模型
    repository/                # 数据访问端口 + 实现
    server/                    # HTTP（chi）路由
  migrations/
    000001_init.up.sql
    000001_init.down.sql
  AGENTS.md
```

#### 1.1 库与表（`tokenmp_upstream`）

```sql
-- 000001_init.up.sql

CREATE TABLE user_upstream_accounts (
    id                      uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id           uuid        NOT NULL,
    name                    text        NOT NULL,
    provider_type           text        NOT NULL,   -- openai|anthropic|custom-openai|...
    base_url                text        NOT NULL,   -- HTTPS 强制
    encrypted_api_key       bytea       NOT NULL,   -- AES-256-GCM v1: 格式密文
    key_prefix              text        NOT NULL,   -- 展示用前缀
    key_suffix              text        NOT NULL,   -- 展示用后缀
    provider_name           text        UNIQUE,     -- 全局唯一，selector @provider 段（P1 可空，P2 启用）
    status                  text        NOT NULL DEFAULT 'draft',   -- draft|published|disabled
    sharing                 text        NOT NULL DEFAULT 'private', -- private|public
    owner_self_use          boolean     NOT NULL DEFAULT true,
    owner_self_deduct_quota boolean     NOT NULL DEFAULT false,
    owner_self_record_usage boolean     NOT NULL DEFAULT true,
    per_caller_rpm          integer     NOT NULL DEFAULT 0,         -- P2 启用
    per_caller_daily_cap    integer     NOT NULL DEFAULT 0,         -- P2 启用
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    published_at            timestamptz,
    disabled_at             timestamptz,
    CONSTRAINT uua_status_chk CHECK (status IN ('draft','published','disabled')),
    CONSTRAINT uua_sharing_chk CHECK (sharing IN ('private','public')),
    -- 核心约束：published 时必须 sharing=public OR owner_self_use=true
    CONSTRAINT uua_published_active_chk CHECK (
        status <> 'published' OR (sharing = 'public' OR owner_self_use = true)
    )
);

CREATE INDEX uua_owner_idx ON user_upstream_accounts (owner_user_id, status);
CREATE UNIQUE INDEX uua_provider_name_uidx
    ON user_upstream_accounts (provider_name)
    WHERE provider_name IS NOT NULL AND status <> 'disabled';

CREATE TABLE user_upstream_models (
    id                      uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id              uuid        NOT NULL REFERENCES user_upstream_accounts(id) ON DELETE CASCADE,
    upstream_model_id       text        NOT NULL,   -- 传给上游的真实 model 名
    display_model_id        text        NOT NULL,   -- 对外 catalog 名
    enabled                 boolean     NOT NULL DEFAULT true,
    price_input_per_1k      integer,                -- NULL=回退平台基础价（P3 计费用）
    price_output_per_1k     integer,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX uum_account_idx ON user_upstream_models (account_id, enabled);

COMMENT ON TABLE user_upstream_accounts IS '用户上游账号（P1：自用优先；P2：共享；P3：记账）';
COMMENT ON COLUMN user_upstream_accounts.encrypted_api_key IS 'AES-256-GCM 密文，v1: 格式，运行时解密';
```

> P1 不建 `call_records`/`settlements`/`balances` 表（P3 再加迁移）。

#### 1.2 加解密

`internal/crypto`：
- `Encrypt(plaintext []byte) ([]byte, error)` — AES-256-GCM，key 从 `UPSTREAM_MASTER_ENCRYPTION_KEY` env（SHA-256 派生），输出 `v1:` + base64url(nonce(12)+ciphertext+tag(16))。
- `Decrypt(blob []byte) ([]byte, error)` — 解密 `v1:` 格式。
- 密钥从不入日志/错误。错误返回 sentinel，不泄露内容。

#### 1.3 Repository

端口（不暴露密文）：
- `Create(ctx, acc *UserUpstreamAccount) error`
- `GetByID(ctx, id, ownerUserID string) (*UserUpstreamAccount, error)` — owner 隔离
- `ListByOwner(ctx, ownerUserID string) ([]UserUpstreamAccount, error)` — owner 的全部（含 draft/disabled）
- `ListPublishedSelfUse(ctx, ownerUserID string) ([]UserUpstreamAccount, error)` — Executor 查询用：`status='published' AND owner_self_use=true AND owner_user_id=?`
- `Update(ctx, acc *UserUpstreamAccount) error` — 更新可变字段（name/base_url/sharing/owner_self_use 等），强制状态约束（DB CHECK + 应用层校验）
- `Publish(ctx, id, ownerUserID string) error` — draft→published，默认 private+true
- `Disable(ctx, id, ownerUserID string) error` — →disabled
- `RotateKey(ctx, id, ownerUserID string, encryptedNew []byte, prefix, suffix string) error`
- `CreateModel`/`ListModels`/`UpdateModel`/`DeleteModel`

#### 1.4 Server 路由

JWT 鉴权（复用 Auth Ed25519 公钥本地验证，同 Notice 模式）。Bearer token 提取 `sub`=owner_user_id。

```
GET    /healthz
GET    /readyz

# 用户上游账号（owner scope）
GET    /v1/upstream/accounts                    # 列出自己的（含 draft）
POST   /v1/upstream/accounts                    # 创建（一次性返回明文 key）
GET    /v1/upstream/accounts/{id}
PATCH  /v1/upstream/accounts/{id}               # 改 name/base_url/sharing/owner_self_use 等
POST   /v1/upstream/accounts/{id}/publish       # draft→published
POST   /v1/upstream/accounts/{id}/disable      # →disabled
POST   /v1/upstream/accounts/{id}/rotate-key    # 轮换 key（一次性返回明文）
DELETE /v1/upstream/accounts/{id}               # 硬删除（仅 draft 或 disabled 可删）

# 模型清单
GET    /v1/upstream/accounts/{id}/models
POST   /v1/upstream/accounts/{id}/models
PATCH  /v1/upstream/accounts/{id}/models/{modelId}
DELETE /v1/upstream/accounts/{id}/models/{modelId}
```

- 创建/轮换成功响应**一次性**返回明文 API key，后续永不返回。
- 列表/详情响应只返回 `key_prefix`/`key_suffix`，不返回 `encrypted_api_key`。
- 所有响应 `Cache-Control: no-store`。
- 状态约束校验：应用层在 publish/patch 前校验 `published → (public OR self_use)`，DB CHECK 兜底。
- `provider_name` 字段 P1 可创建（用于将来），但 Executor P1 不解析。全局唯一校验生效。

### 2. Executor 改造（owner 自有优先）

#### 2.1 路由 Resolver 候选来源扩展

当前 `routing.Resolver` 只从平台 `snapshot.CompiledSnapshot` 取候选。P1 扩展：

- 新增 `UserUpstreamSource` 端口（在 `internal/userupstream` 新包），方法：
  ```go
  type Source interface {
      // SelfUseCandidates 返回 owner 的 published+self_use 上游候选（按 modelID）。
      SelfUseCandidates(ctx context.Context, ownerUserID string) ([]UpstreamCandidate, error)
  }
  type UpstreamCandidate struct {
      AccountID, ProviderType, BaseURL string
      UpstreamModelID, DisplayModelID string
      EncryptedAPIKey []byte           // 密文，由凭证端口按需解密
  }
  ```
- `composition.Build` 注入 `UserUpstreamSource`（HTTP 客户端调 upstream 服务）。
- Resolver 在 `Resolve(selector)` 且**不带 `@provider`** 时：
  1. 先查 owner（caller_user_id，从 Principal）的 self_use 候选，若 model 匹配 → 优先返回。
  2. 否则回退平台 snapshot 候选（现有逻辑）。
- 带 `@provider` 的请求 P1 走平台 provider 匹配（用户 provider_name 解析留 P2）。

#### 2.2 凭证解析

当前 `credentialenv.Resolver` 只从 env 读平台凭证。P1 扩展：
- 新增 `internal/userupstream.CredentialResolver`：接收 `UpstreamCandidate.EncryptedAPIKey`，调 upstream 服务的解密端口（或本地解密，若 key 共享）拿到明文 opaque secret。
- Runner 在 `Prepare` 阶段，若候选来自用户上游 → 用 `CredentialResolver` 解密，否则用现有 `credentialenv`。
- 明文 secret 仅在本次 attempt 内存中，不进日志/不进 compiled snapshot。

> 决策点（P1 实现时定）：upstream 服务是否暴露「解密」HTTP 端口给 Executor？还是 Executor 共享 `UPSTREAM_MASTER_ENCRYPTION_KEY` 本地解密？倾向**本地解密**（避免明文过网络），Executor 与 upstream 服务共享同一 master key env。

#### 2.3 Catalog 合并

`modelcatalogfacade` 在 P1：
- `GET /v1/models` 除了平台 snapshot model，再合并 owner 的 self_use 上游 model。
- owner 自有 model 的 `id` = `display_model_id`（不加命名空间前缀，因为 owner 自用不需要消歧）。
- 来源标记 `owned_by: "self"`（供前端区分，P2 共享时加 `owned_by: <ownerhandle>`）。

### 3. Frontend（Panel「我的上游」页）

`apps/web/src/app/panel/upstream/page.tsx`：
- 账号列表（name、provider_type、base_url、key_prefix/suffix、status badge、sharing/self_use 标记）。
- 创建表单：name、provider_type（下拉）、base_url、API key（一次性输入，提交后不可见）、sharing、owner_self_use 开关。
- 编辑：改 name/base_url/sharing/owner_self_use（强制状态约束前端校验）。
- 上架/下架/轮换 key 操作。
- 模型清单子表：每个账号下管理 models。
- 调用 API 走 `/api/v1/upstream/*`（Edge 转发到 upstream 服务，同现有 notice 模式）。

Admin 侧 P1 不做。

### 4. API/Edge 转发

`services/api` 增加 upstream 转发路由（同 keys 转发到 Auth 模式）：
- `/api/v1/upstream/*` → upstream 服务，Bearer token 透传。
- 不走配额。

## 前置条件

- Auth Ed25519 公钥文件可被 upstream 服务读取（同 Notice 配置）。
- `UPSTREAM_MASTER_ENCRYPTION_KEY` env 配置（与 Executor 共享）。
- dev 服务器 PG 可建新库 `tokenmp_upstream`。
- `go.work` 加入 upstream module。

## 实施步骤

1. **新建 upstream 服务骨架**
   - `go.work` 加 module。
   - `cmd/upstream/main.go`、`internal/config`、`internal/database`、`internal/server`（healthz/readyz）。
   - 迁移文件 `000001_init.up.sql`。
   - dev 建 `tokenmp_upstream` 库。
2. **加解密 + repository + models**
   - `internal/crypto`、`internal/models`、`internal/repository`（含状态约束测试）。
3. **HTTP 路由 + JWT 鉴权**
   - `internal/jwtverifier`（复用 Auth 公钥）。
   - 全部账号 + 模型 CRUD 路由。
   - 一次性明文返回、no-store、状态约束校验。
4. **Executor owner 自有优先路由**
   - `internal/userupstream`（Source + CredentialResolver）。
   - Resolver 扩展：owner self_use 优先 + 平台回退。
   - composition 注入。
   - catalog 合并 owner model。
5. **API/Edge 转发 + 前端**
   - API 转发路由。
   - Panel「我的上游」页。
6. **测试与部署**
   - upstream 服务单测 + 集成测试（DSN gate，同 notice）。
   - Executor 路由测试（owner 优先、平台回退、catalog 合并）。
   - 部署 dev 验证。

## 验证

- [ ] upstream 服务 healthz/readyz 正常。
- [ ] Panel 创建账号 → 返回明文 key → 再次 GET 不返回明文。
- [ ] DB 中 `encrypted_api_key` 为密文，`key_prefix/suffix` 明文。
- [ ] 状态约束：draft→published 默认 private+true；published 时设 private+false 返回 400。
- [ ] owner 用不带 `@` 的 model 请求 → 命中自己 published+self_use 上游（而非平台池）。
- [ ] owner 无匹配自有上游 → 回退平台池。
- [ ] `GET /v1/models` 包含 owner 自有 model。
- [ ] 密钥不入日志、不进 compiled snapshot。

## 风险与回滚

- **密钥泄露**：加解密在 upstream 服务 + Executor 本地，master key 通过 env 注入不落盘。错误返回 sentinel 不泄露内容。审计日志记录 create/rotate/disable。
- **Executor 路由回归**：owner 自有优先是新分支，平台回退保持原逻辑。若 userupstream Source 不可用 → fail-open 回退平台池（P1）还是 fail-closed？**决策：fail-open 回退平台池**（用户上游是增强，不应阻断基础调用）。
- **DB 状态约束**：应用层 + DB CHECK 双重，防止脏数据。
- **回滚**：upstream 服务独立，Executor 注入可选（Source 为 nil 时纯平台池），可单独下线。

## 决策与阻塞项

- [ ] 待确认：upstream 服务是否暴露解密 HTTP 端口给 Executor，还是 Executor 共享 master key 本地解密？（倾向本地解密）
- [ ] 待确认：owner 自有优先的 catalog model 是否需要前缀区分？（P1 倾向不加前缀，owner 自用无歧义）
- [ ] 待确认：Edge 转发 upstream 是否新建独立 path `/api/v1/upstream/*`（倾向是）。

## 完成后的文档同步

- P1 完成后，将 upstream 服务的最终架构同步到 `services/AGENTS.md`、根 `AGENTS.md`「已实施模块」、新建 `services/upstream/AGENTS.md`。
- Executor 改造同步到 `services/executor/AGENTS.md`。
- 本计划状态改为 completed，长期结论迁移到正式文档后可删除。
