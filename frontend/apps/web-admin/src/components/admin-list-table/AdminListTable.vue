<script lang="ts" setup>
import type { TableProps } from 'ant-design-vue';
import type { ColumnType } from 'ant-design-vue/es/table';

import { computed, useAttrs, useSlots } from 'vue';

import { Table } from 'ant-design-vue';

defineOptions({
  name: 'AdminListTable',
  inheritAttrs: false,
});

const props = withDefaults(
  defineProps<{
    /** 列定义；未设 ellipsis 的数据列默认开启省略，actions 列默认右固定。 */
    columns?: TableProps['columns'];
    /** 横向滚动下限；列宽之和不足时使用该值。 */
    minScrollX?: number;
    /** 透传 ant Table scroll；显式传入时优先生效。 */
    scroll?: TableProps['scroll'];
    size?: TableProps['size'];
  }>(),
  {
    columns: () => [],
    minScrollX: 1100,
    scroll: undefined,
    size: 'middle',
  },
);

const attrs = useAttrs();
const slots = useSlots();

type AnyColumn = ColumnType<Record<string, unknown>>;

function isActionsColumn(column: AnyColumn): boolean {
  return column.key === 'actions' || column.dataIndex === 'actions';
}

function hasCustomCell(column: AnyColumn): boolean {
  return Boolean(
    column.customRender ||
    column.slots ||
    (column as { slotName?: string }).slotName,
  );
}

const normalizedColumns = computed(() => {
  const source = (props.columns ?? []) as AnyColumn[];
  return source.map((column) => {
    const next: AnyColumn = { ...column };
    if (isActionsColumn(next)) {
      if (next.fixed === undefined) {
        next.fixed = 'right';
      }
      return next;
    }
    if (next.ellipsis === undefined && !hasCustomCell(next)) {
      next.ellipsis = true;
    }
    return next;
  });
});

const resolvedScroll = computed(() => {
  if (props.scroll !== undefined) {
    return props.scroll;
  }
  const widths = normalizedColumns.value.map((column) => {
    const width = column.width;
    if (typeof width === 'number') {
      return width;
    }
    if (typeof width === 'string') {
      const parsed = Number.parseFloat(width);
      return Number.isFinite(parsed) ? parsed : 160;
    }
    return 160;
  });
  const total = widths.reduce((sum, width) => sum + width, 0);
  return { x: Math.max(total, props.minScrollX) };
});
</script>

<template>
  <Table
    v-bind="attrs"
    :columns="normalizedColumns"
    :scroll="resolvedScroll"
    :size="size"
  >
    <template v-for="(_, name) in slots" :key="name" #[name]="slotData">
      <slot :name="name" v-bind="slotData || {}"></slot>
    </template>
  </Table>
</template>
