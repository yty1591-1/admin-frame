import { describe, expect, it } from 'vitest';
import {
  buildSpanAttributes,
  sanitizeRoute,
  SPAN_ATTRIBUTE_KEYS,
  type AttributeInput,
} from './privacy';

const BASE: AttributeInput = {
  serviceName: 'admin-frame-admin',
  serviceVersion: '0.1.0',
  environment: 'prod',
};

describe('buildSpanAttributes', () => {
  it('只输出白名单属性，忽略敏感键', () => {
    const attributes = buildSpanAttributes({
      ...BASE,
      route: '/iam/roles?keyword=张三#top',
      code: 50000,
      kind: 'server',
      traceId: 'trace-1',
      clientGenerated: false,
      source: 'query',
      token: 'secret-token',
      password: 'p@ssw0rd',
    } as unknown as AttributeInput);

    expect(Object.keys(attributes).sort()).toEqual([...SPAN_ATTRIBUTE_KEYS].sort());
    expect(attributes['page.route']).toBe('/iam/roles');
    expect(attributes).not.toHaveProperty('token');
    expect(attributes).not.toHaveProperty('password');
  });

  it('缺省字段不输出', () => {
    expect(buildSpanAttributes(BASE)).toEqual({
      'service.name': 'admin-frame-admin',
      'service.version': '0.1.0',
      'deployment.environment': 'prod',
    });
  });

  it('规整换行并截断超长值', () => {
    const attributes = buildSpanAttributes({
      ...BASE,
      source: `line\n${'x'.repeat(300)}`,
    });

    expect(attributes['error.source']).toHaveLength(200);
    expect(attributes['error.source']).not.toContain('\n');
  });
});

describe('sanitizeRoute', () => {
  it('去除 query 与 hash', () => {
    expect(sanitizeRoute('/iam/roles?page=2&keyword=ops#top')).toBe('/iam/roles');
    expect(sanitizeRoute('/')).toBe('/');
  });
});
