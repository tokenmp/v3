import { test, expect, type Page } from '@playwright/test';
import { skipAdminIfNoCreds } from '../utils/credentials';
import { TestUtils } from '../utils/test-utils';

async function loginAndVisit(page: Page, path: string) {
  const utils = new TestUtils(page);
  await utils.loginAsAdmin();
  await page.goto(path);
  await utils.waitForPageLoad();
}

const readOnlyPages = [
  ['/admin/models', '模型配置'],
  ['/admin/routes', '路由配置'],
  ['/admin/credentials', '上游账号'],
  ['/admin/retry', '重试策略'],
  ['/admin/auto-model', 'Auto 模型'],
  ['/admin/api-keys', 'API 密钥'],
  ['/admin/billing/usage', '用量统计'],
  ['/admin/request-logs', '请求日志'],
] as const;

test.describe('Admin 完整只读路由覆盖', () => {
  skipAdminIfNoCreds(test);

  for (const [path, navigationLabel] of readOnlyPages) {
    test(`${navigationLabel} 路由可访问`, async ({ page }) => {
      await loginAndVisit(page, path);
      expect(page.url()).toContain(path);
      await expect(page.locator('main')).toBeVisible();
    });
  }

  test('执行配置页使用 DOM 内容就绪而非 networkidle', async ({ page }) => {
    await loginAndVisit(page, '/admin/models');
    await expect(page.getByRole('button', { name: /新建/ })).toBeVisible();
  });

  test('用户详情是套餐分配和限制覆盖的当前入口', async ({ page }) => {
    await loginAndVisit(page, '/admin/users');
    const firstUser = page.locator('tbody a').first();
    test.skip(!(await firstUser.isVisible().catch(() => false)), 'Live Admin fixture has no user detail row to inspect.');
    await firstUser.click();
    await expect(page).toHaveURL(/\/admin\/users\//);
    await expect(page.locator('main')).toBeVisible();
  });

  test('已移除的用户套餐列表路由', async () => {
    test.skip(true, 'The standalone /admin/user-plans route was removed; plan assignment now belongs to the user detail page.');
  });

  test('共享环境中的执行配置写操作', async () => {
    test.skip(true, 'Model, route, credential, retry, auto-model, Provider, and publish actions mutate shared executor configuration; run them only against a dedicated disposable Config fixture.');
  });
});
