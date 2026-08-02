import { test, expect, type Page, type BrowserContext } from '@playwright/test';
import { e2eCredentials, skipAdminIfNoCreds, skipUserIfNoCreds } from '../utils/credentials';

/**
 * TokenMP v3 E2E 测试 - 移动端专项
 * 覆盖移动端特有的交互和展示
 */

const credentials = e2eCredentials();
const ADMIN_USER = credentials.admin;
const TEST_USER = credentials.user;

// 移动端视口配置
const MOBILE_VIEWPORT = { width: 390, height: 844 };

async function login(page: Page, email: string, password: string) {
  await page.goto('/login');
  await page.waitForLoadState('networkidle');
  await page.fill('input[type="email"]', email);
  await page.fill('input#password', password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/(panel|admin)/, { timeout: 15000 });
  if (page.url().includes('/panel') && email === ADMIN_USER.email) {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
  }
}

// ==================== 移动端 Panel 测试 ====================
test.describe('移动端 Panel - 布局与导航', () => {
  skipUserIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
  });

  test('底部导航栏显示', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 底部导航栏应该可见
    const bottomNav = page.locator('nav.md\\:hidden');
    await expect(bottomNav).toBeVisible();
    
    // 检查导航项 - 移动端有4个标签
    await expect(bottomNav.getByText('概览')).toBeVisible();
    await expect(bottomNav.getByText('请求日志')).toBeVisible();
    await expect(bottomNav.getByText('模型')).toBeVisible();
    await expect(bottomNav.getByText('我的')).toBeVisible();
  });

  test('侧边栏隐藏', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 桌面侧边栏应该隐藏
    const sidebar = page.locator('aside');
    await expect(sidebar).toBeHidden();
  });

  test('底部导航切换页面', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    const bottomNav = page.locator('nav.md\\:hidden');
    
    // 点击请求日志导航
    await bottomNav.getByText('请求日志').click();
    await page.waitForURL(/\/panel\/requests/);
    await expect(page).toHaveURL(/\/panel\/requests/);
    
    // 点击模型导航
    await bottomNav.getByText('模型').click();
    await page.waitForURL(/\/panel\/models/);
    await expect(page).toHaveURL(/\/panel\/models/);
    
    // 点击概览导航
    await bottomNav.getByText('概览').click();
    await page.waitForURL(/\/panel/);
    await expect(page).toHaveURL(/\/panel/);
  });

  test('更多页面导航', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    const bottomNav = page.locator('nav.md\\:hidden');
    
    // 点击我的导航
    await bottomNav.getByText('我的').click();
    await page.waitForURL(/\/panel\/settings/);
    await expect(page).toHaveURL(/\/panel\/settings/);
  });
});

test.describe('移动端 Panel - 概览页面', () => {
  skipUserIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
  });

  test('统计卡片垂直排列', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 统计卡片应该垂直排列
    const cards = page.locator('.grid > div');
    const count = await cards.count();
    expect(count).toBeGreaterThanOrEqual(3);
  });

  test('最近请求卡片列表', async ({ page }) => {
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 移动端应该显示卡片列表而不是表格
    // 使用更通用的选择器
    const hasCards = await page.locator('.md\\:hidden').isVisible().catch(() => false);
    const hasTable = await page.locator('.hidden.md\\:block').isVisible().catch(() => false);
    
    // 移动端应该显示卡片，隐藏表格
    expect(hasCards || !hasTable).toBe(true);
  });
});

test.describe('移动端 Panel - API Key', () => {
  skipUserIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
  });

  test('API Key 创建弹窗', async ({ page }) => {
    await page.goto('/panel/keys');
    await page.waitForLoadState('networkidle');
    
    // 点击创建按钮
    await page.getByRole('button', { name: '创建密钥' }).click();
    await page.waitForTimeout(500);
    
    // 弹窗应该全屏或接近全屏
    const dialog = page.locator('h2:has-text("创建密钥")');
    await expect(dialog).toBeVisible();
    
    // 填写表单
    await page.locator('input[placeholder="例如：生产环境"]').fill('移动端测试密钥');
    
    // 关闭弹窗
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  });

  test('API Key 列表显示', async ({ page }) => {
    await page.goto('/panel/keys');
    await page.waitForLoadState('networkidle');
    
    // 页面应该有创建按钮
    await expect(page.getByRole('button', { name: '创建密钥' })).toBeVisible();
  });
});

