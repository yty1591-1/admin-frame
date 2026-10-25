import { expect, test } from '@playwright/test';

test('A 端未登录访问根路径跳转登录页', async ({ page }) => {
  await page.goto('/');

  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByText('使用平台账号登录')).toBeVisible();
  await expect(page.getByLabel('账号')).toBeVisible();
  await expect(page.getByLabel('密码')).toBeVisible();
  await expect(page.getByText('前端配置加载失败')).toHaveCount(0);
});

test('A 端未知路由在未登录时同样进入登录页', async ({ page }) => {
  await page.goto('/unknown-route');

  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByText('使用平台账号登录')).toBeVisible();
});
