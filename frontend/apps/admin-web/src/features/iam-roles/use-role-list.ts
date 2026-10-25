import type { AdminIamApi } from '@admin-frame/api-client';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { iamRoleKeys, type RoleListParams } from './query-keys';

/** 角色列表查询：参数由 URL 状态派生，翻页期间保留上一页数据（§8.3）。 */
export function useRoleList(api: AdminIamApi, params: RoleListParams) {
  return useQuery({
    queryKey: iamRoleKeys.list(params),
    queryFn: ({ signal }) =>
      api.listRoles({
        status: params.status,
        keyword: params.keyword,
        page: params.page,
        pageSize: params.pageSize,
        signal,
      }),
    placeholderData: keepPreviousData,
  });
}
