# API Contracts

> 作用域：`packages/contracts/`。继承仓库根目录与 `packages/AGENTS.md`。

## 模块职责

- 负责：TokenMP v3 跨程序 API 协议唯一事实来源。以语言中立的 OpenAPI 契约声明所有服务的 HTTP 接口，供消费者发现和验证 API 行为，无需读取任何服务的私有源码、数据库模型或 migration。
- 不负责：服务实现、客户端代码生成（TS 客户端生成尚未实施）、数据库 schema、运行时逻辑或部署配置。
- 所有者：TokenMP 后端基础设施。

## 必读文档

- 模块说明：`README.md`
- 架构决策：`../../docs/adr/0006-api-contracts-package.md`
- Auth 契约：`openapi/auth/v1.yaml`
- Go 生成配置：`go/auth-v1-models.yaml`、`go/auth-v1-server.yaml`
- Go 生成脚本：`go/generate.sh`
- Go 新鲜度检查：`go/check-generated.sh`

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `@tokenmp/contracts/openapi/auth/v1.yaml` | YAML 读取能力 | Auth Service v1 OpenAPI 3.0.3 契约；无副作用 | stable | `openapi/auth/v1.yaml` |
| `generate:auth:go` 脚本 | Go 1.26.5+，oapi-codegen v2.8.0（自动下载） | 生成 `models.gen.go` 与 `server.gen.go` | stable | 两份 `go/auth-v1-*.yaml` + `openapi/auth/v1.yaml` |
| `check:generated` 脚本 | Go 1.26.5+ | 临时目录重生成并 byte compare；exit 0=新鲜，exit 1=过期 | stable | 同上 |

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | Node.js | 构建和契约验证 | 开发工具 | `scripts/*.mjs` | package 全部检查 |
| 依赖 | `yaml` npm 包 | 验证脚本真正解析 YAML | devDependency | `scripts/validate.mjs` | `lint` + `typecheck` |
| 依赖 | Go 1.26.5+ | oapi-codegen 代码生成 | 开发工具（`go run @version`） | `go/generate.sh` | `check:generated` + `go test` |
| 被依赖 | `services/auth` | Auth 实现遵循此契约 | 设计/构建时契约依赖 + 生成代码 | `openapi/auth/v1.yaml` → `{models,server}.gen.go` | Auth conformance test + 生成物新鲜度测试 + 集成测试 |

未来消费者（Web/Admin/Gateway）将通过此 package 发现 API，但尚未实施，不列入依赖表。Auth conformance test（`services/auth/internal/server/contract_test.go`）是当前唯一已实施的直接消费者/验证方：它在无数据库环境下加载此契约，用 kin-openapi 解析并 Validate，从 Chi 实际路由提取所有 HTTP method+path，与契约双向严格比较。

依赖方向：Auth 实现与测试必须符合 contracts 的协议，属于设计/构建时契约依赖，不是 Go runtime import；contracts 不依赖 Auth。消费者只依赖 contracts，不依赖 Auth 源码。

## Go 代码生成

### 工具链

- **oapi-codegen v2.8.0**：从 OpenAPI 3.0.3 YAML 分别生成 Go models 与 Chi/strict server 代码
- **oapi-codegen type-mapping**：通过 `output-options.type-mapping` 配置将 `format: email` → `string`、`format: uuid` → `uuid.UUID`（`github.com/google/uuid`），避免 `oapi-codegen/runtime` 依赖；`additional-imports` 提供 `uuid` 别名导入
- 生成方式：`go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0`，不污染任何 module 的依赖图
- 版本固定在 `go/generate.sh` 脚本中；变更需同步更新 auth `go.mod` 的 `github.com/google/uuid` 依赖

### 生成物

- 输出：`services/auth/internal/contract/authv1/models.gen.go`（仅 models）和 `server.gen.go`（Chi handler、StrictServerInterface、strict response types），均含 `DO NOT EDIT` 头。server 配置以官方 `skip-prune: true` 复用同 package models，不重复生成。
- 提交进仓库；`dist/.turbo` 不提交

### 新鲜度保障

- `go/check-generated.sh`：临时目录重生成 + byte compare，不修改工作区，可从任意 cwd 工作
- `services/auth/internal/contract/authv1/freshness_test.go`：轻量级 Go test，逐一验证两个 `.gen.go` 文件包含 oapi-codegen 源头标记和 DO NOT EDIT 标记，不运行生成器，快速且离线
- 完整新鲜度检查（重生成 + 字节对比）由 `go/check-generated.sh` 及 CI `go-auth` job 早期步骤执行，不依赖普通 `go test`

