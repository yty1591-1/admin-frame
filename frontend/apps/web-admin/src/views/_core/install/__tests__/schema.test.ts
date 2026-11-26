import type {
  InstallSchema,
  SchemaDefault,
  SchemaField,
} from '#/api/configcenter/install';

import { describe, expect, it } from 'vitest';

import {
  buildSections,
  collectValues,
  configFields,
  declaredKeyRefs,
  defaultBadge,
  filledCount,
  hasValue,
  missingFields,
  secretFields,
} from '../schema';

const noDefault: SchemaDefault = { source: 'none', value: '' };

function field(overrides: Partial<SchemaField> = {}): SchemaField {
  return {
    blocking: false,
    default: noDefault,
    deprecated: false,
    description: '',
    group: 'core',
    group_title: '核心',
    hot_whitelist: false,
    key: 'db.driver',
    label: '数据库驱动',
    namespace: 'platform',
    order: 1,
    owner: 'platform',
    reload_mode: 'restart',
    required: false,
    sensitive: false,
    value_type: 'string',
    widget: 'text',
    ...overrides,
  };
}

function schema(fields: SchemaField[]): InstallSchema {
  return {
    blocking_count: 0,
    blocking_keys: [],
    env: 'dev',
    fields,
    mode: 'first_install',
    namespaces: [],
    status: 'pending',
  };
}

describe('configFields', () => {
  const fields = [
    field({ blocking: true, key: 'http.base_url', order: 2, required: true }),
    field({ key: 'db.driver', order: 1 }),
    field({ key: 'smtp.password', sensitive: true, order: 3 }),
  ];

  it('首装保留全部非敏感字段并按 order 排序', () => {
    expect(
      configFields(schema(fields), 'first_install').map((f) => f.key),
    ).toEqual(['db.driver', 'http.base_url']);
  });

  it('repair 只保留阻塞键', () => {
    expect(configFields(schema(fields), 'repair').map((f) => f.key)).toEqual([
      'http.base_url',
    ]);
  });

  it('敏感键只出现在 secretFields', () => {
    expect(secretFields(schema(fields)).map((f) => f.key)).toEqual([
      'smtp.password',
    ]);
  });
});

describe('buildSections', () => {
  const fields = [
    field({
      group: 'mail',
      group_title: '邮件',
      namespace: 'notification',
      order: 30,
    }),
    field({
      group: 'http',
      group_title: 'HTTP',
      namespace: 'platform',
      order: 20,
    }),
    field({ blocking: true, group: 'core', namespace: 'platform', order: 10 }),
  ];

  it('按 namespace → group 两级分组，并把阻塞数记在域上', () => {
    const sections = buildSections(fields, { platform: '平台与基础设施' });
    expect(sections.map((s) => s.namespace)).toEqual([
      'platform',
      'notification',
    ]);
    expect(sections[0]?.title).toBe('平台与基础设施');
    expect(sections[0]?.blockingCount).toBe(1);
    expect(sections[0]?.groups.map((g) => g.id)).toEqual(['core', 'http']);
    expect(sections[1]?.title).toBe('notification');
  });

  it('缺省标题回退到域 ID', () => {
    const sections = buildSections(fields);
    expect(sections[0]?.title).toBe('platform');
  });
});

describe('defaultBadge', () => {
  it('没有默认值时不显示', () => {
    expect(defaultBadge(field())).toBe('');
  });

  it('代码默认值与生产基线各有文案', () => {
    expect(
      defaultBadge(
        field({ default: { source: 'code_default', value: 'memory' } }),
      ),
    ).toBe('默认：memory');
    expect(
      defaultBadge(
        field({ default: { source: 'prod_default', value: 'mysql' } }),
      ),
    ).toBe('默认：mysql（生产基线）');
  });

  it('空串默认值显式标注', () => {
    expect(
      defaultBadge(field({ default: { source: 'code_default', value: '' } })),
    ).toBe('默认：空');
  });
});

describe('missingFields', () => {
  const fields = [
    field({ blocking: true, key: 'http.base_url', required: true }),
    field({ key: 'db.driver' }),
  ];

  it('首装要求 required 键填值', () => {
    expect(
      missingFields(fields, {}, 'first_install').map((f) => f.key),
    ).toEqual(['http.base_url']);
    expect(
      missingFields(
        fields,
        { platform: { 'http.base_url': ' https://x ' } },
        'first_install',
      ),
    ).toEqual([]);
  });

  it('repair 只要求阻塞键填值', () => {
    expect(missingFields([field({ key: 'db.driver' })], {}, 'repair')).toEqual(
      [],
    );
  });

  it('空白值不算已填', () => {
    expect(hasValue('   ')).toBe(false);
    expect(hasValue('x')).toBe(true);
  });
});

describe('collectValues', () => {
  it('按 namespace 分组、去空白并 trim', () => {
    const fields = [field({ key: 'db.driver' }), field({ key: 'log.level' })];
    const values = collectValues(fields, {
      platform: { 'db.driver': '  mysql ', 'log.level': '   ' },
    });
    expect(values).toEqual({ platform: { 'db.driver': 'mysql' } });
  });

  it('没有填写时返回空对象', () => {
    expect(collectValues([field()], {})).toEqual({});
  });
});

describe('declaredKeyRefs', () => {
  it('按空白/逗号/分号切分，去重排序，丢弃非 namespace/key 片段', () => {
    expect(
      declaredKeyRefs(
        'platform/auth.jwt.secret, platform/b  ; platform/a\ninvalid',
      ),
    ).toEqual(['platform/a', 'platform/auth.jwt.secret', 'platform/b']);
  });

  it('空输入返回空数组', () => {
    expect(declaredKeyRefs('  ')).toEqual([]);
  });
});

describe('filledCount', () => {
  it('只统计非空值', () => {
    expect(
      filledCount({
        platform: { 'a.b': 'x', 'c.d': ' ' },
        identity: { 'e.f': 'y' },
      }),
    ).toBe(2);
  });
});
