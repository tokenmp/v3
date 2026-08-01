# Config Service

> 作用域：`services/config/`。继承仓库根 `AGENTS.md` 与 `services/AGENTS.md`。

## 职责

Config Service 是 TokenMP V3 分层架构的**控制平面**配置服务：

- 管理 Config DB（`infra/db/migrations/config/`）中的 provider/model/route/credential/adapter 配置。Provider 是协议中立的供应商/账号池维度；protocol 由 route/adapter/endpoint 表达，legacy `providers.sdk_kind`/`providers.protocol` 仅用于兼容旧行和默认值。`providers` 可配置供应商默认 context/max output/RPM/TPM，`route_mappings` 用于 provider+model 覆盖，`upstream_credentials` 与 `route_credentials` 用于账号级/路由账号绑定级覆盖；当前为配置与后台管理入口，执行侧 softmax/限流 enforcement 分后续批次接入。
- 发布版本化的配置快照（`config_revisions` draft/published/archived + `config_revision_snapshots.snapshot_json`）。
- 通过 HTTP `GET /v1/config/snapshots/latest` 把最新 published 的 raw `ConfigSnapshot` JSON 下发给 executor。

**编译边界**：Config Service **不编译**。它只下发 raw `ConfigSnapshot` JSON。编译成运行时 compiled snapshot 在 executor 端做（executor 的 `configsource.LoadFromConfigService` 拉取后本地 `snapshot.Compile`）。这样 Config Service 不依赖 executor 的 internal 包，executor 复用现有编译逻辑、热重载、runtime facade。

executor 不直连 Config DB；Config Service 是唯一读写方。

## 当前实施状态（骨架）

- `cmd/config/main.go`：入口，加载 `CONFIG_*` env、连 DB、建 server、graceful shutdown（SIGINT/SIGTERM）。
- `internal/config`：env 配置加载与严格校验（`CONFIG_DATABASE_URL` 限定 `postgres/postgresql` + 路径 `/tokenmp_config`，支持 host 形式与 Unix socket 形式；连接串从不入日志/错误）。
- `internal/database`：GORM 连接，AutoMigrate 禁止，schema 由 `migrations/` 版本化 SQL 管理（golang-migrate）。`Open` 返回稳定 classified sentinel 错误，driver 错误（可能含 DSN）绝不经 `Error()`/`Unwrap()` 暴露。
- `internal/repository`：读 published revision + snapshot + 原子写状态机 `Writer`。`LatestPublished` 选 `status='published'` 中 `published_at` 最新的一条，返回 raw JSON + revision + sha256。draft/archived 不暴露。失败映射为 `ErrNotFound`/`ErrQueryFailed`。`PublishRevision`/`RollbackAsNew` 先锁 `config_revisions` 行再单独 fetch snapshot（避免 LEFT JOIN `FOR UPDATE`，PostgreSQL ERROR 0A000）；`CreateDraftWithSnapshot`/`RollbackAsNew` 用 `tx.Raw(...).Scan()` 读 `RETURNING id`；`auditRow` 显式 `TableName()="config_audit_log"`。
- `internal/server`：HTTP（chi）。`GET /healthz`（liveness）、`GET /readyz`（DB ping）、`GET /v1/config/snapshots/latest`（下发 **raw `ConfigSnapshot` JSON body**——响应体即为原始快照本身，**不**包裹 `{revision,snapshot,sha256,...}` wrapper、也不走 `{code,data,message}` 错误信封；revision/sha256 只放 `X-Config-Revision`/`X-Config-SHA256` 响应头，另设 `Cache-Control: no-store`、`X-Content-Type-Options: nosniff`；存储 body 在下发前校验为单一严格 JSON 值，空/null/多值/trailing-data 一律 500 不出体）。此为 OpenAPI 契约 `getConfigSnapshotLatest` 权威格式，也是 executor `configsource.LoadFromConfigService` 严格 raw-body decoder 的消费端。写路径 `/v1/config/drafts`、`/v1/config/drafts/{id}`、`/v1/config/revisions/{id}/{publish,archive,revert}`、`/v1/config/revisions`、`/v1/config/audit` 由 admin-auth 保护。`/revert` 为契约路径（内部 `RollbackAsNew`）。错误为协议原生 JSON，不泄漏 DSN/SQL/凭据。contract↔router 双向 conformance 测试与 latest-snapshot raw-body 契约锁定测试在 `internal/server/{contract_conformance,server}_test.go`。
- `migrations/000001–000004`：Config DB schema（golang-migrate up/down）。000001 从 `infra/db` 转；000002 明文 api_key（历史）；000003 limits/routing policy；000004 加固写路径（draft `version` CAS、global singleton published partial unique index、rollback 元数据 `source_revision_id`/`rollback_note`、audit 扩展与收紧 action 枚举、secret 边界 `credential_ref` 恢复 NOT NULL+unique）并修正 000001 遗留缺陷：`config_revisions.parent_revision_id` 原为 `bigserial`（NOT NULL+序列默认），改为可选 `bigint`（DROP NOT NULL+DROP DEFAULT+删遗留序列），否则无法创建无父 draft。000004 的 api_key COMMENT 与所有 down ALTER 均用 DO block 守卫表/列存在，使 down 在干净库上幂等。000004 的 **down 为 fail-closed contract 回退**：在所有破坏性 DDL 之前的前置 preflight guard（`config_revisions.parent_revision_id IS NULL`、`version<>1`、非空 `source_revision_id`/`rollback_note`、`config_audit_log.action='rollback_publish'`、非空 `actor_kind`/`request_id`）在旧 schema 无法表达的数据存在时 RAISE EXCEPTION，经 golang-migrate 文件级默认事务整体回滚，绝不静默回填或丢历史；干净库上 down 成功。真实 PG 迁移测试在 `migrations/migrations_test.go`（`CONFIG_REPO_TEST_DSN` 未设时 SKIP）。

