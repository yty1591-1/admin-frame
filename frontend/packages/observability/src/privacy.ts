/**
 * 上报属性白名单（S3 / §12.2、D3-02）。
 * 只允许下列键进入 span；禁止表单值、Token、Cookie、存储键值、URL query 原文与用户标识。
 */
export const SPAN_ATTRIBUTE_KEYS = [
  'service.name',
  'service.version',
  'deployment.environment',
  'page.route',
  'error.code',
  'error.kind',
  'trace.id',
  'client.trace.id',
  'error.source',
] as const;

export type SpanAttributeKey = (typeof SPAN_ATTRIBUTE_KEYS)[number];

export type SpanAttributes = Partial<Record<SpanAttributeKey, string | number | boolean>>;

export type AttributeInput = {
  serviceName: string;
  serviceVersion: string;
  environment: string;
  route?: string | undefined;
  code?: number | undefined;
  kind?: string | undefined;
  traceId?: string | undefined;
  clientGenerated?: boolean | undefined;
  source?: string | undefined;
};

const MAX_VALUE_LENGTH = 200;
const UNSAFE_CHARS = /[\r\n\t]/g;

function sanitizeString(value: string): string {
  const flattened = value.replace(UNSAFE_CHARS, ' ').trim();
  return flattened.length > MAX_VALUE_LENGTH ? flattened.slice(0, MAX_VALUE_LENGTH) : flattened;
}

/** 只保留 pathname，去掉 query 与 hash，避免把筛选条件等用户数据带进上报。 */
export function sanitizeRoute(route: string): string {
  const cut = route.search(/[?#]/);
  return sanitizeString(cut === -1 ? route : route.slice(0, cut));
}

/** 只读取白名单字段；调用方传入的其他键一律忽略。 */
export function buildSpanAttributes(input: AttributeInput): SpanAttributes {
  const attributes: SpanAttributes = {
    'service.name': sanitizeString(input.serviceName),
    'service.version': sanitizeString(input.serviceVersion),
    'deployment.environment': sanitizeString(input.environment),
  };
  if (input.route !== undefined) {
    attributes['page.route'] = sanitizeRoute(input.route);
  }
  if (input.code !== undefined && Number.isInteger(input.code)) {
    attributes['error.code'] = input.code;
  }
  if (input.kind !== undefined) {
    attributes['error.kind'] = sanitizeString(input.kind);
  }
  if (input.traceId !== undefined) {
    attributes['trace.id'] = sanitizeString(input.traceId);
  }
  if (input.clientGenerated !== undefined) {
    attributes['client.trace.id'] = input.clientGenerated;
  }
  if (input.source !== undefined) {
    attributes['error.source'] = sanitizeString(input.source);
  }
  return attributes;
}
