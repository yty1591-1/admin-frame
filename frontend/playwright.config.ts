import { defineConfig, devices } from '@playwright/test';

/**
 * E2E（FE-18 / 前端架构 §14.1）。
 * 用例只依赖静态产物与运行期 config.js，不依赖真实后端；
 * 合入 PR 的 smoke 由独立 job 执行（frontend-e2e），不并入 frontend-ci。
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env['CI']),
  retries: process.env['CI'] ? 1 : 0,
  reporter: process.env['CI'] ? [['github'], ['html', { open: 'never' }]] : 'list',
  use: {
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'admin',
      use: { ...devices['Desktop Chrome'], baseURL: 'http://127.0.0.1:4173' },
      testDir: './e2e/admin',
    },
    {
      name: 'biz',
      use: { ...devices['Desktop Chrome'], baseURL: 'http://127.0.0.1:4174' },
      testDir: './e2e/biz',
    },
  ],
  webServer: [
    {
      command: 'pnpm -F @admin-frame/admin-web preview',
      url: 'http://127.0.0.1:4173',
      reuseExistingServer: !process.env['CI'],
      timeout: 60_000,
    },
    {
      command: 'pnpm -F @admin-frame/biz-web preview',
      url: 'http://127.0.0.1:4174',
      reuseExistingServer: !process.env['CI'],
      timeout: 60_000,
    },
  ],
});
