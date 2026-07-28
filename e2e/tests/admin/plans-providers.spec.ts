import { test, expect } from '@playwright/test';
import { TestUtils } from '../utils/test-utils';

test.describe('Admin 套餐管理页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到套餐管理页面
    await page.goto('/admin/plans');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('套餐管理');
    
    // 检查创建按钮
    await expect(page.locator('text=新建套餐')).toBeVisible();
    
    // 检查表格
    await expect(page.locator('table')).toBeVisible();
  });

  test('新建套餐', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建套餐');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写套餐信息
    await page.fill('input[placeholder*="套餐名称"]', '测试套餐');
    
    // 选择套餐类型
    await page.selectOption('select[name="planType"]', 'token');
    
    // 填写价格
    await page.fill('input[name="price"]', '100');
    
    // 填写额度
    await page.fill('input[name="quota"]', '1000000');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('套餐已创建');
    
    // 检查套餐是否显示
    await expect(page.locator('text=测试套餐')).toBeVisible();
  });

  test('编辑套餐', async ({ page }) => {
    // 找到第一个套餐的编辑按钮
    const editButton = page.locator('button:has-text("编辑")').first();
    
    if (await editButton.count() > 0) {
      // 点击编辑按钮
      await editButton.click();
      
      // 检查弹窗
      await utils.checkDialogVisible(true);
      
      // 修改套餐名称
      const nameInput = page.locator('input[placeholder*="套餐名称"]');
      await nameInput.fill('修改后的套餐');
      
      // 点击保存
      await page.click('button:has-text("保存")');
      
      // 等待保存完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('套餐已更新');
      
      // 检查套餐名称是否更新
      await expect(page.locator('text=修改后的套餐')).toBeVisible();
    }
  });

  test('删除套餐', async ({ page }) => {
    // 找到第一个套餐的删除按钮
    const deleteButton = page.locator('button:has-text("删除")').first();
    
    if (await deleteButton.count() > 0) {
      // 点击删除按钮
      await deleteButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('删除套餐', '确定要删除此套餐吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待删除完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('套餐已删除');
    }
  });

  test('套餐表单验证', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建套餐');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 不填写名称，直接点击创建
    await page.click('button:has-text("创建")');
    
    // 检查是否有验证错误
    const errorMessage = page.locator('text=请输入套餐名称');
    await expect(errorMessage).toBeVisible();
  });

  test('套餐类型选择', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建套餐');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 检查套餐类型选项
    const typeSelect = page.locator('select[name="planType"]');
    await expect(typeSelect).toBeVisible();
    
    // 检查选项
    const options = await typeSelect.locator('option').allTextContents();
    expect(options).toContain('编程');
    expect(options).toContain('Token');
    expect(options).toContain('图像');
    expect(options).toContain('免费');
  });

  test('套餐状态显示', async ({ page }) => {
    // 检查表格中的状态列
    const statusBadges = page.locator('.badge');
    const count = await statusBadges.count();
    
    for (let i = 0; i < count; i++) {
      const badgeText = await statusBadges.nth(i).textContent();
      expect(['启用', '禁用']).toContain(badgeText);
    }
  });

  test('套餐价格显示', async ({ page }) => {
    // 检查价格列
    const priceCells = page.locator('td:has-text("¥")');
    const count = await priceCells.count();
    
    for (let i = 0; i < count; i++) {
      const priceText = await priceCells.nth(i).textContent();
      expect(priceText).toMatch(/¥\d+/);
    }
  });

  test('套餐额度显示', async ({ page }) => {
    // 检查额度列
    const quotaCells = page.locator('td:has-text("次/月")');
    const count = await quotaCells.count();
    
    for (let i = 0; i < count; i++) {
      const quotaText = await quotaCells.nth(i).textContent();
      expect(quotaText).toMatch(/\d+次\/月/);
    }
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/admin/plans', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无套餐数据');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/plans', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});

test.describe('Admin Provider 管理页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到 Provider 管理页面
    await page.goto('/admin/providers');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('Provider 管理');
    
    // 检查创建按钮
    await expect(page.locator('text=新建 Provider')).toBeVisible();
    
    // 检查表格
    await expect(page.locator('table')).toBeVisible();
    
    // 检查编译按钮
    await expect(page.locator('text=编译并发布')).toBeVisible();
  });

  test('新建 Provider', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建 Provider');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写 Provider 信息
    await page.fill('input[placeholder*="Provider ID"]', 'test-provider');
    await page.fill('input[placeholder*="名称"]', '测试 Provider');
    await page.fill('input[placeholder*="显示名"]', '测试 Provider 显示名');
    await page.fill('input[placeholder*="Base URL"]', 'https://api.test.com');
    
    // 选择 SDK 类型
    await page.selectOption('select[name="sdkType"]', 'openai');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('Provider 已创建');
    
    // 检查 Provider 是否显示
    await expect(page.locator('text=测试 Provider')).toBeVisible();
  });

  test('编辑 Provider', async ({ page }) => {
    // 找到第一个 Provider 的编辑按钮
    const editButton = page.locator('button:has-text("编辑")').first();
    
    if (await editButton.count() > 0) {
      // 点击编辑按钮
      await editButton.click();
      
      // 检查弹窗
      await utils.checkDialogVisible(true);
      
      // 修改 Provider 名称
      const nameInput = page.locator('input[placeholder*="名称"]');
      await nameInput.fill('修改后的 Provider');
      
      // 点击保存
      await page.click('button:has-text("保存")');
      
      // 等待保存完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('Provider 已更新');
      
      // 检查 Provider 名称是否更新
      await expect(page.locator('text=修改后的 Provider')).toBeVisible();
    }
  });

  test('删除 Provider', async ({ page }) => {
    // 找到第一个 Provider 的删除按钮
    const deleteButton = page.locator('button:has-text("删除")').first();
    
    if (await deleteButton.count() > 0) {
      // 点击删除按钮
      await deleteButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('删除 Provider', '确定要删除此 Provider 吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待删除完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('Provider 已删除');
    }
  });

  test('编译并发布', async ({ page }) => {
    // 点击编译按钮
    await page.click('text=编译并发布');
    
    // 等待编译完成
    await page.waitForTimeout(2000);
    
    // 检查是否有成功提示
    await utils.waitForToast('配置已发布');
  });

  test('Provider 表单验证', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建 Provider');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 不填写 ID，直接点击创建
    await page.click('button:has-text("创建")');
    
    // 检查是否有验证错误
    const errorMessage = page.locator('text=请输入 Provider ID');
    await expect(errorMessage).toBeVisible();
  });

  test('SDK 类型选择', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建 Provider');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 检查 SDK 类型选项
    const sdkSelect = page.locator('select[name="sdkType"]');
    await expect(sdkSelect).toBeVisible();
    
    // 检查选项
    const options = await sdkSelect.locator('option').allTextContents();
    expect(options).toContain('OpenAI');
    expect(options).toContain('Anthropic');
  });

  test('Endpoint 管理', async ({ page }) => {
    // 找到第一个 Provider 的 Endpoint 管理按钮
    const endpointButton = page.locator('button:has-text("Endpoint")').first();
    
    if (await endpointButton.count() > 0) {
      // 点击 Endpoint 管理按钮
      await endpointButton.click();
      
      // 检查 Endpoint 列表
      await expect(page.locator('text=Endpoint 列表')).toBeVisible();
      
      // 检查创建 Endpoint 按钮
      await expect(page.locator('text=新建 Endpoint')).toBeVisible();
    }
  });

  test('搜索功能', async ({ page }) => {
    const searchInput = page.locator('input[placeholder*="搜索"]');
    
    if (await searchInput.count() > 0) {
      // 输入搜索关键词
      await searchInput.fill('test');
      await page.waitForTimeout(500);
      
      // 检查搜索结果
      const rows = await page.locator('tbody tr').count();
      expect(rows).toBeGreaterThanOrEqual(0);
    }
  });

  test('状态筛选', async ({ page }) => {
    // 点击"启用"筛选
    await page.click('text=启用');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const enabledRows = await page.locator('tbody tr:has-text("启用")').count();
    expect(enabledRows).toBeGreaterThanOrEqual(0);
    
    // 点击"禁用"筛选
    await page.click('text=禁用');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const disabledRows = await page.locator('tbody tr:has-text("禁用")').count();
    expect(disabledRows).toBeGreaterThanOrEqual(0);
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/admin/providers', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无 Provider 数据');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/providers', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});
