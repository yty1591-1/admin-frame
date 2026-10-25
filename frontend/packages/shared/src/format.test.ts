import { describe, expect, it } from 'vitest';
import { formatAmount, formatDateTime } from './format';

describe('formatDateTime', () => {
  it('按指定时区展示 UTC 时间', () => {
    expect(formatDateTime('2026-01-02T03:04:05Z', 'Asia/Shanghai')).toBe('2026-01-02 11:04:05');
  });

  it('非法时间回落占位符', () => {
    expect(formatDateTime('not-a-date')).toBe('-');
  });
});

describe('formatAmount', () => {
  it('使用统一货币格式化', () => {
    expect(formatAmount(1234.5)).toContain('1,234.50');
  });

  it('非法数值回落占位符', () => {
    expect(formatAmount(Number.POSITIVE_INFINITY)).toBe('-');
  });
});
