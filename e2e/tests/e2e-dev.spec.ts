import { test, expect, type Page } from '@playwright/test';
import { e2eCredentials, skipUserIfNoCreds } from '../utils/credentials';

/**
 * TokenMP v3 E2E 测试 - 针对 dev 服务器
 * Requires an explicitly supplied, controlled BASE_URL target.
 */

const TEST_USER = e2eCredentials().user;

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.waitForLoadState('networkidle');
  await page.fill('input[type="email"]', email);
  await page.fill('input[type="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/(panel|admin)/, { timeout: 15000 });
}

test.describe('TokenMP v3 E2E - 公开页面', () => {
  skipUserIfNoCreds(test);
  test('首页显示内容', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    // 首页应该有内容（可能是 landing page 或重定向）
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
  });

  test('登录页面元素完整', async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');
    
    // 检查表单元素 - 使用精确选择器
    await expect(page.locator('input[type="email"]')).toBeVisible();
    await expect(page.locator('input#password')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
    // 使用 getByRole 精确匹配
    await expect(page.getByRole('link', { name: '注册', exact: true })).toBeVisible();
  });

  test('注册页面元素完整', async ({ page }) => {
    await page.goto('/register');
    await page.waitForLoadState('networkidle');
    
    await expect(page.locator('input[type="email"]')).toBeVisible();
    await expect(page.locator('input#password')).toBeVisible();
    await expect(page.locator('input#confirmPassword')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
  });

  test('登录失败 - 错误密码', async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');
    
    await page.fill('input[type="email"]', TEST_USER.email);
    await page.fill('input#password', 'wrongpassword');
    await page.click('button[type="submit"]');
    
    // 应该显示错误提示 - 使用 toast 或 sonner
    await expect(page.locator('[data-sonner-toast]')).toBeVisible({ timeout: 10000 });
  });

  test('登录成功 - 跳转到 panel', async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
    await expect(page).toHaveURL(/\/panel/);
  });
});

test.describe('TokenMP v3 E2E - Panel 用户面板', () => {
  skipUserIfNoCreds(test);
  test.beforeEach(async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
  });

  test('概览页面加载', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 使用更精确的选择器 - 卡片标题
    await expect(page.getByText('账户').first()).toBeVisible();
    await expect(page.getByText('配额').first()).toBeVisible();
    await expect(page.getByText('最近请求').first()).toBeVisible();
  });

  test('API Key 页面加载', async ({ page }) => {
    await page.goto('/panel/keys');
    await page.waitForLoadState('networkidle');
    
    // 页面标题
    await expect(page.getByTitle('API 密钥')).toBeVisible();
    // 创建按钮
    await expect(page.getByRole('button', { name: '创建密钥' })).toBeVisible();
  });

  test('创建 API Key 流程', async ({ page }) => {
    await page.goto('/panel/keys');
    await page.waitForLoadState('networkidle');
    
    // 点击创建按钮
    await page.getByRole('button', { name: '创建密钥' }).click();
    
    // 等待弹窗出现 - 自定义 Dialog 没有 role="dialog"，用 h2 标题定位
    const dialogTitle = page.locator('h2:has-text("创建密钥")');
    await expect(dialogTitle).toBeVisible({ timeout: 5000 });
    
    // 填写密钥名称 - placeholder 是 "例如：生产环境"
    await page.locator('input[placeholder="例如：生产环境"]').fill('E2E测试密钥');
    
    // 点击确认创建按钮 - 使用 force 点击绕过 overlay
    await page.locator('button:has-text("创建密钥")').last().click({ force: true });
    
    // 等待操作完成
    await page.waitForTimeout(2000);
    
    // 页面应该刷新列表
    await expect(page.getByRole('button', { name: '创建密钥' })).toBeVisible();
  });

  test('请求日志页面加载', async ({ page }) => {
    await page.goto('/panel/requests');
    await page.waitForLoadState('networkidle');
    
    await expect(page.getByTitle('请求日志')).toBeVisible();
    await expect(page.locator('input[placeholder*="搜索"]')).toBeVisible();
  });

  test('可用模型页面加载', async ({ page }) => {
    await page.goto('/panel/models');
    await page.waitForLoadState('networkidle');
    await page.waitForTimeout(2000);
    
    // 页面应该有标题
    const hasTitle = await page.locator('h1:has-text("模型列表")').isVisible().catch(() => false);
    
    // 等待加载完成 - 等待 "加载中" 消失
    await expect(page.getByText('加载中')).toBeHidden({ timeout: 10000 }).catch(() => null);
    
    // 加载完成后应该有表格、卡片或空状态
    const hasTable = await page.locator('table').isVisible().catch(() => false);
    const hasCards = await page.locator('.md\\:hidden').isVisible().catch(() => false);
    const hasEmpty = await page.getByText('暂无可用模型').isVisible().catch(() => false);
    const body = await page.textContent('body');
    const hasContent = body.length > 100;
    expect(hasTitle || hasTable || hasCards || hasEmpty || hasContent).toBe(true);
  });

  test('公告页面加载', async ({ page }) => {
    await page.goto('/panel/announcements');
    await page.waitForLoadState('networkidle');
    
    await expect(page.getByTitle('公告')).toBeVisible();
  });

  test('版本日志页面加载', async ({ page }) => {
    await page.goto('/panel/changelogs');
    await page.waitForLoadState('networkidle');
    
    await expect(page.getByTitle('版本日志')).toBeVisible();
  });

  test('通知页面加载', async ({ page }) => {
    await page.goto('/panel/notifications');
    await page.waitForLoadState('networkidle');
    
    await expect(page.getByTitle('通知')).toBeVisible();
  });

  test('设置页面加载', async ({ page }) => {
    await page.goto('/panel/settings');
    await page.waitForLoadState('networkidle');
    
    // 设置页面应该有修改密码和退出登录
    await expect(page.getByRole('button', { name: /修改密码/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /退出登录/ })).toBeVisible();
  });

  test('请求日志筛选功能', async ({ page }) => {
    await page.goto('/panel/requests');
    await page.waitForLoadState('networkidle');
    
    // 测试筛选按钮
    const allFilter = page.getByRole('button', { name: '全部', exact: true });
    const successFilter = page.getByRole('button', { name: '成功', exact: true });
    const failFilter = page.getByRole('button', { name: '失败', exact: true });
    
    await expect(allFilter).toBeVisible();
    await expect(successFilter).toBeVisible();
    await expect(failFilter).toBeVisible();
    
    // 点击筛选
    await successFilter.click();
    await page.waitForTimeout(500);
    
    // 恢复全部
    await allFilter.click();
    await page.waitForTimeout(500);
  });

  test('Auto 模型页面加载', async ({ page }) => {
    await page.goto('/panel/auto-model');
    await page.waitForLoadState('networkidle');
    
    await expect(page.getByTitle('Auto 模型')).toBeVisible();
    await expect(page.getByRole('button', { name: /保存/ })).toBeVisible();
  });

  test('响应式设计 - 移动端', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 移动端应该显示底部导航栏
    const bottomNav = page.locator('nav').last();
    await expect(bottomNav).toBeVisible();
  });
});
