<script lang="ts" setup>
import type { SchemaField } from '#/api/configcenter/install';

import { computed } from 'vue';

import { Input, InputNumber, Select, Switch, Tag } from 'ant-design-vue';

import { defaultBadge } from '../schema';

const props = withDefaults(
  defineProps<{
    /** 紧凑行：标签与控件同一行（业务域）。 */
    compact?: boolean;
    field: SchemaField;
    filled?: boolean;
    modelValue: string;
  }>(),
  {
    compact: false,
    filled: false,
  },
);

const emit = defineEmits<{
  'update:modelValue': [value: string];
}>();

const badge = computed(() => defaultBadge(props.field));

const selectOptions = computed(() =>
  (props.field.enum ?? []).map((item) => ({ label: item, value: item })),
);

function setValue(value: boolean | number | string) {
  if (typeof value === 'boolean') {
    emit('update:modelValue', String(value));
    return;
  }
  emit('update:modelValue', String(value ?? ''));
}

function onInput(event: Event) {
  const target = event.target as HTMLInputElement | null;
  emit('update:modelValue', target?.value ?? '');
}
</script>

<template>
  <div
    :class="
      compact
        ? 'flex flex-col gap-2 border-border/60 border-b py-3 last:border-b-0 sm:flex-row sm:flex-wrap sm:items-center sm:gap-3 sm:py-2'
        : 'border-border/60 grid grid-cols-1 gap-3 border-b py-4 last:border-b-0 md:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)]'
    "
  >
    <div :class="compact ? 'flex min-w-0 flex-wrap items-center gap-2 sm:min-w-56' : 'space-y-1.5'">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-foreground text-sm font-medium">
          {{ field.label }}
        </span>
        <Tag v-if="field.required" color="red">必填</Tag>
        <Tag v-if="field.blocking" color="volcano">
          {{ compact ? '阻塞' : '阻塞启动' }}
        </Tag>
        <Tag v-if="!compact && field.deprecated" color="default">已废弃</Tag>
        <Tag v-if="!compact && field.hot_whitelist" color="green">可热更</Tag>
      </div>
      <p v-if="!compact && field.description" class="text-muted-foreground text-xs leading-relaxed">
        {{ field.description }}
      </p>
      <p v-if="!compact && badge" class="text-primary text-xs">
        {{ badge }}
      </p>
      <code v-if="!compact" class="text-muted-foreground/80 block font-mono text-[11px]">
        {{ field.key }}
      </code>
    </div>

    <div
      :class="
        compact
          ? 'flex w-full min-w-0 flex-1 flex-col gap-1.5 sm:flex-row sm:flex-wrap sm:items-center sm:gap-2'
          : 'space-y-1.5'
      "
    >
      <Switch
        v-if="field.widget === 'switch'"
        :checked="modelValue === 'true'"
        @change="(checked) => setValue(Boolean(checked))"
      />
      <Select
        v-else-if="field.widget === 'select' && selectOptions.length"
        :class="compact ? 'w-full sm:min-w-64 sm:w-auto' : 'w-full'"
        :options="selectOptions"
        :placeholder="field.default.source === 'none' ? '请选择' : `默认 ${field.default.value}`"
        :value="modelValue || undefined"
        @change="(value) => setValue(value as string)"
      />
      <InputNumber
        v-else-if="field.widget === 'number'"
        :class="compact ? 'w-full sm:min-w-40 sm:w-auto' : 'w-full'"
        :max="field.max_int ?? undefined"
        :min="field.min_int ?? undefined"
        :placeholder="field.default.value || '整数'"
        :value="modelValue ? Number(modelValue) : undefined"
        @change="(value) => setValue(value ?? '')"
      />
      <Input.TextArea
        v-else-if="field.widget === 'code'"
        :placeholder="field.default.value || 'JSON'"
        :rows="3"
        :value="modelValue"
        class="font-mono text-xs"
        @input="onInput"
      />
      <Input
        v-else
        :class="compact ? 'w-full sm:min-w-64 sm:flex-1' : 'w-full'"
        :placeholder="field.default.value || field.value_type"
        :type="field.widget === 'password' ? 'password' : 'text'"
        :value="modelValue"
        autocomplete="off"
        @input="onInput"
      />
      <template v-if="compact">
        <span v-if="badge" class="text-primary text-xs">{{ badge }}</span>
        <code class="text-muted-foreground/70 break-all font-mono text-[11px]">
          {{ field.key }}
        </code>
      </template>
      <p v-else class="text-muted-foreground/70 text-xs">
        {{ filled ? '已填写（覆盖默认值）' : '留空 = 使用默认值' }}
      </p>
    </div>
  </div>
</template>
