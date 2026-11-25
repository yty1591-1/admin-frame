<script lang="ts" setup>
import type { FieldSection } from '../schema';

import { Tag } from 'ant-design-vue';

import FieldControl from './FieldControl.vue';

withDefaults(
  defineProps<{
    compact?: boolean;
    /** 某域无字段时的提示（如 repair 空域）。 */
    emptyMessage?: string;
    draft: Record<string, Record<string, string>>;
    sections: FieldSection[];
  }>(),
  {
    compact: false,
    emptyMessage: '',
  },
);

const emit = defineEmits<{
  setValue: [namespace: string, key: string, value: string];
}>();

function fieldValue(
  draft: Record<string, Record<string, string>>,
  namespace: string,
  key: string,
): string {
  return draft[namespace]?.[key] ?? '';
}

function isFilled(
  draft: Record<string, Record<string, string>>,
  namespace: string,
  key: string,
): boolean {
  return (draft[namespace]?.[key] ?? '').trim() !== '';
}
</script>

<template>
  <div v-if="sections.length === 0 && emptyMessage" class="text-muted-foreground text-sm">
    {{ emptyMessage }}
  </div>
  <div v-else class="space-y-4">
    <section
      v-for="section in sections"
      :key="section.namespace"
      class="border-border bg-card overflow-hidden rounded-lg border"
    >
      <header
        class="border-border bg-muted/50 flex flex-wrap items-center gap-2 border-b px-4 py-3"
      >
        <h3 class="text-foreground text-[15px] font-semibold">
          {{ section.title }}
        </h3>
        <Tag v-if="section.blockingCount > 0" color="red"> 阻塞 {{ section.blockingCount }} </Tag>
        <code class="text-muted-foreground/70 font-mono text-[11px]">
          {{ section.namespace }}
        </code>
      </header>

      <div class="space-y-5 px-4 py-3.5">
        <div v-for="group in section.groups" :key="group.id" class="space-y-1">
          <h4 class="text-muted-foreground mb-1.5 text-[13px] font-medium">
            {{ group.title }}
          </h4>
          <FieldControl
            v-for="field in group.fields"
            :key="field.key"
            :compact="compact"
            :field="field"
            :filled="isFilled(draft, field.namespace, field.key)"
            :model-value="fieldValue(draft, field.namespace, field.key)"
            @update:model-value="(value) => emit('setValue', field.namespace, field.key, value)"
          />
        </div>
      </div>
    </section>
  </div>
</template>
