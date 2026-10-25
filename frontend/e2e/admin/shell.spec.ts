import { expect, test } from '@playwright/test';

test('A 端静态产物可加载且端别一致', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: 'A 端运营后台' })).toBeVisible();
  await expect(page.getByText('前端配置加载失败')).toHaveCount(0);
});

test('A 端未知路由渲染 404 页面', async ({ page }) => {
  await page.goto('/unknown-route');

  await expect(page.getByText('页面不存在')).toBeVisible();
});
