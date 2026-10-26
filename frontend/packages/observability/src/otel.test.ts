import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  spans: [] as { attributes: Record<string, unknown>; status: unknown }[],
}));

vi.mock('@opentelemetry/sdk-trace-web', () => ({
  WebTracerProvider: class {
    getTracer() {
      return {
        startSpan: () => {
          const span = { attributes: {} as Record<string, unknown>, status: undefined as unknown };
          mocks.spans.push(span);
          return {
            setAttribute: (key: string, value: unknown) => {
              span.attributes[key] = value;
            },
            setStatus: (status: unknown) => {
              span.status = status;
            },
            end: () => undefined,
          };
        },
      };
    }
  },
  BatchSpanProcessor: class {
    constructor(_options: unknown) {}
  },
  TraceIdRatioBasedSampler: class {
    constructor(_ratio: number) {}
  },
}));

vi.mock('@opentelemetry/exporter-trace-otlp-http', () => ({
  OTLPTraceExporter: class {
    constructor(_options: unknown) {}
  },
}));

vi.mock('@opentelemetry/resources', () => ({
  resourceFromAttributes: (attributes: unknown) => ({ attributes }),
}));

vi.mock('@opentelemetry/semantic-conventions', () => ({
  ATTR_SERVICE_NAME: 'service.name',
  ATTR_SERVICE_VERSION: 'service.version',
}));

vi.mock('@opentelemetry/api', () => ({
  SpanStatusCode: { ERROR: 2 },
}));

import { getPendingReportCount, reportError, resetReports } from './report';
import { resetLogger } from './logger';
import { initObservability, isObservabilityReady, resetObservability } from './otel';

const OPTIONS = {
  endpoint: 'https://collector.example/v1/traces',
  sampleRate: 0.5,
  release: '1.0.0',
  channel: 'admin',
  env: 'prod',
};

beforeEach(() => {
  resetObservability();
  resetReports();
  resetLogger();
  mocks.spans.length = 0;
});

afterEach(() => {
  resetObservability();
});

describe('initObservability', () => {
  it('endpoint 为空或采样率为 0 时不初始化', async () => {
    initObservability({ ...OPTIONS, endpoint: null });
    initObservability({ ...OPTIONS, endpoint: '   ' });
    initObservability({ ...OPTIONS, sampleRate: 0 });
    await Promise.resolve();

    expect(isObservabilityReady()).toBe(false);
    reportError({ traceId: 'trace-1' });
    expect(getPendingReportCount()).toBe(1);
  });

  it('就绪后按白名单上报错误 span', async () => {
    initObservability(OPTIONS);
    await vi.waitFor(() => {
      expect(isObservabilityReady()).toBe(true);
    });

    reportError(
      { traceId: 'trace-9', code: 50000, kind: 'server' },
      {
        route: '/iam/roles?page=2',
        source: 'query',
      },
    );

    expect(mocks.spans).toHaveLength(1);
    const span = mocks.spans[0];
    expect(span?.attributes).toEqual({
      'service.name': 'admin-frame-admin',
      'service.version': '1.0.0',
      'deployment.environment': 'prod',
      'page.route': '/iam/roles',
      'error.code': 50000,
      'error.kind': 'server',
      'trace.id': 'trace-9',
      'client.trace.id': false,
      'error.source': 'query',
    });
    expect(span?.status).toEqual({ code: 2 });
  });

  it('只在首次就绪前补发缓冲错误', async () => {
    reportError({ traceId: 'trace-buffered' });
    expect(getPendingReportCount()).toBe(1);

    initObservability(OPTIONS);
    await vi.waitFor(() => {
      expect(isObservabilityReady()).toBe(true);
    });

    expect(getPendingReportCount()).toBe(0);
    expect(mocks.spans).toHaveLength(1);
    expect(mocks.spans[0]?.attributes['trace.id']).toBe('trace-buffered');
    expect(mocks.spans[0]?.attributes['client.trace.id']).toBe(false);
  });
});
