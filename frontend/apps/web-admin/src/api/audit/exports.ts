import type { AuditEventListParams } from './types';

import { requestClient } from '#/api/request';

export type AuditExportStatus =
  | 'expired'
  | 'failed'
  | 'pending'
  | 'running'
  | 'succeeded';

export interface AuditExportJob {
  created_at: string;
  download_path?: string;
  error?: string;
  expires_at?: string;
  export_id: string;
  finished_at?: string;
  rows: number;
  status: AuditExportStatus;
}

function exportQuery(params: AuditEventListParams) {
  return {
    action: params.action,
    actor_id: params.actor_id,
    from: params.from,
    outcome: params.outcome,
    resource_id: params.resource_id,
    resource_type: params.resource_type,
    to: params.to,
    trace_id: params.trace_id,
  };
}

/** 创建异步审计导出任务。 */
export async function createAuditExportJobApi(
  params: AuditEventListParams,
): Promise<AuditExportJob> {
  return requestClient.post<AuditExportJob>('/audit/events/export', null, {
    params: exportQuery(params),
  });
}

/** 同步导出审计事件 CSV（async 关闭时的兜底）。 */
export async function exportAuditEventsCsvApi(
  params: AuditEventListParams,
): Promise<Blob> {
  return requestClient.get<Blob>('/audit/events/export', {
    params: {
      ...exportQuery(params),
      format: 'csv',
    },
    responseType: 'blob',
  });
}

/** 查询异步导出任务状态。 */
export async function getAuditExportJobApi(
  id: string,
): Promise<AuditExportJob> {
  return requestClient.get<AuditExportJob>(`/audit/exports/${id}`);
}
