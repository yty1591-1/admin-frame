import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  configureLogger,
  getBufferedLogs,
  logger,
  resetLogger,
  type LogFields,
  type LogRecord,
} from './logger';

afterEach(() => {
  resetLogger();
});

describe('logger', () => {
  it('只保留白名单字段，敏感键直接丢弃', () => {
    logger.info({
      event: 'ui.uncaught',
      traceId: 'trace-1',
      route: '/iam/roles',
      password: 'p@ssw0rd',
      token: 'token-value',
      cookie: 'session=abc',
    } as LogFields);

    const [record] = getBufferedLogs();
    expect(record?.fields).toMatchObject({
      event: 'ui.uncaught',
      traceId: 'trace-1',
      route: '/iam/roles',
    });
    expect(record?.fields).not.toHaveProperty('password');
    expect(record?.fields).not.toHaveProperty('token');
    expect(record?.fields).not.toHaveProperty('cookie');
  });

  it('字符串截断至 200 字符，非有限数字与非原始类型被规整', () => {
    logger.info({
      event: 'query.error',
      route: 'x'.repeat(300),
      durationMs: Number.NaN,
      source: { href: '/boot' } as unknown as string,
    });

    const [record] = getBufferedLogs();
    expect(record?.fields['route']).toHaveLength(200);
    expect(record?.fields).not.toHaveProperty('durationMs');
    expect(record?.fields['source']).toBe('[object]');
  });

  it('按级别过滤，空 event 记为 unknown', () => {
    configureLogger({ level: 'warn' });
    logger.debug({ event: 'debug.event' });
    logger.info({ event: 'info.event' });
    logger.warn({ event: '' });

    const records = getBufferedLogs();
    expect(records).toHaveLength(1);
    expect(records[0]?.level).toBe('warn');
    expect(records[0]?.event).toBe('unknown');
  });

  it('环形缓冲不超过配置上限，只保留最新记录', () => {
    configureLogger({ bufferSize: 3 });
    for (let index = 0; index < 5; index += 1) {
      logger.info({ event: `event.${index}` });
    }

    const records = getBufferedLogs();
    expect(records.map((record) => record.event)).toEqual(['event.2', 'event.3', 'event.4']);
  });

  it('sink 接收记录，异常不外抛且不递归', () => {
    const received: LogRecord[] = [];
    const sink = vi.fn((record: LogRecord) => {
      received.push(record);
      logger.error({ event: 'sink.nested' });
    });
    configureLogger({ sink });

    expect(() => {
      logger.info({ event: 'ui.uncaught' });
    }).not.toThrow();
    expect(sink).toHaveBeenCalledTimes(1);
    expect(received[0]?.event).toBe('ui.uncaught');
    expect(getBufferedLogs().map((record) => record.event)).toEqual(['ui.uncaught', 'sink.nested']);
  });

  it('sink 抛错时静默处理', () => {
    configureLogger({
      sink: () => {
        throw new Error('transport down');
      },
    });

    expect(() => {
      logger.warn({ event: 'query.error' });
    }).not.toThrow();
    expect(getBufferedLogs()).toHaveLength(1);
  });

  it('getBufferedLogs 返回快照副本', () => {
    logger.info({ event: 'a' });
    const snapshot = [...getBufferedLogs()];
    snapshot.length = 0;

    expect(getBufferedLogs()).toHaveLength(1);
  });
});
