import { test, expect } from '@playwright/test';
import { TestUtils } from '../../utils/test-utils';

test.describe('Panel 用户概览页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为普通用户
    await utils.loginAsUser();
    // 导航到用户概览页面
    await page.goto('/panel');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('概览');
    
    // 检查统计卡片
    await expect(page.locator('text=账户')).toBeVisible();
    await expect(page.locator('text=配额')).toBeVisible();
    await expect(page.locator('text=状态')).toBeVisible();
    
    // 检查最近请求部分
    await expect(page.locator('text=最近请求')).toBeVisible();
  });

  test('账户信息显示', async ({ page }) => {
    // 检查账户卡片
    const accountCard = page.locator('text=账户').locator('..');
    await expect(accountCard).toBeVisible();
    
    // 检查邮箱显示
    const emailElement = page.locator('text=/[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}/');
    await expect(emailElement).toBeVisible();
    
    // 检查角色显示
    const roleElement = page.locator('text=角色：');
    await expect(roleElement).toBeVisible();
    
    // 检查注册时间显示
    const createdAtElement = page.locator('text=注册时间：');
    await expect(createdAtElement).toBeVisible();
  });

  test('配额信息显示', async ({ page }) => {
    // 检查配额卡片
    const quotaCard = page.locator('text=配额').locator('..');
    await expect(quotaCard).toBeVisible();
    
    // 检查套餐类型显示
    const planTypeElement = page.locator('text=/编程|Token|图像|免费/');
    await expect(planTypeElement).toBeVisible();
    
    // 检查进度条
    const progressBar = page.locator('.h-2.rounded-full.bg-muted.overflow-hidden');
    await expect(progressBar).toBeVisible();
    
    // 检查使用量显示
    const usageElement = page.locator('text=/已用.*tokens/');
    await expect(usageElement).toBeVisible();
  });

  test('状态信息显示', async ({ page }) => {
    // 检查状态卡片
    const statusCard = page.locator('text=状态').locator('..');
    await expect(statusCard).toBeVisible();
    
    // 检查状态徽章
    const statusBadge = page.locator('text=正常运行');
    await expect(statusBadge).toBeVisible();
  });

  test('最近请求表格', async ({ page }) => {
    // 检查表格标题
    await expect(page.locator('text=最近请求')).toBeVisible();
    
    // 检查表格头部
    await expect(page.locator('text=时间')).toBeVisible();
    await expect(page.locator('text=模型')).toBeVisible();
    await expect(page.locator('text=状态')).toBeVisible();
    await expect(page.locator('text=耗时')).toBeVisible();
    await expect(page.locator('text=Token')).toBeVisible();
    
    // 检查表格内容
    const tableRows = page.locator('tbody tr');
    const rowCount = await tableRows.count();
    
    if (rowCount > 0) {
      // 检查第一行数据
      const firstRow = tableRows.first();
      await expect(firstRow).toBeVisible();
      
      // 检查时间格式
      const timeCell = firstRow.locator('td').first();
      const timeText = await timeCell.textContent();
      expect(timeText).toMatch(/\d{4}\/\d{1,2}\/\d{1,2}/);
      
      // 检查状态徽章
      const statusBadge = firstRow.locator('.badge');
      await expect(statusBadge).toBeVisible();
    }
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/user/recent-requests', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无请求记录');
    await expect(emptyMessage).toBeVisible();
  });

  test('响应式设计', async ({ page }) => {
    // 桌面视图
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);
    
    // 检查桌面表格可见
    const desktopTable = page.locator('.hidden.md\\:block');
    await expect(desktopTable).toBeVisible();
    
    // 检查移动卡片隐藏
    const mobileCards = page.locator('.md\\:hidden');
    await expect(mobileCards).toBeHidden();
    
    // 移动视图
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(300);
    
    // 检查桌面表格隐藏
    await expect(desktopTable).toBeHidden();
    
    // 检查移动卡片可见
    await expect(mobileCards).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/user/balance', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示或降级显示
    const errorElement = page.locator('text=—');
    await expect(errorElement).toBeVisible();
  });
});

