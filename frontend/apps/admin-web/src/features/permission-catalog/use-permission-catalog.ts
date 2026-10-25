import type { AdminIamApi } from '@admin-frame/api-client';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { permissionCatalogKeys, type PermissionCatalogParams } from './query-keys';

/**
 * 权限目录列表查询：参数由 URL 状态派生，翻页期间保留上一页数据避免闪烁（§8.3）。
 * `signal` 交给 HttpClient 中止过期请求，避免慢响应覆盖新结果。
 */
export function usePermissionCatalog(api: AdminIamApi, params: PermissionCatalogParams) {
  return useQuery({
    queryKey: permissionCatalogKeys.list(params),
    queryFn: ({ signal }) =>
      api.listPermissions({
        channel: params.channel,
        keyword: params.keyword,
        page: params.page,
        pageSize: params.pageSize,
        signal,
      }),
    placeholderData: keepPreviousData,
  });
}