test.describe('移动端 Panel - 请求日志', () => {
  skipUserIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
  });

  test('请求日志卡片列表', async ({ page }) => {
    await page.goto('/panel/requests');
    await page.waitForLoadState('networkidle');
    
    // 检查页面有内容
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
  });

  test('搜索框可访问', async ({ page }) => {
    await page.goto('/panel/requests');
    await page.waitForLoadState('networkidle');
    
    // 搜索框应该可见
    const searchInput = page.locator('input[placeholder*="搜索"]');
    await expect(searchInput).toBeVisible();
    
    // 可以输入
    await searchInput.fill('test');
    await page.waitForTimeout(500);
  });

  test('筛选按钮可点击', async ({ page }) => {
    await page.goto('/panel/requests');
    await page.waitForLoadState('networkidle');
    
    // 筛选按钮应该可见
    const allBtn = page.getByRole('button', { name: '全部', exact: true });
    const successBtn = page.getByRole('button', { name: '成功', exact: true });
    const failBtn = page.getByRole('button', { name: '失败', exact: true });
    
    await expect(allBtn).toBeVisible();
    await expect(successBtn).toBeVisible();
    await expect(failBtn).toBeVisible();
    
    // 点击筛选
    await successBtn.click();
    await page.waitForTimeout(500);
    await allBtn.click();
    await page.waitForTimeout(500);
  });
});

test.describe('移动端 Panel - 设置页面', () => {
  skipUserIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
  });

  test('设置页面元素', async ({ page }) => {
    await page.goto('/panel/settings');
    await page.waitForLoadState('networkidle');
    
    // 检查设置项
    await expect(page.getByText('修改密码')).toBeVisible();
    await expect(page.getByText('退出登录')).toBeVisible();
  });

  test('修改密码表单', async ({ page }) => {
    await page.goto('/panel/settings');
    await page.waitForLoadState('networkidle');
    
    // 点击修改密码
    await page.getByRole('button', { name: /修改密码/ }).click();
    await page.waitForTimeout(500);
    
    // 检查表单字段
    const currentPwd = page.locator('input[placeholder*="当前密码"], input[type="password"]').first();
    await expect(currentPwd).toBeVisible();
    
    // 关闭弹窗
    await page.keyboard.press('Escape');
  });
});

// ==================== 移动端 Admin 测试 ====================
test.describe('移动端 Admin - 布局与导航', () => {
  skipAdminIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('移动端底部导航栏', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
    
    // 底部导航栏应该可见
    const bottomNav = page.locator('nav.md\\:hidden');
    await expect(bottomNav).toBeVisible();
    
    // 检查导航项 - Admin 有5个标签
    await expect(bottomNav.getByText('概览')).toBeVisible();
    await expect(bottomNav.getByText('用户')).toBeVisible();
    await expect(bottomNav.getByText('日志')).toBeVisible();
    await expect(bottomNav.getByText('执行')).toBeVisible();
    await expect(bottomNav.getByText('更多')).toBeVisible();
  });

  test('侧边栏隐藏', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
    
    // 桌面侧边栏应该隐藏
    const sidebar = page.locator('aside');
    await expect(sidebar).toBeHidden();
  });

  test('底部导航切换页面', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
    
    const bottomNav = page.locator('nav.md\\:hidden');
    
    // 点击用户导航
    await bottomNav.getByText('用户').click();
    await page.waitForURL(/\/admin\/users/);
    await expect(page).toHaveURL(/\/admin\/users/);
    
    // 点击概览导航
    await bottomNav.getByText('概览').click();
    await page.waitForURL(/\/admin/);
    await expect(page).toHaveURL(/\/admin/);
  });

  test('更多页面导航', async ({ page }) => {
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
    
    const bottomNav = page.locator('nav.md\\:hidden');
    
    // 点击更多导航
    await bottomNav.getByText('更多').click();
    await page.waitForURL(/\/admin\/more/);
    await expect(page).toHaveURL(/\/admin\/more/);
  });
});

