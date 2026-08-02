import { test, expect } from '@playwright/test';
import { skipAdminIfNoCreds, skipUserIfNoCreds } from '../utils/credentials';
import { TestUtils } from '../utils/test-utils';

const adminPaths = [
  '/admin/users',
  '/admin/plans',
  '/admin/providers',
  '/admin/models',
  '/admin/announcements',
  '/admin/settings',
] as const;

const panelPaths = [
  '/panel/keys',
  '/panel/requests',
  '/panel/models',
  '/panel/announcements',
  '/panel/notifications',
] as const;

test.describe('TokenMP v3 跨角色组合流程', () => {
  skipAdminIfNoCreds(test);
  skipUserIfNoCreds(test);

  test('认证页在登录和注册之间导航', async ({ page }) => {
    await page.goto('/login');
    await expect(page.getByRole('link', { name: /注册/ }).first()).toBeVisible();
    await page.getByRole('link', { name: /注册/ }).first().click();
    await expect(page).toHaveURL('/register');
    await expect(page.getByRole('link', { name: /登录/ }).first()).toBeVisible();
  });

  for (const path of adminPaths) {
    test(`管理员可访问 ${path}`, async ({ page }) => {
      const utils = new TestUtils(page);
      await utils.loginAsAdmin();
      await page.goto(path);
      await expect(page).toHaveURL(path);
      await expect(page.locator('body')).toBeVisible();
    });
  }

  for (const path of panelPaths) {
    test(`普通用户可访问 ${path}`, async ({ page }) => {
      const utils = new TestUtils(page);
      await utils.loginAsUser();
      await page.goto(path);
      await expect(page).toHaveURL(path);
      await expect(page.locator('body')).toBeVisible();
    });
  }

  test('未认证与管理员访问受保护入口维持当前行为', async ({ page }) => {
    const utils = new TestUtils(page);
    await page.goto('/admin');
    await expect(page).toHaveURL(/\/login(?:\?reason=session_expired)?$/);
    await utils.loginAsAdmin();
    await page.goto('/panel');
    await expect(page).toHaveURL('/panel');
  });

  test('管理员和用户共享的响应式壳层切换', async ({ page }) => {
    const utils = new TestUtils(page);
    await utils.loginAsAdmin();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.locator('nav.md\\:hidden')).toBeVisible();
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(page.locator('aside')).toBeVisible();
  });

  test('普通用户不能访问 Admin', async () => {
    test.skip(true, 'The supplied live user identity is currently an admin; this authorization assertion requires a distinct disposable non-admin fixture.');
  });

  test('跨角色写入生命周期', async () => {
    test.skip(true, 'API key, package, announcement, notification, and user mutations change shared live data; run only against dedicated disposable fixtures.');
  });
});
