import { describe, expect, it } from 'vitest';
import { AppError, codeFromStatus, isAppError, kindFromCode, kindFromStatus } from './app-error';

describe('app-error', () => {
  it('业务码映射到稳定分类', () => {
    expect(kindFromCode(40100)).toBe('unauthorized');
    expect(kindFromCode(40900)).toBe('conflict');
    expect(kindFromCode(99999)).toBe('unknown');
  });

  it('HTTP 状态兜底映射', () => {
    expect(kindFromStatus(404)).toBe('not_found');
    expect(kindFromStatus(502)).toBe('server');
    expect(kindFromStatus(418)).toBe('unknown');
    expect(codeFromStatus(429)).toBe(42900);
    expect(codeFromStatus(418)).toBe(50000);
  });

  it('保留 trace_id 与字段错误且可识别', () => {
    const error = new AppError({
      kind: 'validation',
      code: 40000,
      message: 'invalid',
      traceId: 'trace-1',
      fieldErrors: { subject: '必填' },
    });
    expect(isAppError(error)).toBe(true);
    expect(error.traceId).toBe('trace-1');
    expect(error.fieldErrors).toEqual({ subject: '必填' });
  });
});
