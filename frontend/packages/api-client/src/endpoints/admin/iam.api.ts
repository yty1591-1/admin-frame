import type { Channel } from '@admin-frame/shared';
import type { HttpClient } from '../../http/client';
import { permissionListDataSchema, subjectPermissionsSchema } from '../../schemas';
import type { PermissionListData, SubjectPermissions } from '../../types';

export type ListPermissionsParams = {
  channel?: Channel | undefined;
  keyword?: string | undefined;
  page?: number | undefined;
  pageSize?: number | undefined;
  signal?: AbortSignal | undefined;
};

export type AdminIamApi = {
  /** 服务端权限快照是唯一权威来源（§10.4）。 */
  mePermissions(): Promise<SubjectPermissions>;
  /** 权限点目录（只读分页）；用于权限目录页与授权选择器。 */
  listPermissions(params?: ListPermissionsParams): Promise<PermissionListData>;
};

export function createAdminIamApi(http: HttpClient): AdminIamApi {
  return {
    mePermissions: () =>
      http.request<SubjectPermissions>('/admin/v1/iam/me/permissions', {
        schema: subjectPermissionsSchema,
        retry: 'safe',
      }),
    listPermissions: (params = {}) =>
      http.request<PermissionListData>('/admin/v1/iam/permissions', {
        query: {
          channel: params.channel,
          keyword: params.keyword,
          page: params.page,
          page_size: params.pageSize,
        },
        signal: params.signal,
        schema: permissionListDataSchema,
        retry: 'safe',
      }),
  };
}
