import type { VbenFormProps } from '@vben/app-kernel/adapter/form';

/**
 * 列表页筛选区默认值（对齐 Pencil 画布）：
 * 单行、无外置 label、查询/重置内联在表单动作区。
 *
 * 调用方仍须提供 schema，以及 handleSubmit / handleReset
 *（自定义 handleReset 时需自行 formApi.reset()）。
 */
export function withListQueryForm<T extends VbenFormProps>(options: T): T {
  const schema = (options.schema ?? []).map((field) => ({
    ...field,
    formItemClass: field.formItemClass ?? 'w-[180px] max-w-full',
  }));

  return {
    actionButtonsReverse: true,
    actionLayout: 'inline',
    actionPosition: 'left',
    compact: true,
    layout: 'inline',
    resetButtonOptions: { content: '重置' },
    submitButtonOptions: { content: '查询' },
    wrapperClass: 'items-center gap-3',
    ...options,
    commonConfig: {
      componentProps: { class: 'w-full' },
      hideLabel: true,
      ...options.commonConfig,
    },
    schema,
  } as T;
}

/** 筛选 + 表格同卡容器 class（画布：一张 card 内工具栏与表格）。 */
export const listQueryCardClass = 'bg-card rounded-lg border p-4';
