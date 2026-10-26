/** trace_id 结构化读取与前端兜底生成（S3 / §12.2）。 */

export type TraceReadResult = {
  traceId: string;
  /** true 表示前端生成，避免与后端链路混淆。 */
  clientGenerated: boolean;
};

const TRACE_ID = /^[A-Za-z0-9._:-]{1,128}$/;
const MAX_CAUSE_DEPTH = 3;

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : null;
}

/** 沿 cause 链读取合法 traceId；不解析堆栈、不展示 message。 */
export function getTraceId(error: unknown): string | undefined {
  let current: unknown = error;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH; depth += 1) {
    const record = asRecord(current);
    if (record === null) {
      return undefined;
    }
    let traceId: unknown;
    let cause: unknown;
    try {
      traceId = record['traceId'];
      cause = record['cause'];
    } catch {
      return undefined;
    }
    if (typeof traceId === 'string' && TRACE_ID.test(traceId)) {
      return traceId;
    }
    if (cause === undefined || cause === null || cause === current) {
      return undefined;
    }
    current = cause;
  }
  return undefined;
}

function generateTraceId(): string {
  const cryptoApi = typeof crypto === 'undefined' ? undefined : crypto;
  if (cryptoApi !== undefined && typeof cryptoApi.randomUUID === 'function') {
    return cryptoApi.randomUUID();
  }
  return `client-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/** 优先使用后端 trace_id；缺失时生成 client_trace_id。 */
export function ensureTraceId(error: unknown): TraceReadResult {
  const traceId = getTraceId(error);
  return traceId === undefined
    ? { traceId: generateTraceId(), clientGenerated: true }
    : { traceId, clientGenerated: false };
}
