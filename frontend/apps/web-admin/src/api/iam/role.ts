import type {
  CreateRolePayload,
  PageQuery,
  ReplacePermissionsPayload,
  Role,
  RoleConstraint,
  RoleListData,
  RoleStatus,
  UpdateRolePayload,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface RoleListParams extends PageQuery {
  channel?: 'admin' | 'biz';
  keyword?: string;
  merchant_id?: string;
  status?: RoleStatus;
}

export async function listRolesApi(
  params: RoleListParams = {},
): Promise<RoleListData> {
  return requestClient.get<RoleListData>('/iam/roles', {
    params: toPageQuery(params),
  });
}

export async function getRoleApi(id: string): Promise<Role> {
  return requestClient.get<Role>(`/iam/roles/${id}`);
}

export async function createRoleApi(payload: CreateRolePayload): Promise<Role> {
  return requestClient.post<Role>('/iam/roles', payload);
}

export async function updateRoleApi(
  id: string,
  payload: UpdateRolePayload,
): Promise<Role> {
  return requestClient.request<Role>(`/iam/roles/${id}`, {
    data: payload,
    method: 'PATCH',
  });
}

export async function deleteRoleApi(
  id: string,
  payload: { reason?: string; version: number },
): Promise<{ id: string }> {
  return requestClient.delete<{ id: string }>(`/iam/roles/${id}`, {
    data: payload,
  });
}

export async function replaceRolePermissionsApi(
  id: string,
  payload: ReplacePermissionsPayload,
): Promise<{ added: string[]; removed: string[]; role: Role }> {
  return requestClient.put<{ added: string[]; removed: string[]; role: Role }>(
    `/iam/roles/${id}/permissions`,
    payload,
  );
}

export async function getRoleConstraintsApi(
  id: string,
): Promise<RoleConstraint> {
  return requestClient.get<RoleConstraint>(`/iam/roles/${id}/constraints`);
}

export async function putRoleConstraintsApi(
  id: string,
  payload: {
    max_bindings: null | number;
    reason?: string;
    required_role_ids: string[];
    version: number;
  },
): Promise<RoleConstraint> {
  return requestClient.put<RoleConstraint>(
    `/iam/roles/${id}/constraints`,
    payload,
  );
}

export async function cloneRoleApi(
  id: string,
  payload: { code: string; name: string },
): Promise<Role> {
  return requestClient.post<Role>(`/iam/roles/${id}/clone`, payload);
}
