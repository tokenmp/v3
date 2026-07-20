# Services 分区

> 作用域：`services/`。继承仓库根目录 `AGENTS.md`。

## 分区职责

`services/` 用于可独立开发、测试、构建和部署的后端服务。当前服务清单：

- `auth/`：TokenMP v3 认证服务，Go 1.26.5、Chi、GORM、PostgreSQL（库 `tokenmp_auth`）。已实现 Auth Identity Flows：注册/登录、Ed25519/EdDSA Access Token 签发、opaque Refresh Token 轮换与 reuse 检测、logout/logout-all、/me、Argon2id 密码哈希与 bcrypt 兼容升级；API 契约已抽离至 `packages/contracts/openapi/auth/v1.yaml`（见 ADR 0006），API 路由由 oapi-codegen 生成的 Chi strict handler 注册（contract-first），Auth conformance test（`internal/server/contract_test.go`）是当前唯一已实施的直接消费者/验证方。模块文档：`auth/AGENTS.md`。
- `executor/`：TokenMP v3 模型请求执行服务 Mock-first Foundation，Go 1.26.5 module `github.com/tokenmp/v3/services/executor`。已实施 HTTP health、运行时配置、优雅关闭、Mock/InMemory ports、quota reservation terminal、generated models/strict server、adapter skeleton、Config compiler、immutable generation-aware snapshot store、三份脱敏 config fixtures、C01–C27 compiler/snapshot 安全校验与真实编译测试，以及 route/HTTP conformance tests。Config compiler 在 `internal/snapshot.Compile` → `internal/adapter.Compile` 中执行校验与 normalization；默认值为 `2m/45s/30s/10m/200ms/3/2/90s`（request/TTFT/idle/lifetime/backoff/total attempts/same-target attempts/total retry duration），结果可按 generation/revision 原子发布不可变 snapshot；尚未接入运行时配置源或请求 pipeline。公开模型业务路由不注册到 runtime，业务执行未实现；仍无数据库、SDK、Docker 或独立 Executor CI job。generated files 随变更提交，`check:generated:executor` 是现有 `go-auth` CI job 中的门禁步骤。模块文档：`executor/AGENTS.md`。

## 新增模块准入

新增 `services/<name>/` 前必须确认：

- 服务职责、非职责范围和所有者。
- 公开及内部契约、调用方和返回/错误语义。
- 数据库、schema、migration 和外部资源所有权。
- 上下游依赖、鉴权、超时、重试和幂等要求。
- 测试、健康检查、镜像和部署边界。
- 模块 README 和模块级 `AGENTS.md`。

## 依赖边界

- 服务可以依赖边界明确的 `packages/*` 公开入口。
- 服务间通过已确认且可版本化的网络或事件契约通信。
- 服务不得导入其他服务的私有源码。
- 服务不得直接访问其他服务拥有的数据库或运行其 migration。

## 开发与验证

具体语言、框架和命令由每个服务定义。Node.js 服务应接入根 pnpm/Turborepo 任务；Go 服务保持独立 module 边界。仓库已引入 `go.work`（`use ./services/auth`、`./services/executor`）；Go modules 为 `github.com/tokenmp/v3/services/auth` 和 `github.com/tokenmp/v3/services/executor`；后续 Go 服务通过独立变更新增 `go.work` 条目与模块级 `AGENTS.md`。

## DO NOT

- **DO NOT** 为尚未实施的服务建立空目录或虚假接口——通过独立变更渐进添加。
- **DO NOT** 使用共享数据库或源码 import 替代服务契约——保持数据与部署自治。
- **DO NOT** 在服务中提交密钥和环境专属配置——只提交模板与验证规则。

## 文档维护

创建、移动或删除服务时，同步更新当前服务清单、根 `AGENTS.md` 及提供方和消费者的依赖记录。
