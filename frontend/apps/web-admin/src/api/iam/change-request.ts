import type {
  ChangeAction,
  ChangeRequest,
  ChangeRequestListData,
  ChangeStatus,
  CreateChangeRequestPayload,
  DecideChangeRequestPayload,
  PageQuery,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface ChangeListParams extends PageQuery {
  action?: ChangeAction;
  requested_by?: string;
  status?: ChangeStatus;
}

export async function listChangeRequestsApi(
  params: ChangeListParams = {},
): Promise<ChangeRequestListData> {
  return requestClient.get<ChangeRequestListData>('/iam/change-requests', {
    params: toPageQuery(params),
  });
}

export async function createChangeRequestApi(
  payload: CreateChangeRequestPayload,
): Promise<ChangeRequest> {
  return requestClient.post<ChangeRequest>('/iam/change-requests', payload);
}

export async function approveChangeRequestApi(
  id: string,
  payload: DecideChangeRequestPayload,
): Promise<ChangeRequest> {
  return requestClient.post<ChangeRequest>(
    `/iam/change-requests/${id}/approve`,
    payload,
  );
}

export async function rejectChangeRequestApi(
  id: string,
  payload: DecideChangeRequestPayload,
): Promise<ChangeRequest> {
  return requestClient.post<ChangeRequest>(
    `/iam/change-requests/${id}/reject`,
    payload,
  );
}

export async function cancelChangeRequestApi(
  id: string,
  payload: DecideChangeRequestPayload,
): Promise<ChangeRequest> {
  return requestClient.post<ChangeRequest>(
    `/iam/change-requests/${id}/cancel`,
    payload,
  );
}

export async function applyChangeRequestApi(
  id: string,
  payload: DecideChangeRequestPayload,
): Promise<ChangeRequest> {
  return requestClient.post<ChangeRequest>(
    `/iam/change-requests/${id}/apply`,
    payload,
  );
}
