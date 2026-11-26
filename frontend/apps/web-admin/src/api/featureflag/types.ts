import type { PageQuery } from '../configcenter/types';

export type FeatureFlagStatus = 'disabled' | 'enabled' | 'expired';

export interface FeatureFlagRule {
  channel?: 'admin' | 'biz' | 'c';
  percentage?: number;
  tenant_ids?: string[];
}

export interface FeatureFlag {
  created_by?: string;
  default_enabled: boolean;
  description: string;
  effective_within_seconds?: number;
  expires_at?: string;
  key: string;
  permanent?: boolean;
  rules?: FeatureFlagRule[];
  status: FeatureFlagStatus;
  version: number;
}

export interface FeatureFlagListData {
  items: FeatureFlag[];
  limit?: number;
  offset?: number;
  total: number;
}

export interface FeatureFlagListParams extends PageQuery {
  state?: FeatureFlagStatus;
}
