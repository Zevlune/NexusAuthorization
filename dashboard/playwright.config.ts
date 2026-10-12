import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '../src/test/dashboard/e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'list',
  use: {
    baseURL: 'http://127.0.0.1:5275',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'off',
    launchOptions: process.env.PLAYWRIGHT_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
      : undefined,
    ...devices['Desktop Chrome'],
  },
  webServer: {
    command: 'yarn dev --host 127.0.0.1 --port 5275',
    url: 'http://127.0.0.1:5275',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
