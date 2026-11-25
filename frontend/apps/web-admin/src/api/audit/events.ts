import type { AuditEventListData, AuditEventListParams } from './types';

import { requestClient } from '#/api/request';

import { toPageQuery } from '../configcenter/types';

/** 查询审计事件；from/to 为 RFC3339 且时间窗必填，分页上限 100。 */
export async function listAuditEventsApi(
  params: AuditEventListParams,
): Promise<AuditEventListData> {
  return requestClient.get<AuditEventListData>('/audit/events', {
    params: toPageQuery(params),
  });
}
