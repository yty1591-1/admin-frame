import type { PageQuery } from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export type CertificationStatus = 'active' | 'closed' | 'draft';

export interface Certification {
  due_at: string;
  id: string;
  name: string;
  status: CertificationStatus;
  task_count?: number;
}

export interface CertificationListData {
  items: Certification[];
  total: number;
}

export interface CertificationListParams extends PageQuery {
  status?: CertificationStatus;
}

export interface CertificationCreatePayload {
  channel?: 'admin' | 'biz';
  due_at: string;
  name: string;
  privileged_only?: boolean;
  reason?: string;
  role_ids?: string[];
}

/** 访问复核活动列表。 */
export async function listCertificationsApi(
  params: CertificationListParams = {},
): Promise<CertificationListData> {
  return requestClient.get<CertificationListData>('/iam/certifications', {
    params: toPageQuery(params),
  });
}

/** 创建访问复核活动。 */
export async function createCertificationApi(
  payload: CertificationCreatePayload,
): Promise<Certification> {
  return requestClient.post<Certification>('/iam/certifications', payload);
}

/** 查询复核活动详情。 */
export async function getCertificationApi(id: string): Promise<Certification> {
  return requestClient.get<Certification>(`/iam/certifications/${id}`);
}
