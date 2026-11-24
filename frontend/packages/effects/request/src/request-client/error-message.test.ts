import { describe, expect, it } from 'vitest';

import {
  errorMessageKey,
  resolveRequestErrorMessage,
  sanitizeTraceId,
} from './error-message';

/** 注入式翻译：返回 `key` 本身，便于断言命中了哪条映射。 */
const translate = (key: string, params: string[] = []): string => {
  if (key === 'ui.fallback.http.errorWithTrace') {
    return `${params[0]} (trace: ${params[1]})`;
  }
  return key;
};

describe('errorMessageKey', () => {
  it('maps known business codes to locale keys', () => {
    expect(errorMessageKey(40_100)).toBe('ui.fallback.http.unauthorized');
    expect(errorMessageKey(40_900)).toBe('ui.fallback.http.conflict');
    expect(errorMessageKey(42_900)).toBe('ui.fallback.http.tooManyRequests');
    expect(errorMessageKey(50_000)).toBe(
      'ui.fallback.http.internalServerError',
    );
    expect(errorMessageKey(50_351)).toBe('ui.fallback.http.notInitialized');
  });

  it('returns undefined for unknown or non-numeric codes', () => {
    expect(errorMessageKey(49_999)).toBeUndefined();
    expect(errorMessageKey(undefined)).toBeUndefined();
    expect(errorMessageKey('40100')).toBeUndefined();
  });
});

describe('sanitizeTraceId', () => {
  it('removes control characters and trims', () => {
    expect(sanitizeTraceId('  ab\u0000c\u0007\t ')).toBe('abc');
  });

  it('truncates to 64 visible characters', () => {
    expect(sanitizeTraceId('x'.repeat(100))).toHaveLength(64);
  });

  it('returns empty string for non-string input', () => {
    expect(sanitizeTraceId(undefined)).toBe('');
  });
});

describe('resolveRequestErrorMessage', () => {
  it('uses the local mapping for a known code', () => {
    const message = resolveRequestErrorMessage(
      { code: 42_900, message: 'slow down' },
      'fallback',
      translate,
    );
    expect(message).toBe('ui.fallback.http.tooManyRequests');
  });

  it('falls back to the interceptor message for unknown codes', () => {
    expect(
      resolveRequestErrorMessage({ code: 49_999 }, 'fallback', translate),
    ).toBe('fallback');
  });

  it('falls back for empty payloads', () => {
    expect(resolveRequestErrorMessage(undefined, 'fallback', translate)).toBe(
      'fallback',
    );
    expect(resolveRequestErrorMessage({}, 'fallback', translate)).toBe(
      'fallback',
    );
  });

  it('never echoes the server message', () => {
    const message = resolveRequestErrorMessage(
      { code: 40_000, message: '<script>alert(1)</script>' },
      'fallback',
      translate,
    );
    expect(message).toBe('ui.fallback.http.badRequest');
    expect(message).not.toContain('script');
  });

  it('appends a sanitized trace id when present', () => {
    const message = resolveRequestErrorMessage(
      { code: 50_000, trace_id: 'abc\u0000def' },
      'fallback',
      translate,
    );
    expect(message).toBe(
      'ui.fallback.http.internalServerError (trace: abcdef)',
    );
  });

  it('keeps the fallback when trace_id is empty', () => {
    expect(
      resolveRequestErrorMessage(
        { code: 40_400, trace_id: '' },
        'fallback',
        translate,
      ),
    ).toBe('ui.fallback.http.notFound');
  });
});
