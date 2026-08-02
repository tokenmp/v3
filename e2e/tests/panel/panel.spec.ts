import { test, expect } from '@playwright/test';
import { skipUserIfNoCreds } from '../../utils/credentials';
import { TestUtils } from '../../utils/test-utils';

const requestLogs = '**/api/v1/request-logs?*';

test.describe('Panel 用户概览页面', () => {
  skipUserIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    await utils.loginAsUser();
    await page.goto('/panel');
    await utils.waitForPageLoad();
  });

  test('展示当前套餐和最近请求', async ({ page }) => {
    await utils.checkPageTitle('概览');
    await expect(page.getByRole('heading', { name: '当前套餐' })).toBeVisible();
    await expect(page.getByText('最近请求', { exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: '计费设置' })).toBeVisible();
    await expect(page.getByRole('link', { name: '查看请求记录' }).first()).toBeVisible();
  });

  test('展示套餐用量和状态', async ({ page }) => {
    await expect(page.getByText(/个生效套餐/)).toBeVisible();
    await expect(page.getByText(/编程套餐|Token 套餐/).first()).toBeVisible();
    await expect(page.getByText('生效中').first()).toBeVisible();
    await expect(page.locator('div.h-2.overflow-hidden.rounded-full.bg-muted').first()).toBeVisible();
  });

  test('最近请求适配当前视口', async ({ page }) => {
    const desktop = page.locator('.hidden.md\\:block').filter({ has: page.locator('table') });
    const mobile = page.locator('.md\\:hidden').filter({ hasText: /暂无请求记录|成功|失败/ });
    if ((page.viewportSize()?.width ?? 0) < 768) {
      await expect(mobile).toBeVisible();
      await expect(desktop).toBeHidden();
      return;
    }
    for (const name of ['时间', '模型', '协议', '状态', '输入', '输出', '速度', '耗时']) {
      await expect(page.getByRole('columnheader', { name })).toBeVisible();
    }
  });

  test('最近请求在桌面与移动视图间切换', async ({ page }) => {
    const desktop = page.locator('.hidden.md\\:block').filter({ has: page.locator('table') });
    const mobile = page.locator('.md\\:hidden').filter({ hasText: /暂无请求记录|成功|失败/ });
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(desktop).toBeVisible();
    await expect(mobile).toBeHidden();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(desktop).toBeHidden();
    await expect(mobile).toBeVisible();
  });
});

test.describe('Panel API Key 管理页面', () => {
  skipUserIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    await utils.loginAsUser();
    await page.goto('/panel/keys');
    await utils.waitForPageLoad();
  });

  test('展示密钥列表与创建入口', async ({ page }) => {
    await utils.checkPageTitle('API 密钥');
    await expect(page.getByRole('button', { name: '创建密钥' })).toBeVisible();
    if ((page.viewportSize()?.width ?? 0) < 768) {
      await expect(page.locator('.md\\:hidden').getByText(/创建：/).first()).toBeVisible();
    } else {
      await expect(page.getByRole('columnheader', { name: '密钥' })).toBeVisible();
    }
  });

  test('创建密钥表单使用可访问名称', async ({ page }) => {
    await page.getByRole('button', { name: '创建密钥' }).click();
    await utils.checkDialogVisible(true);
    await expect(page.getByLabel('名称')).toBeVisible();
    await expect(page.getByRole('button', { name: '创建', exact: true })).toBeDisabled();
  });

  test('密钥空态', async ({ page }) => {
    await page.route('**/api/v1/keys', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ keys: [] }) }),
    );
    await page.reload();
    const emptyState = (page.viewportSize()?.width ?? 0) < 768
      ? page.locator('.md\\:hidden').getByText('暂无密钥', { exact: true })
      : page.locator('.hidden.md\\:block').getByText('暂无密钥', { exact: true });
    await expect(emptyState).toBeVisible();
  });
});

test.describe('Panel 请求日志页面', () => {
  skipUserIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    await utils.loginAsUser();
    await page.goto('/panel/requests');
    await utils.waitForPageLoad();
  });

  test('展示搜索、筛选和日志表格', async ({ page }) => {
    await utils.checkPageTitle('请求日志');
    await expect(page.getByPlaceholder('搜索模型 / Request ID')).toBeVisible();
    for (const name of ['全部', '成功', '失败', '已取消', '处理中']) {
      await expect(page.getByRole('button', { name, exact: true })).toBeVisible();
    }
    if ((page.viewportSize()?.width ?? 0) < 768) {
      await expect(page.locator('.md\\:hidden').first()).toBeVisible();
    } else {
      await expect(page.getByRole('columnheader', { name: '请求ID' })).toBeVisible();
      await expect(page.getByRole('columnheader', { name: 'Thinking' })).toBeVisible();
    }
  });

  test('请求日志空态', async ({ page }) => {
    await page.route(requestLogs, (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ logs: [], total: 0 }) }),
    );
    await page.reload();
    const emptyState = (page.viewportSize()?.width ?? 0) < 768
      ? page.locator('.md\\:hidden').getByText('暂无请求记录', { exact: true })
      : page.locator('.hidden.md\\:block').getByText('暂无请求记录', { exact: true });
    await expect(emptyState).toBeVisible();
    await expect(page.getByText('共 0 条')).toBeVisible();
  });

  test('请求日志响应式列表', async ({ page }) => {
    const desktop = page.locator('.hidden.md\\:block').filter({ has: page.locator('table') });
    const mobile = page.locator('.md\\:hidden').filter({ hasText: /暂无请求记录|成功|失败/ });
    await page.setViewportSize({ width: 1440, height: 900 });
    await expect(desktop).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(desktop).toBeHidden();
    await expect(mobile).toBeVisible();
  });
});
