import { defineConfig, devices } from '@playwright/test';

const localPort = Number(process.env.E2E_PORT ?? 3101);
const hasExplicitBaseURL = Boolean(process.env.BASE_URL);
const baseURL = process.env.BASE_URL ?? `http://127.0.0.1:${localPort}`;

/**
 * TokenMP v3 E2E configuration.
 *
 * A supplied BASE_URL is an explicit opt-in to a separately managed target.
 * Without it, only the credential-free local smoke project is available and
 * Playwright starts an isolated mock web app on the loopback interface.
 */
export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [['html', { open: 'never' }], ['list']],
  use: {
    baseURL,
    headless: true,
    viewport: { width: 1440, height: 900 },
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10_000,
    navigationTimeout: 30_000,
    ignoreHTTPSErrors: true,
  },
  projects: hasExplicitBaseURL
    ? [
        { name: 'chromium', testIgnore: /smoke\//, use: { ...devices['Desktop Chrome'] } },
        { name: 'firefox', testIgnore: /smoke\//, use: { ...devices['Desktop Firefox'] } },
        { name: 'webkit', testIgnore: /smoke\//, use: { ...devices['Desktop Safari'] } },
        { name: 'Mobile Chrome', testIgnore: /smoke\//, use: { ...devices['Pixel 5'] } },
        { name: 'Mobile Safari', testIgnore: /smoke\//, use: { ...devices['iPhone 12'] } },
      ]
    : [
        {
          name: 'chromium-smoke',
          testMatch: /smoke\/.*\.spec\.ts/,
          use: { ...devices['Desktop Chrome'] },
        },
        {
          name: 'Mobile Chrome-smoke',
          testMatch: /smoke\/.*\.spec\.ts/,
          use: { ...devices['Pixel 5'] },
        },
      ],
  webServer: hasExplicitBaseURL
    ? undefined
    : {
        command: `pnpm --dir .. --filter @tokenmp/web exec next dev --port ${localPort}`,
        url: baseURL,
        reuseExistingServer: false,
        timeout: 120_000,
        env: {
          E2E_NEXT_DIST_DIR: '.next-e2e',
          NEXT_PUBLIC_USE_MOCK_AUTH: '1',
          NEXT_PUBLIC_USE_MOCK_NOTICE: '1',
        },
      },
});
