import type {
  ConfigEnv,
  ConfigItem,
  ConfigItemCreatePayload,
  ConfigItemListData,
  ConfigItemStatus,
  ConfigItemUpdatePayload,
  ConfigItemVersion,
  ConfigItemVersionListData,
  ConfigNamespaceListData,
  PageQuery,
} from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from './types';

export interface ConfigItemListParams extends PageQuery {
  env: ConfigEnv;
  namespace: string;
  status?: ConfigItemStatus;
}

export async function listConfigNamespacesApi(): Promise<ConfigNamespaceListData> {
  return requestClient.get<ConfigNamespaceListData>('/configs/namespaces');
}

export async function listConfigItemsApi(
  params: ConfigItemListParams,
): Promise<ConfigItemListData> {
  return requestClient.get<ConfigItemListData>('/configs/items', {
    params: toPageQuery(params),
  });
}

export async function getConfigItemApi(id: number): Promise<ConfigItem> {
  return requestClient.get<ConfigItem>(`/configs/items/${id}`);
}

export async function createConfigItemApi(
  payload: ConfigItemCreatePayload,
): Promise<ConfigItem> {
  return requestClient.post<ConfigItem>('/configs/items', payload);
}

export async function updateConfigItemValueApi(
  id: number,
  payload: ConfigItemUpdatePayload,
): Promise<ConfigItem> {
  return requestClient.put<ConfigItem>(`/configs/items/${id}`, payload);
}

export interface ConfigItemVersionListParams extends PageQuery {}

export async function listConfigItemVersionsApi(
  id: number,
  params: ConfigItemVersionListParams = {},
): Promise<ConfigItemVersionListData> {
  return requestClient.get<ConfigItemVersionListData>(
    `/configs/items/${id}/versions`,
    { params: toPageQuery(params) },
  );
}

export async function getConfigItemVersionApi(
  id: number,
  version: number,
): Promise<ConfigItemVersion> {
  return requestClient.get<ConfigItemVersion>(
    `/configs/items/${id}/versions/${version}`,
  );
}
