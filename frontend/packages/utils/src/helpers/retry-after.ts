/**
 * 解析 `Retry-After` 响应头为秒数（FE-T-01）。
 *
 * 支持两种合法形式：
 * - delta-seconds（如 `"30"`）；
 * - HTTP-date（如 `"Wed, 21 Oct 2026 07:28:00 GMT"`）。
 * 非法值返回 0，调用方据此决定是否启用倒计时。
 */
export function parseRetryAfterSeconds(
  headers?: null | Record<string, unknown>,
): number {
  const raw = headers?.['retry-after'] ?? headers?.['Retry-After'];
  if (typeof raw !== 'number' && typeof raw !== 'string') {
    return 0;
  }

  const seconds = Number(raw);
  if (Number.isFinite(seconds)) {
    return seconds > 0 ? Math.ceil(seconds) : 0;
  }

  const timestamp = Date.parse(String(raw));
  if (Number.isNaN(timestamp)) {
    return 0;
  }
  return Math.max(0, Math.ceil((timestamp - Date.now()) / 1000));
}
