# Packages 分区

> 作用域：`packages/`。继承仓库根目录 `AGENTS.md`。

## 分区职责

`packages/` 只存放具有已确认跨模块消费者、稳定边界和明确所有权的共享代码、契约、客户端或工程配置。

## 当前 package

- `ui-tokens/`：框架无关 UI Design Tokens，以及 Tailwind CSS v4 和 shadcn CSS integration；Web、Admin 是已确认但尚未实施的未来消费者。
- `contracts/`：语言中立跨程序 API 协议唯一事实来源（`@tokenmp/contracts`）；当前包含 Auth Service v1 OpenAPI 契约；oapi-codegen v2.8.0 从 Auth 契约生成 Go models 与 server 代码。Auth 实现与测试必须符合此 package 的协议，属于设计/构建时契约依赖，不是 Go runtime import；Auth conformance test（`services/auth/internal/server/contract_test.go`）是当前唯一已实施的直接消费者/验证方；未来消费者（Web/Admin/Gateway）将通过此 package 发现 API，不得读取服务源码。

## 新增模块准入

新增 `packages/<name>/` 前必须记录：

- 公开入口、输入、返回、错误和副作用。
- 稳定性等级与契约事实来源。
- 每个直接消费者及其具体使用功能。
- 为什么该能力不应保留在单一 app/service 内。
- 版本影响和全部消费者验证方式。
- README 和模块级 `AGENTS.md`。

## 依赖边界

- Package 不得反向依赖具体 app 或 service。
- Package 之间不得形成循环依赖。
- 共享契约优先使用语言中立 schema 作为跨语言事实来源。
- 公开入口之外的内部文件不得被消费者深层导入。

## 开发与验证

每个 package 必须提供真实的 lint、typecheck、test 和 build 脚本，并接入根任务图。公开契约变化后必须验证所有直接消费者。

## DO NOT

- **DO NOT** 创建无边界的 `common`、`shared` 或 `utils` 大杂烩——按稳定职责命名和拆分。
- **DO NOT** 为消除少量重复而过早抽象——先证明存在真实消费者。
- **DO NOT** 共享 ORM client、repository 或服务私有业务逻辑——不得绕过服务与数据边界。
- **DO NOT** 修改返回契约后只验证 package 自身——同步验证全部消费者。

## 文档维护

创建、移动或删除 package 时，同步更新当前清单、根 `AGENTS.md`、公开契约以及提供方/消费者双方的依赖表。
