import { test, expect, type Page } from '@playwright/test';
import { skipAdminIfNoCreds } from '../../utils/credentials';
import { TestUtils } from '../../utils/test-utils';

async function openDialog(page: Page, name: string) {
  await page.getByRole('button', { name }).click();
  const dialog = page.locator('[role="dialog"], dialog[open], .fixed.inset-0.z-50').last();
  await expect(dialog).toBeVisible();
  return dialog;
}

async function loginAndVisit(page: Page, path: string) {
  const utils = new TestUtils(page);
  await utils.loginAsAdmin();
  await page.goto(path);
  await utils.waitForPageLoad();
  return utils;
}

test.describe('Admin 公告管理页面', () => {
  skipAdminIfNoCreds(test);

  test('加载公告管理页及创建入口', async ({ page }) => {
    await loginAndVisit(page, '/admin/announcements');
    await expect(page.getByRole('button', { name: '新建公告' })).toBeVisible();
    await expect(page.getByText(/暂无公告|标题/, { exact: true }).first()).toBeVisible();
  });

  test('新建公告表单使用当前控件并校验标题', async ({ page }) => {
    await loginAndVisit(page, '/admin/announcements');
    const dialog = await openDialog(page, '新建公告');

    await expect(dialog.getByLabel('标题')).toBeVisible();
    await expect(dialog.getByLabel('摘要')).toBeVisible();
    await expect(dialog.getByLabel('内容（Markdown）')).toBeVisible();
    await expect(dialog.getByRole('button', { name: '提醒' })).toBeVisible();
    await expect(dialog.getByRole('button', { name: '警告' })).toBeVisible();
    await expect(dialog.getByRole('checkbox', { name: '立即发布' })).toBeVisible();

    await dialog.getByRole('button', { name: '保存' }).click();
    await expect(page.getByText('请填写标题', { exact: true })).toBeVisible();
  });

  test('创建、编辑和删除公告', async () => {
    test.skip(true, 'Requires an isolated Notice fixture: this live target has shared announcement data and the CRUD flow is destructive.');
  });

  test('公告列表请求使用当前 Notice admin 路径', async ({ page }) => {
    await page.route('**/api/v1/notice/admin/announcements', async (route) => {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ items: [] }) });
    });
    await loginAndVisit(page, '/admin/announcements');
    await expect(page.getByText('暂无公告', { exact: true })).toBeVisible();
  });

  test('公告列表请求失败时不伪造旧版加载失败文案', async ({ page }) => {
    await page.route('**/api/v1/notice/admin/announcements', async (route) => {
      await route.abort('failed');
    });
    await loginAndVisit(page, '/admin/announcements');
    await expect(page.getByRole('button', { name: '新建公告' })).toBeVisible();
  });
});

test.describe('Admin 版本日志管理页面', () => {
  skipAdminIfNoCreds(test);

  test('加载版本日志页及创建入口', async ({ page }) => {
    const utils = await loginAndVisit(page, '/admin/changelogs');
    await utils.checkPageTitle('版本日志');
    await expect(page.getByRole('button', { name: '新建版本' })).toBeVisible();
    await expect(page.getByText(/暂无版本日志|版本号/, { exact: true }).first()).toBeVisible();
  });

  test('新建版本日志表单显示 Markdown 预览并校验必填字段', async ({ page }) => {
    await loginAndVisit(page, '/admin/changelogs');
    const dialog = await openDialog(page, '新建版本');

    await expect(dialog.getByLabel('版本号')).toBeVisible();
    await expect(dialog.getByLabel('标题')).toBeVisible();
    const body = dialog.getByLabel('内容（Markdown）');
    await body.fill('# E2E 预览标题\n\n- 条目');
    await expect(dialog.getByText('预览', { exact: true })).toBeVisible();
    await expect(dialog.getByRole('heading', { name: 'E2E 预览标题' })).toBeVisible();

    await dialog.getByLabel('版本号').fill('v-e2e-preview');
    await dialog.getByRole('button', { name: '创建' }).click();
    await expect(page.getByText('请填写版本号和标题', { exact: true })).toBeVisible();
  });

  test('创建、编辑和删除版本日志', async () => {
    test.skip(true, 'Requires an isolated Notice fixture: this live target has shared changelog data and the CRUD flow is destructive.');
  });

  test('版本日志列表请求使用当前 Notice admin 路径', async ({ page }) => {
    await page.route('**/api/v1/notice/admin/changelogs', async (route) => {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ items: [] }) });
    });
    await loginAndVisit(page, '/admin/changelogs');
    await expect(page.getByText('暂无版本日志', { exact: true })).toBeVisible();
  });

  test('版本日志列表失败不依赖已删除的统一错误文案', async ({ page }) => {
    await page.route('**/api/v1/notice/admin/changelogs', async (route) => {
      await route.abort('failed');
    });
    await loginAndVisit(page, '/admin/changelogs');
    await expect(page.getByRole('button', { name: '新建版本' })).toBeVisible();
  });
});
