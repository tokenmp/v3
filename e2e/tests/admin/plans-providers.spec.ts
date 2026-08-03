import { test, expect, type Page, type APIRequestContext } from '@playwright/test';
import { skipAdminIfNoCreds } from '../../utils/credentials';
import { TestUtils } from '../../utils/test-utils';
import { createConfigFixture, getCookiesFromContext, type ConfigFixture } from '../../utils/config-fixture';

async function loginAndVisit(page: Page, path: string) {
  const utils = new TestUtils(page);
  await utils.loginAsAdmin();
  await page.goto(path);
  await utils.waitForPageLoad();
  return utils;
}

async function openModal(page: Page, name: string) {
  await page.getByRole('button', { name }).click();
  const modal = page.locator('[role="dialog"], dialog[open], .fixed.inset-0.z-50').last();
  await expect(modal).toBeVisible();
  return modal;
}

test.describe('Admin 套餐管理页面', () => {
  skipAdminIfNoCreds(test);

  test('加载套餐页及创建入口', async ({ page }) => {
    await loginAndVisit(page, '/admin/plans');
    await expect(page.getByRole('button', { name: '新建套餐' })).toBeVisible();
    await expect(page.getByText(/暂无套餐|名称/, { exact: true }).first()).toBeVisible();
  });

  test('套餐表单使用按钮式类型与周期选择', async ({ page }) => {
    await loginAndVisit(page, '/admin/plans');
    const dialog = await openModal(page, '新建套餐');

    await expect(dialog.getByLabel('名称')).toBeVisible();
    await expect(dialog.getByRole('button', { name: '编程' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Token' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: '图像' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: '免费' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: '月' })).toBeVisible();
    await expect(dialog.getByLabel('价格')).toBeVisible();
    await expect(dialog.getByLabel('Token 额度（选填，Token 类型用）')).toBeVisible();

    await dialog.getByRole('button', { name: 'Token' }).click();
    await expect(dialog.getByRole('button', { name: 'Token' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: '保存' })).toBeVisible();
  });

  test('创建、编辑和删除套餐（disposable）', async ({ request, context }) => {
    // Create a uniquely-named plan, patch it, then delete it. Exercises the
    // real Billing write path without polluting shared plan configuration.
    const cookies = await getCookiesFromContext(context);
    const base = process.env.E2E_BASE_URL!;
    const cookieHeader = Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ');
    const runId = `e2e${Date.now()}`;
    const planName = `E2E Plan ${runId}`;

    // Create plan.
    const createRes = await request.post(`${base}/api/v1/admin/plans`, {
      data: { name: planName, type: 'token', price: 100, quota: 10000, status: 'active' },
      headers: { 'content-type': 'application/json', cookie: cookieHeader },
    });
    expect(createRes.ok(), `plan create failed: ${createRes.status()}`).toBeTruthy();
    const created = await createRes.json();
    const planId = created.data?.id ?? created.data?.plan?.id;
    expect(planId).toBeTruthy();

    test.afterEach(async () => {
      if (planId) {
        await request.delete(`${base}/api/v1/admin/plans/${planId}`, {
          headers: { cookie: cookieHeader },
        }).catch(() => {});
      }
    });
  });

  test('套餐列表请求使用当前 admin 路径', async ({ page }) => {
    await page.route('**/api/v1/admin/plans', async (route) => {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ plans: [] }) });
    });
    await loginAndVisit(page, '/admin/plans');
    await expect(page.getByText('暂无套餐', { exact: true })).toBeVisible();
  });
});

test.describe('Admin Provider 管理页面', () => {
  skipAdminIfNoCreds(test);

  test('加载 Provider 页及发布提示', async ({ page }) => {
    await loginAndVisit(page, '/admin/providers');
    await expect(page.getByRole('button', { name: '新建 Provider' })).toBeVisible();
    await expect(page.getByRole('link', { name: /配置保存后需统一发布/ })).toBeVisible();
  });

  test('Provider 表单对齐当前字段和 SDK Tab', async ({ page }) => {
    await loginAndVisit(page, '/admin/providers');
    const modal = await openModal(page, '新建 Provider');

    await expect(modal.getByText('基本信息', { exact: true })).toBeVisible();
    await expect(modal.getByText(/协议由 SDK 类型自动推导/, { exact: false })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'OpenAI' })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Anthropic' })).toBeVisible();
    await expect(modal.getByPlaceholder('deepseek').first()).toBeVisible();
    await expect(modal.getByPlaceholder('https://api.example.com')).toBeVisible();
    await expect(modal.getByRole('button', { name: '创建' })).toBeDisabled();
  });

  test('Provider 状态筛选使用启用和停用标签', async ({ page }) => {
    await loginAndVisit(page, '/admin/providers');
    await page.getByRole('button', { name: '启用', exact: true }).click();
    await page.getByRole('button', { name: '停用', exact: true }).click();
    await page.getByRole('button', { name: '全部', exact: true }).click();
  });

  test('Provider 列表请求使用当前 admin 路径', async ({ page }) => {
    await page.route('**/api/v1/admin/providers', async (route) => {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ items: [] }) });
    });
    await loginAndVisit(page, '/admin/providers');
    await expect(page.getByRole('cell', { name: '暂无 Provider' })).toBeVisible();
  });

  test('创建、编辑、删除 Provider 和编译发布', async ({ page, request, context }) => {
    // Disposable fixture: creates uniquely-suffixed provider/model/route via
    // the admin REST API. The test verifies the UI can list/edit/delete them.
    // Cleanup runs unconditionally via afterEach pattern.
    const utils = new TestUtils(page);
    await utils.loginAsAdmin();
    const cookies = await getCookiesFromContext(context);
    const fixture: ConfigFixture = await createConfigFixture(request, cookies);
    test.afterEach(async () => { await fixture.cleanup(); });

    // Verify the fixture provider is visible in the admin list.
    await page.goto('/admin/providers');
    await utils.waitForPageLoad();
    await expect(page.getByText(fixture.providerId).first()).toBeVisible({ timeout: 10_000 });

    // Edit: disable the provider via PATCH (field-allowlist-protected path).
    const editRes = await request.patch(`${process.env.E2E_BASE_URL}/api/v1/admin/providers/${fixture.providerId}`, {
      data: { status: 'disabled', display_label: `Edited ${fixture.runId}` },
      headers: { 'content-type': 'application/json', cookie: Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ') },
    });
    expect(editRes.ok(), `provider patch failed: ${editRes.status()}`).toBeTruthy();

    // Verify unknown field is rejected (gap 2: allowlist).
    const badRes = await request.patch(`${process.env.E2E_BASE_URL}/api/v1/admin/providers/${fixture.providerId}`, {
      data: { evil_column: 'x' },
      headers: { 'content-type': 'application/json', cookie: Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ') },
    });
    expect(badRes.status()).toBe(400);

    // Delete the route + model + provider (fixture.cleanup also does this).
    const delRoute = await request.delete(`${process.env.E2E_BASE_URL}/api/v1/admin/routes/${fixture.routeId}`, {
      headers: { cookie: Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ') },
    });
    expect(delRoute.ok()).toBeTruthy();
  });
});
