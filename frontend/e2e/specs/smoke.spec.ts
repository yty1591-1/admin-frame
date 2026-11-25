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

/**
 * 回归：api/request 与 api/core/auth 的模块环曾让 prd 产物登录时报
 * `Cannot read properties of undefined (reading 'post')`（FE-T-04 发现并修复），
 * 这里断言提交确实发出 POST /auth/login，避免该接线再次静默断掉。
 */
test('登录提交发出 POST /auth/login', async ({ page }) => {
  const loginRequests: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().includes('/auth/login')) {
      loginRequests.push(request.url());
    }
  });

  await page.goto('/#/auth/login');
  const inputs = page.locator('input');
  await inputs.nth(0).fill('e2e');
  await inputs.nth(1).fill('secret');
  await page.locator('button', { hasText: '登录' }).first().click();

  await expect.poll(() => loginRequests.length).toBeGreaterThan(0);
});
