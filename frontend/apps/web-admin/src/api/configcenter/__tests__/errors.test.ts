import { describe, expect, it } from 'vitest';

import { getConfigErrorMessage } from '../errors';

describe('getConfigErrorMessage', () => {
  it('优先使用后端响应里的 message', () => {
    const error = {
      message: 'Request failed with status code 400',
      response: {
        data: { code: 40_003, message: '配置版本不存在' },
        status: 400,
      },
    };
    expect(getConfigErrorMessage(error)).toBe('配置版本不存在');
  });

  it('后端没有 message 时回退到 Error.message', () => {
    expect(getConfigErrorMessage(new Error('network down'))).toBe(
      'network down',
    );
  });

  it('两者都缺失时使用默认兜底文案', () => {
    expect(getConfigErrorMessage(undefined)).toBe('操作失败');
    expect(getConfigErrorMessage(null, '自定义兜底')).toBe('自定义兜底');
  });

  it('response.data 为空对象时回退到 Error.message', () => {
    expect(
      getConfigErrorMessage({ message: 'boom', response: { data: {} } }),
    ).toBe('boom');
  });
});
