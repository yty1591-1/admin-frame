import type { Channel } from '@admin-frame/shared';
import type { HttpClient } from '../../http/client';
import {
  permissionListDataSchema,
  roleListDataSchema,
  subjectPermissionsSchema,
} from '../../schemas';
import type { PermissionListData, Role, RoleListData, SubjectPermissions } from '../../types';

export type ListPermissionsParams = {
  channel?: Channel | undefined;
  keyword?: string | undefined;
  page?: number | undefined;
  pageSize?: number | undefined;
  signal?: AbortSignal | undefined;
};

export type ListRolesParams = {
  channel?: Channel | undefined;
  status?: Role['status'] | undefined;
  keyword?: string | undefined;
  page?: number | undefined;
  pageSize?: number | undefined;
  signal?: AbortSignal | undefined;
};

export type DeleteRoleParams = {
  /** 变更原因写入审计。 */
  reason?: string | undefined;
  /** 乐观锁版本；与 If-Match 二选一。 */
  version?: number | undefined;
  /** 写操作必须显式提供；同一次提交的重试复用同一 key（§7.3）。 */
  idempotencyKey: string;
};

export type AdminIamApi = {
  /** 服务端权限快照是唯一权威来源（§10.4）。 */
  mePermissions(): Promise<SubjectPermissions>;
  /** 权限点目录（只读分页）；用于权限目录页与授权选择器。 */
  listPermissions(params?: ListPermissionsParams): Promise<PermissionListData>;
  /** 角色列表（只读分页）；A 端可跨端筛选，B 端由令牌决定商户。 */
  listRoles(params?: ListRolesParams): Promise<RoleListData>;
  /** 删除角色（软删）；40900 表示需审批，调用方必须转审批引导。 */
  deleteRole(id: string, params: DeleteRoleParams): Promise<void>;
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
    listRoles: (params = {}) =>
      http.request<RoleListData>('/admin/v1/iam/roles', {
        query: {
          channel: params.channel,
          status: params.status,
          keyword: params.keyword,
          page: params.page,
          page_size: params.pageSize,
        },
        signal: params.signal,
        schema: roleListDataSchema,
        retry: 'safe',
      }),
    deleteRole: (id, params) =>
      http.requestVoid(`/admin/v1/iam/roles/${encodeURIComponent(id)}`, {
        method: 'DELETE',
        body: { reason: params.reason, version: params.version },
        idempotencyKey: params.idempotencyKey,
        requireIdempotencyKey: true,
      }),
  };
}
