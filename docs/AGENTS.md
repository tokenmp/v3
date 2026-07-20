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
- Executor 架构设计基线（设计已确认；Foundation 已实施，SDK/adapter/stream 等后续能力未实施）：`executor/architecture.md`
- Executor 测试策略（Foundation 测试已实施；后续阶段测试未实施）：`executor/testing-strategy.md`
- UI 设计规范：`ui/design-system.md`

新增、替代或删除文档时同步维护本索引或相应主题索引。
