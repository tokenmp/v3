# @tokenmp/ui-tokens

TokenMP 的框架无关 Design Token CSS package。视觉值以旧版 TokenMP 已落地 Token 为兼容基线，v3 在其上增加分层命名、公开 package 边界和契约验证。视觉方向保持 **Industrial / Utilitarian**：中性表面、高辨识状态、克制层级和适合数据密集界面的排版。

本阶段只定义 Token，不包含应用、React 组件、字体文件或主题切换脚本。Web 与 Admin 是已确认的未来消费者；尚未进行应用构建和浏览器视觉回归验证。

## 公开入口

| 入口 | 内容 | 稳定性 |
|---|---|---|
| `@tokenmp/ui-tokens` | Reference、Semantic、Light/Dark 和布局 Token | experimental |
| `@tokenmp/ui-tokens/tailwind` | Tailwind CSS v4 `@theme inline` 映射 | experimental |
| `@tokenmp/ui-tokens/shadcn` | shadcn/ui 常用变量兼容别名 | experimental |

未来 Tailwind v4 应用的导入顺序：

```css
@import "tailwindcss";
@import "@tokenmp/ui-tokens";
@import "@tokenmp/ui-tokens/tailwind";
@import "@tokenmp/ui-tokens/shadcn";
```

Tailwind、shadcn 和字体资产由消费应用安装或加载。本 package 不替代这些依赖；integration 文件只把它们映射到 TokenMP 的 `--tmp-*` 语义契约。

## 主题

- 未设置 `data-theme`：浅色为默认，并通过 `prefers-color-scheme: dark` 跟随系统。
- `data-theme="light"`：显式浅色。
- `data-theme="dark"`：显式深色。
- `.dark`：兼容 Tailwind/shadcn 常见类名方式。

应用只能切换主题选择器，不应覆盖 Reference Token。状态不能只依靠颜色传达，仍须使用文字或图标。

## 命名和消费规则

- `--tmp-ref-*`：原始设计尺度，不供业务页面直接使用。
- `--tmp-color-*`、`--tmp-font-*` 等：语义 Token，供组件和应用使用。
- integrations 只能 alias 语义 Token，不能定义新的颜色、间距或圆角值。
- 禁止深层导入 `src/*`；只使用 package exports。

字体 Token 延续旧版 stack：`PingFang SC`、Apple/Segoe UI/Roboto 回退，以及 `ui-monospace`/`SFMono-Regular` 等宽回退。本 package 不分发字体；消费应用引入额外 Web Font 时必须作为独立视觉决策评审。

## 开发与验证

```bash
pnpm --filter @tokenmp/ui-tokens lint
pnpm --filter @tokenmp/ui-tokens typecheck
pnpm --filter @tokenmp/ui-tokens test
pnpm --filter @tokenmp/ui-tokens build
```

`test` 依赖 `build` 生成 `dist/`。契约测试检查主题集合、引用完整性、integration 边界以及公开导出。首次应用接入时仍需补充 Tailwind 实际编译、WCAG 对比度和浏览器视觉回归验证。
