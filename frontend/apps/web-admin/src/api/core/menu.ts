import type { RouteRecordStringComponent } from '@vben/types';

import { requestClient } from '#/api/request';

/**
 * 获取当前主体可见菜单
 *
 * 后端端点：GET /admin/v1/iam/me/menus，requestClient 的 baseURL 已包含端别前缀，
 * 因此这里只写相对路径 `/iam/me/menus`。
 *
 * 必须 `responseReturn: 'data'`：客户端默认 raw 会把 AxiosResponse 整包返回，
 * mapTree 会报 `tree.map is not a function`；解包后仍做数组兜底。
 */
export async function getAllMenusApi() {
  const data = await requestClient.get<RouteRecordStringComponent[]>(
    '/iam/me/menus',
    { responseReturn: 'data' },
  );
  return Array.isArray(data) ? data : [];
}
