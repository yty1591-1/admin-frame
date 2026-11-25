import { describe, expect, it } from 'vitest';

import { toPageQuery as configToPageQuery } from '../configcenter/types';
import { toPageQuery as iamToPageQuery } from '../iam/types';

const mappers = [
  ['iam', iamToPageQuery],
  ['configcenter', configToPageQuery],
] as const;

describe.each(mappers)('%s toPageQuery', (_name, toPageQuery) => {
  it('默认 page=1、page_size=20', () => {
    expect(toPageQuery({})).toEqual({ page: 1, page_size: 20 });
  });

  it('page_size 上限夹到 100', () => {
    expect(toPageQuery({ page: 2, pageSize: 500 })).toEqual({
      page: 2,
      page_size: 100,
    });
  });

  it('page_size 下限夹到 1', () => {
    expect(toPageQuery({ pageSize: 0 })).toEqual({ page: 1, page_size: 1 });
    expect(toPageQuery({ pageSize: -5 })).toEqual({ page: 1, page_size: 1 });
  });

  it('透传业务筛选字段且不重复 page/pageSize', () => {
    const result = toPageQuery({ keyword: 'alice', page: 1, pageSize: 10 });
    expect(result).toEqual({ keyword: 'alice', page: 1, page_size: 10 });
    expect(result).not.toHaveProperty('pageSize');
  });
});
