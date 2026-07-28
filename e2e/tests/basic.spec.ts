import { test, expect } from '@playwright/test';

test.describe('TokenMP v3 基础测试', () => {
  test('首页加载', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    
    // 检查页面是否加载
    await expect(page).toHaveTitle(/TokenMP/);
  });

  test('登录页面', async ({ page }) => {
    await page.goto('/login');
    await page.waitForLoadState('networkidle');
    
    // 检查登录表单
    await expect(page.locator('input[type="email"]')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
  });

  test('注册页面', async ({ page }) => {
    await page.goto('/register');
    await page.waitForLoadState('networkidle');
    
    // 检查注册表单
    await expect(page.locator('input[type="email"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toBeVisible();
    await expect(page.locator('input[name="confirmPassword"]')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
  });

  test('响应式设计', async ({ page }) => {
    await page.goto('/');
    await page.waitForLoadState('networkidle');
    
    // 桌面视图
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);
    
    // 移动视图
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(300);
  });
});
