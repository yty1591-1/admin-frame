import type { RouteRecordStringComponent } from '@vben/types';

import { requestClient } from '#/api/request';

/**
 * 获取当前主体可见菜单
 *
 * 后端端点：GET /biz/v1/iam/me/menus，requestClient 的 baseURL 已包含端别前缀，
 * 因此这里只写相对路径 `/iam/me/menus`。
 *
 * 必须 `responseReturn: 'data'`，避免 raw AxiosResponse 进入 mapTree。
 */
export async function getAllMenusApi() {
  const data = await requestClient.get<RouteRecordStringComponent[]>(
    '/iam/me/menus',
    { responseReturn: 'data' },
  );
  return Array.isArray(data) ? data : [];
}
