import { describe, expect, it } from 'vitest';

import {
  ensureIdempotencyHeader,
  isIdempotentMethod,
  newIdempotencyKey,
} from '../idempotency';

describe('isIdempotentMethod', () => {
  it('把 patch/post/put 视为写方法（大小写不敏感）', () => {
    expect(isIdempotentMethod('post')).toBe(true);
    expect(isIdempotentMethod('PATCH')).toBe(true);
    expect(isIdempotentMethod('Put')).toBe(true);
  });

  it('把 get/delete/head 视为安全方法', () => {
    expect(isIdempotentMethod('get')).toBe(false);
    expect(isIdempotentMethod('delete')).toBe(false);
    expect(isIdempotentMethod('head')).toBe(false);
    expect(isIdempotentMethod()).toBe(false);
  });
});

describe('newIdempotencyKey', () => {
  it('生成可打印 ASCII 且长度不超过 255 的键', () => {
    const key = newIdempotencyKey();
    expect(key).toMatch(/^[\w-]+$/);
    expect(key.length).toBeLessThanOrEqual(255);
  });

  it('每次生成的新键互不相同', () => {
    const keys = new Set(Array.from({ length: 20 }, () => newIdempotencyKey()));
    expect(keys.size).toBe(20);
  });
});

describe('ensureIdempotencyHeader', () => {
  it('写方法且缺键时注入 Idempotency-Key', () => {
    const headers: Record<string, string> = {};
    ensureIdempotencyHeader(headers, 'post');
    expect(headers['Idempotency-Key']).toMatch(/^[\w-]+$/);
  });

  it('已有键时不覆盖（小写键也视为已有）', () => {
    const headers: Record<string, string> = { 'idempotency-key': 'keep-me' };
    ensureIdempotencyHeader(headers, 'patch');
    expect(headers['idempotency-key']).toBe('keep-me');
    expect(headers['Idempotency-Key']).toBeUndefined();
  });

  it('支持 Headers 实例', () => {
    const headers = new Headers();
    ensureIdempotencyHeader(headers, 'put');
    expect(headers.get('Idempotency-Key')).toMatch(/^[\w-]+$/);
  });

  it('安全方法与空 headers 不注入也不抛错', () => {
    const headers: Record<string, string> = {};
    ensureIdempotencyHeader(headers, 'get');
    expect(headers).toEqual({});

    expect(() => ensureIdempotencyHeader(null, 'post')).not.toThrow();
    expect(() => ensureIdempotencyHeader(undefined, 'post')).not.toThrow();
  });
});
