<script lang="ts" setup>
/**
 * 桌面：左栏进度 + 运行上下文。
 * 移动：顶栏品牌 + 横向步进 + 可折叠上下文（单栏主导）。
 */
export type RailStep = {
  hint: string;
  key: string;
  title: string;
};

export type RailMetaRow = {
  tone?: 'danger' | 'muted' | 'primary' | 'success' | 'warning';
  label: string;
  value: string;
};

const props = defineProps<{
  contextTitle: string;
  current: number;
  lastError?: string;
  lastErrorPrefix?: string;
  metaRows: RailMetaRow[];
  steps: RailStep[];
  subtitle: string;
  title: string;
}>();

const metaOpen = defineModel<boolean>('metaOpen', { default: false });

const currentStep = () => props.steps[props.current];
</script>

<template>
  <!-- Desktop rail -->
  <aside
    class="border-border bg-background hidden h-full w-[280px] shrink-0 flex-col justify-between border-r px-4 py-6 lg:flex"
  >
    <div class="space-y-6">
      <div class="flex items-center gap-2.5 px-1">
        <div
          class="bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-lg text-sm font-semibold"
        >
          AF
        </div>
        <div class="min-w-0">
          <div class="text-foreground truncate text-sm font-semibold">
            {{ title }}
          </div>
          <div class="text-muted-foreground truncate text-[11px]">
            {{ subtitle }}
          </div>
        </div>
      </div>

      <nav class="space-y-1" aria-label="install steps">
        <div
          v-for="(item, index) in steps"
          :key="item.key"
          class="flex items-start gap-2.5 rounded-lg px-3 py-2.5"
          :class="
            index === current
              ? 'bg-primary/10 ring-primary/30 ring-1'
              : 'hover:bg-muted/60'
          "
        >
          <span
            class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold"
            :class="
              index < current
                ? 'bg-success text-success-foreground'
                : index === current
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground'
            "
          >
            <template v-if="index < current">✓</template>
            <template v-else>{{ index + 1 }}</template>
          </span>
          <div class="min-w-0">
            <div
              class="truncate text-[13px]"
              :class="
                index === current
                  ? 'text-foreground font-semibold'
                  : index < current
                    ? 'text-foreground font-medium'
                    : 'text-muted-foreground font-medium'
              "
            >
              {{ item.title }}
            </div>
            <div
              class="truncate text-[11px]"
              :class="
                index === current ? 'text-primary' : 'text-muted-foreground/80'
              "
            >
              {{ item.hint }}
            </div>
          </div>
        </div>
      </nav>
    </div>

    <div class="space-y-2 px-1">
      <div class="text-muted-foreground text-[11px] font-medium">
        {{ contextTitle }}
      </div>
      <div class="space-y-1.5">
        <div
          v-for="row in metaRows"
          :key="row.label"
          class="bg-muted flex items-center justify-between gap-2 rounded px-2 py-1.5 text-xs"
        >
          <span class="text-muted-foreground shrink-0">{{ row.label }}</span>
          <span
            class="truncate font-medium"
            :class="{
              'text-destructive': row.tone === 'danger',
              'text-primary': row.tone === 'primary',
              'text-success': row.tone === 'success',
              'text-warning': row.tone === 'warning',
              'text-foreground': !row.tone || row.tone === 'muted',
            }"
          >
            {{ row.value || '—' }}
          </span>
        </div>
      </div>
      <p v-if="lastError" class="text-destructive text-[11px] leading-relaxed">
        {{ lastErrorPrefix }}{{ lastError }}
      </p>
    </div>
  </aside>

  <!-- Mobile top bar -->
  <header
    class="border-border bg-background sticky top-0 z-20 shrink-0 border-b lg:hidden"
  >
    <div class="flex items-center justify-between gap-3 px-4 py-3">
      <div class="flex min-w-0 items-center gap-2.5">
        <div
          class="bg-primary text-primary-foreground flex size-8 shrink-0 items-center justify-center rounded-lg text-sm font-semibold"
        >
          AF
        </div>
        <div class="min-w-0">
          <div class="text-foreground truncate text-sm font-semibold">
            {{ title }}
          </div>
          <div class="text-muted-foreground truncate text-[11px]">
            {{ currentStep()?.title }}
          </div>
        </div>
      </div>
      <button
        type="button"
        class="border-border text-muted-foreground hover:bg-muted inline-flex h-8 items-center rounded-md border px-2.5 text-xs font-medium"
        :aria-expanded="metaOpen"
        @click="metaOpen = !metaOpen"
      >
        {{ contextTitle }}
        <span class="ml-1">{{ metaOpen ? '▴' : '▾' }}</span>
      </button>
    </div>

    <div
      v-if="metaOpen"
      class="border-border bg-muted/40 space-y-1.5 border-t px-4 py-3"
    >
      <div
        v-for="row in metaRows"
        :key="`m-${row.label}`"
        class="bg-background flex items-center justify-between gap-2 rounded px-2 py-1.5 text-xs"
      >
        <span class="text-muted-foreground">{{ row.label }}</span>
        <span
          class="truncate font-medium"
          :class="{
            'text-destructive': row.tone === 'danger',
            'text-primary': row.tone === 'primary',
            'text-success': row.tone === 'success',
            'text-warning': row.tone === 'warning',
            'text-foreground': !row.tone || row.tone === 'muted',
          }"
        >
          {{ row.value || '—' }}
        </span>
      </div>
      <p v-if="lastError" class="text-destructive text-[11px] leading-relaxed">
        {{ lastErrorPrefix }}{{ lastError }}
      </p>
    </div>

    <nav
      class="flex gap-1 overflow-x-auto px-3 pb-3 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      aria-label="install steps"
    >
      <div
        v-for="(item, index) in steps"
        :key="`m-step-${item.key}`"
        class="flex min-w-[4.5rem] flex-1 flex-col items-center gap-1.5 px-1"
      >
        <div class="flex w-full items-center">
          <div
            class="h-0.5 flex-1"
            :class="
              index === 0
                ? 'bg-transparent'
                : index <= current
                  ? 'bg-primary'
                  : 'bg-border'
            "
          ></div>
          <span
            class="flex size-7 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold"
            :class="
              index < current
                ? 'bg-success text-success-foreground'
                : index === current
                  ? 'bg-primary text-primary-foreground'
                  : 'bg-muted text-muted-foreground'
            "
          >
            <template v-if="index < current">✓</template>
            <template v-else>{{ index + 1 }}</template>
          </span>
          <div
            class="h-0.5 flex-1"
            :class="
              index === steps.length - 1
                ? 'bg-transparent'
                : index < current
                  ? 'bg-primary'
                  : 'bg-border'
            "
          ></div>
        </div>
        <span
          class="max-w-full truncate text-center text-[10px] leading-tight"
          :class="
            index === current
              ? 'text-primary font-semibold'
              : 'text-muted-foreground'
          "
        >
          {{ item.title }}
        </span>
      </div>
    </nav>
  </header>
</template>
