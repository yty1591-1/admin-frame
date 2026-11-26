import type { PageQuery } from '../configcenter/types';

export type TenantStatus = 'active' | 'pending' | 'suspended' | 'terminated';

export interface Tenant {
  code: string;
  contact_email?: string;
  contact_name?: string;
  created_at: string;
  external_id?: string;
  id: string;
  industry?: string;
  locale?: string;
  name: string;
  plan_id?: string;
  status: TenantStatus;
  status_reason?: string;
  timezone?: string;
  updated_at: string;
  version: number;
}

export interface TenantListData {
  items: Tenant[];
  page: number;
  page_size: number;
  total: number;
}

export interface TenantListParams extends PageQuery {
  keyword?: string;
  status?: TenantStatus;
}
