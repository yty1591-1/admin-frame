import type {
  NotificationListData,
  NotificationListParams,
  NotificationRequest,
  NotificationTemplate,
  NotificationTemplateListData,
} from './types';

import { requestClient } from '#/api/request';

export * from './types';

export interface NotificationSendPayload {
  channels: Array<'email' | 'push' | 'sms'>;
  email?: string;
  locale?: string;
  phone?: string;
  push_token?: string;
  scene: string;
  scene_class?: 'marketing' | 'operational' | 'transactional';
  template_key: string;
  tenant_id?: string;
  user_id?: string;
}

export interface NotificationAccepted {
  id: string;
  status?: string;
}

export interface NotificationTemplateUpsertPayload {
  body: string;
  channel: 'email' | 'push' | 'sms';
  key: string;
  locale?: string;
  subject?: string;
}

/** 查询通知单列表；后端使用 limit/offset。 */
export async function listNotificationsApi(
  params: NotificationListParams = {},
): Promise<NotificationListData> {
  const page = params.page ?? 1;
  const pageSize = Math.min(Math.max(params.pageSize ?? 20, 1), 100);
  return requestClient.get<NotificationListData>('/notifications', {
    params: {
      limit: pageSize,
      offset: (page - 1) * pageSize,
      scene: params.scene,
      status: params.status,
      tenant_id: params.tenant_id,
    },
  });
}

/** 查询通知单详情。 */
export async function getNotificationApi(
  id: string,
): Promise<NotificationRequest> {
  return requestClient.get<NotificationRequest>(`/notifications/${id}`);
}

/** 代发通知。 */
export async function sendNotificationApi(
  payload: NotificationSendPayload,
): Promise<NotificationAccepted> {
  return requestClient.post<NotificationAccepted>('/notifications', payload);
}

/** 按模板键查询模板版本历史。 */
export async function listNotificationTemplatesApi(
  key: string,
): Promise<NotificationTemplateListData> {
  return requestClient.get<NotificationTemplateListData>(
    '/notification-templates',
    {
      params: { key },
    },
  );
}

/** 登记通知模板（追加版本）。 */
export async function upsertNotificationTemplateApi(
  payload: NotificationTemplateUpsertPayload,
): Promise<NotificationTemplate> {
  return requestClient.post<NotificationTemplate>(
    '/notification-templates',
    payload,
  );
}
