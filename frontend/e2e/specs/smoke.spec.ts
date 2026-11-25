import { expect, test } from '@playwright/test';

/**
 * E2E 冒烟（FE-T-04）基线用例。nightly 运行，不进入 PR 必跑门禁。
 *
 * 当前已落地：未登录访问受保护路由 → 登录页并携带 redirect。
 * 待补（需 page.route 桩数据）：登录页渲染、菜单、权限按钮、分页、错误提示。
 */

test.describe('路由守卫', () => {
  test('未登录访问首页重定向到登录页并携带 redirect', async ({ page }) => {
    await page.goto('/');

    await expect(page).toHaveURL(/\/auth\/login/);
    await expect(page).toHaveURL(/redirect=/);
  });
});
