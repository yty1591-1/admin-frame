import type {
  PageQuery,
  RoleStatus,
  SodGroup,
  SodGroupListData,
  SodGroupPayload,
  SodViolationListData,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface SodListParams extends PageQuery {
  channel?: 'admin' | 'biz';
  keyword?: string;
  merchant_id?: string;
  status?: RoleStatus;
}

export async function listSodGroupsApi(
  params: SodListParams = {},
): Promise<SodGroupListData> {
  return requestClient.get<SodGroupListData>('/iam/sod-groups', {
    params: toPageQuery(params),
  });
}

export async function getSodGroupApi(id: string): Promise<SodGroup> {
  return requestClient.get<SodGroup>(`/iam/sod-groups/${id}`);
}

export async function createSodGroupApi(
  payload: SodGroupPayload,
): Promise<SodGroup> {
  return requestClient.post<SodGroup>('/iam/sod-groups', payload);
}

export async function updateSodGroupApi(
  id: string,
  payload: Partial<SodGroupPayload> & { version: number },
): Promise<SodGroup> {
  return requestClient.request<SodGroup>(`/iam/sod-groups/${id}`, {
    data: payload,
    method: 'PATCH',
  });
}

export async function deleteSodGroupApi(
  id: string,
  payload: { reason?: string; version: number },
): Promise<{ status: string }> {
  return requestClient.delete<{ status: string }>(`/iam/sod-groups/${id}`, {
    data: payload,
  });
}

/** 候选配置预检：返回会被该配置判为违规的主体。 */
export async function preflightSodGroupApi(payload: {
  max_active_roles?: number;
  role_ids: string[];
}): Promise<SodViolationListData> {
  return requestClient.post<SodViolationListData>(
    '/iam/sod-groups/preflight',
    payload,
  );
}

export async function listSodViolationsApi(
  id: string,
  limit = 100,
): Promise<SodViolationListData> {
  return requestClient.get<SodViolationListData>(
    `/iam/sod-groups/${id}/violations`,
    { params: { limit } },
  );
}
