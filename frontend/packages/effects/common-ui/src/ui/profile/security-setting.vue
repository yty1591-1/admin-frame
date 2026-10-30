<script setup lang="ts">
import type { BadgeVariants } from '@vben-core/shadcn-ui';

import type { SecuritySchemaItem, SecuritySettingProps } from './types';

import { $t } from '@vben/locales';

import { Badge, VbenButton, VbenSpinner } from '@vben-core/shadcn-ui';

withDefaults(defineProps<SecuritySettingProps>(), {
  formSchema: () => [],
  loading: false,
});

const emit = defineEmits<{
  /** 点击某行的操作入口；由页面决定跳转或弹出对应流程。 */
  action: [fieldName: string];
}>();

/**
 * 状态到徽标样式与文案的映射。
 *
 * `unavailable` 不表示「未绑定」：它表示系统暂无该能力或状态不可获取，
 * 文案统一为中性占位，避免把未知状态说成安全风险。
 */
const STATUS_META = {
  bound: { label: 'profile.securityStatusBound', variant: 'secondary' },
  unbound: { label: 'profile.securityStatusUnbound', variant: 'outline' },
  unavailable: {
    label: 'profile.securityStatusUnavailable',
    variant: 'outline',
  },
} as const satisfies Record<
  SecuritySchemaItem['status'],
  { label: string; variant: BadgeVariants['variant'] }
>;

function statusMetaOf(item: SecuritySchemaItem) {
  return STATUS_META[item.status];
}
</script>
<template>
  <div class="space-y-4">
    <template v-if="loading">
      <div
        v-for="index in formSchema.length || 5"
        :key="index"
        class="flex flex-row items-center justify-between rounded-lg border p-4"
      >
        <div class="space-y-2">
          <div class="bg-muted h-4 w-24 animate-pulse rounded"></div>
          <div class="bg-muted h-3 w-56 animate-pulse rounded"></div>
        </div>
        <VbenSpinner class="size-4" />
      </div>
    </template>
    <template v-else>
      <div
        v-for="item in formSchema"
        :key="item.fieldName"
        class="flex flex-row items-center justify-between gap-4 rounded-lg border p-4"
      >
        <div class="space-y-1">
          <div class="flex flex-row items-center gap-2">
            <span class="text-base font-medium">{{ item.label }}</span>
            <Badge
              :class="
                item.status === 'unavailable' ? 'text-muted-foreground' : ''
              "
              :variant="statusMetaOf(item).variant"
            >
              {{ item.statusLabel ?? $t(statusMetaOf(item).label) }}
            </Badge>
          </div>
          <p class="text-muted-foreground text-sm">
            {{ item.description }}
          </p>
        </div>
        <VbenButton
          v-if="item.actionLabel"
          :disabled="item.actionDisabled"
          size="sm"
          variant="outline"
          @click="emit('action', item.fieldName)"
        >
          {{ item.actionLabel }}
        </VbenButton>
      </div>
    </template>
  </div>
</template>
