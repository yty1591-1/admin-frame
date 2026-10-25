/** 统一错误模型（前端架构 §9.3）。 */
export type AppErrorKind =
  | 'network'
  | 'timeout'
  | 'unauthorized'
  | 'forbidden'
  | 'validation'
  | 'not_found'
  | 'conflict'
  | 'rate_limited'
  | 'server'
  | 'unavailable'
  | 'unknown';

const CODE_KIND: Readonly<Record<number, AppErrorKind>> = {
  40000: 'validation',
  40100: 'unauthorized',
  40300: 'forbidden',
  40400: 'not_found',
  40900: 'conflict',
  42900: 'rate_limited',
  50000: 'server',
  50300: 'unavailable',
};

const STATUS_KIND: Readonly<Record<number, AppErrorKind>> = {
  400: 'validation',
  401: 'unauthorized',
  403: 'forbidden',
  404: 'not_found',
  409: 'conflict',
  422: 'validation',
  429: 'rate_limited',
  503: 'unavailable',
};

export function kindFromCode(code: number): AppErrorKind {
  return CODE_KIND[code] ?? 'unknown';
}

export function kindFromStatus(status: number): AppErrorKind {
  if (STATUS_KIND[status]) {
    return STATUS_KIND[status];
  }
  return status >= 500 ? 'server' : 'unknown';
}

/** 非信封错误时按 HTTP 状态反推稳定业务码（只用于展示与分类）。 */
export function codeFromStatus(status: number): number {
  switch (status) {
    case 400:
      return 40000;
    case 401:
      return 40100;
    case 403:
      return 40300;
    case 404:
      return 40400;
    case 409:
      return 40900;
    case 429:
      return 42900;
    case 503:
      return 50300;
    default:
      return 50000;
  }
}

export type AppErrorInit = {
  kind: AppErrorKind;
  code: number;
  message: string;
  httpStatus?: number | undefined;
  traceId?: string | undefined;
  fieldErrors?: Record<string, string> | undefined;
  cause?: unknown;
};

export class AppError extends Error {
  readonly kind: AppErrorKind;
  readonly code: number;
  readonly httpStatus: number | undefined;
  readonly traceId: string | undefined;
  readonly fieldErrors: Record<string, string> | undefined;

  constructor(init: AppErrorInit) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause });
    this.name = 'AppError';
    this.kind = init.kind;
    this.code = init.code;
    this.httpStatus = init.httpStatus;
    this.traceId = init.traceId;
    this.fieldErrors = init.fieldErrors;
  }
}

export function isAppError(error: unknown): error is AppError {
  return error instanceof AppError;
}

/**
 * 40900 表示 IAM 高风险写操作需转审批（§7.3、D2-05）：
 * 必须走审批引导，禁止按普通冲突自动重试。
 */
export function isApprovalRequired(error: unknown): boolean {
  return isAppError(error) && error.code === 40900;
}
