import type {
  Channel,
  MenuAdmin,
  MenuAdminListData,
  MenuAdminTreeNode,
  MenuPayload,
  PageQuery,
  RoleStatus,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface MenuListParams extends PageQuery {
  channel?: Channel;
  keyword?: string;
  parent_code?: string;
  status?: RoleStatus;
}

export async function listMenusApi(
  params: MenuListParams = {},
): Promise<MenuAdminListData> {
  return requestClient.get<MenuAdminListData>('/iam/menus', {
    params: toPageQuery(params),
  });
}

export async function getMenuTreeApi(
  includeDisabled = true,
): Promise<MenuAdminTreeNode[]> {
  return requestClient.get<MenuAdminTreeNode[]>('/iam/menus/tree', {
    params: { include_disabled: includeDisabled },
  });
}

/** 可引用的前端页面组件清单（菜单表单下拉）。 */
export async function listMenuComponentsApi(): Promise<string[]> {
  return requestClient.get<string[]>('/iam/menus/components');
}

export async function getMenuApi(id: string): Promise<MenuAdmin> {
  return requestClient.get<MenuAdmin>(`/iam/menus/${id}`);
}

export async function createMenuApi(payload: MenuPayload): Promise<MenuAdmin> {
  return requestClient.post<MenuAdmin>('/iam/menus', payload);
}

export async function updateMenuApi(
  id: string,
  payload: Partial<MenuPayload> & { version: number },
): Promise<MenuAdmin> {
  return requestClient.request<MenuAdmin>(`/iam/menus/${id}`, {
    data: payload,
    method: 'PATCH',
  });
}

export async function disableMenuApi(
  id: string,
  payload: { reason?: string; version: number },
): Promise<MenuAdmin> {
  return requestClient.delete<MenuAdmin>(`/iam/menus/${id}`, {
    data: payload,
  });
}

export async function enableMenuApi(
  id: string,
  payload: { reason?: string; version: number },
): Promise<MenuAdmin> {
  return requestClient.post<MenuAdmin>(`/iam/menus/${id}/enable`, payload);
}
