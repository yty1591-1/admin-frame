<script lang="ts" setup>
import { onMounted, ref } from 'vue';

import { Select } from 'ant-design-vue';

import { listRolesApi } from '#/api';

const props = withDefaults(
  defineProps<{
    modelValue?: string[];
    placeholder?: string;
  }>(),
  { modelValue: () => [], placeholder: '请选择角色' },
);

const emit = defineEmits<{
  'update:modelValue': [string[]];
}>();

const loading = ref(false);
const options = ref<{ label: string; value: string }[]>([]);

async function load() {
  loading.value = true;
  try {
    const result = await listRolesApi({ pageSize: 100, status: 'active' });
    options.value = result.items.map((role) => ({
      label: `${role.name}（${role.code}）`,
      value: role.id,
    }));
  } finally {
    loading.value = false;
  }
}

onMounted(load);
</script>

<template>
  <Select
    :loading="loading"
    :options="options"
    :placeholder="placeholder"
    :value="props.modelValue"
    class="w-full"
    mode="multiple"
    @update:value="
      (value) => emit('update:modelValue', (value as string[]) ?? [])
    "
  />
</template>
