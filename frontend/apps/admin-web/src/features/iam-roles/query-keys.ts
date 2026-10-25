import type { Role } from '@admin-frame/api-client';
import { normalizeQueryParams } from '@admin-frame/shared';

export type RoleListParams = {
  page: number;
  pageSize: number;
  keyword?: string | undefined;
  status?: Role['status'] | undefined;
};

/**
 * 角色列表 Query Key（§8.2）：端别固定前缀 + 稳定序列化参数，
 * 与权限目录保持同一约定，写操作后按 `all` 精确失效。
 */
export const iamRoleKeys = {
  all: ['admin', 'iam', 'roles'] as const,
  list: (params: RoleListParams) =>
    [
      ...iamRoleKeys.all,
      'list',
      normalizeQueryParams({
        page: params.page,
        page_size: params.pageSize,
        keyword: params.keyword,
        status: params.status,
      }),
    ] as const,
};
