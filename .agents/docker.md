# TokenMP v3 Docker 规范

## 资源命名

所有 TokenMP v3 Docker 资源必须包含明确的 `tokenmp-v3` 标识，避免与历史版本或其他项目混用。

- Compose project：固定为 `tokenmp-v3`。
- Compose 文件应设置顶层项目名：

  ```yaml
  name: tokenmp-v3
  ```

- 如果命令行指定项目名，只能使用：

  ```bash
  docker compose -p tokenmp-v3 build
  docker compose -p tokenmp-v3 up -d
  ```

  是否需要 sudo 由目标环境的权限规则决定。

- 镜像名：`tokenmp-v3-<service>:<tag>`，例如 `tokenmp-v3-auth:latest`。
- 容器名：优先使用 Compose 自动生成的 `tokenmp-v3-<service>-<index>`。
- 自定义网络名：`tokenmp-v3-<purpose>`。
- 命名卷：`tokenmp-v3-<purpose>`。
- 不得构建或启动 `tokenmp-auth`、`tokenmp-api`、`tokenmp-executor` 等缺少 v3 标识的新资源。
- 不要设置缺少 v3 标识的 `container_name`。

## 构建与部署

- Monorepo 中每个可部署服务使用独立镜像。
- 禁止制作包含所有服务的单一超级镜像。
- 构建前核对 Compose project、服务名、镜像名和 build context。
- 不得用模糊名称或跨项目批量命令执行构建、部署和清理。
- 不得因为使用 Monorepo 而把全部源码无条件复制到每个镜像。
- 每个服务应有独立健康检查，并验证实际服务能力而不只是进程存在。
- 私有服务器、端口、SSH、部署路径和运行状态由本地私有文档提供，不应写入公开规则。

## 清理约束

- 清理前核对 Compose project、working directory、mount、network 和 volume。
- 未确认数据归属及备份前，不使用 `docker compose down -v`、`docker volume rm` 或等价破坏性命令。
- 公共数据库、缓存、代理和可观测性组件不得作为单个应用的附属资源清理。
- 镜像、容器、网络和卷应按完整 v3 标识精确选择，禁止通过宽泛 grep 结果直接批量删除。

## 验证

Docker 相关变更至少验证：

```bash
docker compose -p tokenmp-v3 config
docker compose -p tokenmp-v3 build
docker compose -p tokenmp-v3 ps
```

并检查实际资源标签：

- `com.docker.compose.project=tokenmp-v3`
- service 与预期一致
- working directory 与目标环境一致
- 镜像名称带 `tokenmp-v3` 标识
- 端口、网络和挂载没有与其他项目冲突

## Compose 编排

根目录 `compose.yaml` 是全部 7 个 Go 服务与 Web 的公共应用编排；它设置
`name: tokenmp-v3`，只创建 `tokenmp-v3-backend` 应用网络。PostgreSQL、Redis、
OpenResty/其他反向代理均为外部共享基础设施，不得加入此 Compose 文件或随项目
启动、停止、删除。

- 每项服务使用独立 `tokenmp-v3-<service>:${TOKENMP_V3_IMAGE_TAG:-latest}` 镜像；不要设置
  `container_name`，让 Compose 生成带 project 标识的容器名。
- Web 的 Compose 默认 build 必须使用 `apps/web/Dockerfile.web` 和仓库根 context：在镜像内
  以固定 Node/pnpm 版本从 clean checkout 构建 Contracts、UI Tokens、Next standalone。不得把
  服务器裸机 Node/pnpm、预构建 `.next` 或可选未跟踪 `apps/web/public` 当作部署前提；旧
  artifact-only Dockerfile 不得成为 Compose 默认。
