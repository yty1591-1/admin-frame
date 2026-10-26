import { describe, expect, it } from 'vitest';
import { describeError, type ErrorPresentation } from './error-presentation';
import { zhCN, type MessageKey } from './messages/zh-CN';

type Case = {
  name: string;
  input: unknown;
  expected: Pick<ErrorPresentation, 'code' | 'messageKey' | 'action' | 'retryable'>;
};

const CASES: readonly Case[] = [
  {
    name: '40000 参数校验',
    input: { code: 40000 },
    expected: { code: 40000, messageKey: 'error.validation', action: 'inline', retryable: false },
  },
  {
    name: '40100 未认证',
    input: { code: 40100 },
    expected: { code: 40100, messageKey: 'error.unauthorized', action: 'login', retryable: false },
  },
  {
    name: '40300 无权限',
    input: { code: 40300 },
    expected: { code: 40300, messageKey: 'error.forbidden', action: 'page', retryable: false },
  },
  {
    name: '40400 资源不存在',
    input: { code: 40400 },
    expected: { code: 40400, messageKey: 'error.notFound', action: 'inline', retryable: false },
  },
  {
    name: '40900 需审批',
    input: { code: 40900 },
    expected: {
      code: 40900,
      messageKey: 'error.approvalRequired',
      action: 'approval',
      retryable: false,
    },
  },
  {
    name: '42900 限流',
    input: { code: 42900 },
    expected: { code: 42900, messageKey: 'error.rateLimited', action: 'toast', retryable: true },
  },
  {
    name: '50000 服务端错误',
    input: { code: 50000 },
    expected: { code: 50000, messageKey: 'error.server', action: 'inline', retryable: true },
  },
  {
    name: '50300 服务不可用',
    input: { code: 50300 },
    expected: { code: 50300, messageKey: 'error.unavailable', action: 'page', retryable: true },
  },
  {
    name: 'network',
    input: { kind: 'network' },
    expected: { code: 0, messageKey: 'error.network', action: 'toast', retryable: true },
  },
  {
    name: 'timeout',
    input: { kind: 'timeout' },
    expected: { code: 0, messageKey: 'error.timeout', action: 'toast', retryable: true },
  },
  {
    name: '未知错误',
    input: new Error('boom'),
    expected: { code: 0, messageKey: 'error.unknown', action: 'toast', retryable: false },
  },
];

describe('describeError', () => {
  it.each(CASES)('$name 映射 messageKey/action/retryable', ({ input, expected }) => {
    expect(describeError(input)).toMatchObject(expected);
  });

  it('40900 明确不可重试，且不会进入普通冲突处理', () => {
    const presentation = describeError({ code: 40900 });
    expect(presentation.retryable).toBe(false);
    expect(presentation.action).toBe('approval');
  });

  it('所有映射 key 均存在于 zh-CN 字典', () => {
    const keys = new Set<MessageKey>();
    for (const { input } of CASES) {
      keys.add(describeError(input).messageKey);
    }
    for (const key of keys) {
      expect(zhCN[key]).toBeTypeOf('string');
    }
  });

  it('沿 cause 链读取 traceId，并拒绝非法格式', () => {
    expect(describeError({ code: 50000, cause: { traceId: 'trace-abc.1' } }).traceId).toBe(
      'trace-abc.1',
    );
    expect(describeError({ code: 50000, traceId: 'bad trace id!' }).traceId).toBeUndefined();
    expect(describeError({ code: 50000, traceId: 'x'.repeat(129) }).traceId).toBeUndefined();
  });

  it('非法 code/kind 不采纳，字符串码不当作业务码', () => {
    expect(describeError({ code: '40900' }).messageKey).toBe('error.unknown');
    expect(describeError({ code: 123 }).messageKey).toBe('error.unknown');
    expect(describeError({ kind: 'conflict' }).messageKey).toBe('error.unknown');
    expect(describeError({ kind: 'not-a-kind' }).messageKey).toBe('error.unknown');
  });

  it('原始值、null 与 getter 异常均不抛出', () => {
    expect(describeError(null).messageKey).toBe('error.unknown');
    expect(describeError('boom').messageKey).toBe('error.unknown');
    expect(describeError(42).messageKey).toBe('error.unknown');

    const throwing = Object.defineProperty({}, 'code', {
      get() {
        throw new Error('getter');
      },
    });
    expect(describeError(throwing).messageKey).toBe('error.unknown');
  });
});
