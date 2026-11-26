import type { Tenant, TenantListData, TenantListParams } from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from '../configcenter/types';

export * from './types';

export interface TenantCreatePayload {
  code?: string;
  name: string;
  owner_subject_id?: string;
  plan_code?: string;
}

/** 查询租户列表。 */
export async function listTenantsApi(
  params: TenantListParams = {},
): Promise<TenantListData> {
  return requestClient.get<TenantListData>('/tenants', {
    params: toPageQuery(params),
  });
}

/** 查询租户详情。 */
export async function getTenantApi(id: string): Promise<Tenant> {
  return requestClient.get<Tenant>(`/tenants/${id}`);
}

/** 创建租户（初始 pending）。 */
export async function createTenantApi(
  payload: TenantCreatePayload,
): Promise<Tenant> {
  return requestClient.post<Tenant>('/tenants', payload);
}
