import { describe, expect, it } from 'vitest';
import { clampPageSize, defaultPageSize, maxPageSize } from './pagination';
import { RuntimeConfigError, loadRuntimeConfig } from './runtime-config';

describe('loadRuntimeConfig', () => {
  it('接受合法配置并补齐可选字段', () => {
    const config = loadRuntimeConfig('admin', {
      apiBaseUrl: '',
      env: 'local',
      channel: 'admin',
    });
    expect(config.channel).toBe('admin');
    expect(config.features).toEqual({});
    expect(config.monitoring).toEqual({ dsn: null, sampleRate: 0 });
  });

  it('端别不一致时 fail-closed', () => {
    expect(() =>
      loadRuntimeConfig('admin', { apiBaseUrl: '', env: 'local', channel: 'biz' }),
    ).toThrowError(RuntimeConfigError);
  });

  it('非法配置（采样率越界）直接拒绝', () => {
    expect(() =>
      loadRuntimeConfig('biz', {
        apiBaseUrl: '',
        env: 'prod',
        channel: 'biz',
        monitoring: { sampleRate: 2 },
      }),
    ).toThrowError(RuntimeConfigError);
  });

  it('缺少配置时不回退默认值', () => {
    expect(() => loadRuntimeConfig('admin', undefined)).toThrowError(RuntimeConfigError);
  });
});

describe('clampPageSize', () => {
  it('非法值回落默认值', () => {
    expect(clampPageSize(0)).toBe(defaultPageSize);
    expect(clampPageSize(1.5)).toBe(defaultPageSize);
    expect(clampPageSize(Number.NaN)).toBe(defaultPageSize);
  });

  it('上限不超过 100', () => {
    expect(clampPageSize(500)).toBe(maxPageSize);
    expect(clampPageSize(50)).toBe(50);
  });
});
