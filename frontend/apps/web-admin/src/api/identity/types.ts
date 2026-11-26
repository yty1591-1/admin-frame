import type { PageQuery } from '../configcenter/types';

export type AccountStatus = 'active' | 'disabled' | 'locked' | 'pending';

export interface AccountSummary {
  channel: string;
  created_at: string;
  deleted_at?: string;
  id: string;
  locked_until?: string;
  merchant_id?: string;
  status: AccountStatus;
  subject: string;
  token_version: number;
  updated_at: string;
}

export interface AccountListData {
  items: AccountSummary[];
  total: number;
}

export interface AccountListParams extends PageQuery {
  keyword?: string;
  status?: AccountStatus;
  subject?: string;
}

export type M2MClientStatus = 'active' | 'deleted' | 'disabled';

export interface M2MClient {
  allowed_channels: string[];
  allowed_scopes: string[];
  client_id: string;
  created_at?: string;
  id: string;
  name: string;
  owner?: string;
  rotation_due_at?: string;
  status: M2MClientStatus;
  token_version: number;
  updated_at?: string;
}

export interface M2MClientListData {
  items: M2MClient[];
  page: number;
  size: number;
  total: number;
}

export interface M2MClientListParams extends PageQuery {
  channel?: 'admin' | 'biz' | 'c';
  status?: M2MClientStatus;
}

export type ScimTokenStatus = 'active' | 'retiring' | 'revoked';

export interface ScimToken {
  created_at: string;
  expires_at?: string;
  fingerprint: string;
  id: string;
  provider?: string;
  revoked_at?: string;
  status: ScimTokenStatus;
}

export interface ScimTokenListData {
  items: ScimToken[];
  total: number;
}

export interface ScimGroupMapping {
  display_name: string;
  external_id: string;
  id: string;
  mapped_role: string;
  member_count: number;
  version: number;
}

export interface ScimGroupListData {
  items: ScimGroupMapping[];
  total: number;
}
