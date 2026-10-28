<script lang="ts" setup>
import type { CheckboxChangeEvent } from 'ant-design-vue/es/checkbox/interface';

import type { Permission } from '#/api';

import { computed, onMounted, ref } from 'vue';

import { Checkbox, Empty, Input, Spin, Tag } from 'ant-design-vue';

import { listAllPermissionsApi } from '#/api';

const props = withDefaults(
  defineProps<{
    modelValue?: string[];
  }>(),
  { modelValue: () => [] },
);

const emit = defineEmits<{
  'update:modelValue': [string[]];
}>();

const loading = ref(false);
const keyword = ref('');
const permissions = ref<Permission[]>([]);
const selected = computed(() => new Set(props.modelValue));

const groups = computed(() => {
  const word = keyword.value.trim().toLowerCase();
  const visible = word
    ? permissions.value.filter(
        (item) => item.code.toLowerCase().includes(word) || item.name.toLowerCase().includes(word),
      )
    : permissions.value;
  const map = new Map<string, Permission[]>();
  for (const item of visible) {
    const list = map.get(item.resource) ?? [];
    list.push(item);
    map.set(item.resource, list);
  }
  return [...map.entries()]
    .map(([resource, items]) => ({
      items: items.toSorted((a, b) => a.sort - b.sort || a.code.localeCompare(b.code)),
      resource,
    }))
    .toSorted((a, b) => a.resource.localeCompare(b.resource));
});

onMounted(async () => {
  loading.value = true;
  try {
    permissions.value = await listAllPermissionsApi();
  } finally {
    loading.value = false;
  }
});

function update(next: string[]) {
  emit('update:modelValue', [...new Set(next)].toSorted());
}

function toggle(code: string, event: CheckboxChangeEvent) {
  const checked = event.target.checked;
  const next = new Set(props.modelValue);
  if (checked) {
    next.add(code);
  } else {
    next.delete(code);
  }
  update([...next]);
}

function groupState(items: Permission[]) {
  const count = items.filter((item) => selected.value.has(item.code)).length;
  return {
    all: count > 0 && count === items.length,
    some: count > 0 && count < items.length,
  };
}

function toggleGroup(items: Permission[], event: CheckboxChangeEvent) {
  const checked = event.target.checked;
  const next = new Set(props.modelValue);
  for (const item of items) {
    if (checked) {
      next.add(item.code);
    } else {
      next.delete(item.code);
    }
  }
  update([...next]);
}
</script>

<template>
  <Spin :spinning="loading">
    <Input v-model:value="keyword" allow-clear class="mb-3" placeholder="搜索权限码或名称" />
    <div class="max-h-[52vh] space-y-3 overflow-auto pr-1">
      <div
        v-for="group in groups"
        :key="group.resource"
        class="border-border rounded-lg border p-3"
      >
        <div class="mb-2 flex items-center justify-between gap-2">
          <div class="flex items-center gap-2">
            <Checkbox
              :checked="groupState(group.items).all"
              :indeterminate="groupState(group.items).some"
              @change="toggleGroup(group.items, $event)"
            />
            <span class="text-foreground text-sm font-medium">
              {{ group.resource }}
            </span>
            <span class="text-muted-foreground text-xs"> {{ group.items.length }} 项 </span>
          </div>
        </div>
        <div class="grid grid-cols-2 gap-x-3 gap-y-2">
          <Checkbox
            v-for="item in group.items"
            :key="item.code"
            :checked="selected.has(item.code)"
            @change="toggle(item.code, $event)"
          >
            <span class="text-foreground text-sm">{{ item.name }}</span>
            <span class="text-muted-foreground ml-1 font-mono text-xs">
              {{ item.code }}
            </span>
            <Tag v-if="item.status === 'deprecated'" class="ml-1" color="default"> 已弃用 </Tag>
          </Checkbox>
        </div>
      </div>
      <Empty v-if="groups.length === 0 && !loading" description="暂无权限点" />
    </div>
  </Spin>
</template>
