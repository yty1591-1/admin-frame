<script lang="ts" setup>
import type { DataScope } from '#/api';

import { computed } from 'vue';

import { Button, Input, Select } from 'ant-design-vue';

const props = withDefaults(
  defineProps<{
    modelValue?: DataScope[];
  }>(),
  { modelValue: () => [] },
);

const emit = defineEmits<{
  'update:modelValue': [DataScope[]];
}>();

const rows = computed(() => props.modelValue ?? []);

const scopeOptions = [
  { label: '全部数据', value: 'all' },
  { label: '指定商户', value: 'merchant_set' },
];

function updateRow(index: number, patch: Partial<DataScope>) {
  const next = rows.value.map((row, current) =>
    current === index ? { ...row, ...patch } : { ...row },
  );
  emit('update:modelValue', next);
}

function addRow() {
  emit('update:modelValue', [...rows.value, { resource: '*', scope_type: 'all' }]);
}

function removeRow(index: number) {
  emit(
    'update:modelValue',
    rows.value.filter((_, current) => current !== index),
  );
}
</script>

<template>
  <div class="space-y-2">
    <div v-for="(row, index) in rows" :key="index" class="grid grid-cols-12 items-center gap-2">
      <Input
        :value="row.resource"
        class="col-span-3"
        placeholder="资源段，如 iam 或 *"
        @update:value="(value) => updateRow(index, { resource: value })"
      />
      <Select
        :options="scopeOptions"
        :value="row.scope_type"
        class="col-span-3"
        @update:value="
          (value) => updateRow(index, { scope_type: value as DataScope['scope_type'] })
        "
      />
      <Select
        v-if="row.scope_type === 'merchant_set'"
        :value="row.merchant_ids ?? []"
        class="col-span-5"
        mode="tags"
        placeholder="输入商户 ID，回车添加，最多 100 个"
        @update:value="
          (value) =>
            updateRow(index, {
              merchant_ids: (value as string[]).slice(0, 100),
            })
        "
      />
      <div v-else class="text-muted-foreground col-span-5 text-xs">该资源允许访问全部数据</div>
      <Button danger type="text" @click="removeRow(index)">删除</Button>
    </div>
    <Button block type="dashed" @click="addRow">新增数据范围</Button>
    <p class="text-muted-foreground text-xs">
      A 端仅支持 all / merchant_set；merchant_set 需填写商户 ID（≤100）。
    </p>
  </div>
</template>
