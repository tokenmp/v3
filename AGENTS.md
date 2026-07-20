# AGENTS.md

开始开发、部署、排障或数据操作前，请按任务需要读取以下文档。进入具体模块工作时，必须从仓库根目录到目标目录逐层读取适用的 `AGENTS.md`。

如果 `.agents/local.md` 存在，先读取其中与任务相关的本地私有上下文；该文件不属于仓库公共事实来源，且不得要求其他环境必须存在。

## 项目规则

- 文档治理规范：`.agents/documentation.md`
- Agent 临时计划约定：`.agents/plans/README.md`
- Monorepo 架构与迁移规则：`.agents/monorepo.md`
- 模块级 AGENTS.md 模板：`.agents/templates/module-AGENTS.md`
- Docker 规范：`.agents/docker.md`
- Git 开发流程：`.agents/gitflow.md`
- 操作约束：`.agents/operations.md`

## Monorepo 分区

- Apps：`apps/AGENTS.md`
- Services：`services/AGENTS.md`
- Packages：`packages/AGENTS.md`
- Infra：`infra/AGENTS.md`
- Tools：`tools/AGENTS.md`
- Docs：`docs/AGENTS.md`

## 已实施模块

- UI Design Tokens：`packages/ui-tokens/AGENTS.md`
- API Contracts：`packages/contracts/AGENTS.md`（语言中立跨程序 API 协议唯一事实来源；Auth OpenAPI 契约位于 `openapi/auth/v1.yaml`；Executor OpenAPI 契约位于 `openapi/executor/v1.yaml`；Auth conformance test 是当前唯一已实施的直接消费者/验证方；oapi-codegen v2.8.0 从 Auth 契约生成 Go server 代码，输出到 `services/auth/internal/contract/authv1/{models,server}.gen.go`）
- Auth Service：`services/auth/AGENTS.md`（Go 1.26.5，首个 Go module，`go.work` 已创建）。已实现 Auth Identity Flows：注册、登录、Ed25519/EdDSA Access Token、opaque Refresh Token 轮换与 reuse 检测、logout/logout-all、/me、Argon2id 密码哈希与 bcrypt 兼容升级。Auth 实现与测试必须符合 `@tokenmp/contracts` 的协议，属于设计/构建时契约依赖，不是 Go runtime import；消费者不得读取 Auth 源码发现 API。API 路由由 oapi-codegen 生成的 Chi strict handler 注册（contract-first）。

新增、移动或删除模块时，必须同步维护根索引、分区 `AGENTS.md` 和模块 `AGENTS.md`。

## 本地私有上下文

- 可选本地入口：`.agents/local.md`（存在时读取，不提交到 Git）

公开规则与本地私有上下文冲突时，先通过仓库现状或实时只读检查验证；涉及架构、安全或破坏性操作且无法确认时，停止并询问用户。
