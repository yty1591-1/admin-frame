import type { App, Component } from 'vue';
import type { Router } from 'vue-router';

import { createApp, watchEffect } from 'vue';

import { registerAccessDirective } from '@vben/access';
import { registerLoadingDirective } from '@vben/common-ui/es/loading';
import { $t } from '@vben/locales';
import { preferences } from '@vben/preferences';
import { initStores } from '@vben/stores';
import '@vben/styles';
import '@vben/styles/antd';

import { useTitle } from '@vueuse/core';

import { initComponentAdapter } from './adapter/component';
import { initSetupVbenForm } from './adapter/form';

export interface AppKernelBootstrapOptions {
  /** 应用 namespace，用于偏好/存储 key 前缀。 */
  namespace: string;
  /** 应用根组件，通常由 createAppRoot 生成。 */
  rootComponent: Component;
  /** 应用自己的 vue-router 实例。 */
  router: Router;
  /** 应用 i18n 初始化（语言包由 app 提供）。 */
  setupI18n: (app: App) => Promise<unknown> | unknown;
  /** 覆盖默认适配器初始化；不传则使用内核内置适配器。 */
  initAdapters?: () => Promise<unknown> | unknown;
}

async function initDefaultAdapters(): Promise<void> {
  await initComponentAdapter();
  await initSetupVbenForm();
}

/**
 * 通用启动流程：适配器 → 根应用 → 进度指令 → i18n → stores → 权限指令 →
 * tippy → router → motion → 动态标题 → mount。
 */
async function bootstrapApp(options: AppKernelBootstrapOptions): Promise<void> {
  const { namespace, rootComponent, router, setupI18n, initAdapters } = options;

  await (initAdapters ? initAdapters() : initDefaultAdapters());

  const app = createApp(rootComponent);

  registerLoadingDirective(app, {
    loading: 'loading',
    spinning: 'spinning',
  });

  await setupI18n(app);
  await initStores(app, { namespace });
  registerAccessDirective(app);

  const { initTippy } = await import('@vben/common-ui/es/tippy');
  initTippy(app);

  app.use(router);

  const { MotionPlugin } = await import('@vben/plugins/motion');
  app.use(MotionPlugin);

  watchEffect(() => {
    if (preferences.app.dynamicTitle) {
      const routeTitle = router.currentRoute.value.meta?.title;
      const pageTitle =
        (routeTitle ? `${$t(routeTitle)} - ` : '') + preferences.app.name;
      useTitle(pageTitle);
    }
  });

  app.mount('#app');
}

export { bootstrapApp };
