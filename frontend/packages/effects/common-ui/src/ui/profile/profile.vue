<script setup lang="ts">
import type { Props } from './types';

import { computed } from 'vue';

import {
  Badge,
  Tabs,
  TabsList,
  TabsTrigger,
  VbenAvatar,
} from '@vben-core/shadcn-ui';

import { Page } from '../../components';

defineOptions({
  name: 'ProfileUI',
});

const props = withDefaults(defineProps<Props>(), {
  title: '',
  tabs: () => [],
  statusLabel: '',
});

const tabsValue = defineModel<string | undefined>('modelValue', {
  default: undefined,
});

/**
 * 头像兜底：不引用 `preferences.app.defaultAvatar`（远端 unpkg 资源），
 * 没有头像时交给 `VbenAvatar` 用昵称首字母渲染，离线与内网环境都不会出现破图。
 */
const avatarSrc = computed(() => props.userInfo?.avatar ?? '');

/** 昵称优先，未设置时回退登录名，避免身份卡出现空行。 */
const displayName = computed(() => {
  const { realName, username } = props.userInfo ?? {};
  return realName || username || '';
});

const loginName = computed(() => props.userInfo?.username ?? '');

/** 昵称与登录名相同时不重复第二行，否则会被读成两个账号。 */
const showLoginName = computed(
  () => !!loginName.value && loginName.value !== displayName.value,
);
</script>
<template>
  <!--
    不使用 Page 的页头：面包屑已经标明「个人中心」，再加一条只有标题的
    分隔栏会白占 70px 高度，并让下方出现「栏 + 卡片」双层边框。
    标题保留在 sr-only 中，读屏仍能识别当前页面。
  -->
  <Page auto-content-height content-class="bg-muted/40 p-0">
    <h1 class="sr-only">{{ title }}</h1>

    <div
      class="mx-auto flex w-full max-w-[1320px] flex-col gap-4 p-4 sm:p-6 xl:flex-row xl:gap-6 xl:p-8"
    >
      <!--
        双栏断点放在 xl：lg（1024）时应用侧边栏仍展开，扣掉 224px 后再切
        288px 的身份栏，内容区只剩 450px 左右，表单与三列只读区都会被挤窄。
      -->
      <aside class="w-full flex-none xl:w-72">
        <div class="flex flex-col gap-4 xl:sticky xl:top-0">
          <!-- 身份卡：窄屏横向一行，桌面端叠在渐变头部上并居中 -->
          <div class="overflow-hidden rounded-2xl border bg-card">
            <div
              class="from-primary/25 via-primary/10 hidden h-20 bg-linear-to-br to-transparent xl:block"
            ></div>
            <div
              class="flex items-center gap-4 p-4 xl:flex-col xl:gap-2 xl:px-5 xl:pb-6 xl:text-center"
            >
              <!-- 用卡片色内边距模拟描边：直接给 VbenAvatar 加 ring 会被内层容器裁掉。 -->
              <div class="bg-card rounded-full p-1 xl:-mt-15">
                <VbenAvatar
                  :alt="displayName"
                  :src="avatarSrc"
                  class="size-14 rounded-full xl:size-20"
                />
              </div>
              <div class="min-w-0 flex-1 xl:w-full xl:flex-none">
                <div
                  class="flex min-w-0 items-center gap-2 xl:flex-col xl:gap-1"
                >
                  <p class="truncate text-base font-semibold">
                    {{ displayName || '-' }}
                  </p>
                  <Badge
                    v-if="statusLabel"
                    class="shrink-0 font-normal"
                    variant="secondary"
                  >
                    {{ statusLabel }}
                  </Badge>
                </div>
                <p
                  v-if="showLoginName"
                  class="text-muted-foreground mt-0.5 truncate text-sm"
                >
                  {{ loginName }}
                </p>
              </div>
            </div>
          </div>

          <!-- 导航：同一 v-model 的两个实例，纯 CSS 断点切换，不引 JS 监听 -->
          <nav class="rounded-2xl border bg-card p-1.5">
            <Tabs v-model="tabsValue" class="xl:hidden">
              <TabsList
                class="flex h-auto w-full justify-start gap-1 overflow-x-auto bg-transparent p-0"
              >
                <TabsTrigger
                  v-for="tab in tabs"
                  :key="tab.value"
                  :value="tab.value"
                  class="text-muted-foreground hover:bg-accent hover:text-foreground dark:data-[state=active]:border-transparent dark:data-[state=active]:bg-primary/20 dark:data-[state=active]:text-foreground data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-9 shrink-0 gap-2 rounded-lg px-3 data-[state=active]:shadow-none"
                >
                  <component :is="tab.icon" v-if="tab.icon" class="size-4" />
                  {{ tab.label }}
                </TabsTrigger>
              </TabsList>
            </Tabs>

            <Tabs
              v-model="tabsValue"
              class="hidden xl:block"
              orientation="vertical"
            >
              <TabsList
                class="flex h-auto w-full flex-col items-stretch gap-1 bg-transparent p-0"
              >
                <TabsTrigger
                  v-for="tab in tabs"
                  :key="tab.value"
                  :value="tab.value"
                  class="text-muted-foreground hover:bg-accent hover:text-foreground dark:data-[state=active]:border-transparent dark:data-[state=active]:bg-primary/20 dark:data-[state=active]:text-foreground data-[state=active]:bg-primary/10 data-[state=active]:text-primary h-10 flex-none justify-start gap-2.5 rounded-lg px-3 data-[state=active]:shadow-none"
                >
                  <component :is="tab.icon" v-if="tab.icon" class="size-4" />
                  {{ tab.label }}
                </TabsTrigger>
              </TabsList>
            </Tabs>
          </nav>
        </div>
      </aside>

      <section class="min-w-0 flex-1">
        <div class="rounded-2xl border bg-card p-4 sm:p-6 xl:p-8">
          <slot name="content"></slot>
        </div>
      </section>
    </div>
  </Page>
</template>
