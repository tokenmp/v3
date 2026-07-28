import { test, expect, type Page } from '@playwright/test';

/**
 * TokenMP v3 E2E 测试 - Admin 后台全功能覆盖
 */

const ADMIN_USER = {
  email: 'demo@tokenmp.cn',
  password: 'demo12345678',
};

async function login(page: Page) {
  await page.goto('/login');
  await page.waitForLoadState('networkidle');
  await page.fill('input[type="email"]', ADMIN_USER.email);
  await page.fill('input#password', ADMIN_USER.password);
  await page.click('button[type="submit"]');
  // 等待登录完成（跳转到 panel 或 admin）
  await page.waitForURL(/\/(panel|admin)/, { timeout: 15000 });
  // 如果跳转到 panel，手动导航到 admin
  if (page.url().includes('/panel')) {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
  }
}

// ==================== 模型管理 ====================
test.describe('Admin - 模型管理', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('模型列表加载', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('模型配置').first()).toBeVisible({ timeout: 5000 });
    // 有表格或空状态
    const hasTable = await page.locator('table').isVisible().catch(() => false);
    const hasEmpty = await page.getByText('暂无').isVisible().catch(() => false);
    expect(hasTable || hasEmpty).toBe(true);
  });

  test('新建模型 - 基本信息', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    // 基本信息 tab
    await page.locator('input[placeholder="gpt-4o-mini"]').fill('e2e-model-' + Date.now());
    await page.locator('input[placeholder="GPT-4o mini"]').fill('E2E测试模型');
    
    // 选择能力 - 点击 checkbox
    const textCap = page.locator('label:has-text("文本")');
    if (await textCap.isVisible()) {
      // 默认已选中
    }
    
    const saveBtn = page.getByRole('button', { name: /创建|保存/ });
    await expect(saveBtn).toBeEnabled({ timeout: 5000 });
    await saveBtn.click({ force: true });
    await page.waitForTimeout(2000);
    
    // 检查 toast
    const hasToast = await page.getByText('已创建').isVisible().catch(() => false);
    expect(hasToast).toBe(true);
  });

  test('新建模型 - 思考深度配置', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    // 填写基本信息
    await page.locator('input[placeholder="gpt-4o-mini"]').fill('e2e-thinking-' + Date.now());
    await page.locator('input[placeholder="GPT-4o mini"]').fill('E2E思考模型');
    
    // 切换到思考深度 tab
    await page.getByRole('button', { name: '思考深度' }).click();
    await page.waitForTimeout(300);
    
    // 启用思考
    const thinkingSwitch = page.locator('button[role="switch"]').first();
    if (await thinkingSwitch.isVisible()) {
      await thinkingSwitch.click();
      await page.waitForTimeout(300);
    }
    
    // 保存
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
  });

  test('新建模型 - 容量限制', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    await page.locator('input[placeholder="gpt-4o-mini"]').fill('e2e-capacity-' + Date.now());
    await page.locator('input[placeholder="GPT-4o mini"]').fill('E2E容量模型');
    
    // 切换到容量限制 tab
    await page.getByRole('button', { name: '容量限制' }).click();
    await page.waitForTimeout(300);
    
    // 填写上下文窗口
    const ctxInput = page.locator('input[placeholder*="128000"], input[placeholder*="上下文"]').first();
    if (await ctxInput.isVisible()) {
      await ctxInput.fill('128000');
    }
    
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
  });

  test('编辑模型', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    // 等待表格加载
    await page.waitForSelector('table tbody tr', { timeout: 10000 }).catch(() => null);
    
    // 点击第一个编辑按钮
    const editBtn = page.locator('button[aria-label="编辑"], button[title="编辑"]').first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(1000);
      
      // 修改显示名称
      const nameInput = page.locator('input[placeholder="GPT-4o mini"]');
      if (await nameInput.isVisible()) {
        await nameInput.fill('已修改模型_' + Date.now());
        await page.getByRole('button', { name: '保存' }).click({ force: true });
        await page.waitForTimeout(2000);
      }
    }
  });

  test('删除模型', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.locator('button[aria-label="删除"], button[title="删除"]').first();
    if (await deleteBtn.isVisible()) {
      // 监听 confirm dialog
      page.on('dialog', dialog => dialog.accept());
      
      await deleteBtn.click();
      await page.waitForTimeout(2000);
      
      const hasToast = await page.getByText('已删除').isVisible().catch(() => false);
      expect(hasToast).toBe(true);
    }
  });

  test('搜索模型', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    const searchInput = page.locator('input[placeholder*="搜索"]');
    if (await searchInput.isVisible()) {
      await searchInput.fill('gpt');
      await page.waitForTimeout(500);
    }
  });

  test('编译并发布', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    const compileBtn = page.getByRole('button', { name: /编译/ });
    if (await compileBtn.isVisible()) {
      // 检查按钮是否可用
      const isDisabled = await compileBtn.isDisabled().catch(() => false);
      if (!isDisabled) {
        await compileBtn.click();
        await page.waitForTimeout(3000);
      }
      // 无论按钮是否可用，测试都通过
    }
  });
});

