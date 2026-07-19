# UI Design Tokens

> 作用域：`packages/ui-tokens/`。继承仓库根目录与 `packages/AGENTS.md`。

## 模块职责

- 负责：TokenMP Reference/Semantic Design Tokens、Light/Dark 主题，以及 Tailwind v4、shadcn CSS 集成映射。
- 不负责：应用、React 组件、字体文件、主题状态管理、业务状态判断或运行时框架安装。
- 所有者：TokenMP 前端基础设施。

## 必读文档

- 模块说明：`README.md`
- UI 规范：`../../docs/ui/design-system.md`
- 架构决策：`../../docs/adr/0002-ui-design-tokens.md`

## 对外能力与返回契约

| 能力/导出 | 输入与前置条件 | 返回/副作用 | 稳定性 | 契约来源 |
|---|---|---|---|---|
| `@tokenmp/ui-tokens` | 浏览器 CSS；按 README 选择主题 | 注册 `--tmp-*` Token；不执行脚本 | experimental | `src/index.css`、契约测试 |
| `@tokenmp/ui-tokens/tailwind` | Tailwind CSS v4 编译环境，且已导入核心入口 | 注册 `@theme inline` 映射 | experimental | `src/integrations/tailwind.css` |
| `@tokenmp/ui-tokens/shadcn` | 已导入核心入口 | 注册 shadcn 无前缀变量别名 | experimental | `src/integrations/shadcn.css` |

## 依赖关系与消费者

| 方向 | 模块/资源 | 使用功能 | 依赖方式 | 契约/入口 | 变更后验证 |
|---|---|---|---|---|---|
| 依赖 | Node.js | 构建和契约验证 | 开发工具 | `scripts/*.mjs` | package 全部检查 |
| 被依赖 | Web、Admin（未来） | 共享视觉与主题 | package CSS import | 三个公开 exports | 首次接入时构建与视觉验证 |

当前没有已实施的直接消费者，不得把未来消费者描述为已集成。

## 开发与验证

```bash
pnpm --filter @tokenmp/ui-tokens lint
pnpm --filter @tokenmp/ui-tokens typecheck
pnpm --filter @tokenmp/ui-tokens test
pnpm --filter @tokenmp/ui-tokens build
```

- 最小验证：`lint`、`typecheck`、`test`、`build`。
- 契约测试：`tests/token-contract.test.mjs`。
- 集成测试：首次 app 接入时补充 Tailwind 编译和浏览器测试。

## 模块边界

- 允许访问：自身 CSS、Node.js 内建构建脚本和正式 UI 文档。
- 禁止访问：未来 app 私有源码、后端业务代码和运行时主题状态。
- 数据所有权：无。
- 配置和环境变量：无。
- Docker 镜像/部署单元：无。
- 健康检查：不适用。

## DO NOT

- **DO NOT** 在 integration 中写原始设计值——它们只能映射核心 `--tmp-*` Token，否则主题会漂移。
- **DO NOT** 让业务页面直接消费 `--tmp-ref-*`——应使用稳定的 Semantic Token。
- **DO NOT** 深层导入 `src/*`——仅使用 package exports，避免绕过公开契约。
- **DO NOT** 把 Tailwind、shadcn 或字体包设为本 package 的运行时能力——它们由未来消费应用提供。

## 已知陷阱与历史教训

暂无已确认的实施陷阱。首次应用集成后，将可复用结论记录在本节。

## 文档维护触发器

公开入口、Token 名称、主题集合、integration 映射、未来消费者状态或验证命令变化时，同步更新本文件、README、UI 规范、ADR 和上下游索引。
