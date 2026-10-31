<script setup lang="ts">
import type { ProfileSessionListProps, SessionItem } from './types';

import { $t } from '@vben/locales';

import { Badge, VbenButton, VbenSpinner } from '@vben-core/shadcn-ui';

const props = withDefaults(defineProps<ProfileSessionListProps>(), {
  items: () => [],
  loading: false,
  error: false,
  hasMore: false,
  currentId: '',
  revokingId: '',
  revokingAll: false,
});

const emit = defineEmits<{
  loadMore: [];
  retry: [];
  revoke: [id: string];
  revokeAll: [];
}>();

/** 当前设备判定：后端 `current` 优先，缺失时用页面传入的 `currentId` 兜底。 */
function isCurrent(item: SessionItem) {
  return (
    item.current === true || (!!props.currentId && item.id === props.currentId)
  );
}

/** 本地时区 `YYYY-MM-DD HH:mm`；非法或缺失统一显示 `-`，不输出 `Invalid Date`。 */
function formatTime(value?: null | string) {
  if (!value) {
    return '-';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '-';
  }
  const pad = (input: number) => String(input).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(
    date.getDate(),
  )} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
</script>
<template>
  <div class="space-y-5">
    <div
      class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"
    >
      <div class="space-y-1">
        <h3 class="text-base font-semibold">{{ $t('profile.deviceList') }}</h3>
        <p class="text-muted-foreground text-sm">
          {{ $t('profile.deviceListHint') }}
        </p>
      </div>
      <VbenButton
        v-if="items.length > 0"
        :disabled="revokingAll"
        :loading="revokingAll"
        class="h-9 shrink-0 px-4"
        size="sm"
        variant="outline"
        @click="emit('revokeAll')"
      >
        {{ $t('profile.deviceRevokeAll') }}
      </VbenButton>
    </div>

    <!-- 错误态优先于空态：加载失败不能被误读成「没有登录设备」。 -->
    <div
      v-if="error"
      class="flex flex-col items-start gap-3 rounded-xl border border-dashed p-5 sm:flex-row sm:items-center sm:justify-between"
    >
      <span class="text-muted-foreground text-sm">
        {{ $t('profile.sessionLoadFailed') }}
      </span>
      <VbenButton
        class="h-9 px-4"
        size="sm"
        variant="outline"
        @click="emit('retry')"
      >
        {{ $t('profile.sessionRetry') }}
      </VbenButton>
    </div>

    <div
      v-else-if="loading && items.length === 0"
      class="divide-y divide-border overflow-hidden rounded-xl border border-border"
    >
      <div
        v-for="index in 3"
        :key="index"
        class="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between"
      >
        <div class="space-y-2">
          <div class="bg-muted h-4 w-24 animate-pulse rounded"></div>
          <div class="bg-muted h-3 w-56 animate-pulse rounded"></div>
        </div>
        <VbenSpinner class="size-4" />
      </div>
    </div>

    <div
      v-else-if="items.length === 0"
      class="text-muted-foreground rounded-xl border border-dashed p-10 text-center text-sm"
    >
      {{ $t('profile.deviceEmpty') }}
    </div>

    <template v-else>
      <div
        class="divide-y divide-border overflow-hidden rounded-xl border border-border"
      >
        <div
          v-for="item in items"
          :key="item.id"
          class="hover:bg-accent/40 flex flex-col gap-3 p-4 transition-colors sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="min-w-0 flex-1 space-y-2">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium">
                {{ $t('profile.deviceUnknown') }}
              </span>
              <!-- 接口不下发 UA，用会话短 ID 区分多台设备，避免多行完全同名。 -->
              <span class="text-muted-foreground font-mono text-xs">
                #{{ item.id.slice(0, 8) }}
              </span>
              <Badge v-if="isCurrent(item)" variant="secondary">
                {{ $t('profile.deviceCurrent') }}
              </Badge>
              <span v-if="item.revokedAt" class="text-muted-foreground text-xs">
                {{ $t('profile.deviceRevoked') }}
              </span>
            </div>
            <!-- 窄屏单列，宽屏三列：时间不折行、不挤成一行省略号。 -->
            <dl
              class="text-muted-foreground grid grid-cols-1 gap-1 text-xs sm:grid-cols-3 sm:gap-4"
            >
              <div class="flex gap-1">
                <dt>{{ $t('profile.deviceCreatedAt') }}</dt>
                <dd class="break-all">{{ formatTime(item.createdAt) }}</dd>
              </div>
              <div class="flex gap-1">
                <dt>{{ $t('profile.deviceLastUsedAt') }}</dt>
                <dd class="break-all">{{ formatTime(item.lastUsedAt) }}</dd>
              </div>
              <div class="flex gap-1">
                <dt>{{ $t('profile.deviceExpiresAt') }}</dt>
                <dd class="break-all">{{ formatTime(item.expiresAt) }}</dd>
              </div>
            </dl>
          </div>
          <VbenButton
            :disabled="revokingAll"
            :loading="revokingId === item.id"
            class="h-9 w-full shrink-0 px-4 sm:w-auto"
            size="sm"
            variant="outline"
            @click="emit('revoke', item.id)"
          >
            {{ $t('profile.deviceRevoke') }}
          </VbenButton>
        </div>
      </div>

      <div v-if="hasMore" class="flex justify-center">
        <VbenButton
          :loading="loading"
          class="h-9 px-4"
          size="sm"
          variant="ghost"
          @click="emit('loadMore')"
        >
          {{ $t('profile.deviceLoadMore') }}
        </VbenButton>
      </div>
    </template>
  </div>
</template>
