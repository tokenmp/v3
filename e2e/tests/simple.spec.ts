import { test, expect } from '@playwright/test';

test('简单测试', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveTitle(/TokenMP/);
});
