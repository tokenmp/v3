import { test, expect } from '@playwright/test';
import { TestUtils } from '../../utils/test-utils';

test.describe('Admin 系统设置页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到系统设置页面
    await page.goto('/admin/settings');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('系统设置');
    
    // 检查平台信息部分
    await expect(page.locator('text=平台信息')).toBeVisible();
    
    // 检查认证配置部分
    await expect(page.locator('text=认证配置')).toBeVisible();
    
    // 检查服务状态部分
    await expect(page.locator('text=服务状态')).toBeVisible();
  });

  test('平台信息显示', async ({ page }) => {
    // 检查平台名称
    await expect(page.locator('text=TokenMP')).toBeVisible();
    
    // 检查版本信息
    await expect(page.locator('text=版本')).toBeVisible();
    
    // 检查环境信息
    await expect(page.locator('text=环境')).toBeVisible();
  });

  test('认证配置显示', async ({ page }) => {
    // 检查 JWT 配置
    await expect(page.locator('text=JWT')).toBeVisible();
    
    // 检查 Token 过期时间
    await expect(page.locator('text=Token 过期时间')).toBeVisible();
    
    // 检查 Refresh Token 过期时间
    await expect(page.locator('text=Refresh Token 过期时间')).toBeVisible();
  });

  test('服务状态显示', async ({ page }) => {
    // 检查 Edge 服务状态
    await expect(page.locator('text=Edge 服务')).toBeVisible();
    
    // 检查 Auth 服务状态
    await expect(page.locator('text=Auth 服务')).toBeVisible();
    
    // 检查 Notice 服务状态
    await expect(page.locator('text=Notice 服务')).toBeVisible();
    
    // 检查 Logging 服务状态
    await expect(page.locator('text=Logging 服务')).toBeVisible();
    
    // 检查 Billing 服务状态
    await expect(page.locator('text=Billing 服务')).toBeVisible();
    
    // 检查 Config 服务状态
    await expect(page.locator('text=Config 服务')).toBeVisible();
  });

  test('服务状态颜色', async ({ page }) => {
    // 检查状态指示器
    const statusIndicators = page.locator('.rounded-full');
    const count = await statusIndicators.count();
    
    for (let i = 0; i < count; i++) {
      const indicator = statusIndicators.nth(i);
      const classList = await indicator.getAttribute('class');
      
      // 检查是否有绿色（运行中）或红色（不可用）样式
      expect(classList).toMatch(/bg-green|bg-red/);
    }
  });

  test('服务状态自动刷新', async ({ page }) => {
    // 等待 30 秒自动刷新
    await page.waitForTimeout(30000);
    
    // 检查是否有刷新迹象（网络请求）
    const requests = [];
    page.on('request', (request) => {
      if (request.url().includes('/api/v1/admin/health')) {
        requests.push(request);
      }
    });
    
    // 再等待一段时间
    await page.waitForTimeout(5000);
    
    // 检查是否有新的健康检查请求
    expect(requests.length).toBeGreaterThan(0);
  });

  test('功能开关显示', async ({ page }) => {
    // 检查用户注册开关
    await expect(page.locator('text=用户注册')).toBeVisible();
    
    // 检查维护模式开关
    await expect(page.locator('text=维护模式')).toBeVisible();
  });

  test('功能开关交互', async ({ page }) => {
    // 找到用户注册开关
    const userRegistrationSwitch = page.locator('text=用户注册').locator('..').locator('button');
    
    if (await userRegistrationSwitch.count() > 0) {
      // 记录初始状态
      const initialClass = await userRegistrationSwitch.getAttribute('class');
      
      // 点击开关
      await userRegistrationSwitch.click();
      
      // 等待状态变化
      await page.waitForTimeout(500);
      
      // 检查状态是否改变
      const newClass = await userRegistrationSwitch.getAttribute('class');
      expect(newClass).not.toBe(initialClass);
    }
  });

  test('响应式设计', async ({ page }) => {
    // 桌面视图
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);
    
    // 检查布局
    const mainContent = page.locator('main');
    await expect(mainContent).toBeVisible();
    
    // 移动视图
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(300);
    
    // 检查布局自适应
    await expect(mainContent).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/health', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示或降级显示
    const errorIndicators = page.locator('text=不可用');
    const count = await errorIndicators.count();
    expect(count).toBeGreaterThan(0);
  });
});

