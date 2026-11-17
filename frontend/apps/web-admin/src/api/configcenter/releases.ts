import type {
  ConfigEnv,
  ConfigRelease,
  ConfigReleaseApprovePayload,
  ConfigReleaseCancelPayload,
  ConfigReleaseCreatePayload,
  ConfigReleaseListData,
  ConfigReleasePublishPayload,
  ConfigReleaseRollbackPayload,
  ConfigReleaseStatus,
  PageQuery,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface ConfigReleaseListParams extends PageQuery {
  env: ConfigEnv;
  keyword?: string;
  namespace: string;
  owner?: string;
  status?: ConfigReleaseStatus;
}

export async function listConfigReleasesApi(
  params: ConfigReleaseListParams,
): Promise<ConfigReleaseListData> {
  return requestClient.get<ConfigReleaseListData>('/configs/releases', {
    params: toPageQuery(params),
  });
}

export async function getConfigReleaseApi(id: number): Promise<ConfigRelease> {
  return requestClient.get<ConfigRelease>(`/configs/releases/${id}`);
}

export async function createConfigReleaseApi(
  payload: ConfigReleaseCreatePayload,
): Promise<ConfigRelease> {
  return requestClient.post<ConfigRelease>('/configs/releases', payload);
}

export async function submitConfigReleaseApi(
  id: number,
): Promise<ConfigRelease> {
  return requestClient.post<ConfigRelease>(`/configs/releases/${id}/submit`);
}

export async function approveConfigReleaseApi(
  id: number,
  payload: ConfigReleaseApprovePayload,
): Promise<ConfigRelease> {
  return requestClient.post<ConfigRelease>(
    `/configs/releases/${id}/approve`,
    payload,
  );
}

export async function publishConfigReleaseApi(
  id: number,
  payload: ConfigReleasePublishPayload = {},
): Promise<ConfigRelease> {
  return requestClient.post<ConfigRelease>(
    `/configs/releases/${id}/publish`,
    payload,
  );
}

export async function rollbackConfigReleaseApi(
  id: number,
  payload: ConfigReleaseRollbackPayload = {},
): Promise<ConfigRelease> {
  return requestClient.post<ConfigRelease>(
    `/configs/releases/${id}/rollback`,
    payload,
  );
}

export async function cancelConfigReleaseApi(
  id: number,
  payload: ConfigReleaseCancelPayload,
): Promise<ConfigRelease> {
  return requestClient.post<ConfigRelease>(
    `/configs/releases/${id}/cancel`,
    payload,
  );
}
