import { logger } from './logger';
import { buildSpanAttributes } from './privacy';
import { setReportTransport } from './report';

/** OTel 初始化参数（S3 / §5.3）。endpoint 为空或采样率为 0 时视为关闭。 */
export type ObservabilityOptions = {
  endpoint: string | null;
  sampleRate: number;
  release: string;
  channel: string;
  env: string;
};

let ready = false;
let initializing = false;

export function isObservabilityReady(): boolean {
  return ready;
}

function resolveEndpoint(endpoint: string | null): string | null {
  if (endpoint === null) {
    return null;
  }
  const trimmed = endpoint.trim();
  return trimmed === '' ? null : trimmed;
}

/**
 * 幂等初始化：endpoint 为空、采样率为 0 或已初始化时直接返回。
 * 所有 OTel SDK 均在首屏后动态导入，任何 SDK 代码不得进入入口 chunk。
 */
export function initObservability(options: ObservabilityOptions): void {
  if (ready || initializing) {
    return;
  }
  const endpoint = resolveEndpoint(options.endpoint);
  if (endpoint === null || !Number.isFinite(options.sampleRate) || options.sampleRate <= 0) {
    return;
  }
  initializing = true;
  void bootstrap(options, endpoint)
    .catch(() => {
      // 初始化失败只落下诊断日志，不得影响页面渲染。
      logger.warn({ event: 'otel.init_failed', source: 'otel' });
    })
    .finally(() => {
      initializing = false;
    });
}

async function bootstrap(options: ObservabilityOptions, endpoint: string): Promise<void> {
  const [
    { WebTracerProvider, BatchSpanProcessor, TraceIdRatioBasedSampler },
    { OTLPTraceExporter },
    { resourceFromAttributes },
    { ATTR_SERVICE_NAME, ATTR_SERVICE_VERSION },
    { SpanStatusCode },
  ] = await Promise.all([
    import('@opentelemetry/sdk-trace-web'),
    import('@opentelemetry/exporter-trace-otlp-http'),
    import('@opentelemetry/resources'),
    import('@opentelemetry/semantic-conventions'),
    import('@opentelemetry/api'),
  ]);

  const serviceName = `admin-frame-${options.channel}`;
  const provider = new WebTracerProvider({
    resource: resourceFromAttributes({
      [ATTR_SERVICE_NAME]: serviceName,
      [ATTR_SERVICE_VERSION]: options.release,
      'deployment.environment': options.env,
    }),
    sampler: new TraceIdRatioBasedSampler(options.sampleRate),
    spanProcessors: [new BatchSpanProcessor(new OTLPTraceExporter({ url: endpoint }))],
  });
  const tracer = provider.getTracer('frontend');

  setReportTransport({
    send(record) {
      const span = tracer.startSpan('frontend.error');
      const attributes = buildSpanAttributes({
        serviceName,
        serviceVersion: options.release,
        environment: options.env,
        ...(record.route === undefined ? {} : { route: record.route }),
        ...(record.code === undefined ? {} : { code: record.code }),
        ...(record.kind === undefined ? {} : { kind: record.kind }),
        traceId: record.traceId,
        clientGenerated: record.clientGenerated,
        ...(record.source === undefined ? {} : { source: record.source }),
      });
      for (const [key, value] of Object.entries(attributes)) {
        span.setAttribute(key, value);
      }
      span.setStatus({ code: SpanStatusCode.ERROR });
      span.end();
    },
  });

  ready = true;
}

/** 仅测试使用：重置初始化状态并解绑上报通道。 */
export function resetObservability(): void {
  ready = false;
  initializing = false;
  setReportTransport(null);
}
