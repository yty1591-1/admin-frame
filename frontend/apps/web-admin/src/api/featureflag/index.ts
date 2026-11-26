import type {
  FeatureFlag,
  FeatureFlagListData,
  FeatureFlagListParams,
} from './types';

import { requestClient } from '#/api/request';

export * from './types';

export interface FeatureFlagCreatePayload {
  default_enabled?: boolean;
  description: string;
  expires_at?: string;
  key: string;
  permanent?: boolean;
}

/** 查询特性开关列表；后端使用 limit/offset。 */
export async function listFeatureFlagsApi(
  params: FeatureFlagListParams = {},
): Promise<FeatureFlagListData> {
  const page = params.page ?? 1;
  const pageSize = Math.min(Math.max(params.pageSize ?? 20, 1), 100);
  return requestClient.get<FeatureFlagListData>('/feature-flags', {
    params: {
      limit: pageSize,
      offset: (page - 1) * pageSize,
      state: params.state,
    },
  });
}

/** 新建特性开关。 */
export async function createFeatureFlagApi(
  payload: FeatureFlagCreatePayload,
): Promise<FeatureFlag> {
  return requestClient.post<FeatureFlag>('/feature-flags', payload);
}
