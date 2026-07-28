import { defineConfig, devices } from '@playwright/test';

/**
 * TokenMP v3 E2E 测试配置
 * @see https://playwright.dev/docs/test-configuration
 */
export default defineConfig({
  // 测试目录
  testDir: './tests',
  
  // 并行运行测试
  fullyParallel: true,
  
  // CI 环境下禁止并行失败
  forbidOnly: !!process.env.CI,
  
  // 重试次数
  retries: process.env.CI ? 2 : 0,
  
  // 并行 worker 数量
  workers: process.env.CI ? 1 : undefined,
  
  // 报告器
  reporter: [
    ['html', { open: 'never' }],
    ['list']
  ],
  
  // 全局设置
  use: {
    // 基础 URL - 优先使用环境变量，默认指向 dev 服务器
    baseURL: process.env.BASE_URL || 'http://122.51.255.26',
    
    // 浏览器设置
    headless: true,
    viewport: { width: 1440, height: 900 },
    
    // 截图和视频
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    
    // 超时设置
    actionTimeout: 10000,
    navigationTimeout: 30000,
    
    // 忽略 HTTPS 错误
    ignoreHTTPSErrors: true,
  },
  
  // 项目配置
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },
    {
      name: 'webkit',
      use: { ...devices['Desktop Safari'] },
    },
    {
      name: 'Mobile Chrome',
      use: { ...devices['Pixel 5'] },
    },
    {
      name: 'Mobile Safari',
      use: { ...devices['iPhone 12'] },
    },
  ],
  
  // 本地开发服务器 - 已禁用，因为服务器已在运行
  // webServer: {
  //   command: 'cd .. && pnpm dev',
  //   url: 'http://localhost:3100',
  //   reuseExistingServer: !process.env.CI,
  //   timeout: 120000,
  // },
});
