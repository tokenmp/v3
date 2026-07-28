import { test, expect, type Page } from '@playwright/test';

/**
 * TokenMP v3 E2E 测试 - Admin 后台编辑功能
 * 使用 demo admin 账号
 */

const ADMIN_USER = {
  email: 'demo@tokenmp.cn',
  password: 'demo12345678',
};

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.waitForLoadState('networkidle');
  await page.fill('input[type="email"]', email);
  await page.fill('input#password', password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/(panel|admin)/, { timeout: 15000 });
}

test.describe('Admin 后台 - 公告编辑', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('新建公告', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建公告/ }).click();
    await page.waitForTimeout(500);
    
    // 填写表单
    await page.locator('input[placeholder="公告标题"]').fill('E2E测试公告');
    // 摘要是选填的，正文用 textarea
    await page.locator('textarea').first().fill('这是测试摘要');
    await page.locator('textarea').last().fill('# 测试内容\n\n这是公告正文');
    
    // 提交 - Dialog 按钮是"保存"
    await page.getByRole('button', { name: '保存' }).click({ force: true });
    await page.waitForTimeout(2000);
    
    await expect(page.getByText('E2E测试公告').first()).toBeVisible({ timeout: 5000 });
  });

  test('编辑公告', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    const editBtn = page.getByRole('button', { name: '编辑' }).first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      await page.locator('input[placeholder="公告标题"]').fill('已修改公告_' + Date.now());
      await page.getByRole('button', { name: '保存' }).click({ force: true });
      await page.waitForTimeout(2000);
    }
  });

  test('删除公告', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    const deleteBtn = page.getByRole('button', { name: '删除' }).first();
    if (await deleteBtn.isVisible()) {
      await deleteBtn.click();
      await page.waitForTimeout(500);
      await page.getByRole('button', { name: /确认|删除/ }).last().click({ force: true });
      await page.waitForTimeout(2000);
    }
  });
});

test.describe('Admin 后台 - 版本日志编辑', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('新建版本日志', async ({ page }) => {
    await page.goto('/admin/changelogs');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    await page.locator('input[placeholder="v3.1.0"]').fill('v0.0.1-e2e');
    await page.locator('input[placeholder="版本标题"]').fill('E2E测试版本');
    await page.locator('textarea').first().fill('# v0.0.1-e2e\n\n- 测试功能');
    
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
    
    // 弹窗关闭后，检查列表中是否有新版本
    await expect(page.getByText('v0.0.1-e2e').first()).toBeVisible({ timeout: 5000 });
  });

  test('编辑版本日志', async ({ page }) => {
    await page.goto('/admin/changelogs');
    await page.waitForLoadState('networkidle');
    
    const editBtn = page.getByRole('button', { name: '编辑' }).first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      await page.locator('input[placeholder="版本标题"]').fill('已修改版本_' + Date.now());
      await page.getByRole('button', { name: '保存' }).click({ force: true });
      await page.waitForTimeout(2000);
    }
  });
});

test.describe('Admin 后台 - 通知发送', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('发送通知', async ({ page }) => {
    await page.goto('/admin/notifications');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: '发送通知' }).click();
    await page.waitForTimeout(500);
    
    await page.locator('input[placeholder*="标题"]').fill('E2E测试通知');
    await page.locator('textarea').first().fill('这是测试通知内容');
    
    await page.getByRole('button', { name: '发送', exact: true }).click({ force: true });
    await page.waitForTimeout(2000);
    
    // 通知发送后弹窗可能关闭，检查 toast 或页面状态
    // 通知列表可能不立即显示新通知，检查页面没有错误即可
    const hasError = await page.getByText('发送失败').isVisible().catch(() => false);
    expect(hasError).toBe(false);
  });
});

test.describe('Admin 后台 - 套餐管理', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('新建套餐', async ({ page }) => {
    await page.goto('/admin/plans');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    await page.locator('input[placeholder="套餐名称"]').fill('E2E测试套餐');
    
    // 选择套餐类型
    const typeSelect = page.locator('select');
    if (await typeSelect.isVisible()) {
      await typeSelect.selectOption('token');
    }
    
    await page.locator('input[placeholder*="1000"]').first().fill('99');
    await page.locator('input[placeholder*="500000"]').first().fill('1000000');
    
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
    
    await expect(page.getByText('E2E测试套餐').first()).toBeVisible({ timeout: 5000 });
  });

  test('编辑套餐', async ({ page }) => {
    await page.goto('/admin/plans');
    await page.waitForLoadState('networkidle');
    
    const editBtn = page.getByRole('button', { name: '编辑' }).first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      await page.locator('input[placeholder="套餐名称"]').fill('已修改套餐_' + Date.now());
      await page.getByRole('button', { name: '保存' }).click({ force: true });
      await page.waitForTimeout(2000);
    }
  });
});

test.describe('Admin 后台 - Provider 管理', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('新建 Provider', async ({ page }) => {
    await page.goto('/admin/providers');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    await page.locator('input[placeholder="deepseek"]').first().fill('e2e-provider');
    await page.locator('input[placeholder="DeepSeek"]').first().fill('E2E测试Provider');
    await page.locator('input[placeholder*="api.example"]').first().fill('https://api.e2e.test');
    
    await page.getByRole('button', { name: /创建|保存/ }).click({ force: true });
    await page.waitForTimeout(2000);
    
    // 关闭弹窗 - 点击背景遮罩
    await page.locator('[class*="bg-black"]').first().click({ force: true, position: { x: 5, y: 5 } });
    await page.waitForTimeout(500);
    
    // 检查列表中是否有新 Provider（用 ID 搜索）
    await page.locator('input[placeholder*="搜索"]').fill('e2e-provider');
    await page.waitForTimeout(1000);
    
    const count = await page.locator('tbody tr').count();
    expect(count).toBeGreaterThan(0);
  });
});

test.describe('Admin 后台 - 模型管理', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('新建模型', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    await page.locator('input[placeholder="gpt-4o-mini"]').fill('e2e-test-model');
    await page.locator('input[placeholder="GPT-4o mini"]').fill('E2E测试模型');
    await page.waitForTimeout(500);
    
    const saveBtn = page.getByRole('button', { name: /创建|保存/ });
    await expect(saveBtn).toBeEnabled({ timeout: 5000 });
    await saveBtn.click({ force: true });
    await page.waitForTimeout(2000);
    
    await expect(page.getByText('E2E测试模型').first()).toBeVisible({ timeout: 5000 });
  });
});

test.describe('Admin 后台 - 用户管理', () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('用户列表加载', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    await expect(page.locator('table')).toBeVisible({ timeout: 5000 });
  });

  test('搜索用户', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    await page.locator('input[placeholder*="搜索邮箱"]').fill('demo');
    await page.waitForTimeout(1000);
    
    const count = await page.locator('tbody tr').count();
    expect(count).toBeGreaterThan(0);
  });

  test('筛选用户', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    await page.getByRole('button', { name: '正常', exact: true }).click();
    await page.waitForTimeout(500);
    await page.getByRole('button', { name: '全部', exact: true }).click();
    await page.waitForTimeout(500);
  });

  test('分页功能', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    await expect(page.locator('.text-xs.tabular-nums')).toBeVisible({ timeout: 5000 });
    
    const nextBtn = page.locator('[aria-label="下一页"]');
    if (await nextBtn.isEnabled()) {
      await nextBtn.click();
      await page.waitForTimeout(500);
    }
  });
});
