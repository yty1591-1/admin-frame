import { afterEach, describe, expect, it, vi } from 'vitest';
import { getPendingReportCount, reportError, resetReports, setReportTransport } from './report';
import type { ReportRecord } from './report';

afterEach(() => {
  resetReports();
});

describe('reportError', () => {
  it('传输未就绪时缓冲记录', () => {
    reportError(new Error('boom'));

    expect(getPendingReportCount()).toBe(1);
  });

  it('缓冲上限 20 条并丢弃最早记录', () => {
    for (let index = 0; index < 25; index += 1) {
      reportError({ traceId: `trace-${index}` });
    }
    expect(getPendingReportCount()).toBe(20);

    const received: ReportRecord[] = [];
    setReportTransport({ send: (record) => received.push(record) });

    expect(received).toHaveLength(20);
    expect(received[0]?.traceId).toBe('trace-5');
    expect(received[19]?.traceId).toBe('trace-24');
    expect(getPendingReportCount()).toBe(0);
  });

  it('传输就绪后立即发送并携带上下文', () => {
    const received: ReportRecord[] = [];
    setReportTransport({ send: (record) => received.push(record) });

    reportError({ traceId: 'trace-9' }, { route: '/iam/roles', source: 'route' });

    expect(received).toHaveLength(1);
    expect(received[0]).toMatchObject({
      traceId: 'trace-9',
      clientGenerated: false,
      route: '/iam/roles',
      source: 'route',
    });
    expect(received[0]?.time).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  });

  it('传输抛错时静默处理', () => {
    setReportTransport({
      send: () => {
        throw new Error('collector down');
      },
    });

    expect(() => {
      reportError(new Error('boom'));
    }).not.toThrow();
  });

  it('resetReports 清空传输与缓冲', () => {
    const send = vi.fn();
    setReportTransport({ send });
    reportError(new Error('boom'));
    resetReports();

    reportError(new Error('boom'));
    expect(send).toHaveBeenCalledTimes(1);
    expect(getPendingReportCount()).toBe(1);
  });
});