## 开发与验证

```bash
pnpm --filter @tokenmp/contracts lint
pnpm --filter @tokenmp/contracts typecheck
pnpm --filter @tokenmp/contracts test
pnpm --filter @tokenmp/contracts build

# Go 代码生成
pnpm --filter @tokenmp/contracts generate:auth:go

# 生成物新鲜度检查
pnpm --filter @tokenmp/contracts check:generated
```

- 最小验证：`lint`（YAML 基本结构 + 禁止内部术语）、`typecheck`（跨文件 operationId 唯一性 + $ref 解析）、`test`（Node test runner 契约测试）、`build`（复制到 dist）。
- 契约测试：`tests/openapi-auth-v1.test.mjs`。
- 生成测试：`services/auth/internal/contract/authv1/freshness_test.go`。
- 集成测试：首次消费者接入时补充端到端验证。

## 模块边界

- 允许访问：自身 OpenAPI YAML、Node.js 内建构建脚本、Go 生成配置与脚本。
- 禁止访问：任何服务的私有源码、数据库模型、migration 或运行时配置。
- 数据所有权：无。
- 配置和环境变量：无。
- Docker 镜像/部署单元：无。
- 健康检查：不适用。

## DO NOT

- **DO NOT** 在契约中暴露服务私有实现细节（SQL、hash 算法、数据库列名、事务语义、ORM 模型、migration 结构等）——消费者不得需要理解 Auth 内部实现才能使用 API。
- **DO NOT** 在此 package 中引入运行时第三方依赖——验证脚本使用 `yaml` npm 包（devDependency）真正解析 YAML，运行时零依赖。
- **DO NOT** 在此 package 中生成客户端代码——契约是唯一事实来源，代码生成由消费者自行决定。
- **DO NOT** 让 contracts 依赖任何 service 或 app——依赖方向是 service → contracts，不是反向。
- **DO NOT** 在契约中描述速率限制为已实现——速率限制是部署阻塞项，尚未实施。
- **DO NOT** 保留服务源码中的第二份 OpenAPI 副本——`packages/contracts/openapi/auth/v1.yaml` 是唯一权威副本。
- **DO NOT** 手动编辑 `services/auth/internal/contract/authv1/{models,server}.gen.go`——通过 `generate.sh` 重生成。
- **DO NOT** 将 oapi-codegen CLI 作为 Go module 依赖——使用 `go run @version` 避免污染依赖图。

## 已知陷阱与历史教训

### 契约泄露内部实现术语

- 症状：消费者需要理解 Argon2id、bcrypt、GORM、SHA-256、BYTEA、revoke_reason 等内部术语才能解读 API 行为。
- 根因：原始 OpenAPI 文档由实现者编写，自然包含实现细节。
- DO：契约只描述可观察的 HTTP 行为、安全语义、幂等性和错误契约；内部实现术语由 `scripts/contract-helpers.mjs` 的 `forbiddenTerms` 列表自动检测。
- DO NOT：在消费者契约中提及 hash 算法、数据库列名、事务策略或 ORM 概念。
- 验证：`pnpm --filter @tokenmp/contracts lint` 和 `test` 自动检测禁止术语。
- 适用范围：所有 `packages/contracts/openapi/` 下的契约。

### 生成物过期

- 症状：任一 `.gen.go` 文件与当前 OpenAPI 契约不一致，导致编译错误或运行时行为偏离契约。
- 根因：修改了 OpenAPI 契约但忘记重生成 Go 代码。
- DO：每次修改契约后运行 `generate:auth:go`；CI 早期执行 `check:generated`；Go test `freshness_test.go` 也在本地检测。
- DO NOT：手动编辑 `.gen.go` 文件；跳过新鲜度检查。
- 验证：`check-generated.sh` + `freshness_test.go` + CI step。
- 适用范围：所有使用 oapi-codegen 生成的服务。

## 文档维护触发器

出现以下变化时同步更新本文件：

- 新增、移除或修改 OpenAPI 契约文件。
- 新增或删除直接消费者。
- 禁止术语列表变化。
- 验证脚本或测试命令变化。
- oapi-codegen 版本变化。
- 生成配置或脚本变化。
