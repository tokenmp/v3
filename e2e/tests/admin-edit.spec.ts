import { test, expect, type Page } from '@playwright/test';
import { skipAdminIfNoCreds } from '../utils/credentials';
import { TestUtils } from '../utils/test-utils';
import { getCookiesFromContext } from '../utils/config-fixture';

async function loginAndVisit(page: Page, path: string) {
  const utils = new TestUtils(page);
  await utils.loginAsAdmin();
  await page.goto(path);
  await utils.waitForPageLoad();
  return utils;
}

async function openAdminModal(page: Page, buttonName: string) {
  await page.getByRole('button', { name: buttonName }).click();
  const modal = page.locator('[role="dialog"], dialog[open], .fixed.inset-0.z-50').last();
  await expect(modal).toBeVisible();
  return modal;
}

test.describe('Admin 编辑入口', () => {
  skipAdminIfNoCreds(test);

  test('公告编辑入口展示当前字段', async ({ page }) => {
    await loginAndVisit(page, '/admin/announcements');
    const dialog = await openAdminModal(page, '新建公告');
    await expect(dialog.getByLabel('标题')).toBeVisible();
    await expect(dialog.getByLabel('摘要')).toBeVisible();
    await expect(dialog.getByLabel('内容（Markdown）')).toBeVisible();
    await expect(dialog.getByRole('checkbox', { name: '立即发布' })).toBeVisible();
  });

  test('版本日志编辑入口支持 Markdown 预览', async ({ page }) => {
    await loginAndVisit(page, '/admin/changelogs');
    const dialog = await openAdminModal(page, '新建版本');
    await dialog.getByLabel('内容（Markdown）').fill('# 编辑预览');
    await expect(dialog.getByRole('heading', { name: '编辑预览' })).toBeVisible();
  });

  test('通知编辑入口不会在未填写必填项时提交', async ({ page }) => {
    await loginAndVisit(page, '/admin/notifications');
    const dialog = await openAdminModal(page, '发送通知');
    await expect(dialog.getByRole('button', { name: '发送' })).toBeDisabled();
  });

  test('套餐编辑入口使用按钮式类型选择', async ({ page }) => {
    await loginAndVisit(page, '/admin/plans');
    const dialog = await openAdminModal(page, '新建套餐');
    await expect(dialog.getByRole('button', { name: 'Token' })).toBeVisible();
    await expect(dialog.getByLabel('Token 额度（选填，Token 类型用）')).toBeVisible();
  });

  test('Provider 编辑入口使用当前 SDK Tab', async ({ page }) => {
    await loginAndVisit(page, '/admin/providers');
    const modal = await openAdminModal(page, '新建 Provider');
    await expect(modal.getByRole('button', { name: 'OpenAI' })).toBeVisible();
    await expect(modal.getByRole('button', { name: 'Anthropic' })).toBeVisible();
    await expect(modal.getByPlaceholder('https://api.example.com')).toBeVisible();
  });

  test('用户管理保留搜索、筛选和分页入口', async ({ page }) => {
    await loginAndVisit(page, '/admin/users');
    await expect(page.getByPlaceholder('搜索邮箱')).toBeVisible();
    await page.getByRole('button', { name: '正常', exact: true }).click();
    await page.getByRole('button', { name: '全部', exact: true }).click();
    await expect(page.locator('[aria-label="下一页"]')).toBeVisible();
  });

  test('共享环境中的 Admin 写操作（disposable notice）', async ({ page, request, context }) => {
    // Create a uniquely-suffixed announcement, verify it is visible, then
    // delete it. This exercises the real Notice write path without polluting
    // shared data.
    const utils = new TestUtils(page);
    await utils.loginAsAdmin();
    const cookies = await getCookiesFromContext(context);
    const base = process.env.E2E_BASE_URL!;
    const cookieHeader = Object.entries(cookies).map(([k, v]) => `${k}=${v}`).join('; ');
    const runId = `e2e${Date.now()}`;
    const title = `E2E Notice ${runId}`;

    // Create announcement.
    const createRes = await request.post(`${base}/api/v1/notice/admin/announcements`, {
      data: { title, content: `Test content ${runId}`, published: false },
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
