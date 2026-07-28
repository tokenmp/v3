import { test, expect } from '@playwright/test';
import { TestUtils } from '../utils/test-utils';

test.describe('TokenMP v3 完整 E2E 测试', () => {
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
  });

  test('用户注册和登录流程', async ({ page }) => {
    // 访问首页
    await page.goto('/');
    await utils.waitForPageLoad();
    
    // 检查是否跳转到登录页
    await page.waitForURL('/login');
    await utils.checkUrl('/login');
    
    // 检查登录表单
    await expect(page.locator('input[type="email"]')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
    
    // 测试注册链接
    await page.click('text=注册');
    await page.waitForURL('/register');
    await utils.checkUrl('/register');
    
    // 检查注册表单
    await expect(page.locator('input[type="email"]')).toBeVisible();
    await expect(page.locator('input[type="password"]')).toBeVisible();
    await expect(page.locator('input[placeholder*="确认密码"]')).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
    
    // 返回登录页
    await page.click('text=登录');
    await page.waitForURL('/login');
  });

  test('管理员登录和后台访问', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 检查是否跳转到 admin 后台
    await page.waitForURL('/admin');
    await utils.checkUrl('/admin');
    
    // 检查 admin 侧边栏
    await expect(page.locator('text=控制台')).toBeVisible();
    await expect(page.locator('text=用户管理')).toBeVisible();
    await expect(page.locator('text=套餐管理')).toBeVisible();
    await expect(page.locator('text=Provider 管理')).toBeVisible();
    await expect(page.locator('text=模型配置')).toBeVisible();
    await expect(page.locator('text=路由管理')).toBeVisible();
    await expect(page.locator('text=公告管理')).toBeVisible();
    await expect(page.locator('text=版本日志')).toBeVisible();
    await expect(page.locator('text=通知管理')).toBeVisible();
    await expect(page.locator('text=系统设置')).toBeVisible();
    
    // 测试导航到各个页面
    await page.click('text=用户管理');
    await page.waitForURL('/admin/users');
    await utils.checkUrl('/admin/users');
    
    await page.click('text=套餐管理');
    await page.waitForURL('/admin/plans');
    await utils.checkUrl('/admin/plans');
    
    await page.click('text=Provider 管理');
    await page.waitForURL('/admin/providers');
    await utils.checkUrl('/admin/providers');
    
    // 返回控制台
    await page.click('text=控制台');
    await page.waitForURL('/admin');
    await utils.checkUrl('/admin');
  });

  test('普通用户登录和面板访问', async ({ page }) => {
    // 登录为普通用户
    await utils.loginAsUser();
    
    // 检查是否跳转到 panel 面板
    await page.waitForURL('/panel');
    await utils.checkUrl('/panel');
    
    // 检查 panel 侧边栏
    await expect(page.locator('text=概览')).toBeVisible();
    await expect(page.locator('text=API 密钥')).toBeVisible();
    await expect(page.locator('text=请求日志')).toBeVisible();
    await expect(page.locator('text=可用模型')).toBeVisible();
    await expect(page.locator('text=Auto 模型')).toBeVisible();
    await expect(page.locator('text=公告')).toBeVisible();
    await expect(page.locator('text=版本日志')).toBeVisible();
    await expect(page.locator('text=通知')).toBeVisible();
    await expect(page.locator('text=设置')).toBeVisible();
    
    // 测试导航到各个页面
    await page.click('text=API 密钥');
    await page.waitForURL('/panel/keys');
    await utils.checkUrl('/panel/keys');
    
    await page.click('text=请求日志');
    await page.waitForURL('/panel/requests');
    await utils.checkUrl('/panel/requests');
    
    await page.click('text=可用模型');
    await page.waitForURL('/panel/models');
    await utils.checkUrl('/panel/models');
    
    // 返回概览
    await page.click('text=概览');
    await page.waitForURL('/panel');
    await utils.checkUrl('/panel');
  });

  test('权限控制测试', async ({ page }) => {
    // 未登录访问 admin 后台
    await page.goto('/admin');
    await page.waitForURL('/login');
    await utils.checkUrl('/login');
    
    // 未登录访问 panel 面板
    await page.goto('/panel');
    await page.waitForURL('/login');
    await utils.checkUrl('/login');
    
    // 普通用户访问 admin 后台
    await utils.loginAsUser();
    await page.goto('/admin');
    await page.waitForURL('/panel');
    await utils.checkUrl('/panel');
    
    // 管理员访问 panel 面板
    await utils.loginAsAdmin();
    await page.goto('/panel');
    await page.waitForURL('/panel');
    await utils.checkUrl('/panel');
  });

  test('响应式设计测试', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 桌面视图
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(300);
    
    // 检查桌面布局
    const desktopSidebar = page.locator('.hidden.md\\:block');
    await expect(desktopSidebar).toBeVisible();
    
    // 移动视图
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(300);
    
    // 检查移动布局
    const mobileBottomNav = page.locator('.md\\:hidden');
    await expect(mobileBottomNav).toBeVisible();
  });

  test('API Key 完整生命周期', async ({ page }) => {
    // 登录为普通用户
    await utils.loginAsUser();
    
    // 导航到 API Key 管理页面
    await page.goto('/panel/keys');
    await utils.waitForPageLoad();
    
    // 创建 API Key
    await page.click('text=创建密钥');
    await utils.checkDialogVisible(true);
    
    await page.fill('input[placeholder*="密钥名称"]', 'E2E 测试密钥');
    await page.click('button:has-text("创建")');
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('密钥已创建');
    
    // 检查密钥是否显示
    await expect(page.locator('text=E2E 测试密钥')).toBeVisible();
    
    // 复制 API Key
    await page.click('button:has-text("复制")');
    await utils.waitForToast('已复制');
    
    // 轮换 API Key
    await page.click('button:has-text("轮换")');
    await utils.checkConfirmDialog('轮换密钥', '确定要轮换此密钥吗？');
    await utils.clickConfirmButton();
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('密钥已轮换');
    
    // 撤销 API Key
    await page.click('button:has-text("撤销")');
    await utils.checkConfirmDialog('撤销密钥', '确定要撤销此密钥吗？');
    await utils.clickConfirmButton();
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('密钥已撤销');
  });

  test('套餐管理完整流程', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 导航到套餐管理页面
    await page.goto('/admin/plans');
    await utils.waitForPageLoad();
    
    // 创建套餐
    await page.click('text=新建套餐');
    await utils.checkDialogVisible(true);
    
    await page.fill('input[placeholder*="套餐名称"]', 'E2E 测试套餐');
    await page.selectOption('select[name="planType"]', 'token');
    await page.fill('input[name="price"]', '99');
    await page.fill('input[name="quota"]', '1000000');
    
    await page.click('button:has-text("创建")');
    await page.waitForTimeout(1000);
    await utils.waitForToast('套餐已创建');
    
    // 检查套餐是否显示
    await expect(page.locator('text=E2E 测试套餐')).toBeVisible();
    
    // 编辑套餐
    await page.click('button:has-text("编辑")');
    await utils.checkDialogVisible(true);
    
    await page.fill('input[placeholder*="套餐名称"]', 'E2E 测试套餐（已修改）');
    await page.click('button:has-text("保存")');
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('套餐已更新');
    
    // 检查套餐名称是否更新
    await expect(page.locator('text=E2E 测试套餐（已修改）')).toBeVisible();
    
    // 删除套餐
    await page.click('button:has-text("删除")');
    await utils.checkConfirmDialog('删除套餐', '确定要删除此套餐吗？');
    await utils.clickConfirmButton();
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('套餐已删除');
  });

  test('公告管理完整流程', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 导航到公告管理页面
    await page.goto('/admin/announcements');
    await utils.waitForPageLoad();
    
    // 创建公告
    await page.click('text=新建公告');
    await utils.checkDialogVisible(true);
    
    await page.fill('input[placeholder*="标题"]', 'E2E 测试公告');
    await page.fill('textarea[placeholder*="摘要"]', '这是一条 E2E 测试公告');
    await page.fill('textarea[placeholder*="内容"]', '# E2E 测试公告\n\n这是公告的详细内容。');
    await page.selectOption('select[name="level"]', 'info');
    await page.check('input[name="publishNow"]');
    
    await page.click('button:has-text("创建")');
    await page.waitForTimeout(1000);
    await utils.waitForToast('公告已创建');
    
    // 检查公告是否显示
    await expect(page.locator('text=E2E 测试公告')).toBeVisible();
    
    // 编辑公告
    await page.click('button:has-text("编辑")');
    await utils.checkDialogVisible(true);
    
    await page.fill('input[placeholder*="标题"]', 'E2E 测试公告（已修改）');
    await page.click('button:has-text("保存")');
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('公告已更新');
    
    // 检查公告标题是否更新
    await expect(page.locator('text=E2E 测试公告（已修改）')).toBeVisible();
    
    // 删除公告
    await page.click('button:has-text("删除")');
    await utils.checkConfirmDialog('删除公告', '确定要删除此公告吗？');
    await utils.clickConfirmButton();
    
    await page.waitForTimeout(1000);
    await utils.waitForToast('公告已删除');
  });

  test('用户管理完整流程', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 导航到用户管理页面
    await page.goto('/admin/users');
    await utils.waitForPageLoad();
    
    // 搜索用户
    const searchInput = page.locator('input[placeholder="搜索邮箱"]');
    await searchInput.fill('test@example.com');
    await page.waitForTimeout(500);
    
    // 筛选用户
    await page.click('text=正常');
    await page.waitForTimeout(500);
    
    await page.click('text=管理员');
    await page.waitForTimeout(500);
    
    // 测试分页
    const nextButton = page.locator('[aria-label="下一页"]');
    if (await nextButton.isEnabled()) {
      await nextButton.click();
      await page.waitForTimeout(500);
      
      const pageText = await page.locator('.text-xs.tabular-nums').textContent();
      expect(pageText).toContain('2');
      
      const prevButton = page.locator('[aria-label="上一页"]');
      await prevButton.click();
      await page.waitForTimeout(500);
    }
    
    // 测试禁用用户
    const activeUserRow = page.locator('tbody tr:has-text("正常")').first();
    if (await activeUserRow.count() > 0) {
      await activeUserRow.locator('text=禁用').click();
      await utils.checkConfirmDialog('禁用用户', '确定要禁用用户');
      await utils.clickConfirmButton();
      
      await page.waitForTimeout(1000);
      await utils.waitForToast('用户已禁用');
    }
  });

  test('系统设置页面功能', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 导航到系统设置页面
    await page.goto('/admin/settings');
    await utils.waitForPageLoad();
    
    // 检查平台信息
    await expect(page.locator('text=TokenMP')).toBeVisible();
    
    // 检查服务状态
    await expect(page.locator('text=Edge 服务')).toBeVisible();
    await expect(page.locator('text=Auth 服务')).toBeVisible();
    
    // 检查功能开关
    await expect(page.locator('text=用户注册')).toBeVisible();
    await expect(page.locator('text=维护模式')).toBeVisible();
    
    // 测试功能开关交互
    const userRegistrationSwitch = page.locator('text=用户注册').locator('..').locator('button');
    if (await userRegistrationSwitch.count() > 0) {
      await userRegistrationSwitch.click();
      await page.waitForTimeout(500);
    }
  });

  test('错误处理和恢复', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 模拟网络错误
    await page.route('**/api/v1/admin/users', (route) => {
      route.abort('failed');
    });
    
    // 导航到用户管理页面
    await page.goto('/admin/users');
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=操作失败');
    await expect(errorMessage).toBeVisible();
    
    // 移除网络错误模拟
    await page.unroute('**/api/v1/admin/users');
    
    // 刷新页面恢复
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查页面是否恢复正常
    await expect(page.locator('table')).toBeVisible();
  });

  test('可访问性测试', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 导航到用户管理页面
    await page.goto('/admin/users');
    await utils.waitForPageLoad();
    
    // 测试键盘导航
    await page.keyboard.press('Tab');
    await page.waitForTimeout(300);
    
    // 检查焦点是否在搜索框上
    const searchInput = page.locator('input[placeholder="搜索邮箱"]');
    await expect(searchInput).toBeFocused();
    
    // 测试按钮可访问性
    const buttons = page.locator('button');
    const count = await buttons.count();
    
    for (let i = 0; i < count; i++) {
      const button = buttons.nth(i);
      const ariaLabel = await button.getAttribute('aria-label');
      const textContent = await button.textContent();
      
      // 检查按钮是否有可访问的名称
      expect(ariaLabel || textContent).toBeTruthy();
    }
  });

  test('性能测试', async ({ page }) => {
    // 登录为管理员
    await utils.loginAsAdmin();
    
    // 测试页面加载时间
    const startTime = Date.now();
    await page.goto('/admin');
    await utils.waitForPageLoad();
    const endTime = Date.now();
    
    const loadTime = endTime - startTime;
    console.log(`Admin 控制台加载时间: ${loadTime}ms`);
    
    // 检查加载时间是否在合理范围内（小于 5 秒）
    expect(loadTime).toBeLessThan(5000);
    
    // 测试页面切换时间
    const startSwitchTime = Date.now();
    await page.click('text=用户管理');
    await page.waitForURL('/admin/users');
    await utils.waitForPageLoad();
    const endSwitchTime = Date.now();
    
    const switchTime = endSwitchTime - startSwitchTime;
    console.log(`页面切换时间: ${switchTime}ms`);
    
    // 检查切换时间是否在合理范围内（小于 3 秒）
    expect(switchTime).toBeLessThan(3000);
  });
});
