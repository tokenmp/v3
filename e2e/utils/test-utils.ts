import { test, expect, Page } from '@playwright/test';
import { e2eCredentials } from './credentials';

// 测试工具函数
export class TestUtils {
  constructor(private page: Page) {}

  // 等待页面加载完成
  async waitForPageLoad() {
    // Live pages poll health and request-log endpoints, so networkidle is not a
    // deterministic readiness signal. Wait for the document, then let each
    // test assert its page-specific, user-visible affordance.
    await this.page.waitForLoadState('domcontentloaded');
    await this.page.waitForSelector('body', { state: 'visible' });
  }

  // 登录为管理员
  async loginAsAdmin(
    email: string = e2eCredentials().admin.email,
    password: string = e2eCredentials().admin.password,
  ) {
    await this.page.goto('/login');
    await this.page.fill('input[type="email"]', email);
    await this.page.fill('input[type="password"]', password);
    await this.page.click('button[type="submit"]');
    // The shared login flow always lands on the Panel; admin access is verified
    // by explicitly navigating to the protected admin landing page afterwards.
    await this.page.waitForURL('/panel');
    await this.page.goto('/admin');
    await this.page.waitForURL('/admin');
  }

  // 登录为普通用户
  async loginAsUser(
    email: string = e2eCredentials().user.email,
    password: string = e2eCredentials().user.password,
  ) {
    await this.page.goto('/login');
    await this.page.fill('input[type="email"]', email);
    await this.page.fill('input[type="password"]', password);
    await this.page.click('button[type="submit"]');
    await this.page.waitForURL('/panel');
  }

  // 截图并保存
  async takeScreenshot(name: string) {
    await this.page.screenshot({ path: `./screenshots/${name}.png`, fullPage: true });
  }

  // 等待 Toast 消息
  async waitForToast(message: string) {
    await this.page.waitForSelector(`text=${message}`);
  }

  // 检查元素是否存在
  async elementExists(selector: string) {
    return await this.page.locator(selector).count() > 0;
  }

  // 检查元素是否可见
  async elementVisible(selector: string) {
    const element = this.page.locator(selector);
    return await element.isVisible();
  }

  // 等待并点击按钮
  async waitAndClick(selector: string) {
    await this.page.waitForSelector(selector, { state: 'visible' });
    await this.page.click(selector);
  }

  // 填写表单字段
  async fillFormField(selector: string, value: string) {
    await this.page.waitForSelector(selector, { state: 'visible' });
    await this.page.fill(selector, value);
  }

  // 选择下拉选项
  async selectOption(selector: string, value: string) {
    await this.page.waitForSelector(selector, { state: 'visible' });
    await this.page.selectOption(selector, value);
  }

  // The redesigned app deliberately keeps the document title at "TokenMP".
  // Admin pages expose their route name as h1; panel pages expose it through
  // the accessible breadcrumb instead of duplicating a visual page heading.
  async checkPageTitle(expectedTitle: string) {
    const heading = this.page.getByRole('heading', { level: 1, name: expectedTitle });
    if (await heading.count()) {
      await expect(heading).toBeVisible();
      return;
    }
    await expect(
      this.page.locator('nav[aria-label="面包屑"]').getByText(expectedTitle, { exact: true }),
    ).toBeVisible();
  }

  // 检查 URL
  async checkUrl(expectedUrl: string) {
    expect(this.page.url()).toContain(expectedUrl);
  }

  // 等待加载完成
  async waitForLoading() {
    await this.page.waitForSelector('[data-testid="loading"]', { state: 'hidden', timeout: 10000 }).catch(() => {
      // 如果没有 loading 元素，等待网络空闲
    });
  }

  // 检查表格行数
  async checkTableRowCount(selector: string, expectedCount: number) {
    const rows = await this.page.locator(`${selector} tbody tr`).count();
    expect(rows).toBe(expectedCount);
  }

  // 检查分页信息
  async checkPaginationInfo(expectedText: string) {
    const paginationText = await this.page.locator('.text-xs.text-muted-foreground').first().textContent();
    expect(paginationText).toContain(expectedText);
  }

  // 检查按钮状态
  async checkButtonDisabled(selector: string, disabled: boolean) {
    const button = this.page.locator(selector);
    if (disabled) {
      await expect(button).toBeDisabled();
    } else {
      await expect(button).toBeEnabled();
    }
  }

  // 检查 Badge 内容
  async checkBadgeContent(selector: string, expectedText: string) {
    const badge = this.page.locator(selector);
    await expect(badge).toContainText(expectedText);
  }

  // 检查输入框值
  async checkInputValue(selector: string, expectedValue: string) {
    const input = this.page.locator(selector);
    await expect(input).toHaveValue(expectedValue);
  }

  // 检查复选框状态
  async checkCheckboxState(selector: string, checked: boolean) {
    const checkbox = this.page.locator(selector);
    if (checked) {
      await expect(checkbox).toBeChecked();
    } else {
      await expect(checkbox).not.toBeChecked();
    }
  }