- 所有 Go 服务的 builder 在首次 `go mod download` 前声明并导出 `GOPROXY`；默认值固定为
  `https://proxy.golang.org,direct`，使后续 `go build` 按需下载也使用同一值。Compose 将
  `TOKENMP_V3_GO_PROXY` 作为该 build arg 的环境可覆盖入口；不得把环境特定模块代理写为公共默认。
  该输入仅用于镜像构建，不会进入运行时服务环境。
- Next `NEXT_PUBLIC_*` 是 build-time public inputs，必须在 Compose `build.args` 传入，且只可
  包含公开 flags/base URL；它们会固化到 bundle，运行时 environment 无法覆盖。默认同源 base URL
  为空，dev 部署 mock flags 为 `0`。
- 数据库 DSN、私钥、JWT 公钥路径和 Executor credential mapping 都由必填环境变量提供。
  JWT 文件以只读 bind mount 注入，绝不复制进镜像或提交 `.env`。Compose 未声明业务数据卷。
- 所有 Go 服务都在内部网络监听；仅 Edge `3002` 与 Web `3100` 默认发布到宿主，且可通过
  `TOKENMP_V3_API_HOST_PORT`、`TOKENMP_V3_WEB_HOST_PORT` 覆盖。共享反向代理应仅连接这些
  明确发布的入口，不属于此项目管理范围。
- Compose 不猜测宿主网关：外部 DB/基础设施端点必须由 DSN/URL 环境变量明确提供。跨 macOS
  Docker Desktop 与 Linux 的可移植方式是使用两端都可解析且可路由的基础设施 DNS 名称；若必须
  使用宿主服务，则环境专属的受控 override 文件可明确设置 Linux `host-gateway` 或 Docker Desktop
  的 `host.docker.internal`，该 override 不提交。先以 `docker compose --env-file /tmp/... config`
  验证渲染结果，再启动。

建议的非破坏性渲染检查（临时 env 文件不得入库）：

```bash
docker compose --env-file /tmp/tokenmp-v3.compose.env -p tokenmp-v3 config
docker compose -p tokenmp-v3 build
docker compose -p tokenmp-v3 ps
```

### Feature-branch secure runtime inputs

Compose declares the integration contract used after the named feature branches merge. It
never creates Redis or PostgreSQL; Redis is externally owned and its address is supplied by
the target environment. HMAC and service tokens are source files mounted only as read-only
Compose secrets, never interpolated as environment values.

- After `shared-rate-limit` merges, Auth and API consume their exact
  `*_RATE_LIMIT_*` variables: enabled flag, external Redis address/DB, trusted-proxy CIDRs,
  HMAC secret-file path, policy capacities/refills, and bucket TTL. Compose intentionally
  has no `AUTH_REDIS_URL`, `API_REDIS_URL`, `AUTH_HMAC_SECRET_FILE`, or
  `API_HMAC_SECRET_FILE` aliases.
- `CONFIG_ADMIN_TOKEN_FILE` already names the Config secret path in the Config feature
  branch. API and Billing require their corresponding business changes to consume
  `API_CONFIG_SERVICE_TOKEN_FILE` and `BILLING_LOGGING_SERVICE_TOKEN_FILE`; their current
  feature branches only accept string token variables. Do not substitute a secret-file path
  into those string variables, use an entrypoint wrapper, or put token contents in Compose
  environment/config output.
- `BILLING_LOGGING_URL` targets the internal Logging container after
  `billing-settlement` merges; its sweeper settings are explicit in Compose.

`tools/check-compose-env-contract.sh` is the repository-local allowlist check for this
cross-branch contract. It is self-contained so CI does not depend on sibling worktrees.

CI additionally performs build-only clean-context image gates; Go image builds explicitly pass
`GOPROXY=https://proxy.golang.org,direct`, and the Web gate uses `apps/web/Dockerfile.web` without
a Go proxy argument. No image is run or pushed. The Dockerfile guard checks Go `GOPROXY` declaration/
export ordering and Compose build-arg coverage, as well as clean-context COPY inputs and exclusion of
optional `apps/web/public` and prebuilt `.next`.
