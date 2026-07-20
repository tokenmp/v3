# @tokenmp/contracts

TokenMP v3 跨程序 API 协议唯一事实来源。

## 目的

此 package 以语言中立的 OpenAPI 契约声明所有服务的 HTTP 接口。消费者只需读取此 package 中的契约即可理解 API 行为，**无需读取任何服务的私有源码、数据库模型、migration 或运行时配置**。

## 依赖方向

```text
services/auth ──设计/构建时契约依赖──▶ @tokenmp/contracts
Auth conformance test ──直接加载验证──▶ @tokenmp/contracts
未来消费者  ──依赖──▶ @tokenmp/contracts
@tokenmp/contracts     ──不依赖──▶ 任何 service 或 app
```

## 当前契约

| 契约 | 服务 | 版本 | 路径 |
|---|---|---|---|
| Auth API | Auth Service | v1 | `openapi/auth/v1.yaml` |
| Executor API | Executor Service | v1 | `openapi/executor/v1.yaml` |

## 消费者须知

- **不得**读取 `services/auth` 源码、GORM 模型、migration 或数据库结构来发现 API。
- **必须**以 `openapi/auth/v1.yaml` 作为 Auth API 的唯一权威来源。
- 契约只描述可观察的 HTTP 行为、安全语义、幂等性和错误契约。
- 契约不包含服务内部实现细节（hash 算法、数据库列名、事务策略等）。

## 安装

```bash
pnpm install
```

## 脚本

```bash
pnpm --filter @tokenmp/contracts lint               # YAML 结构 + 禁止内部术语
pnpm --filter @tokenmp/contracts typecheck           # 跨文件 operationId 唯一性 + $ref 解析
pnpm --filter @tokenmp/contracts test                # Node test runner 契约测试
pnpm --filter @tokenmp/contracts build               # 复制契约到 dist/
pnpm --filter @tokenmp/contracts generate:auth:go    # 生成 Auth Go server 代码
pnpm --filter @tokenmp/contracts generate:executor:go # Executor 模块实施后生成 Go server 代码
pnpm --filter @tokenmp/contracts check:generated     # 验证 Auth 生成物新鲜度
pnpm --filter @tokenmp/contracts check:generated:executor # Executor 模块及生成物实施后验证
```

## Go 生成治理

Auth Go 边界由固定的 oapi-codegen v2.8.0 和两份明确配置确定性生成：

- `go/auth-v1-models.yaml` → `internal/contract/authv1/models.gen.go`（仅 models）
- `go/auth-v1-server.yaml` → `internal/contract/authv1/server.gen.go`（Chi + strict server；官方 `skip-prune: true` 引用同 package models，不重复生成）

两个文件均提交、带源契约和 `DO NOT EDIT` 头；`check:generated` 在临时目录重生成并逐一字节比较，且拒绝旧 `generated.go`。GitHub 将 `*.gen.go` 标记为 generated，默认折叠；评审先看 OpenAPI、adapter、测试与 freshness 结果。

为保持 diff 可审阅，生成器升级、API 行为变更和 OpenAPI 全文格式化必须分开提交；`operationId` 与 schema 名是稳定生成标识，禁止无意义重命名或重排。

## 添加新契约

1. 在 `openapi/<service>/v1.yaml` 创建新契约文件。
2. 契约只描述消费者可观察的 HTTP 行为，不暴露实现细节。
3. 在 `package.json` 的 `exports` 中添加入口。
4. 更新此 README 的当前契约表。
5. 更新 `AGENTS.md` 和相关索引文档。
6. 运行 `lint`、`typecheck`、`test`、`build` 验证。

## 依赖

- **运行时**：零第三方依赖。`dist/` 中的 YAML 契约可被任何语言直接消费。
- **开发期**：`yaml` npm 包（devDependency）用于验证脚本真正解析 YAML，不随 `dist/` 发布。
- **Go 代码生成**：oapi-codegen v2.8.0（通过 `go run @version` 下载，不作为 module 依赖）；通过 `output-options.type-mapping` 配置将 `format: email` → `string`、`format: uuid` → `uuid.UUID`（`github.com/google/uuid`），避免 `oapi-codegen/runtime` 依赖。`additional-imports` 提供 `uuid` 别名导入。

## 架构决策

- `docs/adr/0006-api-contracts-package.md`
