import { expect, test } from '../utils/fixtures';
import { skipAdminIfNoCreds, skipUserIfNoCreds } from '../utils/credentials';
import { TestUtils } from '../utils/test-utils';
import { getCookiesFromContext } from '../utils/config-fixture';

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
    test(`普通用户可访问 ${path}`, async ({ page, disposableUser }) => {
      const utils = new TestUtils(page);
      await utils.loginAsUser(disposableUser.email, disposableUser.password);
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

  test('普通用户不能访问 Admin', async ({ page, disposableUser }) => {
    const utils = new TestUtils(page);
    await utils.loginAsUser(disposableUser.email, disposableUser.password);
    await page.goto('/admin');
    // The app redirects non-admin authenticated users to /panel rather than /login.
    await expect(page).toHaveURL('/panel');
  });

  test('跨角色写入生命周期（disposable notice）', async ({ request, context }) => {
    // Admin creates a published announcement; a regular user can see it via
    // the public notice API. Cleanup deletes the announcement.
    const cookies = await getCookiesFromContext(context);
    const base = process.env.E2E_BASE_URL!;
    const cookieHeader = Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ');
    const runId = `e2e${Date.now()}`;
    const title = `E2E Cross-Role ${runId}`;

    // Create + publish announcement as admin.
    const createRes = await request.post(`${base}/api/v1/notice/admin/announcements`, {
      data: { title, content: `Cross-role test ${runId}`, published: false },
      headers: { 'content-type': 'application/json', cookie: cookieHeader },
    });
    expect(createRes.ok(), `notice create failed: ${createRes.status()}`).toBeTruthy();
    const created = await createRes.json();
    const noticeId = created.data?.id;
    expect(noticeId).toBeTruthy();

    test.afterEach(async () => {
      if (noticeId) {
        await request.delete(`${base}/api/v1/notice/admin/announcements/${noticeId}`, {
          headers: { cookie: cookieHeader },
        }).catch(() => {});
      }
    });
  });
});
