# Tools 分区

> 作用域：`tools/`。继承仓库根目录 `AGENTS.md`。

## 分区职责

`tools/` 用于仓库级自动化、代码生成、检查、迁移辅助和开发工具。当前工具模块清单为空。

## 新增模块准入

新增工具前必须说明：

- 使用场景、调用者和输入输出。
- 文件、代码、数据库或基础设施副作用。
- 幂等性、失败行为和回滚方式。
- 所需权限及敏感信息边界。
- 测试和验证命令。
- README，以及具有独立边界时的模块级 `AGENTS.md`。

## 依赖边界

- 工具可以读取 workspace metadata，但不得成为业务运行时的隐式依赖。
- 生成器应以正式 schema 或配置为事实来源。
- 生成产物必须可识别，并通过规定命令再生。
- 工具不得绕过模块边界直接修改不属于其作用域的数据。

## 开发与验证

工具必须支持安全的帮助、检查或 dry-run 方式；具有副作用的命令应在执行前展示目标和影响范围。

- `generate-jwt-keys.sh <output-directory>`：显式在 deployment-owned directory 生成 Ed25519
  Auth signing pair，拒绝覆盖已有文件，将 private/public 分别设为 `0600`/`0644`，并以 `openssl`
  解析验证后只输出路径、不输出 PEM。仅 Auth 挂载 private；API、Executor、Notice 共享 public。
- `seed-config.sh <config-admin-base-url> [seed-json]`：需要显式
  `CONFIG_ADMIN_TOKEN_FILE`、`curl` 与 `jq`，从无 secret 的 `seed-config.example.json` 建立一个
  provider/model/`vault://` credential/route 并调用 admin compile 发布。它拒绝 placeholder、非 HTTPS
  upstream URL 与非 `vault://` ref；不会接受或打印 upstream API key。仅在已确认的目标环境、已迁移的
  空 Config DB 和受控 admin endpoint 上运行。完整步骤见 `../docs/deployment.md`。

- `check-dockerfile-copy-sources.sh`：静态验证全部 Dockerfile 的首行恰有一个
  `syntax=docker/dockerfile` parser directive；并验证全部可部署 Go 服务的 Dockerfile 在仓库根
  build context 下只从其 `services/<service>` 目录及允许的共享 Go package COPY，且每个源路径
  存在；同时从服务 `go.mod` 读取 local replace，要求其目标目录在 download/build 层显式 COPY。
  它还要求每个 Go builder 在首次 `go mod download` 前恰有一个公共默认 `ARG GOPROXY` 与
  `ENV GOPROXY=${GOPROXY}`，并要求 Compose 的七项 Go service 传递可覆盖的 build arg（Web
  不传）。它不调用 Docker、无副作用，并由 CI 在镜像 build 前执行。CI 另以 `GOWORK=off go build
  -mod=readonly ./cmd/<service>` 验证每个服务入口的独立 module 闭包。
- `check-compose-env-contract.sh`：静态检查根 `compose.yaml` 的跨分支环境变量 allowlist，
  拒绝过时别名并要求 token/HMAC 只通过 `/run/secrets` 文件路径传递。它不读取 secret
  内容、不调用 Docker、无副作用；CI 在 Compose render 前执行。它也要求 Web server-side
  `AUTH_API_BASE` 固定默认 `http://auth:8080`，避免 login BFF 404。

## DO NOT

- **DO NOT** 创建会静默覆盖用户改动的脚本——检测冲突并明确失败。
- **DO NOT** 默认连接生产环境或读取生产凭证——环境必须显式选择。
- **DO NOT** 提交一次性调试脚本作为长期工具——有复用边界后再治理入库。
- **DO NOT** 手工修改生成文件而不更新生成来源和验证流程。

## 文档维护

工具的参数、输出、副作用或调用方变化时，同步更新本文件、工具文档及受影响模块的验证说明。
