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
- API Contracts：`packages/contracts/AGENTS.md`（语言中立跨程序 API 协议唯一事实来源；Auth 与 Executor OpenAPI 契约分别位于 `openapi/auth/v1.yaml` 与 `openapi/executor/v1.yaml`；oapi-codegen v2.8.0 为两个 Go 服务生成 models/strict server。Auth 与 Executor conformance tests 是已实施的直接消费者/验证方。）
- Auth Service：`services/auth/AGENTS.md`（Go 1.26.5，首个 Go module，`go.work` 已创建）。已实现 Auth Identity Flows：注册、登录、Ed25519/EdDSA Access Token、opaque Refresh Token 轮换与 reuse 检测、logout/logout-all、/me、Argon2id 密码哈希与 bcrypt 兼容升级。Auth 实现与测试必须符合 `@tokenmp/contracts` 的协议，属于设计/构建时契约依赖，不是 Go runtime import；消费者不得读取 Auth 源码发现 API。API 路由由 oapi-codegen 生成的 Chi strict handler 注册（contract-first）。
- Executor Service Foundation：`services/executor/AGENTS.md`（Go 1.26.5 module `github.com/tokenmp/v3/services/executor`，已加入 `go.work`）。已实施 HTTP health、运行时配置、优雅关闭、Mock/InMemory ports、quota reservation terminal、generated models/strict server、adapter skeleton、Config compiler、immutable generation-aware snapshot store、三份脱敏 config fixtures、C01–C27 compiler/snapshot 安全校验与真实编译测试、routing selector/resolver/Plan/quarantine、纯 Go Adapter Engine，以及 route/HTTP conformance tests。Engine 以严格 JSON 和有限 DSL 执行全部 request actions；header/query 仅接受 literal，`ValueRef` 继续拒绝，并以选中 model 的 thinking bounds 限制映射。它安全映射 response（已编译顺序、维度间 AND/维度内 OR 与固定 default），具备 atomic/mutation/race/fuzz 覆盖，但未接入 pipeline。routing 的公开 Candidate/Plan 不含 credential ref 或 secret：仅安全 credential ID/priority；`Plan.Next` 只定义候选范围，不是 RetryDecider。compiler 在 `services/executor/internal/snapshot.Compile` → `internal/adapter.Compile` 中校验/normalization，使用默认值 `2m/45s/30s/10m/200ms/3/2/90s`（request/TTFT/idle/lifetime/backoff/total attempts/same-target attempts/total retry duration），并可按 generation/revision 原子发布不可变 compiled snapshot；尚未接入 runtime config source 或请求 pipeline。已实施内部 shared `sdk` port 及官方 `github.com/openai/openai-go/v3` **v3.44.0** 的 OpenAI Chat Completions 非流式 adapter：每次调用独立校验 HTTPS target、上游 model 与 secret，SDK retry=0、禁止 redirect；对 Chat 请求执行严格契约验证（含 tools、vision 与 thinking 字段），安全注入与唯一 Bearer 鉴权，记录不含 URL/请求/响应/凭据的 attempt observer metadata；成功返回安全 request ID/status metadata，失败将 timeout、transport、protocol 与 HTTP 状态安全分类。TLS `httptest` 覆盖 target/header 隔离、retry/redirect、分类与无泄漏。仍未实施 credential resolver/secret injector、RetryDecider/attempt budget、pipeline/runtime 路由；该 SDK 能力未接入 pipeline 或 runtime routing，因此不是端到端业务能力。Responses、Images、stream、Anthropic SDK/provider、数据库、Docker 或独立 Executor CI job 仍未实施。generated files 随变更提交，`check:generated:executor` 是现有 `go-auth` CI job 中的 CI 门禁步骤。

新增、移动或删除模块时，必须同步维护根索引、分区 `AGENTS.md` 和模块 `AGENTS.md`。

## 本地私有上下文

- 可选本地入口：`.agents/local.md`（存在时读取，不提交到 Git）

公开规则与本地私有上下文冲突时，先通过仓库现状或实时只读检查验证；涉及架构、安全或破坏性操作且无法确认时，停止并询问用户。
