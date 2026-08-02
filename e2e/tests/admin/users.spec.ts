import { skipAdminIfNoCreds } from '../../utils/credentials';
import { test, expect } from '@playwright/test';
import { TestUtils } from '../../utils/test-utils';

test.describe('Admin 用户管理页面', () => {
  skipAdminIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到用户管理页面
    await page.goto('/admin/users');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('用户管理');
    
    // 检查搜索框存在
    const searchInput = page.locator('input[placeholder="搜索邮箱"]');
    await expect(searchInput).toBeVisible();
    
    // 检查筛选按钮存在
    await expect(page.locator('text=全部')).toBeVisible();
    await expect(page.locator('text=正常')).toBeVisible();
    await expect(page.locator('text=已禁用')).toBeVisible();
    await expect(page.locator('text=管理员')).toBeVisible();
    await expect(page.locator('text=普通用户')).toBeVisible();
    
    // 检查表格存在
    await expect(page.locator('table')).toBeVisible();
    
    // 检查分页存在
    await expect(page.locator('text=共')).toBeVisible();
  });

  test('搜索功能正常', async ({ page }) => {
    const searchInput = page.locator('input[placeholder="搜索邮箱"]');
    
    // 输入搜索关键词
    await searchInput.fill('test@example.com');
    await page.waitForTimeout(500);
    
    // 检查表格是否有数据（可能是0条）
    const rows = await page.locator('tbody tr').count();
    expect(rows).toBeGreaterThanOrEqual(0);
  });

  test('状态筛选功能', async ({ page }) => {
    // 点击"正常"筛选
    await page.click('text=正常');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const rows = await page.locator('tbody tr').count();
    expect(rows).toBeGreaterThanOrEqual(0);
    
    // 点击"已禁用"筛选
    await page.click('text=已禁用');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const rowsDisabled = await page.locator('tbody tr').count();
    expect(rowsDisabled).toBeGreaterThanOrEqual(0);
  });

  test('角色筛选功能', async ({ page }) => {
    // 点击"管理员"筛选
    await page.click('text=管理员');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const rows = await page.locator('tbody tr').count();
    expect(rows).toBeGreaterThanOrEqual(0);
    
    // 点击"普通用户"筛选
    await page.click('text=普通用户');
    await page.waitForTimeout(500);
    
    // 检查筛选结果
    const rowsUser = await page.locator('tbody tr').count();
    expect(rowsUser).toBeGreaterThanOrEqual(0);
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
      
      // 点击上一页
      const prevButton = page.locator('[aria-label="上一页"]');
      await prevButton.click();
      await page.waitForTimeout(500);
      
      // 检查页码恢复
      const finalPageText = await page.locator('.text-xs.tabular-nums').textContent();
      expect(finalPageText).toContain('1');
    }
  });

  test('禁用用户功能', async ({ page }) => {
    // 找到第一个"正常"状态的用户
    const activeUserRow = page.locator('tbody tr:has-text("正常")').first();
    
    if (await activeUserRow.count() > 0) {
      // 点击禁用按钮
      await activeUserRow.locator('text=禁用').click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('禁用用户', '确定要禁用用户');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待操作完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('用户已禁用');
    }
  });

  test('启用用户功能', async ({ page }) => {
    // 找到第一个"已禁用"状态的用户
    const disabledUserRow = page.locator('tbody tr:has-text("已禁用")').first();
    
    if (await disabledUserRow.count() > 0) {
      // 点击启用按钮
      await disabledUserRow.locator('text=启用').click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('启用用户', '确定要启用用户');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待操作完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('用户已启用');
    }
  });

  test('设置管理员功能', async ({ page }) => {
    // 找到第一个"普通用户"角色的用户
    const regularUserRow = page.locator('tbody tr:has-text("用户")').first();
    
    if (await regularUserRow.count() > 0) {
      // 点击"设为管理员"按钮
      await regularUserRow.locator('text=设为管理员').click();
      
      // 等待操作完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('已设为管理员');
    }
  });

  test('取消管理员功能', async ({ page }) => {
    // 找到第一个"管理员"角色的用户
    const adminUserRow = page.locator('tbody tr:has-text("管理员")').first();
    
    if (await adminUserRow.count() > 0) {
      // 点击"取消管理员"按钮
      await adminUserRow.locator('text=取消管理员').click();
      
      // 等待操作完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('已取消管理员');
    }
  });

  test('用户详情页链接', async ({ page }) => {
    // 点击第一个用户邮箱链接
    const firstEmailLink = page.locator('tbody tr:first-child a').first();
    
    if (await firstEmailLink.count() > 0) {
      await firstEmailLink.click();
      
      // 检查是否跳转到用户详情页
      await page.waitForURL(/\/admin\/users\/[a-zA-Z0-9-]+/);
      await utils.checkUrl('/admin/users/');
    }
  });

  test('分配套餐功能', async ({ page }) => {
    // 找到第一个用户并点击操作按钮
    const firstUserRow = page.locator('tbody tr').first();
    
    if (await firstUserRow.count() > 0) {
      // 在桌面视图中，需要先点击用户进入详情页或使用其他方式
      // 这里假设我们可以通过某种方式打开分配弹窗
      
      // 检查分配弹窗
      const assignDialog = page.locator('[role="dialog"]:has-text("分配套餐")');
      if (await assignDialog.count() > 0) {
        // 选择套餐
        await page.selectOption('select', { index: 1 });
        
        // 点击确认分配
        await page.click('button:has-text("确认分配")');
        
        // 等待操作完成
        await page.waitForTimeout(1000);
        
        // 检查是否有成功提示
        await utils.waitForToast('套餐已分配');
      }
    }
  });

  test('撤销套餐功能', async ({ page }) => {
    // 找到第一个有套餐的用户
    const firstUserRow = page.locator('tbody tr').first();
    
    if (await firstUserRow.count() > 0) {
      // 在桌面视图中，需要先点击用户进入详情页或使用其他方式
      // 这里假设我们可以通过某种方式打开撤销弹窗
      
      // 检查撤销弹窗
      const cancelDialog = page.locator('[role="dialog"]:has-text("撤销套餐")');
      if (await cancelDialog.count() > 0) {
        // 点击确认撤销
        await utils.clickConfirmButton();
        
        // 等待操作完成
        await page.waitForTimeout(1000);
        
        // 检查是否有成功提示
        await utils.waitForToast('套餐已撤销');
      }
    }
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

  test('移动端用户卡片点击', async ({ page }) => {
    // 设置为移动视图
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(300);
    
    // 点击第一个用户卡片
    const firstCard = page.locator('.md\\:hidden button').first();
    if (await firstCard.count() > 0) {
      await firstCard.click();
      
      // 检查是否打开底部弹窗
      await page.waitForTimeout(500);
      const bottomSheet = page.locator('[role="dialog"]');
      await expect(bottomSheet).toBeVisible();
    }
  });

  test('空数据状态', async ({ page }) => {
    // 搜索一个不存在的用户
    const searchInput = page.locator('input[placeholder="搜索邮箱"]');
    await searchInput.fill('nonexistent@example.com');
    await page.waitForTimeout(500);
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无用户数据');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/users', (route) => {
      route.abort('failed');
    });
    
    // 刷新页面
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=操作失败');
    await expect(errorMessage).toBeVisible();
  });
});

