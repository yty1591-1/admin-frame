/** 配置中心契约类型（与 api/openapi/schemas/admin/configcenter.yaml 对齐）。 */

export type ConfigEnv = 'dev' | 'local' | 'prod' | 'staging' | 'test';

export type ConfigItemStatus =
  | 'active'
  | 'archived'
  | 'deprecated'
  | 'draft';

export type ConfigValueType =
  | 'bool'
  | 'duration'
  | 'int'
  | 'json'
  | 'string';

export type ConfigReleaseStatus =
  | 'active'
  | 'approved'
  | 'cancelled'
  | 'draft'
  | 'pending_review'
  | 'rejected'
  | 'rolled_back'
  | 'superseded';

export type ConfigReleaseStrategy = 'all';

export type ConfigReviewDecision = 'approved' | 'rejected';

export interface PageQuery {
  page?: number;
  pageSize?: number;
}

/** 前端 page/pageSize → 后端 page/page_size，并夹紧上限 100。 */
export function toPageQuery<T extends PageQuery>(
  query: T,
): Record<string, unknown> {
  const { page = 1, pageSize = 20 } = query;
  const rest: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(query)) {
    if (key !== 'page' && key !== 'pageSize') {
      rest[key] = value;
    }
  }
  return {
    ...rest,
    page,
    page_size: Math.min(Math.max(pageSize, 1), 100),
  };
}

export interface ConfigNamespace {
  key_count: number;
  namespace: string;
}

export interface ConfigNamespaceListData {
  items: ConfigNamespace[];
  total: number;
}

export interface ConfigItem {
  created_at: string;
  created_by: string;
  current_version: number;
  description: string;
  env: ConfigEnv;
  expires_at?: null | string;
  id: number;
  key: string;
  namespace: string;
  owner: string;
  sensitive: boolean;
  status: ConfigItemStatus;
  updated_at: string;
  updated_by: string;
  value?: null | string;
  value_hash?: string;
  value_type: ConfigValueType;
  version: number;
}

export interface ConfigItemListData {
  items: ConfigItem[];
  page: number;
  page_size: number;
  total: number;
}

export interface ConfigItemCreatePayload {
  change_summary?: string;
  description?: string;
  env: ConfigEnv;
  expires_at?: null | string;
  key: string;
  namespace: string;
  owner?: string;
  status?: ConfigItemStatus;
  value?: null | string;
}

export interface ConfigItemUpdatePayload {
  change_summary?: string;
  expected_version?: number;
  value: string;
}

export interface ConfigItemVersion {
  change_summary: string;
  created_at: string;
  created_by: string;
  id: number;
  item_id: number;
  value?: null | string;
  value_hash: string;
  version: number;
}

export interface ConfigItemVersionListData {
  items: ConfigItemVersion[];
  page: number;
  page_size: number;
  total: number;
}

export interface ConfigReleaseItem {
  config_key: string;
  item_id: number;
  item_version: number;
  value_hash: string;
}

export interface ConfigReleaseReview {
  comment: string;
  created_at: string;
  decision: ConfigReviewDecision;
  id: number;
  release_id: number;
  reviewer: string;
}

export interface ConfigRelease {
  approved_at?: null | string;
  approved_by?: string;
  based_on_release_id?: null | number;
  created_at: string;
  created_by: string;
  description: string;
  env: ConfigEnv;
  id: number;
  items?: ConfigReleaseItem[];
  name: string;
  namespace: string;
  published_at?: null | string;
  release_no: number;
  reviews?: ConfigReleaseReview[];
  snapshot_hash: string;
  snapshot_key_id?: string;
  snapshot_signature?: string;
  snapshot_size: number;
  status: ConfigReleaseStatus;
  strategy: string;
  submitted_by?: string;
  target_rule_json?: string;
  updated_at: string;
}

export interface ConfigReleaseListData {
  items: ConfigRelease[];
  page: number;
  page_size: number;
  total: number;
}

export interface ConfigReleaseEntry {
  item_id: number;
  item_version: number;
}

export interface ConfigReleaseCreatePayload {
  based_on_release_id?: null | number;
  description?: string;
  entries: ConfigReleaseEntry[];
  env: ConfigEnv;
  name: string;
  namespace: string;
  strategy?: ConfigReleaseStrategy;
  target_rule_json?: string;
}

export interface ConfigReleaseApprovePayload {
  comment?: string;
  decision: ConfigReviewDecision;
}

export interface ConfigReleasePublishPayload {
  expected_pointer_version?: null | number;
}

export interface ConfigReleaseRollbackPayload {
  comment?: string;
  expected_pointer_version?: null | number;
}

export const CONFIG_ENV_OPTIONS: { label: string; value: ConfigEnv }[] = [
  { label: 'local', value: 'local' },
  { label: 'dev', value: 'dev' },
  { label: 'test', value: 'test' },
  { label: 'staging', value: 'staging' },
  { label: 'prod', value: 'prod' },
];

export const CONFIG_ITEM_STATUS_OPTIONS: {
  label: string;
  value: ConfigItemStatus;
}[] = [
  { label: '草稿', value: 'draft' },
  { label: '生效', value: 'active' },
  { label: '废弃', value: 'deprecated' },
  { label: '归档', value: 'archived' },
];

export const CONFIG_RELEASE_STATUS_OPTIONS: {
  label: string;
  value: ConfigReleaseStatus;
}[] = [
  { label: '草稿', value: 'draft' },
  { label: '待审批', value: 'pending_review' },
  { label: '已通过', value: 'approved' },
  { label: '已生效', value: 'active' },
  { label: '已替代', value: 'superseded' },
  { label: '已回滚', value: 'rolled_back' },
  { label: '已驳回', value: 'rejected' },
  { label: '已取消', value: 'cancelled' },
];
