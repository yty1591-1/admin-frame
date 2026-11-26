import type { Locale } from 'ant-design-vue/es/locale';

import type { Ref } from 'vue';

import { computed, defineComponent, h } from 'vue';
import { RouterView } from 'vue-router';

import { useAntdDesignTokens } from '@vben/hooks';
import { preferences, usePreferences } from '@vben/preferences';

import { App as AntApp, ConfigProvider, theme } from 'ant-design-vue';

export interface AppRootOptions {
  /** 应用特有的 antd 语言包（由 app 的 locales 提供）。 */
  antdLocale: Ref<Locale>;
}

/**
 * 创建通用根组件：ConfigProvider（主题/语言）→ AntApp → RouterView。
 */
function createAppRoot({ antdLocale }: AppRootOptions) {
  return defineComponent({
    name: 'App',
    setup() {
      const { isDark } = usePreferences();
      const { tokens } = useAntdDesignTokens();

      const tokenTheme = computed(() => {
        const algorithm = isDark.value
          ? [theme.darkAlgorithm]
          : [theme.defaultAlgorithm];

        // antd 紧凑模式算法
        if (preferences.app.compact) {
          algorithm.push(theme.compactAlgorithm);
        }

        return {
          algorithm,
          token: tokens,
        };
      });

      return () =>
        h(
          ConfigProvider,
          { locale: antdLocale.value, theme: tokenTheme.value },
          () => h(AntApp, null, () => h(RouterView)),
        );
    },
  });
}

export { createAppRoot };
