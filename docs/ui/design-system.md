# TokenMP UI Design System

- 文档类型：长期 UI 规范
- 状态：accepted
- 适用范围：TokenMP Web 界面；首期只落地 Design Tokens
- 事实来源：`packages/ui-tokens/src/` 及其契约测试

## 1. 目标与边界

TokenMP 面向普通用户和运营人员，界面需要同时支持可信的产品表达和高密度技术数据操作。v3 以旧版 TokenMP 已落地 Token 数值和 shadcn/Tailwind 语义为视觉兼容基线，同时采用 **Industrial / Utilitarian** 方向：中性表面、明确边界、克制阴影、高辨识状态，以及偏左对齐的工具型信息层级。

本规范已落地 Design Token package，但尚未创建 Web、Admin 或共享 React 组件。Web 与 Admin 是已确认的未来消费者，不得描述为已经接入。首个应用接入时必须补充真实编译、浏览器、响应式和视觉回归验证。

## 2. 设计原则

1. **语义优先**：组件使用 `surface`、`text-primary`、`status-danger` 等语义，不直接绑定色阶。
2. **密度可控**：用户端可适当宽松，Admin 可采用紧凑布局，但共享颜色、排版、焦点和状态契约。
3. **边框优于装饰**：普通层级主要通过表面、边框和间距表达；阴影保留给 raised、overlay 和 modal。
4. **状态不只靠颜色**：成功、警告、错误和信息必须同时提供文字或专业图标。
5. **框架解耦**：核心 Token 是普通 CSS；Tailwind 和 shadcn 通过公开 integration 映射。
6. **可访问性默认开启**：主题、焦点、动效和点击区域从基础规范开始考虑，不在页面完成后补救。

## 3. Token 模型

### 3.1 Reference Tokens

`--tmp-ref-*` 表达原始色阶、间距、字号、圆角、阴影、动效和层级。它们是语义 Token 的原料，不是业务页面的公共语言。

示例：

```css
--tmp-ref-color-neutral-950
--tmp-ref-space-4
--tmp-ref-font-size-sm
--tmp-ref-duration-fast
```

### 3.2 Semantic Tokens

应用与组件应优先消费 `--tmp-color-*`、`--tmp-font-*`、`--tmp-radius-*` 等语义 Token：

```css
--tmp-color-bg-canvas
--tmp-color-text-primary
--tmp-color-border-default
--tmp-color-action-primary
--tmp-color-status-success-bg
```

业务状态需要先映射为视觉语义。例如 `expired` 可按产品语义映射为 warning 或 danger，但不得新增 `--tmp-color-plan-expired` 一类业务专属全局 Token。

### 3.3 Integration Tokens

- `@tokenmp/ui-tokens/tailwind`：映射到 Tailwind CSS v4 `@theme inline`。
- `@tokenmp/ui-tokens/shadcn`：映射到 shadcn/ui 常用无前缀变量。

Integration 只允许 alias 核心语义 Token，禁止拥有 OKLCH、Hex、间距或圆角原始值。它们是预定义且已测试的公开契约，但尚未经真实 app 编译验证。

## 4. 命名规范

统一使用 `--tmp-` 前缀：

```text
--tmp-ref-<category>-<scale>
--tmp-color-<role>-<variant>
--tmp-text-<role>-<property>
--tmp-control-<property>-<size>
--tmp-layer-<role>
```

要求：

- 名称表达用途，不表达具体值，如使用 `text-secondary` 而不是 `gray-600`。
- Light/Dark 必须暴露相同的 `--tmp-color-*` 集合。
- 删除或重命名公开语义 Token 属于破坏性变更，必须验证全部消费者。
- 消费者只通过 package exports 导入，不深层引用 `src/*`。

## 5. 色彩与主题

颜色使用 OKLCH，并原样继承旧版无彩中性灰及蓝、绿、红、琥珀核心色阶；蓝色用于信息，绿色用于成功，琥珀用于警告，红色用于危险。焦点环继续使用中性灰基线。数据可视化扩展色与状态色分离，避免图表颜色隐含业务结果。

