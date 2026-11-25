<script lang="ts" setup>
/**
 * 本步指引：编号清单，降低向导认知负担（设计稿 GuidePanel）。
 */
defineProps<{
  items: string[];
  title: string;
}>();
</script>

<template>
  <div class="bg-muted space-y-2.5 rounded-lg p-3.5">
    <div class="text-primary flex items-center gap-1.5 text-xs font-semibold">
      <span
        class="bg-background text-primary inline-flex size-[18px] items-center justify-center rounded-full text-[11px] font-semibold"
        aria-hidden="true"
      >
        i
      </span>
      {{ title }}
    </div>
    <ol class="space-y-2">
      <li v-for="(item, index) in items" :key="index" class="flex items-start gap-2">
        <span
          class="bg-background text-primary mt-0.5 inline-flex size-[18px] shrink-0 items-center justify-center rounded-full text-[11px] font-semibold"
        >
          {{ index + 1 }}
        </span>
        <span class="text-muted-foreground text-xs leading-relaxed">
          {{ item }}
        </span>
      </li>
    </ol>
  </div>
</template>
