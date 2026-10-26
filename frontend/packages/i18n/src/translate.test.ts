import { describe, expect, it } from 'vitest';
import type { MessageKey } from './messages/zh-CN';
import { createTranslator } from './translate';

describe('createTranslator', () => {
  it('返回字典原文', () => {
    expect(createTranslator()('common.empty')).toBe('暂无数据');
  });

  it('支持 {name} 插值并按字符串输出数字参数', () => {
    const t = createTranslator();
    expect(t('common.listTotal', { count: 42 })).toBe('共 42 条');
    expect(t('common.traceId', { traceId: 'trace-1' })).toBe('trace_id: trace-1');
  });

  it('缺失参数时保留占位符', () => {
    expect(createTranslator()('common.listTotal')).toBe('共 {count} 条');
  });

  it('缺失 key 回退为 key 本身并去重记录', () => {
    const missing = 'missing.key' as MessageKey;
    const t = createTranslator({});

    expect(t(missing)).toBe('missing.key');
    expect(t(missing)).toBe('missing.key');
    expect(t.missingKeys).toEqual(['missing.key']);
  });
});