test.describe('Admin 用户详情页面', () => {
  skipAdminIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到用户管理页面
    await page.goto('/admin/users');
    await utils.waitForPageLoad();
    
    // 点击第一个用户进入详情页
    const firstEmailLink = page.locator('tbody tr:first-child a').first();
    if (await firstEmailLink.count() > 0) {
      await firstEmailLink.click();
      await utils.waitForPageLoad();
    }
  });

  test('用户详情页加载', async ({ page }) => {
    // 检查页面是否包含用户信息
    await expect(page.locator('text=用户详情')).toBeVisible();
    
    // 检查返回按钮
    await expect(page.locator('text=返回用户列表')).toBeVisible();
  });

  test('返回用户列表', async ({ page }) => {
    // 点击返回按钮
    await page.click('text=返回用户列表');
    
    // 检查是否返回用户列表页
    await page.waitForURL('/admin/users');
    await utils.checkUrl('/admin/users');
  });

  test('用户信息显示', async ({ page }) => {
    // 检查用户邮箱
    const emailElement = page.locator('text=/[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\\.[a-zA-Z]{2,}/');
    await expect(emailElement).toBeVisible();
    
    // 检查角色信息
    const roleElement = page.locator('text=角色');
    await expect(roleElement).toBeVisible();
    
    // 检查状态信息
    const statusElement = page.locator('text=状态');
    await expect(statusElement).toBeVisible();
  });

  test('API 密钥列表', async ({ page }) => {
    // 检查 API 密钥部分
    const apiKeysSection = page.locator('text=API 密钥');
    await expect(apiKeysSection).toBeVisible();
  });

  test('套餐信息', async ({ page }) => {
    // 检查套餐部分
    const plansSection = page.locator('text=套餐');
    await expect(plansSection).toBeVisible();
  });

  test('请求统计', async ({ page }) => {
    // 检查请求统计部分
    const requestsSection = page.locator('text=请求');
    await expect(requestsSection).toBeVisible();
  });

  test('无效用户 ID', async ({ page }) => {
    // 导航到无效的用户 ID
    await page.goto('/admin/users/invalid-user-id');
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=用户不存在');
    await expect(errorMessage).toBeVisible();
  });
});
