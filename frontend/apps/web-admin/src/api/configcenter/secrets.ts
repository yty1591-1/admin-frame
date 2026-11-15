import type { Recordable } from '@vben/types';

import { requestClient } from '#/api/request';

export type ConfigSecretStatus =
  | 'draft'
  | 'pending_review'
  | 'active'
  | 'rejected'
  | 'revoked'
  | 'destroyed';

export interface ConfigSecret {
  id: number;
  env: string;
  namespace: string;
  name: string;
  description: string;
  owner: string;
  status: ConfigSecretStatus;
  active_version?: number | null;
  version: number;
  fingerprint: string;
  rotate_interval_days: number;
  dual_version: boolean;
  pending_action?: string;
  submitted_by?: string;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

export interface ConfigSecretListResult {
  items: ConfigSecret[];
  total: number;
}

export interface ConfigSecretGrant {
  id: number;
  env: string;
  namespace: string;
  service_identity: string;
  permission: string;
  created_by: string;
  created_at: string;
  revoked_at?: string | null;
}

export async function listConfigSecretsApi(params: Recordable<any>) {
  return requestClient.get<ConfigSecretListResult>('/admin/v1/configs/secrets', {
    params,
  });
}

export async function createConfigSecretApi(data: Recordable<any>) {
  return requestClient.post<ConfigSecret>('/admin/v1/configs/secrets', data);
}

export async function getConfigSecretApi(id: number) {
  return requestClient.get<ConfigSecret>(`/admin/v1/configs/secrets/${id}`);
}

export async function submitConfigSecretApi(id: number) {
  return requestClient.post<ConfigSecret>(
    `/admin/v1/configs/secrets/${id}/submit`,
  );
}

export async function approveConfigSecretApi(
  id: number,
  data: { decision: 'approved' | 'rejected'; comment?: string },
) {
  return requestClient.post<ConfigSecret>(
    `/admin/v1/configs/secrets/${id}/approve`,
    data,
  );
}

export async function rotateConfigSecretApi(
  id: number,
  data: { plaintext: string; comment?: string },
) {
  return requestClient.post<ConfigSecret>(
    `/admin/v1/configs/secrets/${id}/rotate`,
    data,
  );
}

export async function revokeConfigSecretApi(
  id: number,
  data?: { comment?: string },
) {
  return requestClient.post<ConfigSecret>(
    `/admin/v1/configs/secrets/${id}/revoke`,
    data ?? {},
  );
}

export async function listConfigSecretGrantsApi(id: number) {
  return requestClient.get<{ items: ConfigSecretGrant[]; total: number }>(
    `/admin/v1/configs/secrets/${id}/grants`,
  );
}

export async function createConfigSecretGrantApi(
  id: number,
  data: { service_identity: string },
) {
  return requestClient.post<ConfigSecretGrant>(
    `/admin/v1/configs/secrets/${id}/grants`,
    data,
  );
}