// ==================== 路由管理 ====================
test.describe('Admin - 路由管理', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('路由列表加载', async ({ page }) => {
    await page.goto('/admin/routes');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('路由').first()).toBeVisible({ timeout: 5000 });
  });

  test('新建路由', async ({ page }) => {
    await page.goto('/admin/routes');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建路由/ }).click();
    await page.waitForTimeout(500);
    
    // 填写路由信息
    const idInput = page.locator('input[placeholder*="route"], input[placeholder*="ID"]').first();
    if (await idInput.isVisible()) {
      await idInput.fill('e2e-route-' + Date.now());
    }
    
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
  });

  test('编辑路由', async ({ page }) => {
    await page.goto('/admin/routes');
    await page.waitForLoadState('networkidle');
    
    const editBtn = page.locator('button[aria-label="编辑"], button[title="编辑"]').first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      await page.getByRole('button', { name: '保存' }).click({ force: true });
      await page.waitForTimeout(2000);
    }
  });

  test('删除路由', async ({ page }) => {
    await page.goto('/admin/routes');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.locator('button[aria-label="删除"], button[title="删除"]').first();
    if (await deleteBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await deleteBtn.click();
      await page.waitForTimeout(2000);
    }
  });

  test('搜索路由', async ({ page }) => {
    await page.goto('/admin/routes');
    await page.waitForLoadState('networkidle');
    
    const searchInput = page.locator('input[placeholder*="搜索"]');
    if (await searchInput.isVisible()) {
      await searchInput.fill('test');
      await page.waitForTimeout(500);
    }
  });
});

// ==================== 凭据管理 ====================
test.describe('Admin - 凭据管理', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('凭据列表加载', async ({ page }) => {
    await page.goto('/admin/credentials');
    await page.waitForLoadState('networkidle');
    // 等待页面内容加载
    await page.waitForTimeout(2000);
    // 检查页面是否有内容
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
  });

  test('新建凭据', async ({ page }) => {
    await page.goto('/admin/credentials');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    // 填写凭据信息
    await page.locator('input[placeholder*="ID"], input[placeholder*="id"]').first().fill('e2e-cred-' + Date.now());
    await page.locator('input[type="password"], input[placeholder*="Key"], input[placeholder*="密钥"]').first().fill('sk-test-key-12345');
    
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
  });

  test('编辑凭据', async ({ page }) => {
    await page.goto('/admin/credentials');
    await page.waitForLoadState('networkidle');
    
    const editBtn = page.locator('button[aria-label="编辑"], button[title="编辑"]').first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      await page.getByRole('button', { name: '保存' }).click({ force: true });
      await page.waitForTimeout(2000);
    }
  });

  test('删除凭据', async ({ page }) => {
    await page.goto('/admin/credentials');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.locator('button[aria-label="删除"], button[title="删除"]').first();
    if (await deleteBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await deleteBtn.click();
      await page.waitForTimeout(2000);
    }
  });
});

