import type { PageQuery } from '../configcenter/types';

/** 审计事件查询参数；后端要求 from/to 为 RFC3339 且时间窗必填。 */
export interface AuditEventListParams extends PageQuery {
  /** 起始时间（RFC3339）。 */
  from: string;
  /** 结束时间（RFC3339）。 */
  to: string;
  actor_id?: string;
  action?: string;
  outcome?: string;
  resource_type?: string;
  resource_id?: string;
  trace_id?: string;
}

/** 审计事件（AUD-04 字段全集）。 */
export interface AuditEvent {
  event_id: string;
  action: string;
  actor_id: string;
  actor_type?: string;
  channel: string;
  merchant_id?: string;
  resource_type: string;
  resource_id: string;
  outcome: string;
  occurred_at: string;
  trace_id: string;
  service: string;
  reason?: string;
  attrs?: Record<string, string>;
}

/** 审计事件分页响应。 */
export interface AuditEventListData {
  items: AuditEvent[];
  total: number;
}
