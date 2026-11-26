import { $t } from '@vben/locales';

/**
 * 通用错误呈现（FE-SEC-03）。
 *
 * 规则：
 * - 只按业务码映射本地文案，**不直出服务端 `message`**（可能是代理页、HTML 或内部细节）；
 * - 未知业务码退回 HTTP 拦截器给出的兜底文案；
 * - 服务端返回 `trace_id` 时附在文案末尾，便于用户反馈与日志检索。
 */
export interface ApiErrorBody {
  code?: number;
  message?: string;
  trace_id?: string;
}

type Translate = (key: string, params?: string[]) => string;

/** 业务码 → locale key；与 `internal/shared/apperr` 的稳定数字码一致。 */
export const ERROR_CODE_MESSAGE_KEYS: Readonly<Record<number, string>> = {
  40_000: 'ui.fallback.http.badRequest',
  40_100: 'ui.fallback.http.unauthorized',
  40_300: 'ui.fallback.http.forbidden',
  40_400: 'ui.fallback.http.notFound',
  40_900: 'ui.fallback.http.conflict',
  40_951: 'ui.fallback.http.installInProgress',
  40_952: 'ui.fallback.http.installCompleted',
  42_900: 'ui.fallback.http.tooManyRequests',
  50_000: 'ui.fallback.http.internalServerError',
  50_300: 'ui.fallback.http.serviceUnavailable',
  50_351: 'ui.fallback.http.notInitialized',
};

/** 返回业务码对应的 locale key；未知或缺失返回 `undefined`。 */
export function errorMessageKey(code: unknown): string | undefined {
  if (typeof code !== 'number') {
    return undefined;
  }
  return ERROR_CODE_MESSAGE_KEYS[code];
}

/** 清洗 trace_id：只保留可见字符，去掉控制字符并截断到 64 字符。 */
export function sanitizeTraceId(traceId?: string): string {
  if (typeof traceId !== 'string') {
    return '';
  }
  return [...traceId]
    .filter((char) => {
      const code = char.codePointAt(0) ?? 0;
      // 去掉 C0 控制字符与 DEL（0x20 以下 / 0x7F），避免日志注入。
      return code >= 32 && code !== 127;
    })
    .join('')
    .trim()
    .slice(0, 64);
}

function readBody(payload: unknown): ApiErrorBody {
  if (!payload || typeof payload !== 'object') {
    return {};
  }
  return payload as ApiErrorBody;
}

/**
 * 把响应体解析为可展示文案。
 *
 * @param payload 响应体（`{code,message,trace_id}`），通常取 `error.response.data`
 * @param fallback HTTP 拦截器给出的兜底文案（网络错误/状态码文案）
 * @param translate 翻译函数，默认 `$t`；单测注入以便断言 key
 */
export function resolveRequestErrorMessage(
  payload: unknown,
  fallback: string,
  translate: Translate = (key, params) => {
    const t = $t as unknown as (k: string, p?: unknown) => string;
    return t(key, params);
  },
): string {
  const body = readBody(payload);
  const key = errorMessageKey(body.code);
  const base = key ? translate(key) : fallback;
  const traceId = sanitizeTraceId(body.trace_id);
  return traceId
    ? translate('ui.fallback.http.errorWithTrace', [base, traceId])
    : base;
}