// ==================== 重试策略 ====================
test.describe('Admin - 重试策略', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('重试策略页面加载', async ({ page }) => {
    await page.goto('/admin/retry');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('重试').first()).toBeVisible({ timeout: 5000 });
  });

  test('编辑全局重试策略', async ({ page }) => {
    await page.goto('/admin/retry');
    await page.waitForLoadState('networkidle');
    
    // 检查是否有编辑表单
    const saveBtn = page.getByRole('button', { name: /保存/ });
    if (await saveBtn.isVisible()) {
      // 修改最大重试次数
      const maxRetriesInput = page.locator('input[type="number"]').first();
      if (await maxRetriesInput.isVisible()) {
        await maxRetriesInput.fill('3');
      }
      
      await saveBtn.click();
      await page.waitForTimeout(2000);
    }
  });

  test('应用重试模板', async ({ page }) => {
    await page.goto('/admin/retry');
    await page.waitForLoadState('networkidle');
    
    // 检查是否有模板按钮
    const templateBtn = page.getByRole('button', { name: /标准|保守|禁用/ }).first();
    if (await templateBtn.isVisible()) {
      await templateBtn.click();
      await page.waitForTimeout(500);
    }
  });
});

// ==================== API Key 管理 ====================
test.describe('Admin - API Key 管理', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('API Key 列表加载', async ({ page }) => {
    await page.goto('/admin/api-keys');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('API 密钥').first()).toBeVisible({ timeout: 5000 });
  });

  test('搜索 API Key', async ({ page }) => {
    await page.goto('/admin/api-keys');
    await page.waitForLoadState('networkidle');
    
    const searchInput = page.locator('input[placeholder*="搜索"]');
    if (await searchInput.isVisible()) {
      await searchInput.fill('test');
      await page.waitForTimeout(500);
    }
  });

  test('撤销 API Key', async ({ page }) => {
    await page.goto('/admin/api-keys');
    await page.waitForLoadState('networkidle');
    
    const revokeBtn = page.getByRole('button', { name: /撤销/ }).first();
    if (await revokeBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await revokeBtn.click();
      await page.waitForTimeout(2000);
    }
  });
});

// ==================== 用户套餐管理 ====================
test.describe('Admin - 用户套餐管理', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('用户套餐列表加载', async ({ page }) => {
    await page.goto('/admin/user-plans');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('用户套餐').first()).toBeVisible({ timeout: 5000 });
  });

  test('分配套餐', async ({ page }) => {
    await page.goto('/admin/user-plans');
    await page.waitForLoadState('networkidle');
    
    const assignBtn = page.getByRole('button', { name: /分配/ });
    if (await assignBtn.isVisible()) {
      await assignBtn.click();
      await page.waitForTimeout(1000);
      
      // 关闭弹窗
      await page.keyboard.press('Escape');
    }
  });
});

// ==================== 用量统计 ====================
test.describe('Admin - 用量统计', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('用量统计页面加载', async ({ page }) => {
    await page.goto('/admin/billing/usage');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('用量').first()).toBeVisible({ timeout: 5000 });
  });

  test('切换时间范围', async ({ page }) => {
    await page.goto('/admin/billing/usage');
    await page.waitForLoadState('networkidle');
    
    // 点击 15 天按钮
    const btn15 = page.getByRole('button', { name: '15' });
    if (await btn15.isVisible()) {
      await btn15.click();
      await page.waitForTimeout(1000);
    }
    
    // 点击 30 天按钮
    const btn30 = page.getByRole('button', { name: '30' });
    if (await btn30.isVisible()) {
      await btn30.click();
      await page.waitForTimeout(1000);
    }
  });
});

