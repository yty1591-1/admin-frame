import { describe, expect, it } from 'vitest';
import { ensureTraceId, getTraceId } from './trace';

function chain(depth: number, traceId?: string): Record<string, unknown> {
  const head: Record<string, unknown> = {};
  let current = head;
  for (let index = 0; index < depth; index += 1) {
    const next: Record<string, unknown> = {};
    current['cause'] = next;
    current = next;
  }
  if (traceId !== undefined) {
    current['traceId'] = traceId;
  }
  return head;
}

describe('getTraceId', () => {
  it('直接读取合法 traceId', () => {
    expect(getTraceId({ traceId: 'trace-1' })).toBe('trace-1');
  });

  it('忽略不合法的 traceId 与原始值', () => {
    expect(getTraceId({ traceId: 'bad trace!' })).toBeUndefined();
    expect(getTraceId({ traceId: 42 })).toBeUndefined();
    expect(getTraceId(null)).toBeUndefined();
    expect(getTraceId('trace-1')).toBeUndefined();
  });

  it('沿 cause 链最多读取三层', () => {
    expect(getTraceId(chain(2, 'trace-deep'))).toBe('trace-deep');
    expect(getTraceId(chain(3, 'trace-too-deep'))).toBeUndefined();
  });

  it('cause 自引用与读取异常时安全返回', () => {
    const cyclic: Record<string, unknown> = {};
    cyclic['cause'] = cyclic;
    expect(getTraceId(cyclic)).toBeUndefined();

    const throwing = {};
    Object.defineProperty(throwing, 'traceId', {
      get() {
        throw new Error('boom');
      },
    });
    expect(getTraceId(throwing)).toBeUndefined();
  });
});

describe('ensureTraceId', () => {
  it('优先复用后端 trace_id', () => {
    expect(ensureTraceId({ traceId: 'trace-backend' })).toEqual({
      traceId: 'trace-backend',
      clientGenerated: false,
    });
  });

  it('缺失时生成 client_trace_id 并标记来源', () => {
    const first = ensureTraceId(new Error('boom'));
    const second = ensureTraceId(new Error('boom'));

    expect(first.clientGenerated).toBe(true);
    expect(first.traceId.length).toBeGreaterThan(0);
    expect(first.traceId).not.toBe(second.traceId);
  });
});