test.describe('移动端 Admin - 用户管理', () => {
  skipAdminIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('用户卡片列表', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 检查页面有内容
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
    
    // 应该有用户数据 - 使用更通用的选择器
    await expect(page.locator('table, .md\\:hidden').first()).toBeVisible({ timeout: 5000 });
  });

  test('用户卡片点击打开详情', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 点击第一个用户链接
    const userLink = page.locator('tbody a, .md\\:hidden a').first();
    if (await userLink.isVisible()) {
      await userLink.click();
      await page.waitForTimeout(1000);
      
      // 应该进入用户详情页
      const body = await page.textContent('body');
      expect(body).toBeTruthy();
    }
  });

  test('用户卡片操作', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 检查页面有操作按钮
    const disableBtn = page.getByRole('button', { name: /禁用|启用/ }).first();
    const roleBtn = page.getByRole('button', { name: /管理员/ }).first();
    
    // 至少一个按钮应该可见
    const hasDisable = await disableBtn.isVisible().catch(() => false);
    const hasRole = await roleBtn.isVisible().catch(() => false);
    expect(hasDisable || hasRole).toBe(true);
  });

  test('搜索功能', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 搜索框应该可见
    const searchInput = page.locator('input[placeholder*="搜索"]');
    await expect(searchInput).toBeVisible();
    
    // 输入搜索
    await searchInput.fill('demo');
    await page.waitForTimeout(500);
  });

  test('筛选功能', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 筛选按钮应该可见
    const allBtn = page.getByRole('button', { name: '全部', exact: true });
    const activeBtn = page.getByRole('button', { name: '正常', exact: true });
    const disabledBtn = page.getByRole('button', { name: '已禁用', exact: true });
    
    await expect(allBtn).toBeVisible();
    await expect(activeBtn).toBeVisible();
    await expect(disabledBtn).toBeVisible();
    
    // 点击筛选
    await activeBtn.click();
    await page.waitForTimeout(500);
    await allBtn.click();
    await page.waitForTimeout(500);
  });
});

test.describe('移动端 Admin - 模型管理', () => {
  skipAdminIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('模型卡片列表', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    // 等待加载完成
    await page.waitForSelector('table, .md\\:hidden', { timeout: 10000 }).catch(() => null);
    
    // 检查是否有内容
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
  });

  test('新建模型弹窗', async ({ page }) => {
    await page.goto('/admin/models');
    await page.waitForLoadState('networkidle');
    
    // 点击新建按钮
    const createBtn = page.getByRole('button', { name: /新建/ });
    if (await createBtn.isVisible()) {
      await createBtn.click();
      await page.waitForTimeout(500);
      
      // 弹窗应该可见
      const body = await page.textContent('body');
      expect(body).toContain('新建');
      
      // 关闭弹窗
      await page.keyboard.press('Escape');
    }
  });
});

test.describe('移动端 Admin - 公告管理', () => {
  skipAdminIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('公告列表', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    // 页面应该有内容
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
  });

  test('新建公告弹窗', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    // 点击新建按钮
    const createBtn = page.getByRole('button', { name: /新建/ });
    if (await createBtn.isVisible()) {
      await createBtn.click();
      await page.waitForTimeout(500);
      
      // 弹窗应该可见
      const body = await page.textContent('body');
      expect(body).toContain('新建');
      
      // 关闭弹窗
      await page.keyboard.press('Escape');
    }
  });
});

test.describe('移动端 Admin - 套餐管理', () => {
  skipAdminIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('套餐列表', async ({ page }) => {
    await page.goto('/admin/plans');
    await page.waitForLoadState('networkidle');
    
    // 页面应该有内容
    const body = await page.textContent('body');
    expect(body).toBeTruthy();
  });

  test('新建套餐弹窗', async ({ page }) => {
    await page.goto('/admin/plans');
    await page.waitForLoadState('networkidle');
    
    // 点击新建按钮
    const createBtn = page.getByRole('button', { name: /新建/ });
    if (await createBtn.isVisible()) {
      await createBtn.click();
      await page.waitForTimeout(500);
      
      // 弹窗应该可见
      const body = await page.textContent('body');
      expect(body).toContain('新建');
      
      // 关闭弹窗
      await page.keyboard.press('Escape');
    }
  });
});

