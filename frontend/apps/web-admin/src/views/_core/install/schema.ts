/**
 * 安装向导的纯函数层（ADR-0018）：把 `/install/schema` 的扁平字段清单切成
 * 「命名空间 → 分组 → 字段」的渲染结构，并负责草稿值 → apply 请求体的转换。
 *
 * 单独抽出来是为了可测：视图只做渲染与交互，分组/过滤/校验都在这里，
 * 避免向导里散落硬编码的分组表。
 */

import type {
  InstallMode,
  InstallSchema,
  SchemaField,
} from '#/api/configcenter/install';

export type FieldGroup = {
  fields: SchemaField[];
  id: string;
  order: number;
  title: string;
};

export type FieldSection = {
  /** 该域在 repair 模式下的阻塞键数（用于徽标）。 */
  blockingCount: number;
  groups: FieldGroup[];
  namespace: string;
  order: number;
  title: string;
};

/** 草稿：namespace → key → 用户输入值（空串表示「用默认值」）。 */
export type InstallDraft = Record<string, Record<string, string>>;

/** 表单里真正需要用户填的字段：敏感键走密钥服务，不在这里渲染成输入框。 */
export function configFields(
  schema: InstallSchema,
  mode: InstallMode,
): SchemaField[] {
  return schema.fields
    .filter((field) => !field.sensitive)
    .filter((field) => (mode === 'repair' ? field.blocking : true))
    .toSorted((a, b) => byOrder(a, b));
}

/** 敏感键：自动生成项只展示规则；`auto_generate=false` 允许向导填写覆盖值。 */
export function secretFields(schema: InstallSchema): SchemaField[] {
  return schema.fields
    .filter((field) => field.sensitive)
    .filter((field) => (schema.mode === 'repair' ? field.blocking : true))
    .toSorted((a, b) => byOrder(a, b));
}

/** 可在向导中填写明文的敏感键（安装时写入密钥子域）。 */
export function writableSecretFields(schema: InstallSchema): SchemaField[] {
  return secretFields(schema).filter(
    (field) => field.secret && !field.secret.auto_generate,
  );
}

/**
 * 把字段按 namespace → group 两级切开，保持服务端给的 order 顺序。
 *
 * `titles` 来自 schema 的 `namespaces[]`（namespace → 中文标题），缺省回退到域 ID。
 */
export function buildSections(
  fields: SchemaField[],
  titles: Record<string, string> = {},
): FieldSection[] {
  const sections = new Map<string, FieldSection>();
  for (const field of fields) {
    let section = sections.get(field.namespace);
    if (!section) {
      section = {
        blockingCount: 0,
        groups: [],
        namespace: field.namespace,
        order: Number.MAX_SAFE_INTEGER,
        title: titles[field.namespace] ?? field.namespace,
      };
      sections.set(field.namespace, section);
    }
    if (field.blocking) {
      section.blockingCount += 1;
    }
    let group = section.groups.find((item) => item.id === field.group);
    if (!group) {
      group = {
        fields: [],
        id: field.group,
        order: field.order,
        title: field.group_title || field.group,
      };
      section.groups.push(group);
    } else if (field.order < group.order) {
      // 组顺序取组内最小字段 order，避免依赖服务端枚举顺序。
      group.order = field.order;
    }
    group.fields.push(field);
  }
  return [...sections.values()]
    .map((section) => ({
      ...section,
      groups: section.groups.toSorted((a, b) => byOrder(a, b)),
      order: sectionOrder(section.namespace, fields),
    }))
    .toSorted((a, b) => byOrder(a, b));
}

/** 默认值徽标文案；`none` 表示没有默认值，不显示徽标。 */
export function defaultBadge(field: SchemaField): string {
  if (!field.default || field.default.source === 'none') {
    return '';
  }
  if (field.default.value === '') {
    return '默认：空';
  }
  let suffix = '';
  if (field.default.source === 'prod_default') {
    suffix = '（生产基线）';
  } else if (field.default.source === 'dev_default') {
    suffix = '（开发基线）';
  }
  return `默认：${field.default.value}${suffix}`;
}

/** 值是否算「已填」：空白不算。 */
export function hasValue(value: null | string | undefined): boolean {
  return (value ?? '').trim() !== '';
}

/**
 * 校验提交前的草稿：必填键（不一定是阻塞键）为空时提示。
 *
 * `repair` 模式下服务端只接受阻塞键，所以这里只要求阻塞键填值；
 * 首装模式下 `required` 字段也纳入——它们是「进程虽然能起但语义上必须给」的键。
 */
export function missingFields(
  fields: SchemaField[],
  draft: InstallDraft,
  mode: InstallMode,
): SchemaField[] {
  return fields.filter((field) => {
    if (mode === 'repair' ? !field.blocking : !field.required) {
      return false;
    }
    return !hasValue(draft[field.namespace]?.[field.key]);
  });
}

/** 草稿 → apply 的 `values`（按 namespace 分组，去掉空输入与首尾空格）。 */
export function collectValues(
  fields: SchemaField[],
  draft: InstallDraft,
): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {};
  for (const field of fields) {
    const value = draft[field.namespace]?.[field.key];
    if (!hasValue(value)) {
      continue;
    }
    out[field.namespace] ??= {};
    const target = out[field.namespace];
    if (target) {
      target[field.key] = (value ?? '').trim();
    }
  }
  return out;
}

/** `namespace/key` 形式的阻塞键列表（用于 repair 的声明白名单）。 */
export function declaredKeyRefs(text: string): string[] {
  const seen = new Set<string>();
  for (const part of text.split(/[\s,;]+/)) {
    const ref = part.trim();
    if (ref && ref.includes('/')) {
      seen.add(ref);
    }
  }
  return [...seen].toSorted();
}

/** 已填值数量，用于「预览」步骤显示本次会写入多少键。 */
export function filledCount(draft: InstallDraft): number {
  return filledEntries(draft).length;
}

/** 已填条目清单（预览步变更摘要）。 */
export function filledEntries(
  draft: InstallDraft,
): Array<{ key: string; namespace: string; value: string }> {
  const rows: Array<{ key: string; namespace: string; value: string }> = [];
  for (const [namespace, group] of Object.entries(draft)) {
    for (const [key, value] of Object.entries(group)) {
      if (hasValue(value)) {
        rows.push({ key, namespace, value: value.trim() });
      }
    }
  }
  return rows.toSorted((a, b) =>
    `${a.namespace}/${a.key}`.localeCompare(`${b.namespace}/${b.key}`),
  );
}

function byOrder(a: { order: number }, b: { order: number }): number {
  return a.order - b.order;
}

/** 命名空间顺序取该域字段的最小 order，避免再依赖 namespaces 列表。 */
function sectionOrder(namespace: string, fields: SchemaField[]): number {
  let min = Number.MAX_SAFE_INTEGER;
  for (const field of fields) {
    if (field.namespace === namespace && field.order < min) {
      min = field.order;
    }
  }
  return min;
}
