import { expect, test } from '@playwright/test';

test.describe('local mock public smoke', () => {
  test('home exposes only same-origin navigation', async ({ page }) => {
    const requests: string[] = [];
    page.on('request', (request) => requests.push(request.url()));

    await page.goto('/');

    await expect(page).toHaveTitle('TokenMP');
    await expect(page.getByRole('heading', { name: 'TokenMP' })).toBeVisible();
    await expect(page.getByRole('link', { name: '登录' })).toHaveAttribute('href', '/login');
    await expect(page.getByRole('link', { name: '注册' })).toHaveAttribute('href', '/register');
    expect(requests.every((url) => new URL(url).hostname === '127.0.0.1')).toBe(true);
  });

  test('login and registration forms are available without credentials', async ({ page }) => {
    await page.goto('/login');
    await expect(page.locator('input#email')).toBeVisible();
    await expect(page.locator('input#password')).toBeVisible();
    await expect(page.getByRole('button', { name: '登录' })).toBeVisible();
    await expect(page.getByRole('link', { name: '忘记密码？' })).toHaveAttribute(
      'href',
      '/forgot-password',
    );

    await page.goto('/register');
    await expect(page.locator('input#email')).toBeVisible();
    await expect(page.locator('input#password')).toBeVisible();
    await expect(page.locator('input#confirmPassword')).toBeVisible();
    await expect(page.getByRole('button', { name: '注册' })).toBeVisible();
  });
});