  // 检查弹窗是否存在
  private dialog() {
    // The app uses both native <dialog> modals and overlay dialogs. Keep the
    // helper aligned with either accessible implementation.
    return this.page.locator('dialog[open], [role="dialog"], .fixed.inset-0.z-50').last();
  }

  async checkDialogVisible(visible: boolean) {
    const dialog = this.dialog();
    if (visible) {
      await expect(dialog).toBeVisible();
    } else {
      await expect(dialog).not.toBeVisible();
    }
  }

  // 检查确认弹窗
  async checkConfirmDialog(title: string, description: string) {
    const dialog = this.dialog();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('heading', { name: title })).toBeVisible();
    await expect(dialog).toContainText(description);
  }

  // 点击确认弹窗的确认按钮
  async clickConfirmButton() {
    await this.dialog().getByRole('button', { name: /确认/ }).click();
  }

  // 点击确认弹窗的取消按钮
  async clickCancelButton() {
    await this.dialog().getByRole('button', { name: '取消', exact: true }).click();
  }

  // 检查表格单元格内容
  async checkTableCell(row: number, column: number, expectedText: string) {
    const cell = this.page.locator(`tbody tr:nth-child(${row}) td:nth-child(${column})`);
    await expect(cell).toContainText(expectedText);
  }

  // 检查搜索功能
  async testSearch(searchInput: string, searchText: string, expectedResults: number) {
    await this.page.fill(searchInput, searchText);
    await this.page.waitForTimeout(500); // 等待防抖
    const rows = await this.page.locator('tbody tr').count();
    expect(rows).toBe(expectedResults);
  }

  // 检查筛选功能
  async testFilter(filterSelector: string, filterValue: string, expectedResults: number) {
    await this.page.click(filterSelector);
    await this.page.waitForTimeout(500);
    const rows = await this.page.locator('tbody tr').count();
    expect(rows).toBe(expectedResults);
  }

  // 检查分页功能
  async testPagination(nextButton: string, prevButton: string, currentPage: number) {
    // 点击下一页
    await this.page.click(nextButton);
    await this.page.waitForTimeout(500);
    const pageText = await this.page.locator('.text-xs.tabular-nums').textContent();
    expect(pageText).toContain(`${currentPage + 1}`);
    
    // 点击上一页
    await this.page.click(prevButton);
    await this.page.waitForTimeout(500);
    const pageTextAfter = await this.page.locator('.text-xs.tabular-nums').textContent();
    expect(pageTextAfter).toContain(`${currentPage}`);
  }

  // 检查响应式设计
  async testResponsiveDesign(desktopSelector: string, mobileSelector: string) {
    // 桌面视图
    await this.page.setViewportSize({ width: 1440, height: 900 });
    await this.page.waitForTimeout(300);
    const desktopVisible = await this.page.locator(desktopSelector).isVisible();
    const mobileHidden = await this.page.locator(mobileSelector).isHidden();
    expect(desktopVisible).toBe(true);
    expect(mobileHidden).toBe(true);
    
    // 移动视图
    await this.page.setViewportSize({ width: 390, height: 844 });
    await this.page.waitForTimeout(300);
    const desktopHidden = await this.page.locator(desktopSelector).isHidden();
    const mobileVisible = await this.page.locator(mobileSelector).isVisible();
    expect(desktopHidden).toBe(true);
    expect(mobileVisible).toBe(true);
  }

  // 检查错误处理
  async testErrorHandling(errorMessage: string) {
    await this.page.waitForSelector(`text=${errorMessage}`);
    const errorElement = this.page.locator(`text=${errorMessage}`);
    await expect(errorElement).toBeVisible();
  }

  // 检查加载状态
  async testLoadingState(loadingSelector: string) {
    const loading = this.page.locator(loadingSelector);
    await expect(loading).toBeVisible();
    await loading.waitFor({ state: 'hidden', timeout: 10000 });
  }

  // 检查可访问性
  async testAccessibility(selector: string) {
    const element = this.page.locator(selector);
    const ariaLabel = await element.getAttribute('aria-label');
    expect(ariaLabel).toBeTruthy();
  }

  // 检查键盘导航
  async testKeyboardNavigation(selector: string) {
    const element = this.page.locator(selector);
    await element.focus();
    await this.page.keyboard.press('Enter');
    await this.page.waitForTimeout(300);
  }
}

// 通用测试夹具
export const testFixture = {
  // 管理员用户数据
  adminUser: {
    email: e2eCredentials().admin.email,
    password: e2eCredentials().admin.password,
    role: 'admin',
    status: 'active',
  },
  
  // 普通用户数据
  regularUser: {
    email: e2eCredentials().user.email,
    password: e2eCredentials().user.password,
    role: 'user',
    status: 'active',
  },
  
  // 测试套餐数据
  testPlan: {
    name: '测试套餐',
    type: 'token',
    price: 100,
    quota: 1000000,
    status: 'active',
  },
  
  // 测试 API Key 数据
  testApiKey: {
    name: '测试密钥',
    key: 'sk-test123456789',
  },
};
