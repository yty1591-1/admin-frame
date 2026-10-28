import type {
  BreakglassGrant,
  DecideChangeRequestPayload,
  SubjectType,
} from './types';

import { requestClient } from '#/api/request';

export interface CreateBreakglassPayload {
  duration_seconds: number;
  reason: string;
  subject_id: string;
  subject_type: SubjectType;
}

/** 申请破玻璃（只创建审批单，不立即授权）。 */
export async function requestBreakglassApi(
  payload: CreateBreakglassPayload,
): Promise<BreakglassGrant> {
  return requestClient.post<BreakglassGrant>('/iam/breakglass', payload);
}

/** 审批并通过破玻璃；id 即审批单 ID。 */
export async function approveBreakglassApi(
  id: string,
  payload: DecideChangeRequestPayload,
): Promise<BreakglassGrant> {
  return requestClient.post<BreakglassGrant>(
    `/iam/breakglass/${id}/approve`,
    payload,
  );
}

/** 提前撤销破玻璃授权；id 即审批单 ID。 */
export async function revokeBreakglassApi(
  id: string,
  payload: DecideChangeRequestPayload,
): Promise<BreakglassGrant> {
  return requestClient.post<BreakglassGrant>(
    `/iam/breakglass/${id}/revoke`,
    payload,
  );
}
