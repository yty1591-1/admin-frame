import { expect, test } from '@playwright/test';

/**
 * E2E 冒烟（FE-T-04）基线。nightly 运行，不进入 PR 必跑门禁。
 * 应用使用 hash 路由，路径写作 `/#/...`。
 */

test('未登录访问根路径重定向到登录页并携带 redirect', async ({ page }) => {
  await page.goto('/');

  await expect(page).toHaveURL(/auth\/login/);
  await expect(page).toHaveURL(/redirect=/);
});

test('登录页渲染账号、密码输入与登录按钮', async ({ page }) => {
  await page.goto('/#/auth/login');

  await expect(page.locator('input')).toHaveCount(2);
  await expect(
    page.locator('button', { hasText: '登录' }).first(),
  ).toBeVisible();
});
