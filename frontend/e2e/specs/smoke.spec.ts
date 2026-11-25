import type { Page } from '@playwright/test';

import { expect, test } from '@playwright/test';

const apiPrefix = '/admin/v1';
const rolePath = '/system/role';
const timestamp = '2026-01-01T00:00:00Z';

function apiResponse(data: unknown) {
  return { code: 0, data, message: 'OK' };
}

function roleRow(id: number) {
  return {
    channel: 'admin',
    code: `e2e_role_${id}`,
    created_at: timestamp,
    data_scopes: [],
    description: '',
    id: String(id),
    name: `冒烟角色 ${id}`,
    permissions: [],
    role_type: 'custom',
    scope_type: 'platform',
    status: 'active',
    updated_at: timestamp,
    version: 1,
  };
}

async function stubRoleSession(
  page: Page,
  { canWrite = false, failSecondPage = false } = {},
) {
  await page.route(`**${apiPrefix}/**`, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    switch (`${request.method()} ${url.pathname.slice(apiPrefix.length)}`) {
      case 'GET /iam/me/menus': {
        return route.fulfill({
          json: apiResponse([
            {
              children: [
                {
                  component: '/system/role/index.vue',
                  meta: { title: 'page.system.role' },
                  name: 'SystemRole',
                  path: rolePath,
                },
              ],
              meta: { title: 'page.system.title' },
              name: 'System',
              path: '/system',
              redirect: rolePath,
            },
            {
              children: [
                {
                  component: '/iam/permission/index.vue',
                  meta: { title: 'page.iam.permission' },
                  name: 'IamPermission',
                  path: '/iam/permission',
                },
              ],
              meta: { title: 'page.iam.title' },
              name: 'Iam',
              path: '/iam',
              redirect: '/iam/permission',
            },
          ]),
        });
      }
      case 'GET /iam/me/permissions': {
        return route.fulfill({
          json: apiResponse({
            channel: 'admin',
            data_scopes: [],
            generated_at: timestamp,
            permissions: [
              'iam:permission:read',
              'iam:role:read',
              ...(canWrite ? ['iam:role:write'] : []),
            ],
            roles: ['e2e'],
            snapshot_version: 1,
            subject_id: '1',
            subject_type: 'admin',
          }),
        });
      }
      case 'GET /iam/permissions': {
        return route.fulfill({ json: apiResponse({ items: [], total: 0 }) });
      }
      case 'GET /iam/roles': {
        const current = Number(url.searchParams.get('page'));
        const size = Number(url.searchParams.get('page_size'));
        if (current === 2 && failSecondPage) {
          return route.fulfill({
            json: {
              code: 50_000,
              message: 'E2E internal details',
              trace_id: 'e2e-role-list',
            },
            status: 500,
          });
        }
        const items = Array.from({ length: 21 }, (_, index) =>
          roleRow(index + 1),
        );
        return route.fulfill({
          json: apiResponse({
            items: items.slice((current - 1) * size, current * size),
            total: items.length,
          }),
        });
      }
      case 'GET /me': {
        return route.fulfill({
          json: apiResponse({
            channel: 'admin',
            display_name: '冒烟用户',
            has_password: true,
            merchant_id: '',
            mfa_enabled: false,
            must_change_password: false,
            phone_bound: false,
            status: 'active',
            subject: 'e2e',
            subject_id: '1',
            token_version: 1,
          }),
        });
      }
      case 'POST /auth/login': {
        return route.fulfill({
          json: apiResponse({
            access_token: 'e2e-access',
            expires_in: 3600,
            refresh_token: 'e2e-refresh',
            token_type: 'Bearer',
          }),
        });
      }
      default: {
        throw new Error(`未配置接口桩：${request.method()} ${url.pathname}`);
      }
    }
  });
}

async function submitLogin(page: Page) {
  await page.goto('/#/auth/login');
  const inputs = page.locator('input');
  await inputs.nth(0).fill('e2e');
  await inputs.nth(1).fill('secret');
  await page.getByRole('button', { exact: true, name: 'login' }).click();
}

function roleRowOnPage(page: Page, id: number) {
  return page.getByRole('cell', { exact: true, name: `冒烟角色 ${id}` });
}

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

test('登录后渲染后端菜单并可从菜单切换页面', async ({ page }) => {
  await stubRoleSession(page);
  await submitLogin(page);
  await expect(roleRowOnPage(page, 1)).toBeVisible();

  await page.getByText('权限管理', { exact: true }).click();
  await page.getByRole('menuitem', { name: '权限目录' }).click();
  await expect(page).toHaveURL(/#\/iam\/permission$/);
  await expect(
    page.getByRole('columnheader', { name: '权限码' }),
  ).toBeVisible();
});

test('新建角色按钮随写权限显示或隐藏', async ({ browser, page }) => {
  await stubRoleSession(page);
  await submitLogin(page);
  await expect(roleRowOnPage(page, 1)).toBeVisible();
  await expect(page.getByRole('button', { name: '新建角色' })).toBeHidden();

  const writerPage = await browser.newPage();
  try {
    await stubRoleSession(writerPage, { canWrite: true });
    await submitLogin(writerPage);
    await expect(
      writerPage.getByRole('button', { name: '新建角色' }),
    ).toBeVisible();
  } finally {
    await writerPage.close();
  }
});

test('切换角色列表页码后按新页码请求并替换表格数据', async ({ page }) => {
  await stubRoleSession(page);
  await submitLogin(page);
  await expect(roleRowOnPage(page, 1)).toBeVisible();

  const pageTwoResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      response.request().method() === 'GET' &&
      url.pathname === `${apiPrefix}/iam/roles` &&
      url.searchParams.get('page') === '2' &&
      url.searchParams.get('page_size') === '20'
    );
  });
  await page.getByTitle('2', { exact: true }).click();
  const response = await pageTwoResponse;
  expect(response.ok()).toBe(true);
  await expect(roleRowOnPage(page, 21)).toBeVisible();
  await expect(roleRowOnPage(page, 1)).toBeHidden();
});

test('列表请求失败时显示本地化错误与追踪编号', async ({ page }) => {
  await stubRoleSession(page, { failSecondPage: true });
  await submitLogin(page);
  await expect(roleRowOnPage(page, 1)).toBeVisible();
  await page.getByTitle('2', { exact: true }).click();

  await expect(
    page.getByText('内部服务器错误，请稍后再试。（trace: e2e-role-list）', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByText('E2E internal details')).toBeHidden();
});
