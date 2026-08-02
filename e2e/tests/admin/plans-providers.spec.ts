import { test, expect, type Page } from '@playwright/test';
import { skipAdminIfNoCreds } from '../../utils/credentials';
import { TestUtils } from '../../utils/test-utils';

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

  test('创建、编辑和删除套餐', async () => {
    test.skip(true, 'Requires an isolated Billing fixture: package mutation changes shared live configuration.');
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

  test('创建、编辑、删除 Provider 和编译发布', async () => {
    test.skip(true, 'Provider writes and snapshot publication alter shared executor configuration; run only against a dedicated disposable Config fixture.');
  });
});
