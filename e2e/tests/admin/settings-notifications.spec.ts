import { expect, test } from '../../utils/fixtures';
import type { Page } from '@playwright/test';
import { skipAdminIfNoCreds } from '../../utils/credentials';
import { TestUtils } from '../../utils/test-utils';

async function loginAndVisit(page: Page, path: string) {
  const utils = new TestUtils(page);
  await utils.loginAsAdmin();
  await page.goto(path);
  await utils.waitForPageLoad();
  return utils;
}

function activeDialog(page: Page) {
  // The current Dialog overlay has no role="dialog"; target its visible
  // container so controls remain scoped even before its accessibility markup
  // is improved by the frontend owner.
  return page.locator('dialog[open], [role="dialog"], .fixed.inset-0.z-50').last();
}

async function openDialog(page: Page) {
  await page.getByRole('button', { name: '发送通知' }).click();
  const dialog = activeDialog(page);
  await expect(dialog).toBeVisible();
  return dialog;
}

function notificationItems(payload: unknown): Array<{ title?: string }> {
  // Direct service responses use the project HTTP envelope while browser API
  // clients unwrap it in core.request. APIRequestContext returns raw JSON.
  if (payload && typeof payload === 'object' && 'data' in payload) {
    return notificationItems((payload as { data: unknown }).data);
  }
  if (payload && typeof payload === 'object' && Array.isArray((payload as { items?: unknown }).items)) {
    return (payload as { items: Array<{ title?: string }> }).items;
  }
  return [];
}