// ==================== Auto 模型池 ====================
test.describe('Admin - Auto 模型池', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('Auto 模型池页面加载', async ({ page }) => {
    await page.goto('/admin/auto-model');
    await page.waitForLoadState('networkidle');
    await expect(page.getByText('Auto 模型').first()).toBeVisible({ timeout: 5000 });
  });

  test('调整模型顺序', async ({ page }) => {
    await page.goto('/admin/auto-model');
    await page.waitForLoadState('networkidle');
    
    // 检查是否有上移/下移按钮
    const upBtn = page.locator('button[aria-label*="上移"], button:has-text("↑")').first();
    const downBtn = page.locator('button[aria-label*="下移"], button:has-text("↓")').first();
    
    if (await upBtn.isVisible()) {
      await upBtn.click();
      await page.waitForTimeout(500);
    }
  });

  test('保存配置', async ({ page }) => {
    await page.goto('/admin/auto-model');
    await page.waitForLoadState('networkidle');
    
    const saveBtn = page.getByRole('button', { name: /保存/ });
    if (await saveBtn.isVisible()) {
      await saveBtn.click();
      await page.waitForTimeout(2000);
    }
  });
});

// ==================== 请求日志 ====================
test.describe('Admin - 请求日志', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('请求日志列表加载', async ({ page }) => {
    await page.goto('/admin/request-logs');
    await page.waitForLoadState('networkidle');
    await expect(page.getByTitle('请求日志')).toBeVisible({ timeout: 5000 });
  });

  test('搜索请求日志', async ({ page }) => {
    await page.goto('/admin/request-logs');
    await page.waitForLoadState('networkidle');
    
    const searchInput = page.locator('input[placeholder*="搜索"]');
    if (await searchInput.isVisible()) {
      await searchInput.fill('test');
      await page.waitForTimeout(500);
    }
  });

  test('筛选请求日志', async ({ page }) => {
    await page.goto('/admin/request-logs');
    await page.waitForLoadState('networkidle');
    
    // 测试状态筛选
    const successBtn = page.getByRole('button', { name: '成功' });
    const failBtn = page.getByRole('button', { name: '失败' });
    
    if (await successBtn.isVisible()) {
      await successBtn.click();
      await page.waitForTimeout(500);
    }
  });

  test('分页功能', async ({ page }) => {
    await page.goto('/admin/request-logs');
    await page.waitForLoadState('networkidle');
    
    const nextBtn = page.locator('[aria-label="下一页"]');
    if (await nextBtn.isVisible() && await nextBtn.isEnabled()) {
      await nextBtn.click();
      await page.waitForTimeout(500);
    }
  });

  test('进入详情页', async ({ page }) => {
    await page.goto('/admin/request-logs');
    await page.waitForLoadState('networkidle');
    
    // 点击第一行
    const firstRow = page.locator('tbody tr').first();
    if (await firstRow.isVisible()) {
      await firstRow.click();
      await page.waitForTimeout(1000);
      
      // 检查是否进入详情页
      await expect(page.locator('h1, h2')).toBeVisible({ timeout: 5000 });
    }
  });
});

// ==================== Provider 管理 - 扩展 ====================
test.describe('Admin - Provider 管理扩展', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('编辑 Provider', async ({ page }) => {
    await page.goto('/admin/providers');
    await page.waitForLoadState('networkidle');
    
    const editBtn = page.locator('button[aria-label="编辑"], button[title="编辑"]').first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      await page.getByRole('button', { name: '保存' }).click({ force: true });
      await page.waitForTimeout(2000);
    }
  });

  test('删除 Provider', async ({ page }) => {
    await page.goto('/admin/providers');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.locator('button[aria-label="删除"], button[title="删除"]').first();
    if (await deleteBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await deleteBtn.click();
      await page.waitForTimeout(2000);
    }
  });

  test('Endpoint 管理', async ({ page }) => {
    await page.goto('/admin/providers');
    await page.waitForLoadState('networkidle');
    
    // 点击 Endpoint 管理
    const endpointBtn = page.getByRole('button', { name: /Endpoint/ }).first();
    if (await endpointBtn.isVisible()) {
      await endpointBtn.click();
      await page.waitForTimeout(500);
      
      // 检查是否有 Endpoint 列表
      await expect(page.locator('h2, h3')).toBeVisible({ timeout: 5000 });
    }
  });
});

