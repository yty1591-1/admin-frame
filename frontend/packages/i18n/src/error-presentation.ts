import type { MessageKey } from './messages/zh-CN';

export type ErrorAction = 'inline' | 'toast' | 'page' | 'approval' | 'login';

export type ErrorPresentation = {
  /** 无业务码时为 0（network/timeout/unknown）。 */
  code: number;
  messageKey: MessageKey;
  action: ErrorAction;
  retryable: boolean;
  traceId: string | undefined;
};

type Mapping = {
  messageKey: MessageKey;
  action: ErrorAction;
  retryable: boolean;
};

const VALIDATION: Mapping = { messageKey: 'error.validation', action: 'inline', retryable: false };
const UNAUTHORIZED: Mapping = {
  messageKey: 'error.unauthorized',
  action: 'login',
  retryable: false,
};
const FORBIDDEN: Mapping = { messageKey: 'error.forbidden', action: 'page', retryable: false };
const NOT_FOUND: Mapping = { messageKey: 'error.notFound', action: 'inline', retryable: false };
const APPROVAL: Mapping = {
  messageKey: 'error.approvalRequired',
  action: 'approval',
  retryable: false,
};
const RATE_LIMITED: Mapping = { messageKey: 'error.rateLimited', action: 'toast', retryable: true };
const SERVER: Mapping = { messageKey: 'error.server', action: 'inline', retryable: true };
const UNAVAILABLE: Mapping = { messageKey: 'error.unavailable', action: 'page', retryable: true };

/** 业务码 → 用户文案 / 展示位置（与 docs/frontend-s3-plan.md §5.1 表一致）。 */
const CODE_MAP: Readonly<Record<number, Mapping>> = {
  40000: VALIDATION,
  40100: UNAUTHORIZED,
  40300: FORBIDDEN,
  40400: NOT_FOUND,
  40900: APPROVAL,
  42900: RATE_LIMITED,
  50000: SERVER,
  50300: UNAVAILABLE,
};

/** 无业务码时按 kind 兜底；`conflict` 不得误判为审批。 */
const KIND_MAP: Readonly<Record<string, Mapping>> = {
  validation: VALIDATION,
  unauthorized: UNAUTHORIZED,
  forbidden: FORBIDDEN,
  not_found: NOT_FOUND,
  rate_limited: RATE_LIMITED,
  server: SERVER,
  unavailable: UNAVAILABLE,
  network: { messageKey: 'error.network', action: 'toast', retryable: true },
  timeout: { messageKey: 'error.timeout', action: 'toast', retryable: true },
};

const UNKNOWN: Mapping = { messageKey: 'error.unknown', action: 'toast', retryable: false };

const KIND_VALUES = new Set([
  'network',
  'timeout',
  'unauthorized',
  'forbidden',
  'validation',
  'not_found',
  'conflict',
  'rate_limited',
  'server',
  'unavailable',
  'unknown',
]);

const TRACE_ID = /^[A-Za-z0-9._:-]{1,128}$/;
const MAX_CAUSE_DEPTH = 3;

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : null;
}

/** 沿 cause 链结构化读取字段；忽略 getter 异常与循环引用。 */
function readField(error: unknown, key: string): unknown {
  let current: unknown = error;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH; depth += 1) {
    const record = asRecord(current);
    if (record === null) {
      return undefined;
    }
    let value: unknown;
    let cause: unknown;
    try {
      value = record[key];
      cause = record['cause'];
    } catch {
      return undefined;
    }
    if (value !== undefined && value !== null) {
      return value;
    }
    if (cause === undefined || cause === null || cause === current) {
      return undefined;
    }
    current = cause;
  }
  return undefined;
}

function readCode(error: unknown): number {
  const raw = readField(error, 'code');
  return typeof raw === 'number' && Number.isInteger(raw) && raw >= 10000 && raw <= 99999 ? raw : 0;
}

function readKind(error: unknown): string | undefined {
  const raw = readField(error, 'kind');
  return typeof raw === 'string' && KIND_VALUES.has(raw) ? raw : undefined;
}

function readTraceId(error: unknown): string | undefined {
  const raw = readField(error, 'traceId');
  return typeof raw === 'string' && TRACE_ID.test(raw) ? raw : undefined;
}

/**
 * 把任意错误归类为「用户文案 + 建议动作」（S3 / §3.1）。
 * 只读 `code/kind/traceId`，不 import api-client，不展示后端 message。
 * 优先级：显式 code → kind → unknown。
 */
export function describeError(error: unknown): ErrorPresentation {
  const code = readCode(error);
  const kind = readKind(error);
  const mapping =
    (code !== 0 ? CODE_MAP[code] : undefined) ??
    (kind === undefined ? undefined : KIND_MAP[kind]) ??
    UNKNOWN;

  return {
    code,
    messageKey: mapping.messageKey,
    action: mapping.action,
    retryable: mapping.retryable,
    traceId: readTraceId(error),
  };
}