test.describe('Admin 系统设置页面', () => {
  skipAdminIfNoCreds(test);

  test('展示当前平台、认证与服务状态', async ({ page }) => {
    const utils = await loginAndVisit(page, '/admin/settings');
    await utils.checkPageTitle('系统设置');
    await expect(page.getByText('平台信息', { exact: true })).toBeVisible();
    await expect(page.getByText('认证配置', { exact: true })).toBeVisible();
    await expect(page.getByText('服务状态', { exact: true })).toBeVisible();
    await expect(page.getByText('平台名称', { exact: true })).toBeVisible();
    await expect(page.getByText('TokenMP', { exact: true })).toBeVisible();
    await expect(page.getByText('JWT 算法', { exact: true })).toBeVisible();
    await expect(page.getByText('Access Token TTL', { exact: true })).toBeVisible();
    await expect(page.getByText('Edge/BFF (gateway)', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('Auth', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('Notice', { exact: true }).first()).toBeVisible();
  });

  test('服务检查暴露加载或最终的可访问状态', async ({ page }) => {
    await loginAndVisit(page, '/admin/settings');
    await expect(page.getByText(/检查中…|运行中|不可用/, { exact: true }).first()).toBeVisible();
  });

  test('配置发布入口与 UI-only 开关可访问', async ({ page }) => {
    await loginAndVisit(page, '/admin/settings');
    await expect(page.getByText('配置发布', { exact: true })).toBeVisible();
    await expect(page.getByText('功能开关待后端实现，当前仅 UI 演示', { exact: true })).toBeVisible();
    await expect(page.getByRole('checkbox', { name: /用户注册/ })).toBeVisible();
    await expect(page.getByRole('checkbox', { name: /维护模式/ })).toBeVisible();
  });

  test('编译并发布 snapshot', async () => {
    test.skip(true, 'Publishing affects the shared executor configuration; run only against a dedicated disposable Config fixture.');
  });
});

test.describe('Admin 通知管理页面', () => {
  skipAdminIfNoCreds(test);

  test('加载通知页并显示当前列表列', async ({ page }) => {
    const utils = await loginAndVisit(page, '/admin/notifications');
    await utils.checkPageTitle('通知');
    await expect(page.getByRole('button', { name: '发送通知' })).toBeVisible();
    for (const heading of ['接收者', '类型', '标题', 'Action', '已读', '发送时间', '操作']) {
      await expect(page.getByRole('columnheader', { name: heading })).toBeVisible();
    }
  });

  test('广播通知表单使用当前类型和内容字段', async ({ page }) => {
    await loginAndVisit(page, '/admin/notifications');
    const dialog = await openDialog(page);

    await expect(dialog.getByRole('checkbox', { name: '发送给全体用户' })).toBeChecked();
    await expect(dialog.getByRole('combobox').first()).toBeVisible();
    await expect(dialog.getByPlaceholder('标题（必填）')).toBeVisible();
    await expect(dialog.getByPlaceholder('通知内容（必填）')).toBeVisible();
    await expect(dialog.getByRole('button', { name: '发送' })).toBeDisabled();

    await dialog.getByPlaceholder('标题（必填）').fill('仅验证表单');
    await dialog.getByPlaceholder('通知内容（必填）').fill('不提交，避免污染共享 live 环境。');
    await expect(dialog.getByRole('button', { name: '发送' })).toBeEnabled();
  });

  test('指定 disposable 用户通知使用搜索和选择流程', async ({ page, disposableUser }) => {
    await loginAndVisit(page, '/admin/notifications');
    const dialog = await openDialog(page);

    await dialog.getByRole('checkbox', { name: '发送给全体用户' }).click();
    const search = dialog.getByPlaceholder('输入邮箱搜索用户');
    await expect(search).toBeVisible();
    await search.fill(disposableUser.email);
    await expect(dialog.getByText(disposableUser.email, { exact: true })).toBeVisible();
    await dialog.getByText(disposableUser.email, { exact: true }).click();
    await expect(dialog.getByRole('button', { name: '取消选择用户' })).toBeVisible();
  });

  test('Action 选择器仅允许站内 panel 目标', async ({ page }) => {
    await loginAndVisit(page, '/admin/notifications');
    const dialog = await openDialog(page);
    const selects = dialog.getByRole('combobox');
    await selects.nth(1).selectOption('panel-keys');
    await expect(dialog.getByPlaceholder('按钮文本')).toHaveValue('管理 API 密钥');
    await expect(dialog.getByPlaceholder('/panel/xxx')).toHaveValue('/panel/keys');
  });

  test('通知列表请求使用当前 Notice admin 路径', async ({ page }) => {
    await page.route('**/api/v1/notice/admin/notifications', async (route) => {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ items: [] }) });
    });
    await loginAndVisit(page, '/admin/notifications');
    await expect(page.getByRole('cell', { name: '暂无通知' })).toBeVisible();
  });

  test('发送、验证收件并删除 disposable 用户通知', async ({ page, request, disposableUser }) => {
    await loginAndVisit(page, '/admin/notifications');
    const title = `E2E fixture notification ${disposableUser.id}`;
    const dialog = await openDialog(page);
    await dialog.getByRole('checkbox', { name: '发送给全体用户' }).click();
    await dialog.getByPlaceholder('输入邮箱搜索用户').fill(disposableUser.email);
    await dialog.getByText(disposableUser.email, { exact: true }).click();
    await dialog.getByPlaceholder('标题（必填）').fill(title);
    await dialog.getByPlaceholder('通知内容（必填）').fill('Created only for isolated E2E CRUD coverage.');
    await dialog.getByRole('button', { name: '发送', exact: true }).click();
    await expect(page.getByText('通知已发送', { exact: true })).toBeVisible();

    const inbox = await request.get('/api/v1/notice/notifications', {
      headers: { authorization: `Bearer ${disposableUser.accessToken}` },
    });
    expect(inbox.ok()).toBe(true);
    expect(notificationItems(await inbox.json()).some((item) => item.title === title)).toBe(true);

    const row = page.locator('tbody tr').filter({ hasText: title });
    await expect(row).toBeVisible();
    // The desktop delete action is icon-only; this uniquely-created row has a
    // single action button in the current UI.
    await row.getByRole('button').click();
    const confirm = activeDialog(page);
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: /确认/ }).click();
    await expect(row).toBeHidden();
  });
});
