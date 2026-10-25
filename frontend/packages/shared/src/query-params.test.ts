import { describe, expect, it } from 'vitest';
import { defaultPageSize, maxPageSize } from './pagination';
import { normalizeQueryParams, parsePage, parsePageSize } from './query-params';

describe('normalizeQueryParams', () => {
  it('去除 undefined/null 并排序 key', () => {
    expect(
      normalizeQueryParams({ keyword: 'iam', page: 2, channel: undefined, status: null }),
    ).toEqual({ keyword: 'iam', page: 2 });
  });

  it('相同参数产生稳定顺序', () => {
    const first = normalizeQueryParams({ page: 1, pageSize: 20, keyword: 'x' });
    const second = normalizeQueryParams({ keyword: 'x', pageSize: 20, page: 1 });
    expect(Object.keys(first)).toEqual(Object.keys(second));
  });
});

describe('parsePage', () => {
  it('非法值回落 1', () => {
    expect(parsePage(null)).toBe(1);
    expect(parsePage('0')).toBe(1);
    expect(parsePage('-2')).toBe(1);
    expect(parsePage('abc')).toBe(1);
  });

  it('保留合法页码', () => {
    expect(parsePage('3')).toBe(3);
  });
});

describe('parsePageSize', () => {
  it('缺省或非法回落默认值', () => {
    expect(parsePageSize(null)).toBe(defaultPageSize);
    expect(parsePageSize('abc')).toBe(defaultPageSize);
    expect(parsePageSize('0')).toBe(defaultPageSize);
  });

  it('上限收敛为 100', () => {
    expect(parsePageSize('500')).toBe(maxPageSize);
    expect(parsePageSize('50')).toBe(50);
  });
});
