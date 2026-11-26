/**
 * 登录后回跳地址白名单（FE-S-07）。
 *
 * 只接受站内相对路径：必须是 `/` 开头、同源、无协议与反斜杠；
 * 畸形 `decodeURIComponent`（如 `%E0%A4%A`）直接退回兜底地址，绝不抛错。
 */

const FALLBACK_ORIGIN = 'http://localhost';

function currentOrigin(): string {
  const origin = globalThis.location?.origin;
  return origin && origin !== 'null' ? origin : FALLBACK_ORIGIN;
}

function hasControlCharacter(value: string): boolean {
  return [...value].some((char) => {
    const code = char.codePointAt(0) ?? 0;
    return code < 32 || code === 127;
  });
}

/**
 * 解析 `redirect` 查询参数为可安全 push 的站内路径。
 *
 * @param target 原始值（通常已 `encodeURIComponent`；也接受数组）
 * @param fallback 非法/缺失时的兜底路径（同样应为站内路径）
 * @param origin 同源基准，默认取 `location.origin`；单测可注入
 */
export function resolveSafeRedirect(
  target: unknown,
  fallback: string,
  origin: string = currentOrigin(),
): string {
  const raw = Array.isArray(target) ? target[0] : target;
  if (typeof raw !== 'string' || raw.trim() === '') {
    return fallback;
  }

  let decoded: string;
  try {
    decoded = decodeURIComponent(raw.trim());
  } catch {
    // 畸形转义（如 %E0%A4%A）会让 decodeURIComponent 抛错，导致白屏。
    return fallback;
  }

  if (
    !decoded.startsWith('/') ||
    decoded.startsWith('//') ||
    decoded.includes('\\') ||
    hasControlCharacter(decoded)
  ) {
    return fallback;
  }

  try {
    const resolved = new URL(decoded, origin);
    if (resolved.origin !== origin) {
      return fallback;
    }
    return `${resolved.pathname}${resolved.search}${resolved.hash}`;
  } catch {
    return fallback;
  }
}
