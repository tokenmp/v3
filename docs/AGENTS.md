# Docs 分区

> 作用域：`docs/`。继承仓库根目录 `AGENTS.md`，并遵循 `.agents/documentation.md`。

## 分区职责

`docs/` 存放需要提交、共享和评审的项目文档，包括架构、ADR、接口、数据、开发和运维说明。当前包含 `adr/` 决策记录和 `ui/` 长期 UI 规范。

## 文档类型与位置

- 架构决策：`adr/`，记录 proposed、accepted、superseded 或 deprecated 状态。
- 长期项目文档：按主题建立清晰目录，避免使用模糊的 `misc/`。
- 临时 Agent 计划：放在被忽略的 `.agents/plans/`，不放入本目录。
- 私有服务器与本地上下文：放在 `.agents/local.md` 或 `*.local.md`，不得进入本目录。

## 维护规则

- 文档必须与仓库落地状态一致，区分规划、决策、实施和历史。
- ADR 被替代时标记替代关系，不静默改写历史决定。
- 代码、契约、目录或部署方式变化时，主动搜索并更新相关文档。
- 引用路径必须可验证；示例必须明确标注，不能伪装成现有事实。

## 验证

提交前检查链接、索引、旧名称、旧路径、临时分支和敏感信息。详细完成标准见 `.agents/documentation.md`。

## DO NOT

- **DO NOT** 在公共文档中记录服务器 IP、私有 SSH 用户、内部路径、凭证或实时拓扑。
- **DO NOT** 将临时计划或聊天记录直接当成正式项目文档。
- **DO NOT** 把建议写成已接受决策，或把规划写成已实施事实。
- **DO NOT** 复制同一事实到多个位置而不定义权威来源。

## 当前索引

- Monorepo 工具选型：`adr/0001-monorepo-tooling.md`
- UI Design Tokens 决策：`adr/0002-ui-design-tokens.md`
- CI 基线决策：`adr/0003-ci-baseline.md`
- Auth Service Foundation 决策：`adr/0004-auth-service-foundation.md`
- Auth Identity Flows 决策：`adr/0005-auth-identity-flows.md`
- API Contracts Package 决策：`adr/0006-api-contracts-package.md`
- Executor 架构设计基线（Foundation、Config compiler/immutable snapshot、routing、Adapter Engine、retry State、两个 non-stream SDK adapter、内部 non-stream Runner、non-stream HTTP transport 层、transport-neutral non-stream facade 前置四包（`internal/nonstream`/`internal/nonstreamfacade`/`internal/authcontext`/`internal/requestid`）、phase 7.7 config source 前置 `internal/configsource`、credential env `internal/credentialenv`、identity env `internal/identityenv` 与模块内 quarantine bridge `internal/quarantinebridge`（把 runtime quarantine port/state 适配为 routing quarantine reader，fail-closed，经 `internal/composition` 接入 runtime facade）均已实施并经 user-authorized runtime composition（`internal/composition`）接入公开 runtime server/路由（生成 7 条路由均为运行时实际路由，启动拒绝不受支持的 enabled SDK/protocol route）；credential env uses a strict secret-free vault-ref → `EXECUTOR_CREDENTIAL_*` map, per-attempt lookup/rotation, opaque secrets, and `ValidateCompiled` exact enabled-ref startup validation; map JSON/secret env values are not committed; identity env is wired into `main`/routes via composition and reads opaque API-key env per auth lookup (no Auth JWT); composition-level route conformance 与 process binary test 已落地；Runner/transport 层/config 文件源/credential/identity env/quarantine bridge 均不提供 wire-attempt proof 或跨进程 exactly-once；durable idempotency/replay、config source 热重载 loop、remote resolver、`Retry-After` 解析、public/provider streaming driver 与 transport/composition stream 接线、Responses 执行（路由仍 501）未实施；legacy Images completion-only non-stream 已经 registry/composition/transport 接入 runtime）：`executor/architecture.md`
- Executor 测试策略（Foundation、compiler/snapshot、routing、Adapter Engine、retry State、两个 non-stream SDK adapter、内部 Runner、non-stream HTTP transport 层、transport-neutral facade 前置四包、模块内 config 文件源 `internal/configsource`、credential/identity env 与模块内 quarantine bridge `internal/quarantinebridge`（fail-closed 适配 runtime quarantine port/state 到 routing reader，经 composition 接入 runtime facade）的包测试已实施；runtime composition-level route conformance（枚举 OpenAPI 契约全部 operation，经全包装 handler 断言匿名/鉴权状态，不依赖 chi.Walk）与 process binary test（实际进程 health、unauth chat 401、鉴权空配置 chat 404 与 501 route、invalid 配置证明未 bind 监听器）已落地；durable idempotency/replay、config source 热重载、`Retry-After` 解析、remote quota/credential resolver、stream commit 与其余协议阶段测试未实施）：`executor/testing-strategy.md`
- Executor Phase 10 HTTP streaming：Chat/Messages `stream:true` 已由 generated hybrid plain+strict handler、SSE sink 和 composition 接入 runtime；auth 在 capture/body read 前，pre-commit 是协议 JSON、post-commit 无 fallback；OpenAI `[DONE]`、Anthropic native event framing、StreamDriver+exact SDK registry 已接线。无 HTTP atomicity/wire proof/public-provider E2E，Responses 仍 501；Images legacy non-stream 已执行：`executor/architecture.md`、`executor/testing-strategy.md`
- Executor Phase 11 Images HTTP：official `openai-go/v3` legacy Images Generate 已以 completion-only non-stream capability 注册到 execution registry、composition 与鉴权 `POST /v1/images/generations` runtime；facade 精确委托 Runner，Runner 一次 quota reservation 并按冻结策略 retry。SDK/transport 共用 `internal/imagecontract`：untrimmed nonempty prompt、1 MiB prompt、512-byte CTL-free user、default wire/renderer `url`、16 MiB wire/10 MiB item/12 MiB aggregate、URL/streaming base64、usage/extensions/revised-prompt 边界；所有 Images 终态 `Cache-Control: no-store`。不支持 GPT Image 特有参数或 usage quota；仅 `/v1/models` 与 `/v1/responses` 保持鉴权 501；CI race 显式包含独立包 `./internal/imagecontract/...`：`executor/architecture.md`、`executor/testing-strategy.md`
- Executor Phase 12.1 typed quota domain：`internal/quota.Repository`、`DomainInMemory`、`TypedMock` 与 shared contract/race/fuzz coverage 已实施；typed domain state 与仍被 Runner、StreamDriver、runtime 消费的 legacy `quota.Port` state 有意分离。Phase 12.2 必须迁移消费者并删除 legacy Port；尚无 usage charging、数据库或 durable storage：`executor/architecture.md`、`executor/testing-strategy.md`
- UI 设计规范：`ui/design-system.md`

新增、替代或删除文档时同步维护本索引或相应主题索引。
