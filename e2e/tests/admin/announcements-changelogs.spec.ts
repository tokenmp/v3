import { expect, test } from '../../utils/fixtures';
import type { Page } from '@playwright/test';
import { skipAdminIfNoCreds } from '../../utils/credentials';
import { TestUtils } from '../../utils/test-utils';

function activeDialog(page: Page) {
  // The app's Dialog currently renders an overlay without role="dialog".
  // Keep all dialog interactions scoped to the visible overlay rather than
  // relying on an absent ARIA role.
  return page.locator('dialog[open], [role="dialog"], .fixed.inset-0.z-50').last();
}

async function openDialog(page: Page, name: string) {
  await page.getByRole('button', { name }).click();
  const dialog = activeDialog(page);
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

  test('创建、编辑和删除 disposable 公告', async ({ page, disposableUser }) => {
    await loginAndVisit(page, '/admin/announcements');
    const title = `E2E fixture announcement ${disposableUser.id}`;
    const dialog = await openDialog(page, '新建公告');
    await dialog.getByLabel('标题').fill(title);
    await dialog.getByLabel('摘要').fill('Disposable E2E record');
    await dialog.getByLabel('内容（Markdown）').fill('Created only for isolated E2E CRUD coverage.');
    await dialog.getByRole('button', { name: '保存', exact: true }).click();
    await expect(page.getByText('公告已创建', { exact: true })).toBeVisible();

    const row = page.locator('tbody tr').filter({ hasText: title });
    await expect(row).toBeVisible();
    // Desktop action controls are icon-only, with edit before delete in the
    // current table. Scope the structural fallback to this uniquely-created row.
    await row.getByRole('button').first().click();
    const edit = activeDialog(page);
    await expect(edit).toBeVisible();
    await edit.getByLabel('摘要').fill('Disposable E2E record updated');
    await edit.getByRole('button', { name: '保存', exact: true }).click();
    await expect(page.getByText('公告已更新', { exact: true })).toBeVisible();

    await row.getByRole('button').nth(1).click();
    const confirm = activeDialog(page);
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: '删除', exact: true }).click();
    await expect(page.getByText('公告已删除', { exact: true })).toBeVisible();
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

  test('创建、编辑和删除 disposable 版本日志', async ({ page, disposableUser }) => {
    await loginAndVisit(page, '/admin/changelogs');
    const version = `e2e-${disposableUser.id.slice(0, 8)}`;
    const title = `E2E fixture changelog ${disposableUser.id}`;
    const dialog = await openDialog(page, '新建版本');
    await dialog.getByLabel('版本号').fill(version);
    await dialog.getByLabel('标题').fill(title);
    await dialog.getByLabel('内容（Markdown）').fill('Created only for isolated E2E CRUD coverage.');
    await dialog.getByRole('button', { name: '创建', exact: true }).click();
    await expect(page.getByText('版本日志已创建', { exact: true })).toBeVisible();

    const row = page.locator('tbody tr').filter({ hasText: title });
    await expect(row).toBeVisible();
    // Desktop edit/delete controls are icon-only. The order is fixed by the
    // current UI, and the row is unique to this disposable test record.
    await row.getByRole('button').first().click();
    const edit = activeDialog(page);
    await expect(edit).toBeVisible();
    await edit.getByLabel('标题').fill(`${title} updated`);
    await edit.getByRole('button', { name: '保存', exact: true }).click();
    await expect(page.getByText('版本日志已更新', { exact: true })).toBeVisible();

    await row.getByRole('button').nth(1).click();
    const confirm = activeDialog(page);
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: /确认/ }).click();
    await expect(page.getByText('版本日志已删除', { exact: true })).toBeVisible();
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
