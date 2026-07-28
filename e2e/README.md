# TokenMP v3 E2E 测试

本目录包含 TokenMP v3 前端应用的端到端测试，使用 Playwright 测试框架。

## 目录结构

```
e2e/
├── tests/
│   ├── admin/           # Admin 后台测试
│   │   ├── users.spec.ts
│   │   ├── plans-providers.spec.ts
│   │   ├── announcements-changelogs.spec.ts
│   │   └── settings-notifications.spec.ts
│   └── panel/           # 用户面板测试
│       └── panel.spec.ts
├── utils/
│   └── test-utils.ts    # 测试工具函数
├── playwright.config.ts # Playwright 配置
└── package.json         # 依赖配置
```

## 安装

1. 安装依赖：

```bash
cd e2e
pnpm install
```

2. 安装浏览器：

```bash
pnpm install:browsers
```

## 运行测试

### 运行所有测试

```bash
pnpm test
```

### 运行特定测试文件

```bash
pnpm test tests/admin/users.spec.ts
```

### 运行带 UI 的测试

```bash
pnpm test:ui
```

### 调试测试

```bash
pnpm test:debug
```

### 生成测试代码

```bash
pnpm test:codegen
```

### 查看测试报告

```bash
pnpm test:report
```

## 测试场景

### Admin 后台测试

#### 1. 用户管理 (`users.spec.ts`)

- ✅ 页面加载正确
- ✅ 搜索功能
- ✅ 状态筛选
- ✅ 角色筛选
- ✅ 分页功能
- ✅ 禁用/启用用户
- ✅ 设置/取消管理员
- ✅ 分配套餐
- ✅ 撤销套餐
- ✅ 用户详情页
- ✅ 响应式设计
- ✅ 空数据状态
- ✅ 错误处理

#### 2. 套餐和 Provider 管理 (`plans-providers.spec.ts`)

**套餐管理：**
- ✅ 页面加载正确
- ✅ 新建套餐
- ✅ 编辑套餐
- ✅ 删除套餐
- ✅ 表单验证
- ✅ 套餐类型选择
- ✅ 状态显示
- ✅ 价格和额度显示

**Provider 管理：**
- ✅ 页面加载正确
- ✅ 新建 Provider
- ✅ 编辑 Provider
- ✅ 删除 Provider
- ✅ 编译并发布
- ✅ 表单验证
- ✅ SDK 类型选择
- ✅ Endpoint 管理
- ✅ 搜索和筛选

#### 3. 公告和版本日志管理 (`announcements-changelogs.spec.ts`)

**公告管理：**
- ✅ 页面加载正确
- ✅ 新建公告
- ✅ 编辑公告
- ✅ 删除公告
- ✅ 立即发布功能
- ✅ 公告级别显示
- ✅ 表单验证

**版本日志管理：**
- ✅ 页面加载正确
- ✅ 新建版本日志
- ✅ 编辑版本日志
- ✅ 删除版本日志
- ✅ Markdown 预览
- ✅ 立即发布功能
- ✅ 表单验证

#### 4. 系统设置和通知管理 (`settings-notifications.spec.ts`)

**系统设置：**
- ✅ 页面加载正确
- ✅ 平台信息显示
- ✅ 认证配置显示
- ✅ 服务状态显示
- ✅ 服务状态颜色
- ✅ 服务状态自动刷新
- ✅ 功能开关显示
- ✅ 功能开关交互

**通知管理：**
- ✅ 页面加载正确
- ✅ 发送通知
- ✅ 发送给指定用户
- ✅ 删除通知
- ✅ 表单验证
- ✅ 通知类型选择
- ✅ Action 配置
- ✅ 已读状态显示

### Panel 用户面板测试

#### 1. 用户概览 (`panel.spec.ts`)

- ✅ 页面加载正确
- ✅ 账户信息显示
- ✅ 配额信息显示
- ✅ 状态信息显示
- ✅ 最近请求表格
- ✅ 空数据状态
- ✅ 响应式设计

#### 2. API Key 管理

- ✅ 页面加载正确
- ✅ 创建 API Key
- ✅ 复制 API Key
- ✅ 轮换 API Key
- ✅ 撤销 API Key
- ✅ 密钥显示安全
- ✅ 表单验证

#### 3. 请求日志

- ✅ 页面加载正确
- ✅ 搜索功能
- ✅ 状态筛选
- ✅ 分页功能
- ✅ 表格数据格式
- ✅ 空数据状态

## 测试配置

### 环境变量

- `BASE_URL`: 测试目标 URL（默认：`http://localhost:3100`）
- `CI`: CI 环境标识

### 浏览器配置

测试支持以下浏览器：
- Chromium
- Firefox
- WebKit
- Mobile Chrome
- Mobile Safari

### 超时设置

- 操作超时：10 秒
- 导航超时：30 秒
- 测试超时：60 秒

## 最佳实践

1. **测试隔离**：每个测试用例都应该独立运行，不依赖其他测试的状态
2. **等待策略**：使用适当的等待策略，避免硬编码延时
3. **错误处理**：测试应该验证错误场景和边界情况
4. **响应式测试**：测试应该覆盖桌面和移动视图
5. **可访问性**：测试应该验证基本的可访问性要求

## 故障排除

### 测试失败

1. 检查开发服务器是否运行：`pnpm dev`
2. 检查浏览器是否安装：`pnpm install:browsers`
3. 检查网络连接和 API 服务状态

### 性能问题

1. 减少并行测试数量
2. 使用 headless 模式
3. 优化等待策略

### 调试技巧

1. 使用 `--debug` 模式查看浏览器操作
2. 使用 `--ui` 模式交互式调试
3. 查看测试报告中的截图和视频

## 持续集成

测试已配置为在 CI 环境中运行，包括：

- GitHub Actions
- GitLab CI
- 其他 CI 平台

CI 配置示例：

```yaml
# .github/workflows/e2e.yml
name: E2E Tests
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-node@v3
        with:
          node-version: 18
      - run: cd e2e && npm install
      - run: cd e2e && npx playwright install --with-deps
      - run: cd e2e && npm test
```

## 贡献指南

1. 添加新测试时，请遵循现有的测试结构
2. 为每个功能添加正向和反向测试用例
3. 使用有意义的测试描述
4. 保持测试代码的可读性和可维护性
5. 定期更新测试以匹配 UI 变化
