import type {
  AccountListData,
  AccountListParams,
  AccountSummary,
  M2MClient,
  M2MClientListData,
  M2MClientListParams,
  ScimGroupListData,
  ScimToken,
  ScimTokenListData,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from '../configcenter/types';

export * from './types';

export interface M2MClientCreatePayload {
  allowed_channels: Array<'admin' | 'biz' | 'c'>;
  allowed_scopes: string[];
  name: string;
  owner?: string;
}

export interface M2MClientCreateResult {
  client: M2MClient;
  client_secret: string;
}

export interface ScimTokenIssueResult extends ScimToken {
  token: string;
}

/** 列出身份账号。 */
export async function listAccountsApi(
  params: AccountListParams = {},
): Promise<AccountListData> {
  return requestClient.get<AccountListData>('/auth/accounts', {
    params: toPageQuery(params),
  });
}

/** 查询账号详情。 */
export async function getAccountApi(id: string): Promise<AccountSummary> {
  return requestClient.get<AccountSummary>(`/auth/accounts/${id}`);
}

/** 列出 M2M 客户端。 */
export async function listM2MClientsApi(
  params: M2MClientListParams = {},
): Promise<M2MClientListData> {
  return requestClient.get<M2MClientListData>('/auth/m2m/clients', {
    params: toPageQuery(params),
  });
}

/** 创建 M2M 客户端；密钥仅本次返回。 */
export async function createM2MClientApi(
  payload: M2MClientCreatePayload,
): Promise<M2MClientCreateResult> {
  return requestClient.post<M2MClientCreateResult>(
    '/auth/m2m/clients',
    payload,
  );
}

/** 列出 SCIM 令牌。 */
export async function listScimTokensApi(): Promise<ScimTokenListData> {
  return requestClient.get<ScimTokenListData>('/auth/scim/tokens');
}

/** 签发 SCIM 令牌；明文仅本次返回。 */
export async function createScimTokenApi(): Promise<ScimTokenIssueResult> {
  return requestClient.post<ScimTokenIssueResult>('/auth/scim/tokens');
}

/** 吊销 SCIM 令牌。 */
export async function revokeScimTokenApi(id: string): Promise<void> {
  await requestClient.delete(`/auth/scim/tokens/${id}`);
}

/** 列出 SCIM 组映射。 */
export async function listScimGroupsApi(
  params: { page?: number; pageSize?: number } = {},
): Promise<ScimGroupListData> {
  return requestClient.get<ScimGroupListData>('/auth/scim/groups', {
    params: toPageQuery(params),
  });
}
