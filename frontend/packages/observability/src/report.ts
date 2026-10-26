import { ensureTraceId } from './trace';

/** 错误上报记录：只保留定位所需字段，不含 message、表单值与令牌。 */
export type ReportRecord = {
  error: unknown;
  traceId: string;
  clientGenerated: boolean;
  code?: number | undefined;
  kind?: string | undefined;
  route?: string | undefined;
  source?: string | undefined;
  time: string;
};

export type ReportTransport = {
  send(record: ReportRecord): void;
};

export type ReportContext = {
  route?: string | undefined;
  source?: string | undefined;
};

const MAX_PENDING = 20;
const MAX_CAUSE_DEPTH = 3;

let transport: ReportTransport | null = null;
let pending: ReportRecord[] = [];

/** 结构化读取 code/kind，不依赖 api-client，避免可观测包反向耦合。 */
function readErrorField(error: unknown, key: 'code' | 'kind'): unknown {
  let current: unknown = error;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH; depth += 1) {
    if (typeof current !== 'object' || current === null) {
      return undefined;
    }
    try {
      const record = current as Record<string, unknown>;
      const value = record[key];
      if (value !== undefined && value !== null) {
        return value;
      }
      current = record['cause'];
    } catch {
      return undefined;
    }
  }
  return undefined;
}

function send(record: ReportRecord): void {
  if (transport === null) {
    return;
  }
  try {
    transport.send(record);
  } catch {
    // 上报失败必须静默，不得影响页面渲染。
  }
}

function flush(): void {
  const queued = pending;
  pending = [];
  for (const record of queued) {
    send(record);
  }
}

/** 注入上报传输（OTel 就绪后）；传 null 停止发送并恢复缓冲。 */
export function setReportTransport(next: ReportTransport | null): void {
  transport = next;
  if (next !== null) {
    flush();
  }
}

/** 记录前端异常；transport 未就绪时缓冲最近 20 条。 */
export function reportError(error: unknown, context: ReportContext = {}): void {
  const trace = ensureTraceId(error);
  const code = readErrorField(error, 'code');
  const kind = readErrorField(error, 'kind');
  const record: ReportRecord = {
    error,
    traceId: trace.traceId,
    clientGenerated: trace.clientGenerated,
    ...(typeof code === 'number' && Number.isInteger(code) ? { code } : {}),
    ...(typeof kind === 'string' ? { kind } : {}),
    ...(context.route === undefined ? {} : { route: context.route }),
    ...(context.source === undefined ? {} : { source: context.source }),
    time: new Date().toISOString(),
  };

  if (transport === null) {
    pending.push(record);
    while (pending.length > MAX_PENDING) {
      pending.shift();
    }
    return;
  }
  send(record);
}

export function getPendingReportCount(): number {
  return pending.length;
}

/** 仅测试使用：清空传输与缓冲。 */
export function resetReports(): void {
  transport = null;
  pending = [];
}
