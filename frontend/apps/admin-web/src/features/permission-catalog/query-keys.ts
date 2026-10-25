import { normalizeQueryParams } from '@admin-frame/shared';
import type { Channel } from '@admin-frame/shared';

export type PermissionCatalogParams = {
  page: number;
  pageSize: number;
  keyword?: string | undefined;
  channel?: Channel | undefined;
};

/**
 * 权限目录 Query Key（§8.2）：端别固定前缀 + 稳定序列化参数，
 * 参数去除空值并按 key 排序，保证同一请求不会产生不同缓存条目。
 */
export const permissionCatalogKeys = {
  all: ['admin', 'iam', 'permissions'] as const,
  list: (params: PermissionCatalogParams) =>
    [
      ...permissionCatalogKeys.all,
      'list',
      normalizeQueryParams({
        page: params.page,
        page_size: params.pageSize,
        keyword: params.keyword,
        channel: params.channel,
      }),
    ] as const,
};