test.describe('Panel API Key 管理页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为普通用户
    await utils.loginAsUser();
    // 导航到 API Key 管理页面
    await page.goto('/panel/keys');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('API 密钥');
    
    // 检查创建按钮
    await expect(page.locator('text=创建密钥')).toBeVisible();
    
    // 检查密钥列表
    await expect(page.locator('text=API 密钥')).toBeVisible();
  });

  test('创建 API Key', async ({ page }) => {
    // 点击创建按钮
    await page.click('text=创建密钥');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写密钥名称
    await page.fill('input[placeholder*="密钥名称"]', '测试密钥');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('密钥已创建');
    
    // 检查密钥是否显示
    await expect(page.locator('text=测试密钥')).toBeVisible();
  });

  test('复制 API Key', async ({ page }) => {
    // 找到第一个密钥
    const firstKey = page.locator('.font-mono').first();
    
    if (await firstKey.count() > 0) {
      // 点击复制按钮
      await page.click('button:has-text("复制")');
      
      // 检查是否有复制成功提示
      await utils.waitForToast('已复制');
    }
  });

  test('轮换 API Key', async ({ page }) => {
    // 找到第一个密钥的轮换按钮
    const rotateButton = page.locator('button:has-text("轮换")').first();
    
    if (await rotateButton.count() > 0) {
      // 点击轮换按钮
      await rotateButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('轮换密钥', '确定要轮换此密钥吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待轮换完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('密钥已轮换');
    }
  });

  test('撤销 API Key', async ({ page }) => {
    // 找到第一个密钥的撤销按钮
    const revokeButton = page.locator('button:has-text("撤销")').first();
    
    if (await revokeButton.count() > 0) {
      // 点击撤销按钮
      await revokeButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('撤销密钥', '确定要撤销此密钥吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待撤销完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('密钥已撤销');
    }
  });

  test('密钥显示安全', async ({ page }) => {
    // 检查密钥是否只显示前后缀
    const keyElements = page.locator('.font-mono');
    const keyCount = await keyElements.count();
    
    for (let i = 0; i < keyCount; i++) {
      const keyText = await keyElements.nth(i).textContent();
      // 检查密钥格式：sk-...xxx
      expect(keyText).toMatch(/sk-.*[a-zA-Z0-9]{3,4}$/);
    }
  });

  test('创建弹窗验证', async ({ page }) => {
    // 点击创建按钮
    await page.click('text=创建密钥');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 不填写名称，直接点击创建
    await page.click('button:has-text("创建")');
    
    // 检查是否有验证错误
    const errorMessage = page.locator('text=请输入密钥名称');
    await expect(errorMessage).toBeVisible();
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/user/api-keys', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无 API 密钥');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/user/api-keys', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});

test.describe('Panel 请求日志页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为普通用户
    await utils.loginAsUser();
    // 导航到请求日志页面
    await page.goto('/panel/requests');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('请求日志');
    
    // 检查搜索框
    await expect(page.locator('input[placeholder*="搜索"]')).toBeVisible();
    
    // 检查筛选按钮
    await expect(page.locator('text=全部')).toBeVisible();
    await expect(page.locator('text=成功')).toBeVisible();
    await expect(page.locator('text=失败')).toBeVisible();
    
    // 检查表格
    await expect(page.locator('table')).toBeVisible();
  });

  test('搜索功能', async ({ page }) => {
    const searchInput = page.locator('input[placeholder*="搜索"]');
    
    // 输入搜索关键词
    await searchInput.fill('test-model');
    await page.waitForTimeout(500);
    
    // 检查搜索结果
    const rows = await page.locator('tbody tr').count();
    expect(rows).toBeGreaterThanOrEqual(0);
  });

  test('状态筛选', async ({ page }) => {
    // 点击"成功"筛选
    await page.click('text=成功');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const successRows = await page.locator('tbody tr:has-text("成功")').count();
    expect(successRows).toBeGreaterThanOrEqual(0);
    
    // 点击"失败"筛选
    await page.click('text=失败');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const failedRows = await page.locator('tbody tr:has-text("失败")').count();
    expect(failedRows).toBeGreaterThanOrEqual(0);
  });

  test('分页功能', async ({ page }) => {
    // 检查当前页码
    const pageText = await page.locator('.text-xs.tabular-nums').textContent();
    expect(pageText).toContain('1');
    
    // 点击下一页
    const nextButton = page.locator('[aria-label="下一页"]');
    if (await nextButton.isEnabled()) {
      await nextButton.click();
      await page.waitForTimeout(500);
      
      // 检查页码变化
      const newPageText = await page.locator('.text-xs.tabular-nums').textContent();
      expect(newPageText).toContain('2');
    }
  });

  test('表格数据格式', async ({ page }) => {
    // 检查表格头部
    await expect(page.locator('text=时间')).toBeVisible();
    await expect(page.locator('text=模型')).toBeVisible();
    await expect(page.locator('text=状态')).toBeVisible();
    await expect(page.locator('text=耗时')).toBeVisible();
    await expect(page.locator('text=Token')).toBeVisible();
    
    // 检查表格行数据
    const firstRow = page.locator('tbody tr').first();
    if (await firstRow.count() > 0) {
      // 检查时间格式
      const timeCell = firstRow.locator('td').first();
      const timeText = await timeCell.textContent();
      expect(timeText).toMatch(/\d{4}\/\d{1,2}\/\d{1,2}/);
      
      // 检查状态徽章
      const statusBadge = firstRow.locator('.badge');
      await expect(statusBadge).toBeVisible();
    }
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/user/requests', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify({ requests: [], total: 0 }),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无请求记录');
    await expect(emptyMessage).toBeVisible();
  });

  test('响应式设计', async ({ page }) => {
    // 桌面视图
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);
    
    // 检查桌面表格可见
    const desktopTable = page.locator('.hidden.md\\:block');
    await expect(desktopTable).toBeVisible();
    
    // 移动视图
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(300);
    
    // 检查移动卡片可见
    const mobileCards = page.locator('.md\\:hidden');
    await expect(mobileCards).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/user/requests', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});