## 待实现（后续）

- 清理历史 AI 生成的重复 Provider 数据：同一供应商保留一个 Provider，把不同协议保留在 route/adapter/endpoint 维度；第一轮合并不重命名 `credential_ref`，避免同时改 secret/env mapping。

> executor 端的 `configsource.LoadFromConfigService`（HTTP pull raw snapshot body → 本地 `snapshot.Compile` → 发布）与配置热重载（executor SIGHUP + 可选轮询 latest revision）**已在 Executor 侧实施**，不再属于 Config Service 的待实现项。二者经本服务的 `GET /v1/config/snapshots/latest` 原始 body 契约解偶（见下）。

## 验证

```bash
# 单元测试（无需 DB）
go test ./internal/config/... ./internal/database/... ./internal/server/...

# repository 集成测试（需临时 pg，应用 up/down migration）
CONFIG_REPO_TEST_DSN="postgres:///tokenmp_config?host=/tmp&port=55433" go test ./internal/repository/...

# 全量
go test ./...

# 进程联调（启动临时 pg + apply migration + 跑 binary）
CONFIG_DATABASE_URL=... CONFIG_HTTP_ADDR=127.0.0.1:18082 go run ./cmd/config
```

- build + vet 通过。
- repository 集成测试验证：空库→`ErrNotFound`；多 published 选最新；draft 被忽略。
- process smoke test：healthz 200、readyz 200、snapshot 端点返回 revision/sha256/snapshot JSON。

## 约束

- **DO NOT** 用 `AutoMigrate`——schema 由 `migrations/` 版本化 SQL 管。
- **DO NOT** 在 Config Service 编译 snapshot——编译在 executor 端。
- **DO NOT** 让 driver 错误经 `Error()`/`Unwrap()` 暴露 DSN。
- **DO NOT** 提交密钥/连接串/生产数据——`upstream_credentials` 只存 `vault://` ref；`api_key` 列仅历史遗留，应用层永不写入明文。
- **DO NOT** 把明文 API key 写入 DB、HTTP 响应、snapshot、audit 或日志；handler 与 repository 共同拒绝明文，错误只报 "plaintext secret rejected"。
- **DO NOT** 让写/admin 端点默认开放：生产必须配置 `CONFIG_ADMIN_TOKEN_FILE`，缺配置启动 fail-fast；`CONFIG_ALLOW_NO_ADMIN_AUTH=true` 仅供本地/测试。
- **DO NOT** 修改/复活历史 published/archived revision；rollback 必须创建并发布新 revision。
- **DO NOT** 在 LEFT JOIN 的可空侧用 `FOR UPDATE`（PostgreSQL ERROR 0A000）：先锁定 `config_revisions` 行，再单独 fetch snapshot。
- **DO NOT** 用 `gorm.Exec(...).Scan()` 读 `RETURNING`（不会填充目标）；必须用 `tx.Raw(...).Scan()` 或 `Row().Scan()`。
- **DO NOT** 依赖 GORM 推断的表名：内部 struct 必须显式 `TableName()`（如 `auditRow`→`config_audit_log`），否则会找错表。
- DB 路径硬限 `/tokenmp_config`，绝不连其他库。
- 契约路径用 `/v1/config/revisions/{id}/revert`（不是 `/rollback`，见 `packages/contracts/openapi/config/v1.yaml`）；内部 repository 方法名 `RollbackAsNew` 可保留。

## 文档维护

读写路径、下发协议、编译边界、secret 边界或服务间授权变化时，同步维护本文件、`services/AGENTS.md`、`infra/db/AGENTS.md` 与 `packages/contracts/AGENTS.md`。

## Container image and Compose

- `Dockerfile` is built with the repository root as context and produces only the static
  `config` binary in a non-root Alpine runtime image. Its service-local module download runs
  with `GOWORK=off`; the shared `packages/go/httpresp` replace target is copied explicitly.
- The image health check probes `/readyz`, the HTTP and database readiness route.
- Root `compose.yaml` owns the service definition only; provide required database and key
  inputs at deploy time, and do not add shared PostgreSQL/Redis/proxy resources or secrets.

The repository-root build context means Dockerfile `COPY` sources are rooted at
`services/<service>` (with the shared `packages/go/httpresp` copied from the same root), not
at the Dockerfile directory. `tools/check-dockerfile-copy-sources.sh` statically guards this
before CI Docker builds.

Compose mounts `CONFIG_ADMIN_TOKEN_FILE` as a read-only Compose-secret path, consumed by
the Config Service `internal/config` loader (regular-file, bounded, strict secret-file
safety pattern) to source the admin shared secret at startup.
