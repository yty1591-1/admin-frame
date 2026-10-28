import type {
  Binding,
  BindingListData,
  CreateBindingPayload,
  IamSubjectPermissions,
  SubjectType,
} from './types';

import { requestClient } from '#/api/request';

export interface BindingListParams {
  role_id?: string;
  subject_id?: string;
  subject_type?: SubjectType;
}

/** 绑定查询：role_id 优先，否则按 subject 查询；两者都空时后端返回空列表。 */
export async function listBindingsApi(
  params: BindingListParams,
): Promise<BindingListData> {
  return requestClient.get<BindingListData>('/iam/bindings', { params });
}

export async function createBindingApi(
  payload: CreateBindingPayload,
): Promise<Binding> {
  return requestClient.post<Binding>('/iam/bindings', payload);
}

export async function deleteBindingApi(
  id: string,
  reason?: string,
): Promise<{ id: string }> {
  return requestClient.delete<{ id: string }>(`/iam/bindings/${id}`, {
    data: reason ? { reason } : {},
  });
}

export async function getSubjectPermissionsApi(
  subjectType: SubjectType,
  subjectId: string,
): Promise<IamSubjectPermissions> {
  return requestClient.get<IamSubjectPermissions>(
    `/iam/subjects/${subjectType}/${encodeURIComponent(subjectId)}/permissions`,
  );
}