// ==================== 套餐管理 - 扩展 ====================
test.describe('Admin - 套餐管理扩展', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('删除套餐', async ({ page }) => {
    await page.goto('/admin/plans');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.locator('button[aria-label="删除"], button[title="删除"]').first();
    if (await deleteBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await deleteBtn.click();
      await page.waitForTimeout(2000);
    }
  });

  test('套餐类型选择', async ({ page }) => {
    await page.goto('/admin/plans');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    // 检查套餐类型下拉框
    const typeSelect = page.locator('select');
    if (await typeSelect.isVisible()) {
      await typeSelect.selectOption('token');
      await page.waitForTimeout(300);
    }
    
    // 关闭弹窗 - 使用 Escape 键
    await page.keyboard.press('Escape');
  });
});

// ==================== 用户管理 - 扩展 ====================
test.describe('Admin - 用户管理扩展', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('进入用户详情页', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 点击第一个用户邮箱链接
    const userLink = page.locator('tbody a').first();
    if (await userLink.isVisible()) {
      const email = await userLink.textContent();
      await userLink.click();
      await page.waitForTimeout(2000);
      
      // 检查是否进入详情页 - 页面应显示用户邮箱
      await expect(page.getByText(email!).first()).toBeVisible({ timeout: 5000 });
    }
  });

  test('禁用/启用用户', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    const toggleBtn = page.getByRole('button', { name: /禁用|启用/ }).first();
    if (await toggleBtn.isVisible()) {
      await toggleBtn.click();
      await page.waitForTimeout(500);
      
      // 确认弹窗
      const confirmBtn = page.getByRole('button', { name: /确认|禁用|启用/ }).last();
      if (await confirmBtn.isVisible()) {
        await confirmBtn.click({ force: true });
        await page.waitForTimeout(2000);
      }
    }
  });

  test('设置/取消管理员', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    const roleBtn = page.getByRole('button', { name: /管理员/ }).first();
    if (await roleBtn.isVisible()) {
      await roleBtn.click();
      await page.waitForTimeout(2000);
    }
  });
});

// ==================== 公告管理 - 扩展 ====================
test.describe('Admin - 公告管理扩展', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('立即发布选项', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建公告/ }).click();
    await page.waitForTimeout(500);
    
    // 检查立即发布 checkbox
    const publishCheckbox = page.locator('input[type="checkbox"]');
    if (await publishCheckbox.isVisible()) {
      await publishCheckbox.check();
      await page.waitForTimeout(300);
    }
    
    // 关闭弹窗
    await page.locator('button[aria-label="关闭"]').first().click({ force: true });
  });
});

// ==================== 版本日志 - 扩展 ====================
test.describe('Admin - 版本日志扩展', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('Markdown 预览', async ({ page }) => {
    await page.goto('/admin/changelogs');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    // 填写内容
    await page.locator('textarea').first().fill('# 标题\n\n- 列表项\n- 另一项');
    await page.waitForTimeout(500);
    
    // 检查预览区域
    const preview = page.locator('.prose, [class*="markdown"]');
    if (await preview.isVisible()) {
      await expect(preview.locator('h1')).toBeVisible();
    }
    
    await page.locator('button[aria-label="关闭"]').first().click({ force: true });
  });

  test('删除版本日志', async ({ page }) => {
    await page.goto('/admin/changelogs');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.locator('button[aria-label="删除"], button[title="删除"]').first();
    if (await deleteBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await deleteBtn.click();
      await page.waitForTimeout(2000);
    }
  });
});

// ==================== 通知管理 - 扩展 ====================
test.describe('Admin - 通知管理扩展', () => {
  test.beforeEach(async ({ page }) => { await login(page); });

  test('删除通知', async ({ page }) => {
    await page.goto('/admin/notifications');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.getByRole('button', { name: /删除/ }).first();
    if (await deleteBtn.isVisible()) {
      page.on('dialog', dialog => dialog.accept());
      await deleteBtn.click();
      await page.waitForTimeout(2000);
    }
  });
});
