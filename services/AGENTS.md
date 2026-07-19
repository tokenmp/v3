# Services 分区

> 作用域：`services/`。继承仓库根目录 `AGENTS.md`。

## 分区职责

`services/` 用于可独立开发、测试、构建和部署的后端服务。当前服务清单：

- `auth/`：TokenMP v3 认证服务，Go 1.26.5、Chi、GORM、PostgreSQL（库 `tokenmp_auth`）。已实现 Auth Identity Flows：注册/登录、Ed25519/EdDSA Access Token 签发、opaque Refresh Token 轮换与 reuse 检测、logout/logout-all、/me、Argon2id 密码哈希与 bcrypt 兼容升级；速率限制未实现，为部署阻塞项（见 `auth/AGENTS.md` 与 ADR 0005）。模块文档：`auth/AGENTS.md`。

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

具体语言、框架和命令由每个服务定义。Node.js 服务应接入根 pnpm/Turborepo 任务；Go 服务保持独立 module 边界。仓库已引入 `go.work`（`use ./services/auth`），首个 Go module 为 `github.com/tokenmp/v3/services/auth`；后续 Go 服务通过独立变更新增 `go.work` 条目与模块级 `AGENTS.md`。

## DO NOT

- **DO NOT** 为尚未实施的服务建立空目录或虚假接口——通过独立变更渐进添加。
- **DO NOT** 使用共享数据库或源码 import 替代服务契约——保持数据与部署自治。
- **DO NOT** 在服务中提交密钥和环境专属配置——只提交模板与验证规则。

## 文档维护

创建、移动或删除服务时，同步更新当前服务清单、根 `AGENTS.md` 及提供方和消费者的依赖记录。
