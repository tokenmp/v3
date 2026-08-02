import { skipAdminIfNoCreds } from '../../utils/credentials';
import { test, expect } from '@playwright/test';
import { TestUtils } from '../../utils/test-utils';

test.describe('Admin 公告管理页面', () => {
  skipAdminIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到公告管理页面
    await page.goto('/admin/announcements');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('公告管理');
    
    // 检查创建按钮
    await expect(page.locator('text=新建公告')).toBeVisible();
    
    // 检查表格
    await expect(page.locator('table')).toBeVisible();
  });

  test('新建公告', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建公告');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写公告信息
    await page.fill('input[placeholder*="标题"]', '测试公告');
    await page.fill('textarea[placeholder*="摘要"]', '这是一条测试公告');
    await page.fill('textarea[placeholder*="内容"]', '# 测试公告内容\n\n这是公告的详细内容。');
    
    // 选择级别
    await page.selectOption('select[name="level"]', 'info');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('公告已创建');
    
    // 检查公告是否显示
    await expect(page.locator('text=测试公告')).toBeVisible();
  });

  test('编辑公告', async ({ page }) => {
    // 找到第一个公告的编辑按钮
    const editButton = page.locator('button:has-text("编辑")').first();
    
    if (await editButton.count() > 0) {
      // 点击编辑按钮
      await editButton.click();
      
      // 检查弹窗
      await utils.checkDialogVisible(true);
      
      // 修改公告标题
      const titleInput = page.locator('input[placeholder*="标题"]');
      await titleInput.fill('修改后的公告');
      
      // 点击保存
      await page.click('button:has-text("保存")');
      
      // 等待保存完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('公告已更新');
      
      // 检查公告标题是否更新
      await expect(page.locator('text=修改后的公告')).toBeVisible();
    }
  });

  test('删除公告', async ({ page }) => {
    // 找到第一个公告的删除按钮
    const deleteButton = page.locator('button:has-text("删除")').first();
    
    if (await deleteButton.count() > 0) {
      // 点击删除按钮
      await deleteButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('删除公告', '确定要删除此公告吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待删除完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('公告已删除');
    }
  });

  test('立即发布功能', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建公告');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写公告信息
    await page.fill('input[placeholder*="标题"]', '立即发布的公告');
    await page.fill('textarea[placeholder*="摘要"]', '这是一条立即发布的公告');
    await page.fill('textarea[placeholder*="内容"]', '# 立即发布的公告内容');
    
    // 勾选立即发布
    await page.check('input[name="publishNow"]');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('公告已创建');
    
    // 检查公告状态
    const statusBadge = page.locator('text=已发布').first();
    await expect(statusBadge).toBeVisible();
  });

  test('公告级别显示', async ({ page }) => {
    // 检查级别列
    const levelBadges = page.locator('.badge');
    const count = await levelBadges.count();
    
    for (let i = 0; i < count; i++) {
      const badgeText = await levelBadges.nth(i).textContent();
      expect(['通知', '警告', '维护']).toContain(badgeText);
    }
  });

  test('公告表单验证', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建公告');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 不填写标题，直接点击创建
    await page.click('button:has-text("创建")');
    
    // 检查是否有验证错误
    const errorMessage = page.locator('text=请输入标题');
    await expect(errorMessage).toBeVisible();
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/admin/announcements', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无公告数据');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/announcements', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});

