import type { RouteRecordStringComponent } from '@vben/types';

import { requestClient } from '#/api/request';

/**
 * 获取当前主体可见菜单
 *
 * 后端端点：GET /biz/v1/menus，requestClient 的 baseURL 已包含端别前缀，
 * 因此这里只写相对路径 `/menus`。
 */
export async function getAllMenusApi() {
  return requestClient.get<RouteRecordStringComponent[]>('/menus');
}