test.describe('Admin 通知管理页面', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到通知管理页面
    await page.goto('/admin/notifications');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('通知管理');
    
    // 检查发送按钮
    await expect(page.locator('text=发送通知')).toBeVisible();
    
    // 检查通知列表
    await expect(page.locator('table')).toBeVisible();
  });

  test('发送通知', async ({ page }) => {
    // 点击发送按钮
    await page.click('text=发送通知');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写通知信息
    await page.fill('input[placeholder*="标题"]', '测试通知');
    await page.fill('textarea[placeholder*="正文"]', '这是一条测试通知');
    
    // 选择类型
    await page.selectOption('select[name="type"]', 'info');
    
    // 点击发送
    await page.click('button:has-text("发送")');
    
    // 等待发送完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('通知已发送');
    
    // 检查通知是否显示
    await expect(page.locator('text=测试通知')).toBeVisible();
  });

  test('发送给指定用户', async ({ page }) => {
    // 点击发送按钮
    await page.click('text=发送通知');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写通知信息
    await page.fill('input[placeholder*="标题"]', '指定用户通知');
    await page.fill('textarea[placeholder*="正文"]', '这是一条指定用户通知');
    
    // 选择用户
    await page.fill('input[placeholder*="用户"]', 'user@example.com');
    
    // 点击发送
    await page.click('button:has-text("发送")');
    
    // 等待发送完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('通知已发送');
  });

  test('删除通知', async ({ page }) => {
    // 找到第一个通知的删除按钮
    const deleteButton = page.locator('button:has-text("删除")').first();
    
    if (await deleteButton.count() > 0) {
      // 点击删除按钮
      await deleteButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('删除通知', '确定要删除此通知吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待删除完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('通知已删除');
    }
  });

  test('通知表单验证', async ({ page }) => {
    // 点击发送按钮
    await page.click('text=发送通知');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 不填写标题，直接点击发送
    await page.click('button:has-text("发送")');
    
    // 检查是否有验证错误
    const errorMessage = page.locator('text=请输入标题');
    await expect(errorMessage).toBeVisible();
  });

  test('通知类型选择', async ({ page }) => {
    // 点击发送按钮
    await page.click('text=发送通知');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 检查类型选项
    const typeSelect = page.locator('select[name="type"]');
    await expect(typeSelect).toBeVisible();
    
    // 检查选项
    const options = await typeSelect.locator('option').allTextContents();
    expect(options).toContain('信息');
    expect(options).toContain('警告');
    expect(options).toContain('维护');
  });

  test('Action 配置', async ({ page }) => {
    // 点击发送按钮
    await page.click('text=发送通知');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写通知信息
    await page.fill('input[placeholder*="标题"]', '带 Action 的通知');
    await page.fill('textarea[placeholder*="正文"]', '这是一条带 Action 的通知');
    
    // 填写 Action 信息
    await page.fill('input[placeholder*="Action Label"]', '查看详情');
    await page.fill('input[placeholder*="Action URL"]', 'https://example.com');
    
    // 点击发送
    await page.click('button:has-text("发送")');
    
    // 等待发送完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('通知已发送');
  });

  test('已读状态显示', async ({ page }) => {
    // 检查已读状态列
    const readStatusBadges = page.locator('.badge');
    const count = await readStatusBadges.count();
    
    for (let i = 0; i < count; i++) {
      const badgeText = await readStatusBadges.nth(i).textContent();
      expect(['已读', '未读']).toContain(badgeText);
    }
  });

  test('通知列表格式', async ({ page }) => {
    // 检查表格头部
    await expect(page.locator('text=标题')).toBeVisible();
    await expect(page.locator('text=类型')).toBeVisible();
    await expect(page.locator('text=接收者')).toBeVisible();
    await expect(page.locator('text=发送时间')).toBeVisible();
    await expect(page.locator('text=状态')).toBeVisible();
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/admin/notifications', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无通知数据');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/notifications', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});