主题优先级：

1. `data-theme="light"` 强制浅色。
2. `data-theme="dark"` 强制深色。
3. `.dark` 兼容 Tailwind/shadcn 生态。
4. 未显式选择时，通过 `prefers-color-scheme` 跟随系统。

应用不得修改 Reference Token 来换主题，只切换主题选择器。首次应用接入必须用自动化工具验证至少以下组合达到 WCAG 2.2 AA：

- primary text / canvas
- secondary text / canvas 与 surface
- action foreground / action background
- 各状态 text / status background
- focus ring / 相邻背景

当前数值经过视觉方向设计和结构审查，**尚未完成浏览器计算色值后的正式对比度认证**。

## 6. 排版

- 中文正文优先：`PingFang SC`
- 通用回退：Apple system font、`Segoe UI`、`Roboto`、generic sans-serif
- 模型 ID、请求 ID、代码和等宽数据：`ui-monospace`、`SFMono-Regular`、generic monospace

字体 stack 延续旧版视觉基线，Token package 不分发字体。引入 Noto、IBM Plex 等额外 Web Font 属于独立视觉和性能决策，需要另行评审字体许可、自托管或 CDN、CSP、预加载及回退策略。不要在业务页面随意使用 `text-[10px]` 等值；优先采用 caption、body、heading 语义层级。数据列应根据需要使用等宽字体与 tabular numerals。

## 7. 间距、形状与布局

Reference 间距采用有限尺度。组件内部、表单字段和页面区块应从该尺度选择，不引入只出现一次的 Magic Number。

稳定布局语义包括：

- 三档控件高度：sm、md、lg
- 响应式 page gutter
- page content max width
- sidebar 展开与收起宽度
- sm/md/lg/xl/full 圆角

未来桌面应用推荐 12 列结构和左对齐内容，不强制所有页面使用居中 Card。Admin 数据表在移动端如何转换为列表或 Sheet，应由组件和应用规范在实现阶段确定。

## 8. 阴影、动效与层级

阴影仅分 raised、overlay、modal。普通 Surface 默认依赖背景与边框，不使用大面积装饰性阴影。

动效使用 fast、normal、slow 与 enter/exit/standard easing。`prefers-reduced-motion: reduce` 下，语义 duration 自动变为零；消费者不能使用动画表达唯一信息。

层级顺序固定为：

```text
base < sticky < dropdown < popover < drawer < modal < toast
```

消费者不得把所有浮层统一写成 `z-50`。

## 9. 交互与无障碍基线

- 所有键盘交互元素必须有可见焦点样式。
- 移动端主要点击目标建议至少 `44 × 44 CSS px`。
- Disabled、loading、invalid、selected 不得只用透明度或颜色表达。
- 表单错误应提供 `aria-invalid`、关联说明和合理焦点管理。
- Dialog、Sheet、Menu 必须支持键盘导航、Esc 行为和焦点恢复。
- 图表必须提供文本值、表格或可访问 Tooltip，不只依赖颜色。
- 图标统一使用专业图标库；不使用 Emoji 作为功能图标。

## 10. 使用约束

允许：

```css
color: var(--tmp-color-text-primary);
background: var(--tmp-color-status-warning-bg);
```

禁止业务组件直接使用 Reference Token：

```css
color: var(--tmp-ref-color-amber-700);
```

禁止 Integration 定义新的设计值，禁止应用重建另一套同名语义变量，也禁止在 Tailwind class 中长期散落状态色组合和任意值。

## 11. 变更与验证

修改 Token 时必须：

1. 更新核心 CSS 和必要的 integration 映射。
2. 保持 Light/Dark/System Dark 语义集合一致。
3. 运行 package lint、typecheck、test、build。
4. 公开名称或值语义变化时更新 README、本规范、ADR 或模块契约。
5. 存在应用消费者后，验证所有直接消费者的构建、主题、视觉回归和对比度。

正式命令见 `packages/ui-tokens/README.md`。代码、测试与本文冲突时，以可验证源码为事实并同步修正文档。
