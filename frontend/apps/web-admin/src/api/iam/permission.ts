import type { PageQuery, Permission, PermissionListData } from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface PermissionListParams extends PageQuery {
  channel?: 'admin' | 'biz';
  keyword?: string;
}

/** 权限目录（只读，分页）。 */
export async function listPermissionsApi(
  params: PermissionListParams = {},
): Promise<PermissionListData> {
  return requestClient.get<PermissionListData>('/iam/permissions', {
    params: toPageQuery(params),
  });
}

/** 拉取全量权限（每页 100，循环直到取完），供分组选择器使用。 */
export async function listAllPermissionsApi(): Promise<Permission[]> {
  const items: Permission[] = [];
  let page = 1;
  for (;;) {
    const result = await listPermissionsApi({ page, pageSize: 100 });
    items.push(...result.items);
    if (items.length >= result.total || result.items.length === 0) {
      return items;
    }
    page += 1;
  }
}
