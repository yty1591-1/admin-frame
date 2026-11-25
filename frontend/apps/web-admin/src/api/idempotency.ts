/**
 * 平台幂等键（API-11 / pkg/idempotency）。
 *
 * prod admin/biz 写请求 `idempotency.required=true` 时缺键返回 40000。
 * 键须为可打印 ASCII、无空格，长度 ≤ 255。
 */

const IDEMPOTENT_METHODS = new Set(['patch', 'post', 'put']);

/** 生成客户端幂等键（每次写操作一份；同一请求重试须复用）。 */
export function newIdempotencyKey(): string {
  if (
    typeof crypto !== 'undefined' &&
    typeof crypto.randomUUID === 'function'
  ) {
    return crypto.randomUUID();
  }
  return `web-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

export function isIdempotentMethod(method?: string): boolean {
  return IDEMPOTENT_METHODS.has((method || 'get').toLowerCase());
}

function readHeader(headers: unknown, name: string): string {
  if (!headers || typeof headers !== 'object') {
    return '';
  }
  const h = headers as {
    get?: (key: string) => unknown;
    [key: string]: unknown;
  };
  if (typeof h.get === 'function') {
    const v = h.get(name);
    return typeof v === 'string' ? v.trim() : '';
  }
  const direct = h[name] ?? h[name.toLowerCase()];
  return typeof direct === 'string' ? direct.trim() : '';
}

function writeHeader(headers: unknown, name: string, value: string): void {
  if (!headers || typeof headers !== 'object') {
    return;
  }
  const h = headers as {
    set?: (key: string, value: string) => void;
    [key: string]: unknown;
  };
  if (typeof h.set === 'function') {
    h.set(name, value);
    return;
  }
  h[name] = value;
}

/** 若写方法且尚未设置，则注入 Idempotency-Key（支持 Headers / AxiosHeaders / 普通对象）。 */
export function ensureIdempotencyHeader(
  headers: unknown,
  method?: string,
): void {
  if (!isIdempotentMethod(method) || !headers) {
    return;
  }
  if (readHeader(headers, 'Idempotency-Key')) {
    return;
  }
  writeHeader(headers, 'Idempotency-Key', newIdempotencyKey());
}
