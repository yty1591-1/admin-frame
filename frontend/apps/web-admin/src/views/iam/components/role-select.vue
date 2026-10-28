<script lang="ts" setup>
import type { RoleStatus } from '#/api';

import { onMounted, ref } from 'vue';

import { Select } from 'ant-design-vue';

import { listRolesApi } from '#/api';

const props = withDefaults(
  defineProps<{
    disabled?: boolean;
    modelValue?: string;
    placeholder?: string;
    status?: RoleStatus;
  }>(),
  {
    disabled: false,
    placeholder: '请选择角色',
    status: 'active',
  },
);

const emit = defineEmits<{
  'update:modelValue': [string | undefined];
}>();

const loading = ref(false);
const options = ref<{ label: string; value: string }[]>([]);
let searchTimer: ReturnType<typeof setTimeout> | undefined;

async function load(keyword?: string) {
  loading.value = true;
  try {
    const result = await listRolesApi({
      keyword: keyword?.trim() || undefined,
      pageSize: 100,
      status: props.status,
    });
    options.value = result.items.map((role) => ({
      label: `${role.name}（${role.code}）`,
      value: role.id,
    }));
  } finally {
    loading.value = false;
  }
}

function handleSearch(value: string) {
  if (searchTimer) {
    clearTimeout(searchTimer);
  }
  searchTimer = setTimeout(() => load(value), 300);
}

onMounted(() => load());
</script>

<template>
  <Select
    :disabled="disabled"
    :filter-option="false"
    :loading="loading"
    :options="options"
    :placeholder="placeholder"
    :value="modelValue"
    allow-clear
    class="w-full"
    show-search
    @search="handleSearch"
    @update:value="(value) => emit('update:modelValue', value as string | undefined)"
  />
</template>
