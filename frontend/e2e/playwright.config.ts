import { defineConfig, devices } from '@playwright/test';

/**
 * 前端 E2E 冒烟（FE-T-04）：nightly 运行，不进入 PR 必跑门禁。
 *
 * 约定：
 * - 用例只依赖构建产物与浏览器，不依赖真实后端；需要数据的场景用 `page.route` 桩。
 * - 服务端由 `vite preview` 提供（使用 `pnpm build:admin` 的产物）。
 */
export default defineConfig({
  testDir: './specs',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  retries: 1,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['line']] : [['list']],
  use: {
    baseURL: 'http://127.0.0.1:5668',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'pnpm --filter @vben/web-admin preview --port 5668 --strictPort',
    url: 'http://127.0.0.1:5668',
    reuseExistingServer: true,
    timeout: 120_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
