<script lang="ts" setup>
import type { SchemaField } from '#/api/configcenter/install';

import { Input, Tag } from 'ant-design-vue';

defineProps<{
  draft: Record<string, Record<string, string>>;
  fields: SchemaField[];
}>();

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

function onInput(event: Event, namespace: string, key: string) {
  const target = event.target as HTMLInputElement | null;
  emit('setValue', namespace, key, target?.value ?? '');
}

function secretMeta(field: SchemaField): string {
  const rule = field.secret;
  if (!rule) {
    return '';
  }
  const parts = [
    `算法 ${rule.algorithm || '—'}`,
    `编码 ${rule.encoding || '—'}`,
    `长度 ${rule.min_len}–${rule.max_len}`,
  ];
  if (rule.prefix) {
    parts.push(`前缀 ${rule.prefix}`);
  }
  return parts.join(' · ');
}
</script>

<template>
  <div v-if="fields.length === 0" class="text-muted-foreground text-sm">
    <slot name="empty"></slot>
  </div>
  <div v-else class="space-y-2.5">
    <article
      v-for="field in fields"
      :key="`${field.namespace}/${field.key}`"
      class="border-border space-y-2.5 rounded-lg border bg-background p-3.5"
    >
      <header class="flex items-start gap-3">
        <div
          class="flex size-8 shrink-0 items-center justify-center rounded-md text-xs font-semibold"
          :class="
            field.secret?.auto_generate
              ? 'bg-success/15 text-success'
              : 'bg-primary/10 text-primary'
          "
        >
          {{ field.secret?.auto_generate ? 'A' : 'S' }}
        </div>
        <div class="min-w-0 flex-1 space-y-1">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="text-foreground text-sm font-medium">
              {{ field.label }}
            </span>
            <Tag color="purple">敏感</Tag>
            <Tag v-if="field.secret?.auto_generate" color="green">
              自动生成
            </Tag>
            <Tag v-else color="orange">可填写</Tag>
            <Tag v-if="field.blocking" color="volcano">阻塞启动</Tag>
          </div>
          <code class="text-muted-foreground/80 block font-mono text-[11px]">
            {{ field.key }}
          </code>
        </div>
      </header>

      <p v-if="field.secret" class="text-muted-foreground text-xs">
        {{ secretMeta(field) }}
      </p>
      <p
        v-if="field.description"
        class="text-muted-foreground text-xs leading-relaxed"
      >
        {{ field.description }}
      </p>

      <div
        v-if="field.secret?.auto_generate"
        class="bg-success/10 text-success flex items-center gap-2 rounded-md px-2.5 py-2 text-xs"
      >
        安装时由服务端生成并写入密钥子域，本页不回显明文
      </div>
      <Input
        v-else-if="field.secret && !field.secret.auto_generate"
        class="w-full max-w-md"
        type="password"
        autocomplete="off"
        :value="fieldValue(draft, field.namespace, field.key)"
        :placeholder="`填写 ${field.key}`"
        @input="onInput($event as Event, field.namespace, field.key)"
      />
    </article>
  </div>
</template>
