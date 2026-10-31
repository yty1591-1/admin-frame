<script setup lang="ts">
import { computed } from 'vue';

import { $t } from '#/locales';

/**
 * B 端设备会话：后端目前只在 A 端暴露 `/auth/sessions`，B 端路由尚未收敛。
 *
 * 不渲染任何可点击按钮，避免出现「点了没反应」的控件；
 * 也不复用空态，空态会被误读成「当前没有登录设备」。
 */
const unavailableText = computed(() => $t('profile.sessionUnavailableDesc'));
</script>
<template>
  <div
    class="flex flex-col items-center gap-2 rounded-xl border border-dashed px-6 py-12 text-center"
  >
    <p class="text-sm font-medium">{{ $t('profile.sessionUnavailable') }}</p>
    <p class="text-muted-foreground max-w-md text-sm">
      {{ unavailableText }}
    </p>
  </div>
</template>