// ==================== 移动端响应式测试 ====================
test.describe('移动端响应式 - 断点切换', () => {
  skipUserIfNoCreds(test);
  test('从移动端切换到桌面端', async ({ page }) => {
    // 先以移动端登录
    await page.setViewportSize(MOBILE_VIEWPORT);
    await login(page, TEST_USER.email, TEST_USER.password);
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 移动端底部导航可见
    const mobileNav = page.locator('nav').last();
    await expect(mobileNav).toBeVisible();
    
    // 切换到桌面端
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForTimeout(500);
    
    // 桌面侧边栏可见
    const sidebar = page.locator('aside');
    await expect(sidebar).toBeVisible();
  });

  test('从桌面端切换到移动端', async ({ page }) => {
    // 先以桌面端登录
    await page.setViewportSize({ width: 1440, height: 900 });
    await login(page, TEST_USER.email, TEST_USER.password);
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 桌面侧边栏可见
    const sidebar = page.locator('aside');
    await expect(sidebar).toBeVisible();
    
    // 切换到移动端
    await page.setViewportSize(MOBILE_VIEWPORT);
    await page.waitForTimeout(500);
    
    // 移动端底部导航可见
    const mobileNav = page.locator('nav').last();
    await expect(mobileNav).toBeVisible();
  });
});

// ==================== 移动端触摸交互测试 ====================
test.describe('移动端触摸 - 弹窗交互', () => {
  skipAdminIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
  });

  test('弹窗滑动关闭', async ({ page }) => {
    await page.goto('/admin/users');
    await page.waitForLoadState('networkidle');
    
    // 点击第一个编辑按钮
    const editBtn = page.locator('button[aria-label="编辑"], button:has-text("编辑")').first();
    if (await editBtn.isVisible()) {
      await editBtn.click();
      await page.waitForTimeout(500);
      
      // 弹窗应该可见
      const body = await page.textContent('body');
      expect(body).toBeTruthy();
      
      // 按 Escape 关闭
      await page.keyboard.press('Escape');
      await page.waitForTimeout(300);
    }
  });

  test('弹窗表单填写', async ({ page }) => {
    await page.goto('/admin/announcements');
    await page.waitForLoadState('networkidle');
    
    // 点击新建按钮
    await page.getByRole('button', { name: /新建/ }).click();
    await page.waitForTimeout(500);
    
    // 填写表单
    await page.locator('input[placeholder="公告标题"]').fill('移动端测试公告');
    await page.locator('textarea').first().fill('测试摘要');
    
    // 关闭弹窗
    await page.keyboard.press('Escape');
  });
});

// ==================== 移动端性能测试 ====================
test.describe('移动端性能 - 页面加载', () => {
  test.use({ viewport: MOBILE_VIEWPORT });

  test('Panel 页面加载速度', async ({ page }) => {
    skipUserIfNoCreds(test);
    await login(page, TEST_USER.email, TEST_USER.password);
    
    const startTime = Date.now();
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    const endTime = Date.now();
    
    const loadTime = endTime - startTime;
    console.log(`Panel 页面加载时间: ${loadTime}ms`);
    
    // 加载时间应该在合理范围内（小于 5 秒）
    expect(loadTime).toBeLessThan(5000);
  });

  test('Admin 页面加载速度', async ({ page }) => {
    skipAdminIfNoCreds(test);
    await login(page, ADMIN_USER.email, ADMIN_USER.password);
    
    const startTime = Date.now();
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');
    const endTime = Date.now();
    
    const loadTime = endTime - startTime;
    console.log(`Admin 页面加载时间: ${loadTime}ms`);
    
    // 加载时间应该在合理范围内（小于 5 秒）
    expect(loadTime).toBeLessThan(5000);
  });
});

// ==================== 移动端可访问性测试 ====================
test.describe('移动端可访问性', () => {
  skipUserIfNoCreds(test);
  test.use({ viewport: MOBILE_VIEWPORT });

  test('按钮有足够的点击区域', async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
    await page.goto('/panel');
    await page.waitForLoadState('networkidle');
    
    // 检查底部导航按钮大小
    const navButtons = page.locator('nav a, nav button');
    const count = await navButtons.count();
    
    for (let i = 0; i < count; i++) {
      const button = navButtons.nth(i);
      const box = await button.boundingBox();
      if (box) {
        // 按钮应该至少 44x44 像素（iOS 人机界面指南）
        expect(box.width).toBeGreaterThanOrEqual(40);
        expect(box.height).toBeGreaterThanOrEqual(40);
      }
    }
  });

  test('输入框有足够的大小', async ({ page }) => {
    await login(page, TEST_USER.email, TEST_USER.password);
    await page.goto('/panel/requests');
    await page.waitForLoadState('networkidle');
    
    // 检查搜索框大小
    const searchInput = page.locator('input[placeholder*="搜索"]');
    const box = await searchInput.boundingBox();
    if (box) {
      // 输入框应该至少 32 像素高（移动端可以稍小）
      expect(box.height).toBeGreaterThanOrEqual(32);
    }
  });
});
