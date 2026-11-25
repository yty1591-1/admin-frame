import { describe, expect, it, vi } from 'vitest';

import { parseRetryAfterSeconds } from '../retry-after';

describe('parseRetryAfterSeconds', () => {
  it('解析 delta-seconds（大小写头名均可）', () => {
    expect(parseRetryAfterSeconds({ 'retry-after': '30' })).toBe(30);
    expect(parseRetryAfterSeconds({ 'Retry-After': 5 })).toBe(5);
  });

  it('小于等于 0 或缺失时返回 0', () => {
    expect(parseRetryAfterSeconds({ 'retry-after': '0' })).toBe(0);
    expect(parseRetryAfterSeconds({ 'retry-after': '-3' })).toBe(0);
    expect(parseRetryAfterSeconds({})).toBe(0);
    expect(parseRetryAfterSeconds(undefined)).toBe(0);
  });

  it('解析 HTTP-date 并按剩余时间取整', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-24T12:00:00Z'));
    const future = new Date('2026-11-24T12:01:30Z').toUTCString();
    expect(parseRetryAfterSeconds({ 'retry-after': future })).toBe(90);
    vi.useRealTimers();
  });

  it('非法值返回 0', () => {
    expect(parseRetryAfterSeconds({ 'retry-after': 'not-a-date' })).toBe(0);
    expect(parseRetryAfterSeconds({ 'retry-after': {} })).toBe(0);
  });
});
