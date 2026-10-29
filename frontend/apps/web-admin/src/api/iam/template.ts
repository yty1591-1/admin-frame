import type {
  CreateTemplatePayload,
  DataScope,
  InstantiateTemplatePayload,
  PageQuery,
  Role,
  RoleStatus,
  RoleTemplate,
  RoleTemplateListData,
  TemplateDriftListData,
  UpdateTemplatePayload,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface TemplateListParams extends PageQuery {
  channel?: 'admin' | 'biz';
  keyword?: string;
  status?: RoleStatus;
}

export async function listTemplatesApi(
  params: TemplateListParams = {},
): Promise<RoleTemplateListData> {
  return requestClient.get<RoleTemplateListData>('/iam/role-templates', {
    params: toPageQuery(params),
  });
}

export async function getTemplateApi(id: string): Promise<RoleTemplate> {
  return requestClient.get<RoleTemplate>(`/iam/role-templates/${id}`);
}

export async function createTemplateApi(
  payload: CreateTemplatePayload,
): Promise<RoleTemplate> {
  return requestClient.post<RoleTemplate>('/iam/role-templates', payload);
}

export async function updateTemplateApi(
  id: string,
  payload: UpdateTemplatePayload,
): Promise<RoleTemplate> {
  return requestClient.request<RoleTemplate>(`/iam/role-templates/${id}`, {
    data: payload,
    method: 'PATCH',
  });
}

export async function deleteTemplateApi(
  id: string,
  payload: { reason?: string; version: number },
): Promise<{ id: string }> {
  return requestClient.delete<{ id: string }>(`/iam/role-templates/${id}`, {
    data: payload,
  });
}

export async function replaceTemplatePermissionsApi(
  id: string,
  payload: {
    data_scopes?: DataScope[];
    permissions: string[];
    version: number;
  },
): Promise<RoleTemplate> {
  return requestClient.put<RoleTemplate>(
    `/iam/role-templates/${id}/permissions`,
    payload,
  );
}

export async function getTemplateDriftApi(
  id: string,
): Promise<TemplateDriftListData> {
  return requestClient.get<TemplateDriftListData>(
    `/iam/role-templates/${id}/drift`,
  );
}

export async function syncTemplateApi(
  id: string,
  payload: { dry_run: boolean; reason?: string },
): Promise<TemplateDriftListData> {
  return requestClient.post<TemplateDriftListData>(
    `/iam/role-templates/${id}/sync`,
    payload,
  );
}

export async function instantiateTemplateApi(
  id: string,
  payload: InstantiateTemplatePayload,
): Promise<Role> {
  return requestClient.post<Role>(
    `/iam/role-templates/${id}/instantiate`,
    payload,
  );
}