test.describe('Admin 版本日志管理页面', () => {
  skipAdminIfNoCreds(test);
  let utils: TestUtils;

  test.beforeEach(async ({ page }) => {
    utils = new TestUtils(page);
    // 登录为管理员
    await utils.loginAsAdmin();
    // 导航到版本日志管理页面
    await page.goto('/admin/changelogs');
    await utils.waitForPageLoad();
  });

  test('页面加载正确', async ({ page }) => {
    // 检查页面标题
    await utils.checkPageTitle('版本日志');
    
    // 检查创建按钮
    await expect(page.locator('text=新建版本日志')).toBeVisible();
    
    // 检查表格
    await expect(page.locator('table')).toBeVisible();
  });

  test('新建版本日志', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建版本日志');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写版本日志信息
    await page.fill('input[placeholder*="版本号"]', 'v1.0.0');
    await page.fill('input[placeholder*="标题"]', '测试版本日志');
    await page.fill('textarea[placeholder*="内容"]', '# v1.0.0\n\n- 新增功能\n- 修复问题');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('版本日志已创建');
    
    // 检查版本日志是否显示
    await expect(page.locator('text=v1.0.0')).toBeVisible();
  });

  test('编辑版本日志', async ({ page }) => {
    // 找到第一个版本日志的编辑按钮
    const editButton = page.locator('button:has-text("编辑")').first();
    
    if (await editButton.count() > 0) {
      // 点击编辑按钮
      await editButton.click();
      
      // 检查弹窗
      await utils.checkDialogVisible(true);
      
      // 修改版本日志标题
      const titleInput = page.locator('input[placeholder*="标题"]');
      await titleInput.fill('修改后的版本日志');
      
      // 点击保存
      await page.click('button:has-text("保存")');
      
      // 等待保存完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('版本日志已更新');
      
      // 检查版本日志标题是否更新
      await expect(page.locator('text=修改后的版本日志')).toBeVisible();
    }
  });

  test('删除版本日志', async ({ page }) => {
    // 找到第一个版本日志的删除按钮
    const deleteButton = page.locator('button:has-text("删除")').first();
    
    if (await deleteButton.count() > 0) {
      // 点击删除按钮
      await deleteButton.click();
      
      // 检查确认弹窗
      await utils.checkConfirmDialog('删除版本日志', '确定要删除此版本日志吗？');
      
      // 点击确认
      await utils.clickConfirmButton();
      
      // 等待删除完成
      await page.waitForTimeout(1000);
      
      // 检查是否有成功提示
      await utils.waitForToast('版本日志已删除');
    }
  });

  test('Markdown 预览功能', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建版本日志');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写内容
    await page.fill('textarea[placeholder*="内容"]', '# 标题\n\n- 列表项\n- 另一个列表项\n\n**粗体文本**');
    
    // 检查预览区域
    const previewArea = page.locator('.prose');
    await expect(previewArea).toBeVisible();
    
    // 检查 Markdown 渲染
    await expect(previewArea.locator('h1')).toContainText('标题');
    await expect(previewArea.locator('li')).toHaveCount(2);
    await expect(previewArea.locator('strong')).toContainText('粗体文本');
  });

  test('版本日志表单验证', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建版本日志');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 不填写版本号，直接点击创建
    await page.click('button:has-text("创建")');
    
    // 检查是否有验证错误
    const errorMessage = page.locator('text=请输入版本号');
    await expect(errorMessage).toBeVisible();
  });

  test('立即发布功能', async ({ page }) => {
    // 点击新建按钮
    await page.click('text=新建版本日志');
    
    // 检查弹窗
    await utils.checkDialogVisible(true);
    
    // 填写版本日志信息
    await page.fill('input[placeholder*="版本号"]', 'v1.0.1');
    await page.fill('input[placeholder*="标题"]', '立即发布的版本日志');
    await page.fill('textarea[placeholder*="内容"]', '# v1.0.1\n\n立即发布的版本日志内容');
    
    // 勾选立即发布
    await page.check('input[name="publishNow"]');
    
    // 点击创建
    await page.click('button:has-text("创建")');
    
    // 等待创建完成
    await page.waitForTimeout(1000);
    
    // 检查是否有成功提示
    await utils.waitForToast('版本日志已创建');
    
    // 检查版本日志状态
    const statusBadge = page.locator('text=已发布').first();
    await expect(statusBadge).toBeVisible();
  });

  test('空数据状态', async ({ page }) => {
    // 模拟空数据
    await page.route('**/api/v1/admin/changelogs', (route) => {
      route.fulfill({
        status: 200,
        body: JSON.stringify([]),
      });
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查空数据提示
    const emptyMessage = page.locator('text=暂无版本日志数据');
    await expect(emptyMessage).toBeVisible();
  });

  test('错误处理', async ({ page }) => {
    // 模拟网络错误
    await page.route('**/api/v1/admin/changelogs', (route) => {
      route.abort('failed');
    });
    
    await page.reload();
    await utils.waitForPageLoad();
    
    // 检查是否有错误提示
    const errorMessage = page.locator('text=加载失败');
    await expect(errorMessage).toBeVisible();
  });
});
