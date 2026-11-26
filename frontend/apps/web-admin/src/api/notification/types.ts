import type { PageQuery } from '../configcenter/types';

export type NotificationStatus =
  | 'delivered'
  | 'dispatched'
  | 'failed'
  | 'pending'
  | 'rejected'
  | 'sent'
  | 'undelivered';

export interface NotificationRequest {
  created_at: string;
  id: string;
  priority?: string;
  recipient_ref: string;
  scene: string;
  status: NotificationStatus;
  template_key: string;
  tenant_id?: string;
  updated_at?: string;
}

export interface NotificationListData {
  items: NotificationRequest[];
  limit?: number;
  offset?: number;
  total: number;
}

export interface NotificationListParams extends PageQuery {
  scene?: string;
  status?: NotificationStatus;
  tenant_id?: string;
}

export interface NotificationTemplate {
  body?: string;
  channel: string;
  created_by?: string;
  enabled: boolean;
  key: string;
  locale?: string;
  subject?: string;
  updated_at?: string;
  version: number;
}

export interface NotificationTemplateListData {
  items: NotificationTemplate[];
  total: number;
}
